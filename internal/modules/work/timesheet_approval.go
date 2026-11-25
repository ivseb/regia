package work

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/engine/notify"
	"regia/internal/platform/audit"
)

// Timesheet nel motore delle approvazioni (replica/work.md «Tempo e team»).
//
// La settimana inviata apre una richiesta con soggetto «timesheet» (migrazione 0074): senza
// elemento, con un solo passo il cui insieme di approvatori (approval_step_members) basta che
// ne decida uno. Compare in «Le mie approvazioni» (e vale la delega a tempo) e si decide da lì
// o dal timesheet con lo stesso effetto: l'esito arriva a closeTimesheet.
//
// Regola di transizione: gli approvatori si scelgono all'invio con le regole del tempo di quel
// momento (responsabile del team, persona scelta, amministratori del tempo) e restano quelli. Se
// l'organizzazione cambia chi approva, le settimane già inviate non si spostano: per le nuove
// vale la nuova regola. Gli amministratori del tempo possono sempre decidere: se non sono
// nell'insieme vi si aggiungono al momento di decidere dal timesheet.

// SubjectTimesheet è il soggetto delle richieste di approvazione di una settimana di timesheet.
const SubjectTimesheet = "timesheet"

// UseApprovals collega il timesheet al motore delle approvazioni: la reazione all'esito e la
// separazione dei compiti (nessuno decide la propria settimana). Senza, la decisione dal
// timesheet resta quella diretta, senza richiesta da decidere dall'inbox.
func (s *Service) UseApprovals(ap *approval.Service) {
	if ap == nil {
		return
	}
	s.ap = ap
	ap.OnClose(SubjectTimesheet, s.closeTimesheet)
	ap.SeparateRequester(SubjectTimesheet)
}

