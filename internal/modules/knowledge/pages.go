package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/modules/collab"
	"regia/internal/platform/audit"
)

// pageBody è il contenuto di elements.body e page_versions.body.
type pageBody struct {
	Format   string           `json:"format"`
	MD       string           `json:"md"`
	Mentions []collab.Mention `json:"mentions"`
}

// TreeNode è una pagina nell'albero di navigazione.
type TreeNode struct {
	ID            string    `json:"id"`
	Key           string    `json:"key"`
	Title         string    `json:"title"`
	ParentID      *string   `json:"parent_id"`
	State         string    `json:"state,omitempty"`
	StateCategory string    `json:"state_category,omitempty"`
	Controlled    bool      `json:"controlled"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// SpaceView è lo spazio con l'albero delle pagine (nell'ordine manuale).
type SpaceView struct {
	Space *Space     `json:"space"`
	Pages []TreeNode `json:"pages"`
}

// Tree restituisce lo spazio e tutte le sue pagine in forma compatta: il client costruisce
// l'albero (anche migliaia di pagine stanno in una risposta).
func (s *Service) Tree(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string) (*SpaceView, error) {
	sp, err := s.readableSpace(ctx, tx, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select e.id::text, s.key || '-' || e.number, e.title, e.parent_id::text, coalesce(o.name, ''), coalesce(o.category, ''),
		       coalesce(k.controlled, false), e.updated_at
		from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'page'
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join kb_pages k on k.tenant_id = e.tenant_id and k.element_id = e.id
		where e.space_id = $1 and e.deleted_at is null and not e.is_draft
		order by e.rank collate "C" nulls last, e.number`, sp.ID)
	if err != nil {
		return nil, err
	}
	pages, err := pgx.CollectRows(rows, pgx.RowToStructByPos[TreeNode])
	if pages == nil {
		pages = []TreeNode{}
	}
	return &SpaceView{Space: sp, Pages: pages}, err
}

// Page è una pagina con il suo testo e lo stato di documento controllato.
type Page struct {
	ID              string           `json:"id"`
	Key             string           `json:"key"`
	Title           string           `json:"title"`
	SpaceKey        string           `json:"space_key"`
	SpaceName       string           `json:"space_name"`
	ParentID        *string          `json:"parent_id"`
	Version         int              `json:"version"`
	Body            string           `json:"body"`
	Mentions        []collab.Mention `json:"mentions"`
	State           string           `json:"state"`
	StateCategory   string           `json:"state_category"`
	Controlled      bool             `json:"controlled"`
	ReviewMonths    *int             `json:"review_months"`
	NextReview      *string          `json:"next_review"`
	ApprovedVersion *int             `json:"approved_version"`
	TemplateKey     string           `json:"template_key"`
	CreatedAt       time.Time        `json:"created_at"`
	CreatedBy       string           `json:"created_by"`
	UpdatedAt       time.Time        `json:"updated_at"`
	UpdatedBy       string           `json:"updated_by"`
	// ElementVersion: versione dell'elemento (lock ottimistico del motore).
	ElementVersion int `json:"element_version"`
}

// Crumb è un riferimento breve a una pagina.
type Crumb struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

// Person è un revisore o un approvatore.
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PageView è la pagina da mostrare: testo, percorso, figli, blocco, versione approvata,
// revisori e approvatori, elementi documentati.
type PageView struct {
	Page      Page                   `json:"page"`
	Rights    Rights                 `json:"rights"`
	Path      []Crumb                `json:"path"`
	Children  []Crumb                `json:"children"`
	Lock      *Lock                  `json:"lock"`
	Approved  *VersionView           `json:"approved"`
	Reviewers []Person               `json:"reviewers"`
	Approvers []Person               `json:"approvers"`
	Links     []engine.LinkedElement `json:"links"`
}

// kbRow sono i dati dell'estensione della pagina.
type kbRow struct {
	version         int
	controlled      bool
	reviewMonths    *int
	nextReview      *time.Time
	approvedVersion *int
	templateKey     *string
}

