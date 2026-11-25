package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// «Tutto ciò che si configura evolve nel tempo» (migrazione 0069), applicato ai processi.
//
// Il ciclo è sempre lo stesso: modifica → anteprima → pubblica con data → transizione.
//   - La pubblicazione ha una data di entrata in vigore (oggi o futura) e, se serve, una data di fine.
//     Con la data futura la versione resta «programmata» (status scheduled) e quella in vigore
//     continua a valere; alla data diventa quella in vigore e la precedente è «deprecata».
//   - La regola di transizione dice, per fase, che cosa accade alle richieste già avviate:
//     auto (passano alla versione nuova), resta (restano sulla vecchia), manuale (le sposta
//     l'amministratore con la migrazione esistente). Predefinita: le fasi iniziali (categoria
//     «da fare») passano da sole, le altre restano.
//   - L'entrata in vigore la applica ActivateDue: dal lavoratore periodico (RunVersioni) e, in modo
//     pigro e idempotente, all'avvio di una richiesta. La migrazione è quella esistente (moveGroups).
//
// La logica non dipende dai processi nel codice: un modulo che versiona altro (questionari, scorecard,
// politiche, matrici SLA, cataloghi...) riusa Lifecycle e TransitionRule e applica il proprio spostamento.

// Modi della regola di transizione per una fase.
const (
	ModeAuto   = "auto"
	ModeStay   = "resta"
	ModeManual = "manuale"
)

// Stati derivati di una versione (Lifecycle).
const (
	LifeDraft      = "draft"      // bozza, non ancora pubblicata
	LifeScheduled  = "scheduled"  // pubblicata con una data di entrata in vigore futura
	LifeInForce    = "in_force"   // quella che usano le richieste nuove
	LifeDeprecated = "deprecated" // sostituita da una versione più nuova, ancora in uso dalle richieste avviate
	LifeConcluded  = "concluded"  // la data di fine è passata
	LifeWithdrawn  = "withdrawn"  // sostituita prima di entrare in vigore
)

// TransitionRule è la regola di transizione di una pubblicazione.
type TransitionRule struct {
	// Default: che cosa accade alle fasi non elencate (auto | resta | manuale; vuoto = resta).
	Default string `json:"default,omitempty"`
	// Phases: modo per nome di fase (delle versioni di partenza).
	Phases map[string]string `json:"phases,omitempty"`
}

func validMode(m string) bool { return m == ModeAuto || m == ModeStay || m == ModeManual }

// ModeOf è il modo della fase, senza distinguere maiuscole.
func (r TransitionRule) ModeOf(phase string) string {
	for k, v := range r.Phases {
		if strings.EqualFold(k, phase) {
			return v
		}
	}
	if r.Default == "" {
		return ModeStay
	}
	return r.Default
}

// Check controlla che i modi siano quelli ammessi.
func (r TransitionRule) Check() error {
	if r.Default != "" && !validMode(r.Default) {
		return invalid("transition", "modo di transizione non valido: %q", r.Default)
	}
	for k, v := range r.Phases {
		if !validMode(v) {
			return invalid("transition", "modo di transizione non valido per la fase %q: %q", k, v)
		}
	}
	return nil
}

// DefaultTransitionRule è la regola predefinita: le fasi iniziali (categoria «da fare», sul percorso
// principale) della versione in vigore passano alla nuova in automatico, le altre restano dove sono.
func DefaultTransitionRule(prev *Workflow) TransitionRule {
	r := TransitionRule{Default: ModeStay, Phases: map[string]string{}}
	if prev == nil {
		return r
	}
	for _, p := range prev.Phases {
		if p.Category == "todo" && !p.OffPath {
			r.Phases[p.Name] = ModeAuto
		} else {
			r.Phases[p.Name] = ModeStay
		}
	}
	return r
}

