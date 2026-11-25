package bench

// Dati dei moduli per il benchmark esteso (2026-11-24): impatti, valutazioni, requisiti con le
// celle della matrice, epic, story e task negli sprint dei team, commenti, attività,
// notifiche e campi personalizzati. Le proporzioni sul totale degli elementi del tenant:
//
//	business request 40% · impatti ~15% · valutazioni ~4% · requisiti ~12% · epic ~2% ·
//	story ~13% · task ~13% (più 300 requisiti × 30 impatti su una richiesta grande)
//
// I dati di riferimento (team, spazi di delivery, applicazioni, sprint, campo personalizzato)
// passano dai servizi dei moduli; il volume va con COPY, come le richieste.

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
	"regia/internal/modules/work"
	"regia/internal/platform/db"
)

// BigRequestTitle identifica la richiesta grande (matrice dei requisiti 300 × 30).
const BigRequestTitle = "Programma di trasformazione digitale (benchmark)"

const (
	benchTeams = 12
	benchApps  = 60
)

type state struct{ id, cat string }

type refs struct {
	types      map[string]string           // chiave del tipo -> id
	states     map[string]map[string]state // tipo -> nome della fase -> id e categoria
	spaces     []string                    // spazi di delivery dei team (id), in ordine di team
	spaceKeys  []string
	teams      []string
	apps       []app
	sprints    [][]sprint // per spazio, in ordine di data
	reviewType []reviewType
	revTeams   []string
	plannedIn  string
	priorities []string // opzioni del campo priorita
	fields     map[string]string
}

type app struct{ id, name, team string }

type sprint struct {
	id         string
	start, end time.Time
}

type reviewType struct {
	id, team, template, name, outcome, label, polarity string
}

type request struct {
	id, title, state string
	created          time.Time
}

