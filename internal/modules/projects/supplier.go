package projects

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/platform/audit"
	"regia/internal/platform/calendar"
)

// Fornitori (passo 4, progetti.md §3 «Modo di consegna», §12 decisione 3): un work package in
// modo «fornitore» ha un contratto (riferimento e importo), i deliverable da consegnare e
// accettare e i SAL. Il SAL dichiara la percentuale complessiva raggiunta oppure l'importo del
// periodo; lo accetta o lo respinge il PM, e solo i SAL accettati fanno salire l'avanzamento.
//
// Chi dichiara: il PM sempre; il contatto del fornitore (ruolo supplier_contact sul work
// package) solo se il work package lo prevede (supplier_updates). Il fornitore può così entrare
// come utente esterno, con il solo ruolo sul suo work package, oppure restare fuori da Regia.

// Contract è il contratto del work package. Importi solo a chi pianifica e al contatto del fornitore.
// Ref, AmountCents e Currency sono quelli che valgono: i dati scritti sul work package e, dove
// mancano, quelli del contratto collegato del registro ICT (passo 6). OwnRef e OwnAmountCents sono
// quelli scritti sul work package.
type Contract struct {
	Ref            *string         `json:"ref"`
	AmountCents    *int64          `json:"amount_cents"`
	Currency       string          `json:"currency"`
	OwnRef         *string         `json:"own_ref"`
	OwnAmountCents *int64          `json:"own_amount_cents"`
	Linked         *LinkedContract `json:"linked"`
}

// LinkedContract è il contratto del registro ICT collegato al work package (passo 6).
type LinkedContract struct {
	ID              string  `json:"id"`
	Reference       string  `json:"reference"`
	ProviderID      string  `json:"provider_id"`
	Provider        string  `json:"provider"`
	ServiceType     string  `json:"service_type"`
	StartDate       *string `json:"start_date"`
	EndDate         *string `json:"end_date"`
	AnnualCostCents *int64  `json:"annual_cost_cents"`
	Currency        *string `json:"currency"`
	Expired         bool    `json:"expired"`
}

// contractAmountSQL è il valore del contratto su cui si misurano i SAL a importo: quello scritto
// sul work package, altrimenti il costo annuo del contratto collegato del registro ICT.
const contractAmountSQL = `coalesce(w.contract_amount_cents, (select ic.annual_cost_cents from ict_contracts ic where ic.tenant_id = w.tenant_id and ic.id = w.contract_id))`

// Claim è un SAL.
type Claim struct {
	ID           string   `json:"id"`
	Key          string   `json:"key"`
	Number       int      `json:"number"`
	PeriodEnd    *string  `json:"period_end"`
	Percent      *float64 `json:"percent"`
	AmountCents  *int64   `json:"amount_cents"`
	Status       string   `json:"status"` // submitted | accepted | rejected | withdrawn
	State        string   `json:"state"`
	Note         string   `json:"note"`
	SubmittedBy  *string  `json:"submitted_by"`
	SubmittedAt  string   `json:"submitted_at"`
	DecidedBy    *string  `json:"decided_by"`
	DecidedAt    *string  `json:"decided_at"`
	DecisionNote string   `json:"decision_note"`
	// Value: l'avanzamento del work package se questo SAL è (o fosse) l'ultimo accettato.
	Value *float64 `json:"value"`
}

// Deliverable è un deliverable del work package.
type Deliverable struct {
	ID           string  `json:"id"`
	Key          string  `json:"key"`
	Title        string  `json:"title"`
	Status       string  `json:"status"` // pending | delivered | accepted | rejected | cancelled
	State        string  `json:"state"`
	Due          *string `json:"due"`
	MilestoneID  *string `json:"milestone_id"`
	Milestone    *string `json:"milestone"`
	DeliveredBy  *string `json:"delivered_by"`
	DeliveredAt  *string `json:"delivered_at"`
	DeliveryNote string  `json:"delivery_note"`
	DecidedBy    *string `json:"decided_by"`
	DecidedAt    *string `json:"decided_at"`
	DecisionNote string  `json:"decision_note"`
	// Restricted: deliverable che l'attore non legge (senza titolo né note).
	Restricted bool `json:"restricted,omitempty"`
}

// Person è un membro citato (il contatto del fornitore).
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SupplierView è la parte «fornitore» del work package.
type SupplierView struct {
	ItemID          string        `json:"item_id"`
	Mode            string        `json:"delivery_mode"`
	SupplierID      *string       `json:"supplier_id"`
	Supplier        *string       `json:"supplier"`
	SupplierUpdates bool          `json:"supplier_updates"`
	Contacts        []Person      `json:"contacts"`
	Contract        Contract      `json:"contract"`
	Claims          []Claim       `json:"claims"`
	Deliverables    []Deliverable `json:"deliverables"`
	Progress        float64       `json:"progress"`
	ProgressFrom    string        `json:"progress_source"`
	// AcceptedCents: somma degli importi dei SAL accettati (solo con gli importi visibili).
	AcceptedCents  *int64 `json:"accepted_cents"`
	AmountsVisible bool   `json:"amounts_visible"`
	// CanEdit: contratto, contatto, accettazione, nuovi deliverable (chi pianifica il progetto).
	CanEdit bool `json:"can_edit"`
	// CanDeclare: dichiarare un SAL e consegnare un deliverable.
	CanDeclare bool `json:"can_declare"`
}

