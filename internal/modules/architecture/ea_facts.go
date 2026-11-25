package architecture

// Fatti dell'architettura enterprise per le condizioni di processi e regole
// (replica/architettura-ea.md §8).
//
// Applicazione: capability (nomi e codici, comprese le capability che le contengono),
// dipendenti (applicazioni che ne dipendono, a tutti i livelli), dipendenti_diretti,
// dipende_da (nomi e codici degli elementi da cui dipende direttamente), time,
// fine_supporto_giorni (giorni alla fine supporto più vicina tra le sue tecnologie).
// Impatto: dipendenti, time e fine_supporto_giorni della sua applicazione.
// Richiesta: dipendenti_max tra le applicazioni impattate.
// Esempio di regola: "dipendenti: >=5" sull'impatto apre una design review.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// depEdgesSQL: archi "dependent dipende da dependency" tra le relazioni dei tipi
// dell'architettura, secondo il verso di ogni tipo.
const depEdgesSQL = `select case when rt.impact_direction = 'reverse' then r.target_id else r.source_id end as dependent,
	       case when rt.impact_direction = 'reverse' then r.source_id else r.target_id end as dependency
	from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id
	where rt.ea_category is not null and rt.impact_direction <> 'none'`

// appDependents conta le applicazioni (in esercizio o no, non cancellate) che dipendono
// da id, a tutti i livelli. "union" scarta le righe già viste: i cicli terminano.
func appDependents(ctx context.Context, tx pgx.Tx, id string) (all, direct int, err error) {
	err = tx.QueryRow(ctx, `with recursive edges as materialized (`+depEdgesSQL+`),
		d(id, lvl) as (
			select dependent, 1 from edges where dependency = $1
			union
			select e.dependent, 2 from edges e join d on e.dependency = d.id
		)
		select count(distinct x.id), count(distinct x.id) filter (where exists (select 1 from edges where dependency = $1 and dependent = x.id))
		from d join elements x on x.id = d.id and x.deleted_at is null
		join element_types t on t.tenant_id = x.tenant_id and t.id = x.type_id and t.key = 'application'
		where d.id <> $1`, id).Scan(&all, &direct)
	return all, direct, err
}

type appEA struct {
	caps       []any
	dependsOn  []any
	time       *string
	eosDays    *int
	dependents int
	direct     int
}

