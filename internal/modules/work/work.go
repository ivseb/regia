// Package work è il work management di base (piano, passi 10 e 18; replica/work.md):
// spazi dei team con epic, story, task e bug, board con colonne dalle fasi del processo e
// limiti WIP, ordinamento manuale con indice frazionario, sprint come elementi con backlog e
// burndown, ore lavorate che diventano costi (modulo finance) e l'epic che nasce nello spazio
// del team quando il team accetta l'impatto di una richiesta, con l'avanzamento che risale.
//
// Tutto è dato del motore: tipi, processi (pacchetti di partenza in starter/), campi
// (priorità, punti, stima), relazione "pianificato in". Il modulo aggiunge viste e azioni.
package work

import (
	"context"
	"embed"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
)

const (
	TypeEpic   = "epic"
	TypeStory  = "story"
	TypeTask   = "task"
	TypeBug    = "bug"
	TypeSprint = "sprint"

	ImpactType      = "impact"           // modulo architettura
	WorkPackageType = "work_package"     // modulo dei progetti
	RequestType     = "business_request" // modulo demand

	// RelPlannedIn: elemento → sprint in cui è pianificato (al massimo uno).
	RelPlannedIn = "planned_in"

	// Azioni delle regole di processo registrate dal modulo.
	ActionCreateEpic  = "create_epic"  // sull'impatto (o sulla richiesta): epic nello spazio del team
	ActionSprintStart = "sprint_start" // sullo sprint: fotografa l'impegno per il burndown
	ActionSprintClose = "sprint_close" // sullo sprint: sposta gli incompiuti (ref = next | backlog)

	// Campi di default (work_fields.json): chiavi stabili, nomi modificabili.
	FieldPriority = "priorita"
	FieldPoints   = "punti"
	FieldEstimate = "stima"

	// PermWrite: gestire spazi e sprint (oltre a tenant.admin e ai referenti).
	permArch = "architecture.write"
)

// workTypes sono i tipi del modulo, ammessi in ogni spazio di team.
var workTypes = []struct{ key, name, ext string }{
	{TypeEpic, "Epic", ""},
	{TypeStory, "Story", ""},
	{TypeTask, "Task", ""},
	{TypeBug, "Bug", ""},
	{TypeSprint, "Sprint", "sprint"},
}

//go:embed starter/*.json
var starters embed.FS

var processFiles = []string{"epic_standard.json", "story_standard.json", "task_standard.json", "bug_standard.json", "sprint_standard.json"}

type Service struct {
	eng *engine.Service
	// ap: motore delle approvazioni, per le settimane di timesheet inviate (UseApprovals).
	ap *approval.Service
}

// Register collega il modulo al motore: fatti per i gate, azioni delle regole, pacchetti di
// partenza, campo di query "sprint" e la reazione ai passaggi degli epic.
func Register(eng *engine.Service) *Service {
	s := &Service{eng: eng}
	// Fatti dell'elemento di lavoro: per modulo, così valgono anche per i tipi creati dall'editor.
	eng.FactsModule("work", s.itemFacts)
	eng.Facts(TypeEpic, s.itemFacts)
	eng.Facts(TypeSprint, s.sprintFacts)
	eng.Facts(ImpactType, s.deliveryFacts)
	eng.Facts(RequestType, s.deliveryFacts)
	eng.Facts(WorkPackageType, s.deliveryFacts)
	eng.Action(ActionCreateEpic, s.actionCreateEpic)
	eng.Action(ActionSprintStart, s.actionSprintStart)
	eng.Action(ActionSprintClose, s.actionSprintClose, checkSprintClose)
	eng.OnTransition(s.afterTransition)
	describe(eng)
	for _, f := range processFiles {
		raw, err := starters.ReadFile("starter/" + f)
		if err != nil {
			panic(err)
		}
		eng.Starter(raw)
	}
	eng.QueryField(engine.QueryField{
		Key: "sprint", Names: []string{"sprint"},
		Label: "Sprint in cui è pianificato (chiave o titolo, es. WEB-12)",
		Kind:  "ref", RefType: TypeSprint, RefTitles: true,
		Source: `select r.source_id as element_id, r.target_id as v from relations r
			join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = '` + RelPlannedIn + `'`,
	})
	eng.QueryField(engine.QueryField{
		Key: "label", Names: []string{"etichetta", "etichette", "label", "tag"},
		Label:   "Etichetta (tag) dell'elemento",
		Kind:    "ref",
		Source:  `select et.element_id, et.tag_id as v from element_tags et`,
		Lookup:  `select id::text, lower(name) from tags where owner_id is null and lower(name) = any($1)`,
		Suggest: `select name, 'etichetta' from tags where owner_id is null and name ilike $1 order by name limit $2`,
	})
	return s
}

