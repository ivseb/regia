package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Element è la vista di un elemento restituita dal motore.
type Element struct {
	ID            string         `json:"id"`
	Key           string         `json:"key"` // es. DEM-12; vuota per le bozze
	SpaceID       string         `json:"space_id"`
	SpaceKey      string         `json:"space_key"`
	TypeID        string         `json:"type_id"`
	TypeKey       string         `json:"type"`
	TypeName      string         `json:"type_name"`
	Number        *int64         `json:"number"`
	Title         string         `json:"title"`
	BodyText      string         `json:"body_text"`
	StateID       *string        `json:"state_id"`
	State         string         `json:"state"`
	StateCategory string         `json:"state_category"`
	AssigneeID    *string        `json:"assignee_id"`
	ReporterID    *string        `json:"reporter_id"`
	ParentID      *string        `json:"parent_id"`
	Fields        map[string]any `json:"fields"`
	Version       int            `json:"version"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	ResolvedAt    *time.Time     `json:"resolved_at"`
	// ProcessVersionID: versione del processo su cui si trova l'elemento (nil = tipo senza processo).
	ProcessVersionID *string `json:"process_version_id"`
	// StartAt e DueAt: finestra pianificata (giorni interi), usata dal Gantt e dalla roadmap.
	StartAt *time.Time `json:"start_at"`
	DueAt   *time.Time `json:"due_at"`
	// Tags: etichette dell'elemento, solo nelle liste (la ricerca le aggiunge con AttachTags).
	Tags []Tag `json:"tags,omitempty"`
}

// elementColumns ed elementFrom compongono selectElement; la ricerca vi aggiunge la
// colonna del valore di ordinamento (query_plan.go).
const elementColumns = `
	e.id, coalesce(s.key || '-' || e.number, ''), s.id, s.key, t.id, t.key, t.name, e.number,
	       e.title, e.body_text, e.state_id, coalesce(o.name, ''), coalesce(o.category, ''),
	       e.assignee_id, e.reporter_id, e.parent_id, e.fields, e.version, e.created_at, e.updated_at, e.resolved_at, e.process_version_id, e.start_at, e.due_at`

const elementFrom = `
	from elements e
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id`

const selectElement = `select` + elementColumns + elementFrom

func scanElement(row pgx.Row) (*Element, error) {
	return scanElementWith(row)
}

// scanElementWith legge le colonne di selectElement seguite da eventuali colonne extra.
func scanElementWith(row pgx.Row, extra ...any) (*Element, error) {
	el := &Element{}
	var fields []byte
	dest := []any{&el.ID, &el.Key, &el.SpaceID, &el.SpaceKey, &el.TypeID, &el.TypeKey, &el.TypeName, &el.Number,
		&el.Title, &el.BodyText, &el.StateID, &el.State, &el.StateCategory,
		&el.AssigneeID, &el.ReporterID, &el.ParentID, &fields, &el.Version, &el.CreatedAt, &el.UpdatedAt, &el.ResolvedAt, &el.ProcessVersionID, &el.StartAt, &el.DueAt}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	el.Fields = map[string]any{}
	if len(fields) > 0 {
		if err := json.Unmarshal(fields, &el.Fields); err != nil {
			return nil, err
		}
	}
	return el, nil
}

var keyRe = regexp.MustCompile(`^([A-Z][A-Z0-9_]{0,19})-([1-9][0-9]{0,17})$`)
var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Get trova un elemento per chiave leggibile (DEM-12) o per id. Le bozze sono
// visibili solo al proprietario; gli elementi cancellati non si vedono.
func (s *Service) Get(ctx context.Context, tx pgx.Tx, actor Actor, ref string) (*Element, error) {
	var row pgx.Row
	if m := keyRe.FindStringSubmatch(strings.ToUpper(ref)); m != nil {
		n, _ := strconv.ParseInt(m[2], 10, 64)
		row = tx.QueryRow(ctx, selectElement+` where s.key = $1 and e.number = $2 and e.deleted_at is null`, m[1], n)
	} else if uuidRe.MatchString(strings.ToLower(ref)) {
		// Senza membro (i job di sistema, es. le scadenze delle deroghe) non si vedono bozze.
		member := actor.MemberID
		if member == "" {
			member = "00000000-0000-0000-0000-000000000000"
		}
		row = tx.QueryRow(ctx, selectElement+` where e.id = $1 and e.deleted_at is null
			and (not e.is_draft or e.draft_owner_id = $2)`, strings.ToLower(ref), member)
	} else if id := s.catalogCodeID(ctx, tx, ref); id != "" {
		// Codice del catalogo di architettura (APP-MOB): vale come chiave dell'elemento.
		row = tx.QueryRow(ctx, selectElement+` where e.id = $1 and e.deleted_at is null and not e.is_draft`, id)
	} else {
		return nil, ErrNotFound
	}
	el, err := scanElement(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if ok, err := s.CanRead(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNotFound // non si rivela l'esistenza
	}
	return el, nil
}

var catalogCodeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{1,59}$`)

