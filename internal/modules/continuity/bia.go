package continuity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/i18n"
)

// Impacts è la griglia della BIA: categoria → orizzonte → livello.
type Impacts map[string]map[string]int

// Proposal è ciò che il sistema propone dalla griglia; la persona conferma o corregge.
type Proposal struct {
	MTPD     *int    `json:"mtpd_minutes"`
	RTO      *int    `json:"rto_minutes"`
	Critical bool    `json:"critical"`
	Category *string `json:"category"` // categoria che raggiunge per prima l'intollerabile
	Horizon  *string `json:"horizon"`
}

// propose: MTPD = primo orizzonte (per durata) in cui una categoria raggiunge il suo livello
// intollerabile; RTO = l'orizzonte precedente (metà del primo, se è il primo); senza
// intollerabile, MTPD assente e RTO = ultimo orizzonte. Critica se l'MTPD sta entro la soglia.
func propose(cfg *Config, imp Impacts) *Proposal {
	if len(imp) == 0 || len(cfg.Horizons) == 0 {
		return nil
	}
	p := &Proposal{}
	for i, h := range cfg.Horizons {
		for _, c := range cfg.Categories {
			lv, ok := imp[c.Key][h.Key]
			if !ok || lv < c.Intolerable {
				continue
			}
			mtpd := h.Minutes
			rto := h.Minutes / 2
			if i > 0 {
				rto = cfg.Horizons[i-1].Minutes
			}
			ck, hk := c.Key, h.Key
			p.MTPD, p.RTO, p.Category, p.Horizon = &mtpd, &rto, &ck, &hk
			p.Critical = mtpd <= cfg.CriticalMTPDMinutes
			return p
		}
	}
	last := cfg.Horizons[len(cfg.Horizons)-1].Minutes
	p.RTO = &last
	return p
}

// complete dice se la griglia ha un livello per ogni categoria e orizzonte.
func complete(cfg *Config, imp Impacts) bool {
	for _, c := range cfg.Categories {
		for _, h := range cfg.Horizons {
			if _, ok := imp[c.Key][h.Key]; !ok {
				return false
			}
		}
	}
	return true
}

func validImpacts(cfg *Config, imp Impacts) error {
	for ck, row := range imp {
		var cat *Category
		for i := range cfg.Categories {
			if cfg.Categories[i].Key == ck {
				cat = &cfg.Categories[i]
			}
		}
		if cat == nil {
			return engine.InvalidParams("validation.continuity.config", "impacts", nil, "categoria %q non configurata", ck)
		}
		for hk, lv := range row {
			okH := false
			for _, h := range cfg.Horizons {
				okH = okH || h.Key == hk
			}
			okL := false
			for _, l := range cat.Levels {
				okL = okL || l.Value == lv
			}
			if !okH || !okL {
				return engine.InvalidParams("validation.continuity.config", "impacts", nil, "orizzonte %q o livello %d non configurati", hk, lv)
			}
		}
	}
	return nil
}

// BIABrief è la BIA in un elenco: valori in vigore (approvati) o, senza approvazione, la bozza.
type BIABrief struct {
	ID              string     `json:"id"`
	Key             string     `json:"key"`
	State           string     `json:"state"`
	StateCategory   string     `json:"state_category"`
	Critical        *bool      `json:"critical"`
	MTPD            *int       `json:"mtpd_minutes"`
	RTO             *int       `json:"rto_minutes"`
	RPO             *int       `json:"rpo_minutes"`
	ApprovedVersion *int       `json:"approved_version"`
	ApprovedAt      *time.Time `json:"approved_at"`
	ReviewDue       *string    `json:"review_due"`
}

func (m *model) biaBrief(bn *biaN) *BIABrief {
	if bn == nil {
		return nil
	}
	b := &BIABrief{ID: bn.ID, Key: bn.Key, State: bn.State, StateCategory: bn.StateCat, Critical: bn.Critical, MTPD: bn.MTPD, RTO: bn.RTO, RPO: bn.RPO}
	if v := bn.Approved; v != nil {
		c := v.Critical
		b.Critical, b.MTPD, b.RTO, b.RPO = &c, v.MTPD, v.RTO, v.RPO
		b.ApprovedVersion, b.ApprovedAt = &v.Version, &v.At
		due := v.At.AddDate(0, m.cfg.BIAReviewMonths, 0).Format(time.DateOnly)
		b.ReviewDue = &due
	}
	return b
}