// loadPage legge la pagina (lettura verificata dal motore, anche per lo spazio) e lo spazio.
func (s *Service) loadPage(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, lock bool) (*engine.Element, *Space, *kbRow, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, nil, nil, err
	}
	if el.TypeKey != TypePage {
		return nil, nil, nil, engine.ErrNotFound
	}
	sp, err := s.space(ctx, tx, actor, el.SpaceKey)
	if err != nil {
		return nil, nil, nil, err
	}
	q := `select version, controlled, review_months, next_review, approved_version, template_key from kb_pages where element_id = $1`
	if lock {
		q += " for update"
	}
	var k kbRow
	if err := tx.QueryRow(ctx, q, el.ID).Scan(&k.version, &k.controlled, &k.reviewMonths, &k.nextReview, &k.approvedVersion, &k.templateKey); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, engine.ErrNotFound
		}
		return nil, nil, nil, err
	}
	return el, sp, &k, nil
}

func readBody(raw []byte) pageBody {
	var b pageBody
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &b)
	}
	if b.Mentions == nil {
		b.Mentions = []collab.Mention{}
	}
	return b
}

// GetPage restituisce la pagina con tutto ciò che serve alla vista.
func (s *Service) GetPage(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*PageView, error) {
	el, sp, k, err := s.loadPage(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	var raw []byte
	p := Page{ID: el.ID, Key: el.Key, Title: el.Title, SpaceKey: sp.Key, SpaceName: sp.Name, ParentID: el.ParentID, Version: k.version,
		State: el.State, StateCategory: el.StateCategory, Controlled: k.controlled, ReviewMonths: k.reviewMonths,
		ApprovedVersion: k.approvedVersion, TemplateKey: deref(k.templateKey), CreatedAt: el.CreatedAt, UpdatedAt: el.UpdatedAt,
		ElementVersion: el.Version}
	if k.nextReview != nil {
		d := k.nextReview.Format("2006-01-02")
		p.NextReview = &d
	}
	if err := tx.QueryRow(ctx, `select e.body, coalesce(c.display_name, ''), coalesce(u.display_name, '')
		from elements e
		left join members c on c.tenant_id = e.tenant_id and c.id = e.created_by
		left join members u on u.tenant_id = e.tenant_id and u.id = e.updated_by
		where e.id = $1`, el.ID).Scan(&raw, &p.CreatedBy, &p.UpdatedBy); err != nil {
		return nil, err
	}
	b := readBody(raw)
	p.Body, p.Mentions = b.MD, b.Mentions
	v := &PageView{Page: p, Rights: sp.Rights, Path: []Crumb{}, Children: []Crumb{}, Reviewers: []Person{}, Approvers: []Person{}}
	// Percorso dalla radice (al massimo 20 livelli).
	rows, err := tx.Query(ctx, `
		with recursive up(id, parent_id, depth) as (
			select e.id, e.parent_id, 0 from elements e where e.id = $1
			union all
			select p.id, p.parent_id, up.depth + 1 from elements p join up on p.id = up.parent_id
			where up.depth < 20 and p.deleted_at is null
		)
		select s.key || '-' || e.number, e.title from up
		join elements e on e.id = up.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where up.depth > 0 order by up.depth desc`, el.ID)
	if err != nil {
		return nil, err
	}
	if v.Path, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Crumb]); err != nil {
		return nil, err
	}
	if v.Path == nil {
		v.Path = []Crumb{}
	}
	rows, err = tx.Query(ctx, `select s.key || '-' || e.number, e.title from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where e.parent_id = $1 and e.deleted_at is null and not e.is_draft order by e.rank collate "C" nulls last, e.number limit 500`, el.ID)
	if err != nil {
		return nil, err
	}
	if v.Children, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Crumb]); err != nil {
		return nil, err
	}
	if v.Children == nil {
		v.Children = []Crumb{}
	}
	if v.Lock, err = activeLock(ctx, tx, el.ID, actor.MemberID); err != nil {
		return nil, err
	}
	if k.approvedVersion != nil && *k.approvedVersion != k.version {
		if v.Approved, err = s.version(ctx, tx, el.ID, *k.approvedVersion, k); err != nil {
			return nil, err
		}
	}
	people := func(role string) ([]Person, error) {
		rows, err := tx.Query(ctx, `select coalesce(sh.member_id::text, sh.group_id::text), coalesce(m.display_name, g.name, '')
			from stakeholders sh
			join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = $2
			left join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id
			left join groups g on g.tenant_id = sh.tenant_id and g.id = sh.group_id
			where sh.element_id = $1 order by 2`, el.ID, role)
		if err != nil {
			return nil, err
		}
		out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Person])
		if out == nil {
			out = []Person{}
		}
		return out, err
	}
	if v.Reviewers, err = people(RoleReviewer); err != nil {
		return nil, err
	}
	if v.Approvers, err = people(RoleApprover); err != nil {
		return nil, err
	}
	links, err := s.eng.ElementLinks(ctx, tx, actor, el)
	if err != nil {
		return nil, err
	}
	v.Links = []engine.LinkedElement{}
	for _, l := range links.Relations {
		if l.Relation == "documenta" || l.Relation == "" {
			v.Links = append(v.Links, l)
		}
	}
	return v, nil
}