// catalogCodeID trova l'elemento il cui codice di catalogo (applicazione o capability, es.
// APP-MOB) è quello indicato. Vuoto se non esiste. Il codice di catalogo è in evidenza nelle
// viste di architettura e vale in link e ricerca, come la chiave dell'elemento (ARC-8).
func (s *Service) catalogCodeID(ctx context.Context, tx pgx.Tx, code string) string {
	code = strings.TrimSpace(code)
	if !catalogCodeRe.MatchString(code) {
		return ""
	}
	var id string
	err := tx.QueryRow(ctx, `select element_id::text from (
		select a.element_id, a.code from ext_application a union all select c.element_id, c.code from ext_capability c) x
		where lower(x.code) = lower($1) limit 1`, code).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}

// CanRead: con element.read si legge tutto il tenant (la visibilità per gruppi
// arriverà con element_visibility); il richiedente del portale vede solo gli
// elementi in cui è segnalatore o stakeholder.
func (s *Service) CanRead(ctx context.Context, tx pgx.Tx, actor Actor, elementID string) (bool, error) {
	// Accesso per spazio (space_access.go): uno spazio riservato decide da solo, un ruolo
	// sullo spazio concede la lettura anche senza element.read.
	if ok, decided, err := s.readBySpace(ctx, tx, actor, elementID); err != nil || decided {
		return ok, err
	}
	if actor.Has("element.read") {
		return true, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, `select exists (select 1 from elements where id = $1 and reporter_id = $2)
		or `+stakeholderOf("$1", "$2"), elementID, actor.MemberID).Scan(&ok)
	return ok, err
}

// CreateInput descrive un nuovo elemento.
type CreateInput struct {
	SpaceKey   string
	TypeKey    string
	Title      string
	BodyText   string
	Fields     map[string]any
	AssigneeID string
	ParentID   string
	// DeferStart: il modulo scrive prima i suoi dati e poi chiama StartProcess, così la
	// scelta del processo e le regole di creazione vedono tutti i fatti.
	DeferStart bool
}

