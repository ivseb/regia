// Package wiki è la wiki personale (replica/ai.md §2 punto 1, ondata 4): le pagine in Markdown
// di ogni persona, con la storia di ogni modifica, l'autore («persona» o «assistente»), le
// differenze e l'annullamento.
//
// Regole:
//   - ogni funzione lavora in una transazione del tenant con il membro impostato
//     (db.TenantOpts.Member): le politiche RLS della migrazione 0087 lasciano leggere e scrivere
//     solo le pagine della persona, e leggere quelle che gli altri hanno condiviso;
//   - ogni modifica è una revisione con l'istantanea completa: annullare vuol dire tornare
//     all'istantanea precedente con una revisione nuova, così anche l'annullamento si annulla;
//   - il pacchetto non chiama il modello: l'aggiornamento con l'AI sta in internal/ai.
package wiki

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/knowledge"
)

// Autori di una modifica.
const (
	AuthorPerson    = "persona"
	AuthorAssistant = "assistente"
)

// Operazioni della storia.
const (
	OpCreate  = "crea"
	OpEdit    = "modifica"
	OpDelete  = "elimina"
	OpRestore = "ripristina"
	OpUndo    = "annulla"
)

// Limiti.
const (
	MaxTitle = 120
	MaxBody  = 50000
	MaxNote  = 500
	MaxPages = 200
	MaxText  = 8000
)