// CreateInput crea una pagina.
type CreateInput struct {
	Title    string `json:"title"`
	Parent   string `json:"parent"`   // chiave o id della pagina genitore (vuoto = radice)
	Template string `json:"template"` // chiave o id del template
	Body     string `json:"body"`
	Note     string `json:"note"`
	After    string `json:"after"` // posizione tra i fratelli (vuoto = in fondo)
	Before   string `json:"before"`
}

// CreatePage crea una pagina nello spazio: versione 1, eventualmente da un template (che può
// renderla subito documento controllato).
func (s *Service) CreatePage(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string, in CreateInput) (*PageView, error) {
	sp, err := s.readableSpace(ctx, tx, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Write {
		return nil, engine.ErrForbidden
	}
	var parentID string
	if in.Parent != "" {
		parent, err := s.eng.Get(ctx, tx, actor, in.Parent)
		if err != nil || parent.TypeKey != TypePage || parent.SpaceID != sp.ID {
			return nil, engine.NotFound("parent", "page", "la pagina genitore non esiste in questo spazio")
		}
		parentID = parent.ID
	}
	body := in.Body
	var tpl *Template
	if in.Template != "" {
		if tpl, err = templateFor(ctx, tx, sp.ID, in.Template); err != nil {
			return nil, err
		}
		if strings.TrimSpace(body) == "" {
			var author string
			_ = tx.QueryRow(ctx, `select display_name from members where id::text = $1`, actor.MemberID).Scan(&author)
			body = fill(tpl.Body, strings.TrimSpace(in.Title), author, time.Now())
		}
	}
	body, ok := cleanBody(body)
	if !ok {
		return nil, engine.InvalidValue("body", "testo non valido o troppo lungo")
	}
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: sp.Key, TypeKey: TypePage, Title: in.Title,
		BodyText: PlainText(body), ParentID: parentID, DeferStart: true})
	if err != nil {
		return nil, err
	}
	rank, err := placeAmong(ctx, tx, sp.ID, el.ID, parentID, in.After, in.Before)
	if err != nil {
		return nil, err
	}
	mentions, err := collab.ResolveMentions(ctx, tx, body)
	if err != nil {
		return nil, err
	}
	pb := pageBody{Format: "markdown", MD: body, Mentions: mentions}
	// Una pagina semplice non ha fase: lo stato iniziale del tipo vale solo per i documenti
	// controllati, che lo ricevono da StartProcess.
	if _, err := tx.Exec(ctx, `update elements set body = $2, rank = $3, state_id = null where id = $1`, el.ID, pb, rank); err != nil {
		return nil, err
	}
	var tplKey any
	controlled := false
	var reviewMonths *int
	if tpl != nil {
		tplKey, controlled, reviewMonths = tpl.Key, tpl.Controlled, tpl.ReviewMonths
	}
	if _, err := tx.Exec(ctx, `insert into kb_pages (tenant_id, element_id, version, template_key, controlled, review_months)
		values (app_tenant(), $1, 1, $2, $3, $4)`, el.ID, tplKey, controlled, reviewMonths); err != nil {
		return nil, err
	}
	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = "Prima versione"
	}
	if _, err := tx.Exec(ctx, `insert into page_versions (tenant_id, element_id, version, title, body, author_id, note)
		values (app_tenant(), $1, 1, $2, $3, $4, $5)`, el.ID, el.Title, pb, nullable(actor.MemberID), note); err != nil {
		return nil, err
	}
	if err := s.syncRefs(ctx, tx, el.ID, body); err != nil {
		return nil, err
	}
	if err := notifyMentions(ctx, tx, actor, el.ID, nil, mentions, body); err != nil {
		return nil, err
	}
	if controlled {
		if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
			return nil, err
		}
	}
	return s.GetPage(ctx, tx, actor, el.ID)
}

