package privacy

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

// ElRef è un elemento collegato alla DPIA: un rischio del registro rischi o un controllo GRC.
type ElRef struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
	State string `json:"state"`
	// Detail: per un controllo l'efficacia (not_assessed, effective, partial, ineffective); per un rischio il livello residuo se noto.
	Detail string `json:"detail"`
}

// ReviewRef è una valutazione «DPIA preliminare» del questionario delle valutazioni, riusata nella DPIA.
type ReviewRef struct {
	ID       string  `json:"id"`
	Key      string  `json:"key"`
	Request  string  `json:"request"`
	Type     string  `json:"type"`
	Stage    string  `json:"stage"`
	Outcome  *string `json:"outcome"`
	Polarity *string `json:"polarity"`
}

// DPIA è la valutazione d'impatto di un trattamento (art. 35 GDPR, da verificare).
type DPIA struct {
	ID             string     `json:"id"`
	TreatmentID    string     `json:"treatment_id"`
	Treatment      string     `json:"treatment"`
	Status         string     `json:"status"` // draft | in_review | approved
	Description    string     `json:"description"`
	Necessity      string     `json:"necessity"`
	RiskAssessment string     `json:"risk_assessment"`
	DPOOpinion     string     `json:"dpo_opinion"`
	ReviewID       *string    `json:"review_id"`
	Review         *ReviewRef `json:"review"`
	SubmittedAt    *time.Time `json:"submitted_at"`
	ApprovedAt     *time.Time `json:"approved_at"`
	ApprovedBy     *string    `json:"approved_by"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Risks          []ElRef    `json:"risks"`
	Controls       []ElRef    `json:"controls"`
	// Reviews: le valutazioni DPIA del questionario tra cui scegliere (se ce ne sono).
	Reviews []ReviewRef `json:"reviews"`
}

// DPIAInput è il contenuto della DPIA (si sostituisce per intero finché non è approvata).
type DPIAInput struct {
	Description    string   `json:"description"`
	Necessity      string   `json:"necessity"`
	RiskAssessment string   `json:"risk_assessment"`
	DPOOpinion     string   `json:"dpo_opinion"`
	ReviewID       string   `json:"review_id"`
	RiskIDs        []string `json:"risk_ids"`
	ControlIDs     []string `json:"control_ids"`
}

// DPIAItem è una riga dell'elenco delle DPIA.
type DPIAItem struct {
	ID          string    `json:"id"`
	TreatmentID string    `json:"treatment_id"`
	Treatment   string    `json:"treatment"`
	Status      string    `json:"status"`
	Risks       int       `json:"risks"`
	Controls    int       `json:"controls"`
	HasReview   bool      `json:"has_review"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// DPIAs elenca le DPIA (privacy.read), le più recenti per prime.
func (s *Service) DPIAs(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]DPIAItem, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select d.id::text, d.treatment_id::text, t.name, d.status,
		(select count(*) from privacy_dpia_risks r where r.dpia_id = d.id)::int,
		(select count(*) from privacy_dpia_controls c where c.dpia_id = d.id)::int,
		d.review_id is not null, d.updated_at
		from privacy_dpias d join privacy_treatments t on t.tenant_id = d.tenant_id and t.id = d.treatment_id
		order by d.updated_at desc, d.id`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[DPIAItem])
	if out == nil {
		out = []DPIAItem{}
	}
	return out, err
}

const reviewCols = `e.id::text, coalesce(s.key || '-' || e.number, ''), r.title, rt.name, x.stage, x.outcome_label, x.outcome_polarity`
const reviewFrom = `
	from ext_review x
	join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	join elements r on r.tenant_id = x.tenant_id and r.id = x.request_id
	join review_types rt on rt.tenant_id = x.tenant_id and rt.id = x.review_type_id`

func scanReviews(rows pgx.Rows) ([]ReviewRef, error) {
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ReviewRef])
	if out == nil {
		out = []ReviewRef{}
	}
	return out, err
}

// DPIA legge la DPIA di un trattamento (privacy.read); non esiste ancora: ErrNotFound.
func (s *Service) DPIA(ctx context.Context, tx pgx.Tx, actor engine.Actor, treatmentID string) (*DPIA, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	return loadDPIA(ctx, tx, treatmentID)
}

func loadDPIA(ctx context.Context, tx pgx.Tx, treatmentID string) (*DPIA, error) {
	if !isUUID(treatmentID) {
		return nil, engine.ErrNotFound
	}
	d := &DPIA{}
	err := tx.QueryRow(ctx, `select d.id::text, d.treatment_id::text, t.name, d.status, d.description, d.necessity, d.risk_assessment, d.dpo_opinion,
		d.review_id::text, d.submitted_at, d.approved_at, m.display_name, d.updated_at
		from privacy_dpias d join privacy_treatments t on t.tenant_id = d.tenant_id and t.id = d.treatment_id
		left join members m on m.tenant_id = d.tenant_id and m.id = d.approved_by
		where d.treatment_id = $1`, treatmentID).
		Scan(&d.ID, &d.TreatmentID, &d.Treatment, &d.Status, &d.Description, &d.Necessity, &d.RiskAssessment, &d.DPOOpinion,
			&d.ReviewID, &d.SubmittedAt, &d.ApprovedAt, &d.ApprovedBy, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rr, err := tx.Query(ctx, `select e.id::text, coalesce(s.key || '-' || e.number, ''), e.title, coalesce(o.name, ''), ''
		from privacy_dpia_risks p join elements e on e.tenant_id = p.tenant_id and e.id = p.risk_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where p.dpia_id = $1 order by e.title`, d.ID)
	if err != nil {
		return nil, err
	}
	if d.Risks, err = pgx.CollectRows(rr, pgx.RowToStructByPos[ElRef]); err != nil {
		return nil, err
	}
	cr, err := tx.Query(ctx, `select e.id::text, coalesce(s.key || '-' || e.number, ''), e.title, coalesce(o.name, ''), coalesce(c.effectiveness, 'not_assessed')
		from privacy_dpia_controls p join elements e on e.tenant_id = p.tenant_id and e.id = p.control_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join ext_control c on c.tenant_id = e.tenant_id and c.element_id = e.id
		where p.dpia_id = $1 order by e.title`, d.ID)
	if err != nil {
		return nil, err
	}
	if d.Controls, err = pgx.CollectRows(cr, pgx.RowToStructByPos[ElRef]); err != nil {
		return nil, err
	}
	if d.Risks == nil {
		d.Risks = []ElRef{}
	}
	if d.Controls == nil {
		d.Controls = []ElRef{}
	}
	// Le valutazioni «DPIA» del questionario: quella scelta e quelle tra cui scegliere.
	vr, err := tx.Query(ctx, `select `+reviewCols+reviewFrom+`
		where x.superseded_by is null and x.stage <> 'cancelled' and (rt.key like 'dpia%' or rt.template_key like 'dpia%')
		order by e.created_at desc limit 50`)
	if err != nil {
		return nil, err
	}
	if d.Reviews, err = scanReviews(vr); err != nil {
		return nil, err
	}
	if d.ReviewID != nil {
		for i := range d.Reviews {
			if d.Reviews[i].ID == *d.ReviewID {
				d.Review = &d.Reviews[i]
			}
		}
		if d.Review == nil {
			one, err := tx.Query(ctx, `select `+reviewCols+reviewFrom+` where e.id = $1`, *d.ReviewID)
			if err != nil {
				return nil, err
			}
			if list, err := scanReviews(one); err == nil && len(list) > 0 {
				d.Review = &list[0]
			}
		}
	}
	return d, nil
}

// SaveDPIA crea o sostituisce il contenuto della DPIA di un trattamento (privacy.write). Una DPIA
// approvata non si modifica: va prima riaperta.
func (s *Service) SaveDPIA(ctx context.Context, tx pgx.Tx, actor engine.Actor, treatmentID string, in DPIAInput) (*DPIA, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	if _, err := loadTreatment(ctx, tx, treatmentID); err != nil {
		return nil, err
	}
	for _, c := range []struct {
		f, v string
	}{{"description", in.Description}, {"necessity", in.Necessity}, {"risk_assessment", in.RiskAssessment}, {"dpo_opinion", in.DPOOpinion}} {
		if err := checkLen(c.f, c.v, 10000); err != nil {
			return nil, err
		}
	}
	in.Description, in.Necessity = strings.TrimSpace(in.Description), strings.TrimSpace(in.Necessity)
	in.RiskAssessment, in.DPOOpinion = strings.TrimSpace(in.RiskAssessment), strings.TrimSpace(in.DPOOpinion)
	in.RiskIDs, in.ControlIDs = uniq(in.RiskIDs), uniq(in.ControlIDs)
	if len(in.RiskIDs) > 50 || len(in.ControlIDs) > 50 {
		return nil, engine.OutOfRange("risk_ids", 0, 50, "al massimo 50 collegamenti")
	}
	if err := checkElements(ctx, tx, "risk_ids", "element", in.RiskIDs, "risk"); err != nil {
		return nil, err
	}
	if err := checkElements(ctx, tx, "control_ids", "element", in.ControlIDs, "control"); err != nil {
		return nil, err
	}
	if in.ReviewID != "" {
		var ok bool
		if !isUUID(in.ReviewID) {
			return nil, engine.NotFound("review_id", "element", "valutazione non trovata")
		}
		if err := tx.QueryRow(ctx, `select exists (select 1 from ext_review where element_id = $1)`, in.ReviewID).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			return nil, engine.NotFound("review_id", "element", "valutazione non trovata")
		}
	}
	var id, status string
	err := tx.QueryRow(ctx, `select id::text, status from privacy_dpias where treatment_id = $1 for update`, treatmentID).Scan(&id, &status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		id = db.NewID()
		if _, err := tx.Exec(ctx, `insert into privacy_dpias (tenant_id, id, treatment_id, created_by) values (app_tenant(), $1, $2, $3)`,
			id, treatmentID, nullable(actor.MemberID)); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case status == "approved":
		return nil, engine.InvalidCode("validation.privacy.dpia_approved", "status", "la DPIA è approvata: riaprila per modificarla")
	}
	if _, err := tx.Exec(ctx, `update privacy_dpias set description = $2, necessity = $3, risk_assessment = $4, dpo_opinion = $5, review_id = $6, updated_at = now()
		where id = $1`, id, in.Description, in.Necessity, in.RiskAssessment, in.DPOOpinion, nullable(in.ReviewID)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `delete from privacy_dpia_risks where dpia_id = $1`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `delete from privacy_dpia_controls where dpia_id = $1`, id); err != nil {
		return nil, err
	}
	if len(in.RiskIDs) > 0 {
		if _, err := tx.Exec(ctx, `insert into privacy_dpia_risks (tenant_id, dpia_id, risk_id) select app_tenant(), $1, r from unnest($2::uuid[]) r`, id, in.RiskIDs); err != nil {
			return nil, err
		}
	}
	if len(in.ControlIDs) > 0 {
		if _, err := tx.Exec(ctx, `insert into privacy_dpia_controls (tenant_id, dpia_id, control_id) select app_tenant(), $1, c from unnest($2::uuid[]) c`, id, in.ControlIDs); err != nil {
			return nil, err
		}
	}
	if err := auditEv(ctx, tx, actor, "privacy.dpia.save", "privacy_dpia", id, map[string]any{"treatment_id": treatmentID, "risks": len(in.RiskIDs), "controls": len(in.ControlIDs)}); err != nil {
		return nil, err
	}
	return loadDPIA(ctx, tx, treatmentID)
}

func uniq(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// TransitionDPIA fa avanzare la DPIA: draft → in_review (serve una descrizione e la valutazione dei rischi),
// in_review → approved (serve il parere del DPO), e indietro (in_review → draft, approved → in_review).
func (s *Service) TransitionDPIA(ctx context.Context, tx pgx.Tx, actor engine.Actor, treatmentID, to string) (*DPIA, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	if !isUUID(treatmentID) {
		return nil, engine.ErrNotFound
	}
	var id, status, desc, risk, opinion string
	err := tx.QueryRow(ctx, `select id::text, status, description, risk_assessment, dpo_opinion from privacy_dpias where treatment_id = $1 for update`,
		treatmentID).Scan(&id, &status, &desc, &risk, &opinion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	switch {
	case status == "draft" && to == "in_review":
		if desc == "" || risk == "" {
			return nil, engine.InvalidCode("validation.privacy.dpia_incomplete", "status", "per metterla in revisione servono la descrizione e la valutazione dei rischi")
		}
		_, err = tx.Exec(ctx, `update privacy_dpias set status = 'in_review', submitted_at = now(), updated_at = now() where id = $1`, id)
	case status == "in_review" && to == "approved":
		if opinion == "" {
			return nil, engine.InvalidCode("validation.privacy.dpia_opinion_required", "status", "per approvarla serve il parere del DPO")
		}
		_, err = tx.Exec(ctx, `update privacy_dpias set status = 'approved', approved_at = now(), approved_by = $2, updated_at = now() where id = $1`, id, nullable(actor.MemberID))
	case status == "in_review" && to == "draft":
		_, err = tx.Exec(ctx, `update privacy_dpias set status = 'draft', updated_at = now() where id = $1`, id)
	case status == "approved" && to == "in_review":
		_, err = tx.Exec(ctx, `update privacy_dpias set status = 'in_review', approved_at = null, approved_by = null, updated_at = now() where id = $1`, id)
	default:
		return nil, engine.InvalidCode("validation.privacy.dpia_transition", "status", "passaggio di stato non ammesso")
	}
	if err != nil {
		return nil, err
	}
	if err := auditEv(ctx, tx, actor, "privacy.dpia.transition", "privacy_dpia", id, map[string]any{"from": status, "to": to}); err != nil {
		return nil, err
	}
	return loadDPIA(ctx, tx, treatmentID)
}