func describe(eng *engine.Service) {
	item := []engine.FactInfo{
		{Key: "assegnatario", Name: "Assegnatario", Kind: "presence"},
		{Key: "figli_aperti", Name: "Elementi figli ancora aperti", Kind: "number", Example: "0"},
		{Key: "figli", Name: "Elementi figli", Kind: "number"},
		{Key: "sprint", Name: "Sprint", Kind: "text"},
		{Key: "ore_registrate", Name: "Ore registrate", Kind: "number"},
	}
	eng.DescribeFactsModule("work", item...)
	eng.DescribeFacts(TypeEpic, append(item, engine.FactInfo{Key: "avanzamento", Name: "Avanzamento (% di story chiuse)", Kind: "number", Example: "100"})...)
	eng.DescribeFacts(TypeSprint,
		engine.FactInfo{Key: "inizio", Name: "Data di inizio", Kind: "date"},
		engine.FactInfo{Key: "fine", Name: "Data di fine", Kind: "date"},
		engine.FactInfo{Key: "sprint_attivi", Name: "Altri sprint attivi nello spazio", Kind: "number", Example: "0"},
		engine.FactInfo{Key: "elementi", Name: "Elementi pianificati", Kind: "number"},
		engine.FactInfo{Key: "elementi_aperti", Name: "Elementi pianificati ancora aperti", Kind: "number"},
	)
	for _, t := range []string{ImpactType, RequestType, WorkPackageType} {
		eng.DescribeFacts(t,
			engine.FactInfo{Key: "avanzamento", Name: "Avanzamento degli epic dei team (%)", Kind: "number", Example: "100"},
			engine.FactInfo{Key: "epic", Name: "Epic dei team", Kind: "text"},
			engine.FactInfo{Key: "epic_aperti", Name: "Epic dei team ancora aperti", Kind: "number", Example: "0"},
		)
	}
	eng.DescribeAction(engine.ActionSchema{Kind: ActionCreateEpic, Name: "Crea l'epic nello spazio del team",
		Description: "Sull'impatto: crea l'epic nello spazio di delivery del team owner, figlio dell'impatto, con l'assegnazione al team. " +
			"Sulla richiesta: un epic per ogni impatto con un team. Lo spazio del team si crea se manca; un epic già creato non si ricrea.",
		Fields: []engine.ActionField{}})
	eng.DescribeAction(engine.ActionSchema{Kind: ActionSprintStart, Name: "Fotografa l'impegno dello sprint",
		Description: "Sullo sprint: registra punti (o elementi) pianificati all'avvio, la base della linea ideale del burndown.",
		Fields:      []engine.ActionField{}})
	eng.DescribeAction(engine.ActionSchema{Kind: ActionSprintClose, Name: "Sposta gli incompiuti dello sprint",
		Description: "Sullo sprint: gli elementi non chiusi passano allo sprint pianificato successivo (next) oppure al backlog (backlog).",
		Fields: []engine.ActionField{{Key: "ref", Name: "Destinazione", Kind: "text", Required: true,
			Help: "next = primo sprint pianificato dello spazio (se non c'è, il backlog); backlog = tornano nel backlog."}}})
}

