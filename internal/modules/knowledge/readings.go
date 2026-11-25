package knowledge

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Conferma di lettura dei documenti controllati. Una campagna dice chi deve leggere
// (persone, gruppi, ruoli), entro quando e quale versione: ogni persona conferma con «Ho letto
// e compreso» e la conferma ricorda versione, persona e momento (evidenza per l'audit, non si
// modifica). La campagna ha promemoria prima della scadenza e dopo, un sollecito manuale e,
// se si vuole, si riapre da sola quando una versione nuova viene approvata.

const (
	campaignActive = "active"
	campaignClosed = "closed"

	// manualRemindGap: intervallo minimo tra due solleciti manuali.
	manualRemindGap = 12 * time.Hour
	// overdueEvery: giorni tra due promemoria di una campagna scaduta.
	overdueEvery = 7
	// maxAudience: voci del pubblico (persone, gruppi e ruoli) di una campagna.
	maxAudience = 100
	// maxCampaignsPerPage: campagne per pagina (le chiuse contano).
	maxCampaignsPerPage = 50
)

// AudienceRef è una voce del pubblico: una persona, un gruppo o un ruolo.
type AudienceRef struct {
	Kind string `json:"kind"` // member | group | role
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Reader è lo stato di lettura di una persona.
type Reader struct {
	MemberID         string     `json:"member_id"`
	Name             string     `json:"name"`
	State            string     `json:"state"` // confirmed | pending | outdated
	ConfirmedVersion *int       `json:"confirmed_version"`
	ConfirmedAt      *time.Time `json:"confirmed_at"`
}

// Campaign è una campagna di lettura con i suoi numeri.
type Campaign struct {
	ID               string        `json:"id"`
	Name             string        `json:"name"`
	Version          int           `json:"version"`
	DueDate          *string       `json:"due_date"`
	Status           string        `json:"status"`
	ReopenOnApproval bool          `json:"reopen_on_approval"`
	RenewDays        int           `json:"renew_days"`
	RemindDays       int           `json:"remind_days"`
	CreatedBy        string        `json:"created_by"`
	CreatedAt        time.Time     `json:"created_at"`
	ClosedAt         *time.Time    `json:"closed_at"`
	RemindedAt       *time.Time    `json:"reminded_at"`
	Audience         []AudienceRef `json:"audience"`
	Total            int           `json:"total"`
	Confirmed        int           `json:"confirmed"`
	Overdue          bool          `json:"overdue"`
	Complete         bool          `json:"complete"`
	MyState          string        `json:"my_state"` // vuoto se non sei nel pubblico
	People           []Reader      `json:"people"`   // solo per chi gestisce la campagna
}

// Readings sono le campagne di una pagina.
type Readings struct {
	Items           []Campaign `json:"items"`
	CanManage       bool       `json:"can_manage"`
	Version         int        `json:"version"`          // versione corrente della pagina
	ApprovedVersion *int       `json:"approved_version"` // versione approvata in vigore
}

// ReadingInput crea o modifica una campagna.
type ReadingInput struct {
	Name             string        `json:"name"`
	Version          int           `json:"version"`
	DueDate          *string       `json:"due_date"` // "" toglie la scadenza
	Audience         []AudienceRef `json:"audience"`
	ReopenOnApproval *bool         `json:"reopen_on_approval"`
	RenewDays        *int          `json:"renew_days"`
	RemindDays       *int          `json:"remind_days"`
	Status           string        `json:"status"` // active | closed (solo in modifica)
}

// AudiencePreview è chi riceverebbe la richiesta e chi resta fuori perché non legge lo spazio.
type AudiencePreview struct {
	People   []Person `json:"people"`
	Excluded []Person `json:"excluded"`
}

func (s *Service) canManageReadings(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, sp *Space) (bool, error) {
	if sp.Rights.Manage {
		return true, nil
	}
	return engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{RoleApprover})
}

