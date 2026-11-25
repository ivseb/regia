// Package bench genera dati sintetici e misura le prestazioni rispetto ai budget
// di architecture.md §7 (p95 < 100 ms con 1M elementi per tenant, ricerca < 200 ms).
// È uno strumento di sviluppo: non va usato su database con dati veri.
package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/architecture"
	"regia/internal/modules/demand"
	"regia/internal/modules/finance"
	"regia/internal/modules/portfolio"
	"regia/internal/modules/requirements"
	"regia/internal/modules/reviews"
	"regia/internal/modules/work"
	"regia/internal/platform/db"
	"regia/internal/platform/tenant"
)

// RequestShare è la quota di business request sul totale degli elementi del tenant; il
// resto sono impatti, valutazioni, requisiti ed elementi del work management (modules.go).
const RequestShare = 0.40

var (
	actions = []string{"Adeguamento", "Migrazione", "Evoluzione", "Integrazione", "Automazione", "Razionalizzazione",
		"Digitalizzazione", "Revisione", "Potenziamento", "Dismissione", "Consolidamento", "Rifacimento"}
	objects = []string{"del portale clienti", "del registro dei fornitori ICT", "del data warehouse", "della firma digitale",
		"del CRM commerciale", "dell'app mobile per i tecnici", "dei processi di acquisto", "del monitoraggio degli incidenti",
		"della conservazione a norma", "del cruscotto KPI", "della gestione delle identità", "del sistema di ticketing",
		"della piattaforma e-commerce", "del contact center", "dei pagamenti SEPA", "della contabilità analitica",
		"del sistema documentale", "della rete delle filiali", "del backup e disaster recovery", "dell'anagrafica clienti"}
	qualifiers = []string{"per DORA", "per NIS2", "per il GDPR", "in cloud europeo", "fase 2", "per la rete vendita",
		"per l'area Nord", "entro il 2027", "con assistente AI", "per le linee guida AgID", "", "", ""}
	sentences = []string{
		"Il processo attuale è manuale e richiede passaggi tra più uffici.",
		"La normativa impone tracciabilità completa e tempi di notifica definiti.",
		"Il fornitore attuale termina il supporto alla versione in uso.",
		"Gli utenti segnalano tempi di risposta elevati nelle ore di punta.",
		"Serve un registro unico consultabile dalle funzioni di controllo.",
		"L'obiettivo è ridurre i costi operativi e gli errori di inserimento.",
		"I dati sono oggi distribuiti su fogli di calcolo non controllati.",
		"Il comitato ha chiesto una stima entro la fine del trimestre.",
		"Sono coinvolte le applicazioni di vendita, contabilità e magazzino.",
		"Il rischio residuo è stato valutato alto dalla funzione di compliance.",
	}
	driversList = []string{"strategic", "regulatory", "operational", "efficiency", "risk"}
)

// Distribuzione degli stati (percentuali cumulative sul totale).
var stateMix = []struct {
	name string
	pct  int
}{
	{"Bozza", 5}, {"Inviata", 5}, {"Triage", 8}, {"Valutazione", 10}, {"Prioritizzazione", 7}, {"Approvazione", 2},
	{"Pianificazione", 8}, {"In esecuzione", 15}, {"Chiusa", 30}, {"Sospesa", 2}, {"Respinta", 6}, {"Ritirata", 2},
}

type ids struct {
	tenant, space, typ, admin, dmGroup, pmoGroup, policy, urgencyField, usersField string
	states                                                                         map[string]string
	categories                                                                     map[string]string
	roles                                                                          map[string]string
	urgency                                                                        []string
	members, sponsors, units                                                       []string
}

// SeedOptions descrive il tenant di prova.
type SeedOptions struct {
	Slug     string
	Elements int    // elementi totali del tenant: il 40% business request, il resto dei moduli
	Password string // dell'amministratore admin@<slug>.test
	Batch    int
	// OnlyRequests: solo business request (il dataset delle prime misure, senza moduli).
	OnlyRequests bool
}