// Lifecycle ricava lo stato di una versione dalla colonna di stato e dalle date.
func Lifecycle(status string, from, to, deprecated *time.Time, now time.Time) string {
	switch status {
	case "draft":
		return LifeDraft
	case "scheduled":
		return LifeScheduled
	case "published":
		if to != nil && !to.After(now) {
			return LifeConcluded
		}
		return LifeInForce
	case "superseded":
		switch {
		case deprecated == nil:
			return LifeWithdrawn
		case to != nil && !to.After(now):
			return LifeConcluded
		}
		return LifeDeprecated
	}
	return status
}

// ParseVigoreDate legge una data di vigore: RFC 3339 oppure solo il giorno (AAAA-MM-GG, a mezzanotte
// ora italiana). Vuoto = nessuna data.
func ParseVigoreDate(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	loc, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		loc = time.UTC
	}
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return nil, invalid("effective_from", "data non valida: usa AAAA-MM-GG")
	}
	return &t, nil
}

// PublishInput è la pubblicazione di una bozza.
type PublishInput struct {
	Notes string `json:"notes"`
	// EffectiveFrom: entrata in vigore (vuoto o passata = subito).
	EffectiveFrom *time.Time `json:"-"`
	EffectiveTo   *time.Time `json:"-"`
	// Transition: regola per le richieste già avviate. Assente = nessuno spostamento (comportamento storico).
	Transition *TransitionRule `json:"transition,omitempty"`
}

// TransitionGroup è un gruppo di richieste: stessa versione di partenza, stessa fase.
type TransitionGroup struct {
	FromVersion int    `json:"from_version"`
	Phase       string `json:"phase"`
	Count       int    `json:"count"`
	Mode        string `json:"mode"` // auto | resta | manuale (effettivo)
	TargetPhase string `json:"target_phase,omitempty"`
	// Missing: la regola dice «auto» ma la versione nuova non ha la fase; il gruppo passa a «manuale».
	Missing bool `json:"missing,omitempty"`
}

// TransitionPlan è l'anteprima (o l'esito) della regola di transizione.
type TransitionPlan struct {
	ToVersion int               `json:"to_version"`
	Rule      TransitionRule    `json:"rule"`
	Groups    []TransitionGroup `json:"groups"`
	Total     int               `json:"total"`
	Auto      int               `json:"auto"`
	Stay      int               `json:"stay"`
	Manual    int               `json:"manual"`
}

// PublishPreview è l'anteprima di una pubblicazione.
type PublishPreview struct {
	Version       int            `json:"version"`
	Immediate     bool           `json:"immediate"`
	EffectiveFrom time.Time      `json:"effective_from"`
	EffectiveTo   *time.Time     `json:"effective_to,omitempty"`
	InForce       int            `json:"in_force"`  // versione oggi in vigore (0 = nessuna)
	Withdrawn     int            `json:"withdrawn"` // versione programmata che questa sostituirebbe (0 = nessuna)
	Plan          TransitionPlan `json:"plan"`
	Problems      []Problem      `json:"problems"`
}

func (s *Service) transitionPlan(ctx context.Context, tx pgx.Tx, processID, excludeVersionID string, toNumber int, target *Workflow, rule TransitionRule) (*TransitionPlan, error) {
	rows, err := tx.Query(ctx, `
		select v.number, coalesce(o.name, ''), count(*)
		from elements e
		join process_versions v on v.tenant_id = e.tenant_id and v.id = e.process_version_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where v.process_id = $1 and v.status in ('published', 'superseded') and ($2::uuid is null or v.id <> $2::uuid)
		  and e.deleted_at is null
		group by 1, 2 order by 1, 2`, processID, nullable(excludeVersionID))
	if err != nil {
		return nil, err
	}
	plan := &TransitionPlan{ToVersion: toNumber, Rule: rule, Groups: []TransitionGroup{}}
	var g TransitionGroup
	if _, err := pgx.ForEachRow(rows, []any{&g.FromVersion, &g.Phase, &g.Count}, func() error {
		tg := g
		tg.Mode = rule.ModeOf(tg.Phase)
		if tg.Mode == ModeAuto {
			for _, p := range target.Phases {
				if strings.EqualFold(p.Name, tg.Phase) {
					tg.TargetPhase = p.Name
				}
			}
			if tg.TargetPhase == "" {
				tg.Mode, tg.Missing = ModeManual, true
			}
		}
		plan.Total += tg.Count
		switch tg.Mode {
		case ModeAuto:
			plan.Auto += tg.Count
		case ModeManual:
			plan.Manual += tg.Count
		default:
			plan.Stay += tg.Count
		}
		plan.Groups = append(plan.Groups, tg)
		return nil
	}); err != nil {
		return nil, err
	}
	return plan, nil
}

