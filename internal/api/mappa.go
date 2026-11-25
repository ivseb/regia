package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/auth"
)

// Mappa d'insieme dei processi (ondata «semplicità»): sola lettura, per tutti.
//
//	GET /api/v1/mappa-processi   per ogni processo pubblicato: fasi in fila e, agganciati alle fasi,
//	                              team coinvolti, valutazioni aperte da regole, approvazioni, gate e azioni.
//
// Legge le versioni pubblicate (process_versions.definition), i tipi di valutazione e le politiche di
// approvazione: non scrive nulla e non porta in giro altro che nomi e condizioni.
func (s *Server) mappaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/mappa-processi", authed(s.mappaProcessi))
}

// mapItem è qualcosa che si aggancia a una fase della mappa.
type mapItem struct {
	// Kind: review | approval | gate | action.
	Kind string `json:"kind"`
	// Key: tipo di valutazione, chiave del gate, tipo di azione o fase dell'approvazione.
	Key  string `json:"key"`
	Name string `json:"name"`
	// On: quando scatta rispetto alla fase: create | enter | exit | transition.
	On string `json:"on"`
	// When: condizione (testo grezzo; l'interfaccia la riscrive in parole).
	When string `json:"when,omitempty"`
	// Scope: per le valutazioni, request | impact.
	Scope string `json:"scope,omitempty"`
	// Team: team valutatore o gruppo coinvolto.
	Team      string `json:"team,omitempty"`
	Mandatory bool   `json:"mandatory,omitempty"`
	// Rule: chiave della regola che lo produce (apre l'editor del processo su quella regola).
	Rule string `json:"rule,omitempty"`
	// Steps: per le approvazioni, i passi in sequenza (ogni passo: chi approva).
	Steps [][]string `json:"steps,omitempty"`
	// Missing: la configurazione a cui rimanda non esiste più (tipo di valutazione archiviato, politica assente…).
	Missing bool `json:"missing,omitempty"`
}

type mapPhase struct {
	Name     string    `json:"name"`
	Category string    `json:"category"`
	OffPath  bool      `json:"off_path,omitempty"`
	Initial  bool      `json:"initial,omitempty"`
	Items    []mapItem `json:"items"`
}

type mapLink struct {
	// Kind: review | epic.
	Kind  string `json:"kind"`
	Scope string `json:"scope,omitempty"`
	Phase string `json:"phase"`
}

type mapProcess struct {
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	TypeKey     string     `json:"type_key"`
	TypeName    string     `json:"type_name"`
	Version     int        `json:"version"`
	AppliesWhen string     `json:"applies_when,omitempty"`
	Phases      []mapPhase `json:"phases"`
	Teams       []string   `json:"teams"`
	Links       []mapLink  `json:"links"`
}

type mapOut struct {
	Processes []mapProcess `json:"processes"`
	// Unpublished: processi che esistono ma non hanno una versione pubblicata (non governano nessun elemento).
	Unpublished []mapUnpublished `json:"unpublished"`
}

type mapUnpublished struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	TypeName string `json:"type_name"`
}

// mapProcRow è una riga letta dal database: il processo con la sua versione pubblicata.
type mapProcRow struct {
	Key, Name, TypeKey, TypeName, AppliesWhen string
	Version                                   int
	Definition                                []byte
}

type mapReviewType struct {
	Name, Team, Scope string
	Mandatory         bool
	Archived          bool
}

type mapPolicy struct {
	Steps [][]string
}

// buildMap trasforma le definizioni pubblicate in mappa. Funzione pura (testabile senza database).
// policies è indicizzata da «tipo|fase» in minuscolo, con «|fase» per le politiche valide per tutti i tipi.
func buildMap(rows []mapProcRow, reviews map[string]mapReviewType, policies map[string]mapPolicy) []mapProcess {
	out := []mapProcess{}
	for _, r := range rows {
		var w engine.Workflow
		if json.Unmarshal(r.Definition, &w) != nil {
			continue
		}
		p := mapProcess{Key: r.Key, Name: r.Name, TypeKey: r.TypeKey, TypeName: r.TypeName, Version: r.Version,
			AppliesWhen: r.AppliesWhen, Phases: []mapPhase{}, Teams: []string{}, Links: []mapLink{}}
		idx := map[string]int{}
		for _, ph := range w.Phases {
			idx[ph.Name] = len(p.Phases)
			p.Phases = append(p.Phases, mapPhase{Name: ph.Name, Category: ph.Category, OffPath: ph.OffPath,
				Initial: ph.Name == w.Initial, Items: []mapItem{}})
		}
		teamSeen := map[string]bool{}
		add := func(phase string, it mapItem) {
			i, ok := idx[phase]
			if !ok {
				return
			}
			p.Phases[i].Items = append(p.Phases[i].Items, it)
			if it.Team != "" && !teamSeen[it.Team] {
				teamSeen[it.Team] = true
				p.Teams = append(p.Teams, it.Team)
			}
		}
		// Regole: «create» sulla fase iniziale, «enter:X» / «exit:X» sulla fase X.
		for _, rule := range w.Rules {
			on, phase := "create", w.Initial
			if k, ph, ok := strings.Cut(rule.On, ":"); ok {
				on, phase = k, ph
			}
			for _, a := range rule.Actions {
				it := mapItem{Kind: "action", Key: a.Kind, Name: rule.Name, On: on, When: joinWhen(rule.When, a.When), Rule: rule.Key}
				switch a.Kind {
				case "open_review":
					rt, ok := reviews[a.Ref]
					it.Kind, it.Key = "review", a.Ref
					it.Name, it.Team, it.Scope, it.Mandatory = rt.Name, rt.Team, rt.Scope, rt.Mandatory
					if !ok || rt.Archived {
						it.Missing = true
						it.Name = a.Ref
					}
					p.Links = append(p.Links, mapLink{Kind: "review", Scope: rt.Scope, Phase: phase})
				case "create_epic":
					p.Links = append(p.Links, mapLink{Kind: "epic", Phase: phase})
				case "assign_role":
					it.Team = a.Group
					it.Key = a.Role
				}
				add(phase, it)
			}
		}
		// Passaggi: gate e approvazioni si agganciano alla fase di arrivo.
		for _, tr := range w.Transitions {
			i, ok := idx[tr.To]
			if !ok {
				continue
			}
			for _, gk := range tr.Gates {
				g := engine.Gate{Key: gk, Name: gk}
				for _, x := range w.Gates {
					if x.Key == gk {
						g = x
					}
				}
				if hasItem(p.Phases[i].Items, "gate", g.Key) {
					continue
				}
				add(tr.To, mapItem{Kind: "gate", Key: g.Key, Name: g.Name, On: "transition", When: g.Condition})
			}
			if tr.Approval != "" && !hasItem(p.Phases[i].Items, "approval", tr.To) {
				it := mapItem{Kind: "approval", Key: tr.To, Name: tr.To, On: "transition", When: tr.When}
				pol, ok := policies[strings.ToLower(r.TypeKey+"|"+tr.To)]
				if !ok {
					pol, ok = policies[strings.ToLower("|"+tr.To)]
				}
				if ok {
					it.Steps = pol.Steps
				} else {
					it.Missing = true
				}
				add(tr.To, it)
			}
		}
		out = append(out, p)
	}
	return out
}