// Seed crea (se manca) il tenant di prova e lo riempie con COPY in lotti.
// Usa il ruolo proprietario: COPY non è ammesso sulle tabelle con RLS per gli altri ruoli.
// Per questo ogni riga porta esplicitamente il tenant_id.
func Seed(ctx context.Context, d *db.DB, o SeedOptions, log *slog.Logger) error {
	if o.Batch <= 0 {
		o.Batch = 50_000
	}
	var exists bool
	if err := d.Owner.QueryRow(ctx, "select exists (select 1 from tenants where slug = $1)", o.Slug).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := tenant.Create(ctx, d, tenant.CreateInput{
			Slug: o.Slug, Name: "Benchmark " + o.Slug, AdminEmail: "admin@" + o.Slug + ".test", AdminName: "Admin benchmark",
			AdminPassword: o.Password, Provision: []tenant.Provisioner{demand.Provision, architecture.Provision, portfolio.Provision,
				finance.Provision, reviews.Provision, work.Provision, requirements.Provision},
		}); err != nil {
			return err
		}
	}
	x, err := lookup(ctx, d, o.Slug)
	if err != nil {
		return err
	}
	var already int
	if err := d.Owner.QueryRow(ctx, "select count(*) from elements where tenant_id = $1", x.tenant).Scan(&already); err != nil {
		return err
	}
	if already > 0 {
		return fmt.Errorf("il tenant %s contiene già %d elementi: usa un tenant nuovo", o.Slug, already)
	}
	if err := seedPeople(ctx, d, x); err != nil {
		return err
	}
	nreq := o.Elements
	var r *refs
	if !o.OnlyRequests {
		nreq = int(float64(o.Elements) * RequestShare)
		if x.usersField, err = seedUsersField(ctx, d, x); err != nil {
			return err
		}
		log.Info("dati di riferimento: team, spazi, applicazioni, sprint")
		if r, err = seedReference(ctx, d, x); err != nil {
			return err
		}
	}
	rnd := rand.New(rand.NewPCG(42, uint64(o.Elements)))
	start := time.Now()
	for from := 0; from < nreq; from += o.Batch {
		n := min(o.Batch, nreq-from)
		if err := seedBatch(ctx, d, x, rnd, from, n); err != nil {
			return err
		}
		done := from + n
		el := time.Since(start)
		log.Info("caricamento", "richieste", done, "di", nreq, "trascorso", el.Round(time.Second),
			"stima_fine", (time.Duration(float64(el) / float64(done) * float64(nreq-done))).Round(time.Second))
	}
	if _, err := d.Owner.Exec(ctx, "update spaces set next_number = $2 where id = $1", x.space, nreq+1); err != nil {
		return err
	}
	if r != nil {
		t0 := time.Now()
		if err := seedModules(ctx, d, x, r, rnd, o.Elements, log); err != nil {
			return err
		}
		log.Info("moduli", "durata", time.Since(t0).Round(time.Second))
	}
	log.Info("assegnatari delle approvazioni aperte")
	if _, err := d.Owner.Exec(ctx, "select approval_refresh_assignees(id, tenant_id) from approval_requests where tenant_id = $1 and status = 'pending'", x.tenant); err != nil {
		return err
	}
	log.Info("statistiche del planner (ANALYZE)")
	_, err = d.Owner.Exec(ctx, "analyze")
	return err
}

func lookup(ctx context.Context, d *db.DB, slug string) (*ids, error) {
	x := &ids{states: map[string]string{}, categories: map[string]string{}, roles: map[string]string{}}
	q := func(sql string, dst ...any) error { return d.Owner.QueryRow(ctx, sql, slug).Scan(dst...) }
	if err := q(`select t.id, s.id, et.id, ra.member_id from tenants t
		join spaces s on s.tenant_id = t.id and s.key = 'DEM'
		join element_types et on et.tenant_id = t.id and et.key = 'business_request'
		join roles r on r.tenant_id = t.id and r.key = 'tenant_admin'
		join role_assignments ra on ra.tenant_id = t.id and ra.role_id = r.id
		where t.slug = $1 limit 1`, &x.tenant, &x.space, &x.typ, &x.admin); err != nil {
		return nil, fmt.Errorf("tenant %s non configurato: %w", slug, err)
	}
	t := x.tenant
	if err := d.Owner.QueryRow(ctx, "select id from groups where tenant_id = $1 and name = 'Demand management'", t).Scan(&x.dmGroup); err != nil {
		return nil, err
	}
	if err := d.Owner.QueryRow(ctx, "select id from groups where tenant_id = $1 and name = 'PMO'", t).Scan(&x.pmoGroup); err != nil {
		return nil, err
	}
	if err := d.Owner.QueryRow(ctx, "select id from approval_policies where tenant_id = $1 limit 1", t).Scan(&x.policy); err != nil {
		return nil, err
	}
	if err := d.Owner.QueryRow(ctx, "select id from field_defs where tenant_id = $1 and key = 'urgenza'", t).Scan(&x.urgencyField); err != nil {
		return nil, err
	}
	rows, err := d.Owner.Query(ctx, `select o.id, o.name, coalesce(o.category, '') from options o
		join element_types et on et.tenant_id = o.tenant_id and et.state_set_id = o.option_set_id
		where o.tenant_id = $1 and et.id = $2`, t, x.typ)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, name, cat string
		if err := rows.Scan(&id, &name, &cat); err != nil {
			return nil, err
		}
		x.states[name], x.categories[name] = id, cat
	}
	rows, err = d.Owner.Query(ctx, `select o.id from options o join field_defs f on f.tenant_id = o.tenant_id and f.option_set_id = o.option_set_id
		where o.tenant_id = $1 and f.key = 'urgenza' order by o.ordinal`, t)
	if err != nil {
		return nil, err
	}
	if x.urgency, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return nil, err
	}
	rows, err = d.Owner.Query(ctx, "select key, id from stakeholder_roles where tenant_id = $1", t)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, id string
		if err := rows.Scan(&k, &id); err != nil {
			return nil, err
		}
		x.roles[k] = id
	}
	return x, rows.Err()
}

