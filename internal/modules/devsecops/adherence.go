package devsecops

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/architecture"
	"regia/internal/modules/devsecops/contract"
)

// Aderenza alla politica (replica/devsecops.md §3.3, fase 3): per ogni servizio, i controlli
// che la pipeline riporta davvero (ultimo check_run del branch predefinito) confrontati con quelli che la
// politica richiede per la classe dell'applicazione. Niente viene letto dal repository:
// conta ciò che la pipeline ha dichiarato di eseguire e in quale modalità.
//
// Stato di un servizio:
//   - aderente: ogni controllo richiesto gira in una modalità almeno forte quanto richiesto;
//   - parziale: alcuni girano, gli altri non sono riportati;
//   - fuori politica: un controllo richiesto gira in modalità più debole (o spento), oppure
//     nessuno di quelli richiesti gira;
//   - non in pipeline: nessun esito dei controlli è mai arrivato.

const (
	StAdherent = "aderente"
	StPartial  = "parziale"
	StOff      = "fuori_politica"
	StNone     = "non_in_pipeline"
)

var statusRank = map[string]int{StAdherent: 0, StPartial: 1, StNone: 2, StOff: 3}

// ControlState è lo stato di un controllo per un servizio.
type ControlState struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Required string `json:"required"`           // off | warn | block
	Observed string `json:"observed,omitempty"` // modalità riportata dalla pipeline; vuoto = non riportato
	Status   string `json:"status"`             // ok | missing | weaker | na
}

// evaluate confronta i controlli riportati con quelli richiesti dalla classe.
func evaluate(def *PolicyDef, class *Class, checks []contract.CheckResult, hasRun bool) (string, []ControlState) {
	idx := def.controlIndex()
	seen := map[string]string{} // controllo → modalità più forte tra quelle riportate
	for _, c := range checks {
		k, ok := idx[strings.ToLower(c.Name)]
		if !ok || c.Outcome == "not_run" {
			continue
		}
		m := c.Mode
		if _, ok := modeRank[m]; !ok {
			m = "warn"
		}
		if cur, ok := seen[k]; !ok || modeRank[m] > modeRank[cur] {
			seen[k] = m
		}
	}
	states := make([]ControlState, 0, len(def.Controls))
	required, met, weaker := 0, 0, 0
	for _, c := range def.Controls {
		st := ControlState{Key: c.Key, Name: c.Name, Required: orOff(class.Modes[c.Key]), Observed: seen[c.Key]}
		switch {
		case st.Required == "off":
			st.Status = "na"
		case hasRun && st.Observed != "" && modeRank[st.Observed] >= modeRank[st.Required]:
			st.Status = "ok"
			required, met = required+1, met+1
		case hasRun && st.Observed != "":
			st.Status = "weaker"
			required, weaker = required+1, weaker+1
		default:
			st.Status = "missing"
			required++
		}
		states = append(states, st)
	}
	switch {
	case !hasRun:
		return StNone, states
	case met == required:
		return StAdherent, states
	case weaker > 0 || met == 0:
		return StOff, states
	}
	return StPartial, states
}

// ServiceAdherence è l'aderenza di un servizio.
type ServiceAdherence struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Repository *string        `json:"repository"`
	Status     string         `json:"status"`
	Controls   []ControlState `json:"controls"`
	Missing    []string       `json:"missing"` // nomi dei controlli richiesti e non rispettati
	LastRun    *time.Time     `json:"last_run"`
	Commit     string         `json:"commit,omitempty"`
}

// AppAdherence è l'aderenza di un'applicazione: lo stato peggiore dei suoi servizi.
type AppAdherence struct {
	ID               string             `json:"id"`
	Key              string             `json:"key"`
	Name             string             `json:"name"`
	Code             *string            `json:"code"`
	Class            string             `json:"class"`
	ClassKey         string             `json:"class_key"`
	Criticality      string             `json:"criticality"`
	CriticalFunction bool               `json:"supports_critical_function"`
	Status           string             `json:"status"`
	Missing          []string           `json:"missing"` // chiavi dei controlli mancanti su almeno un servizio
	Services         []ServiceAdherence `json:"services"`
	Required         []ControlState     `json:"required"` // i controlli richiesti dalla classe, per leggerli senza un servizio
}