// BIAApproved è la versione in vigore.
type BIAApproved struct {
	Version  int       `json:"version"`
	At       time.Time `json:"at"`
	By       *string   `json:"by"`
	MTPD     *int      `json:"mtpd_minutes"`
	RTO      *int      `json:"rto_minutes"`
	RPO      *int      `json:"rpo_minutes"`
	Critical bool      `json:"critical"`
}

// BIAVersion è una versione approvata nello storico.
type BIAVersion struct {
	BIAApproved
	SettingsVersion int `json:"settings_version"`
}

// BIAApp è un'applicazione della capability con il confronto dei valori.
type BIAApp struct {
	Ref
	RTO   *int  `json:"rto_minutes"`
	RPO   *int  `json:"rpo_minutes"`
	RTOOK *bool `json:"rto_ok"`
	RPOOK *bool `json:"rpo_ok"`
}

// BIAView è il dettaglio della BIA.
type BIAView struct {
	ID              string        `json:"id"`
	Key             string        `json:"key"`
	Title           string        `json:"title"`
	State           string        `json:"state"`
	StateCategory   string        `json:"state_category"`
	Capability      Ref           `json:"capability"`
	Impacts         Impacts       `json:"impacts"`
	Critical        *bool         `json:"critical"`
	CriticalReason  string        `json:"critical_reason"`
	MTPD            *int          `json:"mtpd_minutes"`
	RTO             *int          `json:"rto_minutes"`
	RPO             *int          `json:"rpo_minutes"`
	Notes           string        `json:"notes"`
	Proposal        *Proposal     `json:"proposal"`
	SettingsVersion int           `json:"settings_version"`
	Config          Config        `json:"config"`
	ConfigChanged   bool          `json:"config_changed"`
	Approved        *BIAApproved  `json:"approved"`
	Versions        []BIAVersion  `json:"versions"`
	Applications    []BIAApp      `json:"applications"`
	Can             BIAViewAccess `json:"can"`
}

// BIAViewAccess sono le azioni sulla BIA.
type BIAViewAccess struct {
	Edit    bool `json:"edit"`
	Approve bool `json:"approve"`
}

func cmpOK(declared, required *int) *bool {
	if declared == nil || required == nil {
		return nil
	}
	ok := *declared <= *required
	return &ok
}

// biaElement legge l'elemento e controlla che sia una BIA.
func (s *Service) biaElement(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(ref))
	if err != nil {
		return nil, err
	}
	if el.TypeKey != BIAType {
		return nil, engine.NotFound("ref", "", "BIA %q inesistente", ref)
	}
	return el, nil
}