// seedReference crea team, spazi, applicazioni e sprint con i servizi dei moduli.
func seedReference(ctx context.Context, d *db.DB, x *ids) (*refs, error) {
	eng := engine.New()
	arc := architecture.Register(eng)
	wk := work.Register(eng)
	actor := engine.Actor{MemberID: x.admin, Permissions: []string{"*"}}
	r := &refs{types: map[string]string{}, states: map[string]map[string]state{}, fields: map[string]string{}}
	err := d.InTenant(ctx, x.tenant, db.TenantOpts{}, func(tx pgx.Tx) error {
		for i := 0; i < benchTeams; i++ {
			id, err := arc.CreateTeam(ctx, tx, actor, architecture.TeamInput{Name: fmt.Sprintf("Team %02d", i+1), LeadID: x.members[i]})
			if err != nil {
				return fmt.Errorf("team: %w", err)
			}
			space, err := work.EnsureTeamSpace(ctx, tx, actor, work.SpaceInput{TeamID: id, Key: fmt.Sprintf("T%02d", i+1)})
			if err != nil {
				return fmt.Errorf("spazio del team: %w", err)
			}
			r.teams, r.spaces, r.spaceKeys = append(r.teams, id), append(r.spaces, space), append(r.spaceKeys, fmt.Sprintf("T%02d", i+1))
		}
		crit := []string{"low", "medium", "high", "critical"}
		for i := 0; i < benchApps; i++ {
			name, code, c, team := fmt.Sprintf("Applicazione %02d", i+1), fmt.Sprintf("APP-%02d", i+1), crit[i%4], r.teams[i%benchTeams]
			a, err := arc.CreateApplication(ctx, tx, actor, architecture.ApplicationInput{Name: &name, Code: &code, Criticality: &c, TeamID: &team})
			if err != nil {
				return fmt.Errorf("applicazione: %w", err)
			}
			r.apps = append(r.apps, app{id: a.ID, name: name, team: team})
		}
		// Sprint di due settimane: un anno indietro e due in avanti; l'attivo è quello di oggi.
		now := time.Now().UTC().Truncate(24 * time.Hour)
		first := now.AddDate(0, 0, -7*52)
		for s, key := range r.spaceKeys {
			var list []sprint
			for k := 0; k < 28; k++ {
				start := first.AddDate(0, 0, 14*k)
				end := start.AddDate(0, 0, 13)
				title, from, to := fmt.Sprintf("Sprint %d", k+1), start.Format(time.DateOnly), end.Format(time.DateOnly)
				el, err := wk.CreateSprint(ctx, tx, actor, key, work.SprintInput{Title: &title, Start: &from, End: &to})
				if err != nil {
					return fmt.Errorf("sprint dello spazio %d: %w", s, err)
				}
				list = append(list, sprint{id: el.ID, start: start, end: end})
			}
			r.sprints = append(r.sprints, list)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r, loadRefs(ctx, d, x, r)
}

func loadRefs(ctx context.Context, d *db.DB, x *ids, r *refs) error {
	rows, err := d.Owner.Query(ctx, `select t.key, t.id, coalesce(o.name, ''), coalesce(o.id::text, ''), coalesce(o.category, '')
		from element_types t left join options o on o.tenant_id = t.tenant_id and o.option_set_id = t.state_set_id
		where t.tenant_id = $1`, x.tenant)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, id, name, sid, cat string
		if err := rows.Scan(&key, &id, &name, &sid, &cat); err != nil {
			return err
		}
		r.types[key] = id
		if r.states[key] == nil {
			r.states[key] = map[string]state{}
		}
		if name != "" {
			r.states[key][name] = state{sid, cat}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = d.Owner.Query(ctx, `select rt.id, rt.team_id, q.id, rt.name, coalesce(rt.outcomes->0->>'key', ''),
		coalesce(rt.outcomes->0->>'label', ''), coalesce(rt.outcomes->0->>'polarity', 'positive')
		from review_types rt join questionnaire_templates q on q.tenant_id = rt.tenant_id and q.key = rt.template_key and q.status = 'published'
		where rt.tenant_id = $1 and not rt.archived order by rt.key`, x.tenant)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v reviewType
		if err := rows.Scan(&v.id, &v.team, &v.template, &v.name, &v.outcome, &v.label, &v.polarity); err != nil {
			return err
		}
		r.reviewType = append(r.reviewType, v)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(r.reviewType) == 0 {
		return fmt.Errorf("nessun tipo di valutazione: il tenant non ha il modulo valutazioni")
	}
	rows, err = d.Owner.Query(ctx, "select id from review_teams where tenant_id = $1 order by key", x.tenant)
	if err != nil {
		return err
	}
	if r.revTeams, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return err
	}
	if err := d.Owner.QueryRow(ctx, "select id from relation_types where tenant_id = $1 and key = 'planned_in'", x.tenant).Scan(&r.plannedIn); err != nil {
		return fmt.Errorf("relazione planned_in: %w", err)
	}
	rows, err = d.Owner.Query(ctx, `select o.id from options o join field_defs f on f.tenant_id = o.tenant_id and f.option_set_id = o.option_set_id
		where o.tenant_id = $1 and f.key = $2 order by o.ordinal`, x.tenant, work.FieldPriority)
	if err != nil {
		return err
	}
	if r.priorities, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return err
	}
	rows, err = d.Owner.Query(ctx, "select key, id from field_defs where tenant_id = $1", x.tenant)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k, id string
		if err := rows.Scan(&k, &id); err != nil {
			return err
		}
		r.fields[k] = id
	}
	return rows.Err()
}

// gen accumula le righe per COPY e le scrive a lotti in una transazione.
type gen struct {
	tables map[string][][]any
	order  []string
	cols   map[string][]string
}

func newGen() *gen { return &gen{tables: map[string][][]any{}, cols: map[string][]string{}} }

func (g *gen) add(table string, cols []string, row ...any) {
	if _, ok := g.cols[table]; !ok {
		g.cols[table] = cols
		g.order = append(g.order, table)
	}
	g.tables[table] = append(g.tables[table], row)
}

func (g *gen) rows() int {
	n := 0
	for _, r := range g.tables {
		n += len(r)
	}
	return n
}

func (g *gen) flush(ctx context.Context, d *db.DB) error {
	if g.rows() == 0 {
		return nil
	}
	err := pgx.BeginFunc(ctx, d.Owner, func(tx pgx.Tx) error {
		for _, t := range g.order {
			if len(g.tables[t]) == 0 {
				continue
			}
			if _, err := tx.CopyFrom(ctx, pgx.Identifier{t}, g.cols[t], pgx.CopyFromRows(g.tables[t])); err != nil {
				return fmt.Errorf("copy %s: %w", t, err)
			}
		}
		return nil
	})
	g.tables = map[string][][]any{}
	return err
}

var (
	elemCols  = []string{"tenant_id", "id", "space_id", "type_id", "number", "title", "body_text", "state_id", "assignee_id", "reporter_id", "parent_id", "rank", "start_at", "due_at", "fields", "created_by", "updated_by", "created_at", "updated_at", "resolved_at"}
	actCols   = []string{"tenant_id", "element_id", "actor_id", "kind", "field_key", "added", "created_at"}
	valCols   = []string{"tenant_id", "element_id", "field_id", "ord", "v_num", "v_ref"}
	impCols   = []string{"tenant_id", "element_id", "request_id", "application_id", "team_id", "change_kind", "estimate_effort_days"}
	revCols   = []string{"tenant_id", "element_id", "review_type_id", "team_id", "template_id", "request_id", "mandatory", "stage", "answers", "required_total", "required_missing", "submitted_at", "due_on", "outcome", "outcome_label", "outcome_polarity", "decided_at", "decided_by"}
	reqCols   = []string{"tenant_id", "element_id", "request_id", "impact_id", "scope", "origin", "category", "verification_criterion", "verifier_team_id", "stage", "created_at"}
	cellCols  = []string{"tenant_id", "id", "requirement_element_id", "target_element_id", "request_id", "status", "progress", "implemented_by", "implemented_at", "verified_by", "verified_at", "decided_at", "updated_at"}
	relCols   = []string{"tenant_id", "id", "type_id", "source_id", "target_id", "created_by", "created_at"}
	comCols   = []string{"tenant_id", "id", "element_id", "author_id", "body", "body_text", "created_at", "updated_at"}
	notifCols = []string{"tenant_id", "id", "member_id", "element_id", "kind", "payload", "read_at", "created_at"}
)

// elem aggiunge un elemento con la sua attività di creazione.
func (g *gen) elem(x *ids, id, space, typ string, number int64, title, body string, st state, assignee, reporter, parent, rank any,
	start, due any, fields string, created, updated time.Time) {
	var resolved any
	if st.cat == "done" || st.cat == "cancelled" {
		resolved = updated
	}
	g.add("elements", elemCols, x.tenant, id, space, typ, number, title, body, st.id, assignee, reporter, parent, rank, start, due, fields, reporter, reporter, created, updated, resolved)
	g.add("activities", actCols, x.tenant, id, reporter, "created", nil, `{}`, created)
}

func pick[T any](r *rand.Rand, s []T) T { return s[r.IntN(len(s))] }

func between(r *rand.Rand, a, b time.Time) time.Time {
	if !b.After(a) {
		return a
	}
	return a.Add(time.Duration(r.Int64N(int64(b.Sub(a)))))
}

var (
	categories = []string{"Sicurezza", "Crittografia", "Continuità", "Privacy", "Documentazione", "Accessibilità", "Prestazioni"}
	reqTitles  = []string{"Cifratura dei dati in transito", "Accesso con autenticazione a più fattori", "Registro degli accessi amministrativi",
		"Backup giornaliero con prova di ripristino", "Segreti fuori dal codice sorgente", "Nessuna vulnerabilità critica aperta",
		"Conservazione dei log per 12 mesi", "Manuale operativo aggiornato", "Pagine conformi alle linee guida di accessibilità",
		"Tempi di risposta sotto i 2 secondi"}
	storyVerbs = []string{"Pagina", "Servizio", "Integrazione", "Report", "Notifica", "Ricerca", "Esportazione", "Validazione", "Migrazione", "Configurazione"}
	storyObjs  = []string{"dei contratti", "delle fatture", "dei clienti", "dei fornitori", "degli ordini", "dei ticket", "delle utenze", "dei pagamenti", "dei documenti", "delle scadenze"}
	commentTxt = []string{"Ho aggiornato la stima dopo il confronto con il team.", "Serve il parere della sicurezza prima di procedere.",
		"Il fornitore ha confermato le date.", "Allego il verbale della riunione di avvio.", "Ok per me, procediamo con la pianificazione.",
		"Ci sono dipendenze con il progetto dei pagamenti.", "Il cliente interno chiede di anticipare il rilascio.", "Da rivedere dopo il collaudo."}
)

// seedModules genera i dati dei moduli per le richieste già caricate. total è il numero di
// elementi del tenant da raggiungere (richieste comprese).
func seedModules(ctx context.Context, d *db.DB, x *ids, r *refs, rnd *rand.Rand, total int, log *slog.Logger) error {
	reqs, err := loadRequests(ctx, d, x)
	if err != nil {
		return err
	}
	var next int64
	if err := d.Owner.QueryRow(ctx, "select coalesce(max(number), 0) + 1 from elements where tenant_id = $1 and space_id = $2", x.tenant, x.space).Scan(&next); err != nil {
		return err
	}
	demNum := func() int64 { next++; return next - 1 }
	spaceNum := make([]int64, len(r.spaces))
	spaceRank := make([]int, len(r.spaces))
	for i, sp := range r.spaces {
		// Gli sprint creati dal servizio occupano già i primi numeri dello spazio.
		if err := d.Owner.QueryRow(ctx, "select coalesce(max(number), 0) + 1 from elements where tenant_id = $1 and space_id = $2", x.tenant, sp).Scan(&spaceNum[i]); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	g := newGen()
	flush := func(force bool) error {
		if !force && g.rows() < 60_000 {
			return nil
		}
		return g.flush(ctx, d)
	}
	st := func(typ, name string) state {
		s, ok := r.states[typ][name]
		if !ok {
			panic(fmt.Sprintf("fase %q del tipo %s inesistente", name, typ))
		}
		return s
	}
	// Quote sul totale (vedi il commento in testa al file).
	eligible, accepted := 0, 0
	for _, q := range reqs {
		switch q.state {
		case "Pianificazione", "In esecuzione", "Chiusa":
			accepted++
			eligible++
		case "Valutazione", "Prioritizzazione", "Approvazione":
			eligible++
		}
	}
	eligible, accepted = max(eligible, 1), max(accepted, 1)
	impactShare := float64(total) * 0.15 / 1.5 / float64(eligible) // richieste con impatti (1-2 a testa)
	reviewShare := float64(total) * 0.04 / float64(eligible)
	workDone := 0
	workTarget := int(float64(total) * 0.28)
	// Ogni impatto accettato con il suo epic porta in media 6 story e 9 task.
	workProb := float64(workTarget) / (float64(accepted) * min(impactShare, 1) * 1.5 * 15)
	counts := map[string]int{}

	addRequirement := func(req request, impact, impactState string, scope string, title string, created time.Time) {
		id := db.NewID()
		var name, stage, progress string
		switch impactState {
		case "Accettato":
			name, stage, progress = "Accettato", "accepted", "open"
		case "In realizzazione":
			if rnd.IntN(3) == 0 {
				name, stage, progress = "Da verificare", "to_verify", "to_verify"
			} else {
				name, stage, progress = "In realizzazione", "implementing", "open"
			}
		case "Rilasciato":
			name, stage, progress = "Verificato", "verified", "verified"
		default:
			name, stage, progress = "Proposto", "proposed", "open"
		}
		parent := req.id
		var impactID any
		if scope == "application" {
			parent, impactID = impact, impact
		}
		upd := between(rnd, created, now)
		g.elem(x, id, x.space, r.types["requirement"], demNum(), title, "Requisito di esempio del benchmark.", st("requirement", name),
			nil, pick(rnd, x.members), parent, nil, nil, nil, "{}", created, upd)
		g.add("ext_requirement", reqCols, x.tenant, id, req.id, impactID, scope, "manual", pick(rnd, categories), "Verifica manuale", pick(rnd, r.revTeams), stage, created)
		var implBy, implAt, verBy, verAt any
		if progress != "open" {
			implBy, implAt = pick(rnd, x.members), upd
		}
		if progress == "verified" {
			verBy, verAt = pick(rnd, x.members), upd
		}
		g.add("applicability", cellCols, x.tenant, db.NewID(), id, impact, req.id, "applicable", progress, implBy, implAt, verBy, verAt, created, upd)
		counts["requisiti"]++
	}

	// Work: epic nello spazio del team, 4-8 story, 1-2 task per story, negli sprint per data.
	addWork := func(impactID, impactState string, a app, created time.Time) {
		si := 0
		for i, t := range r.teams {
			if t == a.team {
				si = i
			}
		}
		space := r.spaces[si]
		epicState := map[string]string{"Accettato": "Da fare", "In realizzazione": "In corso", "Rilasciato": "Fatto"}[impactState]
		epic := db.NewID()
		spaceRank[si]++
		g.elem(x, epic, space, r.types["epic"], spaceNum[si], "Epic · "+a.name, "", st("epic", epicState), pick(rnd, x.members), pick(rnd, x.members),
			impactID, fmt.Sprintf("h%09d1", spaceRank[si]), nil, nil, fmt.Sprintf(`{"priorita":%q}`, pick(rnd, r.priorities)), created, between(rnd, created, now))
		spaceNum[si]++
		counts["epic"]++
		for s, ns := 0, 4+rnd.IntN(5); s < ns; s++ {
			sc := between(rnd, created, now)
			// Lo sprint è quello della data di creazione (o uno futuro per le story ancora da fare).
			var sp *sprint
			for k := range r.sprints[si] {
				if !sc.Before(r.sprints[si][k].start) && sc.Before(r.sprints[si][k].end.Add(24*time.Hour)) {
					sp = &r.sprints[si][k]
				}
			}
			name := "Fatto"
			switch {
			case epicState == "Da fare":
				name = "Da fare"
			case epicState == "In corso" && sp != nil && sp.end.After(now):
				name = pick(rnd, []string{"Da fare", "In corso", "In corso", "In revisione", "Fatto"})
			case epicState == "In corso":
				name = pick(rnd, []string{"Fatto", "Fatto", "Fatto", "In corso"})
			}
			if rnd.IntN(25) == 0 {
				name = "Annullato"
			}
			prio := pick(rnd, r.priorities)
			pts := []int{1, 2, 3, 5, 8, 13}[rnd.IntN(6)]
			story := db.NewID()
			var asg any
			if name != "Da fare" || rnd.IntN(2) == 0 {
				asg = pick(rnd, x.members[:60])
			}
			spaceRank[si]++
			upd := between(rnd, sc, now)
			g.elem(x, story, space, r.types["story"], spaceNum[si], pick(rnd, storyVerbs)+" "+pick(rnd, storyObjs), "Come utente voglio poter lavorare senza passaggi manuali.",
				st("story", name), asg, pick(rnd, x.members), epic, fmt.Sprintf("h%09d1", spaceRank[si]), nil, nil,
				fmt.Sprintf(`{"priorita":%q,"punti":%d}`, prio, pts), sc, upd)
			spaceNum[si]++
			g.add("element_values", valCols, x.tenant, story, r.fields[work.FieldPriority], 0, nil, prio)
			g.add("element_values", valCols, x.tenant, story, r.fields[work.FieldPoints], 0, pts, nil)
			if sp != nil {
				g.add("relations", relCols, x.tenant, db.NewID(), r.plannedIn, story, sp.id, x.admin, sc)
			}
			counts["story"]++
			workDone++
			for t, nt := 0, 1+rnd.IntN(2); t < nt; t++ {
				tn := name
				if name == "In corso" || name == "In revisione" {
					tn = pick(rnd, []string{"Da fare", "In corso", "Fatto"})
				}
				task := db.NewID()
				spaceRank[si]++
				g.elem(x, task, space, r.types["task"], spaceNum[si], "Task: "+pick(rnd, storyVerbs)+" "+pick(rnd, storyObjs), "", st("task", tn), asg, pick(rnd, x.members), story,
					fmt.Sprintf("h%09d1", spaceRank[si]), nil, nil, fmt.Sprintf(`{"priorita":%q}`, prio), sc, between(rnd, sc, now))
				spaceNum[si]++
				g.add("element_values", valCols, x.tenant, task, r.fields[work.FieldPriority], 0, nil, prio)
				if sp != nil {
					g.add("relations", relCols, x.tenant, db.NewID(), r.plannedIn, task, sp.id, x.admin, sc)
				}
				counts["task"]++
				workDone++
			}
		}
	}

	impactState := func(reqState string) string {
		switch reqState {
		case "Valutazione":
			return pick(rnd, []string{"Da analizzare", "In analisi", "Stimato"})
		case "Prioritizzazione", "Approvazione":
			return "Stimato"
		case "Pianificazione":
			return "Accettato"
		case "In esecuzione":
			return "In realizzazione"
		case "Chiusa":
			return "Rilasciato"
		}
		return ""
	}
	addImpact := func(req request, a app, ist string, created time.Time) string {
		id := db.NewID()
		var start, due, effort any
		if ist != "Da analizzare" {
			effort = 5 + rnd.IntN(60)
		}
		if ist == "Accettato" || ist == "In realizzazione" || ist == "Rilasciato" {
			s := between(rnd, created, created.AddDate(0, 3, 0))
			start, due = s, s.AddDate(0, 0, 14+rnd.IntN(90))
		}
		g.elem(x, id, x.space, r.types["impact"], demNum(), a.name+" · "+req.title, "", st("impact", ist), nil, pick(rnd, x.members), req.id, nil, start, due, "{}", created, between(rnd, created, now))
		g.add("ext_impact", impCols, x.tenant, id, req.id, a.id, a.team, "change", effort)
		counts["impatti"]++
		return id
	}

	for i, req := range reqs {
		ist := impactState(req.state)
		if ist != "" && rnd.Float64() < impactShare {
			used := map[int]bool{}
			for k, nk := 0, 1+rnd.IntN(2); k < nk; k++ {
				ai := rnd.IntN(len(r.apps))
				if used[ai] {
					continue
				}
				used[ai] = true
				a := r.apps[ai]
				created := between(rnd, req.created, req.created.Add(30*24*time.Hour))
				imp := addImpact(req, a, ist, created)
				if rnd.IntN(5) < 4 {
					addRequirement(req, imp, ist, "application", pick(rnd, reqTitles), created)
				}
				if (ist == "Accettato" || ist == "In realizzazione" || ist == "Rilasciato") && workDone < workTarget && rnd.Float64() < workProb {
					addWork(imp, ist, a, created)
				}
			}
		}
		if ist != "" && rnd.Float64() < reviewShare {
			rt := pick(rnd, r.reviewType)
			id := db.NewID()
			name, stage := "Conclusa", "concluded"
			if req.state == "Valutazione" {
				name, stage = pick(rnd, []string{"In compilazione", "Inviata al team", "In esame"}), ""
				stage = map[string]string{"In compilazione": "compile", "Inviata al team": "submitted", "In esame": "exam"}[name]
			}
			created := between(rnd, req.created, req.created.Add(20*24*time.Hour))
			upd := between(rnd, created, now)
			g.elem(x, id, x.space, r.types["review"], demNum(), rt.name+" · "+req.title, "", st("review", name), nil, pick(rnd, x.members), req.id, nil, nil, nil, "{}", created, upd)
			var submitted, due, outcome, label, pol, decAt, decBy any
			if stage != "compile" {
				submitted, due = created, created.AddDate(0, 0, 7)
			}
			if stage == "concluded" {
				outcome, label, pol, decAt, decBy = rt.outcome, rt.label, rt.polarity, upd, pick(rnd, x.members)
			}
			g.add("ext_review", revCols, x.tenant, id, rt.id, rt.team, rt.template, req.id, false, stage, "{}", 4, 0, submitted, due, outcome, label, pol, decAt, decBy)
			counts["valutazioni"]++
		}
		// Commenti e notifiche sulle richieste (le notifiche vanno soprattutto all'amministratore
		// e alle prime persone, come per chi segue molte richieste).
		for c, nc := 0, rnd.IntN(3); c < nc; c++ {
			at := between(rnd, req.created, now)
			g.add("comments", comCols, x.tenant, db.NewID(), req.id, pick(rnd, x.members), `{"format":"markdown","mentions":[]}`, pick(rnd, commentTxt), at, at)
			g.add("activities", actCols, x.tenant, req.id, pick(rnd, x.members), "comment", nil, `{}`, at)
			counts["commenti"]++
		}
		if rnd.IntN(3) == 0 {
			who := x.admin
			if rnd.IntN(4) > 0 {
				who = x.members[rnd.IntN(30)]
			}
			at := between(rnd, req.created, now)
			var read any
			if rnd.IntN(5) > 0 {
				read = at.Add(time.Hour)
			}
			payload, _ := json.Marshal(map[string]string{"from": "Triage", "to": req.state})
			g.add("notifications", notifCols, x.tenant, db.NewID(), who, req.id, "phase", string(payload), read, at)
			counts["notifiche"]++
		}
		if err := flush(false); err != nil {
			return err
		}
		if (i+1)%100_000 == 0 {
			log.Info("moduli", "richieste", i+1, "di", len(reqs), "conteggi", counts)
		}
	}

	// Richiesta grande: 30 impatti e 300 requisiti di progetto, 9.000 celle della matrice.
	big := request{id: db.NewID(), title: BigRequestTitle, state: "In esecuzione", created: now.AddDate(0, -8, 0)}
	g.elem(x, big.id, x.space, x.typ, demNum(), big.title, "Richiesta con molti impatti e requisiti, per misurare la matrice.", state{x.states["In esecuzione"], "in_progress"},
		nil, x.admin, nil, nil, nil, nil, fmt.Sprintf(`{"urgenza":%q}`, x.urgency[0]), big.created, now.Add(-time.Hour))
	g.add("ext_demand", []string{"tenant_id", "element_id", "requesting_unit_id", "driver", "is_mandatory", "estimate_low_cents", "estimate_high_cents", "currency"},
		x.tenant, big.id, x.units[0], "strategic", false, int64(200_000_000), int64(300_000_000), "EUR")
	g.add("stakeholders", []string{"tenant_id", "element_id", "role_id", "member_id", "group_id"}, x.tenant, big.id, x.roles["requester"], x.admin, nil)
	var bigImpacts []string
	for k := 0; k < 30; k++ {
		bigImpacts = append(bigImpacts, addImpact(big, r.apps[k], "In realizzazione", big.created))
	}
	for q := 0; q < 300; q++ {
		id := db.NewID()
		g.elem(x, id, x.space, r.types["requirement"], demNum(), fmt.Sprintf("%s (%03d)", pick(rnd, reqTitles), q+1), "", st("requirement", "In realizzazione"),
			nil, x.admin, big.id, nil, nil, nil, "{}", big.created, now.Add(-time.Hour))
		g.add("ext_requirement", reqCols, x.tenant, id, big.id, nil, "project", "manual", pick(rnd, categories), "Verifica manuale", pick(rnd, r.revTeams), "implementing", big.created)
		for _, imp := range bigImpacts {
			progress := pick(rnd, []string{"open", "open", "to_verify", "verified"})
			var implBy, implAt, verBy, verAt any
			if progress != "open" {
				implBy, implAt = pick(rnd, x.members), now.Add(-48*time.Hour)
			}
			if progress == "verified" {
				verBy, verAt = pick(rnd, x.members), now.Add(-24*time.Hour)
			}
			g.add("applicability", cellCols, x.tenant, db.NewID(), id, imp, big.id, "applicable", progress, implBy, implAt, verBy, verAt, big.created, now)
		}
		counts["requisiti"]++
	}
	// Notifiche e osservazioni dell'amministratore (inbox personali piene).
	for k := 0; k < 3000; k++ {
		req := reqs[rnd.IntN(len(reqs))]
		at := between(rnd, now.AddDate(0, -3, 0), now)
		var read any
		if k%4 > 0 {
			read = at
		}
		g.add("notifications", notifCols, x.tenant, db.NewID(), x.admin, req.id, "comment", `{"excerpt":"Nuovo commento"}`, read, at)
		counts["notifiche"]++
	}
	if err := g.flush(ctx, d); err != nil {
		return err
	}
	// Numeratori degli spazi, appartenenza dell'amministratore ai team valutatori (inbox delle
	// valutazioni e dei requisiti) e sprint: chiusi quelli passati, attivo quello di oggi.
	if _, err := d.Owner.Exec(ctx, "update spaces set next_number = $2 where id = $1", x.space, next); err != nil {
		return err
	}
	for i, sp := range r.spaces {
		if _, err := d.Owner.Exec(ctx, "update spaces set next_number = (select coalesce(max(number), 0) + 1 from elements where space_id = $1) where id = $1", sp); err != nil {
			return err
		}
		for _, s := range r.sprints[i] {
			name, started, closed := "Pianificato", any(nil), any(nil)
			switch {
			case s.end.Add(24 * time.Hour).Before(now):
				name, started, closed = "Chiuso", s.start, s.end
			case !s.start.After(now):
				name, started = "Attivo", s.start
			}
			ss := st("sprint", name)
			if _, err := d.Owner.Exec(ctx, `update elements set state_id = $2, resolved_at = $3 where id = $1`, s.id, ss.id, closed); err != nil {
				return err
			}
			if _, err := d.Owner.Exec(ctx, `update ext_sprint set started_at = $2, closed_at = $3 where element_id = $1`, s.id, started, closed); err != nil {
				return err
			}
		}
	}
	if _, err := d.Owner.Exec(ctx, `insert into group_members (tenant_id, group_id, member_id)
		select $1, group_id, $2 from review_teams where tenant_id = $1 on conflict do nothing`, x.tenant, x.admin); err != nil {
		return err
	}
	log.Info("moduli caricati", "conteggi", counts)
	return nil
}

func loadRequests(ctx context.Context, d *db.DB, x *ids) ([]request, error) {
	rows, err := d.Owner.Query(ctx, `select e.id, e.title, o.name, e.created_at from elements e join options o on o.id = e.state_id
		where e.tenant_id = $1 and e.type_id = $2 order by e.number`, x.tenant, x.typ)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (request, error) {
		var q request
		err := r.Scan(&q.id, &q.title, &q.state, &q.created)
		q.title = strings.TrimSpace(q.title)
		return q, err
	})
}