// applyTransition sposta le richieste dei gruppi «auto» sulla versione che entra in vigore.
func (s *Service) applyTransition(ctx context.Context, tx pgx.Tx, actor Actor, processID, typeID, toID string, toNumber int, rule TransitionRule) (*TransitionPlan, error) {
	target, err := loadVersion(ctx, tx, toID)
	if err != nil {
		return nil, err
	}
	plan, err := s.transitionPlan(ctx, tx, processID, toID, toNumber, target, rule)
	if err != nil {
		return nil, err
	}
	var move []MigrationGroup
	for _, g := range plan.Groups {
		if g.Mode == ModeAuto {
			move = append(move, MigrationGroup{FromVersion: g.FromVersion, Phase: g.Phase, Count: g.Count, TargetPhase: g.TargetPhase, OK: true})
		}
	}
	if err := s.moveGroups(ctx, tx, actor, processID, typeID, toID, toNumber, target, move); err != nil {
		return nil, err
	}
	err = auditProcess(ctx, tx, actor, "process.transition", processID, map[string]any{
		"to_version": toNumber, "auto": plan.Auto, "stay": plan.Stay, "manual": plan.Manual, "groups": plan.Groups})
	return plan, err
}

// publishVersion rende la bozza una versione pubblicata: subito, oppure programmata.
func (s *Service) publishVersion(ctx context.Context, tx pgx.Tx, actor Actor, processID, typeID, vid string, number int, in PublishInput) error {
	var now time.Time
	if err := tx.QueryRow(ctx, "select now()").Scan(&now); err != nil {
		return err
	}
	from := now
	if in.EffectiveFrom != nil && in.EffectiveFrom.After(now) {
		from = *in.EffectiveFrom
	}
	if in.EffectiveTo != nil && !in.EffectiveTo.After(from) {
		return invalid("effective_to", "la data di fine deve essere dopo quella di entrata in vigore")
	}
	var rule []byte
	if in.Transition != nil {
		if err := in.Transition.Check(); err != nil {
			return err
		}
		rule, _ = json.Marshal(in.Transition)
	}
	immediate := !from.After(now)
	// Una nuova pubblicazione sostituisce la versione ancora programmata.
	if _, err := tx.Exec(ctx, "update process_versions set status = 'superseded' where process_id = $1 and status = 'scheduled'", processID); err != nil {
		return err
	}
	status := "scheduled"
	if immediate {
		status = "published"
		if _, err := tx.Exec(ctx, `update process_versions set status = 'superseded', deprecated_at = $2
			where process_id = $1 and status = 'published'`, processID, from); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `update process_versions set status = $2, published_at = now(), published_by = $3,
		effective_from = $4, effective_to = $5, transition = $6 where id = $1`,
		vid, status, nullable(actor.MemberID), from, in.EffectiveTo, rule); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "update process_definitions set updated_at = now() where id = $1", processID); err != nil {
		return err
	}
	if err := auditProcess(ctx, tx, actor, "process.publish", processID, map[string]any{
		"version": number, "version_id": vid, "effective_from": from, "effective_to": in.EffectiveTo, "scheduled": !immediate}); err != nil {
		return err
	}
	if immediate && in.Transition != nil {
		_, err := s.applyTransition(ctx, tx, actor, processID, typeID, vid, number, *in.Transition)
		return err
	}
	return nil
}