// Readings elenca le campagne della pagina; chi le gestisce vede anche lo stato di ogni persona.
func (s *Service) Readings(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Readings, error) {
	el, sp, k, err := s.loadPage(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	return s.readingsView(ctx, tx, actor, el, sp, k)
}

func (s *Service) readingsView(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, sp *Space, k *kbRow) (*Readings, error) {
	manage, err := s.canManageReadings(ctx, tx, actor, el, sp)
	if err != nil {
		return nil, err
	}
	out := &Readings{Items: []Campaign{}, CanManage: manage, Version: k.version, ApprovedVersion: k.approvedVersion}
	rows, err := tx.Query(ctx, `
		select c.id::text, c.name, c.version, c.due_date, c.status, c.reopen_on_approval, c.renew_days, c.remind_days,
		       coalesce(u.display_name, ''), c.created_at, c.closed_at, c.manual_reminded_at,
		       coalesce(c.due_date < current_date, false),
		       (select count(*) from kb_read_targets t join members m on m.tenant_id = t.tenant_id and m.id = t.member_id and m.status = 'active'
		         where t.campaign_id = c.id)::int,
		       (select count(*) from kb_read_targets t join members m on m.tenant_id = t.tenant_id and m.id = t.member_id and m.status = 'active'
		         where t.campaign_id = c.id and exists (select 1 from kb_read_confirms f where f.campaign_id = c.id and f.member_id = t.member_id and f.version = c.version))::int
		from kb_read_campaigns c
		left join members u on u.tenant_id = c.tenant_id and u.id = c.created_by
		where c.page_id = $1
		order by (c.status = 'active') desc, c.created_at desc limit $2`, el.ID, maxCampaignsPerPage)
	if err != nil {
		return nil, err
	}
	var due *time.Time
	var late bool
	for rows.Next() {
		var c Campaign
		if err := rows.Scan(&c.ID, &c.Name, &c.Version, &due, &c.Status, &c.ReopenOnApproval, &c.RenewDays, &c.RemindDays,
			&c.CreatedBy, &c.CreatedAt, &c.ClosedAt, &c.RemindedAt, &late, &c.Total, &c.Confirmed); err != nil {
			rows.Close()
			return nil, err
		}
		if due != nil {
			d := due.Format(time.DateOnly)
			c.DueDate = &d
		}
		c.Complete = c.Total > 0 && c.Confirmed >= c.Total
		c.Overdue = c.Status == campaignActive && late && !c.Complete
		c.Audience, c.People = []AudienceRef{}, []Reader{}
		out.Items = append(out.Items, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out.Items {
		c := &out.Items[i]
		if c.Audience, err = audienceOf(ctx, tx, c.ID); err != nil {
			return nil, err
		}
		people, err := readersOf(ctx, tx, c.ID, c.Version)
		if err != nil {
			return nil, err
		}
		for _, p := range people {
			if p.MemberID == actor.MemberID {
				c.MyState = p.State
			}
		}
		if manage {
			c.People = people
		}
	}
	return out, nil
}

func audienceOf(ctx context.Context, tx pgx.Tx, campaignID string) ([]AudienceRef, error) {
	rows, err := tx.Query(ctx, `
		select a.kind, a.ref_id::text,
		       coalesce(case a.kind when 'member' then (select display_name from members where id = a.ref_id)
		                            when 'group' then (select name from groups where id = a.ref_id)
		                            else (select name from roles where id = a.ref_id) end, '(eliminato)')
		from kb_read_audience a where a.campaign_id = $1 order by a.kind, 3`, campaignID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[AudienceRef])
	if out == nil {
		out = []AudienceRef{}
	}
	return out, err
}

// readersOf: ogni persona del pubblico con lo stato rispetto alla versione da leggere.
func readersOf(ctx context.Context, tx pgx.Tx, campaignID string, version int) ([]Reader, error) {
	rows, err := tx.Query(ctx, `
		select m.id::text, m.display_name,
		       (select max(f.version) from kb_read_confirms f where f.campaign_id = $1 and f.member_id = m.id),
		       (select max(f.confirmed_at) from kb_read_confirms f where f.campaign_id = $1 and f.member_id = m.id)
		from kb_read_targets t
		join members m on m.tenant_id = t.tenant_id and m.id = t.member_id and m.status = 'active'
		where t.campaign_id = $1
		order by m.display_name`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Reader{}
	for rows.Next() {
		var r Reader
		if err := rows.Scan(&r.MemberID, &r.Name, &r.ConfirmedVersion, &r.ConfirmedAt); err != nil {
			return nil, err
		}
		switch {
		case r.ConfirmedVersion != nil && *r.ConfirmedVersion >= version:
			r.State = "confirmed"
		case r.ConfirmedVersion != nil:
			r.State = "outdated"
		default:
			r.State = "pending"
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// resolveAudience trasforma persone, gruppi e ruoli nelle persone attive; chi non può leggere
// lo spazio (e quindi non potrebbe confermare) finisce tra gli esclusi.
func resolveAudience(ctx context.Context, tx pgx.Tx, spaceID string, aud []AudienceRef) (*AudiencePreview, error) {
	out := &AudiencePreview{People: []Person{}, Excluded: []Person{}}
	if len(aud) == 0 {
		return out, nil
	}
	kinds := make([]string, 0, len(aud))
	ids := make([]string, 0, len(aud))
	for _, a := range aud {
		if !slices.Contains([]string{"member", "group", "role"}, a.Kind) || !uuidRe.MatchString(a.ID) {
			return nil, engine.InvalidValue("audience", "voce del pubblico non valida")
		}
		kinds, ids = append(kinds, a.Kind), append(ids, a.ID)
	}
	rows, err := tx.Query(ctx, `
		with aud as (select kind, ref_id from unnest($2::text[], $3::uuid[]) as a(kind, ref_id)),
		people as (
			select ref_id as member_id from aud where kind = 'member'
			union
			select e from aud cross join lateral group_effective_members(aud.ref_id) as e where aud.kind = 'group'
			union
			select ra.member_id from aud join role_assignments ra on ra.role_id = aud.ref_id and (ra.space_id is null or ra.space_id = $1)
			 where aud.kind = 'role' and ra.member_id is not null
			union
			select e from aud join role_assignments ra on ra.role_id = aud.ref_id and (ra.space_id is null or ra.space_id = $1)
			 cross join lateral group_effective_members(ra.group_id) as e where aud.kind = 'role' and ra.group_id is not null
		)
		select m.id::text, m.display_name from people p
		join members m on m.id = p.member_id and m.status = 'active' and m.member_type <> 'requester'
		order by m.display_name`, spaceID, kinds, ids)
	if err != nil {
		return nil, err
	}
	all, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Person])
	if err != nil {
		return nil, err
	}
	for _, p := range all {
		ok, err := memberCanRead(ctx, tx, spaceID, p.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			out.People = append(out.People, p)
		} else {
			out.Excluded = append(out.Excluded, p)
		}
	}
	return out, nil
}

// PreviewAudience mostra chi riceverebbe la richiesta, prima di crearla.
func (s *Service) PreviewAudience(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, aud []AudienceRef) (*AudiencePreview, error) {
	el, sp, _, err := s.loadPage(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canManageReadings(ctx, tx, actor, el, sp); err != nil {
		return nil, err
	} else if !ok {
		return nil, engine.ErrForbidden
	}
	return resolveAudience(ctx, tx, sp.ID, aud)
}

// syncTargets allinea le persone della campagna al pubblico. Restituisce le nuove.
func syncTargets(ctx context.Context, tx pgx.Tx, campaignID, spaceID string) ([]string, error) {
	aud, err := audienceOf(ctx, tx, campaignID)
	if err != nil {
		return nil, err
	}
	res, err := resolveAudience(ctx, tx, spaceID, aud)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(res.People))
	for _, p := range res.People {
		ids = append(ids, p.ID)
	}
	rows, err := tx.Query(ctx, `
		insert into kb_read_targets (tenant_id, campaign_id, member_id)
		select app_tenant(), $1, m from unnest($2::uuid[]) m
		on conflict do nothing returning member_id::text`, campaignID, ids)
	if err != nil {
		return nil, err
	}
	added, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `delete from kb_read_targets where campaign_id = $1 and not (member_id = any($2::uuid[]))`, campaignID, ids); err != nil {
		return nil, err
	}
	return added, nil
}

func replaceAudience(ctx context.Context, tx pgx.Tx, campaignID string, aud []AudienceRef) error {
	if _, err := tx.Exec(ctx, `delete from kb_read_audience where campaign_id = $1`, campaignID); err != nil {
		return err
	}
	for _, a := range aud {
		if _, err := tx.Exec(ctx, `insert into kb_read_audience (tenant_id, campaign_id, kind, ref_id) values (app_tenant(), $1, $2, $3) on conflict do nothing`,
			campaignID, a.Kind, a.ID); err != nil {
			return err
		}
	}
	return nil
}

func parseDue(p *string) (any, bool, error) {
	if p == nil {
		return nil, false, nil
	}
	if strings.TrimSpace(*p) == "" {
		return nil, true, nil
	}
	d, err := time.Parse(time.DateOnly, strings.TrimSpace(*p))
	if err != nil {
		return nil, false, engine.DateInvalid("due_date", "data non valida (usa AAAA-MM-GG)")
	}
	return d.Format(time.DateOnly), true, nil
}

func validateReadingKnobs(in ReadingInput) error {
	if in.RenewDays != nil && (*in.RenewDays < 1 || *in.RenewDays > 180) {
		return engine.OutOfRange("renew_days", 1, 180, "tra 1 e 180 giorni")
	}
	if in.RemindDays != nil && (*in.RemindDays < 0 || *in.RemindDays > 30) {
		return engine.OutOfRange("remind_days", 0, 30, "tra 0 e 30 giorni")
	}
	if len(in.Audience) > maxAudience {
		return engine.InvalidParams("validation.knowledge.audience_too_many", "audience", map[string]any{"max": maxAudience}, "troppe voci (massimo %d)", maxAudience)
	}
	return nil
}

// CreateReading apre una campagna di lettura su un documento controllato.
func (s *Service) CreateReading(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ReadingInput) (*Readings, error) {
	el, sp, k, err := s.loadPage(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canManageReadings(ctx, tx, actor, el, sp); err != nil {
		return nil, err
	} else if !ok {
		return nil, engine.ErrForbidden
	}
	if !k.controlled {
		return nil, engine.InvalidCode("validation.knowledge.reading_not_controlled", "page", "la conferma di lettura è per i documenti controllati")
	}
	if err := validateReadingKnobs(in); err != nil {
		return nil, err
	}
	version := in.Version
	if version == 0 {
		if k.approvedVersion == nil {
			return nil, engine.Required("version", "scegli la versione da leggere (nessuna è ancora approvata)")
		}
		version = *k.approvedVersion
	}
	if version < 1 || version > k.version {
		return nil, engine.NotFound("version", "", "la versione %d non esiste", version)
	}
	due, _, err := parseDue(in.DueDate)
	if err != nil {
		return nil, err
	}
	if due != nil && due.(string) < time.Now().Format(time.DateOnly) {
		return nil, engine.InvalidCode("validation.knowledge.reading_due_past", "due_date", "la scadenza è già passata")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = fmt.Sprintf("Lettura di %s v%d", el.Title, version)
	}
	if utf8.RuneCountInString(name) > 200 {
		return nil, engine.TooLong("name", 200, "nome troppo lungo (massimo 200 caratteri)")
	}
	if len(in.Audience) == 0 {
		return nil, engine.Required("audience", "indica chi deve leggere: persone, gruppi o ruoli")
	}
	var n int
	if err := tx.QueryRow(ctx, `select count(*) from kb_read_campaigns where page_id = $1`, el.ID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= maxCampaignsPerPage {
		return nil, engine.InvalidParams("validation.knowledge.campaigns_limit", "page", map[string]any{"max": maxCampaignsPerPage}, "troppe campagne su questa pagina (massimo %d)", maxCampaignsPerPage)
	}
	res, err := resolveAudience(ctx, tx, sp.ID, in.Audience)
	if err != nil {
		return nil, err
	}
	if len(res.People) == 0 {
		return nil, engine.InvalidCode("validation.knowledge.audience_cannot_read", "audience", "il pubblico scelto non contiene persone che possano leggere lo spazio")
	}
	reopen, renew, remind := true, 14, 3
	if in.ReopenOnApproval != nil {
		reopen = *in.ReopenOnApproval
	}
	if in.RenewDays != nil {
		renew = *in.RenewDays
	}
	if in.RemindDays != nil {
		remind = *in.RemindDays
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into kb_read_campaigns (tenant_id, id, page_id, name, version, due_date, reopen_on_approval, renew_days, remind_days, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, el.ID, name, version, due, reopen, renew, remind, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := replaceAudience(ctx, tx, id, in.Audience); err != nil {
		return nil, err
	}
	added, err := syncTargets(ctx, tx, id, sp.ID)
	if err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "page.reading.create", EntityType: TypePage, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"campaign_id": id, "version": version, "people": len(added)}}); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "page", "reading_opened", map[string]any{"campaign": name, "version": version}, nil); err != nil {
		return nil, err
	}
	if err := emitReading(ctx, tx, el.ID, actor.MemberID, added, "reading_requested", name, version, due); err != nil {
		return nil, err
	}
	return s.readingsView(ctx, tx, actor, el, sp, k)
}

func emitReading(ctx context.Context, tx pgx.Tx, pageID, actorID string, members []string, event, name string, version int, due any) error {
	if len(members) == 0 {
		return nil
	}
	p := map[string]any{"event": event, "version": version, "name": name}
	if d, ok := due.(string); ok && d != "" {
		p["due"] = d
	}
	_, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindDocument, ElementID: pageID, ActorID: actorID, Members: members, Payload: p})
	return err
}

type campaignRow struct {
	id, pageID, name, status string
	version                  int
	due                      *time.Time
	reopen                   bool
}

func loadCampaign(ctx context.Context, tx pgx.Tx, campaignID string) (*campaignRow, error) {
	if !uuidRe.MatchString(campaignID) {
		return nil, engine.ErrNotFound
	}
	var c campaignRow
	err := tx.QueryRow(ctx, `select id::text, page_id::text, name, status, version, due_date, reopen_on_approval
		from kb_read_campaigns where id = $1 for update`, campaignID).Scan(&c.id, &c.pageID, &c.name, &c.status, &c.version, &c.due, &c.reopen)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	return &c, err
}

func dueArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.DateOnly)
}