// Error è un errore da mostrare così com'è (l'API lo traduce con il codice).
type Error struct {
	Status int
	Code   string
	Field  string
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

var (
	// ErrNotFound: pagina inesistente, tolta o di un'altra persona e non condivisa.
	ErrNotFound = &Error{Status: 404, Code: "wiki.not_found", Msg: "pagina non trovata"}
	// ErrConflict: la pagina è cambiata dopo che l'editor (o l'assistente) l'ha letta.
	ErrConflict = &Error{Status: 409, Code: "wiki.conflict", Msg: "la pagina è cambiata nel frattempo: ricaricala e riprova"}
	// ErrNotOwner: la pagina condivisa si legge, ma la modifica solo chi la possiede.
	ErrNotOwner = &Error{Status: 403, Code: "wiki.not_owner", Msg: "la pagina è di un'altra persona: puoi solo leggerla"}
)

func invalid(field, code, msg string) error {
	return &Error{Status: 422, Code: code, Field: field, Msg: msg}
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Page è una pagina della wiki.
type Page struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	Version     int       `json:"version"`
	UpdatedBy   string    `json:"updated_by"`
	Shared      bool      `json:"shared"`
	PromotedKey string    `json:"promoted_key,omitempty"`
	Deleted     bool      `json:"deleted"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Mine: la pagina è della persona (altrimenti è condivisa da Owner, in sola lettura).
	Mine  bool   `json:"mine"`
	Owner string `json:"owner,omitempty"`
}

// Summary è una pagina nell'elenco.
type Summary struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Version     int       `json:"version"`
	UpdatedBy   string    `json:"updated_by"`
	Shared      bool      `json:"shared"`
	PromotedKey string    `json:"promoted_key,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	Excerpt     string    `json:"excerpt"`
	Owner       string    `json:"owner,omitempty"`
}

// Revision è una modifica della storia.
type Revision struct {
	ID              string    `json:"id"`
	PageID          string    `json:"page_id"`
	PageTitle       string    `json:"page_title"`
	Version         int       `json:"version"`
	Op              string    `json:"op"`
	Author          string    `json:"author"`
	Title           string    `json:"title"`
	Note            string    `json:"note"`
	Deleted         bool      `json:"deleted"`
	Added           int       `json:"added"`
	Removed         int       `json:"removed"`
	RestoredVersion *int      `json:"restored_version,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	// Latest: è l'ultima modifica della pagina (si può annullare).
	Latest bool `json:"latest"`
}

// ---------------------------------------------------------------------
// Lettura
// ---------------------------------------------------------------------

const pageCols = `p.id::text, p.title, p.body, p.version, p.updated_by, p.shared, coalesce(p.promoted_key, ''),
	p.deleted_at is not null, p.created_at, p.updated_at, p.member_id::text = $1, coalesce(m.display_name, '')`

func scanPage(row pgx.Row) (*Page, error) {
	var p Page
	err := row.Scan(&p.ID, &p.Title, &p.Body, &p.Version, &p.UpdatedBy, &p.Shared, &p.PromotedKey, &p.Deleted,
		&p.CreatedAt, &p.UpdatedAt, &p.Mine, &p.Owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if p.Mine {
		p.Owner = ""
	}
	return &p, nil
}

// Get legge una pagina della persona o una pagina condivisa da un'altra persona.
func Get(ctx context.Context, tx pgx.Tx, member, id string) (*Page, error) {
	if !uuidRe.MatchString(id) {
		return nil, ErrNotFound
	}
	p, err := scanPage(tx.QueryRow(ctx, `select `+pageCols+` from wiki_pages p left join members m on m.id = p.member_id
		where p.id = $2 and (p.deleted_at is null or p.member_id::text = $1)`, member, id))
	if err != nil {
		return nil, err
	}
	if p.Deleted || !p.Mine && !p.Shared {
		return nil, ErrNotFound
	}
	return p, nil
}

// own legge una pagina della persona (anche tolta), bloccandola per la modifica.
func own(ctx context.Context, tx pgx.Tx, member, id string) (*Page, error) {
	if !uuidRe.MatchString(id) {
		return nil, ErrNotFound
	}
	// Prima senza blocco: una pagina condivisa da altri si legge (e la modifica si rifiuta con
	// un errore chiaro), ma la RLS non la lascia bloccare per la modifica.
	p, err := scanPage(tx.QueryRow(ctx, `select `+pageCols+` from wiki_pages p left join members m on m.id = p.member_id
		where p.id = $2`, member, id))
	if err != nil {
		return nil, err
	}
	if !p.Mine {
		if p.Deleted {
			return nil, ErrNotFound
		}
		return nil, ErrNotOwner
	}
	return scanPage(tx.QueryRow(ctx, `select `+pageCols+` from wiki_pages p left join members m on m.id = p.member_id
		where p.id = $2 for update of p`, member, id))
}

// FindByTitle trova una pagina della persona per titolo (senza badare alle maiuscole).
func FindByTitle(ctx context.Context, tx pgx.Tx, member, title string) (*Page, error) {
	return scanPage(tx.QueryRow(ctx, `select `+pageCols+` from wiki_pages p left join members m on m.id = p.member_id
		where p.member_id::text = $1 and lower(p.title) = lower($2) and p.deleted_at is null`, member, strings.TrimSpace(title)))
}

// All sono le pagine della persona (non tolte), le più recenti prima.
func All(ctx context.Context, tx pgx.Tx, member string) ([]Page, error) {
	rows, err := tx.Query(ctx, `select `+pageCols+` from wiki_pages p left join members m on m.id = p.member_id
		where p.member_id::text = $1 and p.deleted_at is null order by p.updated_at desc limit $2`, member, MaxPages)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Page
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// List riassume le pagine della persona.
func List(ctx context.Context, tx pgx.Tx, member string) ([]Summary, error) {
	pages, err := All(ctx, tx, member)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(pages))
	for _, p := range pages {
		out = append(out, summary(p))
	}
	return out, nil
}

// SharedWithMe sono le pagine che le altre persone hanno condiviso.
func SharedWithMe(ctx context.Context, tx pgx.Tx, member string) ([]Summary, error) {
	rows, err := tx.Query(ctx, `select `+pageCols+` from wiki_pages p left join members m on m.id = p.member_id
		where p.member_id::text <> $1 and p.shared and p.deleted_at is null order by p.updated_at desc limit 100`, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, summary(*p))
	}
	return out, rows.Err()
}

func summary(p Page) Summary {
	return Summary{ID: p.ID, Title: p.Title, Version: p.Version, UpdatedBy: p.UpdatedBy, Shared: p.Shared,
		PromotedKey: p.PromotedKey, UpdatedAt: p.UpdatedAt, Excerpt: Excerpt(p.Body, 160), Owner: p.Owner}
}

var mdNoise = regexp.MustCompile("[#*_`>\\[\\]|]+")

// Excerpt è l'inizio del testo senza i segni del Markdown.
func Excerpt(body string, n int) string {
	var parts []string
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSpace(mdNoise.ReplaceAllString(l, ""))
		l = strings.TrimLeft(l, "-+ ")
		if l != "" {
			parts = append(parts, l)
		}
		if utf8.RuneCountInString(strings.Join(parts, " ")) > n {
			break
		}
	}
	return clip(strings.Join(parts, " "), n)
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n-1])) + "…"
}

// ---------------------------------------------------------------------
// Scrittura
// ---------------------------------------------------------------------

// SaveInput crea (ID vuoto) o modifica una pagina.
type SaveInput struct {
	ID    string
	Title string
	Body  string
	// BaseVersion: versione da cui è partito chi scrive (0 = non controllare).
	BaseVersion int
	Author      string
	Note        string
}