// PreviewPublish dice che cosa farebbe la pubblicazione della bozza: da quando vale, quale versione
// sostituisce e, per fase, quante richieste migrano, restano o vanno spostate a mano.
// Senza regola in input propone quella predefinita.
func (s *Service) PreviewPublish(ctx context.Context, tx pgx.Tx, actor Actor, ref string, in PublishInput) (*PublishPreview, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	id, typeID, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	var vid string
	var number int
	var raw []byte
	err = tx.QueryRow(ctx, "select id, number, definition from process_versions where process_id = $1 and status = 'draft'", id).Scan(&vid, &number, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, invalid("draft", "non c'è nessuna bozza da pubblicare")
	}
	if err != nil {
		return nil, err
	}
	w, err := DecodeDefinition(raw)
	if err != nil {
		return nil, err
	}
	out := &PublishPreview{Version: number, Problems: Validate(w)}
	refs, err := s.validateRefs(ctx, tx, w, typeID)
	if err != nil {
		return nil, err
	}
	out.Problems = append(out.Problems, refs...)
	var now time.Time
	if err := tx.QueryRow(ctx, "select now()").Scan(&now); err != nil {
		return nil, err
	}
	out.EffectiveFrom = now
	if in.EffectiveFrom != nil && in.EffectiveFrom.After(now) {
		out.EffectiveFrom = *in.EffectiveFrom
	}
	out.Immediate = !out.EffectiveFrom.After(now)
	out.EffectiveTo = in.EffectiveTo
	if in.EffectiveTo != nil && !in.EffectiveTo.After(out.EffectiveFrom) {
		return nil, invalid("effective_to", "la data di fine deve essere dopo quella di entrata in vigore")
	}
	var curID *string
	_ = tx.QueryRow(ctx, "select id, number from process_versions where process_id = $1 and status = 'published'", id).Scan(&curID, &out.InForce)
	_ = tx.QueryRow(ctx, "select number from process_versions where process_id = $1 and status = 'scheduled'", id).Scan(&out.Withdrawn)
	var rule TransitionRule
	if in.Transition != nil {
		rule = *in.Transition
		if err := rule.Check(); err != nil {
			return nil, err
		}
	} else {
		var prev *Workflow
		if curID != nil {
			if prev, err = loadVersion(ctx, tx, *curID); err != nil {
				return nil, err
			}
		}
		rule = DefaultTransitionRule(prev)
	}
	plan, err := s.transitionPlan(ctx, tx, id, vid, number, w, rule)
	if err != nil {
		return nil, err
	}
	out.Plan = *plan
	return out, nil
}

// ActivateDue porta in vigore le versioni programmate la cui data è arrivata: la precedente diventa
// deprecata e le richieste dei gruppi «auto» migrano (con storia e audit). È idempotente: una versione
// già attivata non è più «programmata». Restituisce quante versioni sono entrate in vigore.
func (s *Service) ActivateDue(ctx context.Context, tx pgx.Tx) (int, error) {
	rows, err := tx.Query(ctx, `
		select v.id, v.process_id, p.type_id, v.number, v.effective_from, v.transition
		from process_versions v join process_definitions p on p.tenant_id = v.tenant_id and p.id = v.process_id
		where v.status = 'scheduled' and v.effective_from <= now()
		order by v.effective_from
		for update of v skip locked`)
	if err != nil {
		return 0, err
	}
	type due struct {
		id, processID, typeID string
		number                int
		from                  time.Time
		rule                  []byte
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		err := r.Scan(&d.id, &d.processID, &d.typeID, &d.number, &d.from, &d.rule)
		return d, err
	})
	if err != nil || len(list) == 0 {
		return 0, err
	}
	sys := Actor{Permissions: []string{"*"}}
	for _, d := range list {
		if _, err := tx.Exec(ctx, `update process_versions set status = 'superseded', deprecated_at = $2
			where process_id = $1 and status = 'published'`, d.processID, d.from); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, "update process_versions set status = 'published' where id = $1", d.id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, "update process_definitions set updated_at = now() where id = $1", d.processID); err != nil {
			return 0, err
		}
		if err := auditProcess(ctx, tx, sys, "process.activate", d.processID, map[string]any{"version": d.number, "version_id": d.id}); err != nil {
			return 0, err
		}
		if len(d.rule) > 0 {
			var rule TransitionRule
			if err := json.Unmarshal(d.rule, &rule); err != nil {
				return 0, err
			}
			if _, err := s.applyTransition(ctx, tx, sys, d.processID, d.typeID, d.id, d.number, rule); err != nil {
				return 0, err
			}
		}
	}
	return len(list), nil
}

