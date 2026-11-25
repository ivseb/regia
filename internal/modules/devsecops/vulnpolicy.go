package devsecops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Politica di SLA delle vulnerabilità (replica/devsecops.md §3.2). Una tabella di giorni per
// gravità × criticità dell'applicazione, due fattori percentuali (funzione critica DORA ed
// esposizione su Internet) e le regole di escalation. La scadenza di una vulnerabilità è
// «prima rilevazione + giorni» e si ricalcola quando cambiano la politica, la criticità o
// l'esposizione dell'applicazione.

// VulnSeverities e VulnCriticalities sono le righe e le colonne della tabella degli SLA.
var (
	VulnSeverities    = []string{"critical", "high", "medium", "low"}
	VulnCriticalities = []string{"critical", "high", "medium", "low", "none"}
)

// openStages: gli stadi in cui una vulnerabilità è ancora da correggere e il tempo scorre.
const openStages = `('open','taken','in_progress')`

// Escalation: un passo avvisa i destinatari quando la scadenza è a Days giorni (negativo =
// prima, 0 = alla scadenza, positivo = dopo). I destinatari sono ruoli sulla vulnerabilità:
// assignee, team (tutto il team), team_lead (referente), unit_manager (responsabile
// dell'unità del team), parent_manager (responsabile dell'unità sopra) o group:<id>.
type EscStep struct {
	Name string   `json:"name"`
	Days int      `json:"days"`
	To   []string `json:"to"`
}

// VulnPolicy è la politica del tenant.
type VulnPolicy struct {
	Matrix              map[string]map[string]*int `json:"matrix"`
	DoraPercent         int                        `json:"dora_percent"`
	ExposedPercent      int                        `json:"exposed_percent"`
	ExposureField       string                     `json:"exposure_field"`
	ExposureValues      []string                   `json:"exposure_values"`
	SoonDays            int                        `json:"soon_days"`
	AutoAssign          bool                       `json:"auto_assign"`
	ExceptionNoticeDays int                        `json:"exception_notice_days"`
	MaxExceptionDays    int                        `json:"max_exception_days"`
	Escalation          []EscStep                  `json:"escalation"`
	UpdatedAt           *time.Time                 `json:"updated_at"`
	Default             bool                       `json:"default"`
}

func ip(n int) *int { return &n }

// DefaultVulnPolicy: valori di partenza, modificabili per tenant.
func DefaultVulnPolicy() VulnPolicy {
	return VulnPolicy{
		Matrix: map[string]map[string]*int{
			"critical": {"critical": ip(3), "high": ip(7), "medium": ip(14), "low": ip(30), "none": ip(14)},
			"high":     {"critical": ip(7), "high": ip(14), "medium": ip(30), "low": ip(60), "none": ip(30)},
			"medium":   {"critical": ip(30), "high": ip(45), "medium": ip(90), "low": ip(120), "none": ip(90)},
			"low":      {"critical": ip(90), "high": ip(120), "medium": ip(180), "low": nil, "none": ip(180)},
		},
		DoraPercent: 50, ExposedPercent: 75, ExposureValues: []string{"true", "sì", "si", "internet", "esposta", "pubblica"},
		SoonDays: 7, AutoAssign: true, ExceptionNoticeDays: 14, MaxExceptionDays: 180,
		Escalation: []EscStep{
			{Name: "Preavviso", Days: -7, To: []string{"assignee", "team_lead"}},
			{Name: "Scaduta", Days: 0, To: []string{"assignee", "team", "team_lead"}},
			{Name: "Primo livello sopra", Days: 7, To: []string{"unit_manager"}},
			{Name: "Livello superiore", Days: 21, To: []string{"parent_manager"}},
		},
		Default: true,
	}
}