func clean(in *SaveInput) error {
	in.Title = strings.Join(strings.Fields(in.Title), " ")
	if n := utf8.RuneCountInString(in.Title); n < 1 || n > MaxTitle {
		return invalid("title", "wiki.title_invalid", "scrivi un titolo (al massimo 120 caratteri)")
	}
	in.Body = strings.TrimRight(strings.ReplaceAll(in.Body, "\r\n", "\n"), " \n\t")
	if !utf8.ValidString(in.Body) || utf8.RuneCountInString(in.Body) > MaxBody {
		return invalid("body", "wiki.body_invalid", "testo troppo lungo (al massimo 50000 caratteri)")
	}
	in.Note = clip(strings.TrimSpace(in.Note), MaxNote)
	if in.Author != AuthorAssistant {
		in.Author = AuthorPerson
	}
	return nil
}

// Save crea o modifica una pagina della persona, con la revisione.
func Save(ctx context.Context, tx pgx.Tx, member string, in SaveInput) (*Page, error) {
	if err := clean(&in); err != nil {
		return nil, err
	}
	if in.ID == "" {
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from wiki_pages where member_id::text = $1 and deleted_at is null`, member).Scan(&n); err != nil {
			return nil, err
		}
		if n >= MaxPages {
			return nil, &Error{Status: 422, Code: "wiki.too_many", Msg: "la wiki ha già 200 pagine: togline qualcuna"}
		}
		if err := titleFree(ctx, tx, member, in.Title, ""); err != nil {
			return nil, err
		}
		var id string
		if err := tx.QueryRow(ctx, `insert into wiki_pages (tenant_id, member_id, title, body, updated_by)
			values (app_tenant(), $1, $2, $3, $4) returning id::text`, member, in.Title, in.Body, in.Author).Scan(&id); err != nil {
			return nil, err
		}
		note := in.Note
		if note == "" && in.Author == AuthorPerson {
			note = "Pagina creata"
		}
		if err := addRevision(ctx, tx, member, id, 1, OpCreate, in.Author, in.Title, in.Body, false, note, "", nil); err != nil {
			return nil, err
		}
		return Get(ctx, tx, member, id)
	}
	p, err := own(ctx, tx, member, in.ID)
	if err != nil {
		return nil, err
	}
	if p.Deleted {
		return nil, ErrNotFound
	}
	if in.BaseVersion > 0 && in.BaseVersion != p.Version {
		return nil, ErrConflict
	}
	if p.Title == in.Title && p.Body == in.Body {
		return p, nil // niente da salvare: nessuna revisione vuota
	}
	if !strings.EqualFold(p.Title, in.Title) {
		if err := titleFree(ctx, tx, member, in.Title, p.ID); err != nil {
			return nil, err
		}
	}
	if err := write(ctx, tx, member, p, in.Title, in.Body, false, OpEdit, in.Author, in.Note, nil); err != nil {
		return nil, err
	}
	return Get(ctx, tx, member, p.ID)
}

func titleFree(ctx context.Context, tx pgx.Tx, member, title, except string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from wiki_pages where member_id::text = $1 and lower(title) = lower($2)
		and deleted_at is null and id::text <> $3)`, member, title, except).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return &Error{Status: 409, Code: "wiki.title_taken", Field: "title", Msg: "hai già una pagina con questo titolo"}
	}
	return nil
}

// write porta la pagina allo stato nuovo e scrive la revisione.
func write(ctx context.Context, tx pgx.Tx, member string, p *Page, title, body string, deleted bool, op, author, note string, restored *int) error {
	v := p.Version + 1
	if _, err := tx.Exec(ctx, `update wiki_pages set title = $2, body = $3, version = $4, updated_by = $5, updated_at = now(),
		deleted_at = case when $6 then coalesce(deleted_at, now()) else null end where id = $1`, p.ID, title, body, v, author, deleted); err != nil {
		return err
	}
	from := p.Body
	if p.Deleted {
		from = "" // la pagina era tolta: il confronto parte dal vuoto
	}
	return addRevision(ctx, tx, member, p.ID, v, op, author, title, body, deleted, note, from, restored)
}