// UpdateReading cambia nome, scadenza, pubblico, versione e opzioni di una campagna, o la
// chiude e la riapre. Le persone aggiunte ricevono la richiesta.
func (s *Service) UpdateReading(ctx context.Context, tx pgx.Tx, actor engine.Actor, campaignID string, in ReadingInput) (*Readings, error) {
	c, err := loadCampaign(ctx, tx, campaignID)
	if err != nil {
		return nil, err
	}
	el, sp, k, err := s.loadPage(ctx, tx, actor, c.pageID, true)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canManageReadings(ctx, tx, actor, el, sp); err != nil {
		return nil, err
	} else if !ok {
		return nil, engine.ErrForbidden
	}
	if err := validateReadingKnobs(in); err != nil {
		return nil, err
	}
	changed := []string{}
	if n := strings.TrimSpace(in.Name); n != "" && n != c.name {
		if utf8.RuneCountInString(n) > 200 {
			return nil, engine.TooLong("name", 200, "nome troppo lungo (massimo 200 caratteri)")
		}
		if _, err := tx.Exec(ctx, `update kb_read_campaigns set name = $2 where id = $1`, c.id, n); err != nil {
			return nil, err
		}
		c.name = n
		changed = append(changed, "name")
	}
	if due, set, err := parseDue(in.DueDate); err != nil {
		return nil, err
	} else if set {
		if _, err := tx.Exec(ctx, `update kb_read_campaigns set due_date = $2, reminded_for = null, overdue_reminded_on = null where id = $1`, c.id, due); err != nil {
			return nil, err
		}
		if d, ok := due.(string); ok {
			t, _ := time.Parse(time.DateOnly, d)
			c.due = &t
		} else {
			c.due = nil
		}
		changed = append(changed, "due_date")
	}
	if in.Version != 0 && in.Version != c.version {
		if in.Version < 1 || in.Version > k.version {
			return nil, engine.NotFound("version", "", "la versione %d non esiste", in.Version)
		}
		if _, err := tx.Exec(ctx, `update kb_read_campaigns set version = $2 where id = $1`, c.id, in.Version); err != nil {
			return nil, err
		}
		c.version = in.Version
		changed = append(changed, "version")
	}
	if in.ReopenOnApproval != nil {
		if _, err := tx.Exec(ctx, `update kb_read_campaigns set reopen_on_approval = $2 where id = $1`, c.id, *in.ReopenOnApproval); err != nil {
			return nil, err
		}
		changed = append(changed, "reopen_on_approval")
	}
	if in.RenewDays != nil {
		if _, err := tx.Exec(ctx, `update kb_read_campaigns set renew_days = $2 where id = $1`, c.id, *in.RenewDays); err != nil {
			return nil, err
		}
	}
	if in.RemindDays != nil {
		if _, err := tx.Exec(ctx, `update kb_read_campaigns set remind_days = $2, reminded_for = null where id = $1`, c.id, *in.RemindDays); err != nil {
			return nil, err
		}
	}
	var added []string
	if in.Audience != nil {
		if len(in.Audience) == 0 {
			return nil, engine.Required("audience", "indica chi deve leggere: persone, gruppi o ruoli")
		}
		res, err := resolveAudience(ctx, tx, sp.ID, in.Audience)
		if err != nil {
			return nil, err
		}
		if len(res.People) == 0 {
			return nil, engine.InvalidCode("validation.knowledge.audience_cannot_read", "audience", "il pubblico scelto non contiene persone che possano leggere lo spazio")
		}
		if err := replaceAudience(ctx, tx, c.id, in.Audience); err != nil {
			return nil, err
		}
		if added, err = syncTargets(ctx, tx, c.id, sp.ID); err != nil {
			return nil, err
		}
		changed = append(changed, "audience")
	}
	switch in.Status {
	case "":
	case campaignClosed:
		if c.status != campaignClosed {
			if _, err := tx.Exec(ctx, `update kb_read_campaigns set status = 'closed', closed_at = now() where id = $1`, c.id); err != nil {
				return nil, err
			}
			changed = append(changed, "closed")
		}
	case campaignActive:
		if c.status != campaignActive {
			if _, err := tx.Exec(ctx, `update kb_read_campaigns set status = 'active', closed_at = null, reminded_for = null, overdue_reminded_on = null where id = $1`, c.id); err != nil {
				return nil, err
			}
			changed = append(changed, "reopened")
		}
	default:
		return nil, engine.InvalidValue("status", "stato non valido")
	}
	if len(changed) > 0 {
		if err := audit.Append(ctx, tx, audit.Event{Action: "page.reading.update", EntityType: TypePage, EntityID: el.ID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
			Payload: map[string]any{"campaign_id": c.id, "changed": changed}}); err != nil {
			return nil, err
		}
	}
	if len(added) > 0 {
		if err := emitReading(ctx, tx, el.ID, actor.MemberID, added, "reading_requested", c.name, c.version, dueArg(c.due)); err != nil {
			return nil, err
		}
	}
	return s.readingsView(ctx, tx, actor, el, sp, k)
}