// SaveInput salva una nuova versione.
type SaveInput struct {
	Title       *string `json:"title"`
	Body        string  `json:"body"`
	Note        string  `json:"note"`
	BaseVersion int     `json:"base_version"` // versione da cui è partito l'editor
}

// SavePage salva il testo come nuova versione (chi, quando, nota). Rifiuta il salvataggio se
// un'altra persona tiene il blocco o se la pagina è cambiata dall'apertura dell'editor. Un
// documento approvato torna in bozza: la versione approvata resta in vigore e consultabile.
func (s *Service) SavePage(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in SaveInput) (*PageView, error) {
	el, sp, k, err := s.loadPage(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Write {
		return nil, engine.ErrForbidden
	}
	if err := s.save(ctx, tx, actor, el, k, in, "saved"); err != nil {
		return nil, err
	}
	return s.GetPage(ctx, tx, actor, el.ID)
}

func (s *Service) save(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, k *kbRow, in SaveInput, kind string) error {
	if l, err := activeLock(ctx, tx, el.ID, actor.MemberID); err != nil {
		return err
	} else if l != nil && !l.Mine {
		return &LockedError{Lock: l}
	}
	if in.BaseVersion != k.version {
		return ErrStale
	}
	if k.controlled && el.StateCategory == "in_progress" {
		return engine.InvalidCode("validation.knowledge.page_in_review", "body", "il documento è in revisione: si modifica dopo l'esito (o ritirandolo dalla revisione)")
	}
	if k.controlled && el.StateCategory == "cancelled" {
		return engine.InvalidCode("validation.knowledge.page_obsolete", "body", "il documento è obsoleto e non si modifica più")
	}
	body, ok := cleanBody(in.Body)
	if !ok {
		return engine.InvalidValue("body", "testo non valido o troppo lungo")
	}
	title := el.Title
	if in.Title != nil {
		title = strings.TrimSpace(*in.Title)
		if title == "" || len(title) > 500 {
			return engine.TooLong("title", 500, "titolo obbligatorio, massimo 500 caratteri")
		}
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `select body from elements where id = $1`, el.ID).Scan(&raw); err != nil {
		return err
	}
	prev := readBody(raw)
	if body == prev.MD && title == el.Title {
		return nil // niente da salvare: nessuna versione vuota
	}
	mentions, err := collab.ResolveMentions(ctx, tx, body)
	if err != nil {
		return err
	}
	pb := pageBody{Format: "markdown", MD: body, Mentions: mentions}
	n := k.version + 1
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > 500 {
		return engine.TooLong("note", 500, "nota troppo lunga (massimo 500 caratteri)")
	}
	if _, err := tx.Exec(ctx, `update elements set title = $2, body = $3, body_text = $4, version = version + 1, updated_by = $5 where id = $1`,
		el.ID, title, pb, PlainText(body), nullable(actor.MemberID)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update kb_pages set version = $2 where element_id = $1`, el.ID, n); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into page_versions (tenant_id, element_id, version, title, body, author_id, note)
		values (app_tenant(), $1, $2, $3, $4, $5, $6)`, el.ID, n, title, pb, nullable(actor.MemberID), note); err != nil {
		return err
	}
	if err := s.syncRefs(ctx, tx, el.ID, body); err != nil {
		return err
	}
	if err := reanchor(ctx, tx, el.ID, body, n); err != nil {
		return err
	}
	if err := notifyMentions(ctx, tx, actor, el.ID, prev.Mentions, mentions, body); err != nil {
		return err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "page", kind, map[string]any{"version": n, "note": note}, nil); err != nil {
		return err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "page." + strings.TrimSuffix(kind, "d"), EntityType: TypePage, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"version": n}}); err != nil {
		return err
	}
	k.version = n
	// Documento approvato modificato: si lavora alla versione successiva, quella approvata
	// resta in vigore finché la nuova non è approvata.
	if k.controlled && el.StateCategory == "done" {
		if _, err := s.eng.Transition(ctx, tx, actor, el.ID, initialPhase, "Nuova versione in lavorazione", engine.TransitionOpts{}); err != nil {
			return err
		}
	}
	return nil
}