// addRevision scrive la revisione con le righe aggiunte e tolte rispetto a from.
func addRevision(ctx context.Context, tx pgx.Tx, member, pageID string, version int, op, author, title, body string, deleted bool, note, from string, restored *int) error {
	to := body
	if deleted {
		to = ""
	}
	_, st := knowledge.Diff(from, to)
	_, err := tx.Exec(ctx, `insert into wiki_revisions (tenant_id, page_id, member_id, version, op, author, title, body, deleted, note,
		added, removed, restored_version) values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		pageID, member, version, op, author, title, body, deleted, note, st.Added+st.Changed, st.Removed+st.Changed, restored)
	return err
}

// Delete toglie una pagina (resta nella storia e si ripristina con «annulla»).
func Delete(ctx context.Context, tx pgx.Tx, member, id string) error {
	p, err := own(ctx, tx, member, id)
	if err != nil {
		return err
	}
	if p.Deleted {
		return ErrNotFound
	}
	return write(ctx, tx, member, p, p.Title, p.Body, true, OpDelete, AuthorPerson, "Pagina tolta", nil)
}

// Restore riporta una pagina della persona allo stato di una versione (0 = prima della
// creazione: la pagina si toglie), con una revisione nuova.
func Restore(ctx context.Context, tx pgx.Tx, member, id string, version int, op, author, note string) (*Page, error) {
	p, err := own(ctx, tx, member, id)
	if err != nil {
		return nil, err
	}
	if version < 0 || version >= p.Version {
		return nil, invalid("version", "wiki.version_invalid", "versione inesistente o già quella attuale")
	}
	title, body, deleted := p.Title, p.Body, true
	if version > 0 {
		err := tx.QueryRow(ctx, `select title, body, deleted from wiki_revisions where page_id = $1 and version = $2`, p.ID, version).
			Scan(&title, &body, &deleted)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, invalid("version", "wiki.version_invalid", "versione non più conservata")
		}
		if err != nil {
			return nil, err
		}
	}
	if !deleted && (p.Deleted || !strings.EqualFold(p.Title, title)) {
		if err := titleFree(ctx, tx, member, title, p.ID); err != nil {
			return nil, err
		}
	}
	v := version
	if err := write(ctx, tx, member, p, title, body, deleted, op, author, note, &v); err != nil {
		return nil, err
	}
	out, err := own(ctx, tx, member, id)
	return out, err
}

// Undo annulla una modifica: deve essere l'ultima della sua pagina (per le precedenti c'è
// «ripristina questa versione» nella storia).
func Undo(ctx context.Context, tx pgx.Tx, member, revisionID, author string) (*Page, error) {
	if !uuidRe.MatchString(revisionID) {
		return nil, ErrNotFound
	}
	var pageID string
	var version, current int
	err := tx.QueryRow(ctx, `select r.page_id::text, r.version, p.version from wiki_revisions r join wiki_pages p on p.id = r.page_id
		where r.id = $1`, revisionID).Scan(&pageID, &version, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if version != current {
		return nil, &Error{Status: 409, Code: "wiki.undo_not_latest", Msg: "si annulla solo l'ultima modifica della pagina: per le precedenti ripristina una versione dalla storia"}
	}
	return Restore(ctx, tx, member, pageID, version-1, OpUndo, author, fmt.Sprintf("Annullata la modifica %d", version))
}

// SetShared condivide (o no) una pagina della persona con le altre persone dell'organizzazione.
func SetShared(ctx context.Context, tx pgx.Tx, member, id string, shared bool) (*Page, error) {
	p, err := own(ctx, tx, member, id)
	if err != nil {
		return nil, err
	}
	if p.Deleted {
		return nil, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `update wiki_pages set shared = $2 where id = $1`, p.ID, shared); err != nil {
		return nil, err
	}
	return Get(ctx, tx, member, p.ID)
}

// SetPromoted ricorda la pagina Knowledge creata dalla pagina.
func SetPromoted(ctx context.Context, tx pgx.Tx, member, id, key string) error {
	_, err := tx.Exec(ctx, `update wiki_pages set promoted_key = $3 where id = $1 and member_id::text = $2`, id, member, key)
	return err
}

// ---------------------------------------------------------------------
// Storia e registro
// ---------------------------------------------------------------------

const revCols = `r.id::text, r.page_id::text, p.title, r.version, r.op, r.author, r.title, r.note, r.deleted, r.added, r.removed,
	r.restored_version, r.created_at, r.version = p.version`

func scanRevisions(rows pgx.Rows) ([]Revision, error) {
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		var r Revision
		if err := rows.Scan(&r.ID, &r.PageID, &r.PageTitle, &r.Version, &r.Op, &r.Author, &r.Title, &r.Note, &r.Deleted,
			&r.Added, &r.Removed, &r.RestoredVersion, &r.CreatedAt, &r.Latest); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// History è la storia di una pagina della persona, la più recente prima.
func History(ctx context.Context, tx pgx.Tx, member, id string) ([]Revision, error) {
	if !uuidRe.MatchString(id) {
		return nil, ErrNotFound
	}
	rows, err := tx.Query(ctx, `select `+revCols+` from wiki_revisions r join wiki_pages p on p.id = r.page_id
		where r.page_id = $1 and p.member_id::text = $2 order by r.version desc limit 500`, id, member)
	if err != nil {
		return nil, err
	}
	out, err := scanRevisions(rows)
	if err == nil && len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, err
}

// Log è il registro delle modifiche della wiki della persona, le più recenti prima.
func Log(ctx context.Context, tx pgx.Tx, member string, limit int) ([]Revision, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := tx.Query(ctx, `select `+revCols+` from wiki_revisions r join wiki_pages p on p.id = r.page_id
		where r.member_id::text = $1 order by r.created_at desc, r.version desc limit $2`, member, limit)
	if err != nil {
		return nil, err
	}
	return scanRevisions(rows)
}

// Change è una revisione con il confronto rispetto alla precedente.
type Change struct {
	Revision Revision             `json:"revision"`
	Body     string               `json:"body"`
	Lines    []knowledge.DiffLine `json:"lines"`
	Stats    knowledge.DiffStats  `json:"stats"`
	// TitleFrom: il titolo prima della modifica, se è cambiato.
	TitleFrom string `json:"title_from,omitempty"`
}

// Diff confronta una versione con la precedente.
func Diff(ctx context.Context, tx pgx.Tx, member, id string, version int) (*Change, error) {
	if !uuidRe.MatchString(id) {
		return nil, ErrNotFound
	}
	rows, err := tx.Query(ctx, `select `+revCols+` from wiki_revisions r join wiki_pages p on p.id = r.page_id
		where r.page_id = $1 and p.member_id::text = $2 and r.version = $3`, id, member, version)
	if err != nil {
		return nil, err
	}
	revs, err := scanRevisions(rows)
	if err != nil {
		return nil, err
	}
	if len(revs) == 0 {
		return nil, ErrNotFound
	}
	var body, prevBody, prevTitle string
	var deleted, prevDeleted bool
	if err := tx.QueryRow(ctx, `select body, deleted from wiki_revisions where page_id = $1 and version = $2`, id, version).Scan(&body, &deleted); err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `select title, body, deleted from wiki_revisions where page_id = $1 and version = $2`, id, version-1).
		Scan(&prevTitle, &prevBody, &prevDeleted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	from, to := prevBody, body
	if prevDeleted {
		from = ""
	}
	if deleted {
		to = ""
	}
	c := &Change{Revision: revs[0], Body: body}
	c.Lines, c.Stats = knowledge.Diff(from, to)
	if prevTitle != "" && prevTitle != revs[0].Title {
		c.TitleFrom = prevTitle
	}
	return c, nil
}

// ---------------------------------------------------------------------
// Fonti aggiunte a mano
// ---------------------------------------------------------------------

// Source è una fonte aggiunta a mano, da leggere al prossimo aggiornamento.
type Source struct {
	ID        string
	Kind      string
	ElementID string
	PageID    string
	Text      string
	Note      string
	CreatedAt time.Time
}

// AddSource registra una fonte aggiunta a mano.
func AddSource(ctx context.Context, tx pgx.Tx, member, kind, elementID, pageID, text, note string) error {
	var el, pg any
	if elementID != "" {
		el = elementID
	}
	if pageID != "" {
		pg = pageID
	}
	_, err := tx.Exec(ctx, `insert into wiki_sources (tenant_id, member_id, kind, element_id, page_id, text, note)
		values (app_tenant(), $1, $2, $3, $4, $5, $6)`, member, kind, el, pg, clip(text, MaxText), clip(note, MaxNote))
	return err
}

// PendingSources sono le fonti non ancora usate da un aggiornamento (al massimo 20).
func PendingSources(ctx context.Context, tx pgx.Tx, member string) ([]Source, error) {
	rows, err := tx.Query(ctx, `select id::text, kind, coalesce(element_id::text, ''), coalesce(page_id::text, ''), text, note, created_at
		from wiki_sources where member_id::text = $1 and used_at is null order by created_at limit 20`, member)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Source])
}

// MarkUsed segna le fonti come usate.
func MarkUsed(ctx context.Context, tx pgx.Tx, member string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `update wiki_sources set used_at = now() where member_id::text = $1 and id::text = any($2)`, member, ids)
	return err
}

// AddInput è «aggiungi alla wiki»: un elemento (da una scheda) o un testo (una risposta
// dell'assistente), aggiunto in fondo a una pagina e ricordato come fonte per l'assistente.
type AddInput struct {
	Element string `json:"element"`
	Text    string `json:"text"`
	// Page è la pagina della persona; vuota = la pagina con il titolo Title (creata se manca).
	Page  string `json:"page"`
	Title string `json:"title"`
	Note  string `json:"note"`
}

// DefaultAddTitle è la pagina di «aggiungi alla wiki» se la persona non ne sceglie una.
const DefaultAddTitle = "Appunti"

// Add aggiunge un elemento o un testo alla wiki della persona, con i suoi permessi: funziona
// anche senza AI (è una modifica della persona), e il prossimo aggiornamento lo legge.
func Add(ctx context.Context, tx pgx.Tx, eng *engine.Service, actor engine.Actor, in AddInput) (*Page, error) {
	member := actor.MemberID
	in.Text = strings.TrimSpace(strings.ReplaceAll(in.Text, "\r\n", "\n"))
	in.Note = clip(strings.Join(strings.Fields(in.Note), " "), MaxNote)
	in.Element = strings.TrimSpace(in.Element)
	if (in.Element == "") == (in.Text == "") {
		return nil, invalid("element", "wiki.add_invalid", "indica un elemento oppure un testo da aggiungere")
	}
	if utf8.RuneCountInString(in.Text) > MaxText {
		return nil, invalid("text", "wiki.text_invalid", "testo troppo lungo (al massimo 8000 caratteri)")
	}
	var line, note, kind, elementID string
	if in.Element != "" {
		el, err := eng.Get(ctx, tx, actor, in.Element)
		if err != nil {
			return nil, err // inesistente o non visibile: 404 come per la scheda
		}
		kind, elementID = "elemento", el.ID
		key := el.Key
		if key == "" {
			key = el.ID
		}
		line = fmt.Sprintf("- %s — %s", key, strings.Join(strings.Fields(el.Title), " "))
		if in.Note != "" {
			line += ": " + in.Note
		}
		note = "Aggiunto " + key
	} else {
		kind = "risposta"
		var b strings.Builder
		for _, l := range strings.Split(in.Text, "\n") {
			b.WriteString("> " + l + "\n")
		}
		line = strings.TrimRight(b.String(), "\n")
		if in.Note != "" {
			line = in.Note + "\n\n" + line
		}
		note = "Aggiunta una risposta dell'assistente"
	}
	var page *Page
	var err error
	switch {
	case in.Page != "":
		if page, err = own(ctx, tx, member, in.Page); err == nil && page.Deleted {
			err = ErrNotFound
		}
	default:
		title := strings.Join(strings.Fields(in.Title), " ")
		if title == "" {
			title = DefaultAddTitle
		}
		page, err = FindByTitle(ctx, tx, member, title)
		if errors.Is(err, ErrNotFound) {
			page, err = Save(ctx, tx, member, SaveInput{Title: title, Body: line, Author: AuthorPerson, Note: note})
			if err != nil {
				return nil, err
			}
			return page, AddSource(ctx, tx, member, kind, elementID, page.ID, in.Text, in.Note)
		}
	}
	if err != nil {
		return nil, err
	}
	body := line
	if strings.TrimSpace(page.Body) != "" {
		body = page.Body + "\n\n" + line
	}
	if page, err = Save(ctx, tx, member, SaveInput{ID: page.ID, Title: page.Title, Body: body, Author: AuthorPerson, Note: note}); err != nil {
		return nil, err
	}
	return page, AddSource(ctx, tx, member, kind, elementID, page.ID, in.Text, in.Note)
}

// ---------------------------------------------------------------------
// Stato dell'aggiornamento
// ---------------------------------------------------------------------

// State è lo stato dell'aggiornamento con l'AI della wiki della persona.
type State struct {
	Auto        bool       `json:"auto"`
	Running     bool       `json:"running"`
	RequestedAt *time.Time `json:"requested_at,omitempty"`
	LastRunAt   *time.Time `json:"last_run_at,omitempty"`
	LastOutcome string     `json:"last_outcome,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	LastPages   int        `json:"last_pages"`
	LastNote    string     `json:"last_note,omitempty"`
}

// GetState legge lo stato (senza riga: spento, mai aggiornata).
func GetState(ctx context.Context, tx pgx.Tx, member string) (State, error) {
	var s State
	var outcome, errCode *string
	err := tx.QueryRow(ctx, `select auto, coalesce(running_until > now(), false), requested_at, last_run_at, last_outcome, last_error,
		last_pages, last_note from wiki_state where member_id::text = $1`, member).
		Scan(&s.Auto, &s.Running, &s.RequestedAt, &s.LastRunAt, &outcome, &errCode, &s.LastPages, &s.LastNote)
	if errors.Is(err, pgx.ErrNoRows) {
		return State{}, nil
	}
	if outcome != nil {
		s.LastOutcome = *outcome
	}
	if errCode != nil {
		s.LastError = *errCode
	}
	return s, err
}

// SetAuto accende o spegne l'aggiornamento automatico.
func SetAuto(ctx context.Context, tx pgx.Tx, member string, auto bool) error {
	_, err := tx.Exec(ctx, `insert into wiki_state (tenant_id, member_id, auto) values (app_tenant(), $1, $2)
		on conflict (tenant_id, member_id) do update set auto = excluded.auto, updated_at = now()`, member, auto)
	return err
}

// Claim prende l'aggiornamento della persona per sé (per al massimo ttl): falso se un altro
// aggiornamento è già in corso.
func Claim(ctx context.Context, tx pgx.Tx, member string, ttl time.Duration) (bool, error) {
	tag, err := tx.Exec(ctx, `insert into wiki_state (tenant_id, member_id, running_until) values (app_tenant(), $1, now() + $2::interval)
		on conflict (tenant_id, member_id) do update set running_until = excluded.running_until, updated_at = now()
		where wiki_state.running_until is null or wiki_state.running_until < now()`, member, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Request chiede un aggiornamento appena possibile («Aggiorna adesso»).
func Request(ctx context.Context, tx pgx.Tx, member string) error {
	_, err := tx.Exec(ctx, `insert into wiki_state (tenant_id, member_id, requested_at) values (app_tenant(), $1, now())
		on conflict (tenant_id, member_id) do update set requested_at = now(), updated_at = now()`, member)
	return err
}

// Finish chiude un aggiornamento con il suo esito.
func Finish(ctx context.Context, tx pgx.Tx, member, outcome, errCode string, pages int, note string) error {
	var ec any
	if errCode != "" {
		ec = clip(errCode, 80)
	}
	_, err := tx.Exec(ctx, `update wiki_state set running_until = null, requested_at = null, last_run_at = now(), last_outcome = $2,
		last_error = $3, last_pages = $4, last_note = $5, updated_at = now() where member_id::text = $1`, member, outcome, ec, pages, clip(note, 1000))
	return err
}

// ---------------------------------------------------------------------
// Collegamenti agli elementi
// ---------------------------------------------------------------------

// Ref è un elemento citato in una pagina: per chiave (DEM-12) o per codice (APP-MOB).
type Ref struct {
	Token         string `json:"token"`
	Key           string `json:"key"`
	Title         string `json:"title"`
	Type          string `json:"type"`
	TypeName      string `json:"type_name"`
	State         string `json:"state"`
	StateCategory string `json:"state_category"`
}

var (
	keyRe   = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{1,19}-[1-9][0-9]{0,17}\b`)
	codeRe  = regexp.MustCompile(`\b[A-Z][A-Z0-9_]*(?:[-.][A-Z0-9_]+)+\b`)
	fenceRe = regexp.MustCompile("^\\s*(```|~~~)")
)

// Tokens sono le chiavi e i codici citati nel testo, fuori dai blocchi di codice.
func Tokens(md string) (keys, codes []string) {
	inCode := false
	for _, line := range strings.Split(md, "\n") {
		if fenceRe.MatchString(line) {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		for _, k := range keyRe.FindAllString(line, -1) {
			if !slices.Contains(keys, k) && len(keys) < 200 {
				keys = append(keys, k)
			}
		}
		for _, c := range codeRe.FindAllString(line, -1) {
			if !keyRe.MatchString(c) && !slices.Contains(codes, c) && len(codes) < 50 {
				codes = append(codes, c)
			}
		}
	}
	return keys, codes
}

// Cited sono chiavi e codici citati in un testo libero (una domanda).
func Cited(text string) []string {
	k, c := Tokens(text)
	return append(k, c...)
}

// Refs risolve chiavi e codici citati con i permessi di chi legge: ciò che non esiste o non
// si vede resta testo.
func Refs(ctx context.Context, tx pgx.Tx, eng *engine.Service, actor engine.Actor, md string) ([]Ref, error) {
	keys, codes := Tokens(md)
	out := []Ref{}
	for _, tok := range append(keys, codes...) {
		el, err := eng.Get(ctx, tx, actor, tok)
		if errors.Is(err, engine.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, Ref{Token: tok, Key: el.Key, Title: el.Title, Type: el.TypeKey, TypeName: el.TypeName, State: el.State, StateCategory: el.StateCategory})
	}
	return out, nil
}

// ---------------------------------------------------------------------
// Contesto per l'assistente
// ---------------------------------------------------------------------

var stop = map[string]bool{
	"della": true, "delle": true, "dello": true, "degli": true, "nella": true, "nelle": true, "sono": true, "come": true,
	"quali": true, "quale": true, "questa": true, "questo": true, "anche": true, "dove": true, "quando": true, "perché": true,
	"with": true, "what": true, "which": true, "this": true, "that": true, "from": true, "have": true, "there": true,
	"cosa": true, "fare": true, "sulla": true, "sulle": true, "dalla": true, "alla": true, "alle": true, "miei": true, "mie": true,
}

func words(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if utf8.RuneCountInString(w) >= 4 && !stop[w] && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// Pertinent sceglie le pagine della persona pertinenti a un testo (la domanda, la scheda) e
// alle chiavi citate, finché restano maxChars caratteri: prima quelle che citano le stesse
// chiavi, poi quelle con più parole in comune (prima nel titolo). Il testo di una pagina troppo
// lunga si taglia.
func Pertinent(pages []Page, text string, keys []string, maxChars int) []Page {
	type scored struct {
		p     Page
		score int
	}
	ws := words(text)
	upper := strings.ToUpper(text)
	var list []scored
	for _, p := range pages {
		score := 0
		pk, pc := Tokens(p.Body)
		for _, k := range keys {
			if slices.Contains(pk, k) || slices.Contains(pc, k) {
				score += 10
			}
		}
		for _, k := range append(pk, pc...) {
			if strings.Contains(upper, k) && !slices.Contains(keys, k) {
				score += 10
			}
		}
		title := strings.ToLower(p.Title)
		body := strings.ToLower(p.Body)
		for _, w := range ws {
			if strings.Contains(title, w) {
				score += 3
			} else if strings.Contains(body, w) {
				score++
			}
		}
		if score > 0 {
			list = append(list, scored{p, score})
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].p.UpdatedAt.After(list[j].p.UpdatedAt)
	})
	var out []Page
	left := maxChars
	for _, s := range list {
		need := utf8.RuneCountInString(s.p.Title) + utf8.RuneCountInString(s.p.Body) + 20
		if need > left {
			if left < 400 {
				break
			}
			s.p.Body = clip(s.p.Body, left-utf8.RuneCountInString(s.p.Title)-40)
			need = left
		}
		out = append(out, s.p)
		left -= need
		if len(out) >= 5 || left <= 0 {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------------
// Esportazione e conservazione
// ---------------------------------------------------------------------

// Export è la wiki della persona in un solo file Markdown.
func Export(ctx context.Context, tx pgx.Tx, member string) (string, error) {
	pages, err := All(ctx, tx, member)
	if err != nil {
		return "", err
	}
	sort.SliceStable(pages, func(i, j int) bool { return strings.ToLower(pages[i].Title) < strings.ToLower(pages[j].Title) })
	var b strings.Builder
	for i, p := range pages {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		fmt.Fprintf(&b, "# %s\n\n_v%d · %s · %s_\n\n%s\n", p.Title, p.Version, p.UpdatedBy, p.UpdatedAt.UTC().Format(time.RFC3339), p.Body)
	}
	return b.String(), nil
}

// Purge applica la conservazione del tenant alla wiki della persona: le pagine tolte da più di
// days giorni si cancellano, della storia restano le revisioni più recenti di days giorni e
// sempre l'ultima di ogni pagina, le fonti usate si cancellano. Le pagine restano.
func Purge(ctx context.Context, tx pgx.Tx, member string, days int) (int64, error) {
	if days <= 0 {
		days = 90
	}
	var total int64
	for _, q := range []string{
		`delete from wiki_pages where member_id::text = $1 and deleted_at < now() - make_interval(days => $2)`,
		`delete from wiki_revisions r using wiki_pages p where p.id = r.page_id and r.member_id::text = $1
			and r.created_at < now() - make_interval(days => $2) and r.version < p.version`,
		`delete from wiki_sources where member_id::text = $1 and used_at < now() - make_interval(days => $2)`,
	} {
		tag, err := tx.Exec(ctx, q, member, days)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
	}
	return total, nil
}
