package api

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/auth"
	"regia/internal/platform/rbac"
)

// Percorso guidato dell'amministratore (ondata «semplicità»): le aree da configurare in ordine sensato,
// con lo stato calcolato dai dati veri del tenant e le incoerenze da sistemare. Estende «Inizia da qui»
// (avvio.go) senza duplicarne i passi: quelli restano la checklist minima, questo è la vista a tappe,
// buona sia per un'organizzazione nuova sia per una già attiva.
//
//	GET /api/v1/avvio/percorso   le tappe, con conteggi e incoerenze (tenant.admin)
func (s *Server) percorsoRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/avvio/percorso", can(rbac.TenantAdmin, s.avvioPercorso))
}

// percorsoIssue è un'incoerenza trovata: il codice sceglie la frase, Count dice quante volte.
type percorsoIssue struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

type percorsoModule struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// percorsoTappa: Status è done | partial | todo. Optional: la tappa non conta nel progresso.
type percorsoTappa struct {
	Key       string           `json:"key"`
	Status    string           `json:"status"`
	Optional  bool             `json:"optional,omitempty"`
	NotNeeded bool             `json:"not_needed,omitempty"`
	Counts    map[string]int   `json:"counts"`
	Issues    []percorsoIssue  `json:"issues"`
	Modules   []percorsoModule `json:"modules,omitempty"`
}

type percorsoState struct {
	Tappe []percorsoTappa `json:"tappe"`
	Done  int             `json:"done"`
	Total int             `json:"total"`
	// Fresh: organizzazione appena nata (una persona, nessun team, nessun modello): si parte dalla scelta del modello.
	Fresh  bool   `json:"fresh"`
	Model  string `json:"model"`
	Closed bool   `json:"closed"`
	// Next: la prima tappa non fatta e non facoltativa.
	Next string `json:"next"`
}

// statusFor dà lo stato di una tappa: nessun oggetto = da fare; oggetti con incoerenze = parziale.
func statusFor(count int, issues []percorsoIssue) string {
	if count == 0 {
		return "todo"
	}
	for _, i := range issues {
		if i.Count > 0 {
			return "partial"
		}
	}
	return "done"
}

func keepIssues(in ...percorsoIssue) []percorsoIssue {
	out := []percorsoIssue{}
	for _, i := range in {
		if i.Count > 0 {
			out = append(out, i)
		}
	}
	return out
}

