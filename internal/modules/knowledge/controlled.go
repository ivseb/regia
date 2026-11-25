package knowledge

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Documenti controllati (utili al GRC): la pagina segue il processo «Documento controllato»
// (Bozza → In revisione → Approvata → Obsoleta). L'invio in revisione apre una richiesta del
// motore delle approvazioni: prima decide un revisore, poi un approvatore; chi invia non
// decide. La versione approvata resta in vigore e leggibile mentre si scrive la successiva.
// Ogni documento può avere una revisione periodica (mesi): il promemoria parte 14 giorni
// prima della scadenza verso approvatori e responsabile della pagina.

// ControlInput rende una pagina documento controllato (o la riporta a pagina semplice).
type ControlInput struct {
	Controlled   bool     `json:"controlled"`
	ReviewMonths *int     `json:"review_months"`
	Reviewers    []string `json:"reviewers"` // id dei membri
	Approvers    []string `json:"approvers"`
}

// SetControl configura il documento controllato: processo, revisori, approvatori, revisione.
func (s *Service) SetControl(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ControlInput) (*PageView, error) {
	el, sp, k, err := s.loadPage(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Write {
		return nil, engine.ErrForbidden
	}
	if err := validMonths(in.ReviewMonths); err != nil {
		return nil, err
	}
	if !in.Controlled {
		if !k.controlled {
			return s.GetPage(ctx, tx, actor, el.ID)
		}
		if k.approvedVersion != nil || (el.StateCategory != "" && el.StateCategory != "todo") {
			return nil, engine.InvalidCode("validation.knowledge.controlled_locked", "controlled", "il documento è già stato in revisione o approvato: resta controllato (si può rendere obsoleto)")
		}
		if _, err := tx.Exec(ctx, `update kb_pages set controlled = false, review_months = null, next_review = null where element_id = $1`, el.ID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `update elements set process_version_id = null, state_id = null, version = version + 1 where id = $1`, el.ID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `delete from stakeholders sh using stakeholder_roles r
			where r.tenant_id = sh.tenant_id and r.id = sh.role_id and sh.element_id = $1 and r.key in ($2, $3)`, el.ID, RoleReviewer, RoleApprover); err != nil {
			return nil, err
		}
		if err := s.controlAudit(ctx, tx, actor, el.ID, false); err != nil {
			return nil, err
		}
		return s.GetPage(ctx, tx, actor, el.ID)
	}
	for _, list := range [][]string{in.Reviewers, in.Approvers} {
		for _, m := range list {
			ok, err := memberCanRead(ctx, tx, sp.ID, m)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, engine.InvalidParams("validation.knowledge.reviewers_space", "reviewers", map[string]any{"space": sp.Key}, "revisori e approvatori devono poter leggere lo spazio %s", sp.Key)
			}
		}
	}
	if _, err := tx.Exec(ctx, `update kb_pages set controlled = true, review_months = $2,
		next_review = case when $2::int is null then null
		                   when approved_version is not null and review_months is distinct from $2
		                        then (current_date + make_interval(months => $2::int))::date
		                   else next_review end
		where element_id = $1`, el.ID, in.ReviewMonths); err != nil {
		return nil, err
	}
	if el.ProcessVersionID == nil {
		if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
			return nil, err
		}
	}
	if err := setPeople(ctx, tx, actor, el.ID, RoleReviewer, in.Reviewers); err != nil {
		return nil, err
	}
	if err := setPeople(ctx, tx, actor, el.ID, RoleApprover, in.Approvers); err != nil {
		return nil, err
	}
	if !k.controlled {
		if err := s.controlAudit(ctx, tx, actor, el.ID, true); err != nil {
			return nil, err
		}
	}
	return s.GetPage(ctx, tx, actor, el.ID)
}

func (s *Service) controlAudit(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, on bool) error {
	return audit.Append(ctx, tx, audit.Event{Action: "page.control", EntityType: TypePage, EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"controlled": on}})
}

// setPeople allinea i titolari del ruolo alla lista (i nuovi ricevono la notifica del ruolo).
func setPeople(ctx context.Context, tx pgx.Tx, actor engine.Actor, pageID, role string, members []string) error {
	members = slices.Compact(slices.Sorted(slices.Values(members)))
	if _, err := tx.Exec(ctx, `delete from stakeholders sh using stakeholder_roles r
		where r.tenant_id = sh.tenant_id and r.id = sh.role_id and sh.element_id = $1 and r.key = $2
		  and (sh.member_id is null or not (sh.member_id::text = any($3)))`, pageID, role, members); err != nil {
		return err
	}
	for _, m := range members {
		if m == "" {
			continue
		}
		if err := engine.SetStakeholder(ctx, tx, actor, pageID, role, m, "", false); err != nil {
			return err
		}
	}
	return nil
}

// memberCanRead: il membro può leggere lo spazio (ruolo sullo spazio, oppure spazio aperto e
// element.read nel tenant, oppure amministratore).
func memberCanRead(ctx context.Context, tx pgx.Tx, spaceID, memberID string) (bool, error) {
	if !uuidRe.MatchString(memberID) {
		return false, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, `
		select exists (select 1 from members where id::text = $2 and status = 'active') and (
			space_access($1, $2::uuid) && array['*', 'element.read', 'element.write']
			or (not space_restricted($1) and exists (
				select 1 from role_assignments ra join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
				where ra.space_id is null and r.permissions && array['*', 'element.read']
				  and (ra.member_id = $2::uuid or ra.group_id in (select member_groups($2::uuid))))))`,
		spaceID, memberID).Scan(&ok)
	return ok, err
}