// seedPeople: 300 membri senza account (bastano come riferimenti) e 40 unità organizzative.
func seedPeople(ctx context.Context, d *db.DB, x *ids) error {
	var members [][]any
	for i := 0; i < 300; i++ {
		id := db.NewID()
		x.members = append(x.members, id)
		if i < 30 {
			x.sponsors = append(x.sponsors, id)
		}
		members = append(members, []any{x.tenant, id, fmt.Sprintf("Persona %03d", i), "standard", "active"})
	}
	if _, err := d.Owner.CopyFrom(ctx, pgx.Identifier{"members"}, []string{"tenant_id", "id", "display_name", "member_type", "status"}, pgx.CopyFromRows(members)); err != nil {
		return err
	}
	var units [][]any
	for i := 0; i < 40; i++ {
		id := db.NewID()
		x.units = append(x.units, id)
		units = append(units, []any{x.tenant, id, fmt.Sprintf("Unità %02d", i), "business_unit"})
	}
	_, err := d.Owner.CopyFrom(ctx, pgx.Identifier{"org_units"}, []string{"tenant_id", "id", "name", "kind"}, pgx.CopyFromRows(units))
	return err
}

func pickState(r *rand.Rand) string {
	v := r.IntN(100)
	for _, s := range stateMix {
		if v < s.pct {
			return s.name
		}
		v -= s.pct
	}
	return "Chiusa"
}