// BIA restituisce il dettaglio della BIA.
func (s *Service) BIA(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*BIAView, error) {
	if !canRead(actor) {
		return nil, engine.ErrForbidden
	}
	el, err := s.biaElement(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	cfg, version, err := loadConfig(ctx, tx)
	if err != nil {
		return nil, err
	}
	v := &BIAView{ID: el.ID, Key: el.Key, Title: el.Title, State: el.State, StateCategory: el.StateCategory, Config: *cfg, Versions: []BIAVersion{}, Applications: []BIAApp{}}
	var raw []byte
	err = tx.QueryRow(ctx, `select x.capability_id::text, s.key || '-' || c.number, c.title, x.impacts, x.critical, x.critical_reason,
			x.mtpd_minutes, x.rto_minutes, x.rpo_minutes, x.notes, x.settings_version
		from ext_bia x join elements c on c.tenant_id = x.tenant_id and c.id = x.capability_id
		join spaces s on s.tenant_id = c.tenant_id and s.id = c.space_id where x.element_id = $1`, el.ID).
		Scan(&v.Capability.ID, &v.Capability.Key, &v.Capability.Title, &raw, &v.Critical, &v.CriticalReason, &v.MTPD, &v.RTO, &v.RPO, &v.Notes, &v.SettingsVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.NotFound("ref", "", "BIA %q senza dati", ref)
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &v.Impacts); err != nil {
		return nil, err
	}
	if v.Impacts == nil {
		v.Impacts = Impacts{}
	}
	v.Proposal = propose(cfg, v.Impacts)
	v.ConfigChanged = version > 0 && v.SettingsVersion != version
	rows, err := tx.Query(ctx, `select bv.version, bv.approved_at, m.display_name, bv.mtpd_minutes, bv.rto_minutes, bv.rpo_minutes, bv.critical, bv.settings_version
		from bia_versions bv left join members m on m.tenant_id = bv.tenant_id and m.id = bv.approved_by
		where bv.bia_id = $1 order by bv.version desc`, el.ID)
	if err != nil {
		return nil, err
	}
	var it BIAVersion
	if _, err := pgx.ForEachRow(rows, []any{&it.Version, &it.At, &it.By, &it.MTPD, &it.RTO, &it.RPO, &it.Critical, &it.SettingsVersion}, func() error {
		v.Versions = append(v.Versions, it)
		return nil
	}); err != nil {
		return nil, err
	}
	if len(v.Versions) > 0 {
		a := v.Versions[0].BIAApproved
		v.Approved = &a
	}
	m, err := loadModel(ctx, tx, s.Now())
	if err != nil {
		return nil, err
	}
	reqRTO, reqRPO := v.RTO, v.RPO
	if v.Approved != nil {
		reqRTO, reqRPO = v.Approved.RTO, v.Approved.RPO
	}
	for _, a := range m.appsOfCap(v.Capability.ID) {
		v.Applications = append(v.Applications, BIAApp{Ref: a.Ref, RTO: a.RTO, RPO: a.RPO, RTOOK: cmpOK(a.RTO, reqRTO), RPOOK: cmpOK(a.RPO, reqRPO)})
	}
	v.Can = BIAViewAccess{Edit: actor.Has(PermWrite) && el.StateCategory == "todo", Approve: actor.Has(PermApprove) && el.StateCategory == "in_progress"}
	return v, nil
}

// BIAInput crea la BIA di una capability.
type BIAInput struct {
	Capability string `json:"capability"`
}

// CreateBIA avvia la BIA di una capability (continuity.write).
func (s *Service) CreateBIA(ctx context.Context, tx pgx.Tx, actor engine.Actor, in BIAInput) (*BIAView, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	if err := requireReady(ctx, tx); err != nil {
		return nil, err
	}
	c, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(in.Capability))
	if err != nil {
		return nil, engine.NotFound("capability", "", "capability %q inesistente", in.Capability)
	}
	if c.TypeKey != CapabilityType {
		return nil, engine.InvalidCode("validation.continuity.not_capability", "capability", "la BIA si fa su una capability")
	}
	var existing string
	err = tx.QueryRow(ctx, `select s.key || '-' || e.number from ext_bia x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where x.capability_id = $1 and coalesce(o.category, '') <> 'cancelled' limit 1`, c.ID).Scan(&existing)
	if err == nil {
		return nil, engine.InvalidParams("validation.continuity.bia_exists", "capability", map[string]any{"key": existing}, "la capability ha già la BIA %s", existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	_, version, err := loadConfig(ctx, tx)
	if err != nil {
		return nil, err
	}
	lang := i18n.Lang(ctx)
	el, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: SpaceKey, TypeKey: BIAType,
		Title: i18n.T(lang, "continuity.bia.title", i18n.P{"capability": c.Title}), DeferStart: true})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into ext_bia (tenant_id, element_id, capability_id, settings_version) values (app_tenant(), $1, $2, $3)`,
		el.ID, c.ID, max(version, 1)); err != nil {
		return nil, err
	}
	if _, err := s.eng.StartProcess(ctx, tx, actor, el); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "continuity.bia.create", EntityType: BIAType, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"capability": c.Key}}); err != nil {
		return nil, err
	}
	return s.BIA(ctx, tx, actor, el.ID)
}

// optInt distingue un campo assente da uno messo a null.
type optInt struct {
	Set bool
	V   *int
}

func (o *optInt) UnmarshalJSON(b []byte) error {
	o.Set = true
	return json.Unmarshal(b, &o.V)
}

type optBool struct {
	Set bool
	V   *bool
}

func (o *optBool) UnmarshalJSON(b []byte) error {
	o.Set = true
	return json.Unmarshal(b, &o.V)
}

// BIAUpdate sono i campi modificabili in bozza.
type BIAUpdate struct {
	Impacts        *Impacts `json:"impacts"`
	Critical       optBool  `json:"critical"`
	CriticalReason *string  `json:"critical_reason"`
	MTPD           optInt   `json:"mtpd_minutes"`
	RTO            optInt   `json:"rto_minutes"`
	RPO            optInt   `json:"rpo_minutes"`
	Notes          *string  `json:"notes"`
}

func checkMinutes(field string, o optInt) error {
	if o.Set && o.V != nil && (*o.V < 0 || *o.V > 5256000) {
		return engine.OutOfRange(field, 0, 5256000, "minuti fuori intervallo")
	}
	return nil
}

// UpdateBIA modifica la BIA in bozza (continuity.write).
func (s *Service) UpdateBIA(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in BIAUpdate) (*BIAView, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	el, err := s.biaElement(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.StateCategory != "todo" {
		return nil, engine.InvalidCode("validation.continuity.bia_locked", "state", "la BIA si modifica solo in bozza: usa «Aggiorna la BIA» per una versione nuova")
	}
	cfg, version, err := loadConfig(ctx, tx)
	if err != nil {
		return nil, err
	}
	for f, o := range map[string]optInt{"mtpd_minutes": in.MTPD, "rto_minutes": in.RTO, "rpo_minutes": in.RPO} {
		if err := checkMinutes(f, o); err != nil {
			return nil, err
		}
	}
	var impacts []byte
	if in.Impacts != nil {
		if err := validImpacts(cfg, *in.Impacts); err != nil {
			return nil, err
		}
		if impacts, err = json.Marshal(*in.Impacts); err != nil {
			return nil, err
		}
	}
	if in.CriticalReason != nil && len(*in.CriticalReason) > 4000 {
		return nil, engine.TooLong("critical_reason", 4000, "motivazione troppo lunga")
	}
	if in.Notes != nil && len(*in.Notes) > 20000 {
		return nil, engine.TooLong("notes", 20000, "note troppo lunghe")
	}
	if _, err := tx.Exec(ctx, `update ext_bia set
			impacts = coalesce($2::jsonb, impacts),
			critical = case when $3 then $4 else critical end,
			critical_reason = coalesce($5, critical_reason),
			mtpd_minutes = case when $6 then $7 else mtpd_minutes end,
			rto_minutes = case when $8 then $9 else rto_minutes end,
			rpo_minutes = case when $10 then $11 else rpo_minutes end,
			notes = coalesce($12, notes),
			settings_version = $13
		where element_id = $1`, el.ID, impacts, in.Critical.Set, in.Critical.V, in.CriticalReason, in.MTPD.Set, in.MTPD.V,
		in.RTO.Set, in.RTO.V, in.RPO.Set, in.RPO.V, in.Notes, max(version, 1)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update elements set version = version + 1, updated_at = now(), updated_by = $2 where id = $1`, el.ID, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "continuity.bia.update", EntityType: BIAType, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP}); err != nil {
		return nil, err
	}
	return s.BIA(ctx, tx, actor, el.ID)
}

