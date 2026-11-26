package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/architecture"
	"regia/internal/modules/knowledge"
	"regia/internal/modules/work"
	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// Modello «persona sola / home lab»: la configurazione che il pilota ha fatto a mano in una trentina
// di chiamate, in un passo e in una sola transazione. Moduli, tipi (Attività, Bug, Idea), un flusso
// semplice uguale per i tre tipi, solo il campo priorità, un team con la sua board kanban.
// Si può riapplicare: ciò che è già come deve essere non si tocca né si duplica.

const personaKey = "persona_sola"

// avvioParams sono i parametri facoltativi di un modello (oggi solo di persona_sola).
type avvioParams struct {
	BoardKey string `json:"board_key"` // chiave dello spazio e della board (HL)
	TeamName string `json:"team_name"` // nome del team (Home lab)
}

const (
	personaDefaultKey  = "HL"
	personaDefaultTeam = "Home lab"
)

var personaBoardKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,19}$`)

var (
	// Tipi che restano in uso e tipi di partenza che si archiviano se non hanno elementi.
	personaTypes   = []string{"task", "bug", "idea"}
	personaRetired = []string{"story", "epic", "sprint"}
	// Moduli accesi e spenti: Architettura serve al catalogo delle applicazioni del connettore.
	personaOn  = []string{"work", "devsecops", "knowledge", "architecture"}
	personaOff = []string{"demand", "grc"}
)

// personaFlow è il flusso dei tre tipi: come il pacchetto di partenza del task, senza «In revisione»
// e senza il gate sull'assegnatario.
const personaFlow = `{
  "phases": [
    {"name": "Da fare", "category": "todo", "color": "#6B7280"},
    {"name": "In corso", "category": "in_progress", "color": "#3550C8"},
    {"name": "Fatto", "category": "done", "color": "#1F2937"},
    {"name": "Annullato", "category": "cancelled", "color": "#6B7280", "off_path": true}
  ],
  "initial": "Da fare",
  "transitions": [
    {"from": ["Da fare"], "to": "In corso", "label": "Inizia"},
    {"from": ["In corso"], "to": "Fatto", "label": "Chiudi"},
    {"from": ["In corso"], "to": "Da fare", "label": "Rimetti da fare"},
    {"from": ["Fatto"], "to": "In corso", "label": "Riapri"},
    {"from": ["Da fare", "In corso"], "to": "Annullato", "label": "Annulla"},
    {"from": ["Annullato"], "to": "Da fare", "label": "Ripristina"}
  ]
}`

const personaNotes = "Persona sola / home lab: Da fare, In corso, Fatto"

var personaPhaseNames = []string{"Da fare", "In corso", "Fatto", "Annullato"}

// avvioParamsOf legge i parametri: dalla query nell'anteprima (GET), dal corpo, se c'è, nell'applicazione.
func avvioParamsOf(w http.ResponseWriter, r *http.Request) (avvioParams, bool) {
	var par avvioParams
	if r.Method == http.MethodGet {
		par.BoardKey, par.TeamName = r.URL.Query().Get("board_key"), r.URL.Query().Get("team_name")
		return par, true
	}
	if r.Body == nil || r.ContentLength == 0 {
		return par, true
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_json", "corpo della richiesta non leggibile")
		return par, false
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return par, true
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&par); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_json", fmt.Sprintf("JSON non valido: %v", err))
		return par, false
	}
	return par, true
}

func personaState(was bool) string {
	if was {
		return "present"
	}
	return "new"
}

func personaAudit(ctx context.Context, tx pgx.Tx, actor engine.Actor, typeKey string) error {
	return audit.Append(ctx, tx, audit.Event{Action: "type.update", EntityType: "element_type", ActorAccountID: actor.AccountID,
		ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"type": typeKey, "changed": []string{"archived"}, "model": personaKey}})
}

// runPersona esegue il modello e restituisce l'elenco di ciò che nasce e di ciò che c'era già.
func (s *Server) runPersona(ctx context.Context, tx pgx.Tx, p *auth.Principal, actor engine.Actor, par avvioParams, lang string) ([]avvioItem, error) {
	before, err := takeSnap(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, prov := range []provisionFn{architecture.Provision, work.Provision, knowledge.Provision} {
		if err := prov(ctx, tx, p.MemberID); err != nil {
			return nil, err
		}
	}
	adm := actor
	adm.Permissions = append(slices.Clone(actor.Permissions), engine.PermProcessAdmin, "tenant.admin")
	var items []avvioItem

	// Moduli.
	for _, mod := range personaOn {
		if def, ok := moduleByKey(mod); ok {
			items = append(items, avvioItem{Kind: "module", Key: mod, Name: def.Name, State: personaState(!before.modules[mod])})
		}
	}
	for _, mod := range personaOff {
		if def, ok := moduleByKey(mod); ok {
			items = append(items, avvioItem{Kind: "module", Key: mod, Name: def.Name, State: personaState(before.modules[mod]), Off: true})
		}
	}
	for mod, on := range map[string]bool{"work": true, "devsecops": true, "knowledge": true, "architecture": true, "demand": false, "grc": false} {
		if _, err := tx.Exec(ctx, `insert into tenant_modules (tenant_id, module, enabled, updated_by) values (app_tenant(), $1, $3, $2)
			on conflict (tenant_id, module) do update set enabled = excluded.enabled, updated_by = excluded.updated_by, updated_at = now()`, mod, p.MemberID, on); err != nil {
			return nil, err
		}
	}

	// Tipi: Task diventa «Attività» (l'inglese resta «Task»).
	tag, err := tx.Exec(ctx, `update element_types set name = 'Attività', plural_name = 'Attività',
		labels = labels || jsonb_build_object('en', jsonb_build_object('name', 'Task', 'plural', 'Tasks')), updated_at = now()
		where key = 'task' and name in ('Task', 'Attività') and (name = 'Task' or not (labels ? 'en'))`)
	if err != nil {
		return nil, err
	}
	items = append(items, avvioItem{Kind: "type", Key: "task", Name: "Attività", State: personaState(tag.RowsAffected() == 0)})
	var bugName string
	if err := tx.QueryRow(ctx, `select name from element_types where key = 'bug'`).Scan(&bugName); err != nil {
		return nil, err
	}
	items = append(items, avvioItem{Kind: "type", Key: "bug", Name: bugName, State: "present"})

	// Idea: tipo nuovo del modulo work copiato dall'Attività (fasi, campi e spazi); se esiste archiviato si ripristina.
	ideaNew := false
	var ideaArchived *string
	err = tx.QueryRow(ctx, `select archived_at::text from element_types where key = 'idea'`).Scan(&ideaArchived)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := s.eng.CreateType(ctx, tx, adm, engine.TypeCreate{Key: "idea", Name: "Idea", PluralName: "Idee", Module: "work", From: "task"}); err != nil {
			return nil, err
		}
		ideaNew = true
	case err != nil:
		return nil, err
	case ideaArchived != nil:
		if _, err := tx.Exec(ctx, `update element_types set archived_at = null, updated_at = now() where key = 'idea'`); err != nil {
			return nil, err
		}
		if err := personaAudit(ctx, tx, actor, "idea"); err != nil {
			return nil, err
		}
		ideaNew = true
	}
	if _, err := tx.Exec(ctx, `update element_types set labels = labels || jsonb_build_object('en', jsonb_build_object('name', 'Idea', 'plural', 'Ideas'))
		where key = 'idea' and not (labels ? 'en')`); err != nil {
		return nil, err
	}
	items = append(items, avvioItem{Kind: "type", Key: "idea", Name: "Idea", State: personaState(!ideaNew)})

	// Campi: solo la priorità.
	tag, err = tx.Exec(ctx, `delete from type_fields tf using element_types t, field_defs f
		where tf.type_id = t.id and tf.field_id = f.id and t.key = any($1) and f.key = any($2)`, personaTypes, []string{work.FieldPoints, work.FieldEstimate})
	if err != nil {
		return nil, err
	}
	var prio string
	if err := tx.QueryRow(ctx, `select name from field_defs where key = $1`, work.FieldPriority).Scan(&prio); err != nil {
		return nil, err
	}
	items = append(items, avvioItem{Kind: "field", Key: work.FieldPriority, Name: prio, State: personaState(tag.RowsAffected() == 0)})

	// Flusso: nuova versione del processo di ciascun tipo, se non è già quello.
	for _, tk := range personaTypes {
		it, err := s.personaFlow(ctx, tx, adm, tk)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}

	// Tipi di partenza che non servono: si archiviano solo se non hanno elementi.
	rows, err := tx.Query(ctx, `select key, name, archived_at is not null,
		not exists (select 1 from elements e where e.type_id = element_types.id and e.deleted_at is null)
		from element_types where key = any($1)`, personaRetired)
	if err != nil {
		return nil, err
	}
	type retired struct {
		key, name       string
		archived, empty bool
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (retired, error) {
		var x retired
		return x, r.Scan(&x.key, &x.name, &x.archived, &x.empty)
	})
	if err != nil {
		return nil, err
	}
	for _, x := range list {
		switch {
		case x.archived:
			items = append(items, avvioItem{Kind: "type", Key: x.key, Name: x.name, State: "present", Off: true})
		case x.empty:
			if _, err := tx.Exec(ctx, `update element_types set archived_at = coalesce(archived_at, now()), updated_at = now() where key = $1`, x.key); err != nil {
				return nil, err
			}
			if err := personaAudit(ctx, tx, actor, x.key); err != nil {
				return nil, err
			}
			items = append(items, avvioItem{Kind: "type", Key: x.key, Name: x.name, State: "new", Off: true})
		}
	}

	// Team e board.
	bi, err := s.personaBoard(ctx, tx, p, adm, par)
	if err != nil {
		return nil, err
	}
	items = append(items, bi...)

	if err := tenant.Localize(ctx, tx, lang); err != nil {
		return nil, err
	}
	order := map[string]int{"module": 0, "type": 1, "field": 2, "process": 3, "team": 4, "board": 5}
	slices.SortStableFunc(items, func(a, b avvioItem) int {
		if a.Kind != b.Kind {
			return order[a.Kind] - order[b.Kind]
		}
		if a.Off != b.Off {
			if a.Off {
				return 1
			}
			return -1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return items, nil
}

// personaFlow pubblica sul processo del tipo la versione col flusso del modello, se la corrente è diversa.
func (s *Server) personaFlow(ctx context.Context, tx pgx.Tx, adm engine.Actor, typeKey string) (avvioItem, error) {
	var ref string
	err := tx.QueryRow(ctx, `select pd.key from process_definitions pd join element_types t on t.tenant_id = pd.tenant_id and t.id = pd.type_id
		where t.key = $1 and not pd.archived order by pd.priority, pd.key limit 1`, typeKey).Scan(&ref)
	if err != nil {
		return avvioItem{}, fmt.Errorf("processo del tipo %s: %w", typeKey, err)
	}
	info, err := s.eng.GetProcess(ctx, tx, adm, ref)
	if err != nil {
		return avvioItem{}, err
	}
	it := avvioItem{Kind: "process", Key: ref, Name: info.Name, State: "present"}
	cur := info.Current
	same := cur != nil && len(cur.Gates) == 0 && len(cur.Phases) == len(personaPhaseNames)
	if same {
		for i, ph := range cur.Phases {
			if ph.Name != personaPhaseNames[i] {
				same = false
			}
		}
	}
	if !same {
		if _, err := s.eng.SaveDraft(ctx, tx, adm, ref, []byte(personaFlow), personaNotes); err != nil {
			return avvioItem{}, err
		}
		if _, problems, err := s.eng.Publish(ctx, tx, adm, ref, personaNotes); err != nil {
			return avvioItem{}, fmt.Errorf("flusso del tipo %s: %w %v", typeKey, err, problems)
		}
		it.State = "new"
	}
	// Le fasi che il flusso non usa più (In revisione, Da verificare) non devono comparire tra le colonne
	// della board: si archiviano le opzioni senza elementi.
	_, err = tx.Exec(ctx, `update options o set archived = true from element_types t
		where t.key = $1 and o.option_set_id = t.state_set_id and not o.archived and not (o.name = any($2))
		and not exists (select 1 from elements e where e.state_id = o.id)`, typeKey, personaPhaseNames)
	return it, err
}

// personaBoard crea il team (il referente è chi applica il modello), lo spazio di delivery e la board
// kanban senza sprint, con i tipi task, bug e idea e le sole colonne che hanno fasi.
func (s *Server) personaBoard(ctx context.Context, tx pgx.Tx, p *auth.Principal, adm engine.Actor, par avvioParams) ([]avvioItem, error) {
	name := strings.TrimSpace(par.TeamName)
	if name == "" {
		name = personaDefaultTeam
	}
	var teamID string
	var spaceID, spaceKey *string
	err := tx.QueryRow(ctx, `select t.id, t.delivery_space_id, s.key from teams t left join spaces s on s.tenant_id = t.tenant_id and s.id = t.delivery_space_id
		where lower(t.name) = lower($1) and not t.archived`, name).Scan(&teamID, &spaceID, &spaceKey)
	hasTeam := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	teamItem := avvioItem{Kind: "team", Key: "team", Name: name, State: "present"}
	if hasTeam && spaceID != nil {
		return []avvioItem{teamItem, {Kind: "board", Key: *spaceKey, Name: *spaceKey + " · Kanban", State: "present"}}, nil
	}
	key := strings.ToUpper(strings.TrimSpace(par.BoardKey))
	if key == "" {
		key = personaDefaultKey
	}
	if !personaBoardKeyRe.MatchString(key) {
		return nil, engine.InvalidValue("board_key", "chiave non valida: lettere maiuscole e cifre, inizia con una lettera, al massimo 20 caratteri")
	}
	var used bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from spaces where key = $1)", key).Scan(&used); err != nil {
		return nil, err
	}
	if used {
		return nil, engine.Duplicate("board_key", "la chiave %s è già usata da un altro spazio", key)
	}
	if !hasTeam {
		if teamID, err = s.arch.CreateTeam(ctx, tx, adm, architecture.TeamInput{Name: name, LeadID: p.MemberID}); err != nil {
			return nil, err
		}
		teamItem.State = "new"
	}
	if _, err := tx.Exec(ctx, `insert into team_members (tenant_id, team_id, member_id) values (app_tenant(), $1, $2) on conflict do nothing`, teamID, p.MemberID); err != nil {
		return nil, err
	}
	id, err := work.EnsureTeamSpace(ctx, tx, adm, work.SpaceInput{TeamID: teamID, Key: key, Name: name})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into space_types (tenant_id, space_id, type_id)
		select app_tenant(), $1, id from element_types where key = any($2) on conflict do nothing`, id, personaTypes); err != nil {
		return nil, err
	}
	// Il modello kanban calcola le colonne dalle fasi dei tipi della board; quelle senza fasi si tolgono.
	base, err := s.work.Settings(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	base.Types = slices.Clone(personaTypes)
	st, err := work.ModelSettings(ctx, tx, "kanban", *base)
	if err != nil {
		return nil, err
	}
	st.Columns = slices.DeleteFunc(st.Columns, func(c work.BoardColumn) bool { return len(c.Phases) == 0 })
	raw, _ := json.Marshal(st)
	if _, err := tx.Exec(ctx, `insert into views (tenant_id, id, name, kind, settings, space_id, shared, system_key)
		values (app_tenant(), $1, $2, 'board', $3, $4, true, $5)
		on conflict (tenant_id, system_key) where system_key is not null do update set settings = excluded.settings, updated_at = now()`,
		db.NewID(), "Board "+key, raw, id, "board:"+id); err != nil {
		return nil, err
	}
	return []avvioItem{teamItem, {Kind: "board", Key: key, Name: key + " · Kanban", State: "new"}}, nil
}