type fleetSvc struct {
	ID, Name   string
	Repository *string
	Checks     []contract.CheckResult
	HasRun     bool
	At         *time.Time
	Commit     string
}

type fleetApp struct {
	ID, Key, Name string
	Code          *string
	Attrs         AppAttrs
	Services      []fleetSvc
}

// loadFleet legge le applicazioni (tutte, o quelle indicate) con i servizi e l'ultimo
// check_run di ciascuno. Restituisce anche i servizi senza applicazione.
func loadFleet(ctx context.Context, tx pgx.Tx, exposureField string, only []string) ([]*fleetApp, []fleetSvc, error) {
	q := `select e.id::text, sp.key || '-' || e.number, e.title, a.code, coalesce(a.criticality, ''), a.supports_critical_function,
		coalesce(a.data_classification, ''), coalesce(e.fields ->> $1, '')
		from elements e join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		where e.deleted_at is null and coalesce(a.lifecycle, '') <> 'retired'`
	args := []any{exposureField}
	if only != nil {
		q += " and e.id = any($2)"
		args = append(args, only)
	}
	rows, err := tx.Query(ctx, q+" order by e.title limit 3000", args...)
	if err != nil {
		return nil, nil, err
	}
	var apps []*fleetApp
	byID := map[string]*fleetApp{}
	for rows.Next() {
		a := &fleetApp{}
		if err := rows.Scan(&a.ID, &a.Key, &a.Name, &a.Code, &a.Attrs.Criticality, &a.Attrs.CriticalFunction, &a.Attrs.Data, &a.Attrs.Exposure); err != nil {
			rows.Close()
			return nil, nil, err
		}
		apps = append(apps, a)
		byID[a.ID] = a
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	// Conta solo l'ultimo check_run del branch predefinito del repository (quello del
	// connettore, associato al servizio per nome o per repository). Se il branch non è noto,
	// solo i check_run dello stesso commit di un rilascio del servizio: una pipeline di una
	// pull request o di un branch di lavoro non cambia lo stato del servizio.
	sq := `select s.id::text, s.name, s.repository, s.application_element_id::text, cr.at, cr.commit, cr.checks
		from services s
		left join lateral (select r.default_branch from connector_repositories r
		                   where r.mapping_status = 'confirmed' and (lower(r.service_name) = lower(s.name) or lower(r.full_name) = lower(coalesce(s.repository, '')))
		                   order by r.last_synced_at desc nulls last limit 1) db on true
		left join lateral (select c.at, c.commit, c.checks from check_runs c
		                   where c.service_id = s.id
		                     and (case when db.default_branch is not null
		                               then regexp_replace(coalesce(c.ref, ''), '^refs/heads/', '') = db.default_branch
		                               else exists (select 1 from releases rl where rl.service_id = c.service_id and rl.commit = c.commit) end)
		                   order by c.at desc limit 1) cr on true`
	var sargs []any
	if only != nil {
		sq += " where s.application_element_id = any($1)"
		sargs = append(sargs, only)
	}
	srows, err := tx.Query(ctx, sq+" order by s.name limit 6000", sargs...)
	if err != nil {
		return nil, nil, err
	}
	var orphans []fleetSvc
	for srows.Next() {
		var sv fleetSvc
		var appID, commit *string
		var raw []byte
		if err := srows.Scan(&sv.ID, &sv.Name, &sv.Repository, &appID, &sv.At, &commit, &raw); err != nil {
			srows.Close()
			return nil, nil, err
		}
		if commit != nil {
			sv.Commit = *commit
		}
		if sv.At != nil {
			sv.HasRun = true
			if err := json.Unmarshal(raw, &sv.Checks); err != nil {
				srows.Close()
				return nil, nil, err
			}
		}
		if appID != nil && byID[*appID] != nil {
			byID[*appID].Services = append(byID[*appID].Services, sv)
		} else if appID == nil {
			orphans = append(orphans, sv)
		}
	}
	return apps, orphans, srows.Err()
}

func evalService(def *PolicyDef, class *Class, sv fleetSvc) ServiceAdherence {
	st, controls := evaluate(def, class, sv.Checks, sv.HasRun)
	out := ServiceAdherence{ID: sv.ID, Name: sv.Name, Repository: sv.Repository, Status: st, Controls: controls, Missing: []string{}, LastRun: sv.At, Commit: sv.Commit}
	for _, c := range controls {
		if c.Status == "missing" || c.Status == "weaker" {
			out.Missing = append(out.Missing, c.Name)
		}
	}
	return out
}

// aggregate è lo stato di un'applicazione dai suoi servizi.
func aggregate(svcs []ServiceAdherence) string {
	if len(svcs) == 0 {
		return StNone
	}
	worst, none := StAdherent, 0
	for _, s := range svcs {
		if s.Status == StNone {
			none++
			continue
		}
		if statusRank[s.Status] > statusRank[worst] {
			worst = s.Status
		}
	}
	switch {
	case none == len(svcs):
		return StNone
	case none > 0 && worst == StAdherent:
		return StPartial
	}
	return worst
}

func evalApp(def *PolicyDef, a *fleetApp) AppAdherence {
	class := def.ClassOf(a.Attrs)
	out := AppAdherence{ID: a.ID, Key: a.Key, Name: a.Name, Code: a.Code, Class: class.Name, ClassKey: class.Key,
		Criticality: a.Attrs.Criticality, CriticalFunction: a.Attrs.CriticalFunction, Missing: []string{}, Services: []ServiceAdherence{}}
	for _, sv := range a.Services {
		out.Services = append(out.Services, evalService(def, class, sv))
	}
	out.Status = aggregate(out.Services)
	_, out.Required = evaluate(def, class, nil, false)
	out.Required = slices.DeleteFunc(out.Required, func(c ControlState) bool { return c.Required == "off" })
	for _, s := range out.Services {
		for _, c := range s.Controls {
			if (c.Status == "missing" || c.Status == "weaker") && !slices.Contains(out.Missing, c.Key) {
				out.Missing = append(out.Missing, c.Key)
			}
		}
	}
	if len(out.Services) == 0 {
		for _, c := range out.Required {
			out.Missing = append(out.Missing, c.Key)
		}
	}
	return out
}

// Counts conta gli elementi per stato.
type Counts struct {
	Total         int `json:"total"`
	Adherent      int `json:"aderente"`
	Partial       int `json:"parziale"`
	OutOfPolicy   int `json:"fuori_politica"`
	NotOnPipeline int `json:"non_in_pipeline"`
}

func (c *Counts) add(st string) {
	c.Total++
	switch st {
	case StAdherent:
		c.Adherent++
	case StPartial:
		c.Partial++
	case StOff:
		c.OutOfPolicy++
	case StNone:
		c.NotOnPipeline++
	}
}

// ControlGap è quanti servizi non rispettano un controllo.
type ControlGap struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Missing int    `json:"missing"` // richiesto e non riportato
	Weaker  int    `json:"weaker"`  // riportato in una modalità più debole
	Ok      int    `json:"ok"`
}

