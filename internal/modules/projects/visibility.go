package projects

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// reader ricorda quali elementi l'attore può leggere, così ogni elemento si chiede una volta sola.
type reader struct {
	s     *Service
	actor engine.Actor
	seen  map[string]bool
}

func (s *Service) reader(actor engine.Actor) *reader {
	return &reader{s: s, actor: actor, seen: map[string]bool{}}
}

func (r *reader) can(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	if ok, done := r.seen[id]; done {
		return ok, nil
	}
	ok, err := r.s.eng.CanRead(ctx, tx, r.actor, id)
	if err != nil {
		return false, err
	}
	r.seen[id] = ok
	return ok, nil
}

// limitPlan applica al piano i limiti di visibilità del singolo elemento. Una voce che l'attore
// non può leggere resta nel piano con date, avanzamento e dipendenze, perché il Gantt e i numeri
// del progetto tornino, ma perde titolo, persone, team, fornitore e collegamenti. Le epiche, le
// richieste e il programma non leggibili spariscono o perdono il titolo allo stesso modo.
// Va chiamata dopo loadPlan e prima di computeNumbers, così anche ritardi e blocchi la rispettano.
func (s *Service) limitPlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, p *Plan, r *reader) error {
	if r == nil {
		r = s.reader(actor)
	}
	for i := range p.Items {
		it := &p.Items[i]
		ok, err := r.can(ctx, tx, it.ID)
		if err != nil {
			return err
		}
		if !ok {
			it.Restricted = true
			it.Title = ""
			it.TeamID, it.Team = nil, nil
			it.ApplicationID, it.Application = nil, nil
			it.SupplierID, it.Supplier = nil, nil
			it.AssigneeID, it.Assignee = nil, nil
			it.Impact = nil
			it.Epics = []Ref{}
			if it.Conflicts != nil {
				it.Conflicts = []Conflict{}
			}
			continue
		}
		if err := limitConflicts(ctx, tx, r, it.Conflicts); err != nil {
			return err
		}
		if it.Impact != nil {
			if ok, err := r.can(ctx, tx, it.Impact.ID); err != nil {
				return err
			} else if !ok {
				it.Impact = nil
			}
		}
		epics := it.Epics[:0]
		for _, e := range it.Epics {
			ok, err := r.can(ctx, tx, e.ID)
			if err != nil {
				return err
			}
			if ok {
				epics = append(epics, e)
			}
		}
		it.Epics = epics
	}
	// Le richieste restano (servono al budget aggregato e alle scadenze), senza titolo se riservate.
	for i := range p.Project.Requests {
		rq := &p.Project.Requests[i]
		ok, err := r.can(ctx, tx, rq.ID)
		if err != nil {
			return err
		}
		if !ok {
			rq.Restricted, rq.Title, rq.State = true, "", ""
		}
	}
	// Perimetro della baseline: le voci tolte che l'attore non legge restano senza titolo.
	if b := p.Baseline; b != nil {
		for i := range b.scope {
			ok, err := r.can(ctx, tx, b.scope[i].ID)
			if err != nil {
				return err
			}
			if !ok {
				b.scope[i].Restricted, b.scope[i].Title = true, ""
			}
		}
	}
	if err := limitDeps(ctx, tx, r, p.External); err != nil {
		return err
	}
	if err := limitDeps(ctx, tx, r, p.Dependents); err != nil {
		return err
	}
	if pg := p.Project.Program; pg != nil {
		ok, err := r.can(ctx, tx, pg.ID)
		if err != nil {
			return err
		}
		if !ok {
			p.Project.Program = nil
		}
	}
	return nil
}

// limitRefs toglie dall'elenco i riferimenti che l'attore non può leggere (i rischi collegati).
func limitRefs(ctx context.Context, tx pgx.Tx, r *reader, refs []ItemRef) ([]ItemRef, error) {
	out := refs[:0]
	for _, ir := range refs {
		ok, err := r.can(ctx, tx, ir.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, ir)
		}
	}
	return out, nil
}
