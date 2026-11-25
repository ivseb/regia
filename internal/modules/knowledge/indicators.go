package knowledge

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/collab"
)

// Indicatori della home di Knowledge: revisioni scadute, commenti aperti, campagne di
// lettura. Si contano solo le pagine degli spazi che l'attore può leggere.

// Indicators sono i numeri in alto nella home.
type Indicators struct {
	ReviewOverdue    int `json:"review_overdue"`    // revisioni periodiche scadute
	ReviewSoon       int `json:"review_soon"`       // in scadenza entro 30 giorni
	OpenThreads      int `json:"open_threads"`      // discussioni in linea aperte
	OrphanThreads    int `json:"orphan_threads"`    // di cui senza più il testo citato
	CampaignsActive  int `json:"campaigns_active"`  // campagne di lettura attive
	CampaignsOverdue int `json:"campaigns_overdue"` // di cui scadute
	ReadingsMissing  int `json:"readings_missing"`  // conferme ancora mancanti
	MyReadings       int `json:"my_readings"`       // quelle che toccano a me
}

// OpenThread è una discussione in linea aperta, per l'elenco della home.
type OpenThread struct {
	ThreadID  string    `json:"thread_id"`
	Key       string    `json:"key"`
	Title     string    `json:"title"`
	SpaceKey  string    `json:"space_key"`
	Quote     string    `json:"quote"`
	Excerpt   string    `json:"excerpt"`
	Author    string    `json:"author"`
	Replies   int       `json:"replies"`
	Orphan    bool      `json:"orphan"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MyReading è una conferma di lettura che aspetta me.
type MyReading struct {
	Key      string  `json:"key"`
	Title    string  `json:"title"`
	SpaceKey string  `json:"space_key"`
	Name     string  `json:"name"`
	Version  int     `json:"version"`
	Due      *string `json:"due"`
	Overdue  bool    `json:"overdue"`
}

// CampaignRow è una campagna attiva con l'avanzamento.
type CampaignRow struct {
	Key       string  `json:"key"`
	Title     string  `json:"title"`
	SpaceKey  string  `json:"space_key"`
	Name      string  `json:"name"`
	Version   int     `json:"version"`
	Due       *string `json:"due"`
	Total     int     `json:"total"`
	Confirmed int     `json:"confirmed"`
	Overdue   bool    `json:"overdue"`
}

func (s *Service) indicators(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceIDs []string, h *Home) error {
	h.Indicators = Indicators{}
	h.OpenThreads = []OpenThread{}
	h.MyReadings = []MyReading{}
	h.Campaigns = []CampaignRow{}
	in := &h.Indicators
	if err := tx.QueryRow(ctx, `
		select count(*) filter (where k.next_review < current_date)::int,
		       count(*) filter (where k.next_review >= current_date and k.next_review <= current_date + 30)::int
		from kb_pages k join elements e on e.tenant_id = k.tenant_id and e.id = k.element_id and e.deleted_at is null
		where k.controlled and k.next_review is not null and e.space_id = any($1::uuid[])`, spaceIDs).Scan(&in.ReviewOverdue, &in.ReviewSoon); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `
		select count(*)::int, count(*) filter (where t.orphan)::int
		from kb_threads t join elements e on e.tenant_id = t.tenant_id and e.id = t.page_id and e.deleted_at is null
		where t.status = 'open' and e.space_id = any($1::uuid[])
		  and exists (select 1 from kb_thread_comments c where c.thread_id = t.id and c.deleted_at is null)`, spaceIDs).Scan(&in.OpenThreads, &in.OrphanThreads); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		select t.id::text, s.key || '-' || e.number, e.title, s.key, t.quote, t.orphan,
		       coalesce(l.body, ''), coalesce(m.display_name, ''), l.created_at,
		       (select count(*) from kb_thread_comments c where c.thread_id = t.id and c.deleted_at is null)::int - 1
		from kb_threads t
		join elements e on e.tenant_id = t.tenant_id and e.id = t.page_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join lateral (select c.body, c.author_id, c.created_at from kb_thread_comments c
		              where c.thread_id = t.id and c.deleted_at is null order by c.created_at desc limit 1) l on true
		left join members m on m.tenant_id = t.tenant_id and m.id = l.author_id
		where t.status = 'open' and e.space_id = any($1::uuid[])
		order by l.created_at desc limit 8`, spaceIDs)
	if err != nil {
		return err
	}
	for rows.Next() {
		var o OpenThread
		var body string
		if err := rows.Scan(&o.ThreadID, &o.Key, &o.Title, &o.SpaceKey, &o.Quote, &o.Orphan, &body, &o.Author, &o.UpdatedAt, &o.Replies); err != nil {
			rows.Close()
			return err
		}
		o.Excerpt = collab.Excerpt(body, 140)
		o.Quote = collab.Excerpt(o.Quote, 100)
		h.OpenThreads = append(h.OpenThreads, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `
		select count(*)::int, count(*) filter (where c.due_date < current_date)::int
		from kb_read_campaigns c join elements e on e.tenant_id = c.tenant_id and e.id = c.page_id and e.deleted_at is null
		where c.status = 'active' and e.space_id = any($1::uuid[])`, spaceIDs).Scan(&in.CampaignsActive, &in.CampaignsOverdue); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `
		select count(*)::int from kb_read_campaigns c
		join elements e on e.tenant_id = c.tenant_id and e.id = c.page_id and e.deleted_at is null
		join kb_read_targets t on t.tenant_id = c.tenant_id and t.campaign_id = c.id
		join members m on m.tenant_id = t.tenant_id and m.id = t.member_id and m.status = 'active'
		where c.status = 'active' and e.space_id = any($1::uuid[])
		  and not exists (select 1 from kb_read_confirms f where f.campaign_id = c.id and f.member_id = t.member_id and f.version >= c.version)`,
		spaceIDs).Scan(&in.ReadingsMissing); err != nil {
		return err
	}
	rows, err = tx.Query(ctx, `
		select s.key || '-' || e.number, e.title, s.key, c.name, c.version, c.due_date::text, coalesce(c.due_date < current_date, false)
		from kb_read_campaigns c
		join elements e on e.tenant_id = c.tenant_id and e.id = c.page_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join kb_read_targets t on t.tenant_id = c.tenant_id and t.campaign_id = c.id and t.member_id::text = $2
		where c.status = 'active' and e.space_id = any($1::uuid[])
		  and not exists (select 1 from kb_read_confirms f where f.campaign_id = c.id and f.member_id = t.member_id and f.version >= c.version)
		order by c.due_date nulls last, c.created_at limit 20`, spaceIDs, actor.MemberID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var r MyReading
		if err := rows.Scan(&r.Key, &r.Title, &r.SpaceKey, &r.Name, &r.Version, &r.Due, &r.Overdue); err != nil {
			rows.Close()
			return err
		}
		h.MyReadings = append(h.MyReadings, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	in.MyReadings = len(h.MyReadings)
	rows, err = tx.Query(ctx, `
		select s.key || '-' || e.number, e.title, s.key, c.name, c.version, c.due_date::text,
		       (select count(*) from kb_read_targets t join members m on m.tenant_id = t.tenant_id and m.id = t.member_id and m.status = 'active' where t.campaign_id = c.id)::int,
		       (select count(*) from kb_read_targets t join members m on m.tenant_id = t.tenant_id and m.id = t.member_id and m.status = 'active'
		         where t.campaign_id = c.id and exists (select 1 from kb_read_confirms f where f.campaign_id = c.id and f.member_id = t.member_id and f.version >= c.version))::int,
		       coalesce(c.due_date < current_date, false)
		from kb_read_campaigns c
		join elements e on e.tenant_id = c.tenant_id and e.id = c.page_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where c.status = 'active' and e.space_id = any($1::uuid[])
		order by c.due_date nulls last, c.created_at limit 8`, spaceIDs)
	if err != nil {
		return err
	}
	for rows.Next() {
		var r CampaignRow
		if err := rows.Scan(&r.Key, &r.Title, &r.SpaceKey, &r.Name, &r.Version, &r.Due, &r.Total, &r.Confirmed, &r.Overdue); err != nil {
			rows.Close()
			return err
		}
		r.Overdue = r.Overdue && r.Confirmed < r.Total
		h.Campaigns = append(h.Campaigns, r)
	}
	rows.Close()
	return rows.Err()
}
