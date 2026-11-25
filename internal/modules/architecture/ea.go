package architecture

// Architettura enterprise (piano, passo 20; replica/architettura-ea.md): capability di
// business, dipendenze tipizzate tra applicazioni, grafo con analisi d'impatto, ciclo di
// vita (TIME, fine supporto delle tecnologie), diagrammi, import ed export del catalogo.
// Questo file: tipi, provisioning, impostazioni del tenant e funzioni comuni.

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

const (
	CapabilityType = "capability"

	RelSupports  = "supports"        // applicazione → capability
	RelRealizes  = "realizes"        // asset logico → capability
	RelCallsAPI  = "calls_api"       // applicazione → applicazione
	RelReadsData = "reads_data_from" // applicazione → applicazione
	RelSendsEvts = "sends_events_to" // applicazione → applicazione
	RelUsesApp   = "uses_app"        // applicazione → applicazione

	catDependency  = "dependency"
	catCapability  = "capability"
	catComposition = "composition"

	maxCapabilityDepth = 6
)

// Schema degli attributi di una dipendenza: protocollo, criticità, dati scambiati e la
// loro classificazione. I valori di "text" sono suggerimenti, quelli di "enum" vincoli.
const dependencyAttrs = `{"fields":[
 {"key":"protocol","name":"Protocollo","kind":"text","values":["REST","SOAP","gRPC","GraphQL","JDBC","SFTP","MQ","Kafka","File"]},
 {"key":"criticality","name":"Criticità","kind":"enum","values":["low","medium","high","critical"]},
 {"key":"data","name":"Dati scambiati","kind":"text"},
 {"key":"classification","name":"Classificazione dei dati","kind":"text","values":["pubblici","interni","riservati","personali","particolari"]}
]}`

// eaRelationTypes sono i tipi di relazione del pacchetto di partenza. I tipi di sistema si
// possono rinominare, archiviare e adattare (schema degli attributi, tipi ammessi), non
// cancellare; altri tipi si aggiungono dalla configurazione.
var eaRelationTypes = []struct {
	key, out, in, direction, category string
	sources, targets                  []string
	attrs                             bool
}{
	{RelCallsAPI, "chiama le API di", "espone API a", "forward", catDependency, []string{AppType}, []string{AppType}, true},
	{RelReadsData, "legge dati da", "fornisce dati a", "forward", catDependency, []string{AppType}, []string{AppType}, true},
	{RelSendsEvts, "invia eventi a", "riceve eventi da", "reverse", catDependency, []string{AppType}, []string{AppType}, true},
	{RelUsesApp, "usa i servizi di", "offre servizi a", "forward", catDependency, []string{AppType}, []string{AppType}, true},
	{RelSupports, "supporta", "è supportata da", "reverse", catCapability, []string{AppType}, []string{CapabilityType}, false},
	{RelRealizes, "realizza", "è realizzata da", "reverse", catCapability, []string{AssetType}, []string{CapabilityType}, false},
}