// Create crea un elemento pubblicato (con numero) nello stato iniziale del workflow.
func (s *Service) Create(ctx context.Context, tx pgx.Tx, actor Actor, in CreateInput) (*Element, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return nil, invalidCode("validation.title_required", "title", "il titolo è obbligatorio")
	}
	if len(in.Title) > 500 {
		return nil, TooLong("title", 500, "titolo troppo lungo (massimo 500 caratteri)")
	}
	t, err := LoadType(ctx, tx, in.TypeKey)
	if err != nil {
		return nil, err
	}
	var archived bool
	if err := tx.QueryRow(ctx, "select archived_at is not null from element_types where id = $1", t.ID).Scan(&archived); err != nil {
		return nil, err
	}
	if archived {
		return nil, invalid("type", "il tipo %s è archiviato: non si possono creare elementi nuovi", t.Name)
	}
	// Numerazione: lock di riga sullo spazio, il numero non si ripete mai. Prima il lock dell'audit
	// (lo stesso ordine di chi ha già scritto un evento nella transazione): senza, due creazioni
	// concorrenti possono andare in deadlock (una tiene la riga e aspetta l'audit, l'altra il contrario).
	if err := audit.LockChain(ctx, tx); err != nil {
		return nil, err
	}
	var spaceID string
	var number int64
	err = tx.QueryRow(ctx, `
		update spaces set next_number = next_number + 1
		where key = $1 and not archived
		  and exists (select 1 from space_types st where st.space_id = spaces.id and st.type_id = $2)
		returning id, next_number - 1`, in.SpaceKey, t.ID).Scan(&spaceID, &number)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, NotFound("space", "space", "spazio inesistente o tipo non ammesso nello spazio")
	}
	if err != nil {
		return nil, err
	}
	var stateID *string
	if t.Workflow.Initial != "" {
		st, ok := t.state(t.Workflow.Initial)
		if !ok {
			return nil, fmt.Errorf("workflow del tipo %s: stato iniziale %q inesistente", t.Key, t.Workflow.Initial)
		}
		stateID = &st.ID
	}
	defs, err := loadTypeFields(ctx, tx, t.ID)
	if err != nil {
		return nil, err
	}
	fields, rows, err := normalize(ctx, tx, defs, nil, in.Fields, false)
	if err != nil {
		return nil, err
	}
	id := db.NewID()
	_, err = tx.Exec(ctx, `
		insert into elements (tenant_id, id, space_id, type_id, number, title, body_text, state_id,
		                      assignee_id, reporter_id, parent_id, fields, created_by, updated_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $9, $9)`,
		id, spaceID, t.ID, number, in.Title, in.BodyText, stateID,
		nullable(in.AssigneeID), actor.MemberID, nullable(in.ParentID), fields)
	if err != nil {
		return nil, err
	}
	if err := syncValues(ctx, tx, id, defs, rows); err != nil {
		return nil, err
	}
	if err := addActivity(ctx, tx, id, actor.MemberID, "created", "", map[string]any{"type": t.Key, "state": t.Workflow.Initial}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{
		Action: "element.create", EntityType: t.Key, EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"space": in.SpaceKey, "number": number},
	}); err != nil {
		return nil, err
	}
	el, err := s.Get(ctx, tx, Actor{MemberID: actor.MemberID, Permissions: []string{"element.read"}}, id)
	if err != nil || in.DeferStart {
		return el, err
	}
	return s.StartProcess(ctx, tx, actor, el)
}

// UpdateInput: solo i campi valorizzati cambiano. Version è obbligatoria (lock ottimistico).
type UpdateInput struct {
	Version  int
	Title    *string
	BodyText *string
	Fields   map[string]any
}

