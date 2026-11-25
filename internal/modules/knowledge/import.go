package knowledge

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/collab"
	"regia/internal/platform/audit"
)

// Import di uno zip di Markdown, nel formato dell'export (export.go) o scritto altrove:
//   - file.md è una pagina; se esiste una cartella con lo stesso nome, i suoi file sono le
//     sottopagine; index.md o README.md dentro una cartella è la pagina della cartella; una
//     cartella senza pagina ne ottiene una vuota con il suo nome;
//   - titolo: dall'intestazione (titolo:), altrimenti dal primo «# titolo», altrimenti dal
//     nome del file; l'ordine dall'intestazione (ordine:), altrimenti alfabetico;
//   - i collegamenti relativi ad altri .md diventano collegamenti tra pagine, quelli a file
//     dello zip diventano allegati della pagina.
// Si importa sempre in pagine nuove (niente unioni con pagine esistenti). Con Dry si ottiene
// solo l'anteprima di ciò che verrebbe creato.

const (
	// MaxImportBytes: dimensione massima dello zip caricato.
	MaxImportBytes = 64 << 20
	maxImportFiles = 3000
	maxImportPages = 2000
	maxPageFile    = 2 << 20   // un file .md
	maxZipTotal    = 400 << 20 // somma delle dimensioni dichiarate
	maxImportAtts  = 500
)

// ImportOptions guida l'import.
type ImportOptions struct {
	Parent string // chiave della pagina sotto cui importare (vuoto = radice dello spazio)
	Dry    bool
}

// ImportItem è una pagina creata (o da creare).
type ImportItem struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	Level       int    `json:"level"`
	Chars       int    `json:"chars"`
	Attachments int    `json:"attachments"`
	Synthetic   bool   `json:"synthetic"` // pagina vuota creata per una cartella
	Key         string `json:"key,omitempty"`
}

// ImportReport è l'esito (o l'anteprima).
type ImportReport struct {
	Dry         bool         `json:"dry"`
	Pages       int          `json:"pages"`
	Attachments int          `json:"attachments"`
	Items       []ImportItem `json:"items"`
	Warnings    []string     `json:"warnings"`
	Ignored     int          `json:"ignored"` // file dello zip che non servono
}

type importNode struct {
	logical  string // percorso senza estensione; "" mai
	file     string // file .md nello zip ("" se sintetica)
	title    string
	body     string
	order    int
	children []*importNode
	parent   *importNode
	synth    bool
	skipBody bool
	key, id  string
}

func isIndexName(base string) bool {
	b := strings.ToLower(base)
	return b == "index.md" || b == "readme.md"
}

func humanize(name string) string {
	name = strings.TrimSuffix(name, path.Ext(name))
	name = strings.NewReplacer("-", " ", "_", " ").Replace(name)
	name = strings.TrimSpace(name)
	if name == "" {
		return "Senza titolo"
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// splitFrontMatter separa l'intestazione «---» dal testo.
func splitFrontMatter(src string) (map[string]string, string) {
	src = strings.TrimPrefix(src, "\xEF\xBB\xBF")
	src = strings.ReplaceAll(src, "\r\n", "\n")
	meta := map[string]string{}
	if !strings.HasPrefix(src, "---\n") {
		return meta, src
	}
	rest := src[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, src
	}
	after := rest[end+4:]
	if after != "" && !strings.HasPrefix(after, "\n") {
		return meta, src
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch {
		case strings.HasPrefix(v, `"`):
			var s string
			if json.Unmarshal([]byte(v), &s) == nil {
				v = s
			}
		case len(v) >= 2 && strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'"):
			v = strings.ReplaceAll(v[1:len(v)-1], "''", "'")
		}
		meta[k] = v
	}
	return meta, strings.TrimLeft(after, "\n")
}

func firstH1(body string) (string, string, bool) {
	lines := strings.Split(body, "\n")
	inCode := false
	for i, l := range lines {
		if fenceRe.MatchString(l) {
			inCode = !inCode
		}
		if inCode {
			continue
		}
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "# ") {
			rest := lines[i+1:]
			for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
				rest = rest[1:]
			}
			return strings.TrimSpace(strings.TrimLeft(t, "# ")), strings.Join(append(append([]string{}, lines[:i]...), rest...), "\n"), true
		}
		break
	}
	return "", body, false
}

func readZipFile(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("file troppo grande")
	}
	return b, nil
}