// approverPool: chi può decidere la settimana della persona, secondo le regole del tempo.
func approverPool(ctx context.Context, tx pgx.Tx, st TimeSettings, memberID string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		select m.id::text from members m
		where m.status = 'active' and m.id <> $1::uuid and (
		  ($2::text = 'member' and m.id = $3::uuid)
		  or ($2::text = 'team_lead' and m.id in (select t.lead_id from team_members tm
		        join teams t on t.tenant_id = tm.tenant_id and t.id = tm.team_id
		        where tm.member_id = $1::uuid and not t.archived and t.lead_id is not null))
		  or exists (select 1 from role_assignments ra join roles ro on ro.tenant_id = ra.tenant_id and ro.id = ra.role_id
		        where ra.space_id is null and ro.permissions && array['*', 'tenant.admin', 'finance.write']
		          and (ra.member_id = m.id or ra.group_id in (select member_groups(m.id)))))
		order by m.display_name`, memberID, st.ApproverMode, st.ApproverID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// openRequest apre la richiesta di approvazione della settimana inviata e avvisa chi approva.
func (s *Service) openRequest(ctx context.Context, tx pgx.Tx, memberID, week string, notifyApprovers bool) (string, error) {
	st, err := loadTimeSettings(ctx, tx)
	if err != nil {
		return "", err
	}
	pool, err := approverPool(ctx, tx, st, memberID)
	if err != nil {
		return "", err
	}
	var reqID string
	if err := tx.QueryRow(ctx, "select timesheet_open_request(app_tenant(), $1::uuid, $2::date, $3::uuid[])", memberID, week, pool).Scan(&reqID); err != nil {
		return "", err
	}
	if !notifyApprovers {
		return reqID, nil
	}
	var name string
	if err := tx.QueryRow(ctx, "select display_name from members where id = $1", memberID).Scan(&name); err != nil {
		return "", err
	}
	return reqID, notify.TimesheetSubmitted(ctx, tx, memberID, name, week, reqID, pool)
}

// closeRequests chiude la richiesta aperta della settimana (status: cancelled, approved, rejected)
// e toglie gli assegnatari dall'inbox.
func closeRequests(ctx context.Context, tx pgx.Tx, memberID, week, status string) error {
	rows, err := tx.Query(ctx, `update approval_requests set status = $3, closed_at = now()
		where subject = 'timesheet' and status = 'pending' and timesheet_member_id = $1::uuid and timesheet_week = $2::date returning id::text`, memberID, week, status)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, "update approval_steps set decision = 'skipped' where request_id = $1 and decision = 'pending'", id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "select approval_refresh_assignees($1)", id); err != nil {
			return err
		}
	}
	return nil
}

// pendingStep: il passo da decidere della richiesta aperta della settimana ("" se non c'è).
func pendingStep(ctx context.Context, tx pgx.Tx, memberID, week string) (reqID, stepID string, err error) {
	err = tx.QueryRow(ctx, `select rq.id::text, st.id::text from approval_requests rq
		join approval_steps st on st.tenant_id = rq.tenant_id and st.request_id = rq.id and st.decision = 'pending'
		where rq.subject = 'timesheet' and rq.status = 'pending' and rq.timesheet_member_id = $1::uuid and rq.timesheet_week = $2::date`, memberID, week).Scan(&reqID, &stepID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	return reqID, stepID, err
}

// isRequestApprover: l'attore è tra gli approvatori della richiesta aperta della settimana (week
// vuota: di una qualsiasi settimana della persona, anche già decisa), direttamente o per delega.
func isRequestApprover(ctx context.Context, tx pgx.Tx, actor, memberID, week string) (bool, error) {
	var ok bool
	var w any
	if week != "" {
		w = week
	}
	err := tx.QueryRow(ctx, `select exists (
		select 1 from approval_requests rq
		join approval_steps st on st.tenant_id = rq.tenant_id and st.request_id = rq.id
		join approval_step_members pm on pm.tenant_id = st.tenant_id and pm.step_id = st.id
		where rq.subject = 'timesheet' and rq.timesheet_member_id = $2::uuid
		  and ($3::date is null or (rq.timesheet_week = $3::date and rq.status = 'pending'))
		  and pm.member_id in (select $1::uuid union
		        select d.from_member_id from delegations d where d.to_member_id = $1::uuid and d.scope in ('approvals', 'all')
		          and now() between d.valid_from and d.valid_to))`, actor, memberID, w).Scan(&ok)
	return ok, err
}

// mayDecideWeek: chi può approvare o respingere la settimana inviata.
func mayDecideWeek(ctx context.Context, tx pgx.Tx, actor engine.Actor, memberID, week string) (bool, error) {
	if actor.MemberID == memberID {
		return false, nil
	}
	if canAdminTime(actor) {
		return true, nil
	}
	return isRequestApprover(ctx, tx, actor.MemberID, memberID, week)
}

// closeTimesheet applica l'esito della richiesta alla settimana: è la reazione del motore, anche
// quando si decide dall'inbox. Il rinvio non esiste per il timesheet.
func (s *Service) closeTimesheet(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestID, outcome, comment string) error {
	var member, week string
	if err := tx.QueryRow(ctx, "select timesheet_member_id::text, to_char(timesheet_week, 'YYYY-MM-DD') from approval_requests where id = $1", requestID).Scan(&member, &week); err != nil {
		return err
	}
	status := "approved"
	switch outcome {
	case "rejected":
		status = "rejected"
	case "deferred":
		return engine.InvalidCode("validation.work.timesheet_no_defer", "decision", "il timesheet si approva o si respinge: non si rimanda")
	}
	return s.applyDecision(ctx, tx, actor, member, week, status, comment)
}

// applyDecision scrive l'esito sulla settimana inviata, lo registra e avvisa la persona.
func (s *Service) applyDecision(ctx context.Context, tx pgx.Tx, actor engine.Actor, member, week, status, comment string) error {
	tag, err := tx.Exec(ctx, `update timesheets set status = $4, decided_by = $3, decided_at = now(), comment = $5
		where member_id::text = $1 and week_start = $2::date and status = 'submitted'`, member, week, actor.MemberID, status, nullable(comment))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.InvalidCode("validation.work.week_not_pending", "week", "la settimana non è in attesa di approvazione")
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "timesheet." + status, EntityType: "member", EntityID: member,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"week": week, "comment": comment}}); err != nil {
		return err
	}
	return notify.TimesheetOutcome(ctx, tx, actor.MemberID, member, status, week, comment)
}
