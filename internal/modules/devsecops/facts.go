package devsecops

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Fatti per le condizioni dei processi (es. "cve_critiche_produzione: 0" in un gate, o una
// regola che apre un rischio se "runtime_fuori_supporto: sì").

func (s *Service) appFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	// Metriche DORA dell'applicazione (dora_store.go).
	if err := s.doraFacts(ctx, tx, el.ID, f); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, "select id::text, first_seen_at is not null from services where application_element_id = $1", el.ID)
	if err != nil {
		return err
	}
	var ids []string
	off := 0
	var id string
	var on bool
	if _, err := pgx.ForEachRow(rows, []any{&id, &on}, func() error {
		ids = append(ids, id)
		if !on {
			off++
		}
		return nil
	}); err != nil {
		return err
	}
	f.Set("servizi", len(ids))
	f.Set("servizi_fuori_pipeline", off)
	if len(ids) == 0 {
		return nil
	}
	now := s.now()
	states, err := statesAt(ctx, tx, ids, now.Add(time.Minute), true)
	if err != nil {
		return err
	}
	crit, high := 0, 0
	var last *time.Time
	var verSvc, verVer []string
	for sid, st := range states {
		if st.CVE != nil {
			crit += st.CVE.Critical
			high += st.CVE.High
		}
		if st.LastProdRelease != nil && (last == nil || st.LastProdRelease.After(*last)) {
			last = st.LastProdRelease
		}
		if st.ProdVersion != "" {
			verSvc, verVer = append(verSvc, sid), append(verVer, st.ProdVersion)
		}
	}
	f.Set("cve_critiche_produzione", crit)
	f.Set("cve_alte_produzione", high)
	if last != nil {
		f.Set("ultimo_rilascio_giorni", int(now.Sub(*last).Hours()/24))
	}
	settings, err := loadSettings(ctx, tx)
	if err != nil {
		return err
	}
	rts, err := runtimes(ctx, tx, verSvc, verVer, settings, now)
	if err != nil {
		return err
	}
	if len(rts) > 0 {
		unsupported := false
		for _, list := range rts {
			for _, r := range list {
				unsupported = unsupported || r.Unsupported
			}
		}
		f.Set("runtime_fuori_supporto", unsupported)
	}
	return nil
}

func (s *Service) requestFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	var total, prod int
	var last *time.Time
	var changes, services []string
	err := tx.QueryRow(ctx, `select count(r.id), count(r.id) filter (where e.is_production), max(r.released_at),
		coalesce(array_agg(distinct c.number) filter (where c.number is not null), '{}'),
		coalesce(array_agg(distinct sv.name) filter (where sv.name is not null), '{}')
		from changes c
		left join releases r on r.tenant_id = c.tenant_id and r.change_id = c.id
		left join environments e on e.tenant_id = r.tenant_id and e.id = r.environment_id
		left join services sv on sv.tenant_id = r.tenant_id and sv.id = r.service_id
		where c.request_element_id = $1`, el.ID).Scan(&total, &prod, &last, &changes, &services)
	if err != nil {
		return err
	}
	f.Set("rilasci", total)
	f.Set("rilasci_produzione", prod)
	f.Set("rilasciata", prod > 0)
	f.Set("ultimo_rilascio", last)
	f.Set("change", toAny(changes)...)
	f.Set("servizi_rilasciati", toAny(services)...)
	return nil
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