// ApproveInput è l'approvazione della BIA.
type ApproveInput struct {
	Comment string `json:"comment"`
}

// ApproveBIA approva la BIA in revisione (continuity.approve): il passaggio è di sistema, così
// solo chi ha il permesso lo esegue; l'aggancio afterTransition congela la versione.
func (s *Service) ApproveBIA(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in ApproveInput) (*BIAView, error) {
	if !actor.Has(PermApprove) {
		return nil, engine.ErrForbidden
	}
	el, err := s.biaElement(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.StateCategory != "in_progress" {
		return nil, engine.InvalidCode("validation.continuity.bia_locked", "state", "si approva una BIA in revisione")
	}
	// La fase di arrivo è la prima «conclusa» del processo della BIA (il nome si può cambiare).
	to := PhaseBIAApproved
	if phases, _, err := s.eng.Phases(ctx, tx, el); err == nil {
		for _, p := range phases {
			if p.Category == "done" && !p.OffPath {
				to = p.Name
				break
			}
		}
	}
	if _, err := s.eng.Transition(ctx, tx, actor, el.ID, to, in.Comment, engine.TransitionOpts{System: true}); err != nil {
		return nil, err
	}
	return s.BIA(ctx, tx, actor, el.ID)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// guardBIA: griglia completa e RTO/RPO indicati.
func (s *Service) guardBIA(ctx context.Context, tx pgx.Tx, el *engine.Element) error {
	var raw []byte
	var rto, rpo *int
	if err := tx.QueryRow(ctx, `select impacts, rto_minutes, rpo_minutes from ext_bia where element_id = $1`, el.ID).Scan(&raw, &rto, &rpo); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return engine.InvalidCode("validation.continuity.bia_incomplete", "impacts", "BIA senza dati")
		}
		return err
	}
	var imp Impacts
	if err := json.Unmarshal(raw, &imp); err != nil {
		return err
	}
	cfg, _, err := loadConfig(ctx, tx)
	if err != nil {
		return err
	}
	if !complete(cfg, imp) || rto == nil || rpo == nil {
		return engine.InvalidCode("validation.continuity.bia_incomplete", "impacts", "compila tutta la griglia degli impatti e indica RTO e RPO")
	}
	return nil
}