func (s *Server) computePercorso(ctx context.Context, tx pgx.Tx, tenantID string) (*percorsoState, error) {
	off, err := s.modulesOff(ctx, tenantID)
	if err != nil {
		off = map[string]bool{}
	}
	one := func(sql string) (int, error) {
		var n int
		err := tx.QueryRow(ctx, sql).Scan(&n)
		return n, err
	}
	av, err := s.computeAvvio(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	out := &percorsoState{Tappe: []percorsoTappa{}, Model: av.Model, Closed: av.Closed}

	// 1. Organizzazione e persone.
	people, err := one(`select count(*) from members where status in ('active','invited') and member_type = 'standard'`)
	if err != nil {
		return nil, err
	}
	invited, err := one(`select count(*) from invitations`)
	if err != nil {
		return nil, err
	}
	units, err := one(`select count(*) from org_units`)
	if err != nil {
		return nil, err
	}
	t1 := percorsoTappa{Key: "persone", Counts: map[string]int{"people": people, "invited": invited, "units": units}, Issues: keepIssues()}
	switch {
	case people+invited > 1:
		t1.Status = "done"
	case av.Steps[0].Done:
		t1.Status = "partial"
	default:
		t1.Status = "todo"
	}
	out.Tappe = append(out.Tappe, t1)

	// 2. Team.
	teams, err := one(`select count(*) from teams where not archived`)
	if err != nil {
		return nil, err
	}
	noMembers, err := one(`select count(*) from teams t where not archived and not exists
		(select 1 from team_members m where m.team_id = t.id)`)
	if err != nil {
		return nil, err
	}
	noSpace, err := one(`select count(*) from teams where not archived and delivery_space_id is null`)
	if err != nil {
		return nil, err
	}
	t2 := percorsoTappa{Key: "team", Counts: map[string]int{"teams": teams},
		Issues: keepIssues(percorsoIssue{"team_senza_membri", noMembers}, percorsoIssue{"team_senza_spazio", noSpace})}
	t2.Status = statusFor(teams, t2.Issues)
	out.Tappe = append(out.Tappe, t2)

	// Mappa dei processi: serve a processi, valutazioni e approvazioni (incoerenze tra regole e configurazione).
	mp, err := loadMappa(ctx, tx)
	if err != nil {
		return nil, err
	}
	referenced := map[string]bool{}
	missingReview, missingApproval := 0, 0
	for _, p := range mp.Processes {
		for _, ph := range p.Phases {
			for _, it := range ph.Items {
				switch it.Kind {
				case "review":
					referenced[it.Key] = true
					if it.Missing {
						missingReview++
					}
				case "approval":
					if it.Missing {
						missingApproval++
					}
				}
			}
		}
	}

	// 3. Tipi di richiesta e processi.
	published := len(mp.Processes)
	custom, err := one(`select count(*) from process_versions where status = 'published' and number > 1`)
	if err != nil {
		return nil, err
	}
	types, err := one(`select count(*) from element_types`)
	if err != nil {
		return nil, err
	}
	t3 := percorsoTappa{Key: "processi", NotNeeded: off["demand"],
		Counts: map[string]int{"processes": published, "types": types, "custom": custom},
		Issues: keepIssues(percorsoIssue{"processo_non_pubblicato", len(mp.Unpublished)})}
	t3.Status = statusFor(published, t3.Issues)
	if t3.Status == "done" && custom == 0 && av.Model == "" {
		// Solo i processi di partenza: vanno bene, ma non sono stati ancora adattati.
		t3.Status = "partial"
		t3.Issues = append(t3.Issues, percorsoIssue{"processo_di_partenza", published})
	}
	out.Tappe = append(out.Tappe, t3)

	// 4. Valutazioni e approvazioni.
	rteams, err := one(`select count(*) from review_teams where not archived`)
	if err != nil {
		return nil, err
	}
	rtypes, err := one(`select count(*) from review_types where not archived`)
	if err != nil {
		return nil, err
	}
	policies, err := one(`select count(*) from approval_policies where active and stage not like '@%'`)
	if err != nil {
		return nil, err
	}
	emptyTeams, err := one(`select count(*) from review_teams t where not archived and not exists
		(select 1 from group_members g where g.group_id = t.group_id)`)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select key from review_types where not archived`)
	if err != nil {
		return nil, err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	unused := 0
	for _, k := range keys {
		if !referenced[k] {
			unused++
		}
	}
	t4 := percorsoTappa{Key: "valutazioni", NotNeeded: off["demand"],
		Counts: map[string]int{"review_teams": rteams, "review_types": rtypes, "policies": policies},
		Issues: keepIssues(percorsoIssue{"valutazione_team_senza_membri", emptyTeams},
			percorsoIssue{"valutazione_senza_regola", unused},
			percorsoIssue{"regola_valutazione_mancante", missingReview},
			percorsoIssue{"approvazione_senza_politica", missingApproval})}
	t4.Status = statusFor(rtypes+policies, t4.Issues)
	if t4.Status == "todo" && rteams > 0 {
		t4.Status = "partial"
	}
	out.Tappe = append(out.Tappe, t4)

	// 5. Spazi di lavoro.
	spaces, err := one(`select count(*) from spaces where not archived`)
	if err != nil {
		return nil, err
	}
	noLead, err := one(`select count(*) from spaces where not archived and lead_id is null`)
	if err != nil {
		return nil, err
	}
	t5 := percorsoTappa{Key: "spazi", Counts: map[string]int{"spaces": spaces}, Issues: keepIssues(percorsoIssue{"spazio_senza_responsabile", noLead})}
	t5.Status = statusFor(spaces, t5.Issues)
	out.Tappe = append(out.Tappe, t5)

	// 6. Moduli facoltativi: non contano nel progresso, ma si vede cosa è acceso.
	t6 := percorsoTappa{Key: "moduli", Optional: true, Counts: map[string]int{}, Issues: []percorsoIssue{}, Modules: []percorsoModule{}}
	on := 0
	for _, m := range moduleDefs {
		if m.Key == "demand" || m.Key == "work" {
			continue
		}
		e := !off[m.Key]
		if e {
			on++
		}
		t6.Modules = append(t6.Modules, percorsoModule{Key: m.Key, Name: m.Name, Enabled: e})
	}
	t6.Counts["enabled"], t6.Counts["total"] = on, len(t6.Modules)
	configured, err := one(`select count(*) from tenant_modules`)
	if err != nil {
		return nil, err
	}
	if configured > 0 || av.Model != "" {
		t6.Status = "done"
	} else {
		t6.Status = "todo"
	}
	out.Tappe = append(out.Tappe, t6)

	for _, t := range out.Tappe {
		if t.Optional || t.NotNeeded {
			continue
		}
		out.Total++
		if t.Status == "done" {
			out.Done++
		} else if out.Next == "" {
			out.Next = t.Key
		}
	}
	out.Fresh = people+invited <= 1 && teams == 0 && av.Model == ""
	return out, nil
}

func (s *Server) avvioPercorso(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*percorsoState, error) {
		return s.computePercorso(r.Context(), tx, p.TenantID)
	})
}
