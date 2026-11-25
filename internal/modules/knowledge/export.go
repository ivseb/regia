package knowledge

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// Export in Markdown: uno spazio intero o una pagina con le sue sottopagine diventano uno zip
// con una cartella per ogni pagina che ha figli, un file .md per pagina (con una breve
// intestazione: titolo, chiave, versione, stato, ordine), gli allegati in _allegati/<chiave>/
// e i collegamenti riscritti in relativi. L'import (import.go) legge lo stesso formato e
// quindi anche cartelle di Markdown scritte altrove.

// FileStore dà accesso agli allegati senza che il modulo conosca dove stanno i file.
type FileStore interface {
	// Open apre l'allegato (con i permessi dell'attore) e ne restituisce il nome.
	Open(ctx context.Context, tx pgx.Tx, actor engine.Actor, attachmentID string) (name string, r io.ReadCloser, err error)
	// Store salva il file come allegato della pagina e restituisce l'id dell'allegato.
	Store(ctx context.Context, tx pgx.Tx, actor engine.Actor, pageID, name string, r io.Reader) (attachmentID string, err error)
}

// SetFiles collega il modulo al deposito degli allegati (senza, l'export salta gli allegati
// e l'import non li carica).
func (s *Service) SetFiles(f FileStore) { s.files = f }

const (
	// ManifestName è il file con l'elenco delle pagine esportate.
	ManifestName   = "regia-export.json"
	assetsDir      = "_allegati"
	maxExportPages = 5000
)

