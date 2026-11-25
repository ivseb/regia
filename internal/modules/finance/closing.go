package finance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/platform/db"
)

// ---------------------------------------------------------------------
// «Ripartisci» (budget.md §6): anteprima e conferma
// ---------------------------------------------------------------------

// Modi di ripartizione.
const (
	DistEqual    = "equal"    // parti uguali
	DistPercent  = "percent"  // percentuali indicate
	DistPrevious = "previous" // come il periodo precedente
	DistRequests = "requests" // in proporzione alle richieste in graduatoria dell'area
)

// DistTarget è un destinatario: un figlio esistente (allocation_id) o un'unità o elemento
// da aggiungere come figlio.
type DistTarget struct {
	AllocationID    string   `json:"allocation_id"`
	OrgUnitID       *string  `json:"org_unit_id"`
	ElementID       *string  `json:"element_id"`
	Nature          *string  `json:"nature"`
	FundingSourceID *string  `json:"funding_source_id"`
	Percent         *float64 `json:"percent"`
}

type DistInput struct {
	Mode       string       `json:"mode"`
	TotalCents *int64       `json:"total_cents"` // da ripartire; predefinito: lo stanziato del nodo
	Targets    []DistTarget `json:"targets"`     // vuoto: i figli esistenti, o le unità sotto quella del nodo
	Reason     string       `json:"reason"`
}

type DistLine struct {
	AllocationID   *string  `json:"allocation_id"`
	OrgUnitID      *string  `json:"org_unit_id"`
	ElementID      *string  `json:"element_id"`
	Label          string   `json:"label"`
	New            bool     `json:"new"`
	CurrentCents   int64    `json:"current_cents"`
	ProposedCents  int64    `json:"proposed_cents"`
	DeltaCents     int64    `json:"delta_cents"`
	CommittedCents int64    `json:"committed_cents"`
	Percent        float64  `json:"percent"`
	WeightCents    int64    `json:"weight_cents,omitempty"` // modo requests: richieste in graduatoria dell'unità
	Warnings       []string `json:"warnings"`
	target         DistTarget
}

type DistPreview struct {
	NodeID           string     `json:"node_id"`
	Mode             string     `json:"mode"`
	TotalCents       int64      `json:"total_cents"`
	Lines            []DistLine `json:"lines"`
	AllocatedCents   int64      `json:"allocated_cents"`   // stanziato del nodo
	DistributedAfter int64      `json:"distributed_after"` // ripartito ai figli dopo la conferma
	ReserveAfter     int64      `json:"reserve_after"`     // riserva del nodo dopo la conferma
	ExceedsCents     int64      `json:"exceeds_cents"`     // superamento del nodo (0 = ok)
	Warnings         []string   `json:"warnings"`
	Valid            bool       `json:"valid"`
}

// splitProportional divide total in parti intere secondo i pesi (resto ai più grandi).
func splitProportional(total int64, w []float64) []int64 {
	sum := 0.0
	for _, x := range w {
		sum += x
	}
	out := make([]int64, len(w))
	if sum <= 0 || total <= 0 {
		return out
	}
	type rem struct {
		i int
		f float64
	}
	var left int64 = total
	rems := make([]rem, len(w))
	for i, x := range w {
		v := float64(total) * x / sum
		out[i] = int64(math.Floor(v))
		left -= out[i]
		rems[i] = rem{i, v - math.Floor(v)}
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].f > rems[b].f })
	for k := 0; left > 0 && k < len(rems); k++ {
		out[rems[k].i]++
		left--
	}
	return out
}

func (s *Service) previousPeriodID(ctx context.Context, tx pgx.Tx, p *Period) (*string, error) {
	var id string
	err := tx.QueryRow(ctx, "select id from budget_periods where end_date < $1 order by end_date desc limit 1", p.start).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &id, err
}