// claimRow è un SAL accettato, per il calcolo dell'avanzamento.
type claimRow struct {
	Percent *float64
	Amount  *int64
}

// claimValues restituisce, SAL per SAL (in ordine di numero), l'avanzamento che quel SAL porta:
// la percentuale dichiarata, oppure gli importi accettati fin lì sul valore del contratto.
// Senza percentuale né contratto il SAL non dice nulla (nil).
func claimValues(claims []claimRow, contract *int64) []*float64 {
	out := make([]*float64, len(claims))
	var cum int64
	for i, c := range claims {
		if c.Amount != nil {
			cum += *c.Amount
		}
		switch {
		case c.Percent != nil:
			v := *c.Percent
			out[i] = &v
		case contract != nil && *contract > 0:
			v := math.Min(100, round1(float64(cum)*100/float64(*contract)))
			out[i] = &v
		}
	}
	return out
}

// claimProgress è l'avanzamento dai SAL accettati: il valore dell'ultimo che ne porta uno.
func claimProgress(claims []claimRow, contract *int64) *float64 {
	vals := claimValues(claims, contract)
	for i := len(vals) - 1; i >= 0; i-- {
		if vals[i] != nil {
			return vals[i]
		}
	}
	return nil
}

// loadSupplier aggiunge ai work package del fornitore SAL accettati, SAL da accettare e
// deliverable, per l'avanzamento (computeItemProgress) e le tabelle.
func loadSupplier(ctx context.Context, tx pgx.Tx, p *Plan) error {
	rows, err := tx.Query(ctx, `select w.element_id::text, w.supplier_updates, `+contractAmountSQL+`,
		coalesce((select jsonb_agg(jsonb_build_object('Percent', c.percent::float8, 'Amount', c.amount_cents) order by c.number)
		  from ext_progress_claim c join elements ce on ce.tenant_id = c.tenant_id and ce.id = c.element_id and ce.deleted_at is null
		  where c.work_package_id = w.element_id and c.status = 'accepted'), '[]'::jsonb),
		(select count(*) from ext_progress_claim c join elements ce on ce.tenant_id = c.tenant_id and ce.id = c.element_id and ce.deleted_at is null
		  where c.work_package_id = w.element_id and c.status = 'submitted'),
		(select count(*) from ext_deliverable d join elements de on de.tenant_id = d.tenant_id and de.id = d.element_id and de.deleted_at is null
		  where d.work_package_id = w.element_id and d.status <> 'cancelled'),
		(select count(*) from ext_deliverable d join elements de on de.tenant_id = d.tenant_id and de.id = d.element_id and de.deleted_at is null
		  where d.work_package_id = w.element_id and d.status = 'accepted')
		from ext_work_package w where w.project_id = $1 and w.delivery_mode = 'supplier'`, p.Project.ID)
	if err != nil {
		return err
	}
	type sup struct {
		updates            bool
		contract           *int64
		claims             []claimRow
		pending, dels, acc int
	}
	byID := map[string]sup{}
	for rows.Next() {
		var id string
		var s sup
		if err := rows.Scan(&id, &s.updates, &s.contract, &s.claims, &s.pending, &s.dels, &s.acc); err != nil {
			rows.Close()
			return err
		}
		byID[id] = s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range p.Items {
		it := &p.Items[i]
		s, ok := byID[it.ID]
		if !ok {
			continue
		}
		it.SupplierUpdates = s.updates
		it.ClaimsPending = s.pending
		it.Deliverables, it.DeliverablesAccepted = s.dels, s.acc
		it.claimProgress = claimProgress(s.claims, s.contract)
	}
	return nil
}

// supplierWP restituisce il work package (letto con i permessi dell'attore) e il suo progetto.
func (s *Service) supplierWP(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, string, error) {
	el, projectID, err := s.item(ctx, tx, actor, ref)
	if err != nil {
		return nil, "", err
	}
	if el.TypeKey != TypeWorkPackage {
		return nil, "", engine.InvalidCode("validation.projects.not_work_package", "item", "SAL e deliverable stanno su un work package")
	}
	return el, projectID, nil
}

// supplierRights: chi pianifica (contratto, accettazione) e chi dichiara (SAL, consegne).
func supplierRights(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID, wpID string) (plan, declare, contact bool, err error) {
	if plan, err = canPlan(ctx, tx, actor, projectID); err != nil {
		return
	}
	if contact, err = engine.HasRole(ctx, tx, wpID, actor.MemberID, []string{RoleSupplierContact}); err != nil {
		return
	}
	var updates bool
	var mode string
	if err = tx.QueryRow(ctx, "select supplier_updates, delivery_mode from ext_work_package where element_id = $1", wpID).Scan(&updates, &mode); err != nil {
		return
	}
	declare = mode == ModeSupplier && (plan || contact && updates)
	return
}

// Supplier restituisce contratto, SAL e deliverable del work package.
func (s *Service) Supplier(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*SupplierView, error) {
	el, projectID, err := s.supplierWP(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	plan, declare, contact, err := supplierRights(ctx, tx, actor, projectID, el.ID)
	if err != nil {
		return nil, err
	}
	v := &SupplierView{ItemID: el.ID, Contacts: []Person{}, Claims: []Claim{}, Deliverables: []Deliverable{},
		CanEdit: plan, CanDeclare: declare, AmountsVisible: plan || contact}
	var amount, ownAmount *int64
	var lc LinkedContract
	var lcID, lcRef, lcProvID, lcProv, lcService, lcCur *string
	var lcStart, lcEnd *time.Time
	var lcCost *int64
	if err := tx.QueryRow(ctx, `select w.delivery_mode, w.supplier_id::text, su.title, w.supplier_updates, w.contract_ref, w.contract_amount_cents, w.contract_currency,
		ic.id::text, ic.reference, ic.provider_element_id::text, pe.title, ic.service_type, ic.start_date::timestamptz, ic.end_date::timestamptz, ic.annual_cost_cents, ic.currency::text
		from ext_work_package w left join elements su on su.tenant_id = w.tenant_id and su.id = w.supplier_id and su.deleted_at is null
		left join ict_contracts ic on ic.tenant_id = w.tenant_id and ic.id = w.contract_id
		left join elements pe on pe.tenant_id = ic.tenant_id and pe.id = ic.provider_element_id
		where w.element_id = $1`, el.ID).Scan(&v.Mode, &v.SupplierID, &v.Supplier, &v.SupplierUpdates, &v.Contract.OwnRef, &ownAmount, &v.Contract.Currency,
		&lcID, &lcRef, &lcProvID, &lcProv, &lcService, &lcStart, &lcEnd, &lcCost, &lcCur); err != nil {
		return nil, err
	}
	v.Contract.Ref, amount = v.Contract.OwnRef, ownAmount
	if lcID != nil {
		lc = LinkedContract{ID: *lcID, Reference: deref(lcRef), ProviderID: deref(lcProvID), Provider: deref(lcProv), ServiceType: deref(lcService),
			StartDate: dateStr(lcStart), EndDate: dateStr(lcEnd), Currency: lcCur, Expired: lcEnd != nil && lcEnd.Before(today())}
		if v.AmountsVisible {
			lc.AnnualCostCents = lcCost
		}
		v.Contract.Linked = &lc
		if v.Contract.Ref == nil {
			v.Contract.Ref = &lc.Reference
		}
		if amount == nil && lcCost != nil {
			amount = lcCost
			if lcCur != nil {
				v.Contract.Currency = *lcCur
			}
		}
	}
	if v.AmountsVisible {
		v.Contract.AmountCents, v.Contract.OwnAmountCents = amount, ownAmount
	}
	rows, err := tx.Query(ctx, `select m.id::text, m.display_name from stakeholders sh
		join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = $2
		join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id
		where sh.element_id = $1 order by m.display_name`, el.ID, RoleSupplierContact)
	if err != nil {
		return nil, err
	}
	if v.Contacts, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Person]); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select e.id::text, sp.key || '-' || e.number, c.number, to_char(c.period_end, 'YYYY-MM-DD'),
		c.percent::float8, c.amount_cents, c.status, coalesce(o.name, ''), c.note, sm.display_name,
		to_char(e.created_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), dm.display_name,
		to_char(c.decided_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), c.decision_note
		from ext_progress_claim c
		join elements e on e.tenant_id = c.tenant_id and e.id = c.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join members sm on sm.tenant_id = c.tenant_id and sm.id = c.submitted_by
		left join members dm on dm.tenant_id = c.tenant_id and dm.id = c.decided_by
		where c.work_package_id = $1 order by c.number`, el.ID)
	if err != nil {
		return nil, err
	}
	if v.Claims, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Claim, error) {
		var c Claim
		err := r.Scan(&c.ID, &c.Key, &c.Number, &c.PeriodEnd, &c.Percent, &c.AmountCents, &c.Status, &c.State, &c.Note, &c.SubmittedBy,
			&c.SubmittedAt, &c.DecidedBy, &c.DecidedAt, &c.DecisionNote)
		return c, err
	}); err != nil {
		return nil, err
	}
	// Valore di ogni SAL: gli accettati in ordine, e per quello da accettare il valore che avrebbe.
	var acc []claimRow
	var accIdx []int
	var accepted int64
	for i, c := range v.Claims {
		if c.Status == "accepted" || c.Status == "submitted" {
			acc = append(acc, claimRow{Percent: c.Percent, Amount: c.AmountCents})
			accIdx = append(accIdx, i)
			vals := claimValues(acc, amount)
			v.Claims[i].Value = vals[len(vals)-1]
			if c.Status == "submitted" {
				acc, accIdx = acc[:len(acc)-1], accIdx[:len(accIdx)-1]
			} else if c.AmountCents != nil {
				accepted += *c.AmountCents
			}
		}
	}
	if v.AmountsVisible {
		v.AcceptedCents = &accepted
	} else {
		for i := range v.Claims {
			v.Claims[i].AmountCents = nil
			if v.Claims[i].Percent == nil {
				v.Claims[i].Value = nil // dedotto dall'importo: non si mostra a chi non vede gli importi
			}
		}
	}
	rows, err = tx.Query(ctx, `select e.id::text, sp.key || '-' || e.number, e.title, d.status, coalesce(o.name, ''), e.due_at,
		d.milestone_id::text, me.title, dm.display_name, to_char(d.delivered_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		d.delivery_note, cm.display_name, to_char(d.decided_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), d.decision_note
		from ext_deliverable d
		join elements e on e.tenant_id = d.tenant_id and e.id = d.element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join elements me on me.tenant_id = d.tenant_id and me.id = d.milestone_id and me.deleted_at is null
		left join members dm on dm.tenant_id = d.tenant_id and dm.id = d.delivered_by
		left join members cm on cm.tenant_id = d.tenant_id and cm.id = d.decided_by
		where d.work_package_id = $1 order by e.due_at nulls last, e.number`, el.ID)
	if err != nil {
		return nil, err
	}
	if v.Deliverables, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Deliverable, error) {
		var d Deliverable
		var due *time.Time
		err := r.Scan(&d.ID, &d.Key, &d.Title, &d.Status, &d.State, &due, &d.MilestoneID, &d.Milestone, &d.DeliveredBy, &d.DeliveredAt,
			&d.DeliveryNote, &d.DecidedBy, &d.DecidedAt, &d.DecisionNote)
		d.Due = dateStr(due)
		return d, err
	}); err != nil {
		return nil, err
	}
	rd := s.reader(actor)
	for i := range v.Deliverables {
		d := &v.Deliverables[i]
		ok, err := rd.can(ctx, tx, d.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			d.Restricted, d.Title, d.DeliveryNote, d.DecisionNote, d.Milestone = true, "", "", "", nil
		}
		if d.MilestoneID != nil && d.Milestone != nil {
			if ok, err := rd.can(ctx, tx, *d.MilestoneID); err != nil {
				return nil, err
			} else if !ok {
				d.Milestone = nil
			}
		}
	}
	iv, err := s.ItemView(ctx, tx, actor, el.ID)
	if err != nil {
		return nil, err
	}
	v.Progress, v.ProgressFrom = iv.Progress, iv.ProgressFrom
	return v, nil
}

// SupplierInput modifica il contratto e le regole del work package del fornitore (chi pianifica).
// ContactID: il contatto del fornitore (sostituisce quello di prima; "" lo toglie).
type SupplierInput struct {
	SupplierUpdates *bool   `json:"supplier_updates"`
	ContractRef     *string `json:"contract_ref"`
	AmountCents     *int64  `json:"contract_amount_cents"`
	ClearAmount     bool    `json:"clear_amount"`
	Currency        *string `json:"contract_currency"`
	ContactID       *string `json:"contact_id"`
	// ContractID: il contratto del registro ICT (passo 6); "" toglie il collegamento.
	ContractID *string `json:"contract_id"`
}

func (s *Service) UpdateSupplier(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in SupplierInput) (*SupplierView, error) {
	el, projectID, err := s.supplierWP(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, projectID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	if in.ContractRef != nil {
		r := strings.TrimSpace(*in.ContractRef)
		if len(r) > 200 {
			return nil, engine.TooLong("contract_ref", 200, "riferimento troppo lungo (massimo 200 caratteri)")
		}
		in.ContractRef = &r
	}
	if in.AmountCents != nil && *in.AmountCents < 0 {
		return nil, engine.InvalidCode("validation.not_negative", "contract_amount_cents", "non può essere negativo")
	}
	if in.Currency != nil {
		c := strings.ToUpper(strings.TrimSpace(*in.Currency))
		if len(c) != 3 || strings.Trim(c, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
			return nil, engine.InvalidValue("contract_currency", "valuta non valida (tre lettere, per esempio EUR)")
		}
		in.Currency = &c
	}
	if in.ContractID != nil {
		if err := s.linkContract(ctx, tx, actor, el.ID, strings.TrimSpace(*in.ContractID)); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `update ext_work_package set
		supplier_updates = coalesce($2, supplier_updates),
		contract_ref = case when $3::text is null then contract_ref else nullif($3, '') end,
		contract_amount_cents = case when $5 then null else coalesce($4, contract_amount_cents) end,
		contract_currency = coalesce($6, contract_currency)
		where element_id = $1`, el.ID, in.SupplierUpdates, in.ContractRef, in.AmountCents, in.ClearAmount, in.Currency); err != nil {
		return nil, err
	}
	if in.ContactID != nil {
		if err := s.setContact(ctx, tx, actor, el.ID, *in.ContactID); err != nil {
			return nil, err
		}
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "plan", "supplier", toMap(in), nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.supplier.update", EntityType: TypeWorkPackage, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: toMap(in)}); err != nil {
		return nil, err
	}
	return s.Supplier(ctx, tx, actor, el.ID)
}

// linkContract collega il work package a un contratto del registro ICT ("" lo scollega). Il
// contratto deve essere del fornitore del work package; se il work package non ha ancora un
// fornitore, prende quello del contratto.
func (s *Service) linkContract(ctx context.Context, tx pgx.Tx, actor engine.Actor, wpID, contractID string) error {
	if contractID == "" {
		_, err := tx.Exec(ctx, "update ext_work_package set contract_id = null where element_id = $1", wpID)
		return err
	}
	var provider string
	if engine.IsUUID(contractID) {
		_ = tx.QueryRow(ctx, "select provider_element_id::text from ict_contracts where id = $1", contractID).Scan(&provider)
	}
	if provider == "" {
		return engine.NotFound("contract_id", "contract", "contratto inesistente")
	}
	if ok, err := s.eng.CanRead(ctx, tx, actor, provider); err != nil || !ok {
		return firstErr(err, engine.NotFound("contract_id", "contract", "contratto inesistente"))
	}
	var supplier *string
	if err := tx.QueryRow(ctx, "select supplier_id::text from ext_work_package where element_id = $1", wpID).Scan(&supplier); err != nil {
		return err
	}
	if supplier != nil && *supplier != provider {
		return engine.InvalidCode("validation.projects.contract_supplier", "contract_id", "il contratto è di un altro fornitore")
	}
	_, err := tx.Exec(ctx, "update ext_work_package set contract_id = $2, supplier_id = coalesce(supplier_id, $3) where element_id = $1", wpID, contractID, provider)
	return err
}

// ContractOption è un contratto del registro ICT che il work package può collegare.
type ContractOption struct {
	ID          string  `json:"id"`
	Reference   string  `json:"reference"`
	ProviderID  string  `json:"provider_id"`
	Provider    string  `json:"provider"`
	ServiceType string  `json:"service_type"`
	StartDate   *string `json:"start_date"`
	EndDate     *string `json:"end_date"`
	Expired     bool    `json:"expired"`
}

// ContractOptions elenca i contratti collegabili al work package: quelli del suo fornitore o,
// senza fornitore, tutti quelli dei fornitori che l'attore legge. Solo per chi pianifica.
func (s *Service) ContractOptions(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) ([]ContractOption, error) {
	el, projectID, err := s.supplierWP(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, projectID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	rows, err := tx.Query(ctx, `select c.id::text, c.reference, c.provider_element_id::text, pe.title, c.service_type,
		c.start_date::timestamptz, c.end_date::timestamptz
		from ict_contracts c
		join elements pe on pe.tenant_id = c.tenant_id and pe.id = c.provider_element_id and pe.deleted_at is null
		join ext_work_package w on w.tenant_id = c.tenant_id and w.element_id = $1
		where w.supplier_id is null or w.supplier_id = c.provider_element_id
		order by pe.title, c.end_date desc nulls first, c.reference limit 300`, el.ID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ContractOption, error) {
		var c ContractOption
		var start, end *time.Time
		err := r.Scan(&c.ID, &c.Reference, &c.ProviderID, &c.Provider, &c.ServiceType, &start, &end)
		c.StartDate, c.EndDate = dateStr(start), dateStr(end)
		c.Expired = end != nil && end.Before(today())
		return c, err
	})
	if err != nil {
		return nil, err
	}
	out := []ContractOption{}
	rd := s.reader(actor)
	for _, c := range list {
		if ok, err := rd.can(ctx, tx, c.ProviderID); err != nil {
			return nil, err
		} else if ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// setContact sostituisce il contatto del fornitore sul work package e lo porta sui SAL e sui
// deliverable già presenti, così li legge anche senza permessi sul resto del tenant.
func (s *Service) setContact(ctx context.Context, tx pgx.Tx, actor engine.Actor, wpID, memberID string) error {
	if _, err := tx.Exec(ctx, `delete from stakeholders sh using stakeholder_roles r
		where r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = $2
		and (sh.element_id = $1 or sh.element_id in (select element_id from ext_progress_claim where work_package_id = $1
		  union all select element_id from ext_deliverable where work_package_id = $1))`, wpID, RoleSupplierContact); err != nil {
		return err
	}
	if memberID == "" {
		return nil
	}
	if !engine.IsUUID(memberID) {
		return engine.InvalidCode("validation.member_not_found", "contact_id", "persona inesistente")
	}
	if err := engine.SetStakeholder(ctx, tx, actor, wpID, RoleSupplierContact, memberID, "", true); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `select element_id::text from ext_progress_claim where work_package_id = $1
		union all select element_id::text from ext_deliverable where work_package_id = $1`, wpID)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := shareContacts(ctx, tx, wpID, id); err != nil {
			return err
		}
	}
	return nil
}

// shareContacts dà ai contatti del fornitore del work package lo stesso ruolo sull'elemento figlio
// (SAL o deliverable), senza notifiche: lo vedono perché è del loro work package.
func shareContacts(ctx context.Context, tx pgx.Tx, wpID, childID string) error {
	_, err := tx.Exec(ctx, `insert into stakeholders (tenant_id, element_id, role_id, member_id)
		select sh.tenant_id, $2, sh.role_id, sh.member_id from stakeholders sh
		join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = $3
		where sh.element_id = $1 and sh.member_id is not null on conflict do nothing`, wpID, childID, RoleSupplierContact)
	return err
}

// ClaimInput dichiara un SAL: percentuale complessiva raggiunta e/o importo del periodo.
// Accept (solo chi pianifica): il PM inserisce e accetta insieme il SAL ricevuto fuori da Regia.
type ClaimInput struct {
	Percent     *float64 `json:"percent"`
	AmountCents *int64   `json:"amount_cents"`
	PeriodEnd   string   `json:"period_end"`
	Note        string   `json:"note"`
	Accept      bool     `json:"accept"`
}

// CreateClaim dichiara un nuovo SAL del work package.
func (s *Service) CreateClaim(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ClaimInput) (*SupplierView, error) {
	el, projectID, err := s.supplierWP(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	plan, declare, _, err := supplierRights(ctx, tx, actor, projectID, el.ID)
	if err != nil {
		return nil, err
	}
	var mode string
	if err := tx.QueryRow(ctx, "select delivery_mode from ext_work_package where element_id = $1", el.ID).Scan(&mode); err != nil {
		return nil, err
	}
	if mode != ModeSupplier {
		return nil, engine.InvalidCode("validation.projects.not_supplier", "delivery_mode", "i SAL valgono solo per i work package affidati a un fornitore")
	}
	if !declare || in.Accept && !plan {
		return nil, engine.ErrForbidden
	}
	if in.Percent == nil && in.AmountCents == nil {
		return nil, engine.InvalidCode("validation.projects.claim_empty", "percent", "indica la percentuale raggiunta o l'importo del SAL")
	}
	if in.Percent != nil && (math.IsNaN(*in.Percent) || *in.Percent < 0 || *in.Percent > 100) {
		return nil, engine.InvalidCode("validation.projects.percent", "percent", "la percentuale va da 0 a 100")
	}
	if in.AmountCents != nil && *in.AmountCents < 0 {
		return nil, engine.InvalidCode("validation.not_negative", "amount_cents", "non può essere negativo")
	}
	if len(in.Note) > 2000 {
		return nil, engine.TooLong("note", 2000, "nota troppo lunga (massimo 2000 caratteri)")
	}
	var period *time.Time
	if in.PeriodEnd != "" {
		d, err := calendar.Parse(in.PeriodEnd)
		if err != nil {
			return nil, engine.DateInvalid("period_end", "data non valida, formato AAAA-MM-GG")
		}
		period = &d
	}
	var pending bool
	var last *float64
	var next int
	if err := tx.QueryRow(ctx, `select
		exists (select 1 from ext_progress_claim c join elements e on e.id = c.element_id and e.deleted_at is null
		  where c.work_package_id = $1 and c.status = 'submitted'),
		(select c.percent::float8 from ext_progress_claim c join elements e on e.id = c.element_id and e.deleted_at is null
		  where c.work_package_id = $1 and c.status = 'accepted' and c.percent is not null order by c.number desc limit 1),
		coalesce((select max(number) from ext_progress_claim where work_package_id = $1), 0) + 1`, el.ID).Scan(&pending, &last, &next); err != nil {
		return nil, err
	}
	if pending {
		return nil, engine.InvalidCode("validation.projects.claim_pending", "status", "c'è già un SAL da accettare: aspetta la decisione del PM")
	}
	if in.Percent != nil && last != nil && *in.Percent < *last {
		return nil, engine.InvalidParams("validation.projects.claim_backwards", "percent", map[string]any{"percent": *last},
			"la percentuale è complessiva e non può scendere sotto l'ultimo SAL accettato (%v%%)", *last)
	}
	var pct *float64
	if in.Percent != nil {
		v := math.Round(*in.Percent*100) / 100
		pct = &v
	}
	title := "SAL " + strconv.Itoa(next) + " · " + el.Title
	if len(title) > 500 {
		title = title[:500]
	}
	cl, err := s.eng.Create(ctx, tx, sysActor(actor), engine.CreateInput{SpaceKey: SpaceKey, TypeKey: TypeProgressClaim, Title: title,
		BodyText: strings.TrimSpace(in.Note), ParentID: el.ID, DeferStart: true})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into ext_progress_claim (tenant_id, element_id, project_id, work_package_id, number, period_end,
		percent, amount_cents, note, submitted_by) values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		cl.ID, projectID, el.ID, next, period, pct, in.AmountCents, strings.TrimSpace(in.Note), nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if _, err := s.eng.StartProcess(ctx, tx, sysActor(actor), cl); err != nil {
		return nil, err
	}
	if err := shareContacts(ctx, tx, el.ID, cl.ID); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "supplier", "claim", map[string]any{"claim": cl.Key, "number": next, "percent": pct}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.claim.create", EntityType: TypeProgressClaim, EntityID: cl.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"work_package_id": el.ID, "number": next, "percent": pct, "amount_cents": in.AmountCents}}); err != nil {
		return nil, err
	}
	if in.Accept {
		if err := s.decideClaim(ctx, tx, actor, cl.ID, "accepted", ""); err != nil {
			return nil, err
		}
	} else if err := s.notifyPM(ctx, tx, actor, projectID, cl.ID, "claim_submitted", map[string]any{"work_package": el.Key, "number": next}); err != nil {
		return nil, err
	}
	return s.Supplier(ctx, tx, actor, el.ID)
}

// DecideClaim accetta o respinge il SAL dichiarato (chi pianifica il progetto).
func (s *Service) DecideClaim(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, outcome, note string) (*SupplierView, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != TypeProgressClaim {
		return nil, engine.ErrNotFound
	}
	var projectID, wpID string
	if err := tx.QueryRow(ctx, "select project_id::text, work_package_id::text from ext_progress_claim where element_id = $1", el.ID).Scan(&projectID, &wpID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, projectID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	if outcome != "accepted" && outcome != "rejected" {
		return nil, engine.InvalidValue("outcome", "esito non ammesso (accepted, rejected)")
	}
	if outcome == "rejected" && strings.TrimSpace(note) == "" {
		return nil, engine.InvalidCode("validation.projects.reason_required", "note", "spiega al fornitore perché il SAL è respinto")
	}
	if len(note) > 2000 {
		return nil, engine.TooLong("note", 2000, "nota troppo lunga (massimo 2000 caratteri)")
	}
	if err := s.decideClaim(ctx, tx, actor, el.ID, outcome, note); err != nil {
		return nil, err
	}
	return s.Supplier(ctx, tx, actor, wpID)
}

// decideClaim registra l'esito. Il SAL accettato entra nello storico dell'avanzamento del work
// package (ext_plan_progress) con il valore che porta.
func (s *Service) decideClaim(ctx context.Context, tx pgx.Tx, actor engine.Actor, claimID, outcome, note string) error {
	var wpID string
	var number int
	tag, err := tx.Exec(ctx, `update ext_progress_claim set status = $2, decided_by = $3, decided_at = now(), decision_note = $4
		where element_id = $1 and status = 'submitted'`, claimID, outcome, nullable(actor.MemberID), strings.TrimSpace(note))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.InvalidCode("validation.projects.claim_status", "status", "si decide solo un SAL dichiarato e non ancora deciso")
	}
	if err := tx.QueryRow(ctx, "select work_package_id::text, number from ext_progress_claim where element_id = $1", claimID).Scan(&wpID, &number); err != nil {
		return err
	}
	state := "Accettato"
	if outcome == "rejected" {
		state = "Respinto"
	}
	if err := forceState(ctx, tx, claimID, TypeProgressClaim, state); err != nil {
		return err
	}
	if outcome == "accepted" {
		var rows []claimRow
		var contract *int64
		if err := tx.QueryRow(ctx, `select `+contractAmountSQL+`,
			coalesce((select jsonb_agg(jsonb_build_object('Percent', c.percent::float8, 'Amount', c.amount_cents) order by c.number)
			  from ext_progress_claim c join elements ce on ce.tenant_id = c.tenant_id and ce.id = c.element_id and ce.deleted_at is null
			  where c.work_package_id = w.element_id and c.status = 'accepted'), '[]'::jsonb)
			from ext_work_package w where w.element_id = $1`, wpID).Scan(&contract, &rows); err != nil {
			return err
		}
		if v := claimProgress(rows, contract); v != nil {
			if _, err := tx.Exec(ctx, `insert into ext_plan_progress (tenant_id, element_id, percent, note, member_id)
				values (app_tenant(), $1, $2, $3, $4)`, wpID, *v, "SAL "+strconv.Itoa(number), nullable(actor.MemberID)); err != nil {
				return err
			}
		}
	}
	if err := engine.AddActivity(ctx, tx, wpID, actor.MemberID, "supplier", "claim_"+outcome, map[string]any{"number": number, "note": note}, nil); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "plan.claim." + outcome, EntityType: TypeProgressClaim, EntityID: claimID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"note": note}})
}

// DeliverableInput crea un deliverable del work package (chi pianifica).
type DeliverableInput struct {
	Title       string `json:"title"`
	Due         string `json:"due"`
	MilestoneID string `json:"milestone_id"`
}

func (s *Service) CreateDeliverable(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in DeliverableInput) (*SupplierView, error) {
	el, projectID, err := s.supplierWP(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, projectID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, engine.InvalidCode("validation.title_required", "title", "il titolo è obbligatorio")
	}
	var due *time.Time
	if in.Due != "" {
		d, err := calendar.Parse(in.Due)
		if err != nil {
			return nil, engine.DateInvalid("due", "data non valida, formato AAAA-MM-GG")
		}
		due = &d
	}
	var milestone any
	if in.MilestoneID != "" {
		id := resolveID(ctx, tx, in.MilestoneID)
		pid, kind, err := itemProject(ctx, tx, id)
		if err != nil && !errors.Is(err, engine.ErrNotFound) {
			return nil, err
		}
		if err != nil || pid != projectID || kind != TypeMilestone {
			return nil, engine.NotFound("milestone_id", "milestone", "milestone inesistente nel progetto")
		}
		milestone = id
	}
	d, err := s.eng.Create(ctx, tx, sysActor(actor), engine.CreateInput{SpaceKey: SpaceKey, TypeKey: TypeDeliverable, Title: title,
		ParentID: el.ID, DeferStart: true})
	if err != nil {
		return nil, err
	}
	if due != nil {
		if _, err := tx.Exec(ctx, "update elements set start_at = $2, due_at = $2 where id = $1", d.ID, *due); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `insert into ext_deliverable (tenant_id, element_id, project_id, work_package_id, milestone_id)
		values (app_tenant(), $1, $2, $3, $4)`, d.ID, projectID, el.ID, milestone); err != nil {
		return nil, err
	}
	if _, err := s.eng.StartProcess(ctx, tx, sysActor(actor), d); err != nil {
		return nil, err
	}
	if err := shareContacts(ctx, tx, el.ID, d.ID); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "supplier", "deliverable", map[string]any{"deliverable": d.Key}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.deliverable.create", EntityType: TypeDeliverable, EntityID: d.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"work_package_id": el.ID}}); err != nil {
		return nil, err
	}
	return s.Supplier(ctx, tx, actor, el.ID)
}

// deliverableOf trova il deliverable e il suo work package.
func (s *Service) deliverableOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, string, string, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, "", "", err
	}
	if el.TypeKey != TypeDeliverable {
		return nil, "", "", engine.ErrNotFound
	}
	var projectID, wpID string
	if err := tx.QueryRow(ctx, "select project_id::text, work_package_id::text from ext_deliverable where element_id = $1", el.ID).Scan(&projectID, &wpID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", "", engine.ErrNotFound
		}
		return nil, "", "", err
	}
	return el, projectID, wpID, nil
}

// Deliver consegna il deliverable (contatto del fornitore, se il work package lo prevede, o PM).
func (s *Service) Deliver(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, note string) (*SupplierView, error) {
	el, projectID, wpID, err := s.deliverableOf(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	_, declare, _, err := supplierRights(ctx, tx, actor, projectID, wpID)
	if err != nil {
		return nil, err
	}
	if !declare {
		return nil, engine.ErrForbidden
	}
	if len(note) > 2000 {
		return nil, engine.TooLong("note", 2000, "nota troppo lunga (massimo 2000 caratteri)")
	}
	tag, err := tx.Exec(ctx, `update ext_deliverable set status = 'delivered', delivered_by = $2, delivered_at = now(), delivery_note = $3,
		decided_by = null, decided_at = null, decision_note = '' where element_id = $1 and status in ('pending', 'rejected')`,
		el.ID, nullable(actor.MemberID), strings.TrimSpace(note))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.InvalidCode("validation.projects.deliverable_status", "status", "si consegna solo un deliverable da consegnare o respinto")
	}
	if err := forceState(ctx, tx, el.ID, TypeDeliverable, "Consegnato"); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, wpID, actor.MemberID, "supplier", "delivered", map[string]any{"deliverable": el.Key, "note": note}, nil); err != nil {
		return nil, err
	}
	if err := s.notifyPM(ctx, tx, actor, projectID, el.ID, "deliverable_delivered", map[string]any{"deliverable": el.Key}); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.deliverable.deliver", EntityType: TypeDeliverable, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP}); err != nil {
		return nil, err
	}
	return s.Supplier(ctx, tx, actor, wpID)
}

// DecideDeliverable accetta o respinge il deliverable consegnato (chi pianifica). Respinto, torna
// da consegnare.
func (s *Service) DecideDeliverable(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, outcome, note string) (*SupplierView, error) {
	el, projectID, wpID, err := s.deliverableOf(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if ok, err := canPlan(ctx, tx, actor, projectID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	if outcome != "accepted" && outcome != "rejected" {
		return nil, engine.InvalidValue("outcome", "esito non ammesso (accepted, rejected)")
	}
	if outcome == "rejected" && strings.TrimSpace(note) == "" {
		return nil, engine.InvalidCode("validation.projects.reason_required", "note", "spiega al fornitore perché il deliverable è respinto")
	}
	if len(note) > 2000 {
		return nil, engine.TooLong("note", 2000, "nota troppo lunga (massimo 2000 caratteri)")
	}
	tag, err := tx.Exec(ctx, `update ext_deliverable set status = $2, decided_by = $3, decided_at = now(), decision_note = $4
		where element_id = $1 and status = 'delivered'`, el.ID, outcome, nullable(actor.MemberID), strings.TrimSpace(note))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.InvalidCode("validation.projects.deliverable_status", "status", "si decide solo un deliverable consegnato")
	}
	state := "Accettato"
	if outcome == "rejected" {
		state = "Da consegnare"
	}
	if err := forceState(ctx, tx, el.ID, TypeDeliverable, state); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, wpID, actor.MemberID, "supplier", "deliverable_"+outcome, map[string]any{"deliverable": el.Key, "note": note}, nil); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "plan.deliverable." + outcome, EntityType: TypeDeliverable, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"note": note}}); err != nil {
		return nil, err
	}
	return s.Supplier(ctx, tx, actor, wpID)
}

// supplierCancelled: «Ritira» sul SAL dichiarato o «Annulla» sul deliverable, dal processo.
func supplierCancelled(ctx context.Context, tx pgx.Tx, el *engine.Element) error {
	if el.TypeKey == TypeProgressClaim {
		_, err := tx.Exec(ctx, "update ext_progress_claim set status = 'withdrawn', decided_at = now() where element_id = $1 and status = 'submitted'", el.ID)
		return err
	}
	_, err := tx.Exec(ctx, "update ext_deliverable set status = 'cancelled' where element_id = $1 and status <> 'accepted'", el.ID)
	return err
}

// notifyPM avvisa i PM del progetto (SAL dichiarato, deliverable consegnato).
func (s *Service) notifyPM(ctx context.Context, tx pgx.Tx, actor engine.Actor, projectID, elementID, event string, payload map[string]any) error {
	rows, err := tx.Query(ctx, `select sh.member_id::text from stakeholders sh
		join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = 'project_manager'
		where sh.element_id = $1 and sh.member_id is not null`, projectID)
	if err != nil {
		return err
	}
	pms, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(pms) == 0 {
		return err
	}
	payload["event"] = event
	_, err = notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindProject, ElementID: elementID, ActorID: actor.MemberID, Members: pms, Payload: payload})
	return err
}