// RunVersioni porta in vigore le versioni programmate a intervalli, finché ctx non è chiuso.
func (s *Service) RunVersioni(ctx context.Context, d *db.DB, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = time.Minute
	}
	run := func() {
		n, err := s.ActivateAllDue(ctx, d)
		if err != nil {
			log.Warn("entrata in vigore delle versioni dei processi", "errore", err)
		} else if n > 0 {
			log.Info("versioni dei processi entrate in vigore", "numero", n)
		}
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// ActivateAllDue controlla i tenant che hanno una versione la cui data è arrivata. Solo l'elenco
// passa dal ruolo di piattaforma.
func (s *Service) ActivateAllDue(ctx context.Context, d *db.DB) (int, error) {
	rows, err := d.Owner.Query(ctx, `select distinct tenant_id::text from process_versions where status = 'scheduled' and effective_from <= now()`)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	total := 0
	var errs []error
	for _, t := range tenants {
		err := d.InTenant(ctx, t, db.TenantOpts{}, func(tx pgx.Tx) error {
			n, err := s.ActivateDue(ctx, tx)
			total += n
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}

// VigorePatch cambia le date di una versione già pubblicata.
type VigorePatch struct {
	From    *time.Time // solo per una versione programmata
	To      *time.Time
	ClearTo bool
}

// SetVigore cambia la data di entrata in vigore di una versione programmata e la data di fine di una
// versione pubblicata (il contenuto resta immutabile).
func (s *Service) SetVigore(ctx context.Context, tx pgx.Tx, actor Actor, ref string, number int, p VigorePatch) (*ProcessInfo, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	id, _, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	var vid, status string
	var from time.Time
	var to *time.Time
	err = tx.QueryRow(ctx, `select id, status, coalesce(effective_from, now()), effective_to from process_versions
		where process_id = $1 and number = $2 for update`, id, number).Scan(&vid, &status, &from, &to)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status == "draft" {
		return nil, invalid("number", "la bozza non ha date: si impostano pubblicando")
	}
	if p.From != nil {
		if status != "scheduled" {
			return nil, invalid("effective_from", "l'entrata in vigore si cambia solo prima che arrivi")
		}
		var now time.Time
		if err := tx.QueryRow(ctx, "select now()").Scan(&now); err != nil {
			return nil, err
		}
		if !p.From.After(now) {
			return nil, invalid("effective_from", "la nuova data deve essere futura")
		}
		from = *p.From
	}
	if p.To != nil {
		to = p.To
	}
	if p.ClearTo {
		to = nil
	}
	if to != nil && !to.After(from) {
		return nil, invalid("effective_to", "la data di fine deve essere dopo quella di entrata in vigore")
	}
	if _, err := tx.Exec(ctx, `update process_versions set effective_from = $2, effective_to = $3 where id = $1`, vid, from, to); err != nil {
		return nil, err
	}
	if err := auditProcess(ctx, tx, actor, "process.vigore", id, map[string]any{"version": number, "effective_from": from, "effective_to": to}); err != nil {
		return nil, err
	}
	return s.GetProcess(ctx, tx, actor, id)
}

// ---------------------------------------------------------------------------------------------
// Guide allegate

// Guide è un riferimento utile a chi lavora sulla richiesta: un documento, un link o una pagina Knowledge.
type Guide struct {
	ID       string  `json:"id"`
	Phase    *string `json:"phase"` // null = guida dell'intero processo
	Kind     string  `json:"kind"`  // document | link | knowledge
	Title    string  `json:"title"`
	Target   string  `json:"target"` // indirizzo, oppure chiave della pagina Knowledge
	Position int     `json:"position"`
}

// GuideInput crea o modifica una guida.
type GuideInput struct {
	Phase    *string `json:"phase"`
	Kind     string  `json:"kind"`
	Title    string  `json:"title"`
	Target   string  `json:"target"`
	Position *int    `json:"position"`
}

// Guides elenca le guide del processo (la lettura è di tutti, dentro il tenant).
func (s *Service) Guides(ctx context.Context, tx pgx.Tx, ref string) ([]Guide, error) {
	id, _, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	return guidesOf(ctx, tx, id)
}

func guidesOf(ctx context.Context, tx pgx.Tx, processID string) ([]Guide, error) {
	rows, err := tx.Query(ctx, `select id, phase, kind, title, target, position from process_guides
		where process_id = $1 order by position, created_at`, processID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Guide])
	if list == nil {
		list = []Guide{}
	}
	return list, err
}

// GuidesFor sono le guide che servono a chi lavora in una fase: quelle della fase e quelle di tutto il processo.
func (s *Service) GuidesFor(ctx context.Context, tx pgx.Tx, processID, phase string) ([]Guide, error) {
	all, err := guidesOf(ctx, tx, processID)
	if err != nil {
		return nil, err
	}
	out := []Guide{}
	for _, g := range all {
		if g.Phase == nil || strings.EqualFold(*g.Phase, phase) {
			out = append(out, g)
		}
	}
	return out, nil
}

func (in *GuideInput) check(ctx context.Context, tx pgx.Tx, processID string) error {
	in.Title, in.Target = strings.TrimSpace(in.Title), strings.TrimSpace(in.Target)
	if !slices.Contains([]string{"document", "link", "knowledge"}, in.Kind) {
		return invalid("kind", "tipo di guida non valido: documento, link o pagina Knowledge")
	}
	if in.Title == "" || len([]rune(in.Title)) > 200 {
		return invalid("title", "il titolo è obbligatorio (fino a 200 caratteri)")
	}
	if in.Target == "" || len(in.Target) > 2000 {
		return invalid("target", "la destinazione è obbligatoria")
	}
	if in.Kind == "knowledge" {
		if strings.ContainsAny(in.Target, " /?#\\") {
			return invalid("target", "indica la chiave della pagina Knowledge, per esempio DOC-12")
		}
	} else if !(strings.HasPrefix(in.Target, "https://") || strings.HasPrefix(in.Target, "http://") || (strings.HasPrefix(in.Target, "/") && !strings.HasPrefix(in.Target, "//"))) {
		return invalid("target", "l'indirizzo deve iniziare con https://, http:// o /")
	}
	if in.Phase != nil {
		ph := strings.TrimSpace(*in.Phase)
		if ph == "" {
			in.Phase = nil
		} else {
			in.Phase = &ph
			var ok bool
			if err := tx.QueryRow(ctx, `select exists (select 1 from process_versions v, jsonb_array_elements(v.definition->'phases') p
				where v.process_id = $1 and lower(p->>'name') = lower($2))`, processID, ph).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return invalid("phase", "il processo non ha una fase %q", ph)
			}
		}
	}
	return nil
}

// CreateGuide allega una guida al processo o a una sua fase.
func (s *Service) CreateGuide(ctx context.Context, tx pgx.Tx, actor Actor, ref string, in GuideInput) (*Guide, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	id, _, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	if err := in.check(ctx, tx, id); err != nil {
		return nil, err
	}
	pos := 0
	if in.Position != nil {
		pos = *in.Position
	} else if err := tx.QueryRow(ctx, "select coalesce(max(position), 0) + 10 from process_guides where process_id = $1", id).Scan(&pos); err != nil {
		return nil, err
	}
	g := &Guide{Phase: in.Phase, Kind: in.Kind, Title: in.Title, Target: in.Target, Position: pos}
	if err := tx.QueryRow(ctx, `insert into process_guides (tenant_id, process_id, phase, kind, title, target, position, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7) returning id`,
		id, in.Phase, in.Kind, in.Title, in.Target, pos, nullable(actor.MemberID)).Scan(&g.ID); err != nil {
		return nil, err
	}
	return g, auditProcess(ctx, tx, actor, "process.guide.add", id, map[string]any{"guide": g.ID, "kind": g.Kind})
}

// UpdateGuide sostituisce i dati di una guida.
func (s *Service) UpdateGuide(ctx context.Context, tx pgx.Tx, actor Actor, ref, guideID string, in GuideInput) (*Guide, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	id, _, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	if err := in.check(ctx, tx, id); err != nil {
		return nil, err
	}
	g := &Guide{}
	err = tx.QueryRow(ctx, `update process_guides set phase = $3, kind = $4, title = $5, target = $6, position = coalesce($7, position)
		where process_id = $1 and id::text = $2 returning id, phase, kind, title, target, position`,
		id, guideID, in.Phase, in.Kind, in.Title, in.Target, in.Position).Scan(&g.ID, &g.Phase, &g.Kind, &g.Title, &g.Target, &g.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return g, auditProcess(ctx, tx, actor, "process.guide.update", id, map[string]any{"guide": g.ID})
}

// DeleteGuide toglie una guida.
func (s *Service) DeleteGuide(ctx context.Context, tx pgx.Tx, actor Actor, ref, guideID string) error {
	if err := requireProcessAdmin(actor); err != nil {
		return err
	}
	id, _, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "delete from process_guides where process_id = $1 and id::text = $2", id, guideID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return auditProcess(ctx, tx, actor, "process.guide.remove", id, map[string]any{"guide": guideID})
}

// ---------------------------------------------------------------------------------------------
// Avviso di versione deprecata

// VersionNotice avvisa chi lavora su una richiesta ancora su una versione sostituita.
type VersionNotice struct {
	Kind           string     `json:"kind"` // deprecated
	ProcessKey     string     `json:"process_key"`
	ProcessName    string     `json:"process_name"`
	Version        int        `json:"version"`         // quella della richiesta
	CurrentVersion int        `json:"current_version"` // quella in vigore
	Since          time.Time  `json:"since"`
	EndsAt         *time.Time `json:"ends_at,omitempty"`
	// PhaseExists: la fase attuale esiste anche nella versione in vigore (la migrazione è diretta).
	PhaseExists bool `json:"phase_exists"`
	// CanMigrate: chi guarda può chiedere la migrazione (amministratore di processo); gli altri la chiedono a lui.
	CanMigrate bool `json:"can_migrate"`
}

// VersionNoticeOf restituisce l'avviso per l'elemento, o nil se è sulla versione in vigore (o senza processo).
func (s *Service) VersionNoticeOf(ctx context.Context, tx pgx.Tx, actor Actor, el *Element) (*VersionNotice, error) {
	if el.ProcessVersionID == nil {
		return nil, nil
	}
	n := &VersionNotice{Kind: "deprecated", CanMigrate: actor.Has(PermProcessAdmin)}
	var processID string
	var status string
	var since *time.Time
	err := tx.QueryRow(ctx, `select p.id, p.key, p.name, v.number, v.status, v.deprecated_at, v.effective_to
		from process_versions v join process_definitions p on p.tenant_id = v.tenant_id and p.id = v.process_id
		where v.id = $1`, *el.ProcessVersionID).Scan(&processID, &n.ProcessKey, &n.ProcessName, &n.Version, &status, &since, &n.EndsAt)
	if err != nil {
		return nil, err
	}
	if status != "superseded" || since == nil || since.After(time.Now()) {
		return nil, nil
	}
	n.Since = *since
	var curID string
	err = tx.QueryRow(ctx, "select id, number from process_versions where process_id = $1 and status = 'published'", processID).Scan(&curID, &n.CurrentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cur, err := loadVersion(ctx, tx, curID)
	if err != nil {
		return nil, err
	}
	for _, p := range cur.Phases {
		if strings.EqualFold(p.Name, el.State) {
			n.PhaseExists = true
		}
	}
	return n, nil
}

// GuideProcessOf dà l'id del processo dell'elemento (vuoto se non ne ha uno).
func (s *Service) GuideProcessOf(ctx context.Context, tx pgx.Tx, el *Element) (string, error) {
	if el.ProcessVersionID == nil {
		return "", nil
	}
	var id string
	err := tx.QueryRow(ctx, "select process_id from process_versions where id = $1", *el.ProcessVersionID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("processo dell'elemento: %w", err)
	}
	return id, nil
}