func loadVulnPolicy(ctx context.Context, tx pgx.Tx) (VulnPolicy, error) {
	p := DefaultVulnPolicy()
	var rawM, rawE []byte
	var field *string
	err := tx.QueryRow(ctx, `select matrix, dora_percent, exposed_percent, exposure_field, exposure_values, soon_days, auto_assign,
		exception_notice_days, max_exception_days, escalation, updated_at from vuln_policy`).
		Scan(&rawM, &p.DoraPercent, &p.ExposedPercent, &field, &p.ExposureValues, &p.SoonDays, &p.AutoAssign,
			&p.ExceptionNoticeDays, &p.MaxExceptionDays, &rawE, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	p.Default = false
	p.ExposureField = ""
	if field != nil {
		p.ExposureField = *field
	}
	p.Matrix = nil
	if err := json.Unmarshal(rawM, &p.Matrix); err != nil {
		return p, err
	}
	p.Escalation = []EscStep{}
	if err := json.Unmarshal(rawE, &p.Escalation); err != nil {
		return p, err
	}
	if p.ExposureValues == nil {
		p.ExposureValues = []string{}
	}
	return p, nil
}

var groupRe = regexp.MustCompile(`^group:[0-9a-f-]{36}$`)

// validate normalizza e controlla la politica; i gruppi citati devono esistere.
func (p *VulnPolicy) validate(ctx context.Context, tx pgx.Tx) error {
	m := map[string]map[string]*int{}
	for _, sev := range VulnSeverities {
		m[sev] = map[string]*int{}
		for _, crit := range VulnCriticalities {
			var v *int
			if p.Matrix != nil && p.Matrix[sev] != nil {
				v = p.Matrix[sev][crit]
			}
			if v != nil && (*v < 1 || *v > 3650) {
				return engine.InvalidParams("validation.devsecops.vuln_days_range", "matrix", map[string]any{"severity": sev, "criticality": crit}, "i giorni vanno da 1 a 3650 (gravità %s, criticità %s)", sev, crit)
			}
			m[sev][crit] = v
		}
	}
	p.Matrix = m
	if p.DoraPercent < 10 || p.DoraPercent > 300 || p.ExposedPercent < 10 || p.ExposedPercent > 300 {
		return engine.OutOfRange("percent", 10, 300, "le percentuali vanno da 10 a 300")
	}
	if p.SoonDays < 1 || p.SoonDays > 90 {
		return engine.OutOfRange("soon_days", 1, 90, "«in scadenza» vale da 1 a 90 giorni")
	}
	if p.ExceptionNoticeDays < 0 || p.ExceptionNoticeDays > 120 {
		return engine.OutOfRange("exception_notice_days", 0, 120, "preavviso tra 0 e 120 giorni")
	}
	if p.MaxExceptionDays < 1 || p.MaxExceptionDays > 1095 {
		return engine.OutOfRange("max_exception_days", 1, 1095, "durata massima tra 1 e 1095 giorni")
	}
	p.ExposureField = strings.TrimSpace(p.ExposureField)
	if p.ExposureField != "" {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from field_defs where key = $1)", p.ExposureField).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return engine.NotFound("exposure_field", "field", "campo personalizzato inesistente: %s", p.ExposureField)
		}
	}
	vals := []string{}
	for _, v := range p.ExposureValues {
		if v = strings.ToLower(clip(v, 60)); v != "" && !slices.Contains(vals, v) && len(vals) < 20 {
			vals = append(vals, v)
		}
	}
	if p.ExposureField != "" && len(vals) == 0 {
		return engine.Required("exposure_values", "indica almeno un valore che significa «esposta su Internet»")
	}
	p.ExposureValues = vals
	if len(p.Escalation) > 8 {
		return engine.InvalidCode("validation.devsecops.escalation_too_many", "escalation", "al massimo 8 passi")
	}
	for i := range p.Escalation {
		st := &p.Escalation[i]
		st.Name = clip(st.Name, 60)
		if st.Name == "" {
			return engine.Required("escalation", "passo %d: serve un nome", i+1)
		}
		if st.Days < -90 || st.Days > 365 {
			return engine.InvalidParams("validation.devsecops.escalation_step_days", "escalation", map[string]any{"step": i + 1}, "passo %d: i giorni vanno da -90 a 365", i+1)
		}
		if i > 0 && st.Days <= p.Escalation[i-1].Days {
			return engine.InvalidParams("validation.devsecops.escalation_order", "escalation", map[string]any{"step": i + 1}, "i passi vanno in ordine di tempo (il passo %d non è dopo il precedente)", i+1)
		}
		if len(st.To) == 0 {
			return engine.InvalidParams("validation.devsecops.escalation_step_notify", "escalation", map[string]any{"step": i + 1}, "passo %d: indica chi avvisare", i+1)
		}
		seen := []string{}
		for _, to := range st.To {
			switch {
			case to == "assignee" || to == "team" || to == "team_lead" || to == "unit_manager" || to == "parent_manager":
			case groupRe.MatchString(to):
				var ok bool
				if err := tx.QueryRow(ctx, "select exists (select 1 from groups where id::text = $1)", strings.TrimPrefix(to, "group:")).Scan(&ok); err != nil {
					return err
				}
				if !ok {
					return engine.NotFound("escalation", "", "passo %d: gruppo inesistente", i+1)
				}
			default:
				return engine.InvalidValue("escalation", "passo %d: destinatario %q non valido", i+1, to)
			}
			if !slices.Contains(seen, to) {
				seen = append(seen, to)
			}
		}
		st.To = seen
	}
	if p.Escalation == nil {
		p.Escalation = []EscStep{}
	}
	return nil
}