func loadAppEA(ctx context.Context, tx pgx.Tx, id string) (*appEA, error) {
	a := &appEA{}
	var err error
	if a.dependents, a.direct, err = appDependents(ctx, tx, id); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `with recursive up(id) as (
			select r.target_id from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'supports'
			where r.source_id = $1
			union select e.parent_id from elements e join up on e.id = up.id where e.parent_id is not null
		)
		select e.title, c.code from up join elements e on e.id = up.id and e.deleted_at is null
		join ext_capability c on c.tenant_id = e.tenant_id and c.element_id = e.id`, id)
	if err != nil {
		return nil, err
	}
	var title string
	var code *string
	if _, err := pgx.ForEachRow(rows, []any{&title, &code}, func() error {
		a.caps = append(a.caps, title)
		if code != nil && *code != "" {
			a.caps = append(a.caps, *code)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `with edges as (`+depEdgesSQL+`)
		select distinct e.title, coalesce(a.code, c.code) from edges
		join elements e on e.id = edges.dependency and e.deleted_at is null
		left join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		left join ext_capability c on c.tenant_id = e.tenant_id and c.element_id = e.id
		where edges.dependent = $1`, id)
	if err != nil {
		return nil, err
	}
	if _, err := pgx.ForEachRow(rows, []any{&title, &code}, func() error {
		a.dependsOn = append(a.dependsOn, title)
		if code != nil && *code != "" {
			a.dependsOn = append(a.dependsOn, *code)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	var eos *Day
	err = tx.QueryRow(ctx, `select a.time_rating, (select min(coalesce(least(t.end_of_support, t.end_of_life), t.end_of_support, t.end_of_life))
		from application_technologies at join technologies t on t.tenant_id = at.tenant_id and t.id = at.technology_id where at.application_id = a.element_id)
		from ext_application a where a.element_id = $1`, id).Scan(&a.time, &eos)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	a.eosDays = daysLeft(eos)
	return a, nil
}

func (a *appEA) set(f engine.Facts, withLists bool) {
	f.Set("dipendenti", a.dependents)
	if a.time != nil {
		f.Set("time", *a.time, timeNames[*a.time])
	}
	if a.eosDays != nil {
		f.Set("fine_supporto_giorni", *a.eosDays)
	}
	if withLists {
		f.Set("dipendenti_diretti", a.direct)
		f.Set("capability", a.caps...)
		f.Set("dipende_da", a.dependsOn...)
	}
}

func (s *Service) eaAppFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	a, err := loadAppEA(ctx, tx, el.ID)
	if err != nil {
		return err
	}
	a.set(f, true)
	return nil
}

func (s *Service) eaImpactFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	var appID string
	err := tx.QueryRow(ctx, `select application_id from ext_impact where element_id = $1`, el.ID).Scan(&appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	a, err := loadAppEA(ctx, tx, appID)
	if err != nil {
		return err
	}
	a.set(f, false)
	f.Set("capability", a.caps...)
	return nil
}

func (s *Service) eaRequestFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	rows, err := tx.Query(ctx, `select i.application_id from ext_impact i
		join elements e on e.tenant_id = i.tenant_id and e.id = i.element_id and e.deleted_at is null where i.request_id = $1`, el.ID)
	if err != nil {
		return err
	}
	apps, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(apps) == 0 {
		return err
	}
	max := 0
	for _, id := range apps {
		n, _, err := appDependents(ctx, tx, id)
		if err != nil {
			return err
		}
		if n > max {
			max = n
		}
	}
	f.Set("dipendenti_max", max)
	return nil
}

func registerEA(s *Service, eng *engine.Service) {
	eng.Facts(AppType, s.eaAppFacts)
	eng.Facts(ImpactType, s.eaImpactFacts)
	eng.Facts(RequestType, s.eaRequestFacts)
	times := []string{"tollerare", "investire", "migrare", "eliminare"}
	eng.DescribeFacts(AppType,
		engine.FactInfo{Key: "capability", Name: "Capability supportate (anche quelle che le contengono)", Kind: "text"},
		engine.FactInfo{Key: "dipendenti", Name: "Applicazioni che ne dipendono (tutti i livelli)", Kind: "number", Example: "5"},
		engine.FactInfo{Key: "dipendenti_diretti", Name: "Applicazioni che ne dipendono direttamente", Kind: "number", Example: "2"},
		engine.FactInfo{Key: "dipende_da", Name: "Elementi da cui dipende direttamente", Kind: "text"},
		engine.FactInfo{Key: "time", Name: "Valutazione TIME", Kind: "text", Values: times},
		engine.FactInfo{Key: "fine_supporto_giorni", Name: "Giorni alla fine supporto delle tecnologie", Kind: "number", Example: "90"},
	)
	eng.DescribeFacts(ImpactType,
		engine.FactInfo{Key: "dipendenti", Name: "Applicazioni che dipendono dall'applicazione impattata", Kind: "number", Example: "5"},
		engine.FactInfo{Key: "capability", Name: "Capability dell'applicazione impattata", Kind: "text"},
		engine.FactInfo{Key: "time", Name: "Valutazione TIME dell'applicazione", Kind: "text", Values: times},
		engine.FactInfo{Key: "fine_supporto_giorni", Name: "Giorni alla fine supporto dell'applicazione", Kind: "number", Example: "90"},
	)
	eng.DescribeFacts(RequestType,
		engine.FactInfo{Key: "dipendenti_max", Name: "Massimo di applicazioni dipendenti tra quelle impattate", Kind: "number", Example: "5"},
	)
	raw, err := starters.ReadFile("starter/capability_standard.json")
	if err != nil {
		panic(err)
	}
	eng.Starter(raw)
}
