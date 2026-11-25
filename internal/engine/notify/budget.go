package notify

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/i18n"
)

// KindBudget: budget gerarchico (replica/budget.md §3): avviso ai responsabili prima della
// chiusura di un periodo («Ti restano 40.000 € non impegnati nel semestre»). Sta in un file
// a parte per aggiungersi a Kinds senza toccare la lista degli altri moduli. Non è legato a
// un elemento: la notifica ha element_id nullo.
const KindBudget = "budget"

func init() { Kinds = append(Kinds, KindBudget) }

func describeBudget(lang string, it Item) string {
	str := func(k string) string { s, _ := it.Payload[k].(string); return s }
	en := lang == i18n.En
	switch str("event") {
	case "period_closing":
		days, _ := it.Payload["days"].(float64)
		cents, _ := it.Payload["residual_cents"].(float64)
		if en {
			when := fmt.Sprintf("in %d days", int(days))
			switch int(days) {
			case 0:
				when = "today"
			case 1:
				when = "tomorrow"
			}
			return fmt.Sprintf("You have %s not committed in “%s” (%s): the period closes %s",
				euroEn(int64(cents)), str("period"), str("node"), when)
		}
		when := fmt.Sprintf("tra %d giorni", int(days))
		switch int(days) {
		case 0:
			when = "oggi"
		case 1:
			when = "domani"
		}
		return fmt.Sprintf("Ti restano %s non impegnati in «%s» (%s): il periodo si chiude %s",
			euro(int64(cents)), str("period"), str("node"), when)
	}
	return "Budget"
}

// euroEn formatta centesimi all'inglese: «€40,000» (con i decimali solo se servono).
func euroEn(c int64) string {
	it := euro(c) // «-40.000,50 €»
	it = strings.TrimSuffix(it, " €")
	neg := strings.HasPrefix(it, "-")
	it = strings.TrimPrefix(it, "-")
	it = strings.NewReplacer(".", ",", ",", ".").Replace(it)
	if neg {
		return "-€" + it
	}
	return "€" + it
}

// euro formatta centesimi come «40.000 €» (con i decimali solo se servono).
func euro(c int64) string {
	neg := c < 0
	if neg {
		c = -c
	}
	units := fmt.Sprint(c / 100)
	var b strings.Builder
	for i, r := range units {
		if i > 0 && (len(units)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if c%100 != 0 {
		out += fmt.Sprintf(",%02d", c%100)
	}
	if neg {
		out = "-" + out
	}
	return out + " €"
}

// EmitDirect scrive una notifica senza elemento per ogni membro attivo indicato (rispettando le
// preferenze di silenziamento) e, se le email sono attive, l'evento nell'outbox. Restituisce i
// destinatari.
func EmitDirect(ctx context.Context, tx pgx.Tx, kind string, members []string, payload map[string]any) ([]string, error) {
	if !ValidKind(kind) || len(members) == 0 {
		return nil, nil
	}
	if payload == nil {
		payload = map[string]any{}
	}
	rows, err := tx.Query(ctx, `
		with rcpt as (
			select m.id as member_id from members m left join notification_prefs p on p.member_id = m.id
			where m.id = any($1::uuid[]) and m.status = 'active' and not ($2 = any(coalesce(p.muted_kinds, '{}')))
		),
		ins as (
			insert into notifications (tenant_id, member_id, kind, payload)
			select app_tenant(), member_id, $2, $3 from rcpt returning id, member_id
		),
		mail as (
			insert into outbox_events (tenant_id, kind, payload)
			select app_tenant(), 'notification.email', jsonb_build_object('notification_id', ins.id, 'member_id', ins.member_id)
			from ins left join notification_prefs p on p.member_id = ins.member_id
			where $4 and not ($2 = any(coalesce(p.email_muted_kinds, '{}')))
			returning 1
		)
		select member_id::text from ins`, members, kind, payload, EmailEnabled())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