// initialPhase è la fase di bozza del processo di partenza.
const initialPhase = "Bozza"

// notifyMentions avvisa chi è menzionato per la prima volta nel testo.
func notifyMentions(ctx context.Context, tx pgx.Tx, actor engine.Actor, pageID string, before, after []collab.Mention, body string) error {
	old := map[string]bool{}
	for _, m := range before {
		old[m.ID] = true
	}
	var added []string
	for _, m := range after {
		if !old[m.ID] {
			added = append(added, m.ID)
		}
	}
	if len(added) == 0 {
		return nil
	}
	_, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindMention, ElementID: pageID, ActorID: actor.MemberID,
		Members: added, Payload: map[string]any{"excerpt": collab.Excerpt(PlainText(body), 160), "page": true}})
	return err
}

// syncRefs aggiorna gli elementi citati nel testo (kb_refs).
func (s *Service) syncRefs(ctx context.Context, tx pgx.Tx, pageID, body string) error {
	keys := CitedKeys(body)
	if _, err := tx.Exec(ctx, `delete from kb_refs where page_id = $1`, pageID); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `insert into kb_refs (tenant_id, page_id, target_id)
		select app_tenant(), $1, e.id from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where s.key || '-' || e.number = any($2) and e.id <> $1 and e.deleted_at is null
		on conflict do nothing`, pageID, keys)
	return err
}

// MoveInput sposta una pagina nell'albero.
type MoveInput struct {
	Parent *string `json:"parent"` // nuovo genitore (vuoto = radice); assente = stesso genitore
	After  string  `json:"after"`
	Before string  `json:"before"`
}