// freezeBIA congela la versione approvata.
func (s *Service) freezeBIA(ctx context.Context, tx pgx.Tx, ev engine.TransitionEvent) error {
	el := ev.Element
	var snap []byte
	var crit *bool
	var mtpd, rto, rpo *int
	var sv int
	err := tx.QueryRow(ctx, `select jsonb_build_object('impacts', impacts, 'critical', critical, 'critical_reason', critical_reason,
			'mtpd_minutes', mtpd_minutes, 'rto_minutes', rto_minutes, 'rpo_minutes', rpo_minutes, 'notes', notes,
			'config', (select config from continuity_settings)),
			critical, mtpd_minutes, rto_minutes, rpo_minutes, settings_version
		from ext_bia where element_id = $1`, el.ID).Scan(&snap, &crit, &mtpd, &rto, &rpo, &sv)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var version int
	if err := tx.QueryRow(ctx, `insert into bia_versions (tenant_id, bia_id, version, snapshot, critical, mtpd_minutes, rto_minutes, rpo_minutes, settings_version, approved_by)
		values (app_tenant(), $1, coalesce((select max(version) from bia_versions where bia_id = $1), 0) + 1, $2, $3, $4, $5, $6, $7, $8) returning version`,
		el.ID, snap, crit != nil && *crit, mtpd, rto, rpo, sv, nullable(ev.Actor.MemberID)).Scan(&version); err != nil {
		return err
	}
	return audit.Append(ctx, tx, audit.Event{Action: "continuity.bia.approve", EntityType: BIAType, EntityID: el.ID,
		ActorAccountID: ev.Actor.AccountID, ActorMemberID: ev.Actor.MemberID, IP: ev.Actor.IP,
		Payload: map[string]any{"version": version, "rto_minutes": rto, "rpo_minutes": rpo, "mtpd_minutes": mtpd}})
}

// afterTransition: BIA approvata → versione congelata; test con esito → evidenza.
func (s *Service) afterTransition(ctx context.Context, tx pgx.Tx, ev engine.TransitionEvent) error {
	switch ev.Element.TypeKey {
	case BIAType:
		if ev.Element.StateCategory == "done" {
			return s.freezeBIA(ctx, tx, ev)
		}
	case ResTestType:
		return s.testTransition(ctx, tx, ev)
	}
	return nil
}