// AssetAdherence è il riepilogo di un asset logico.
type AssetAdherence struct {
	ID           string `json:"id"`
	Key          string `json:"key"`
	Name         string `json:"name"`
	Applications Counts `json:"applications"`
}

// AdherenceOverview è la vista di portafoglio.
type AdherenceOverview struct {
	Configured   bool             `json:"configured"`
	Version      int              `json:"version"`
	Apps         Counts           `json:"apps"`
	Services     Counts           `json:"services"`
	Orphans      int              `json:"unassigned_services"`
	Gaps         []ControlGap     `json:"gaps"`
	Assets       []AssetAdherence `json:"assets"`
	Applications []AppAdherence   `json:"applications"`
	Truncated    bool             `json:"truncated"`
}

const overviewApps = 400

// Adherence calcola l'aderenza del portafoglio.
func (s *Service) Adherence(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*AdherenceOverview, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	o := &AdherenceOverview{Gaps: []ControlGap{}, Assets: []AssetAdherence{}, Applications: []AppAdherence{}}
	def, v, err := LoadPolicy(ctx, tx)
	if err != nil || def == nil {
		return o, err
	}
	o.Configured, o.Version = true, v
	apps, orphans, err := loadFleet(ctx, tx, def.ExposureField, nil)
	if err != nil {
		return nil, err
	}
	o.Orphans = len(orphans)
	gaps := map[string]*ControlGap{}
	for _, c := range def.Controls {
		gaps[c.Key] = &ControlGap{Key: c.Key, Name: c.Name}
	}
	statusOf := map[string]string{}
	var all []AppAdherence
	for _, a := range apps {
		ev := evalApp(def, a)
		all = append(all, ev)
		statusOf[a.ID] = ev.Status
		o.Apps.add(ev.Status)
		for _, sv := range ev.Services {
			o.Services.add(sv.Status)
			for _, c := range sv.Controls {
				g := gaps[c.Key]
				switch c.Status {
				case "ok":
					g.Ok++
				case "missing":
					g.Missing++
				case "weaker":
					g.Weaker++
				}
			}
		}
	}
	for _, c := range def.Controls {
		o.Gaps = append(o.Gaps, *gaps[c.Key])
	}
	// I peggiori per primi; a parità, per nome.
	slices.SortStableFunc(all, func(a, b AppAdherence) int {
		if d := statusRank[b.Status] - statusRank[a.Status]; d != 0 {
			return d
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	if len(all) > overviewApps {
		all, o.Truncated = all[:overviewApps], true
	}
	o.Applications = all
	// Per asset: le applicazioni che l'asset compone o usa.
	rows, err := tx.Query(ctx, `select e.id::text, s.key || '-' || e.number, e.title,
		coalesce(array_agg(r.target_id::text) filter (where r.target_id is not null), '{}')
		from elements e join ext_logical_asset x on x.tenant_id = e.tenant_id and x.element_id = e.id
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join relation_types rt on rt.tenant_id = e.tenant_id and rt.key in ('`+architecture.RelComposedOf+`', '`+architecture.RelUses+`')
		left join relations r on r.tenant_id = e.tenant_id and r.source_id = e.id and r.type_id = rt.id
		where e.deleted_at is null group by 1, 2, 3 order by e.title limit 500`)
	if err != nil {
		return nil, err
	}
	var a AssetAdherence
	var ids []string
	if _, err := pgx.ForEachRow(rows, []any{&a.ID, &a.Key, &a.Name, &ids}, func() error {
		x := a
		for _, id := range ids {
			if st, ok := statusOf[id]; ok {
				x.Applications.add(st)
			}
		}
		if x.Applications.Total > 0 {
			o.Assets = append(o.Assets, x)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return o, nil
}

// ---------------------------------------------------------------------------
// Anteprima dell'impatto di una politica

// Transition conta i passaggi da uno stato a un altro.
type Transition struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Apps     int    `json:"apps"`
	Services int    `json:"services"`
}

// ImpactItem è un'applicazione o un servizio che cambia stato.
type ImpactItem struct {
	Kind string `json:"kind"` // application | service
	ID   string `json:"id"`
	Key  string `json:"key,omitempty"`
	Name string `json:"name"`
	From string `json:"from"`
	To   string `json:"to"`
	Of   string `json:"of,omitempty"` // per un servizio, l'applicazione
}

// Impact è quanto cambia salvando una politica.
type Impact struct {
	BeforeConfigured bool            `json:"before_configured"`
	Before           ImpactSide      `json:"before"`
	After            ImpactSide      `json:"after"`
	ChangedApps      int             `json:"changed_apps"`
	ChangedServices  int             `json:"changed_services"`
	Transitions      []Transition    `json:"transitions"`
	Items            []ImpactItem    `json:"items"`
	Changes          []Change        `json:"changes"`
	Problems         []PolicyProblem `json:"problems"`
}

// ImpactSide è la distribuzione degli stati prima o dopo.
type ImpactSide struct {
	Apps     Counts `json:"apps"`
	Services Counts `json:"services"`
}

const impactItems = 80

// PolicyImpact confronta la politica salvata con una bozza: quante applicazioni e servizi
// cambiano stato, senza salvare niente.
func (s *Service) PolicyImpact(ctx context.Context, tx pgx.Tx, actor engine.Actor, draft PolicyDef) (*Impact, error) {
	if !actor.Has(PermRead) && !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	im := &Impact{Transitions: []Transition{}, Items: []ImpactItem{}, Changes: []Change{}, Problems: []PolicyProblem{}}
	if ps := draft.Normalize(); len(ps) > 0 {
		im.Problems = ps
		return im, nil
	}
	cur, _, err := LoadPolicy(ctx, tx)
	if err != nil {
		return nil, err
	}
	im.BeforeConfigured = cur != nil
	im.Changes = DiffPolicy(cur, &draft)
	apps, _, err := loadFleet(ctx, tx, draft.ExposureField, nil)
	if err != nil {
		return nil, err
	}
	// La classe dipende dal campo dell'esposizione: se cambia, la politica salvata si valuta
	// con i propri dati. Il caso comune (stesso campo) usa la stessa lettura.
	var apps0 []*fleetApp
	if cur != nil {
		if cur.ExposureField == draft.ExposureField {
			apps0 = apps
		} else if apps0, _, err = loadFleet(ctx, tx, cur.ExposureField, nil); err != nil {
			return nil, err
		}
	}
	before := map[string]AppAdherence{}
	for _, a := range apps0 {
		before[a.ID] = evalApp(cur, a)
	}
	trans := map[[2]string]*Transition{}
	bump := func(from, to string, app bool) {
		t := trans[[2]string{from, to}]
		if t == nil {
			t = &Transition{From: from, To: to}
			trans[[2]string{from, to}] = t
		}
		if app {
			t.Apps++
		} else {
			t.Services++
		}
	}
	for _, a := range apps {
		af := evalApp(&draft, a)
		im.After.Apps.add(af.Status)
		bf, had := before[a.ID]
		if had {
			im.Before.Apps.add(bf.Status)
		}
		from := ""
		if had {
			from = bf.Status
		}
		if had && bf.Status != af.Status {
			im.ChangedApps++
			bump(from, af.Status, true)
			if len(im.Items) < impactItems {
				im.Items = append(im.Items, ImpactItem{Kind: "application", ID: a.ID, Key: a.Key, Name: a.Name, From: from, To: af.Status})
			}
		}
		bsv := map[string]ServiceAdherence{}
		for _, sv := range bf.Services {
			bsv[sv.ID] = sv
		}
		for _, sv := range af.Services {
			im.After.Services.add(sv.Status)
			b, ok := bsv[sv.ID]
			if ok {
				im.Before.Services.add(b.Status)
			}
			if ok && b.Status != sv.Status {
				im.ChangedServices++
				bump(b.Status, sv.Status, false)
				if len(im.Items) < impactItems {
					im.Items = append(im.Items, ImpactItem{Kind: "service", ID: sv.ID, Name: sv.Name, From: b.Status, To: sv.Status, Of: a.Key})
				}
			}
		}
	}
	for _, t := range trans {
		im.Transitions = append(im.Transitions, *t)
	}
	slices.SortFunc(im.Transitions, func(a, b Transition) int {
		return strings.Compare(a.From+">"+a.To, b.From+">"+b.To)
	})
	return im, nil
}

// ---------------------------------------------------------------------------
// Fatti per le condizioni dei processi

func (s *Service) registerPolicyFacts(eng *engine.Service) {
	eng.Facts(architecture.AppType, s.policyFacts)
	eng.DescribeFacts(architecture.AppType,
		engine.FactInfo{Key: "aderenza_pipeline", Name: "Aderenza della pipeline alla politica", Kind: "text",
			Values: []string{StAdherent, StPartial, StOff, StNone}, Example: StAdherent},
		engine.FactInfo{Key: "controlli_mancanti", Name: "Controlli della politica non rispettati", Kind: "number", Example: "2"},
		engine.FactInfo{Key: "controlli_mancanti_elenco", Name: "Controlli della politica non rispettati (chiavi)", Kind: "text", Example: "secrets"},
	)
}

// policyFacts: senza una politica salvata i fatti non esistono (le condizioni non li vedono).
func (s *Service) policyFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	def, _, err := LoadPolicy(ctx, tx)
	if err != nil || def == nil {
		return err
	}
	apps, _, err := loadFleet(ctx, tx, def.ExposureField, []string{el.ID})
	if err != nil || len(apps) == 0 {
		return err
	}
	ev := evalApp(def, apps[0])
	f.Set("aderenza_pipeline", ev.Status)
	f.Set("controlli_mancanti", len(ev.Missing))
	if len(ev.Missing) > 0 {
		f.Set("controlli_mancanti_elenco", toAny(ev.Missing)...)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Pannello della pipeline di un'applicazione: «cosa manca»

// ChecklistItem è una voce dell'elenco guidato.
type ChecklistItem struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	State  string `json:"state"` // ok | todo | info
	Detail string `json:"detail,omitempty"`
	Action string `json:"action,omitempty"` // policy | repository | propose | github | template
}

// AppPipeline è la scheda «Pipeline» di un'applicazione.
type AppPipeline struct {
	Configured  bool            `json:"configured"`
	Version     int             `json:"version"`
	Application AppAdherence    `json:"application"`
	Items       []ChecklistItem `json:"items"`
	CanConfig   bool            `json:"can_configure"`
}

// PipelinePanel compone la scheda della pipeline per un'applicazione (chiave o id) o, per un
// asset logico, per le sue applicazioni.
type PipelineSubject struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// PipelineView è la risposta: una o più applicazioni con l'elenco guidato per ciascuna.
type PipelineView struct {
	Subject      PipelineSubject `json:"subject"`
	Configured   bool            `json:"configured"`
	Version      int             `json:"version"`
	Applications []AppPipeline   `json:"applications"`
	CanConfigure bool            `json:"can_configure"`
}

func (s *Service) PipelineOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*PipelineView, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	id, key, title, typ, err := elementRef(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	var appIDs []string
	switch typ {
	case architecture.AssetType:
		if appIDs, err = assetApps(ctx, tx, id); err != nil {
			return nil, err
		}
	case architecture.AppType:
		appIDs = []string{id}
	default:
		return nil, engine.InvalidCode("validation.devsecops.pipeline_ref_scope", "ref", "la pipeline è di un asset logico o di un'applicazione")
	}
	v := &PipelineView{Subject: PipelineSubject{ID: id, Key: key, Title: title, Type: typ}, Applications: []AppPipeline{}, CanConfigure: CanConfigure(actor)}
	def, ver, err := LoadPolicy(ctx, tx)
	if err != nil {
		return nil, err
	}
	v.Configured, v.Version = def != nil, ver
	exposure := ""
	if def != nil {
		exposure = def.ExposureField
	}
	if len(appIDs) == 0 {
		return v, nil
	}
	apps, _, err := loadFleet(ctx, tx, exposure, appIDs)
	if err != nil {
		return nil, err
	}
	for _, a := range apps {
		ap := AppPipeline{Configured: def != nil, Version: ver, CanConfig: v.CanConfigure}
		if def != nil {
			ap.Application = evalApp(def, a)
		} else {
			ap.Application = AppAdherence{ID: a.ID, Key: a.Key, Name: a.Name, Code: a.Code, Services: []ServiceAdherence{}, Missing: []string{}, Required: []ControlState{}}
		}
		if ap.Items, err = s.checklist(ctx, tx, def, ver, a, ap.Application); err != nil {
			return nil, err
		}
		v.Applications = append(v.Applications, ap)
	}
	return v, nil
}

func (s *Service) checklist(ctx context.Context, tx pgx.Tx, def *PolicyDef, ver int, a *fleetApp, ev AppAdherence) ([]ChecklistItem, error) {
	var items []ChecklistItem
	add := func(key, label, state, detail, action string) {
		items = append(items, ChecklistItem{Key: key, Label: label, State: state, Detail: detail, Action: action})
	}
	if def == nil {
		add("politica", "La politica della pipeline è definita", "todo", "Nessuna politica salvata: non si sa cosa richiedere a questa applicazione.", "policy")
	} else {
		add("politica", "La politica della pipeline è definita", "ok", fmt.Sprintf("Classe «%s», versione %d della politica.", ev.Class, ver), "")
	}
	if len(a.Services) == 0 {
		add("repository", "Un repository è associato all'applicazione", "todo", "Nessun servizio o repository collegato: la pipeline non può essere letta.", "repository")
	} else {
		add("repository", "Un repository è associato all'applicazione", "ok", plural(len(a.Services), "servizio", "servizi")+" collegati.", "")
	}
	runs := 0
	for _, sv := range a.Services {
		if sv.HasRun {
			runs++
		}
	}
	switch {
	case len(a.Services) == 0:
	case runs == 0:
		add("pipeline", "La pipeline invia gli esiti dei controlli", "todo", "Nessun esito ricevuto: il repository non ha ancora una pipeline che lo dichiari.", "propose")
	default:
		add("pipeline", "La pipeline invia gli esiti dei controlli", "ok", fmt.Sprintf("%d servizi su %d hanno inviato almeno un esito.", runs, len(a.Services)), "")
	}
	if def != nil && runs > 0 {
		if len(ev.Missing) == 0 {
			add("controlli", "I controlli richiesti dalla politica girano", "ok", "Tutti i controlli richiesti sono presenti nella modalità giusta.", "")
		} else {
			var names []string
			for _, c := range ev.Required {
				if slices.Contains(ev.Missing, c.Key) {
					names = append(names, fmt.Sprintf("%s (%s)", c.Name, modeIt[c.Required]))
				}
			}
			add("controlli", "I controlli richiesti dalla politica girano", "todo", "Mancano o sono troppo deboli: "+strings.Join(names, ", ")+".", "propose")
		}
	}
	if len(a.Services) > 0 {
		ids := make([]string, len(a.Services))
		for i, sv := range a.Services {
			ids[i] = sv.ID
		}
		var rel, sbom int
		if err := tx.QueryRow(ctx, `select (select count(*) from releases where service_id = any($1)), (select count(*) from sboms where service_id = any($1))`, ids).Scan(&rel, &sbom); err != nil {
			return nil, err
		}
		add("rilasci", "I rilasci arrivano da una pipeline", map[bool]string{true: "ok", false: "info"}[rel > 0], map[bool]string{true: plural(rel, "rilascio registrato", "rilasci registrati") + ".", false: "Nessun rilascio registrato."}[rel > 0], "")
		add("sbom", "Le SBOM sono allegate ai rilasci", map[bool]string{true: "ok", false: "info"}[sbom > 0], map[bool]string{true: plural(sbom, "SBOM ricevuta", "SBOM ricevute") + ".", false: "Nessuna SBOM ricevuta."}[sbom > 0], "")
	}
	return items, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
