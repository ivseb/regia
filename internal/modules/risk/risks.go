package risk

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/engine/notify"
	"regia/internal/platform/rbac"
)

// Trattamenti: chiave interna e nome italiano.
var treatments = map[string]string{"mitigate": "mitigare", "accept": "accettare", "transfer": "trasferire", "avoid": "evitare"}

func normTreatment(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", true
	}
	for en, it := range treatments {
		if s == en || s == it {
			return en, true
		}
	}
	return "", false
}

var acceptanceNames = map[string]string{"none": "nessuna", "pending": "in approvazione", "accepted": "accettato", "rejected": "non approvata", "expired": "scaduta"}

// Score è una valutazione su una matrice: probabilità, impatto, punteggio e fascia.
type Score struct {
	Likelihood     int    `json:"likelihood"`
	LikelihoodName string `json:"likelihood_name"`
	Impact         int    `json:"impact"`
	ImpactName     string `json:"impact_name"`
	Score          int    `json:"score"`
	Band           string `json:"band"`
	BandName       string `json:"band_name"`
	Tone           string `json:"tone"`
	Over           bool   `json:"over_appetite"`
}

func scoreOf(m *Matrix, l, i *int16) *Score {
	if l == nil || i == nil || *l < 1 || *i < 1 || int(*l) > len(m.Likelihood) || int(*i) > len(m.Impact) {
		return nil
	}
	sc := &Score{Likelihood: int(*l), Impact: int(*i), Score: int(*l) * int(*i)}
	sc.LikelihoodName, sc.ImpactName = m.Likelihood[*l-1].Name, m.Impact[*i-1].Name
	if b := m.Band(sc.Score); b != nil {
		sc.Band, sc.BandName, sc.Tone = b.Key, b.Name, b.Tone
	}
	sc.Over = m.Over(sc.Score)
	return sc
}

// riskRow è la riga di ext_risk con i dati dell'elemento.
type riskRow struct {
	ID, Key, Title, Body          string
	Phase, PhaseCat               string
	MatrixID                      string
	IL, II, RL, RI                *int16
	Treatment                     *string
	Category, Plan                string
	TreatmentDue, ReviewDue       *time.Time
	Acceptance                    string
	ApprovalID                    *string
	AcceptedAt                    *time.Time
	AcceptedUntil                 *time.Time
	AssessedAt                    *time.Time
	ReviewNotice, TreatmentNotice *time.Time
	CreatedAt, UpdatedAt          time.Time
}

const selectRisk = `
	select x.element_id::text, s.key || '-' || e.number, e.title, e.body_text, coalesce(o.name, ''), coalesce(o.category, ''),
	       x.matrix_id::text, x.inherent_likelihood, x.inherent_impact, x.residual_likelihood, x.residual_impact, x.treatment,
	       x.category, x.treatment_plan, x.treatment_due, x.review_due, x.acceptance_stage, x.approval_request_id::text,
	       x.accepted_at, x.accepted_until, x.assessed_at, x.review_notice_sent_at, x.treatment_notice_sent_at, x.created_at, e.updated_at
	from ext_risk x
	join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id`