var (
	attachmentURLRe = regexp.MustCompile(`^/api/v1/attachments/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/content(\?download=1)?$`)
	mdLinkRe        = regexp.MustCompile(`(!?)\[([^\]\n]*)\]\(([^)\s]+)\)`)
	pageKeyRe       = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,19}-[1-9][0-9]{0,17}$`)
)

// ExportOptions sceglie cosa esportare.
type ExportOptions struct {
	Root        string // chiave di una pagina: esporta lei e le sottopagine (vuoto = tutto lo spazio)
	Approved    bool   // dei documenti controllati, la versione approvata in vigore
	Attachments bool
}

type exportNode struct {
	TreeNode
	body     pageBody
	title    string
	version  int
	order    int
	dir      string // cartella che contiene il file
	file     string // percorso del file .md
	children []*exportNode
	skip     bool
}

// ExportPlan è un export pronto da scrivere.
type ExportPlan struct {
	Filename string
	Pages    int
	space    *Space
	roots    []*exportNode
	opt      ExportOptions
	s        *Service
	byKey    map[string]*exportNode
}

// PlanExport legge le pagine da esportare (rispettando i permessi dell'attore).
func (s *Service) PlanExport(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string, opt ExportOptions) (*ExportPlan, error) {
	view, err := s.Tree(ctx, tx, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	nodes := map[string]*exportNode{}
	var order []*exportNode
	for i := range view.Pages {
		n := &exportNode{TreeNode: view.Pages[i], order: i + 1}
		nodes[n.ID] = n
		order = append(order, n)
	}
	var roots []*exportNode
	for _, n := range order {
		if n.ParentID != nil {
			if p := nodes[*n.ParentID]; p != nil {
				p.children = append(p.children, n)
				continue
			}
		}
		roots = append(roots, n)
	}
	filename := strings.ToLower(spaceKey)
	if opt.Root != "" {
		var root *exportNode
		for _, n := range order {
			if strings.EqualFold(n.Key, opt.Root) {
				root = n
			}
		}
		if root == nil {
			return nil, engine.ErrNotFound
		}
		roots = []*exportNode{root}
		filename = strings.ToLower(root.Key)
	}
	plan := &ExportPlan{Filename: filename + "-" + time.Now().Format(time.DateOnly) + ".zip", space: view.Space, roots: roots, opt: opt, s: s,
		byKey: map[string]*exportNode{}}
	var ids []string
	var walk func(n *exportNode)
	walk = func(n *exportNode) {
		ids = append(ids, n.ID)
		plan.byKey[n.Key] = n
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	if len(ids) > maxExportPages {
		return nil, engine.InvalidParams("validation.knowledge.export_too_many", "root", map[string]any{"max": maxExportPages}, "troppe pagine da esportare in una volta (massimo %d): scegli una pagina e le sue sottopagine", maxExportPages)
	}
	plan.Pages = len(ids)
	if err := plan.load(ctx, tx, nodes, ids); err != nil {
		return nil, err
	}
	plan.layout()
	return plan, nil
}

func (p *ExportPlan) load(ctx context.Context, tx pgx.Tx, nodes map[string]*exportNode, ids []string) error {
	rows, err := tx.Query(ctx, `select e.id::text, e.title, e.body, k.version, k.approved_version from elements e
		join kb_pages k on k.tenant_id = e.tenant_id and k.element_id = e.id where e.id = any($1::uuid[])`, ids)
	if err != nil {
		return err
	}
	approved := map[string]int{}
	for rows.Next() {
		var id, title string
		var raw []byte
		var version int
		var av *int
		if err := rows.Scan(&id, &title, &raw, &version, &av); err != nil {
			rows.Close()
			return err
		}
		n := nodes[id]
		n.title, n.body, n.version = title, readBody(raw), version
		if av != nil && p.opt.Approved && n.Controlled {
			approved[id] = *av
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if !p.opt.Approved {
		return nil
	}
	for _, n := range nodes {
		if n.Controlled && approved[n.ID] == 0 && n.version > 0 {
			n.skip = true // nessuna versione approvata: niente da esportare
		}
	}
	for id, v := range approved {
		var title string
		var raw []byte
		if err := tx.QueryRow(ctx, `select title, body from page_versions where element_id = $1 and version = $2`, id, v).Scan(&title, &raw); err != nil {
			return err
		}
		n := nodes[id]
		n.title, n.body, n.version = title, readBody(raw), v
	}
	return nil
}

// slugify rende un titolo adatto a un nome di file: minuscolo, senza accenti, trattini.
func slugify(s string) string {
	r := strings.NewReplacer("à", "a", "á", "a", "è", "e", "é", "e", "ì", "i", "í", "i", "ò", "o", "ó", "o", "ù", "u", "ú", "u",
		"À", "a", "È", "e", "É", "e", "Ì", "i", "Ò", "o", "Ù", "u", "ç", "c", "ñ", "n", "ä", "a", "ö", "o", "ü", "u")
	s = strings.ToLower(r.Replace(s))
	var b strings.Builder
	dash := true
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	if out == "" {
		out = "pagina"
	}
	return out
}

// safeFileName tiene lettere, cifre, punto, trattino e sottolineatura.
func safeFileName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		out = "file"
	}
	if len(out) > 100 {
		ext := path.Ext(out)
		if len(ext) > 10 {
			ext = ""
		}
		out = out[:100-len(ext)] + ext
	}
	return out
}

// layout assegna a ogni pagina il suo file: una pagina con figli ha anche la cartella.
func (p *ExportPlan) layout() {
	var place func(list []*exportNode, dir string)
	place = func(list []*exportNode, dir string) {
		used := map[string]bool{}
		for _, n := range list {
			slug := slugify(n.title)
			name := slug
			for i := 2; used[name]; i++ {
				name = slug + "-" + strconv.Itoa(i)
			}
			used[name] = true
			n.dir = dir
			n.file = path.Join(dir, name+".md")
			place(n.children, path.Join(dir, name))
		}
	}
	place(p.roots, "")
}

// relPath è il percorso relativo da un file a un altro (entrambi con "/" e senza radice).
func relPath(fromFile, to string) string {
	from := strings.Split(path.Dir(fromFile), "/")
	if path.Dir(fromFile) == "." {
		from = nil
	}
	dest := strings.Split(to, "/")
	i := 0
	for i < len(from) && i < len(dest)-1 && from[i] == dest[i] {
		i++
	}
	parts := make([]string, 0, len(from)-i+len(dest)-i)
	for j := i; j < len(from); j++ {
		parts = append(parts, "..")
	}
	parts = append(parts, dest[i:]...)
	return strings.Join(parts, "/")
}

func frontMatter(n *exportNode) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(n.title)
	title := strings.TrimSpace(b.String())
	state := n.State
	if state == "" {
		state = "-"
	}
	st, _ := json.Marshal(state)
	return fmt.Sprintf("---\ntitolo: %s\nchiave: %s\nversione: %d\nstato: %s\nordine: %d\n---\n\n", title, n.Key, n.version, st, n.order)
}

// Write scrive lo zip. Gli allegati citati nel testo vengono copiati se il deposito c'è.
func (p *ExportPlan) Write(ctx context.Context, tx pgx.Tx, actor engine.Actor, w io.Writer) error {
	zw := zip.NewWriter(w)
	type manifestPage struct {
		Path    string `json:"path"`
		Key     string `json:"key"`
		Title   string `json:"title"`
		Parent  string `json:"parent,omitempty"`
		Version int    `json:"version"`
		State   string `json:"state,omitempty"`
	}
	var manifest []manifestPage
	files, attachments := 0, 0
	var write func(n *exportNode, parent string) error
	write = func(n *exportNode, parent string) error {
		if !n.skip {
			body, atts, err := p.rewrite(ctx, tx, actor, n)
			if err != nil {
				return err
			}
			hdr := &zip.FileHeader{Name: n.file, Method: zip.Deflate, Modified: n.UpdatedAt}
			fw, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(fw, frontMatter(n)+body+"\n"); err != nil {
				return err
			}
			files++
			manifest = append(manifest, manifestPage{Path: n.file, Key: n.Key, Title: n.title, Parent: parent, Version: n.version, State: n.State})
			for _, a := range atts {
				if err := p.copyAttachment(ctx, tx, actor, zw, a); err != nil {
					return err
				}
				attachments++
			}
		}
		for _, c := range n.children {
			if err := write(c, n.Key); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range p.roots {
		if err := write(r, ""); err != nil {
			return err
		}
	}
	mf, err := zw.CreateHeader(&zip.FileHeader{Name: ManifestName, Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(mf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"formato": "regia-markdown/1", "spazio": p.space.Key, "esportato": time.Now().UTC().Format(time.RFC3339),
		"solo_approvate": p.opt.Approved, "pagine": manifest}); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "space.export", EntityType: "space", EntityID: p.space.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"pages": files, "attachments": attachments, "root": p.opt.Root, "approved": p.opt.Approved}})
}

type exportAttachment struct {
	id, entry string
}

// rewrite riscrive i collegamenti della pagina in relativi e raccoglie gli allegati.
func (p *ExportPlan) rewrite(ctx context.Context, tx pgx.Tx, actor engine.Actor, n *exportNode) (string, []exportAttachment, error) {
	var atts []exportAttachment
	seen := map[string]string{}
	used := map[string]bool{}
	var out []string
	inCode := false
	for _, line := range strings.Split(n.body.MD, "\n") {
		if fenceRe.MatchString(line) {
			inCode = !inCode
			out = append(out, line)
			continue
		}
		if inCode {
			out = append(out, line)
			continue
		}
		line = mdLinkRe.ReplaceAllStringFunc(line, func(m string) string {
			sub := mdLinkRe.FindStringSubmatch(m)
			bang, label, href := sub[1], sub[2], sub[3]
			if am := attachmentURLRe.FindStringSubmatch(href); am != nil {
				if !p.opt.Attachments || p.s.files == nil {
					return m
				}
				entry, ok := seen[am[1]]
				if !ok {
					name, _, err := p.s.files.Open(ctx, tx, actor, am[1])
					if err != nil {
						return m // allegato non leggibile: il collegamento resta com'è
					}
					base := safeFileName(name)
					entry = path.Join(assetsDir, n.Key, base)
					for i := 2; used[entry]; i++ {
						ext := path.Ext(base)
						entry = path.Join(assetsDir, n.Key, strings.TrimSuffix(base, ext)+"-"+strconv.Itoa(i)+ext)
					}
					used[entry] = true
					seen[am[1]] = entry
					atts = append(atts, exportAttachment{id: am[1], entry: entry})
				}
				return bang + "[" + label + "](" + relPath(n.file, entry) + ")"
			}
			if pageKeyRe.MatchString(href) {
				if t := p.byKey[href]; t != nil && !t.skip {
					return bang + "[" + label + "](" + relPath(n.file, t.file) + ")"
				}
			}
			return m
		})
		out = append(out, line)
	}
	return strings.Join(out, "\n"), atts, nil
}

func (p *ExportPlan) copyAttachment(ctx context.Context, tx pgx.Tx, actor engine.Actor, zw *zip.Writer, a exportAttachment) error {
	_, r, err := p.s.files.Open(ctx, tx, actor, a.id)
	if err != nil {
		return nil // già verificato in rewrite; se sparisce nel frattempo si salta
	}
	defer r.Close()
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: a.entry, Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, r)
	return err
}
