package devsecops

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Rilascio → elementi e «in produzione dal…» (docs/sviluppo/commit-elementi.md).
//
// Un rilascio contiene i commit dello stesso servizio fra il rilascio precedente nello stesso
// ambiente (esclusi i gemelli con la stessa versione) e questo. Un elemento è in produzione
// quando, per ogni servizio su cui ha commit, c'è un deploy di produzione riuscito (o un
// rilascio di produzione) con data ≥ del suo ultimo commit su quel servizio, di una versione
// che non era già in produzione prima di quel commit; la data è la più recente fra quelle.

// Gruppi delle note di rilascio.
const (
	NoteNew   = "new"   // novità
	NoteFix   = "fix"   // correzioni
	NoteOther = "other" // altro
)

// noteGroup sceglie il gruppo delle note dal tipo dell'elemento.
func noteGroup(typeKey string) string {
	switch typeKey {
	case "story", "epic", "feature", "change_request", "business_request", "requirement":
		return NoteNew
	case "bug", "incident", "problem", "defect", "vulnerability":
		return NoteFix
	}
	return NoteOther
}

// ReleaseItem è un elemento contenuto in un rilascio.
type ReleaseItem struct {
	ID            string `json:"id"`
	Key           string `json:"key"`
	Title         string `json:"title"`
	Type          string `json:"type"`
	TypeName      string `json:"type_name"`
	State         string `json:"state"`
	StateCategory string `json:"state_category"`
	Group         string `json:"group"`
	Commits       int    `json:"commits"`
	// Continues: l'elemento ha commit successivi su questo servizio (il lavoro prosegue in un
	// rilascio seguente).
	Continues bool `json:"continues"`
}

// ReleaseWindow è l'intervallo dei commit di un rilascio: (From, To].
type ReleaseWindow struct {
	From            *time.Time `json:"from"`
	To              time.Time  `json:"to"`
	PreviousVersion *string    `json:"previous_version"`
}

// ReleaseDetail è la risposta di GET /api/v1/devsecops/releases/{id}.
type ReleaseDetail struct {
	Release ReleaseView   `json:"release"`
	Window  ReleaseWindow `json:"window"`
	Items   []ReleaseItem `json:"items"`
	// Hidden: elementi del rilascio che chi guarda non può leggere (solo il numero).
	Hidden int `json:"hidden"`
	// Commits: commit legati a un elemento nella finestra (anche quelli degli elementi nascosti).
	Commits int `json:"commits"`
}

const maxReleaseItems = 500

// ReleaseDetailOf restituisce il rilascio e gli elementi che contiene.
func (s *Service) ReleaseDetailOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*ReleaseDetail, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	if !engine.IsUUID(id) {
		return nil, engine.ErrNotFound
	}
	rels, err := loadReleases(ctx, tx, "r.id = $1", id)
	if err != nil {
		return nil, err
	}
	if len(rels) == 0 {
		return nil, engine.ErrNotFound
	}
	out := &ReleaseDetail{Release: rels[0], Items: []ReleaseItem{}}
	items, w, total, err := s.releaseItems(ctx, tx, actor, id)
	if err != nil {
		return nil, err
	}
	out.Window = w
	out.Commits = total
	for _, it := range items {
		if it.ID == "" {
			out.Hidden++
			continue
		}
		out.Items = append(out.Items, it)
	}
	return out, nil
}

// releaseWindow calcola l'intervallo dei commit del rilascio. To è il primo deploy riuscito
// di questa versione nell'ambiente (o la data del rilascio, se precede); From è l'ultimo
// deploy riuscito, o rilascio, di un'altra versione prima di To.
func releaseWindow(ctx context.Context, tx pgx.Tx, releaseID string) (svcID string, w ReleaseWindow, err error) {
	var envID, version string
	var at time.Time
	if err = tx.QueryRow(ctx, `select service_id::text, environment_id::text, version, released_at from releases where id = $1`, releaseID).
		Scan(&svcID, &envID, &version, &at); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = engine.ErrNotFound
		}
		return
	}
	var first *time.Time
	if err = tx.QueryRow(ctx, `select min(started_at) from deployments where service_id = $1 and environment_id = $2
		and lower(version) = lower($3) and status = 'succeeded'`, svcID, envID, version).Scan(&first); err != nil {
		return
	}
	w.To = at.UTC()
	if first != nil && first.Before(w.To) {
		w.To = first.UTC()
	}
	var prevAt *time.Time
	var prevVer *string
	err = tx.QueryRow(ctx, `select x.at, x.version from (
			select d.started_at as at, d.version from deployments d
			where d.service_id = $1 and d.environment_id = $2 and d.status = 'succeeded' and d.started_at < $3 and lower(d.version) <> lower($4)
			union all
			select r.released_at, r.version from releases r
			where r.service_id = $1 and r.environment_id = $2 and r.released_at < $3 and lower(r.version) <> lower($4)
		) x order by x.at desc limit 1`, svcID, envID, w.To, version).Scan(&prevAt, &prevVer)
	if errors.Is(err, pgx.ErrNoRows) {
		return svcID, w, nil
	}
	if err != nil {
		return
	}
	if prevAt != nil {
		t := prevAt.UTC()
		w.From, w.PreviousVersion = &t, prevVer
	}
	return svcID, w, nil
}