// ConfirmReading registra «Ho letto e compreso» sulla versione richiesta dalla campagna.
func (s *Service) ConfirmReading(ctx context.Context, tx pgx.Tx, actor engine.Actor, campaignID string, version int) (*Readings, error) {
	c, err := loadCampaign(ctx, tx, campaignID)
	if err != nil {
		return nil, err
	}
	el, sp, k, err := s.loadPage(ctx, tx, actor, c.pageID, false)
	if err != nil {
		return nil, err
	}
	if actor.MemberID == "" {
		return nil, engine.ErrForbidden
	}
	var target bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from kb_read_targets where campaign_id = $1 and member_id = $2)`, c.id, actor.MemberID).Scan(&target); err != nil {
		return nil, err
	}
	if !target {
		return nil, engine.ErrForbidden
	}
	if c.status != campaignActive {
		return nil, engine.InvalidCode("validation.knowledge.campaign_closed", "campaign", "la campagna è chiusa")
	}
	if version != c.version {
		return nil, engine.InvalidParams("validation.knowledge.reading_version_stale", "version", map[string]any{"version": c.version}, "la versione da leggere è la %d: ricarica la pagina", c.version)
	}
	tag, err := tx.Exec(ctx, `insert into kb_read_confirms (tenant_id, campaign_id, member_id, version) values (app_tenant(), $1, $2, $3) on conflict do nothing`,
		c.id, actor.MemberID, c.version)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() > 0 {
		if err := audit.Append(ctx, tx, audit.Event{Action: "page.reading.confirm", EntityType: TypePage, EntityID: el.ID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
			Payload: map[string]any{"campaign_id": c.id, "version": c.version}}); err != nil {
			return nil, err
		}
		if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "page", "reading_confirmed", map[string]any{"campaign": c.name, "version": c.version}, nil); err != nil {
			return nil, err
		}
	}
	return s.readingsView(ctx, tx, actor, el, sp, k)
}

// pendingOf: persone del pubblico che non hanno ancora confermato la versione da leggere.
func pendingOf(ctx context.Context, tx pgx.Tx, campaignID string, version int) ([]string, error) {
	rows, err := tx.Query(ctx, `
		select t.member_id::text from kb_read_targets t
		join members m on m.tenant_id = t.tenant_id and m.id = t.member_id and m.status = 'active'
		where t.campaign_id = $1
		  and not exists (select 1 from kb_read_confirms f where f.campaign_id = $1 and f.member_id = t.member_id and f.version >= $2)`, campaignID, version)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// RemindReading sollecita subito chi non ha ancora confermato (al più ogni 12 ore).
func (s *Service) RemindReading(ctx context.Context, tx pgx.Tx, actor engine.Actor, campaignID string) (*Readings, error) {
	c, err := loadCampaign(ctx, tx, campaignID)
	if err != nil {
		return nil, err
	}
	el, sp, k, err := s.loadPage(ctx, tx, actor, c.pageID, false)
	if err != nil {
		return nil, err
	}
	if ok, err := s.canManageReadings(ctx, tx, actor, el, sp); err != nil {
		return nil, err
	} else if !ok {
		return nil, engine.ErrForbidden
	}
	if c.status != campaignActive {
		return nil, engine.InvalidCode("validation.knowledge.campaign_closed", "campaign", "la campagna è chiusa")
	}
	var last *time.Time
	if err := tx.QueryRow(ctx, `select manual_reminded_at from kb_read_campaigns where id = $1`, c.id).Scan(&last); err != nil {
		return nil, err
	}
	if last != nil && time.Since(*last) < manualRemindGap {
		return nil, engine.InvalidParams("validation.knowledge.reminder_too_soon", "campaign", map[string]any{"time": last.Local().Format("15:04")}, "hai già sollecitato alle %s: riprova più tardi", last.Local().Format("15:04"))
	}
	pending, err := pendingOf(ctx, tx, c.id, c.version)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update kb_read_campaigns set manual_reminded_at = now() where id = $1`, c.id); err != nil {
		return nil, err
	}
	if err := emitReading(ctx, tx, el.ID, actor.MemberID, pending, "reading_reminder", c.name, c.version, dueArg(c.due)); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "page.reading.remind", EntityType: TypePage, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"campaign_id": c.id, "people": len(pending)}}); err != nil {
		return nil, err
	}
	return s.readingsView(ctx, tx, actor, el, sp, k)
}