func cleanZipName(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasSuffix(name, "/") || slices.Contains(strings.Split(name, "/"), "..") {
		return "", false
	}
	name = path.Clean("/" + name)[1:]
	if name == "" || name == "." {
		return "", false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." || strings.HasPrefix(seg, ".") || seg == "__MACOSX" {
			return "", false
		}
	}
	return name, true
}

// ImportZip legge lo zip e crea le pagine (o ne mostra l'anteprima).
func (s *Service) ImportZip(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string, zr *zip.Reader, opt ImportOptions) (*ImportReport, error) {
	sp, err := s.readableSpace(ctx, tx, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Write {
		return nil, engine.ErrForbidden
	}
	parentKey := ""
	if opt.Parent != "" {
		parent, err := s.eng.Get(ctx, tx, actor, opt.Parent)
		if err != nil || parent.TypeKey != TypePage || parent.SpaceID != sp.ID {
			return nil, engine.NotFound("parent", "page", "la pagina genitore non esiste in questo spazio")
		}
		parentKey = parent.Key
	}
	rep := &ImportReport{Dry: opt.Dry, Items: []ImportItem{}, Warnings: []string{}}
	if len(zr.File) > maxImportFiles {
		return nil, engine.InvalidParams("validation.knowledge.zip_too_many_files", "file", map[string]any{"max": maxImportFiles}, "lo zip contiene troppi file (massimo %d)", maxImportFiles)
	}
	var total uint64
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		name, ok := cleanZipName(f.Name)
		if !ok {
			if !strings.HasSuffix(f.Name, "/") {
				rep.Ignored++
			}
			continue
		}
		total += f.UncompressedSize64
		files[name] = f
	}
	if total > maxZipTotal {
		return nil, engine.InvalidParams("validation.knowledge.zip_too_big", "file", map[string]any{"mb": maxZipTotal >> 20}, "lo zip decompresso supera %d MB", maxZipTotal>>20)
	}
	delete(files, ManifestName)
	files = stripWrapper(files)

	// Pagine.
	nodes := map[string]*importNode{}
	var mdNames []string
	for name := range files {
		if strings.EqualFold(path.Ext(name), ".md") {
			mdNames = append(mdNames, name)
		}
	}
	sort.Strings(mdNames)
	for _, name := range mdNames {
		logical := strings.TrimSuffix(name, path.Ext(name))
		dir := path.Dir(name)
		if isIndexName(path.Base(name)) && dir != "." {
			if _, taken := nodes[dir]; !taken {
				logical = dir
			}
		}
		if _, dup := nodes[logical]; dup {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: esiste già una pagina con lo stesso nome, ignorato", name))
			continue
		}
		raw, err := readZipFile(files[name], maxPageFile)
		n := &importNode{logical: logical, file: name}
		nodes[logical] = n
		if err != nil || !utf8.Valid(raw) {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: file troppo grande o non in UTF-8, importata una pagina vuota", name))
			n.title = humanize(path.Base(logical))
			n.skipBody = true
			continue
		}
		meta, body := splitFrontMatter(string(raw))
		title := strings.TrimSpace(firstNonEmpty(meta["titolo"], meta["title"]))
		if title == "" {
			if t, rest, ok := firstH1(body); ok {
				title, body = t, rest
			}
		}
		if title == "" {
			title = humanize(path.Base(logical))
		}
		if o, err := strconv.Atoi(firstNonEmpty(meta["ordine"], meta["order"])); err == nil && o > 0 {
			n.order = o
		}
		if r := []rune(title); len(r) > 200 {
			title = string(r[:200])
		}
		body, ok := cleanBody(body)
		if !ok {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: testo troppo lungo (massimo %d caratteri), importata una pagina vuota", name, maxBodyRunes))
			body, n.skipBody = "", true
		}
		n.title, n.body = title, body
	}
	// Genitori: la cartella che contiene la pagina; se manca ne nasce una vuota.
	var ensure func(logical string) *importNode
	ensure = func(logical string) *importNode {
		if n := nodes[logical]; n != nil {
			return n
		}
		n := &importNode{logical: logical, title: humanize(path.Base(logical)), synth: true}
		nodes[logical] = n
		return n
	}
	for _, n := range slices.Collect(mapValues(nodes)) {
		for cur := n; ; {
			dir := path.Dir(cur.logical)
			if dir == "." {
				break
			}
			p := ensure(dir)
			cur.parent = p
			cur = p
			if cur.parent != nil {
				break
			}
		}
	}
	var roots []*importNode
	all := slices.Collect(mapValues(nodes))
	for _, n := range all {
		if n.parent == nil {
			roots = append(roots, n)
		} else {
			n.parent.children = append(n.parent.children, n)
		}
	}
	if len(all) > maxImportPages {
		return nil, engine.InvalidParams("validation.knowledge.zip_too_many_pages", "file", map[string]any{"max": maxImportPages}, "lo zip contiene troppe pagine (massimo %d)", maxImportPages)
	}
	if len(all) == 0 {
		return nil, engine.InvalidCode("validation.knowledge.zip_no_markdown", "file", "nello zip non c'è nessun file Markdown (.md)")
	}
	sortNodes := func(list []*importNode) {
		sort.SliceStable(list, func(i, j int) bool {
			a, b := list[i], list[j]
			switch {
			case a.order > 0 && b.order > 0 && a.order != b.order:
				return a.order < b.order
			case a.order > 0 != (b.order > 0):
				return a.order > 0
			}
			return strings.ToLower(a.logical) < strings.ToLower(b.logical)
		})
	}
	var order []*importNode
	var level = map[*importNode]int{}
	var walk func(list []*importNode, depth int)
	walk = func(list []*importNode, depth int) {
		sortNodes(list)
		for _, n := range list {
			level[n] = depth
			order = append(order, n)
			walk(n.children, depth+1)
		}
	}
	walk(roots, 0)

	byFile := map[string]*importNode{}
	for _, n := range order {
		if n.file != "" {
			byFile[n.file] = n
		}
	}
	used := map[string]bool{}
	for _, n := range order {
		if n.file != "" {
			used[n.file] = true
		}
	}
	// Collegamenti: quali file dello zip servono come allegati di quali pagine.
	attCount := map[*importNode]int{}
	linkTargets := func(n *importNode, fn func(bang, label, href, target string) string, body string) string {
		var out []string
		inCode := false
		for _, line := range strings.Split(body, "\n") {
			if fenceRe.MatchString(line) {
				inCode = !inCode
			}
			if !inCode {
				line = mdLinkRe.ReplaceAllStringFunc(line, func(m string) string {
					sub := mdLinkRe.FindStringSubmatch(m)
					href := sub[3]
					if strings.Contains(href, ":") && strings.Index(href, ":") < strings.IndexAny(href+"/", "/?#") || strings.HasPrefix(href, "/") || strings.HasPrefix(href, "#") {
						return m
					}
					clean := href
					if i := strings.IndexAny(clean, "?#"); i >= 0 {
						clean = clean[:i]
					}
					if un, err := url.PathUnescape(clean); err == nil {
						clean = un
					}
					base := "."
					if n.file != "" {
						base = path.Dir(n.file)
					}
					target := path.Clean(path.Join(base, clean))
					if clean == "" || strings.HasPrefix(target, "..") {
						return m
					}
					return fn(sub[1], sub[2], href, target)
				})
			}
			out = append(out, line)
		}
		return strings.Join(out, "\n")
	}
	for _, n := range order {
		if n.body == "" {
			continue
		}
		seen := map[string]bool{}
		linkTargets(n, func(bang, label, href, target string) string {
			if _, isPage := byFile[target]; !isPage {
				if _, ok := files[target]; ok && !seen[target] {
					seen[target] = true
					attCount[n]++
				}
			}
			return ""
		}, n.body)
	}
	for _, n := range order {
		rep.Attachments += attCount[n]
		rep.Items = append(rep.Items, ImportItem{Path: firstNonEmpty(n.file, n.logical+"/"), Title: n.title, Level: level[n],
			Chars: utf8.RuneCountInString(n.body), Attachments: attCount[n], Synthetic: n.synth})
	}
	rep.Pages = len(order)
	for name := range files {
		if byFile[name] == nil {
			if strings.EqualFold(path.Ext(name), ".md") {
				continue
			}
			referenced := false
			for _, n := range order {
				if n.body != "" && strings.Contains(n.body, path.Base(name)) {
					referenced = true
					break
				}
			}
			if !referenced {
				rep.Ignored++
			}
		}
	}
	if s.files == nil && rep.Attachments > 0 {
		rep.Warnings = append(rep.Warnings, "gli allegati non sono disponibili su questo server: i collegamenti ai file restano come sono")
		rep.Attachments = 0
	}
	if rep.Attachments > maxImportAtts {
		return nil, engine.InvalidParams("validation.knowledge.zip_too_many_attachments", "file", map[string]any{"max": maxImportAtts}, "lo zip ha troppi allegati (massimo %d)", maxImportAtts)
	}
	if opt.Dry {
		return rep, nil
	}

	// Pagine, nell'ordine dell'albero: ogni genitore prima dei figli.
	for i, n := range order {
		parent := parentKey
		if n.parent != nil {
			parent = n.parent.key
		}
		v, err := s.CreatePage(ctx, tx, actor, sp.Key, CreateInput{Title: n.title, Parent: parent, Body: n.body, Note: "Importata da zip"})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n.title, err)
		}
		n.key, n.id = v.Page.Key, v.Page.ID
		rep.Items[i].Key = n.key
	}
	// Secondo giro: collegamenti tra pagine e allegati, ora che le chiavi esistono.
	stored := 0
	for _, n := range order {
		if n.body == "" || n.skipBody {
			continue
		}
		changed := false
		cache := map[string]string{}
		body := linkTargets(n, func(bang, label, href, target string) string {
			if t := byFile[target]; t != nil && bang == "" && t.key != "" {
				changed = true
				return "[" + label + "](" + t.key + ")"
			}
			f, ok := files[target]
			img := ""
			if bang != "" {
				img = "!"
			}
			if !ok || s.files == nil || byFile[target] != nil {
				return img + "[" + label + "](" + href + ")"
			}
			id, done := cache[target]
			if !done {
				rc, err := f.Open()
				if err == nil {
					id, err = s.files.Store(ctx, tx, actor, n.id, safeFileName(path.Base(target)), io.LimitReader(rc, 512<<20))
					rc.Close()
				}
				if err != nil {
					rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: allegato %s non importato (%v)", n.file, path.Base(target), err))
					cache[target] = ""
					return img + "[" + label + "](" + href + ")"
				}
				cache[target] = id
				stored++
			}
			if id == "" {
				return img + "[" + label + "](" + href + ")"
			}
			changed = true
			u := "/api/v1/attachments/" + id + "/content"
			if img == "" {
				u += "?download=1"
			}
			return img + "[" + label + "](" + u + ")"
		}, n.body)
		if changed {
			if err := s.setImportedBody(ctx, tx, n.id, body); err != nil {
				return nil, err
			}
		}
	}
	rep.Attachments = stored
	if err := audit.Append(ctx, tx, audit.Event{Action: "space.import", EntityType: "space", EntityID: sp.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"pages": rep.Pages, "attachments": stored, "parent": parentKey}}); err != nil {
		return nil, err
	}
	return rep, nil
}