// releaseItems: gli elementi del rilascio, con quelli che l'attore non legge lasciati con ID
// vuoto (il chiamante li conta). Restituisce anche il numero di commit nella finestra.
func (s *Service) releaseItems(ctx context.Context, tx pgx.Tx, actor engine.Actor, releaseID string) ([]ReleaseItem, ReleaseWindow, int, error) {
	svcID, w, err := releaseWindow(ctx, tx, releaseID)
	if err != nil {
		return nil, w, 0, err
	}
	var from any
	if w.From != nil {
		from = *w.From
	}
	rows, err := tx.Query(ctx, `select e.id::text, coalesce(sp.key || '-' || e.number, ''), e.title, t.key, t.name, coalesce(o.name, ''), coalesce(o.category, ''),
			x.n, exists (select 1 from element_commits l where l.element_id = x.element_id and l.service_id = $1 and l.committed_at > $3)
		from (select c.element_id, count(*)::int as n, max(c.committed_at) as last_at from element_commits c
			where c.service_id = $1 and ($2::timestamptz is null or c.committed_at > $2) and c.committed_at <= $3
			group by c.element_id) x
		join elements e on e.tenant_id = app_tenant() and e.id = x.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		order by x.last_at desc, e.id limit $4`, svcID, from, w.To, maxReleaseItems)
	if err != nil {
		return nil, w, 0, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReleaseItem, error) {
		var it ReleaseItem
		err := r.Scan(&it.ID, &it.Key, &it.Title, &it.Type, &it.TypeName, &it.State, &it.StateCategory, &it.Commits, &it.Continues)
		it.Group = noteGroup(it.Type)
		return it, err
	})
	if err != nil {
		return nil, w, 0, err
	}
	total := 0
	for i := range items {
		total += items[i].Commits
		ok, err := s.eng.CanRead(ctx, tx, actor, items[i].ID)
		if err != nil {
			return nil, w, 0, err
		}
		if !ok {
			items[i] = ReleaseItem{}
		}
	}
	// Prima le novità, poi le correzioni, poi il resto; dentro il gruppo l'ordine della query.
	order := map[string]int{NoteNew: 0, NoteFix: 1, NoteOther: 2}
	sort.SliceStable(items, func(a, b int) bool { return order[items[a].Group] < order[items[b].Group] })
	return items, w, total, nil
}

// ---------------------------------------------------------------------------
// «In produzione dal…»

// ElementProduction dice se un elemento con commit è in produzione.
type ElementProduction struct {
	InProduction bool       `json:"in_production"`
	At           *time.Time `json:"at"`         // ingresso in produzione (il più recente fra i servizi)
	Version      string     `json:"version"`    // versione del servizio che entra per ultimo
	Service      string     `json:"service"`    // quel servizio
	ReleaseID    *string    `json:"release_id"` // il rilascio di quella versione, se registrato
	FirstAt      time.Time  `json:"first_at"`   // primo commit
	LastAt       time.Time  `json:"last_at"`    // ultimo commit
	// Pending: i servizi con commit non ancora in produzione.
	Pending []string `json:"pending"`
}