// ReadingCSV è l'esportazione per l'audit: una riga per persona, con la conferma sulla
// versione richiesta e le conferme di versioni precedenti.
func (s *Service) ReadingCSV(ctx context.Context, tx pgx.Tx, actor engine.Actor, campaignID string) (string, []byte, error) {
	c, err := loadCampaign(ctx, tx, campaignID)
	if err != nil {
		return "", nil, err
	}
	el, sp, _, err := s.loadPage(ctx, tx, actor, c.pageID, false)
	if err != nil {
		return "", nil, err
	}
	if ok, err := s.canManageReadings(ctx, tx, actor, el, sp); err != nil {
		return "", nil, err
	} else if !ok {
		return "", nil, engine.ErrForbidden
	}
	rows, err := tx.Query(ctx, `
		select m.display_name, coalesce(m.email, ''),
		       (select max(f.confirmed_at) from kb_read_confirms f where f.campaign_id = $1 and f.member_id = m.id and f.version = $2),
		       coalesce((select string_agg('v' || f.version || ' ' || to_char(f.confirmed_at at time zone 'UTC', 'YYYY-MM-DD HH24:MI') || ' UTC', '; ' order by f.version)
		                 from kb_read_confirms f where f.campaign_id = $1 and f.member_id = m.id and f.version <> $2), '')
		from kb_read_targets t
		join members m on m.tenant_id = t.tenant_id and m.id = t.member_id
		where t.campaign_id = $1 order by m.display_name`, c.id, c.version)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var b bytes.Buffer
	b.WriteString("\xEF\xBB\xBF")
	w := csv.NewWriter(&b)
	w.Comma = ';'
	due := ""
	if c.due != nil {
		due = c.due.Format(time.DateOnly)
	}
	_ = w.Write([]string{"documento", "titolo", "campagna", "versione da leggere", "scadenza", "persona", "email", "stato", "confermata il (UTC)", "conferme di altre versioni"})
	late := c.due != nil && c.due.Before(time.Now().Truncate(24*time.Hour))
	for rows.Next() {
		var name, email, others string
		var at *time.Time
		if err := rows.Scan(&name, &email, &at, &others); err != nil {
			return "", nil, err
		}
		state, when := "da confermare", ""
		switch {
		case at != nil:
			state, when = "confermata", at.UTC().Format("2006-01-02 15:04")
		case late && c.status == campaignActive:
			state = "in ritardo"
		}
		_ = w.Write([]string{csvCell(el.Key), csvCell(el.Title), csvCell(c.name), strconv.Itoa(c.version), due, csvCell(name), csvCell(email), state, when, csvCell(others)})
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	w.Flush()
	if err := audit.Append(ctx, tx, audit.Event{Action: "page.reading.export", EntityType: TypePage, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"campaign_id": c.id}}); err != nil {
		return "", nil, err
	}
	return "letture-" + el.Key + "-v" + strconv.Itoa(c.version) + "-" + time.Now().Format(time.DateOnly) + ".csv", b.Bytes(), nil
}