func (s *Service) Update(ctx context.Context, tx pgx.Tx, actor Actor, elementID string, in UpdateInput) (*Element, error) {
	el, err := s.lockForWrite(ctx, tx, actor, elementID)
	if err != nil {
		return nil, err
	}
	if ok, err := s.writeBySpace(ctx, tx, actor, el); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrForbidden
	}
	if el.Version != in.Version {
		return nil, ErrConflict
	}
	changes := map[string]any{}
	title, body := el.Title, el.BodyText
	if in.Title != nil && strings.TrimSpace(*in.Title) != el.Title {
		title = strings.TrimSpace(*in.Title)
		if title == "" || len(title) > 500 {
			return nil, TooLong("title", 500, "titolo obbligatorio, massimo 500 caratteri")
		}
		changes["title"] = [2]string{el.Title, title}
	}
	if in.BodyText != nil && *in.BodyText != el.BodyText {
		body = *in.BodyText
		changes["body"] = true
	}
	fields := el.Fields
	if len(in.Fields) > 0 {
		defs, err := loadTypeFields(ctx, tx, el.TypeID)
		if err != nil {
			return nil, err
		}
		var rows []valueRow
		fields, rows, err = normalize(ctx, tx, defs, el.Fields, in.Fields, true)
		if err != nil {
			return nil, err
		}
		if err := s.checkRequiredKept(ctx, tx, el, defs, in.Fields, fields); err != nil {
			return nil, err
		}
		if err := syncValues(ctx, tx, el.ID, defs, rows); err != nil {
			return nil, err
		}
		for k := range in.Fields {
			changes["fields."+k] = true
		}
	}
	if len(changes) == 0 {
		return el, nil
	}
	if _, err := tx.Exec(ctx, `update elements set title = $2, body_text = $3, fields = $4, version = version + 1, updated_by = $5
		where id = $1`, el.ID, title, body, fields, actor.MemberID); err != nil {
		return nil, err
	}
	for k := range changes {
		var added, removed any
		if k == "title" {
			removed, added = el.Title, title
		} else if strings.HasPrefix(k, "fields.") {
			f := strings.TrimPrefix(k, "fields.")
			removed, added = el.Fields[f], fields[f]
		}
		if err := addActivity(ctx, tx, el.ID, actor.MemberID, "field", k, added, removed); err != nil {
			return nil, err
		}
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "element.update", EntityType: el.TypeKey, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"changed": keys(changes)}}); err != nil {
		return nil, err
	}
	return s.Get(ctx, tx, actor, el.ID)
}

// checkRequiredKept rifiuta di svuotare un campo obbligatorio nella fase attuale
// (sempre obbligatorio, oppure obbligatorio da una fase già raggiunta, e visibile).
func (s *Service) checkRequiredKept(ctx context.Context, tx pgx.Tx, el *Element, defs []FieldDef, in, after map[string]any) error {
	var states []State
	var facts Facts
	for _, d := range defs {
		if _, touched := in[d.Key]; !touched || !isEmptyValue(after[d.Key]) || isEmptyValue(el.Fields[d.Key]) {
			continue
		}
		if !d.Required && d.RequiredFrom == "" {
			continue
		}
		if states == nil {
			fl, err := s.flowOf(ctx, tx, el)
			if err != nil {
				return err
			}
			states = fl.states
			if states == nil {
				states = []State{}
			}
		}
		if !d.requiredIn(states, el.State) {
			continue
		}
		if d.VisibleWhen != "" {
			if facts == nil {
				f, err := s.ElementFacts(ctx, tx, el)
				if err != nil {
					return err
				}
				facts = f
			}
			if !d.visibleWith(facts) {
				continue
			}
		}
		if el.State != "" {
			return Required(d.Key, "«%s» è obbligatorio nella fase «%s»: non si può svuotare", d.Name, el.State)
		}
		return Required(d.Key, "«%s» è obbligatorio: non si può svuotare", d.Name)
	}
	return nil
}

// lockForWrite carica e blocca la riga dell'elemento, verificando lettura e scrittura.
func (s *Service) lockForWrite(ctx context.Context, tx pgx.Tx, actor Actor, elementID string) (*Element, error) {
	el, err := scanElement(tx.QueryRow(ctx, selectElement+` where e.id = $1 and e.deleted_at is null for update of e`, elementID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if ok, err := s.CanRead(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNotFound
	}
	return el, nil
}

// canWrite: con element.write si modifica; il segnalatore può correggere la propria
// richiesta finché è in uno stato iniziale (categoria "todo").
func canWrite(actor Actor, el *Element) bool {
	if actor.Has("element.write") {
		return true
	}
	return el.ReporterID != nil && *el.ReporterID == actor.MemberID && el.StateCategory == "todo"
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