// planDistribution calcola l'anteprima: nessuna scrittura.
func (s *Service) planDistribution(ctx context.Context, tx pgx.Tx, a engine.Actor, nodeID string, in DistInput) (*DistPreview, *Node, error) {
	var periodID string
	if err := tx.QueryRow(ctx, "select period_id from budget_allocations where id::text = $1", nodeID).Scan(&periodID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, engine.ErrNotFound
		}
		return nil, nil, err
	}
	p, err := s.period(ctx, tx, periodID)
	if err != nil {
		return nil, nil, err
	}
	nodes, err := s.loadNodes(ctx, tx, periodID)
	if err != nil {
		return nil, nil, err
	}
	children, err := s.computeNumbers(ctx, tx, p, nodes)
	if err != nil {
		return nil, nil, err
	}
	var node *Node
	byID := map[string]*Node{}
	for _, n := range nodes {
		byID[n.ID] = n
		if n.ID == nodeID {
			node = n
		}
	}
	if node == nil {
		return nil, nil, engine.ErrNotFound
	}
	if node.Kind == "element" {
		return nil, nil, engine.InvalidCode("validation.finance.item_not_allocatable", "node", "un elemento non si ripartisce")
	}
	if in.Mode == "" {
		in.Mode = DistEqual
	}
	if in.Mode != DistEqual && in.Mode != DistPercent && in.Mode != DistPrevious && in.Mode != DistRequests {
		return nil, nil, engine.InvalidValue("mode", "modo non ammesso (equal, percent, previous, requests)")
	}
	total := node.AllocatedCents
	if in.TotalCents != nil {
		if *in.TotalCents < 0 {
			return nil, nil, engine.InvalidCode("validation.not_negative", "total_cents", "il totale non può essere negativo")
		}
		total = *in.TotalCents
	}
	// Destinatari.
	targets := in.Targets
	if len(targets) == 0 {
		for _, c := range children[node.ID] {
			if !c.Archived {
				targets = append(targets, DistTarget{AllocationID: c.ID})
			}
		}
		if len(targets) == 0 {
			var rows pgx.Rows
			if node.Kind == "root" {
				rows, err = tx.Query(ctx, "select id from org_units where parent_id is null and not archived order by lower(name)")
			} else {
				rows, err = tx.Query(ctx, "select id from org_units where parent_id = $1 and not archived order by lower(name)", *node.OrgUnitID)
			}
			if err != nil {
				return nil, nil, err
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return nil, nil, err
			}
			for _, id := range ids {
				id := id
				targets = append(targets, DistTarget{OrgUnitID: &id})
			}
		}
	}
	if len(targets) == 0 {
		return nil, nil, engine.InvalidCode("validation.finance.targets_none", "targets", "non ci sono destinatari: indica le unità o gli elementi a cui ripartire")
	}
	if len(targets) > 300 {
		return nil, nil, engine.InvalidCode("validation.finance.targets_too_many", "targets", "al massimo 300 destinatari per volta")
	}
	pv := &DistPreview{NodeID: node.ID, Mode: in.Mode, TotalCents: total, AllocatedCents: node.AllocatedCents, Lines: []DistLine{}, Warnings: []string{}}
	var prevID *string
	if in.Mode == DistPrevious {
		if prevID, err = s.previousPeriodID(ctx, tx, p); err != nil {
			return nil, nil, err
		}
		if prevID == nil {
			return nil, nil, engine.InvalidCode("validation.finance.mode_no_previous_period", "mode", "non c'è un periodo precedente")
		}
	}
	seen := map[string]bool{}
	weights := make([]float64, len(targets))
	for i, t := range targets {
		l := DistLine{Warnings: []string{}, target: t}
		var existing *Node
		switch {
		case t.AllocationID != "":
			c := byID[t.AllocationID]
			if c == nil || c.ParentID == nil || *c.ParentID != node.ID {
				return nil, nil, engine.InvalidCode("validation.finance.target_not_child", "targets", "il nodo %s non è un figlio di questo nodo", t.AllocationID)
			}
			existing = c
			t.OrgUnitID, t.ElementID, t.Nature, t.FundingSourceID = c.OrgUnitID, c.ElementID, c.Nature, c.FundingSourceID
			l.target = t
		case (t.OrgUnitID == nil || *t.OrgUnitID == "") == (t.ElementID == nil || *t.ElementID == ""):
			return nil, nil, engine.InvalidCode("validation.finance.target_unit_or_item", "targets", "ogni destinatario è un'unità oppure un elemento")
		default:
			for _, c := range children[node.ID] {
				if ptrEq(c.OrgUnitID, t.OrgUnitID) && ptrEq(c.ElementID, t.ElementID) && ptrEq(blankNil(c.Nature), blankNil(t.Nature)) && ptrEq(c.FundingSourceID, blankNil(t.FundingSourceID)) {
					existing = c
					break
				}
			}
		}
		key := fmt.Sprint(t.OrgUnitID, t.ElementID, t.Nature, t.FundingSourceID)
		if seen[key] {
			return nil, nil, engine.InvalidCode("validation.finance.target_repeated", "targets", "destinatario ripetuto")
		}
		seen[key] = true
		l.OrgUnitID, l.ElementID = t.OrgUnitID, t.ElementID
		if existing != nil {
			l.AllocationID, l.Label, l.CurrentCents, l.CommittedCents = &existing.ID, existing.Label, existing.AllocatedCents, existing.CommittedCents
		} else {
			l.New = true
			if t.OrgUnitID != nil {
				_ = tx.QueryRow(ctx, "select name from org_units where id::text = $1", *t.OrgUnitID).Scan(&l.Label)
			} else {
				_ = tx.QueryRow(ctx, `select coalesce(sp.key || '-' || e.number, '') || ' ' || e.title from elements e join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
					where e.id::text = $1`, *t.ElementID).Scan(&l.Label)
			}
			if l.Label == "" {
				return nil, nil, engine.NotFound("targets", "", "destinatario inesistente")
			}
		}
		switch in.Mode {
		case DistEqual:
			weights[i] = 1
		case DistPercent:
			if t.Percent == nil || *t.Percent < 0 {
				return nil, nil, engine.InvalidParams("validation.finance.target_percent_missing", "targets", map[string]any{"label": l.Label}, "«%s»: manca la percentuale", l.Label)
			}
			weights[i] = *t.Percent
			l.Percent = *t.Percent
		case DistRequests:
			if t.OrgUnitID == nil {
				return nil, nil, engine.InvalidParams("validation.finance.target_ranking_between_units", "targets", map[string]any{"label": l.Label}, "«%s»: le richieste in graduatoria si ripartiscono tra unità", l.Label)
			}
			if err := tx.QueryRow(ctx, `select coalesce(sum(coalesce(d.estimate_high_cents, d.estimate_low_cents, 0)), 0)
				from ext_demand d join elements e on e.tenant_id = d.tenant_id and e.id = d.element_id and e.deleted_at is null
				where d.requesting_unit_id in (select org_subtree($1::uuid)) and d.rank is not null and d.merged_into_element_id is null`,
				*t.OrgUnitID).Scan(&l.WeightCents); err != nil {
				return nil, nil, err
			}
			weights[i] = float64(l.WeightCents)
		}
		pv.Lines = append(pv.Lines, l)
	}
	switch in.Mode {
	case DistEqual:
		for i, v := range splitProportional(total, weights) {
			pv.Lines[i].ProposedCents = v
		}
	case DistPercent:
		sum := 0.0
		for _, w := range weights {
			sum += w
		}
		if sum > 100.0001 {
			return nil, nil, engine.InvalidParams("validation.finance.percent_over_100", "targets", map[string]any{"sum": fmt.Sprintf("%.2f", sum)}, "le percentuali sommano %.2f%%: al massimo 100%%", sum)
		}
		// La quota indicata di ogni destinatario, arrotondata; il resto resta di riserva.
		for i, w := range weights {
			pv.Lines[i].ProposedCents = int64(math.Round(float64(total) * w / 100))
		}
	case DistRequests:
		sum := 0.0
		for _, w := range weights {
			sum += w
		}
		if sum <= 0 {
			return nil, nil, engine.InvalidCode("validation.finance.ranking_none", "mode", "nessuna richiesta in graduatoria per queste unità")
		}
		for i, v := range splitProportional(total, weights) {
			pv.Lines[i].ProposedCents = v
			pv.Lines[i].Percent = math.Round(weights[i]*10000/sum) / 100
		}
	case DistPrevious:
		for i := range pv.Lines {
			l := &pv.Lines[i]
			if err := tx.QueryRow(ctx, `select coalesce(sum(m.amount_cents), 0) from budget_allocations a2
				join budget_movements m on m.tenant_id = a2.tenant_id and m.allocation_id = a2.id and m.closure_id is null
				where a2.period_id::text = $1 and a2.kind <> 'root'
				  and a2.org_unit_id is not distinct from $2::uuid and a2.element_id is not distinct from $3::uuid
				  and a2.nature is not distinct from $4 and a2.funding_source_id is not distinct from $5::uuid`,
				*prevID, l.OrgUnitID, l.ElementID, blankNil(l.target.Nature), blankNil(l.target.FundingSourceID)).Scan(&l.ProposedCents); err != nil {
				return nil, nil, err
			}
			if l.ProposedCents == 0 {
				l.Warnings = append(l.Warnings, "nel periodo precedente non aveva stanziamento")
			}
		}
	}
	// Totali e avvisi. Gli altri figli (non destinatari) restano come sono.
	var others int64
	targeted := map[string]bool{}
	for i := range pv.Lines {
		l := &pv.Lines[i]
		l.DeltaCents = l.ProposedCents - l.CurrentCents
		if l.AllocationID != nil {
			targeted[*l.AllocationID] = true
		}
		if l.ProposedCents < l.CommittedCents && !l.New {
			l.Warnings = append(l.Warnings, fmt.Sprintf("sono già impegnati %s: lo stanziamento non può scendere sotto", money(l.CommittedCents)))
		}
		if l.AllocationID != nil {
			var inner int64
			for _, c := range children[*l.AllocationID] {
				inner += c.AllocatedCents
			}
			if l.ProposedCents < inner {
				l.Warnings = append(l.Warnings, fmt.Sprintf("il nodo ha già ripartito %s ai suoi figli", money(inner)))
			}
		}
		pv.DistributedAfter += l.ProposedCents
	}
	for _, c := range children[node.ID] {
		if !targeted[c.ID] {
			others += c.AllocatedCents
		}
	}
	pv.DistributedAfter += others
	pv.ReserveAfter = node.AllocatedCents - pv.DistributedAfter
	if pv.ReserveAfter < 0 {
		pv.ExceedsCents = -pv.ReserveAfter
		pv.Warnings = append(pv.Warnings, fmt.Sprintf("la ripartizione supera di %s lo stanziamento del nodo (%s)", money(pv.ExceedsCents), money(node.AllocatedCents)))
	}
	pv.Valid = pv.ExceedsCents == 0
	for i := range pv.Lines {
		for _, w := range pv.Lines[i].Warnings {
			if strings.Contains(w, "non può scendere") || strings.Contains(w, "già ripartito") {
				pv.Valid = false
			}
		}
	}
	return pv, node, nil
}