// MovePage cambia genitore e posizione tra i fratelli (una riga scritta).
func (s *Service) MovePage(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in MoveInput) (*PageView, error) {
	el, sp, _, err := s.loadPage(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Write {
		return nil, engine.ErrForbidden
	}
	parentID := deref(el.ParentID)
	if in.Parent != nil {
		parentID = ""
		if *in.Parent != "" {
			parent, err := s.eng.Get(ctx, tx, actor, *in.Parent)
			if err != nil || parent.TypeKey != TypePage || parent.SpaceID != sp.ID {
				return nil, engine.NotFound("parent", "page", "la pagina genitore non esiste in questo spazio")
			}
			if parent.ID == el.ID {
				return nil, engine.InvalidCode("validation.knowledge.page_parent_self", "parent", "una pagina non può stare dentro sé stessa")
			}
			var cycle bool
			if err := tx.QueryRow(ctx, `
				with recursive up(id, depth) as (
					select parent_id, 0 from elements where id = $1
					union all
					select e.parent_id, up.depth + 1 from elements e join up on e.id = up.id where up.depth < 50
				)
				select exists (select 1 from up where id = $2)`, parent.ID, el.ID).Scan(&cycle); err != nil {
				return nil, err
			}
			if cycle {
				return nil, engine.InvalidCode("validation.knowledge.page_parent_descendant", "parent", "una pagina non può stare dentro una sua sottopagina")
			}
			parentID = parent.ID
		}
	}
	rank, err := placeAmong(ctx, tx, sp.ID, el.ID, parentID, in.After, in.Before)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update elements set parent_id = $2, rank = $3 where id = $1`, el.ID, nullable(parentID), rank); err != nil {
		return nil, err
	}
	if parentID != deref(el.ParentID) {
		if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "page", "moved", map[string]any{"parent_id": parentID}, map[string]any{"parent_id": deref(el.ParentID)}); err != nil {
			return nil, err
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "page.move", EntityType: TypePage, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"parent_id": parentID}}); err != nil {
		return nil, err
	}
	return s.GetPage(ctx, tx, actor, el.ID)
}

// DeletePage elimina (logicamente) una pagina; le sottopagine salgono al genitore. Un
// documento controllato già approvato non si elimina: si rende obsoleto (è un'evidenza).
func (s *Service) DeletePage(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) error {
	el, sp, k, err := s.loadPage(ctx, tx, actor, ref, true)
	if err != nil {
		return err
	}
	if !sp.Rights.Write {
		return engine.ErrForbidden
	}
	if k.controlled && k.approvedVersion != nil {
		return engine.InvalidCode("validation.knowledge.controlled_delete", "page", "un documento controllato approvato non si elimina: rendilo obsoleto")
	}
	if l, err := activeLock(ctx, tx, el.ID, actor.MemberID); err != nil {
		return err
	} else if l != nil && !l.Mine {
		return &LockedError{Lock: l}
	}
	if _, err := tx.Exec(ctx, `update elements set parent_id = $2 where parent_id = $1 and deleted_at is null`, el.ID, el.ParentID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update elements set deleted_at = now(), updated_by = $2 where id = $1`, el.ID, nullable(actor.MemberID)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from page_locks where element_id = $1`, el.ID); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "page.delete", EntityType: TypePage, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"space_id": sp.ID}})
}

// Recent sono le pagine aggiornate di recente negli spazi leggibili.
type Recent struct {
	Key           string    `json:"key"`
	Title         string    `json:"title"`
	SpaceKey      string    `json:"space_key"`
	SpaceName     string    `json:"space_name"`
	State         string    `json:"state"`
	StateCategory string    `json:"state_category"`
	Controlled    bool      `json:"controlled"`
	UpdatedAt     time.Time `json:"updated_at"`
	UpdatedBy     string    `json:"updated_by"`
}

// Home è la pagina «Documenti»: spazi, pagine recenti, revisioni in scadenza.
type Home struct {
	Spaces      []Space  `json:"spaces"`
	Recent      []Recent `json:"recent"`
	ReviewDue   []Recent `json:"review_due"`
	CanCreate   bool     `json:"can_create"`
	ToReviewFor []Recent `json:"to_review"` // documenti in revisione in cui sono revisore o approvatore
	// Indicatori (indicators.go): revisioni scadute, commenti in linea aperti, campagne di lettura.
	Indicators  Indicators    `json:"indicators"`
	OpenThreads []OpenThread  `json:"open_threads"`
	MyReadings  []MyReading   `json:"my_readings"`
	Campaigns   []CampaignRow `json:"campaigns"`
}

// Home raccoglie spazi e pagine recenti dell'attore.
func (s *Service) Home(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Home, error) {
	spaces, err := s.Spaces(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	h := &Home{Spaces: spaces, Recent: []Recent{}, ReviewDue: []Recent{}, ToReviewFor: []Recent{},
		OpenThreads: []OpenThread{}, MyReadings: []MyReading{}, Campaigns: []CampaignRow{},
		CanCreate: actor.Has("element.write") || actor.Has("tenant.admin")}
	var ids []string
	for _, sp := range spaces {
		if sp.Rights.Read {
			ids = append(ids, sp.ID)
		}
	}
	if len(ids) == 0 {
		return h, nil
	}
	if err := s.indicators(ctx, tx, actor, ids, h); err != nil {
		return nil, err
	}
	list := func(where, order string, limit int, args ...any) ([]Recent, error) {
		rows, err := tx.Query(ctx, `
			select s.key || '-' || e.number, e.title, s.key, s.name, coalesce(o.name, ''), coalesce(o.category, ''),
			       coalesce(k.controlled, false), e.updated_at, coalesce(m.display_name, '')
			from elements e
			join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'page'
			left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			left join kb_pages k on k.tenant_id = e.tenant_id and k.element_id = e.id
			left join members m on m.tenant_id = e.tenant_id and m.id = e.updated_by
			where e.space_id = any($1::uuid[]) and e.deleted_at is null and not e.is_draft and `+where+`
			order by `+order+` limit `+strconv.Itoa(limit), append([]any{ids}, args...)...)
		if err != nil {
			return nil, err
		}
		out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Recent])
		if out == nil {
			out = []Recent{}
		}
		return out, err
	}
	if h.Recent, err = list("true", "e.updated_at desc", 15); err != nil {
		return nil, err
	}
	if h.ReviewDue, err = list("k.controlled and k.next_review <= current_date + 30", "k.next_review", 10); err != nil {
		return nil, err
	}
	if actor.MemberID != "" {
		if h.ToReviewFor, err = list(`o.category = 'in_progress' and exists (select 1 from stakeholders sh
			join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key in ('doc_reviewer', 'doc_approver')
			where sh.element_id = e.id and (sh.member_id = $2 or sh.group_id in (select member_groups($2))))`,
			"e.updated_at desc", 10, actor.MemberID); err != nil {
			return nil, err
		}
	}
	return h, nil
}