// GetVulnPolicy restituisce la politica del tenant (o quella di partenza).
func (s *Service) GetVulnPolicy(ctx context.Context, tx pgx.Tx, actor engine.Actor) (VulnPolicy, error) {
	return loadVulnPolicy(ctx, tx)
}

// SaveVulnPolicy salva la politica e ricalcola le scadenze delle vulnerabilità aperte.
func (s *Service) SaveVulnPolicy(ctx context.Context, tx pgx.Tx, actor engine.Actor, in VulnPolicy) (VulnPolicy, int, error) {
	if !CanConfigure(actor) {
		return VulnPolicy{}, 0, engine.ErrForbidden
	}
	if err := in.validate(ctx, tx); err != nil {
		return VulnPolicy{}, 0, err
	}
	rawM, _ := json.Marshal(in.Matrix)
	rawE, _ := json.Marshal(in.Escalation)
	if _, err := tx.Exec(ctx, `insert into vuln_policy (tenant_id, matrix, dora_percent, exposed_percent, exposure_field, exposure_values, soon_days,
		auto_assign, exception_notice_days, max_exception_days, escalation, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
		on conflict (tenant_id) do update set matrix = excluded.matrix, dora_percent = excluded.dora_percent, exposed_percent = excluded.exposed_percent,
		  exposure_field = excluded.exposure_field, exposure_values = excluded.exposure_values, soon_days = excluded.soon_days,
		  auto_assign = excluded.auto_assign, exception_notice_days = excluded.exception_notice_days, max_exception_days = excluded.max_exception_days,
		  escalation = excluded.escalation, updated_by = excluded.updated_by, updated_at = now()`,
		rawM, in.DoraPercent, in.ExposedPercent, nullable(in.ExposureField), in.ExposureValues, in.SoonDays, in.AutoAssign,
		in.ExceptionNoticeDays, in.MaxExceptionDays, rawE, nullable(actor.MemberID)); err != nil {
		return VulnPolicy{}, 0, err
	}
	pol, err := loadVulnPolicy(ctx, tx)
	if err != nil {
		return pol, 0, err
	}
	n, err := s.recomputeDue(ctx, tx, pol, "")
	if err != nil {
		return pol, 0, err
	}
	// La politica è cambiata: i passi di escalation già notificati si rivalutano da capo.
	if _, err := tx.Exec(ctx, "update findings set notice_level = 0 where notice_level > 0"); err != nil {
		return pol, 0, err
	}
	return pol, n, auditEvent(ctx, tx, actor, "devsecops.vuln_policy", "vuln_policy", "", map[string]any{"recomputed": n,
		"dora_percent": pol.DoraPercent, "exposed_percent": pol.ExposedPercent, "steps": len(pol.Escalation)})
}