func blankNil(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return p
}

func ptrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// DistributePreview calcola la ripartizione senza scrivere nulla.
func (s *Service) DistributePreview(ctx context.Context, tx pgx.Tx, a engine.Actor, nodeID string, in DistInput) (*DistPreview, error) {
	pv, _, err := s.planDistribution(ctx, tx, a, nodeID, in)
	return pv, err
}

// Distribute applica la ripartizione: stessa logica dell'anteprima, ricalcolata con il periodo
// bloccato. Rifiuta ciò che l'anteprima segnala come non valido.
func (s *Service) Distribute(ctx context.Context, tx pgx.Tx, a engine.Actor, nodeID string, in DistInput) (*DistPreview, error) {
	var periodID string
	if err := tx.QueryRow(ctx, "select period_id from budget_allocations where id::text = $1", nodeID).Scan(&periodID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	if _, err := s.writablePeriod(ctx, tx, a, periodID); err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx, tx, a)
	if err != nil {
		return nil, err
	}
	pv, node, err := s.planDistribution(ctx, tx, a, nodeID, in)
	if err != nil {
		return nil, err
	}
	if !sc.canAllocateUnder(node) {
		return nil, engine.ErrForbidden
	}
	if !pv.Valid {
		msg := strings.Join(pv.Warnings, "; ")
		for _, l := range pv.Lines {
			for _, w := range l.Warnings {
				msg += fmt.Sprintf("; %s: %s", l.Label, w)
			}
		}
		return nil, engine.InvalidValue("targets", "la ripartizione non è valida: %s", strings.TrimPrefix(msg, "; "))
	}
	batch := db.NewID()
	touched := []string{node.ID}
	for i := range pv.Lines {
		l := &pv.Lines[i]
		id := ""
		if l.New {
			t := l.target
			id, err = s.insertNode(ctx, tx, a, node, NodeInput{PeriodID: periodID, ParentID: node.ID, OrgUnitID: t.OrgUnitID, ElementID: t.ElementID,
				Nature: t.Nature, FundingSourceID: t.FundingSourceID})
			if err != nil {
				return nil, err
			}
			l.AllocationID = &id
		} else {
			id = *l.AllocationID
		}
		if l.DeltaCents == 0 {
			continue
		}
		kind := MoveAllocate
		if l.DeltaCents < 0 {
			kind = MoveAdjust
		}
		if err := s.addMove(ctx, tx, a, periodID, move{alloc: id, counterpart: node.ID, kind: kind, amount: l.DeltaCents, batch: batch,
			reason: firstNonEmpty(in.Reason, "Ripartizione ("+pv.Mode+")")}); err != nil {
			return nil, err
		}
		touched = append(touched, id)
	}
	if err := s.verify(ctx, tx, touched...); err != nil {
		return nil, err
	}
	if err := s.aud(ctx, tx, a, "budget.distribute", "budget_allocation", node.ID, map[string]any{"mode": pv.Mode, "lines": len(pv.Lines),
		"distributed_cents": pv.DistributedAfter, "batch_id": batch}); err != nil {
		return nil, err
	}
	return pv, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------------
// Fine periodo (budget.md §3)
// ---------------------------------------------------------------------

type ClosureLine struct {
	AllocationID   string   `json:"allocation_id"`
	Label          string   `json:"label"`
	Depth          int      `json:"depth"`
	Kind           string   `json:"kind"`
	Policy         string   `json:"policy"`        // return | carry | lapse
	PolicySource   string   `json:"policy_source"` // tenant | node
	AllocatedCents int64    `json:"allocated_cents"`
	CommittedCents int64    `json:"committed_cents"`
	RemainderCents int64    `json:"remainder_cents"` // non impegnato e non ripartito del nodo
	ReturnCents    int64    `json:"return_cents"`    // movimenti del nodo (anche per effetto dei discendenti)
	CarryCents     int64    `json:"carry_cents"`
	LapseCents     int64    `json:"lapse_cents"`
	Warnings       []string `json:"warnings"`
}

type ClosurePreview struct {
	PeriodID       string        `json:"period_id"`
	NextPeriodID   *string       `json:"next_period_id"`
	NextPeriod     *string       `json:"next_period"`
	DefaultPolicy  string        `json:"default_policy"`
	Lines          []ClosureLine `json:"lines"`
	ReturnCents    int64         `json:"return_cents"`
	CarryCents     int64         `json:"carry_cents"`
	LapseCents     int64         `json:"lapse_cents"`
	OpenCommitment int           `json:"open_commitments"` // nodi con ordini aperti o sforamento
	Warnings       []string      `json:"warnings"`
	NeedsApproval  bool          `json:"needs_approval"` // c'è un riporto: va confermato in modo esplicito
	CanConfirm     bool          `json:"can_confirm"`
	// Interni: i movimenti da scrivere.
	moves []closureMove
}

type closureMove struct {
	alloc  string
	kind   string
	amount int64 // positivo: quanto esce dal nodo
}

// planClosure calcola cosa torna, cosa si riporta e cosa decade, nodo per nodo, dal basso.
func (s *Service) planClosure(ctx context.Context, tx pgx.Tx, p *Period, nodes []*Node, children map[string][]*Node) (*ClosurePreview, error) {
	st, err := GetBudgetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	pv := &ClosurePreview{PeriodID: p.ID, DefaultPolicy: st.EndPolicy, Lines: []ClosureLine{}, Warnings: []string{}}
	next := ""
	err = tx.QueryRow(ctx, `select id, name from budget_periods where start_date > $1 order by start_date limit 1`, p.start).Scan(&next, new(string))
	if err == nil {
		pv.NextPeriodID = &next
		var nm string
		_ = tx.QueryRow(ctx, "select name from budget_periods where id = $1", next).Scan(&nm)
		pv.NextPeriod = &nm
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	byID := map[string]*Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	alloc := map[string]int64{}
	for _, n := range nodes {
		alloc[n.ID] = n.AllocatedCents
	}
	ordered, _ := forest(nodes)
	lines := map[string]*ClosureLine{}
	for _, n := range ordered {
		pol, src := st.EndPolicy, "tenant"
		if n.EndPolicy != nil {
			pol, src = *n.EndPolicy, "node"
		}
		lines[n.ID] = &ClosureLine{AllocationID: n.ID, Label: n.Label, Depth: n.Depth, Kind: n.Kind, Policy: pol, PolicySource: src,
			AllocatedCents: n.AllocatedCents, CommittedCents: n.CommittedCents, Warnings: []string{}}
	}
	out := map[string]*closureMove{} // chiave alloc|kind
	add := func(id, kind string, v int64) {
		k := id + "|" + kind
		if out[k] == nil {
			out[k] = &closureMove{alloc: id, kind: kind}
		}
		out[k].amount += v
	}
	var post func(n *Node)
	post = func(n *Node) {
		var kids int64
		for _, c := range children[n.ID] {
			post(c)
			kids += alloc[c.ID]
		}
		l := lines[n.ID]
		var own int64
		if n.Kind == "element" {
			own = n.CommittedCents
			if open := n.OrdersCents - n.SpentCents; open > 0 {
				l.Warnings = append(l.Warnings, fmt.Sprintf("ordini aperti per %s", money(open)))
				pv.OpenCommitment++
			}
			if n.CommittedCents > n.AllocatedCents {
				l.Warnings = append(l.Warnings, fmt.Sprintf("impegnato oltre lo stanziamento di %s", money(n.CommittedCents-n.AllocatedCents)))
				pv.OpenCommitment++
			}
		}
		rem := alloc[n.ID] - kids - own
		if rem <= 0 {
			return
		}
		l.RemainderCents = rem
		switch l.Policy {
		case "return":
			if n.Kind == "root" {
				return // il non impegnato della radice resta nel plafond del periodo
			}
			alloc[n.ID] -= rem
			add(n.ID, MoveReturn, rem)
		case "carry", "lapse":
			kind := MoveCarry
			if l.Policy == "lapse" {
				kind = MoveLapse
			}
			// Il denaro lascia il periodo: esce dal nodo e da tutti gli antenati, che altrimenti
			// resterebbero con uno stanziamento che nessuno può più usare.
			for x := n; x != nil; {
				alloc[x.ID] -= rem
				add(x.ID, kind, rem)
				if x.ParentID == nil || byID[*x.ParentID] == nil {
					break
				}
				x = byID[*x.ParentID]
			}
		}
	}
	for _, n := range ordered {
		if n.ParentID == nil || byID[*n.ParentID] == nil {
			post(n)
		}
	}
	for _, m := range out {
		l := lines[m.alloc]
		switch m.kind {
		case MoveReturn:
			l.ReturnCents += m.amount
			pv.ReturnCents += m.amount
		case MoveCarry:
			l.CarryCents += m.amount
		case MoveLapse:
			l.LapseCents += m.amount
		}
		pv.moves = append(pv.moves, *m)
	}
	// I totali di riporto e decadenza contano una volta il denaro (nella radice che lo contiene).
	for _, n := range ordered {
		if n.Kind == "root" || n.ParentID == nil || byID[*n.ParentID] == nil {
			pv.CarryCents += lines[n.ID].CarryCents
			pv.LapseCents += lines[n.ID].LapseCents
		}
	}
	for _, n := range ordered {
		pv.Lines = append(pv.Lines, *lines[n.ID])
	}
	sort.Slice(pv.moves, func(i, j int) bool {
		if pv.moves[i].alloc != pv.moves[j].alloc {
			return pv.moves[i].alloc < pv.moves[j].alloc
		}
		return pv.moves[i].kind < pv.moves[j].kind
	})
	pv.NeedsApproval = pv.CarryCents > 0
	pv.CanConfirm = true
	if pv.CarryCents > 0 && pv.NextPeriodID == nil {
		pv.CanConfirm = false
		pv.Warnings = append(pv.Warnings, "c'è un importo da riportare ma non esiste il periodo successivo")
	}
	if pv.OpenCommitment > 0 {
		pv.Warnings = append(pv.Warnings, fmt.Sprintf("%d nodi hanno impegni aperti o sforamenti: la chiusura non li tocca", pv.OpenCommitment))
	}
	if p.Status == "planning" || p.Status == "closed" {
		pv.CanConfirm = false
		pv.Warnings = append(pv.Warnings, "il periodo deve essere aperto o in chiusura")
	}
	return pv, nil
}

// ClosePreview mostra nodo per nodo cosa tornerebbe, si riporterebbe o decadrebbe. Nessuna scrittura.
func (s *Service) ClosePreview(ctx context.Context, tx pgx.Tx, a engine.Actor, periodID string) (*ClosurePreview, error) {
	if err := requireWrite(a); err != nil {
		return nil, err
	}
	p, err := s.period(ctx, tx, periodID)
	if err != nil {
		return nil, err
	}
	nodes, err := s.loadNodes(ctx, tx, periodID)
	if err != nil {
		return nil, err
	}
	children, err := s.computeNumbers(ctx, tx, p, nodes)
	if err != nil {
		return nil, err
	}
	return s.planClosure(ctx, tx, p, nodes, children)
}

// StartClosing porta il periodo in chiusura: da qui solo la finanza modifica gli stanziamenti.
func (s *Service) StartClosing(ctx context.Context, tx pgx.Tx, a engine.Actor, periodID string) (*Period, error) {
	if err := requireWrite(a); err != nil {
		return nil, err
	}
	p, err := s.lockPeriod(ctx, tx, periodID)
	if err != nil {
		return nil, err
	}
	if p.Status != "open" {
		return nil, engine.InvalidCode("validation.finance.period_only_open_to_closing", "status", "solo un periodo aperto passa in chiusura")
	}
	if _, err := tx.Exec(ctx, "update budget_periods set status = 'closing' where id = $1", periodID); err != nil {
		return nil, err
	}
	if err := s.aud(ctx, tx, a, "budget.period.closing", "budget_period", periodID, nil); err != nil {
		return nil, err
	}
	return s.period(ctx, tx, periodID)
}

// mirror trova o crea nel periodo successivo il nodo corrispondente a src, con i suoi antenati.
func (s *Service) mirror(ctx context.Context, tx pgx.Tx, a engine.Actor, next *Period, src *Node, byID map[string]*Node, cache map[string]string) (string, error) {
	if id, ok := cache[src.ID]; ok {
		return id, nil
	}
	var id string
	if src.Kind == "root" {
		err := tx.QueryRow(ctx, "select id from budget_allocations where period_id = $1 and kind = 'root'", next.ID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			id = db.NewID()
			if _, err := tx.Exec(ctx, `insert into budget_allocations (tenant_id, id, period_id, kind, name, created_by) values (app_tenant(), $1, $2, 'root', $3, $4)`,
				id, next.ID, "Plafond "+next.Name, nullable(a.MemberID)); err != nil {
				return "", err
			}
		} else if err != nil {
			return "", err
		}
		cache[src.ID] = id
		return id, nil
	}
	err := tx.QueryRow(ctx, `select id from budget_allocations where period_id = $1 and kind = $2
		and org_unit_id is not distinct from $3::uuid and element_id is not distinct from $4::uuid
		and nature is not distinct from $5 and funding_source_id is not distinct from $6::uuid`,
		next.ID, src.Kind, src.OrgUnitID, src.ElementID, src.Nature, src.FundingSourceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		if src.ParentID == nil || byID[*src.ParentID] == nil {
			return "", engine.Required("period", "manca il nodo padre da riportare")
		}
		parent, err := s.mirror(ctx, tx, a, next, byID[*src.ParentID], byID, cache)
		if err != nil {
			return "", err
		}
		id = db.NewID()
		if _, err := tx.Exec(ctx, `insert into budget_allocations (tenant_id, id, period_id, parent_id, kind, org_unit_id, element_id, nature, funding_source_id, cost_category_id, end_policy, note, created_by)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			id, next.ID, parent, src.Kind, src.OrgUnitID, src.ElementID, src.Nature, src.FundingSourceID, src.CostCategoryID, src.EndPolicy, src.Note, nullable(a.MemberID)); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	cache[src.ID] = id
	return id, nil
}

type Closure struct {
	ID           string     `json:"id"`
	PeriodID     string     `json:"period_id"`
	NextPeriodID *string    `json:"next_period_id"`
	Status       string     `json:"status"` // confirmed | undone
	ReturnCents  int64      `json:"return_cents"`
	CarryCents   int64      `json:"carry_cents"`
	LapseCents   int64      `json:"lapse_cents"`
	CreatedAt    time.Time  `json:"created_at"`
	UndoneAt     *time.Time `json:"undone_at"`
}

// CloseInput conferma la chiusura. Se c'è un riporto serve approve_carry: il riporto lo
// approva esplicitamente chi chiude (budget.md §3).
type CloseInput struct {
	ApproveCarry bool `json:"approve_carry"`
}

// ClosePeriod scrive i movimenti di fine periodo e chiude il periodo.
func (s *Service) ClosePeriod(ctx context.Context, tx pgx.Tx, a engine.Actor, periodID string, in CloseInput) (*Closure, error) {
	if err := requireWrite(a); err != nil {
		return nil, err
	}
	p, err := s.lockPeriod(ctx, tx, periodID)
	if err != nil {
		return nil, err
	}
	if p.Status != "open" && p.Status != "closing" {
		return nil, engine.InvalidCode("validation.finance.period_must_be_open_or_closing", "status", "il periodo deve essere aperto o in chiusura")
	}
	nodes, err := s.loadNodes(ctx, tx, periodID)
	if err != nil {
		return nil, err
	}
	children, err := s.computeNumbers(ctx, tx, p, nodes)
	if err != nil {
		return nil, err
	}
	pv, err := s.planClosure(ctx, tx, p, nodes, children)
	if err != nil {
		return nil, err
	}
	if !pv.CanConfirm {
		return nil, engine.InvalidParams("validation.finance.period_warnings", "period", map[string]any{"detail": strings.Join(pv.Warnings, "; ")}, "%s", strings.Join(pv.Warnings, "; "))
	}
	if pv.NeedsApproval && !in.ApproveCarry {
		return nil, engine.InvalidParams("validation.finance.carry_needs_approval", "approve_carry", map[string]any{"amount": money(pv.CarryCents)}, "il riporto di %s al periodo successivo richiede la tua approvazione esplicita", money(pv.CarryCents))
	}
	var next *Period
	if pv.CarryCents > 0 {
		if next, err = s.lockPeriod(ctx, tx, *pv.NextPeriodID); err != nil {
			return nil, err
		}
		if next.Status == "closed" || next.Status == "closing" {
			return nil, engine.InvalidCode("validation.finance.next_period_closed", "period", "il periodo successivo è già chiuso o in chiusura")
		}
	}
	byID := map[string]*Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	closureID := db.NewID()
	summary, _ := json.Marshal(map[string]any{"return_cents": pv.ReturnCents, "carry_cents": pv.CarryCents, "lapse_cents": pv.LapseCents, "lines": pv.Lines})
	if _, err := tx.Exec(ctx, `insert into budget_closures (tenant_id, id, period_id, next_period_id, summary, created_by) values (app_tenant(), $1, $2, $3, $4, $5)`,
		closureID, periodID, pv.NextPeriodID, summary, nullable(a.MemberID)); err != nil {
		return nil, err
	}
	batch := db.NewID()
	cache := map[string]string{}
	touched := []string{}
	for _, m := range pv.moves {
		mv := move{alloc: m.alloc, kind: m.kind, amount: -m.amount, batch: batch, closure: closureID,
			reason: map[string]string{MoveReturn: "Restituzione di fine periodo", MoveCarry: "Riporto al periodo successivo", MoveLapse: "Decadenza di fine periodo"}[m.kind]}
		if m.kind == MoveReturn {
			if n := byID[m.alloc]; n != nil && n.ParentID != nil {
				mv.counterpart = *n.ParentID
			}
		}
		if m.kind == MoveCarry {
			mid, err := s.mirror(ctx, tx, a, next, byID[m.alloc], byID, cache)
			if err != nil {
				return nil, err
			}
			mv.counterpart = mid
			if err := s.addMove(ctx, tx, a, next.ID, move{alloc: mid, counterpart: m.alloc, kind: MoveCarry, amount: m.amount, batch: batch, closure: closureID,
				reason: "Riporto dal periodo " + p.Name}); err != nil {
				return nil, err
			}
			touched = append(touched, mid)
		}
		if err := s.addMove(ctx, tx, a, periodID, mv); err != nil {
			return nil, err
		}
		touched = append(touched, m.alloc)
	}
	if err := s.verify(ctx, tx, touched...); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "update budget_periods set status = 'closed', closed_at = now(), closed_by = $2 where id = $1", periodID, nullable(a.MemberID)); err != nil {
		return nil, err
	}
	if err := s.syncEnvelope(ctx, tx, p.FiscalYearID); err != nil {
		return nil, err
	}
	if next != nil {
		if err := s.syncEnvelope(ctx, tx, next.FiscalYearID); err != nil {
			return nil, err
		}
	}
	if err := s.aud(ctx, tx, a, "budget.period.close", "budget_period", periodID, map[string]any{"closure_id": closureID, "return_cents": pv.ReturnCents,
		"carry_cents": pv.CarryCents, "lapse_cents": pv.LapseCents}); err != nil {
		return nil, err
	}
	return s.closure(ctx, tx, closureID)
}

func (s *Service) closure(ctx context.Context, tx pgx.Tx, id string) (*Closure, error) {
	var c Closure
	err := tx.QueryRow(ctx, `select id, period_id, next_period_id, status,
		coalesce((summary->>'return_cents')::bigint, 0), coalesce((summary->>'carry_cents')::bigint, 0), coalesce((summary->>'lapse_cents')::bigint, 0),
		created_at, undone_at from budget_closures where id::text = $1`, id).
		Scan(&c.ID, &c.PeriodID, &c.NextPeriodID, &c.Status, &c.ReturnCents, &c.CarryCents, &c.LapseCents, &c.CreatedAt, &c.UndoneAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	return &c, err
}

// Closures elenca le chiusure di un periodo.
func (s *Service) Closures(ctx context.Context, tx pgx.Tx, periodID string) ([]Closure, error) {
	rows, err := tx.Query(ctx, `select id, period_id, next_period_id, status,
		coalesce((summary->>'return_cents')::bigint, 0), coalesce((summary->>'carry_cents')::bigint, 0), coalesce((summary->>'lapse_cents')::bigint, 0),
		created_at, undone_at from budget_closures where period_id::text = $1 order by created_at`, periodID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Closure])
	if out == nil {
		out = []Closure{}
	}
	return out, err
}

// UndoClose annulla l'ultima chiusura del periodo finché il periodo successivo non ha
// ripartizioni: scrive i movimenti opposti (il registro non si modifica) e riapre il periodo.
func (s *Service) UndoClose(ctx context.Context, tx pgx.Tx, a engine.Actor, periodID string) (*Period, error) {
	if err := requireWrite(a); err != nil {
		return nil, err
	}
	p, err := s.lockPeriod(ctx, tx, periodID)
	if err != nil {
		return nil, err
	}
	if p.Status != "closed" {
		return nil, engine.InvalidCode("validation.finance.period_not_closed", "status", "il periodo non è chiuso")
	}
	var cid string
	var next *string
	if err := tx.QueryRow(ctx, `select id, next_period_id from budget_closures where period_id = $1 and status = 'confirmed' order by created_at desc limit 1`, periodID).
		Scan(&cid, &next); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.InvalidCode("validation.finance.no_closing_to_undo", "status", "nessuna chiusura da annullare")
		}
		return nil, err
	}
	if next != nil {
		if _, err := s.lockPeriod(ctx, tx, *next); err != nil {
			return nil, err
		}
		var n int
		if err := tx.QueryRow(ctx, `select count(*) from budget_movements where period_id = $1 and kind in ('allocate','transfer','adjust') and closure_id is null`, *next).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, engine.InvalidCode("validation.finance.next_period_has_allocations", "status", "il periodo successivo ha già ripartizioni: la chiusura non si annulla più")
		}
		var st string
		if err := tx.QueryRow(ctx, "select status from budget_periods where id = $1", *next).Scan(&st); err != nil {
			return nil, err
		}
		if st == "closed" || st == "closing" {
			return nil, engine.InvalidCode("validation.finance.next_period_closed", "status", "il periodo successivo è già in chiusura o chiuso")
		}
	}
	rows, err := tx.Query(ctx, `select period_id, allocation_id, kind, amount_cents, counterpart_id from budget_movements where closure_id = $1 order by created_at, id`, cid)
	if err != nil {
		return nil, err
	}
	type mv struct {
		period, alloc, kind string
		amount              int64
		cp                  *string
	}
	var ms []mv
	for rows.Next() {
		var m mv
		if err := rows.Scan(&m.period, &m.alloc, &m.kind, &m.amount, &m.cp); err != nil {
			rows.Close()
			return nil, err
		}
		ms = append(ms, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	batch := db.NewID()
	var touched []string
	for _, m := range ms {
		cp := ""
		if m.cp != nil {
			cp = *m.cp
		}
		if err := s.addMove(ctx, tx, a, m.period, move{alloc: m.alloc, counterpart: cp, kind: m.kind, amount: -m.amount, batch: batch, closure: cid,
			reason: "Annullamento della chiusura"}); err != nil {
			return nil, err
		}
		touched = append(touched, m.alloc)
	}
	if err := s.verify(ctx, tx, touched...); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "update budget_closures set status = 'undone', undone_by = $2, undone_at = now() where id = $1", cid, nullable(a.MemberID)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "update budget_periods set status = 'open', closed_at = null, closed_by = null where id = $1", periodID); err != nil {
		return nil, err
	}
	if err := s.syncEnvelope(ctx, tx, p.FiscalYearID); err != nil {
		return nil, err
	}
	if err := s.aud(ctx, tx, a, "budget.period.undo_close", "budget_period", periodID, map[string]any{"closure_id": cid}); err != nil {
		return nil, err
	}
	return s.period(ctx, tx, periodID)
}

// ---------------------------------------------------------------------
// Trasferimenti tra nodi (budget.md §4)
// ---------------------------------------------------------------------

type AllocTransfer struct {
	ID           string     `json:"id"`
	PeriodID     string     `json:"period_id"`
	FromID       string     `json:"from_allocation_id"`
	ToID         string     `json:"to_allocation_id"`
	From         string     `json:"from"`
	To           string     `json:"to"`
	AmountCents  int64      `json:"amount_cents"`
	Reason       string     `json:"reason"`
	Status       string     `json:"status"`        // pending | approved | rejected
	ApprovalKind string     `json:"approval_kind"` // none | manager | body
	ApproverID   *string    `json:"approver_member_id"`
	ApprovalID   *string    `json:"approval_request_id"`
	CreatedBy    *string    `json:"created_by"`
	CreatedAt    time.Time  `json:"created_at"`
	DecidedAt    *time.Time `json:"decided_at"`
	DecisionNote *string    `json:"decision_note"`
}

const transferSelect = `select t.id, t.period_id, t.from_allocation_id, t.to_allocation_id,
	(select coalesce(u.name, nullif(coalesce(sp.key || '-' || e.number, '') || ' ' || coalesce(e.title, ''), ' '), a.name) from budget_allocations a
	   left join org_units u on u.tenant_id = a.tenant_id and u.id = a.org_unit_id
	   left join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
	   where a.id = t.from_allocation_id),
	(select coalesce(u.name, nullif(coalesce(sp.key || '-' || e.number, '') || ' ' || coalesce(e.title, ''), ' '), a.name) from budget_allocations a
	   left join org_units u on u.tenant_id = a.tenant_id and u.id = a.org_unit_id
	   left join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id left join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
	   where a.id = t.to_allocation_id),
	t.amount_cents, t.reason, t.status, t.approval_kind, t.approver_member_id, t.approval_request_id, cm.display_name, t.created_at, t.decided_at, t.decision_note
	from allocation_transfers t left join members cm on cm.tenant_id = t.tenant_id and cm.id = t.created_by`

// AllocTransfers elenca i trasferimenti di un periodo (quelli che l'attore vede).
func (s *Service) AllocTransfers(ctx context.Context, tx pgx.Tx, periodID string) ([]AllocTransfer, error) {
	rows, err := tx.Query(ctx, transferSelect+` where ($1 = '' or t.period_id::text = $1) order by t.created_at desc limit 500`, periodID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AllocTransfer, error) {
		var t AllocTransfer
		var from, to *string
		err := r.Scan(&t.ID, &t.PeriodID, &t.FromID, &t.ToID, &from, &to, &t.AmountCents, &t.Reason, &t.Status, &t.ApprovalKind, &t.ApproverID,
			&t.ApprovalID, &t.CreatedBy, &t.CreatedAt, &t.DecidedAt, &t.DecisionNote)
		t.From, t.To = deref(from), deref(to)
		return t, err
	})
	if out == nil {
		out = []AllocTransfer{}
	}
	return out, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type AllocTransferInput struct {
	FromID      string `json:"from_allocation_id"`
	ToID        string `json:"to_allocation_id"`
	AmountCents int64  `json:"amount_cents"`
	Reason      string `json:"reason"`
}

// ancestors: il nodo e i suoi antenati, dal basso.
func ancestors(n *Node, byID map[string]*Node) []*Node {
	var out []*Node
	for x := n; x != nil; {
		out = append(out, x)
		if x.ParentID == nil {
			break
		}
		x = byID[*x.ParentID]
	}
	return out
}

// approverOf: il responsabile del primo nodo, dal basso, che ne ha uno (responsabile
// indicato sull'unità, altrimenti la persona con ruolo «responsabile» nell'unità).
func (s *Service) approverOf(ctx context.Context, tx pgx.Tx, chain []*Node) (string, error) {
	for _, n := range chain {
		if n.OrgUnitID == nil {
			continue
		}
		var m *string
		if err := tx.QueryRow(ctx, `select coalesce(u.manager_id::text, (select x.member_id::text from org_unit_members x
			where x.org_unit_id = u.id and x.role = 'manager' order by x.created_at, x.member_id limit 1))
			from org_units u where u.id::text = $1`, *n.OrgUnitID).Scan(&m); err != nil {
			return "", err
		}
		if m != nil {
			return *m, nil
		}
	}
	return "", nil
}

// anchorElement: l'elemento a cui si aggancia la richiesta di approvazione di un trasferimento
// (il motore delle approvazioni lavora su elementi): il primo nodo di elemento tra i due,
// altrimenti un portafoglio, altrimenti una richiesta.
func (s *Service) anchorElement(ctx context.Context, tx pgx.Tx, from, to *Node) (*engine.Element, error) {
	var id, typeID, typeKey string
	for _, n := range []*Node{from, to} {
		if n.ElementID != nil {
			err := tx.QueryRow(ctx, `select e.id, e.type_id, t.key from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
				where e.id::text = $1`, *n.ElementID).Scan(&id, &typeID, &typeKey)
			if err == nil {
				return &engine.Element{ID: id, TypeID: typeID, TypeKey: typeKey}, nil
			}
		}
	}
	err := tx.QueryRow(ctx, `select e.id, e.type_id, t.key from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		where t.key in ('portfolio', 'business_request') and e.deleted_at is null and not e.is_draft
		order by (t.key = 'portfolio') desc, e.created_at limit 1`).Scan(&id, &typeID, &typeKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.InvalidCode("validation.finance.approval_no_anchor", "approval", "non c'è un elemento a cui agganciare l'approvazione: crea un portafoglio o una richiesta")
	}
	if err != nil {
		return nil, err
	}
	return &engine.Element{ID: id, TypeID: typeID, TypeKey: typeKey}, nil
}

// CreateAllocTransfer chiede il trasferimento di un importo da un nodo a un altro dello stesso
// periodo. Lo approva il responsabile del nodo comune più vicino (subito, se è chi lo chiede e
// la ripartizione libera è attiva); sopra la soglia del tenant lo approva l'organo indicato.
func (s *Service) CreateAllocTransfer(ctx context.Context, tx pgx.Tx, a engine.Actor, in AllocTransferInput) (*AllocTransfer, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		return nil, engine.Required("reason", "serve una motivazione")
	}
	if in.AmountCents <= 0 {
		return nil, engine.InvalidCode("validation.finance.amount_not_positive", "amount_cents", "l'importo deve essere positivo")
	}
	if in.FromID == in.ToID {
		return nil, engine.InvalidCode("validation.finance.transfer_node_same", "to_allocation_id", "il nodo di destinazione deve essere diverso")
	}
	var periodID, toPeriod string
	if err := tx.QueryRow(ctx, "select period_id from budget_allocations where id::text = $1", in.FromID).Scan(&periodID); err != nil {
		return nil, engine.NotFound("from_allocation_id", "", "nodo di partenza inesistente")
	}
	if err := tx.QueryRow(ctx, "select period_id from budget_allocations where id::text = $1", in.ToID).Scan(&toPeriod); err != nil {
		return nil, engine.NotFound("to_allocation_id", "", "nodo di destinazione inesistente")
	}
	if periodID != toPeriod {
		return nil, engine.InvalidCode("validation.finance.transfer_nodes_same_period", "to_allocation_id", "i nodi devono essere dello stesso periodo")
	}
	p, err := s.writablePeriod(ctx, tx, a, periodID)
	if err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx, tx, a)
	if err != nil {
		return nil, err
	}
	nodes, err := s.loadNodes(ctx, tx, periodID)
	if err != nil {
		return nil, err
	}
	if _, err := s.computeNumbers(ctx, tx, p, nodes); err != nil {
		return nil, err
	}
	byID := map[string]*Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	from, to := byID[in.FromID], byID[in.ToID]
	if from == nil || to == nil {
		return nil, engine.InvalidCode("validation.finance.transfer_nodes_not_visible", "to_allocation_id", "i due nodi devono essere visibili a chi chiede il trasferimento: per spostare denaro tra aree diverse lo chiede la finanza")
	}
	if from.Kind == "root" || to.Kind == "root" {
		return nil, engine.InvalidCode("validation.finance.transfer_root", "from_allocation_id", "la radice non si trasferisce")
	}
	fromChain, toChain := ancestors(from, byID), ancestors(to, byID)
	inTo := map[string]bool{}
	for _, n := range toChain {
		inTo[n.ID] = true
	}
	var lca *Node
	for _, n := range fromChain {
		if inTo[n.ID] {
			lca = n
			break
		}
	}
	if lca == nil {
		return nil, engine.InvalidCode("validation.finance.transfer_no_common_node", "to_allocation_id", "i nodi non hanno un nodo in comune")
	}
	if lca.ID == from.ID || lca.ID == to.ID {
		return nil, engine.InvalidCode("validation.finance.transfer_ancestor", "to_allocation_id", "un nodo e quelli sotto di lui non si scambiano denaro con un trasferimento: usa la ripartizione")
	}
	if !sc.finance {
		managed := false
		for _, n := range fromChain {
			if n.OrgUnitID != nil && sc.managed[*n.OrgUnitID] {
				managed = true
			}
		}
		if !managed {
			return nil, engine.ErrForbidden
		}
	}
	var pending int64
	if err := tx.QueryRow(ctx, "select coalesce(sum(amount_cents), 0) from allocation_transfers where from_allocation_id = $1 and status = 'pending'", from.ID).Scan(&pending); err != nil {
		return nil, err
	}
	avail := min(from.ReserveCents, from.ResidualCents) - pending
	if in.AmountCents > avail {
		return nil, engine.InvalidParams("validation.finance.transfer_available", "amount_cents", map[string]any{"label": from.Label, "amount": money(max(avail, 0))}, "«%s» ha disponibili %s (riserva e residuo, meno i trasferimenti in attesa)", from.Label, money(max(avail, 0)))
	}
	st, err := GetBudgetSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	id := db.NewID()
	kind, approver := "none", ""
	var rule *approval.Rule
	switch {
	case st.TransferThresholdCents != nil && in.AmountCents > *st.TransferThresholdCents:
		kind = "body"
		rule = &approval.Rule{Steps: []approval.Step{{Approvers: []approval.Approver{{Body: st.TransferBodyKind}}}}}
	default:
		approver, err = s.approverOf(ctx, tx, lca.chainFrom(byID))
		if err != nil {
			return nil, err
		}
		switch {
		case approver == "":
			if !sc.finance {
				return nil, engine.InvalidCode("validation.finance.transfer_no_approver", "approval", "nessun responsabile può approvare il trasferimento: chiedilo alla finanza")
			}
		case approver == a.MemberID:
			if !sc.free && !sc.finance {
				return nil, engine.InvalidCode("validation.finance.free_allocation_off", "approval", "la ripartizione libera è disattivata: serve l'approvazione della finanza")
			}
			approver = ""
		default:
			kind = "manager"
			rule = &approval.Rule{Steps: []approval.Step{{Approvers: []approval.Approver{{Member: approver}}}}}
		}
	}
	if _, err := tx.Exec(ctx, `insert into allocation_transfers (tenant_id, id, period_id, from_allocation_id, to_allocation_id, amount_cents, reason, approval_kind, approver_member_id, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, periodID, from.ID, to.ID, in.AmountCents, in.Reason, kind, nullable(approver), nullable(a.MemberID)); err != nil {
		return nil, err
	}
	payload := map[string]any{"from": from.ID, "to": to.ID, "amount_cents": in.AmountCents, "approval_kind": kind}
	if rule != nil {
		anchor, err := s.anchorElement(ctx, tx, from, to)
		if err != nil {
			return nil, err
		}
		reqID, err := s.ap.OpenSubjectRule(ctx, tx, a, anchor, subjectTrans, "Trasferimento di stanziamento", in.AmountCents, *rule)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, "update allocation_transfers set approval_request_id = $2 where id = $1", id, reqID); err != nil {
			return nil, err
		}
		payload["approval_request_id"] = reqID
	}
	if err := s.aud(ctx, tx, a, "budget.alloc_transfer", "allocation_transfer", id, payload); err != nil {
		return nil, err
	}
	if rule == nil {
		if err := s.applyAllocTransfer(ctx, tx, a, id, "approved", ""); err != nil {
			return nil, err
		}
	}
	return s.allocTransfer(ctx, tx, id)
}

// chainFrom: il nodo e i suoi antenati (per cercare il responsabile dal basso).
func (n *Node) chainFrom(byID map[string]*Node) []*Node { return ancestors(n, byID) }

func (s *Service) allocTransfer(ctx context.Context, tx pgx.Tx, id string) (*AllocTransfer, error) {
	all, err := s.AllocTransfers(ctx, tx, "")
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, engine.ErrNotFound
}

// applyAllocTransfer registra l'esito: se approvato scrive i movimenti sul percorso tra i due
// nodi fino al nodo comune (da una parte -X, dall'altra +X), così nessun nodo di mezzo viola la
// regola «figli entro il padre». Gira con la visibilità della finanza: chi decide può essere
// un organo o un responsabile che non vede tutti i nodi.
func (s *Service) applyAllocTransfer(ctx context.Context, tx pgx.Tx, a engine.Actor, id, outcome, note string) error {
	return withFinance(ctx, tx, func() error {
		var periodID, fromID, toID, status string
		var amount int64
		var reqID *string
		if err := tx.QueryRow(ctx, "select period_id, from_allocation_id, to_allocation_id, amount_cents, status, approval_request_id from allocation_transfers where id = $1 for update", id).
			Scan(&periodID, &fromID, &toID, &amount, &status, &reqID); err != nil {
			return err
		}
		if status != "pending" {
			return nil
		}
		newStatus := "rejected"
		if outcome == "approved" || outcome == "approved_with_conditions" {
			newStatus = "approved"
		}
		if newStatus == "approved" {
			p, err := s.lockPeriod(ctx, tx, periodID)
			if err != nil {
				return err
			}
			if p.Status == "closed" {
				return engine.InvalidCode("validation.finance.period_closed", "period_id", "il periodo è chiuso")
			}
			nodes, err := s.loadNodes(ctx, tx, periodID)
			if err != nil {
				return err
			}
			if _, err := s.computeNumbers(ctx, tx, p, nodes); err != nil {
				return err
			}
			byID := map[string]*Node{}
			for _, n := range nodes {
				byID[n.ID] = n
			}
			from, to := byID[fromID], byID[toID]
			if from == nil || to == nil {
				return engine.NotFound("transfer", "", "i nodi del trasferimento non esistono più")
			}
			toSet := map[string]bool{}
			for _, n := range ancestors(to, byID) {
				toSet[n.ID] = true
			}
			batch := db.NewID()
			var touched []string
			for _, n := range ancestors(from, byID) {
				if toSet[n.ID] {
					break
				}
				if err := s.addMove(ctx, tx, a, periodID, move{alloc: n.ID, counterpart: toID, kind: MoveTransfer, amount: -amount, batch: batch, transfer: id,
					approval: deref(reqID), reason: "Trasferimento verso " + to.Label}); err != nil {
					return err
				}
				touched = append(touched, n.ID)
			}
			fromSet := map[string]bool{}
			for _, n := range ancestors(from, byID) {
				fromSet[n.ID] = true
			}
			for _, n := range ancestors(to, byID) {
				if fromSet[n.ID] {
					break
				}
				if err := s.addMove(ctx, tx, a, periodID, move{alloc: n.ID, counterpart: fromID, kind: MoveTransfer, amount: amount, batch: batch, transfer: id,
					approval: deref(reqID), reason: "Trasferimento da " + from.Label}); err != nil {
					return err
				}
				touched = append(touched, n.ID)
			}
			if err := s.verify(ctx, tx, touched...); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `update allocation_transfers set status = $2, decided_by = $3, decided_at = now(), decision_note = $4 where id = $1`,
			id, newStatus, nullable(a.MemberID), nullable(note)); err != nil {
			return err
		}
		return s.aud(ctx, tx, a, "budget.alloc_transfer."+newStatus, "allocation_transfer", id, map[string]any{"outcome": outcome})
	})
}

// closeAllocTransfer applica l'esito di una richiesta di approvazione di un trasferimento di
// stanziamento. Restituisce false se la richiesta non è di questo tipo.
func (s *Service) closeAllocTransfer(ctx context.Context, tx pgx.Tx, actor engine.Actor, reqID, outcome, comment string) (bool, error) {
	var id string
	// Chi decide (un organo, un responsabile) può non vedere i nodi: la richiesta si cerca con la visibilità della finanza.
	err := withFinance(ctx, tx, func() error {
		return tx.QueryRow(ctx, "select id from allocation_transfers where approval_request_id = $1 and status = 'pending' for update", reqID).Scan(&id)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, s.applyAllocTransfer(ctx, tx, actor, id, outcome, comment)
}