// provisionEA configura la parte enterprise del modulo (idempotente): tipo capability con
// il suo processo, tipi di relazione, categoria dei tipi già presenti, impostazioni.
func provisionEA(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	exec := func(sql string, args ...any) error { _, err := tx.Exec(ctx, sql, args...); return err }
	var typeID string
	err := tx.QueryRow(ctx, "select id from element_types where key = $1", CapabilityType).Scan(&typeID)
	if errors.Is(err, pgx.ErrNoRows) {
		typeID = db.NewID()
		err = exec(`insert into element_types (tenant_id, id, key, name, module, extension, is_system) values (app_tenant(), $1, $2, 'Capability di business', 'architecture', 'capability', true)`,
			typeID, CapabilityType)
	}
	if err != nil {
		return err
	}
	if err := exec(`insert into space_types (tenant_id, space_id, type_id)
		select app_tenant(), s.id, $2 from spaces s where s.key = $1 on conflict do nothing`, SpaceKey, typeID); err != nil {
		return err
	}
	for _, r := range eaRelationTypes {
		var schema any
		if r.attrs {
			schema = dependencyAttrs
		}
		if err := exec(`insert into relation_types (tenant_id, id, key, outward_name, inward_name, source_type_ids, target_type_ids,
				attrs_schema, impact_direction, ea_category, is_system)
			values (app_tenant(), $1, $2, $3, $4,
				(select array_agg(id) from element_types where key = any($5)), (select array_agg(id) from element_types where key = any($6)),
				$7::jsonb, $8, $9, true)
			on conflict do nothing`, db.NewID(), r.key, r.out, r.in, r.sources, r.targets, schema, r.direction, r.category); err != nil {
			return err
		}
	}
	// Tipi del passo 2: la composizione degli asset e la dipendenza generica entrano nel
	// grafo. "dipende da" resta senza vincoli di tipo: serve anche, per esempio, per
	// "l'applicazione dipende dal fornitore ICT".
	if err := exec(`update relation_types set ea_category = 'composition' where key in ('composed_of', 'uses') and ea_category is null`); err != nil {
		return err
	}
	if err := exec(`update relation_types set ea_category = 'dependency', attrs_schema = coalesce(attrs_schema, $1::jsonb)
		where key = 'depends_on' and ea_category is null`, dependencyAttrs); err != nil {
		return err
	}
	if err := exec(`insert into ea_settings (tenant_id) values (app_tenant()) on conflict do nothing`); err != nil {
		return err
	}
	var has bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from process_definitions p join element_types t on t.tenant_id = p.tenant_id and t.id = p.type_id
		where t.key = $1)`, CapabilityType).Scan(&has); err != nil || has {
		return err
	}
	raw, err := starters.ReadFile("starter/capability_standard.json")
	if err != nil {
		return err
	}
	b, err := engine.ParseBundle(raw)
	if err != nil {
		return err
	}
	_, problems, err := engine.New().Import(ctx, tx, engine.Actor{MemberID: adminMemberID, Permissions: []string{"*"}}, b, true)
	if err != nil {
		return errors.Join(err, errors.New(problemsText(problems)))
	}
	return nil
}

func problemsText(ps []engine.Problem) string {
	b, _ := json.Marshal(ps)
	return string(b)
}

// ---------------------------------------------------------------------------
// Impostazioni

type Settings struct {
	NodeTypes      []string `json:"node_types"`
	EOLWarningDays int      `json:"eol_warning_days"`
	DuplicateMin   int      `json:"duplicate_min"`
}

func settings(ctx context.Context, tx pgx.Tx) (Settings, error) {
	s := Settings{NodeTypes: []string{AppType, AssetType, CapabilityType, "ict_provider"}, EOLWarningDays: 180, DuplicateMin: 2}
	err := tx.QueryRow(ctx, `select node_types, eol_warning_days, duplicate_min from ea_settings`).Scan(&s.NodeTypes, &s.EOLWarningDays, &s.DuplicateMin)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	return s, err
}

func (s *Service) Settings(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Settings, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	st, err := settings(ctx, tx)
	return &st, err
}

var typeKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func (s *Service) UpdateSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in Settings) (*Settings, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	if len(in.NodeTypes) == 0 || len(in.NodeTypes) > 30 {
		return nil, engine.Required("node_types", "indica da 1 a 30 tipi di elemento")
	}
	for _, k := range in.NodeTypes {
		if !typeKeyRe.MatchString(k) {
			return nil, engine.InvalidValue("node_types", "chiave di tipo %q non valida", k)
		}
	}
	if in.EOLWarningDays < 0 || in.EOLWarningDays > 3650 {
		return nil, engine.OutOfRange("eol_warning_days", 0, 3650, "preavviso tra 0 e 3650 giorni")
	}
	if in.DuplicateMin < 2 || in.DuplicateMin > 50 {
		return nil, engine.OutOfRange("duplicate_min", 2, 50, "soglia dei duplicati tra 2 e 50 applicazioni")
	}
	if _, err := tx.Exec(ctx, `insert into ea_settings (tenant_id, node_types, eol_warning_days, duplicate_min, updated_at)
		values (app_tenant(), $1, $2, $3, now())
		on conflict (tenant_id) do update set node_types = excluded.node_types, eol_warning_days = excluded.eol_warning_days,
			duplicate_min = excluded.duplicate_min, updated_at = now()`, in.NodeTypes, in.EOLWarningDays, in.DuplicateMin); err != nil {
		return nil, err
	}
	if err := s.auditEA(ctx, tx, actor, "architecture.settings.update", "ea_settings", "", map[string]any{"node_types": in.NodeTypes}); err != nil {
		return nil, err
	}
	return s.Settings(ctx, tx, actor)
}

// ---------------------------------------------------------------------------
// Funzioni comuni

// canRead: il grafo dell'architettura è per chi legge gli elementi (non per i richiedenti
// del portale, che citano gli asset ma non vedono l'architettura tecnica).
func canRead(actor engine.Actor) bool { return actor.Has("element.read") }

func (s *Service) auditEA(ctx context.Context, tx pgx.Tx, actor engine.Actor, action, entityType, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: entityType, EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload})
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var elementKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

// nodeRef è un elemento del grafo risolto da un riferimento.
type nodeRef struct {
	ID, Key, Title, TypeID, TypeKey, TypeName string
}

// resolveNode trova un elemento non cancellato per id, chiave (ARC-12), codice
// dell'applicazione o della capability, oppure titolo esatto (senza distinzione di
// maiuscole) tra i tipi indicati. Più elementi con lo stesso titolo: errore.
func resolveNode(ctx context.Context, tx pgx.Tx, ref string, types []string) (*nodeRef, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	base := `select e.id, coalesce(s.key || '-' || e.number, ''), e.title, t.id, t.key, t.name
		from elements e
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where e.deleted_at is null and not e.is_draft and ($2::text[] is null or t.key = any($2)) and `
	var rows pgx.Rows
	var err error
	low := strings.ToLower(ref)
	switch {
	case uuidRe.MatchString(low):
		rows, err = tx.Query(ctx, base+`e.id = $1::uuid`, low, nilIfEmpty(types))
	case elementKeyRe.MatchString(strings.ToUpper(ref)):
		k, n, _ := strings.Cut(strings.ToUpper(ref), "-")
		rows, err = tx.Query(ctx, base+`s.key = split_part($1, '-', 1) and e.number = $3::bigint`, k+"-"+n, nilIfEmpty(types), n)
	default:
		rows, err = tx.Query(ctx, base+`(lower(e.title) = $1
			or exists (select 1 from ext_application a where a.tenant_id = e.tenant_id and a.element_id = e.id and lower(a.code) = $1)
			or exists (select 1 from ext_capability c where c.tenant_id = e.tenant_id and c.element_id = e.id and lower(c.code) = $1))
			limit 3`, low, nilIfEmpty(types))
	}
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (nodeRef, error) {
		var n nodeRef
		err := r.Scan(&n.ID, &n.Key, &n.Title, &n.TypeID, &n.TypeKey, &n.TypeName)
		return n, err
	})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	if len(list) > 1 {
		// Un codice vince sul titolo; altrimenti il riferimento è ambiguo.
		var exact []nodeRef
		for _, n := range list {
			if strings.ToLower(n.Title) != low {
				exact = append(exact, n)
			}
		}
		if len(exact) == 1 {
			return &exact[0], nil
		}
		return nil, engine.InvalidParams("validation.architecture.ref_ambiguous", "ref", map[string]any{"ref": ref}, "%q indica più elementi: usa la chiave o il codice", ref)
	}
	return &list[0], nil
}

func nilIfEmpty(ss []string) any {
	if len(ss) == 0 {
		return nil
	}
	return ss
}

// typeNames traduce id di tipi in chiavi.
func typeKeys(ctx context.Context, tx pgx.Tx, ids []string) ([]string, error) {
	if ids == nil {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `select key from element_types where id = any($1::uuid[]) order by key`, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
