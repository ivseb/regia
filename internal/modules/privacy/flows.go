package privacy

import (
	"context"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Flussi di dati: per ogni trattamento le applicazioni che lo supportano e le dipendenze tra loro
// dal grafo dell'architettura (relazioni EA tra applicazioni), con i tipi di dato dichiarati nel
// catalogo (personali, credenziali, finanziari). Le applicazioni con dati personali senza un
// trattamento collegato sono segnalate a parte.

// FlowNode è un'applicazione nel flusso di un trattamento.
type FlowNode struct {
	AppRef
	// Core: l'applicazione è collegata al trattamento; false = è solo vicina nel grafo.
	Core bool `json:"core"`
	// Linked: l'applicazione è collegata ad almeno un trattamento attivo.
	Linked bool `json:"linked"`
}

// FlowEdge è una dipendenza tra due applicazioni.
type FlowEdge struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Name   string `json:"name"`
	Source string `json:"source"`
	Target string `json:"target"`
	Data   string `json:"data"`
}

// TreatmentFlow è il flusso di un trattamento.
type TreatmentFlow struct {
	Treatment TreatmentRef `json:"treatment"`
	Nodes     []FlowNode   `json:"nodes"`
	Edges     []FlowEdge   `json:"edges"`
	// Exposed: applicazioni vicine che hanno dati personali ma non sono collegate a questo trattamento.
	Exposed int `json:"exposed"`
}

// Flows è la risposta della vista dei flussi.
type Flows struct {
	Treatments []TreatmentFlow `json:"treatments"`
	// Unlinked: applicazioni con dati personali senza trattamento collegato.
	Unlinked []AppRef `json:"unlinked"`
	// Counts: applicazioni del catalogo per tipo di dato dichiarato.
	Personal    int `json:"personal"`
	Credentials int `json:"credentials"`
	Financial   int `json:"financial"`
}

// catalog legge le applicazioni del catalogo e dice quali sono collegate a un trattamento attivo.
func catalog(ctx context.Context, tx pgx.Tx) (map[string]AppRef, map[string]bool, error) {
	rows, err := tx.Query(ctx, `select e.id::text, coalesce(s.key || '-' || e.number, ''), e.title, coalesce(a.data_classification, '')
		from ext_application a join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id and e.deleted_at is null and not e.is_draft
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id`)
	if err != nil {
		return nil, nil, err
	}
	apps := map[string]AppRef{}
	for rows.Next() {
		var a AppRef
		if err := rows.Scan(&a.ID, &a.Key, &a.Name, &a.Classification); err != nil {
			rows.Close()
			return nil, nil, err
		}
		a.Personal, a.Credentials, a.Financial = dataKinds(a.Classification)
		apps[a.ID] = a
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	lr, err := tx.Query(ctx, `select distinct ta.application_id::text from privacy_treatment_apps ta
		join privacy_treatments t on t.tenant_id = ta.tenant_id and t.id = ta.treatment_id and t.status = 'active'`)
	if err != nil {
		return nil, nil, err
	}
	linked := map[string]bool{}
	ids, err := pgx.CollectRows(lr, pgx.RowTo[string])
	if err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		linked[id] = true
	}
	return apps, linked, nil
}

func unlinkedOf(apps map[string]AppRef, linked map[string]bool) []AppRef {
	out := []AppRef{}
	for id, a := range apps {
		if a.Personal && !linked[id] {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b AppRef) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// Flows compone i flussi di dati (privacy.read).
func (s *Service) Flows(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Flows, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	apps, linked, err := catalog(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := &Flows{Treatments: []TreatmentFlow{}, Unlinked: unlinkedOf(apps, linked)}
	for _, a := range apps {
		if a.Personal {
			out.Personal++
		}
		if a.Credentials {
			out.Credentials++
		}
		if a.Financial {
			out.Financial++
		}
	}
	// Dipendenze tra applicazioni (relazioni del grafo EA con entrambi gli estremi applicazioni).
	er, err := tx.Query(ctx, `select r.id::text, rt.key, rt.outward_name, r.source_id::text, r.target_id::text, coalesce(r.attrs->>'data', '')
		from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.ea_category is not null
		join ext_application sa on sa.tenant_id = r.tenant_id and sa.element_id = r.source_id
		join ext_application ta on ta.tenant_id = r.tenant_id and ta.element_id = r.target_id
		order by r.id`)
	if err != nil {
		return nil, err
	}
	edges, err := pgx.CollectRows(er, pgx.RowToStructByPos[FlowEdge])
	if err != nil {
		return nil, err
	}
	list, err := s.Treatments(ctx, tx, actor, false)
	if err != nil {
		return nil, err
	}
	for _, t := range list {
		core := map[string]bool{}
		for _, a := range t.Applications {
			core[a.ID] = true
		}
		f := TreatmentFlow{Treatment: TreatmentRef{ID: t.ID, Name: t.Name}, Nodes: []FlowNode{}, Edges: []FlowEdge{}}
		seen := map[string]bool{}
		add := func(id string, isCore bool) {
			if seen[id] {
				return
			}
			a, ok := apps[id]
			if !ok {
				return
			}
			seen[id] = true
			f.Nodes = append(f.Nodes, FlowNode{AppRef: a, Core: isCore, Linked: linked[id]})
			if !isCore && a.Personal && !core[id] {
				f.Exposed++
			}
		}
		for _, a := range t.Applications {
			add(a.ID, true)
		}
		for _, e := range edges {
			if core[e.Source] || core[e.Target] {
				if _, ok := apps[e.Source]; !ok {
					continue
				}
				if _, ok := apps[e.Target]; !ok {
					continue
				}
				add(e.Source, core[e.Source])
				add(e.Target, core[e.Target])
				f.Edges = append(f.Edges, e)
			}
		}
		out.Treatments = append(out.Treatments, f)
	}
	return out, nil
}