func joinWhen(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return "(" + a + ") + (" + b + ")"
}

func hasItem(items []mapItem, kind, key string) bool {
	for _, i := range items {
		if i.Kind == kind && i.Key == key {
			return true
		}
	}
	return false
}

func (s *Server) mappaProcessi(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if p.TenantID == "" {
		writeProblem(w, r, http.StatusForbidden, "tenant.required", "nessun tenant attivo")
		return
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*mapOut, error) {
		return loadMappa(r.Context(), tx)
	})
}

func loadMappa(ctx context.Context, tx pgx.Tx) (*mapOut, error) {
	rows, err := tx.Query(ctx, `select p.key, p.name, et.key, et.name, p.applies_when, v.number, v.definition
		from process_definitions p
		join process_versions v on v.process_id = p.id and v.status = 'published'
		join element_types et on et.id = p.type_id
		where not p.archived order by et.name, p.priority, p.key`)
	if err != nil {
		return nil, err
	}
	procs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (mapProcRow, error) {
		var m mapProcRow
		err := r.Scan(&m.Key, &m.Name, &m.TypeKey, &m.TypeName, &m.AppliesWhen, &m.Version, &m.Definition)
		return m, err
	})
	if err != nil {
		return nil, err
	}
	rr, err := tx.Query(ctx, `select t.key, t.name, rt.name, t.scope, t.mandatory, t.archived
		from review_types t join review_teams rt on rt.id = t.team_id`)
	if err != nil {
		return nil, err
	}
	reviews := map[string]mapReviewType{}
	var key string
	var rv mapReviewType
	if _, err := pgx.ForEachRow(rr, []any{&key, &rv.Name, &rv.Team, &rv.Scope, &rv.Mandatory, &rv.Archived}, func() error {
		reviews[key] = rv
		return nil
	}); err != nil {
		return nil, err
	}
	pr, err := tx.Query(ctx, `select coalesce(t.key, ''), a.stage, a.rules from approval_policies a
		left join element_types t on t.id = a.type_id where a.active and a.stage not like '@%' order by a.name`)
	if err != nil {
		return nil, err
	}
	policies := map[string]mapPolicy{}
	var tk, stage string
	var raw []byte
	if _, err := pgx.ForEachRow(pr, []any{&tk, &stage, &raw}, func() error {
		var pol struct {
			Rules []struct {
				Steps []struct {
					Approvers []struct{ Role, Body, Member string } `json:"approvers"`
				} `json:"steps"`
			} `json:"rules"`
		}
		if json.Unmarshal(raw, &pol) != nil || len(pol.Rules) == 0 {
			policies[strings.ToLower(tk+"|"+stage)] = mapPolicy{}
			return nil
		}
		// Si mostra la prima regola con passi: è quella che dice chi approva di norma.
		var steps [][]string
		for _, st := range pol.Rules[0].Steps {
			var who []string
			for _, a := range st.Approvers {
				switch {
				case a.Role != "":
					who = append(who, a.Role)
				case a.Body != "":
					who = append(who, a.Body)
				case a.Member != "":
					who = append(who, "member")
				}
			}
			steps = append(steps, who)
		}
		policies[strings.ToLower(tk+"|"+stage)] = mapPolicy{Steps: steps}
		return nil
	}); err != nil {
		return nil, err
	}
	out := &mapOut{Processes: buildMap(procs, reviews, policies)}
	ur, err := tx.Query(ctx, `select p.key, p.name, et.name from process_definitions p join element_types et on et.id = p.type_id
		where not p.archived and not exists (select 1 from process_versions v where v.process_id = p.id and v.status = 'published')
		order by et.name, p.name`)
	if err != nil {
		return nil, err
	}
	out.Unpublished, err = pgx.CollectRows(ur, pgx.RowToStructByPos[mapUnpublished])
	if err != nil {
		return nil, err
	}
	return out, nil
}
