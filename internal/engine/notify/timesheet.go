package notify

import (
	"context"
	"net/url"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/i18n"
)

// KindTimesheet: timesheet settimanale (replica/work.md «Tempo e team»). Eventi nel payload:
//   - submitted: la settimana è stata inviata (a chi approva); più settimane inviate dalla stessa
//     persona restano una sola notifica non letta, con l'elenco in weeks e il numero in count;
//   - approved, rejected (con il motivo in comment): l'esito, alla persona;
//   - reopened: un amministratore del tempo ha riaperto la settimana approvata, alla persona.
//
// Non è legata a un elemento (element_id nullo): apre il timesheet della persona. Sta in un file
// a parte per aggiungersi a Kinds senza toccare la lista degli altri moduli.
const KindTimesheet = "timesheet"

func init() {
	Kinds = append(Kinds, KindTimesheet)
	i18n.Register(map[string]i18n.Msg{
		"notify.timesheet.submitted": {It: "{member} ha inviato {count} settimane di timesheet da approvare", En: "{member} sent {count} timesheet weeks for approval",
			OneIt: "{member} ha inviato il timesheet della settimana del {week} da approvare", OneEn: "{member} sent the timesheet for the week of {week} for approval"},
		"notify.timesheet.approved": {It: "{actor} ha approvato il tuo timesheet della settimana del {week}", En: "{actor} approved your timesheet for the week of {week}"},
		"notify.timesheet.rejected": {It: "{actor} ha respinto il tuo timesheet della settimana del {week}: {comment}", En: "{actor} rejected your timesheet for the week of {week}: {comment}"},
		"notify.timesheet.reopened": {It: "{actor} ha riaperto il tuo timesheet della settimana del {week}: puoi modificarlo e reinviarlo", En: "{actor} reopened your timesheet for the week of {week}: you can edit and resubmit it"},
		"notify.timesheet":          {It: "Timesheet della settimana del {week}", En: "Timesheet for the week of {week}"},
	})
}

// describeTimesheet è il testo delle notifiche KindTimesheet, nella lingua di chi le riceve.
func describeTimesheet(lang string, it Item, p i18n.P) string {
	if _, ok := it.Payload["count"]; !ok {
		p["count"] = 1
	}
	if a, _ := it.Payload["actor"].(string); a != "" {
		p["actor"] = a // chi ha deciso: la notifica non ha un autore proprio
	}
	return byEvent(lang, "notify.timesheet", eventOf(it), p)
}

func eventOf(it Item) string {
	s, _ := it.Payload["event"].(string)
	return s
}

// TimesheetLink è il percorso dell'interfaccia che apre la settimana (per le email).
func TimesheetLink(member, week string) string {
	return "/tempo?persona=" + url.QueryEscape(member) + "&settimana=" + url.QueryEscape(week)
}

// TimesheetSubmitted avvisa chi approva che la persona ha inviato la settimana. Se un
// destinatario ha già una notifica non letta di invio della stessa persona, la settimana si
// aggiunge a quella (una notifica sola per più settimane inviate insieme); gli altri ricevono una
// notifica nuova, con preferenze e outbox delle email come le altre. Chi invia non è avvisato.
func TimesheetSubmitted(ctx context.Context, tx pgx.Tx, memberID, memberName, week, requestID string, approvers []string) error {
	to := make([]string, 0, len(approvers))
	for _, a := range approvers {
		if a != memberID {
			to = append(to, a)
		}
	}
	if len(to) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `
		update notifications n set created_at = now(),
		  payload = n.payload || jsonb_build_object('weeks', x.arr, 'count', jsonb_array_length(x.arr), 'week', $4::text)
		from (select o.id, (select coalesce(jsonb_agg(distinct v), '[]'::jsonb)
		                    from jsonb_array_elements_text(coalesce(o.payload->'weeks', '[]'::jsonb) || to_jsonb($4::text)) v) as arr
		      from notifications o
		      where o.kind = $1 and o.read_at is null and o.member_id = any($2::uuid[])
		        and o.payload->>'event' = 'submitted' and o.payload->>'member_id' = $3) x
		where n.id = x.id
		returning n.member_id::text`, KindTimesheet, to, memberID, week)
	if err != nil {
		return err
	}
	merged, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, m := range merged {
		have[m] = true
	}
	fresh := make([]string, 0, len(to))
	for _, a := range to {
		if !have[a] {
			fresh = append(fresh, a)
		}
	}
	_, err = EmitDirect(ctx, tx, KindTimesheet, fresh, map[string]any{"event": "submitted", "member_id": memberID, "member": memberName,
		"week": week, "weeks": []string{week}, "count": 1, "request_id": requestID})
	return err
}

// TimesheetOutcome avvisa la persona dell'esito della sua settimana (event: approved, rejected,
// reopened). Se a decidere è la persona stessa non c'è nulla da avvisare.
func TimesheetOutcome(ctx context.Context, tx pgx.Tx, actorID, memberID, event, week, comment string) error {
	if actorID == memberID {
		return nil
	}
	p := map[string]any{"event": event, "member_id": memberID, "week": week}
	if comment != "" {
		p["comment"] = comment
	}
	if actorID != "" {
		var name string
		if err := tx.QueryRow(ctx, "select display_name from members where id = $1", actorID).Scan(&name); err == nil {
			p["actor"] = name
		}
	}
	_, err := EmitDirect(ctx, tx, KindTimesheet, []string{memberID}, p)
	return err
}