// csvCell neutralizza le formule: un testo che inizia con = + - @ si aprirebbe come calcolo.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// reopenCampaigns: una nuova versione approvata riapre le campagne che lo prevedono, sulla
// versione nuova e con una scadenza nuova; chi doveva leggere ne è avvisato.
func (s *Service) reopenCampaigns(ctx context.Context, tx pgx.Tx, pageID, spaceID, actorID string, version int) error {
	rows, err := tx.Query(ctx, `
		update kb_read_campaigns set version = $2, status = 'active', closed_at = null, reminded_for = null, overdue_reminded_on = null,
		       due_date = case when due_date is null then null else (current_date + renew_days) end
		where page_id = $1 and reopen_on_approval and version < $2
		returning id::text, name, due_date`, pageID, version)
	if err != nil {
		return err
	}
	type re struct {
		id, name string
		due      *time.Time
	}
	var list []re
	for rows.Next() {
		var r re
		if err := rows.Scan(&r.id, &r.name, &r.due); err != nil {
			rows.Close()
			return err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range list {
		if _, err := syncTargets(ctx, tx, r.id, spaceID); err != nil {
			return err
		}
		people, err := pendingOf(ctx, tx, r.id, version)
		if err != nil {
			return err
		}
		if err := emitReading(ctx, tx, pageID, actorID, people, "reading_reopened", r.name, version, dueArg(r.due)); err != nil {
			return err
		}
	}
	return nil
}

// facts: «letture_mancanti» è il numero di persone che devono ancora confermare in tutte le
// campagne attive della pagina; si può usare nelle condizioni del processo (per esempio per
// non rendere obsoleto un documento finché qualcuno non l'ha letto).
func (s *Service) facts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	var n int
	if err := tx.QueryRow(ctx, `
		select count(distinct t.member_id)::int from kb_read_campaigns c
		join kb_read_targets t on t.tenant_id = c.tenant_id and t.campaign_id = c.id
		join members m on m.tenant_id = t.tenant_id and m.id = t.member_id and m.status = 'active'
		where c.page_id = $1 and c.status = 'active'
		  and not exists (select 1 from kb_read_confirms x where x.campaign_id = c.id and x.member_id = t.member_id and x.version >= c.version)`,
		el.ID).Scan(&n); err != nil {
		return err
	}
	f.Set("letture_mancanti", float64(n))
	return nil
}

// checkReadingReminders manda i promemoria delle campagne del tenant: prima della scadenza
// (una volta per scadenza) e, a campagna scaduta, ogni settimana, a chi non ha confermato; a
// chi l'ha aperta arriva il riepilogo dei ritardi.
func (s *Service) checkReadingReminders(ctx context.Context, tx pgx.Tx) (int, error) {
	rows, err := tx.Query(ctx, `
		select c.id::text, c.page_id::text, c.name, c.version, c.due_date, c.remind_days, c.reminded_for, c.overdue_reminded_on,
		       coalesce(c.created_by::text, ''), e.space_id::text, c.due_date < current_date
		from kb_read_campaigns c
		join elements e on e.tenant_id = c.tenant_id and e.id = c.page_id and e.deleted_at is null
		where c.status = 'active' and c.due_date is not null
		  and c.due_date - c.remind_days <= current_date
		for update of c skip locked`)
	if err != nil {
		return 0, err
	}
	type due struct {
		id, page, name, creator, space string
		version, remind                int
		due                            time.Time
		remindedFor, overdueOn         *time.Time
		late                           bool
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.page, &d.name, &d.version, &d.due, &d.remind, &d.remindedFor, &d.overdueOn, &d.creator, &d.space, &d.late); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	sent := 0
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, d := range list {
		if _, err := syncTargets(ctx, tx, d.id, d.space); err != nil {
			return sent, err
		}
		pending, err := pendingOf(ctx, tx, d.id, d.version)
		if err != nil {
			return sent, err
		}
		if len(pending) == 0 {
			continue
		}
		dueStr := d.due.Format(time.DateOnly)
		switch {
		case !d.late && (d.remindedFor == nil || !d.remindedFor.Equal(d.due)):
			if err := emitReading(ctx, tx, d.page, "", pending, "reading_reminder", d.name, d.version, dueStr); err != nil {
				return sent, err
			}
			if _, err := tx.Exec(ctx, `update kb_read_campaigns set reminded_for = due_date where id = $1`, d.id); err != nil {
				return sent, err
			}
			sent++
		case d.late && (d.overdueOn == nil || today.Sub(*d.overdueOn) >= overdueEvery*24*time.Hour):
			if err := emitReading(ctx, tx, d.page, "", pending, "reading_overdue", d.name, d.version, dueStr); err != nil {
				return sent, err
			}
			if d.creator != "" {
				if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindDocument, ElementID: d.page, Members: []string{d.creator},
					Payload: map[string]any{"event": "reading_overdue_summary", "version": d.version, "name": d.name, "due": dueStr, "missing": len(pending)}}); err != nil {
					return sent, err
				}
			}
			if _, err := tx.Exec(ctx, `update kb_read_campaigns set overdue_reminded_on = current_date where id = $1`, d.id); err != nil {
				return sent, err
			}
			sent++
		}
	}
	return sent, nil
}