func seedBatch(ctx context.Context, d *db.DB, x *ids, r *rand.Rand, from, n int) error {
	now := time.Now().UTC()
	var elems, ext, sh, vals, acts, reqs, steps [][]any
	for i := 0; i < n; i++ {
		num := int64(from + i + 1)
		id := db.NewID()
		st := pickState(r)
		created := now.Add(-time.Duration(r.Int64N(int64(3 * 365 * 24 * time.Hour))))
		updated := created.Add(time.Duration(r.Int64N(int64(now.Sub(created)) + 1)))
		var resolved any
		if c := x.categories[st]; c == "done" || c == "cancelled" {
			resolved = updated
		}
		title := strings.TrimSpace(fmt.Sprintf("%s %s %s", actions[r.IntN(len(actions))], objects[r.IntN(len(objects))], qualifiers[r.IntN(len(qualifiers))]))
		body := sentences[r.IntN(len(sentences))] + " " + sentences[r.IntN(len(sentences))] + " " + sentences[r.IntN(len(sentences))]
		reporter := x.members[r.IntN(len(x.members))]
		urg := x.urgency[r.IntN(len(x.urgency))]
		fv := map[string]any{"urgenza": urg}
		users := 1 + r.IntN(1000) // campo personalizzato numerico "utenti" (query con >, <, intervalli)
		if x.usersField != "" {
			fv["utenti"] = users
		}
		fields, _ := json.Marshal(fv)
		elems = append(elems, []any{x.tenant, id, x.space, x.typ, num, title, body, x.states[st], reporter, string(fields), reporter, reporter, created, updated, resolved})

		low := int64(r.IntN(500)+5) * 1_000_00 // 5k - 505k €
		high := low + int64(r.IntN(200))*1_000_00
		var lowA, highA any
		if st != "Bozza" && st != "Inviata" && st != "Triage" {
			lowA, highA = low, high
		}
		ext = append(ext, []any{x.tenant, id, x.units[r.IntN(len(x.units))], driversList[r.IntN(len(driversList))], r.IntN(10) == 0, lowA, highA, "EUR"})

		sponsor := x.sponsors[r.IntN(len(x.sponsors))]
		sh = append(sh,
			[]any{x.tenant, id, x.roles["requester"], reporter, nil},
			[]any{x.tenant, id, x.roles["sponsor"], sponsor, nil},
			[]any{x.tenant, id, x.roles["demand_manager"], nil, x.dmGroup},
			[]any{x.tenant, id, x.roles["pmo"], nil, x.pmoGroup})
		vals = append(vals, []any{x.tenant, id, x.urgencyField, 0, nil, urg})
		if x.usersField != "" {
			vals = append(vals, []any{x.tenant, id, x.usersField, 0, users, nil})
		}
		acts = append(acts,
			[]any{x.tenant, id, reporter, "created", nil, `{"type":"business_request","state":"Bozza"}`, created},
			[]any{x.tenant, id, reporter, "state", "state", fmt.Sprintf(`{"name":%q}`, st), updated})

		// Approvazioni: in corso per lo stato "Approvazione", concluse per gli stati successivi.
		if st == "Approvazione" || st == "Pianificazione" || st == "In esecuzione" {
			reqID := db.NewID()
			status, dec := "approved", "approved"
			var closed any = updated
			if st == "Approvazione" {
				status, dec, closed = "pending", "pending", nil
			}
			reqs = append(reqs, []any{x.tenant, reqID, id, x.policy, "Approvazione", "stage_gate", status, highA, reporter, updated, closed})
			steps = append(steps, []any{x.tenant, db.NewID(), reqID, 1, 1, "sponsor", dec})
			if high > 5_000_000 {
				steps = append(steps, []any{x.tenant, db.NewID(), reqID, 2, 2, "pmo", dec})
			}
		}
	}
	return pgx.BeginFunc(ctx, d.Owner, func(tx pgx.Tx) error {
		copies := []struct {
			table string
			cols  []string
			rows  [][]any
		}{
			{"elements", []string{"tenant_id", "id", "space_id", "type_id", "number", "title", "body_text", "state_id", "reporter_id", "fields", "created_by", "updated_by", "created_at", "updated_at", "resolved_at"}, elems},
			{"ext_demand", []string{"tenant_id", "element_id", "requesting_unit_id", "driver", "is_mandatory", "estimate_low_cents", "estimate_high_cents", "currency"}, ext},
			{"stakeholders", []string{"tenant_id", "element_id", "role_id", "member_id", "group_id"}, sh},
			{"element_values", []string{"tenant_id", "element_id", "field_id", "ord", "v_num", "v_ref"}, vals},
			{"activities", []string{"tenant_id", "element_id", "actor_id", "kind", "field_key", "added", "created_at"}, acts},
			{"approval_requests", []string{"tenant_id", "id", "element_id", "policy_id", "stage", "subject", "status", "amount_cents", "requested_by", "requested_at", "closed_at"}, reqs},
			{"approval_steps", []string{"tenant_id", "id", "request_id", "ordinal", "parallel_group", "approver_role_key", "decision"}, steps},
		}
		for _, c := range copies {
			if len(c.rows) == 0 {
				continue
			}
			if _, err := tx.CopyFrom(ctx, pgx.Identifier{c.table}, c.cols, pgx.CopyFromRows(c.rows)); err != nil {
				return fmt.Errorf("copy %s: %w", c.table, err)
			}
		}
		return nil
	})
}

// seedUsersField crea il campo personalizzato numerico "utenti" (utenti coinvolti) sulle
// business request, con il servizio dei campi del motore.
func seedUsersField(ctx context.Context, d *db.DB, x *ids) (string, error) {
	eng := engine.New()
	actor := engine.Actor{MemberID: x.admin, Permissions: []string{"*"}}
	key, name := "utenti", "Utenti coinvolti"
	var id string
	err := d.InTenant(ctx, x.tenant, db.TenantOpts{}, func(tx pgx.Tx) error {
		f, err := eng.CreateField(ctx, tx, actor, engine.FieldInput{Key: &key, Name: &name, DataType: "integer"})
		if err != nil {
			return fmt.Errorf("campo utenti: %w", err)
		}
		if _, err := eng.AssociateField(ctx, tx, actor, key, demand.TypeKey, engine.AssocInput{}); err != nil {
			return fmt.Errorf("campo utenti sulle richieste: %w", err)
		}
		id = f.ID
		return nil
	})
	return id, err
}