func checkSprintClose(_ context.Context, _ pgx.Tx, a engine.Action) ([]string, error) {
	if a.Ref != "next" && a.Ref != "backlog" {
		return []string{"destinazione degli incompiuti: next oppure backlog"}, nil
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// Permessi

// canManageSpace: configurare la board e gestire gli sprint dello spazio. Amministratori,
// chi gestisce i team (architecture.write), il responsabile dello spazio o del team, e chi
// può scrivere gli elementi (i team si organizzano da soli; il processo resta il vincolo).
func canManageSpace(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceID string) (bool, error) {
	if actor.Has("tenant.admin") || actor.Has(permArch) || actor.Has("element.write") {
		return true, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, `select exists (select 1 from spaces where id = $1 and lead_id = $2)
		or exists (select 1 from teams where delivery_space_id = $1 and lead_id = $2)`, spaceID, actor.MemberID).Scan(&ok)
	return ok, err
}

// canManageSprints: creare, modificare, avviare e chiudere gli sprint. Oltre a gestire lo spazio
// serve poter lavorare sugli elementi (element.write) o essere amministratore: chi guida un team
// ma ha il ruolo di sola lettura non pianifica, e la pagina non gli mostra i pulsanti.
func canManageSprints(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceID string) (bool, error) {
	if !actor.Has("tenant.admin") && !actor.Has("element.write") {
		return false, nil
	}
	return canManageSpace(ctx, tx, actor, spaceID)
}

// canWork: lavorare sugli elementi di uno spazio di team (creare, ordinare, assegnare,
// pianificare, spostare sulla board). Serve element.write, come per i passaggi di fase dei
// processi senza ruoli: così la board non promette spostamenti che il motore rifiuterebbe.
func canWork(_ context.Context, _ pgx.Tx, actor engine.Actor, _ string) (bool, error) {
	return actor.Has("element.write"), nil
}

// inTeam: l'attore è il responsabile dello spazio, il referente o un membro del team.
func inTeam(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceID string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `select exists (select 1 from spaces where id = $1 and lead_id = $2)
		or exists (select 1 from teams t where t.delivery_space_id = $1
		           and (t.lead_id = $2 or exists (select 1 from team_members tm where tm.team_id = t.id and tm.member_id = $2)))`,
		spaceID, actor.MemberID).Scan(&ok)
	return ok, err
}

// ---------------------------------------------------------------------------
// Helper

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func firstErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

// relationTypeID restituisce l'id del tipo di relazione (creato dal provisioning).
func relationTypeID(ctx context.Context, tx pgx.Tx, key string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, "select id from relation_types where key = $1", key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", engine.InvalidCode("validation.work.not_provisioned", "work", "modulo non configurato in questo tenant: esegui «regia tenant provision»")
	}
	return id, err
}

// spaceRow è uno spazio con il team di cui è lo spazio di delivery.
type spaceRow struct {
	ID     string
	Key    string
	Name   string
	TeamID *string
	Team   *string
}

func loadSpace(ctx context.Context, tx pgx.Tx, key string) (*spaceRow, error) {
	sp := &spaceRow{}
	err := tx.QueryRow(ctx, `select s.id, s.key, s.name, t.id, t.name from spaces s
		left join teams t on t.tenant_id = s.tenant_id and t.delivery_space_id = s.id
		where s.key = $1 and not s.archived limit 1`, strings.ToUpper(strings.TrimSpace(key))).
		Scan(&sp.ID, &sp.Key, &sp.Name, &sp.TeamID, &sp.Team)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	return sp, err
}

// typeIDs risolve le chiavi dei tipi in id.
func typeIDs(ctx context.Context, tx pgx.Tx, keys []string) (map[string]string, error) {
	rows, err := tx.Query(ctx, "select key, id::text from element_types where key = any($1)", keys)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	var k, id string
	_, err = pgx.ForEachRow(rows, []any{&k, &id}, func() error { out[k] = id; return nil })
	return out, err
}