// dueCalc è la tabella dei giorni di SLA per ogni vulnerabilità da correggere. Parametri:
// $1 matrice, $2 percentuale DORA, $3 percentuale di esposizione, $4 campo di esposizione,
// $5 valori di esposizione, $6 servizio (nullo = tutti), $8 istante corrente.
const dueCalc = `
	with calc as (
		select f.id, f.severity, coalesce(a.criticality, 'none') as crit, a.owner_team_id,
		       case when base.v is null then null else greatest(1, round(base.v
		         * (case when coalesce(a.supports_critical_function, false) then $2::numeric / 100 else 1 end)
		         * (case when $4::text <> '' and lower(coalesce(ae.fields ->> $4::text, '')) = any($5::text[]) then $3::numeric / 100 else 1 end)))::int end as days,
		       f.first_seen
		from findings f
		left join ext_application a on a.tenant_id = f.tenant_id and a.element_id = f.application_element_id
		left join elements ae on ae.tenant_id = a.tenant_id and ae.id = a.element_id
		left join lateral (select nullif($1::jsonb -> f.severity ->> coalesce(a.criticality, 'none'), '')::numeric as v) base on true
		where f.stage in ` + openStages + ` and ($6::uuid is null or f.service_id = $6::uuid) and ($7::boolean is not null or $8::timestamptz is not null or true)
	), due as (
		select c.*, case when c.days is null then null else c.first_seen + c.days * interval '1 day' end as due_at from calc c
	)`

func (p VulnPolicy) dueArgs(serviceID string, now time.Time) []any {
	raw, _ := json.Marshal(p.Matrix)
	var svc any
	if serviceID != "" {
		svc = serviceID
	}
	return []any{raw, p.DoraPercent, p.ExposedPercent, p.ExposureField, p.ExposureValues, svc, p.AutoAssign, now}
}

// recomputeDue calcola scadenza e team delle vulnerabilità da correggere (di un servizio,
// o di tutti con serviceID vuoto). Restituisce quante righe sono cambiate.
func (s *Service) recomputeDue(ctx context.Context, tx pgx.Tx, p VulnPolicy, serviceID string) (int, error) {
	tag, err := tx.Exec(ctx, dueCalc+`
		update findings f set sla_days = d.days, due_at = d.due_at,
		  team_id = case when $7::boolean and not f.team_manual then d.owner_team_id else f.team_id end
		from due d where d.id = f.id
		  and (f.sla_days is distinct from d.days or f.due_at is distinct from d.due_at
		       or ($7::boolean and not f.team_manual and f.team_id is distinct from d.owner_team_id))`, p.dueArgs(serviceID, s.now())...)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// ---------------------------------------------------------------------------
// Anteprima: quante vulnerabilità aperte scadrebbero con la politica proposta.

// PreviewCell è una casella della tabella degli SLA con le vulnerabilità che ci cadono.
type PreviewCell struct {
	Severity    string `json:"severity"`
	Criticality string `json:"criticality"`
	Open        int    `json:"open"`
	Overdue     int    `json:"overdue"`
	Soon        int    `json:"soon"`
}

// PreviewTotals sono i totali delle vulnerabilità da correggere.
type PreviewTotals struct {
	Open    int `json:"open"`
	NoSLA   int `json:"no_sla"`
	Overdue int `json:"overdue"`
	Soon    int `json:"soon"`
}

// PolicyPreview confronta la politica salvata con quella proposta.
type PolicyPreview struct {
	Cells    []PreviewCell `json:"cells"`
	Proposed PreviewTotals `json:"proposed"`
	Current  PreviewTotals `json:"current"`
	// Apps: le applicazioni con più scadute con la politica proposta (al massimo 5).
	Apps []PreviewApp `json:"apps"`
}

// PreviewApp è il conto di un'applicazione nell'anteprima.
type PreviewApp struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Overdue int    `json:"overdue"`
	Now     int    `json:"now"`
}