func scanRisk(row pgx.Row) (*riskRow, error) {
	r := &riskRow{}
	err := row.Scan(&r.ID, &r.Key, &r.Title, &r.Body, &r.Phase, &r.PhaseCat, &r.MatrixID, &r.IL, &r.II, &r.RL, &r.RI, &r.Treatment,
		&r.Category, &r.Plan, &r.TreatmentDue, &r.ReviewDue, &r.Acceptance, &r.ApprovalID, &r.AcceptedAt, &r.AcceptedUntil, &r.AssessedAt,
		&r.ReviewNotice, &r.TreatmentNotice, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	return r, err
}

func loadRisk(ctx context.Context, tx pgx.Tx, id string, lock bool) (*riskRow, error) {
	q := selectRisk + " where x.element_id = $1"
	if lock {
		q += " for update of x"
	}
	return scanRisk(tx.QueryRow(ctx, q, id))
}

// RiskInput crea o modifica un rischio. Nella modifica solo i campi valorizzati cambiano.
type RiskInput struct {
	Title              *string  `json:"title"`
	Description        *string  `json:"description"`
	Category           *string  `json:"category"`
	Matrix             *string  `json:"matrix"`
	InherentLikelihood *int     `json:"inherent_likelihood"`
	InherentImpact     *int     `json:"inherent_impact"`
	ResidualLikelihood *int     `json:"residual_likelihood"`
	ResidualImpact     *int     `json:"residual_impact"`
	Treatment          *string  `json:"treatment"`
	TreatmentPlan      *string  `json:"treatment_plan"`
	TreatmentDue       *string  `json:"treatment_due"`
	ReviewDue          *string  `json:"review_due"`
	OwnerID            *string  `json:"owner_id"`
	Targets            []string `json:"targets"` // solo alla creazione: applicazioni, asset o fornitori (chiavi o id)
}

// canEditRisk: risk.admin, oppure il titolare del rischio sui dati del trattamento.
func canEditRisk(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (bool, error) {
	if actor.Has(PermAdmin) {
		return true, nil
	}
	return engine.HasRole(ctx, tx, id, actor.MemberID, []string{RoleRiskOwner})
}

func pair(field string, m *Matrix, l, i *int) (*int16, *int16, error) {
	if l == nil && i == nil {
		return nil, nil, nil
	}
	if l == nil || i == nil {
		return nil, nil, engine.Required(field, "servono sia la probabilità sia l'impatto")
	}
	if *l == 0 && *i == 0 {
		return nil, nil, nil // azzera la valutazione
	}
	if *l < 1 || *l > len(m.Likelihood) || *i < 1 || *i > len(m.Impact) {
		return nil, nil, engine.InvalidParams("validation.risk.matrix_levels", field, map[string]any{"likelihood": len(m.Likelihood), "impact": len(m.Impact)}, "probabilità da 1 a %d e impatto da 1 a %d", len(m.Likelihood), len(m.Impact))
	}
	a, b := int16(*l), int16(*i)
	return &a, &b, nil
}

// CreateRisk crea un rischio nel registro (risk.admin), con la valutazione inerente se c'è.
func (s *Service) CreateRisk(ctx context.Context, tx pgx.Tx, actor engine.Actor, in RiskInput) (*RiskView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if ok, err := typeExists(ctx, tx, RiskType); err != nil {
		return nil, err
	} else if !ok {
		return nil, engine.InvalidCode("validation.risk.not_configured", "type", "i rischi non sono configurati in questo tenant")
	}
	if in.Title == nil {
		return nil, engine.InvalidCode("validation.title_required", "title", "il titolo è obbligatorio")
	}
	m, err := matrixFor(ctx, tx, deref(in.Matrix))
	if err != nil {
		return nil, err
	}
	desc := deref(in.Description)
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: RiskSpace, TypeKey: RiskType, Title: *in.Title, BodyText: desc, DeferStart: true})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into ext_risk (tenant_id, element_id, matrix_id) values (app_tenant(), $1, $2)`, el.ID, m.ID); err != nil {
		return nil, err
	}
	in.Title, in.Description, in.Matrix = nil, nil, nil
	if err := s.writeRisk(ctx, tx, actor, el.ID, m, in, true); err != nil {
		return nil, err
	}
	if err := setOwner(ctx, tx, actor, el.ID, RoleRiskOwner, deref(in.OwnerID)); err != nil {
		return nil, err
	}
	for _, t := range in.Targets {
		if err := s.LinkRisk(ctx, tx, actor, el.ID, t); err != nil {
			return nil, err
		}
	}
	if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "risk.create", RiskType, el.ID, map[string]any{"matrix": m.Key}); err != nil {
		return nil, err
	}
	return s.RiskView(ctx, tx, actor, el.ID)
}

// writeRisk applica la valutazione e il trattamento (già autorizzati).
func (s *Service) writeRisk(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, m *Matrix, in RiskInput, created bool) error {
	cur, err := loadRisk(ctx, tx, id, true)
	if err != nil {
		return err
	}
	changes := map[string]any{}
	il, ii, rl, ri := cur.IL, cur.II, cur.RL, cur.RI
	if in.InherentLikelihood != nil || in.InherentImpact != nil {
		if il, ii, err = pair("inherent", m, in.InherentLikelihood, in.InherentImpact); err != nil {
			return err
		}
		changes["inherent"] = true
	}
	if in.ResidualLikelihood != nil || in.ResidualImpact != nil {
		if rl, ri, err = pair("residual", m, in.ResidualLikelihood, in.ResidualImpact); err != nil {
			return err
		}
		changes["residual"] = true
	}
	// Il residuo non supera l'inerente: la valutazione si fa dopo i controlli, non prima.
	if il != nil && rl != nil && int(*rl)*int(*ri) > int(*il)*int(*ii) {
		return engine.InvalidCode("validation.risk.residual_over_inherent", "residual", "il rischio residuo non può superare quello inerente")
	}
	treatment := cur.Treatment
	if in.Treatment != nil {
		t, ok := normTreatment(*in.Treatment)
		if !ok {
			return engine.InvalidValue("treatment", "trattamento non ammesso (mitigare, accettare, trasferire, evitare)")
		}
		if t == "" {
			treatment = nil
		} else {
			treatment = &t
		}
		changes["treatment"] = t
	}
	category, plan := cur.Category, cur.Plan
	if in.Category != nil {
		category = strings.TrimSpace(*in.Category)
		if len(category) > 120 {
			return engine.TooLong("category", 120, "categoria troppo lunga (massimo 120 caratteri)")
		}
		changes["category"] = true
	}
	if in.TreatmentPlan != nil {
		plan = strings.TrimSpace(*in.TreatmentPlan)
		if len(plan) > 10000 {
			return engine.InvalidCode("validation.risk.plan_too_long", "treatment_plan", "piano troppo lungo")
		}
		changes["treatment_plan"] = true
	}
	tdue, rdue := cur.TreatmentDue, cur.ReviewDue
	if in.TreatmentDue != nil {
		if tdue, err = parseDate("treatment_due", *in.TreatmentDue); err != nil {
			return err
		}
		changes["treatment_due"] = true
	}
	if in.ReviewDue != nil {
		if rdue, err = parseDate("review_due", *in.ReviewDue); err != nil {
			return err
		}
		changes["review_due"] = true
	}
	score := func(l, i *int16) any {
		if l == nil || i == nil {
			return nil
		}
		return int(*l) * int(*i)
	}
	if _, err := tx.Exec(ctx, `update ext_risk set inherent_likelihood = $2, inherent_impact = $3, residual_likelihood = $4, residual_impact = $5,
		inherent_score = $6, residual_score = $7, treatment = $8, category = $9, treatment_plan = $10, treatment_due = $11, review_due = $12,
		assessed_at = case when $13 then now() else assessed_at end,
		treatment_notice_sent_at = case when $14 then null else treatment_notice_sent_at end,
		review_notice_sent_at = case when $15 then null else review_notice_sent_at end
		where element_id = $1`, id, il, ii, rl, ri, score(il, ii), score(rl, ri), treatment, category, plan, tdue, rdue,
		changes["inherent"] != nil || changes["residual"] != nil, changes["treatment_due"] != nil, changes["review_due"] != nil); err != nil {
		return err
	}
	if len(changes) == 0 || created {
		return nil
	}
	payload := map[string]any{}
	if sc := scoreOf(m, il, ii); sc != nil && changes["inherent"] != nil {
		payload["inherent"] = fmt.Sprintf("%d × %d = %d (%s)", sc.Likelihood, sc.Impact, sc.Score, sc.BandName)
	}
	if sc := scoreOf(m, rl, ri); sc != nil && changes["residual"] != nil {
		payload["residual"] = fmt.Sprintf("%d × %d = %d (%s)", sc.Likelihood, sc.Impact, sc.Score, sc.BandName)
	}
	if t, ok := changes["treatment"].(string); ok {
		payload["treatment"] = treatments[t]
	}
	if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "risk", "assessment", payload, nil); err != nil {
		return err
	}
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	return audited(ctx, tx, actor, "risk.update", RiskType, id, map[string]any{"changed": keys})
}

// UpdateRisk modifica valutazione, trattamento, titolare e testi del rischio. Il titolare può
// aggiornare trattamento, piano, scadenze e residuo; il resto vuole risk.admin.
func (s *Service) UpdateRisk(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in RiskInput) (*RiskView, error) {
	el, err := s.resolveTyped(ctx, tx, actor, ref, RiskType)
	if err != nil {
		return nil, err
	}
	ok, err := canEditRisk(ctx, tx, actor, el.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, engine.ErrForbidden
	}
	admin := actor.Has(PermAdmin)
	if !admin && (in.Title != nil || in.Description != nil || in.Category != nil || in.Matrix != nil || in.OwnerID != nil ||
		in.InherentImpact != nil || in.InherentLikelihood != nil) {
		return nil, engine.ErrForbidden
	}
	cur, err := loadRisk(ctx, tx, el.ID, true)
	if err != nil {
		return nil, err
	}
	if cur.Acceptance == "pending" && (in.ResidualImpact != nil || in.ResidualLikelihood != nil || in.Treatment != nil) {
		return nil, engine.InvalidCode("validation.risk.acceptance_pending", "acceptance", "l'accettazione è in approvazione: il residuo e il trattamento non si cambiano ora")
	}
	m, err := matrixByID(ctx, tx, cur.MatrixID)
	if err != nil {
		return nil, err
	}
	if in.Matrix != nil {
		nm, err := matrixFor(ctx, tx, *in.Matrix)
		if err != nil {
			return nil, err
		}
		if nm.ID != m.ID {
			// Cambiando matrice la valutazione va rifatta sui nuovi livelli.
			if _, err := tx.Exec(ctx, `update ext_risk set matrix_id = $2, inherent_likelihood = null, inherent_impact = null, residual_likelihood = null,
				residual_impact = null, inherent_score = null, residual_score = null where element_id = $1`, el.ID, nm.ID); err != nil {
				return nil, err
			}
			m = nm
		}
	}
	if in.Title != nil || in.Description != nil {
		if _, err := s.eng.Update(ctx, tx, actor, el.ID, engine.UpdateInput{Version: el.Version, Title: in.Title, BodyText: in.Description}); err != nil {
			return nil, err
		}
	}
	if err := s.writeRisk(ctx, tx, actor, el.ID, m, in, false); err != nil {
		return nil, err
	}
	if in.OwnerID != nil {
		if err := setOwner(ctx, tx, actor, el.ID, RoleRiskOwner, *in.OwnerID); err != nil {
			return nil, err
		}
	}
	return s.RiskView(ctx, tx, actor, el.ID)
}

// LinkRisk collega il rischio a un'applicazione, un asset o un fornitore (risk_of), oppure a
// un controllo (mitigates, dal controllo al rischio).
func (s *Service) LinkRisk(ctx context.Context, tx pgx.Tx, actor engine.Actor, riskID, target string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	t, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(target))
	if errors.Is(err, engine.ErrNotFound) {
		return engine.NotFound("target", "element", "elemento %q inesistente", target)
	}
	if err != nil {
		return err
	}
	switch t.TypeKey {
	case AppType, AssetType, ProviderType:
		_, err = relate(ctx, tx, actor, RelRiskOf, riskID, t.ID)
	case ControlType:
		_, err = relate(ctx, tx, actor, RelMitigates, t.ID, riskID)
	default:
		return engine.InvalidParams("validation.risk.link_target_kind", "target", map[string]any{"kind": t.TypeName}, "un rischio si collega ad applicazioni, asset, fornitori ICT o controlli (non a %s)", t.TypeName)
	}
	return err
}

// UnlinkRisk toglie il collegamento.
func (s *Service) UnlinkRisk(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, target string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	el, err := s.resolveTyped(ctx, tx, actor, ref, RiskType)
	if err != nil {
		return err
	}
	t, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(target))
	if err != nil {
		return err
	}
	if t.TypeKey == ControlType {
		return unrelate(ctx, tx, actor, RelMitigates, t.ID, el.ID)
	}
	return unrelate(ctx, tx, actor, RelRiskOf, el.ID, t.ID)
}

// LinkRiskRef è LinkRisk con il riferimento del rischio.
func (s *Service) LinkRiskRef(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, target string) (*RiskView, error) {
	el, err := s.resolveTyped(ctx, tx, actor, ref, RiskType)
	if err != nil {
		return nil, err
	}
	if err := s.LinkRisk(ctx, tx, actor, el.ID, target); err != nil {
		return nil, err
	}
	return s.RiskView(ctx, tx, actor, el.ID)
}

// RiskView è la vista del rischio.
type RiskView struct {
	Element       *engine.Element `json:"element"`
	Key           string          `json:"key"`
	Matrix        *Matrix         `json:"matrix"`
	Category      string          `json:"category"`
	Inherent      *Score          `json:"inherent"`
	Residual      *Score          `json:"residual"`
	Treatment     *string         `json:"treatment"`
	TreatmentName string          `json:"treatment_name"`
	TreatmentPlan string          `json:"treatment_plan"`
	TreatmentDue  *string         `json:"treatment_due"`
	ReviewDue     *string         `json:"review_due"`
	Acceptance    string          `json:"acceptance"`
	AcceptanceNm  string          `json:"acceptance_name"`
	AcceptedAt    *time.Time      `json:"accepted_at"`
	AcceptedUntil *string         `json:"accepted_until"`
	OwnerID       *string         `json:"owner_id"`
	Owner         *string         `json:"owner"`
	Targets       []Ref           `json:"targets"`
	Controls      []Ref           `json:"controls"`
	Incidents     []Ref           `json:"incidents"`
	Can           map[string]bool `json:"can"`
}

func (s *Service) viewOf(ctx context.Context, tx pgx.Tx, r *riskRow, m *Matrix) (*RiskView, error) {
	v := &RiskView{Key: r.Key, Matrix: m, Category: r.Category, Inherent: scoreOf(m, r.IL, r.II), Residual: scoreOf(m, r.RL, r.RI),
		Treatment: r.Treatment, TreatmentPlan: r.Plan, TreatmentDue: dateStr(r.TreatmentDue), ReviewDue: dateStr(r.ReviewDue),
		Acceptance: r.Acceptance, AcceptanceNm: acceptanceNames[r.Acceptance], AcceptedAt: r.AcceptedAt, AcceptedUntil: dateStr(r.AcceptedUntil)}
	if r.Treatment != nil {
		v.TreatmentName = treatments[*r.Treatment]
	}
	var err error
	if v.OwnerID, v.Owner, err = holderName(ctx, tx, r.ID, RoleRiskOwner); err != nil {
		return nil, err
	}
	return v, nil
}

// RiskView restituisce il rischio con valutazioni, collegamenti e permessi.
func (s *Service) RiskView(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*RiskView, error) {
	el, err := s.resolveTyped(ctx, tx, actor, ref, RiskType)
	if err != nil {
		return nil, err
	}
	r, err := loadRisk(ctx, tx, el.ID, false)
	if err != nil {
		return nil, err
	}
	m, err := matrixByID(ctx, tx, r.MatrixID)
	if err != nil {
		return nil, err
	}
	v, err := s.viewOf(ctx, tx, r, m)
	if err != nil {
		return nil, err
	}
	v.Element = el
	if v.Targets, err = targetsOf(ctx, tx, RelRiskOf, el.ID, true); err != nil {
		return nil, err
	}
	if v.Controls, err = targetsOf(ctx, tx, RelMitigates, el.ID, false); err != nil {
		return nil, err
	}
	if v.Incidents, err = targetsOf(ctx, tx, RelMaterializes, el.ID, false); err != nil {
		return nil, err
	}
	owner, err := canEditRisk(ctx, tx, actor, el.ID)
	if err != nil {
		return nil, err
	}
	v.Can = map[string]bool{"edit": actor.Has(PermAdmin), "treat": owner && r.Acceptance != "pending",
		"request_acceptance": owner && r.Acceptance != "pending" && r.Treatment != nil && *r.Treatment == "accept" && v.Residual != nil}
	return v, nil
}

func (s *Service) riskFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	r, err := loadRisk(ctx, tx, el.ID, false)
	if errors.Is(err, engine.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	m, err := matrixByID(ctx, tx, r.MatrixID)
	if err != nil {
		return err
	}
	if sc := scoreOf(m, r.IL, r.II); sc != nil {
		f.Set("punteggio_inerente", sc.Score)
		f.Set("livello_inerente", sc.Band, sc.BandName)
	}
	if sc := scoreOf(m, r.RL, r.RI); sc != nil {
		f.Set("punteggio_residuo", sc.Score)
		f.Set("livello_residuo", sc.Band, sc.BandName)
		f.Set("oltre_soglia", sc.Over)
	} else {
		f.Set("oltre_soglia", false)
	}
	if r.Treatment != nil {
		f.Set("trattamento", *r.Treatment, treatments[*r.Treatment])
	}
	if r.Plan != "" {
		f.Set("piano_trattamento", true)
	}
	f.Set("scadenza_trattamento", r.TreatmentDue)
	f.Set("revisione", r.ReviewDue)
	if r.Category != "" {
		f.Set("categoria", r.Category)
	}
	f.Set("accettazione", r.Acceptance, acceptanceNames[r.Acceptance])
	var controls, incidents int
	var apps, providers []any
	var critical bool
	if err := tx.QueryRow(ctx, `select
		(select count(*) from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $2 where r.target_id = $1),
		(select count(*) from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $3 where r.target_id = $1),
		coalesce((select bool_or(a.supports_critical_function) from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $4
			join ext_application a on a.tenant_id = r.tenant_id and a.element_id = r.target_id where r.source_id = $1), false)`,
		el.ID, RelMitigates, RelMaterializes, RelRiskOf).Scan(&controls, &incidents, &critical); err != nil {
		return err
	}
	targets, err := targetsOf(ctx, tx, RelRiskOf, el.ID, true)
	if err != nil {
		return err
	}
	for _, t := range targets {
		switch t.Type {
		case AppType:
			apps = append(apps, t.Title, t.Key)
		case ProviderType:
			providers = append(providers, t.Title, t.Key)
		}
	}
	f.Set("controlli", controls)
	f.Set("incidenti", incidents)
	f.Set("funzione_critica", critical)
	f.Set("applicazioni", apps...)
	f.Set("fornitori", providers...)
	return nil
}

// ---------------------------------------------------------------------------
// Accettazione con approvazione

// RequestAcceptance chiede l'accettazione del residuo fino a una data: imposta il trattamento
// «accettare» e porta il rischio nella fase dell'azione risk_acceptance submitted.
func (s *Service) RequestAcceptance(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, until, comment string) (*RiskView, error) {
	el, err := s.resolveTyped(ctx, tx, actor, ref, RiskType)
	if err != nil {
		return nil, err
	}
	ok, err := canEditRisk(ctx, tx, actor, el.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, engine.ErrForbidden
	}
	u, err := parseDate("until", until)
	if err != nil {
		return nil, err
	}
	if u == nil || !u.After(s.today()) {
		return nil, engine.Required("until", "indica fino a quando vale l'accettazione (una data futura)")
	}
	if u.After(s.today().AddDate(3, 0, 0)) {
		return nil, engine.InvalidCode("validation.risk.acceptance_max_3y", "until", "un'accettazione dura al massimo tre anni")
	}
	if _, err := tx.Exec(ctx, "update ext_risk set accepted_until = $2, treatment = 'accept' where element_id = $1", el.ID, u); err != nil {
		return nil, err
	}
	phase, err := s.phaseFor(ctx, tx, el, ActionAcceptance, "submitted")
	if err != nil {
		return nil, err
	}
	if phase == "" {
		return nil, engine.InvalidCode("validation.risk.process_no_acceptance_stage", "process", "il processo dei rischi non ha una fase con l'azione risk_acceptance submitted")
	}
	if _, err := s.eng.Transition(ctx, tx, actor, el.ID, phase, comment, engine.TransitionOpts{}); err != nil {
		return nil, err
	}
	return s.RiskView(ctx, tx, actor, el.ID)
}

func (s *Service) actionAcceptance(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, a engine.Action) (string, error) {
	if el.TypeKey != RiskType {
		return "risk_acceptance vale solo sui rischi", nil
	}
	r, err := loadRisk(ctx, tx, el.ID, true)
	if errors.Is(err, engine.ErrNotFound) {
		return "", engine.ErrNoEffect
	}
	if err != nil {
		return "", err
	}
	m, err := matrixByID(ctx, tx, r.MatrixID)
	if err != nil {
		return "", err
	}
	switch a.Ref {
	case "submitted":
		res := scoreOf(m, r.RL, r.RI)
		if res == nil {
			return "", engine.InvalidCode("validation.risk.residual_first", "residual", "valuta il rischio residuo prima di chiederne l'accettazione")
		}
		if r.AcceptedUntil == nil || !r.AcceptedUntil.After(s.today()) {
			// Passaggio fatto dal processo senza la data: un anno, poi si rivaluta.
			d := s.today().AddDate(1, 0, 0)
			r.AcceptedUntil = &d
		}
		if s.ap == nil {
			return "", engine.InvalidCode("validation.approval.engine_unavailable", "approval", "motore delle approvazioni non disponibile")
		}
		score := res.Score
		reqID, err := s.ap.OpenSubjectWith(ctx, tx, actor, el, SubjectAcceptance, PolicyAcceptance, "Accettazione del rischio", approval.Facts{Risk: &score})
		if err != nil {
			return "", err
		}
		if reqID == "" {
			return "", engine.InvalidCode("validation.risk.no_acceptance_policy", "approval", "nessuna politica di accettazione attiva: configurala in Rischi e incidenti → Configurazione")
		}
		if _, err := tx.Exec(ctx, `update ext_risk set acceptance_stage = 'pending', approval_request_id = $2, accepted_until = $3, treatment = 'accept'
			where element_id = $1`, el.ID, reqID, r.AcceptedUntil); err != nil {
			return "", err
		}
		if err := audited(ctx, tx, actor, "risk.acceptance_requested", RiskType, el.ID, map[string]any{"score": score, "until": r.AcceptedUntil.Format(time.DateOnly)}); err != nil {
			return "", err
		}
		return fmt.Sprintf("residuo %d (%s), fino al %s", score, res.BandName, r.AcceptedUntil.Format(time.DateOnly)), nil
	case "accepted":
		if _, err := tx.Exec(ctx, `update ext_risk set acceptance_stage = 'accepted', accepted_at = now(), review_due = coalesce(review_due, accepted_until)
			where element_id = $1`, el.ID); err != nil {
			return "", err
		}
		until := ""
		if r.AcceptedUntil != nil {
			until = r.AcceptedUntil.Format(time.DateOnly)
		}
		if err := s.notifyRisk(ctx, tx, actor, el.ID, map[string]any{"event": "risk_accepted", "until": until}); err != nil {
			return "", err
		}
		if err := audited(ctx, tx, actor, "risk.accepted", RiskType, el.ID, map[string]any{"until": until}); err != nil {
			return "", err
		}
		return "accettato fino al " + until, nil
	case "reopened":
		next := ""
		switch r.Acceptance {
		case "pending":
			next = "rejected"
		case "accepted":
			next = "none"
			if r.AcceptedUntil != nil && !r.AcceptedUntil.After(s.today()) {
				next = "expired"
			}
		default:
			return "", engine.ErrNoEffect
		}
		if _, err := tx.Exec(ctx, "update ext_risk set acceptance_stage = $2, approval_request_id = null where element_id = $1", el.ID, next); err != nil {
			return "", err
		}
		if next == "rejected" {
			if err := s.notifyRisk(ctx, tx, actor, el.ID, map[string]any{"event": "risk_rejected"}); err != nil {
				return "", err
			}
		}
		if err := audited(ctx, tx, actor, "risk.acceptance_"+next, RiskType, el.ID, nil); err != nil {
			return "", err
		}
		return "accettazione: " + acceptanceNames[next], nil
	}
	return "stadio " + a.Ref + " non ammesso", nil
}

// closeAcceptance applica l'esito dell'approvazione con un passaggio di sistema: accettato,
// oppure (respinta o rinviata) di nuovo da valutare.
func (s *Service) closeAcceptance(ctx context.Context, tx pgx.Tx, actor engine.Actor, requestID, outcome, comment string) error {
	var id string
	if err := tx.QueryRow(ctx, "select element_id::text from approval_requests where id = $1", requestID).Scan(&id); err != nil {
		return err
	}
	stage := map[string]string{"approved": "accepted", "approved_with_conditions": "accepted", "rejected": "reopened", "deferred": "reopened"}[outcome]
	if stage == "" {
		return nil
	}
	el, err := s.eng.Get(ctx, tx, reader(actor.MemberID), id)
	if err != nil {
		return err
	}
	phase, err := s.phaseFor(ctx, tx, el, ActionAcceptance, stage)
	if err != nil {
		return err
	}
	if phase == "" {
		return engine.InvalidParams("validation.risk.process_no_stage", "process", map[string]any{"stage": stage}, "il processo dei rischi non ha una fase per lo stadio %s", stage)
	}
	sys := engine.Actor{AccountID: actor.AccountID, MemberID: actor.MemberID, IP: actor.IP, Permissions: []string{rbac.ElementRead}}
	_, err = s.eng.Transition(ctx, tx, sys, id, phase, comment, engine.TransitionOpts{System: true})
	return err
}

// notifyRisk avvisa il titolare e il comitato rischi.
func (s *Service) notifyRisk(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID string, payload map[string]any) error {
	members, groups, err := roleHolders(ctx, tx, elementID, RoleRiskOwner, RoleRiskCommittee)
	if err != nil {
		return err
	}
	_, err = notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindRisk, ElementID: elementID, ActorID: actor.MemberID,
		Members: members, Groups: groups, Payload: payload})
	return err
}

func roleHolders(ctx context.Context, tx pgx.Tx, elementID string, roles ...string) (members, groups []string, err error) {
	rows, err := tx.Query(ctx, `select coalesce(sh.member_id::text, ''), coalesce(sh.group_id::text, '') from stakeholders sh
		join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id where sh.element_id = $1 and r.key = any($2)`, elementID, roles)
	if err != nil {
		return nil, nil, err
	}
	var m, g string
	_, err = pgx.ForEachRow(rows, []any{&m, &g}, func() error {
		if m != "" {
			members = append(members, m)
		}
		if g != "" {
			groups = append(groups, g)
		}
		return nil
	})
	return members, groups, err
}

// ---------------------------------------------------------------------------
// Registro dei rischi

// RiskFilter filtra il registro.
type RiskFilter struct {
	Q         string
	Phase     string // nome della fase oppure "aperti"
	Band      string // chiave della fascia del residuo (o dell'inerente se il residuo manca)
	Treatment string
	Category  string
	Over      bool
	Owner     string
	Target    string // chiave o id di un'applicazione, asset o fornitore collegato
}

// RiskItem è una riga del registro.
type RiskItem struct {
	ID            string   `json:"id"`
	Key           string   `json:"key"`
	Title         string   `json:"title"`
	Phase         string   `json:"phase"`
	PhaseCategory string   `json:"phase_category"`
	Category      string   `json:"category"`
	Inherent      *Score   `json:"inherent"`
	Residual      *Score   `json:"residual"`
	Treatment     string   `json:"treatment"`
	TreatmentDue  *string  `json:"treatment_due"`
	ReviewDue     *string  `json:"review_due"`
	Acceptance    string   `json:"acceptance"`
	Owner         *string  `json:"owner"`
	Targets       []string `json:"targets"`
	Controls      int      `json:"controls"`
	Overdue       bool     `json:"overdue"`
}

// RiskRegister è il registro dei rischi filtrato, con le fasi per il filtro.
type RiskRegister struct {
	Items      []RiskItem `json:"items"`
	Phases     []string   `json:"phases"`
	Categories []string   `json:"categories"`
	Matrix     *Matrix    `json:"matrix"`
	CanCreate  bool       `json:"can_create"`
}

// Risks restituisce il registro dei rischi (element.read).
func (s *Service) Risks(ctx context.Context, tx pgx.Tx, actor engine.Actor, f RiskFilter) (*RiskRegister, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectRisk+` order by coalesce(x.residual_score, x.inherent_score, 0) desc, e.number desc limit 2000`)
	if err != nil {
		return nil, err
	}
	var list []*riskRow
	for rows.Next() {
		r, err := scanRisk(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	matrices := map[string]*Matrix{}
	ms, err := s.Matrices(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		matrices[m.ID] = m
	}
	// Collegamenti e titolari in una query sola.
	type extra struct {
		targets  []string
		targetID []string
		controls int
		owner    *string
		ownerID  *string
	}
	ex := map[string]*extra{}
	for _, r := range list {
		ex[r.ID] = &extra{targets: []string{}}
	}
	trows, err := tx.Query(ctx, `select r.source_id::text, e.title, e.id::text, coalesce(s.key || '-' || e.number, '') from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $1
		join elements e on e.tenant_id = r.tenant_id and e.id = r.target_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join ext_risk x on x.tenant_id = r.tenant_id and x.element_id = r.source_id order by e.title`, RelRiskOf)
	if err != nil {
		return nil, err
	}
	var src, title, tid, tkey string
	if _, err := pgx.ForEachRow(trows, []any{&src, &title, &tid, &tkey}, func() error {
		if e := ex[src]; e != nil {
			e.targets = append(e.targets, title)
			e.targetID = append(e.targetID, tid, tkey)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	crows, err := tx.Query(ctx, `select r.target_id::text, count(*) from relations r
		join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $1 group by 1`, RelMitigates)
	if err != nil {
		return nil, err
	}
	var n int
	if _, err := pgx.ForEachRow(crows, []any{&src, &n}, func() error {
		if e := ex[src]; e != nil {
			e.controls = n
		}
		return nil
	}); err != nil {
		return nil, err
	}
	orows, err := tx.Query(ctx, `select sh.element_id::text, sh.member_id::text, coalesce(m.display_name, g.name) from stakeholders sh
		join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = $1
		left join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id
		left join groups g on g.tenant_id = sh.tenant_id and g.id = sh.group_id`, RoleRiskOwner)
	if err != nil {
		return nil, err
	}
	var oid, oname *string
	if _, err := pgx.ForEachRow(orows, []any{&src, &oid, &oname}, func() error {
		if e := ex[src]; e != nil && e.owner == nil {
			e.owner, e.ownerID = oname, oid
		}
		return nil
	}); err != nil {
		return nil, err
	}
	reg := &RiskRegister{Items: []RiskItem{}, Phases: []string{}, Categories: []string{}, CanCreate: actor.Has(PermAdmin)}
	if len(ms) > 0 {
		reg.Matrix = ms[0]
	}
	seenPhase, seenCat := map[string]bool{}, map[string]bool{}
	q := strings.ToLower(strings.TrimSpace(f.Q))
	today := s.today()
	for _, r := range list {
		m := matrices[r.MatrixID]
		if m == nil {
			continue
		}
		if !seenPhase[r.Phase] && r.Phase != "" {
			seenPhase[r.Phase] = true
			reg.Phases = append(reg.Phases, r.Phase)
		}
		if !seenCat[r.Category] && r.Category != "" {
			seenCat[r.Category] = true
			reg.Categories = append(reg.Categories, r.Category)
		}
		e := ex[r.ID]
		it := RiskItem{ID: r.ID, Key: r.Key, Title: r.Title, Phase: r.Phase, PhaseCategory: r.PhaseCat, Category: r.Category,
			Inherent: scoreOf(m, r.IL, r.II), Residual: scoreOf(m, r.RL, r.RI), TreatmentDue: dateStr(r.TreatmentDue), ReviewDue: dateStr(r.ReviewDue),
			Acceptance: acceptanceNames[r.Acceptance], Owner: e.owner, Targets: e.targets, Controls: e.controls}
		if r.Treatment != nil {
			it.Treatment = treatments[*r.Treatment]
		}
		open := r.PhaseCat != "done" && r.PhaseCat != "cancelled"
		it.Overdue = open && ((r.TreatmentDue != nil && r.TreatmentDue.Before(today) && r.PhaseCat == "in_progress" && strings.EqualFold(r.Phase, "In trattamento")) ||
			(r.ReviewDue != nil && r.ReviewDue.Before(today)))
		// Filtri.
		current := it.Residual
		if current == nil {
			current = it.Inherent
		}
		switch {
		case f.Phase == "aperti" && !open:
			continue
		case f.Phase != "" && f.Phase != "aperti" && !strings.EqualFold(f.Phase, r.Phase):
			continue
		case f.Band != "" && (current == nil || current.Band != f.Band):
			continue
		case f.Treatment != "" && (r.Treatment == nil || (*r.Treatment != f.Treatment && treatments[*r.Treatment] != f.Treatment)):
			continue
		case f.Category != "" && !strings.EqualFold(f.Category, r.Category):
			continue
		case f.Over && (it.Residual == nil || !it.Residual.Over || !open):
			continue
		case f.Owner != "" && (e.ownerID == nil || *e.ownerID != f.Owner):
			continue
		case f.Target != "" && !containsFold(e.targetID, f.Target):
			continue
		case q != "" && !strings.Contains(strings.ToLower(r.Title+" "+r.Key+" "+r.Category+" "+strings.Join(e.targets, " ")), q):
			continue
		}
		reg.Items = append(reg.Items, it)
	}
	return reg, nil
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, strings.TrimSpace(v)) {
			return true
		}
	}
	return false
}

// WriteRisksCSV scrive il registro in CSV (separatore punto e virgola, come gli altri export).
func (reg *RiskRegister) WriteCSV(w io.Writer) error {
	if _, err := io.WriteString(w, "\ufeff"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	_ = cw.Write([]string{"Chiave", "Titolo", "Fase", "Categoria", "Probabilità inerente", "Impatto inerente", "Punteggio inerente", "Livello inerente",
		"Probabilità residua", "Impatto residuo", "Punteggio residuo", "Livello residuo", "Oltre la soglia", "Trattamento", "Scadenza trattamento",
		"Revisione", "Accettazione", "Titolare", "Collegato a", "Controlli"})
	num := func(sc *Score, f func(*Score) string) string {
		if sc == nil {
			return ""
		}
		return f(sc)
	}
	for _, it := range reg.Items {
		over := "no"
		if it.Residual != nil && it.Residual.Over {
			over = "sì"
		}
		_ = cw.Write([]string{it.Key, it.Title, it.Phase, it.Category,
			num(it.Inherent, func(s *Score) string { return strconv.Itoa(s.Likelihood) }), num(it.Inherent, func(s *Score) string { return strconv.Itoa(s.Impact) }),
			num(it.Inherent, func(s *Score) string { return strconv.Itoa(s.Score) }), num(it.Inherent, func(s *Score) string { return s.BandName }),
			num(it.Residual, func(s *Score) string { return strconv.Itoa(s.Likelihood) }), num(it.Residual, func(s *Score) string { return strconv.Itoa(s.Impact) }),
			num(it.Residual, func(s *Score) string { return strconv.Itoa(s.Score) }), num(it.Residual, func(s *Score) string { return s.BandName }),
			over, it.Treatment, deref(it.TreatmentDue), deref(it.ReviewDue), it.Acceptance, deref(it.Owner), strings.Join(it.Targets, ", "), strconv.Itoa(it.Controls)})
	}
	cw.Flush()
	return cw.Error()
}

// ---------------------------------------------------------------------------
// Heatmap

// Cell è una cella della heatmap con i rischi che vi cadono.
type Cell struct {
	Likelihood int      `json:"likelihood"`
	Impact     int      `json:"impact"`
	Score      int      `json:"score"`
	Band       string   `json:"band"`
	Tone       string   `json:"tone"`
	Count      int      `json:"count"`
	Keys       []string `json:"keys"`
}

// Heatmap è la matrice con i conteggi del rischio inerente e residuo dei rischi aperti.
type Heatmap struct {
	Matrix   *Matrix `json:"matrix"`
	Inherent []Cell  `json:"inherent"`
	Residual []Cell  `json:"residual"`
}

// HeatmapOf calcola la heatmap della matrice predefinita sui rischi aperti.
func (s *Service) HeatmapOf(ctx context.Context, tx pgx.Tx, m *Matrix) (*Heatmap, error) {
	h := &Heatmap{Matrix: m}
	cells := func() []Cell {
		out := make([]Cell, 0, m.MaxScore())
		for l := 1; l <= len(m.Likelihood); l++ {
			for i := 1; i <= len(m.Impact); i++ {
				c := Cell{Likelihood: l, Impact: i, Score: l * i, Keys: []string{}}
				if b := m.Band(c.Score); b != nil {
					c.Band, c.Tone = b.Key, b.Tone
				}
				out = append(out, c)
			}
		}
		return out
	}
	h.Inherent, h.Residual = cells(), cells()
	rows, err := tx.Query(ctx, `select s.key || '-' || e.number, x.inherent_likelihood, x.inherent_impact, x.residual_likelihood, x.residual_impact
		from ext_risk x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where x.matrix_id = $1 and coalesce(o.category, '') not in ('done', 'cancelled') order by e.number`, m.ID)
	if err != nil {
		return nil, err
	}
	var key string
	var il, ii, rl, ri *int16
	add := func(list []Cell, l, i *int16) {
		if l == nil || i == nil {
			return
		}
		idx := (int(*l)-1)*len(m.Impact) + int(*i) - 1
		if idx >= 0 && idx < len(list) {
			list[idx].Count++
			if len(list[idx].Keys) < 20 {
				list[idx].Keys = append(list[idx].Keys, key)
			}
		}
	}
	_, err = pgx.ForEachRow(rows, []any{&key, &il, &ii, &rl, &ri}, func() error {
		add(h.Inherent, il, ii)
		add(h.Residual, rl, ri)
		return nil
	})
	return h, err
}

// phaseFor trova la fase del processo dell'elemento in cui una regola esegue l'azione
// indicata con quel riferimento (le fasi restano configurabili).
func (s *Service) phaseFor(ctx context.Context, tx pgx.Tx, el *engine.Element, kind, ref string) (string, error) {
	if el.ProcessVersionID == nil {
		return "", nil
	}
	var raw []byte
	if err := tx.QueryRow(ctx, "select definition from process_versions where id = $1", *el.ProcessVersionID).Scan(&raw); err != nil {
		return "", err
	}
	w, err := engine.DecodeDefinition(raw)
	if err != nil {
		return "", err
	}
	for _, r := range w.Rules {
		ph, ok := strings.CutPrefix(r.On, "enter:")
		if !ok || r.When != "" {
			continue
		}
		for _, a := range r.Actions {
			if a.Kind == kind && a.Ref == ref {
				return ph, nil
			}
		}
	}
	return "", nil
}