func mapValues[K comparable, V any](m map[K]V) func(yield func(V) bool) {
	return func(yield func(V) bool) {
		for _, v := range m {
			if !yield(v) {
				return
			}
		}
	}
}

func firstNonEmpty(list ...string) string {
	for _, s := range list {
		if s != "" {
			return s
		}
	}
	return ""
}

// stripWrapper toglie la cartella unica che avvolge tutto lo zip (la crea chi comprime una
// cartella), a meno che non sia lei stessa una pagina.
func stripWrapper(files map[string]*zip.File) map[string]*zip.File {
	top := ""
	for name := range files {
		first, rest, ok := strings.Cut(name, "/")
		if !ok {
			return files
		}
		_ = rest
		if top == "" {
			top = first
		} else if top != first {
			return files
		}
	}
	if top == "" {
		return files
	}
	if _, isPage := files[top+".md"]; isPage {
		return files
	}
	out := make(map[string]*zip.File, len(files))
	for name, f := range files {
		out[strings.TrimPrefix(name, top+"/")] = f
	}
	return out
}

// setImportedBody sostituisce il testo della versione 1 di una pagina appena importata
// (collegamenti riscritti): non nasce una seconda versione.
func (s *Service) setImportedBody(ctx context.Context, tx pgx.Tx, pageID, body string) error {
	mentions, err := collab.ResolveMentions(ctx, tx, body)
	if err != nil {
		return err
	}
	pb := pageBody{Format: "markdown", MD: body, Mentions: mentions}
	if _, err := tx.Exec(ctx, `update elements set body = $2, body_text = $3 where id = $1`, pageID, pb, PlainText(body)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update page_versions set body = $2 where element_id = $1 and version = 1`, pageID, pb); err != nil {
		return err
	}
	return s.syncRefs(ctx, tx, pageID, body)
}