// ReviewDone registra la revisione periodica senza modifiche: la prossima scadenza si sposta
// di review_months. Lo fanno gli approvatori del documento o chi amministra lo spazio.
func (s *Service) ReviewDone(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, note string) (*PageView, error) {
	el, sp, k, err := s.loadPage(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, err
	}
	if !k.controlled || k.approvedVersion == nil || el.StateCategory != "done" || k.reviewMonths == nil {
		return nil, engine.InvalidCode("validation.knowledge.periodic_review_na", "page", "la revisione periodica si conferma su un documento controllato approvato con una periodicità")
	}
	ok, err := engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{RoleApprover})
	if err != nil {
		return nil, err
	}
	if !ok && !sp.Rights.Manage {
		return nil, engine.ErrForbidden
	}
	var next time.Time
	if err := tx.QueryRow(ctx, `update kb_pages set next_review = (current_date + make_interval(months => review_months))::date, reminded_for = null
		where element_id = $1 returning next_review`, el.ID).Scan(&next); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "page", "reviewed", map[string]any{"next_review": next.Format("2006-01-02"), "note": note}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "page.review", EntityType: TypePage, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"version": k.version, "next_review": next.Format("2006-01-02")}}); err != nil {
		return nil, err
	}
	return s.GetPage(ctx, tx, actor, el.ID)
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// reminderDays: giorni di anticipo del promemoria di revisione periodica.
const reminderDays = 14

// RunReminders controlla le revisioni periodiche a intervalli finché ctx non è chiuso.
func (s *Service) RunReminders(ctx context.Context, d *db.DB, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = time.Hour
	}
	run := func() {
		n, err := s.CheckReminders(ctx, d)
		if err != nil {
			log.Warn("promemoria delle revisioni dei documenti", "errore", err)
		} else if n > 0 {
			log.Info("promemoria delle revisioni dei documenti inviati", "numero", n)
		}
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// CheckReminders controlla tutti i tenant (solo l'elenco passa dal ruolo di piattaforma).
func (s *Service) CheckReminders(ctx context.Context, d *db.DB) (int, error) {
	rows, err := d.Owner.Query(ctx, `select id::text from tenants where status = 'active'`)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	total := 0
	var errs []error
	for _, t := range tenants {
		err := d.InTenant(ctx, t, db.TenantOpts{}, func(tx pgx.Tx) error {
			n, err := s.CheckRemindersTenant(ctx, tx)
			total += n
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}

// CheckRemindersTenant manda i promemoria del tenant della transazione: una volta per
// scadenza, agli approvatori e al responsabile della pagina (chi l'ha creata).
func (s *Service) CheckRemindersTenant(ctx context.Context, tx pgx.Tx) (int, error) {
	rows, err := tx.Query(ctx, `
		select k.element_id::text, k.next_review, coalesce(e.reporter_id::text, '')
		from kb_pages k
		join elements e on e.tenant_id = k.tenant_id and e.id = k.element_id and e.deleted_at is null
		join options o on o.tenant_id = e.tenant_id and o.id = e.state_id and o.category = 'done'
		where k.controlled and k.next_review is not null and k.next_review - $1::int <= current_date
		  and k.reminded_for is distinct from k.next_review
		for update of k skip locked`, reminderDays)
	if err != nil {
		return 0, err
	}
	type due struct {
		id, owner string
		at        time.Time
	}
	var list []due
	var x due
	if _, err := pgx.ForEachRow(rows, []any{&x.id, &x.at, &x.owner}, func() error { list = append(list, x); return nil }); err != nil {
		return 0, err
	}
	sent := 0
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, it := range list {
		prow, err := tx.Query(ctx, `select coalesce(sh.member_id::text, ''), coalesce(sh.group_id::text, '') from stakeholders sh
			join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = $2
			where sh.element_id = $1`, it.id, RoleApprover)
		if err != nil {
			return sent, err
		}
		var members, groups []string
		var m, g string
		if _, err := pgx.ForEachRow(prow, []any{&m, &g}, func() error {
			if m != "" {
				members = append(members, m)
			}
			if g != "" {
				groups = append(groups, g)
			}
			return nil
		}); err != nil {
			return sent, err
		}
		if it.owner != "" {
			members = append(members, it.owner)
		}
		event := "review_due"
		if it.at.Before(today) {
			event = "review_overdue"
		}
		if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindDocument, ElementID: it.id, Members: members, Groups: groups,
			Payload: map[string]any{"event": event, "due": it.at.Format("2006-01-02")}}); err != nil {
			return sent, err
		}
		if _, err := tx.Exec(ctx, `update kb_pages set reminded_for = next_review where element_id = $1`, it.id); err != nil {
			return sent, err
		}
		sent++
	}
	n, err := s.checkReadingReminders(ctx, tx)
	return sent + n, err
}