// productionOf calcola lo stato di produzione degli elementi indicati che hanno commit (gli
// altri mancano dalla mappa).
func productionOf(ctx context.Context, tx pgx.Tx, ids []string) (map[string]*ElementProduction, error) {
	out := map[string]*ElementProduction{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `select l.element_id::text, sv.name, l.first_at, l.last_at, p.version, p.at, p.release_id
		from (select element_id, service_id, min(committed_at) as first_at, max(committed_at) as last_at from element_commits
			where element_id = any($1::uuid[]) group by element_id, service_id) l
		join services sv on sv.tenant_id = app_tenant() and sv.id = l.service_id
		left join lateral (
			select x.version, x.at, x.release_id from (
				select d.version, d.started_at as at, d.release_id::text as release_id, 0 as pri from deployments d
				join environments en on en.tenant_id = d.tenant_id and en.id = d.environment_id and en.is_production
				where d.service_id = l.service_id and d.status = 'succeeded' and d.started_at >= l.last_at
				union all
				select r.version, r.released_at, r.id::text, 1 from releases r
				join environments en on en.tenant_id = r.tenant_id and en.id = r.environment_id and en.is_production
				where r.service_id = l.service_id and r.released_at >= l.last_at
			) x
			-- Un gemello (la stessa versione già in produzione prima del commit) non contiene il commit.
			where not exists (select 1 from deployments d0
				join environments e0 on e0.tenant_id = d0.tenant_id and e0.id = d0.environment_id and e0.is_production
				where d0.service_id = l.service_id and d0.status = 'succeeded' and lower(d0.version) = lower(x.version) and d0.started_at < l.last_at)
			order by x.at, x.pri limit 1
		) p on true
		order by 1, 2`, ids)
	if err != nil {
		return nil, err
	}
	var elID, svc string
	var first, last time.Time
	var version, relID *string
	var at *time.Time
	_, err = pgx.ForEachRow(rows, []any{&elID, &svc, &first, &last, &version, &at, &relID}, func() error {
		p := out[elID]
		if p == nil {
			p = &ElementProduction{InProduction: true, FirstAt: first.UTC(), LastAt: last.UTC(), Pending: []string{}}
			out[elID] = p
		}
		if first.Before(p.FirstAt) {
			p.FirstAt = first.UTC()
		}
		if last.After(p.LastAt) {
			p.LastAt = last.UTC()
		}
		if at == nil {
			p.Pending = append(p.Pending, svc)
			return nil
		}
		if p.At == nil || at.After(*p.At) {
			t := at.UTC()
			p.At, p.Service, p.ReleaseID = &t, svc, relID
			p.Version = ""
			if version != nil {
				p.Version = *version
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, p := range out {
		if len(p.Pending) > 0 {
			p.InProduction = false
		}
	}
	return out, nil
}

// ProductionItem è un elemento discendente con codice.
type ProductionItem struct {
	Key           string             `json:"key"`
	Title         string             `json:"title"`
	Type          string             `json:"type"`
	TypeName      string             `json:"type_name"`
	State         string             `json:"state"`
	StateCategory string             `json:"state_category"`
	Production    *ElementProduction `json:"production"`
}

// Production è la risposta di GET /api/v1/elements/{ref}/production.
type Production struct {
	// Self: l'elemento stesso, se ha commit.
	Self *ElementProduction `json:"self"`
	// Items: gli elementi discendenti (figli, nipoti…) con almeno un commit, visibili.
	Items        []ProductionItem `json:"items"`
	Total        int              `json:"total"`         // discendenti con codice
	InProduction int              `json:"in_production"` // di questi, in produzione
	LastAt       *time.Time       `json:"last_at"`       // l'ingresso in produzione più recente
}

const maxProductionItems = 300

// ProductionOf: lo stato di produzione dell'elemento (il chiamante ha già verificato che sia
// visibile) e dei suoi discendenti con codice: richiesta → impatti → epic → story.
func (s *Service) ProductionOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID string) (*Production, error) {
	out := &Production{Items: []ProductionItem{}}
	rows, err := tx.Query(ctx, `with recursive d(id, depth) as (
			select e.id, 0 from elements e where e.id = $1
			union all
			select c.id, d.depth + 1 from elements c join d on c.parent_id = d.id
			where c.tenant_id = app_tenant() and c.deleted_at is null and d.depth < 5
		)
		select e.id::text, coalesce(sp.key || '-' || e.number, ''), e.title, t.key, t.name, coalesce(o.name, ''), coalesce(o.category, '')
		from d join elements e on e.tenant_id = app_tenant() and e.id = d.id
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where d.depth > 0 and coalesce(o.category, '') <> 'cancelled'
		  and exists (select 1 from element_commits c where c.element_id = e.id)
		order by sp.key, e.number limit $2`, elementID, maxProductionItems)
	if err != nil {
		return nil, err
	}
	type desc struct {
		id string
		ProductionItem
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (desc, error) {
		var d desc
		err := r.Scan(&d.id, &d.Key, &d.Title, &d.Type, &d.TypeName, &d.State, &d.StateCategory)
		return d, err
	})
	if err != nil {
		return nil, err
	}
	ids := []string{elementID}
	var visible []desc
	for _, d := range all {
		ok, err := s.eng.CanRead(ctx, tx, actor, d.id)
		if err != nil {
			return nil, err
		}
		if ok {
			visible = append(visible, d)
			ids = append(ids, d.id)
		}
	}
	prod, err := productionOf(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	out.Self = prod[elementID]
	for _, d := range visible {
		d.Production = prod[d.id]
		out.Items = append(out.Items, d.ProductionItem)
		out.Total++
		if d.Production != nil && d.Production.InProduction {
			out.InProduction++
			if out.LastAt == nil || d.Production.At.After(*out.LastAt) {
				out.LastAt = d.Production.At
			}
		}
	}
	return out, nil
}
