package notify

import "regia/internal/platform/i18n"

// KindAI: avvisi dell'AI (replica/ai.md §4). Eventi nel payload:
//   - budget_warning: il consumo del mese ha superato l'80% del budget dell'AI; va una volta
//     al mese a chi amministra l'organizzazione. Il payload porta la percentuale e il mese.
//
// Sta in un file a parte per aggiungersi a Kinds senza toccare la lista degli altri moduli. Non
// è legato a un elemento: la notifica ha element_id nullo.
const KindAI = "ai"

// AdminsSQL sono i membri attivi del tenant con il permesso di amministrarlo (ruoli a livello
// di tenant, anche tramite gruppo).
const AdminsSQL = `select distinct m.id::text from members m
	join role_assignments ra on ra.space_id is null and (ra.member_id = m.id or ra.group_id in (select member_groups(m.id)))
	join roles ro on ro.tenant_id = ra.tenant_id and ro.id = ra.role_id
	where m.status = 'active' and ro.permissions && array['*', 'tenant.admin']`

func init() {
	Kinds = append(Kinds, KindAI)
	i18n.Register(map[string]i18n.Msg{
		"notify.ai.budget_warning": {It: "Il budget dell'AI di {month} è al {percent}%: al 100% le funzioni dell'AI si fermano fino al mese dopo",
			En: "The AI budget for {month} is at {percent}%: at 100% the AI features stop until next month"},
		"notify.ai": {It: "Avviso dell'AI", En: "AI notice"},
	})
}