func (s *Service) previewTotals(ctx context.Context, tx pgx.Tx, p VulnPolicy) (map[[2]string]*PreviewCell, PreviewTotals, error) {
	args := p.dueArgs("", s.now())
	args = append(args, p.SoonDays)
	rows, err := tx.Query(ctx, dueCalc+`
		select severity, crit, count(*), count(*) filter (where due_at is null),
		       count(*) filter (where due_at < $8), count(*) filter (where due_at >= $8 and due_at < $8 + $9::int * interval '1 day')
		from due group by 1, 2`, args...)
	if err != nil {
		return nil, PreviewTotals{}, err
	}
	cells := map[[2]string]*PreviewCell{}
	var tot PreviewTotals
	var sev, crit string
	var open, nosla, over, soon int
	if _, err := pgx.ForEachRow(rows, []any{&sev, &crit, &open, &nosla, &over, &soon}, func() error {
		cells[[2]string{sev, crit}] = &PreviewCell{Severity: sev, Criticality: crit, Open: open, Overdue: over, Soon: soon}
		tot.Open += open
		tot.NoSLA += nosla
		tot.Overdue += over
		tot.Soon += soon
		return nil
	}); err != nil {
		return nil, tot, err
	}
	return cells, tot, nil
}

// PreviewVulnPolicy calcola l'effetto della politica proposta sulle vulnerabilità di oggi,
// senza salvare nulla.
func (s *Service) PreviewVulnPolicy(ctx context.Context, tx pgx.Tx, actor engine.Actor, in VulnPolicy) (*PolicyPreview, error) {
	if !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	if err := in.validate(ctx, tx); err != nil {
		return nil, err
	}
	cur, err := loadVulnPolicy(ctx, tx)
	if err != nil {
		return nil, err
	}
	cells, proposed, err := s.previewTotals(ctx, tx, in)
	if err != nil {
		return nil, err
	}
	_, current, err := s.previewTotals(ctx, tx, cur)
	if err != nil {
		return nil, err
	}
	out := &PolicyPreview{Cells: []PreviewCell{}, Proposed: proposed, Current: current, Apps: []PreviewApp{}}
	for _, sev := range VulnSeverities {
		for _, crit := range VulnCriticalities {
			c := cells[[2]string{sev, crit}]
			if c == nil {
				c = &PreviewCell{Severity: sev, Criticality: crit}
			}
			out.Cells = append(out.Cells, *c)
		}
	}
	// Per applicazione: scadute con la politica proposta e con quella attuale.
	args := in.dueArgs("", s.now())
	rows, err := tx.Query(ctx, dueCalc+`
		select s.key || '-' || e.number, e.title, count(*) filter (where d.due_at < $8)
		from due d join findings f on f.id = d.id
		join elements e on e.tenant_id = f.tenant_id and e.id = f.application_element_id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		group by 1, 2 having count(*) filter (where d.due_at < $8) > 0
		order by 3 desc, 2 limit 5`, args...)
	if err != nil {
		return nil, err
	}
	var a PreviewApp
	if _, err := pgx.ForEachRow(rows, []any{&a.Key, &a.Name, &a.Overdue}, func() error {
		out.Apps = append(out.Apps, a)
		return nil
	}); err != nil {
		return nil, err
	}
	for i := range out.Apps {
		err := tx.QueryRow(ctx, `select count(*) from findings f join elements e on e.tenant_id = f.tenant_id and e.id = f.application_element_id
			join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			where f.stage in `+openStages+` and f.due_at < $2 and s.key || '-' || e.number = $1`, out.Apps[i].Key, s.now()).Scan(&out.Apps[i].Now)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// sevRankOf: punteggio di rischio di una gravità, per le soglie delle approvazioni.
func sevScore(sev string) int {
	switch sev {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	}
	return 1
}

func daysText(n int) string {
	if n == 1 {
		return "1 giorno"
	}
	return fmt.Sprintf("%d giorni", n)
}
