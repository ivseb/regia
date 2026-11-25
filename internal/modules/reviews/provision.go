package reviews

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

//go:embed starter/*.json
var starters embed.FS

// PackageFormat identifica il formato di export e import della configurazione delle valutazioni.
const PackageFormat = "regia.reviews/v1"

var roles = []struct{ key, name string }{
	{RoleOwner, "Responsabile della compilazione"},
	{RoleCompiler, "Compilatore"},
	{RoleTeam, "Team valutatore"},
	{RoleLead, "Responsabile del team valutatore"},
}

// Package è la configurazione delle valutazioni in JSON: gruppi da creare, team, modelli
// di questionario, tipi e modifiche ai processi (regole e gate). È il formato del
// pacchetto di partenza e dell'export.
type Package struct {
	Format         string            `json:"format"`
	Name           string            `json:"name,omitempty"`
	Description    string            `json:"description,omitempty"`
	ExportedAt     string            `json:"exported_at,omitempty"`
	Groups         []string          `json:"groups,omitempty"`
	Teams          []PackageTeam     `json:"teams"`
	Templates      []PackageTemplate `json:"templates"`
	Types          []PackageType     `json:"types"`
	ProcessPatches []ProcessPatch    `json:"process_patches,omitempty"`
}

type PackageTeam struct {
	Key            string `json:"key"`
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	Group          string `json:"group"`
	SLADays        int    `json:"sla_days"`
	ReminderDays   int    `json:"reminder_days"`
	EscalationDays int    `json:"escalation_days,omitempty"`
}

type PackageTemplate struct {
	Key        string          `json:"key"`
	Notes      string          `json:"notes,omitempty"`
	Definition json.RawMessage `json:"definition"`
}

type PackageType struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Team        string    `json:"team"`
	Scope       string    `json:"scope"`
	TemplateKey string    `json:"template_key"`
	Mandatory   bool      `json:"mandatory"`
	Deciders    string    `json:"deciders,omitempty"`
	Outcomes    []Outcome `json:"outcomes,omitempty"`
	OwnerRoles  []string  `json:"owner_roles,omitempty"`
}

// ProcessPatch aggiunge gate, gate sui passaggi e regole a un processo esistente, come
// nuova versione pubblicata. Ciò che c'è già (stessa chiave) non si tocca.
type ProcessPatch struct {
	Process         string           `json:"process"`
	Notes           string           `json:"notes,omitempty"`
	Gates           []engine.Gate    `json:"gates,omitempty"`
	TransitionGates []TransitionGate `json:"transition_gates,omitempty"`
	Rules           []engine.Rule    `json:"rules,omitempty"`
}

// TransitionGate aggiunge un gate ai passaggi (non di sistema) verso una fase.
type TransitionGate struct {
	To   string `json:"to"`
	Gate string `json:"gate"`
}

// ParsePackage legge un pacchetto rifiutando i campi sconosciuti.
func ParsePackage(raw []byte) (*Package, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	p := &Package{}
	if err := dec.Decode(p); err != nil {
		return nil, engine.InvalidValue("package", "JSON non valido: %v", err)
	}
	if p.Format != PackageFormat {
		return nil, engine.InvalidValue("format", "formato %q non riconosciuto (atteso %s)", p.Format, PackageFormat)
	}
	return p, nil
}

// ImportResult riassume un import.
type ImportResult struct {
	Teams     []string `json:"teams"`
	Templates []string `json:"templates"`
	Types     []string `json:"types"`
	Processes []string `json:"processes"`
	Skipped   []string `json:"skipped"`
}

// Import porta un pacchetto nel tenant: crea gruppi e team mancanti (quelli esistenti per
// chiave si aggiornano), salva ogni modello come bozza e, con publish, la pubblica; crea
// o aggiorna i tipi; applica le modifiche ai processi come nuova versione (saltate se il
// processo ha già una bozza in lavorazione, per non sovrascriverla).
func (s *Service) Import(ctx context.Context, tx pgx.Tx, actor engine.Actor, p *Package, publish bool) (*ImportResult, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	res := &ImportResult{Teams: []string{}, Templates: []string{}, Types: []string{}, Processes: []string{}, Skipped: []string{}}
	for _, g := range p.Groups {
		if _, err := tx.Exec(ctx, `insert into groups (tenant_id, id, name) values (app_tenant(), $1, $2) on conflict do nothing`, db.NewID(), strings.TrimSpace(g)); err != nil {
			return nil, err
		}
	}
	for _, t := range p.Teams {
		name, desc, group := t.Name, t.Description, t.Group
		sla, rem, esc := t.SLADays, t.ReminderDays, t.EscalationDays
		if sla == 0 {
			sla = 5
		}
		in := TeamInput{Key: t.Key, Name: &name, Description: &desc, Group: &group, SLADays: &sla, ReminderDays: &rem, EscalationDays: &esc}
		if _, err := s.team(ctx, tx, t.Key); errors.Is(err, engine.ErrNotFound) {
			if _, err := s.createTeam(ctx, tx, actor, in); err != nil {
				return nil, fmt.Errorf("team %s: %w", t.Key, err)
			}
		} else if err != nil {
			return nil, err
		} else if _, err := s.UpdateTeam(ctx, tx, actor, t.Key, in); err != nil {
			return nil, fmt.Errorf("team %s: %w", t.Key, err)
		}
		res.Teams = append(res.Teams, t.Key)
	}
	for _, t := range p.Templates {
		d, err := DecodeDefinition(t.Definition)
		if err != nil {
			return nil, fmt.Errorf("modello %s: %w", t.Key, err)
		}
		if !keyRe.MatchString(t.Key) {
			return nil, engine.InvalidValue("templates", "chiave del modello %q non valida", t.Key)
		}
		notes := t.Notes
		if notes == "" {
			notes = "Importato"
		}
		if err := saveTemplateDraft(ctx, tx, actor, t.Key, d, notes); err != nil {
			return nil, err
		}
		if publish {
			if _, err := s.PublishTemplate(ctx, tx, actor, t.Key, ""); err != nil {
				return nil, fmt.Errorf("modello %s: %w", t.Key, err)
			}
		}
		res.Templates = append(res.Templates, t.Key)
	}
	for _, t := range p.Types {
		name, desc, team, scope, tk, mand := t.Name, t.Description, t.Team, t.Scope, t.TemplateKey, t.Mandatory
		in := TypeInput{Key: t.Key, Name: &name, Description: &desc, Team: &team, Scope: &scope, TemplateKey: &tk, Mandatory: &mand,
			Outcomes: t.Outcomes, OwnerRoles: t.OwnerRoles}
		if t.Deciders != "" {
			in.Deciders = &t.Deciders
		}
		if _, err := s.reviewType(ctx, tx, t.Key); errors.Is(err, engine.ErrNotFound) {
			if _, err := s.createType(ctx, tx, actor, in); err != nil {
				return nil, fmt.Errorf("tipo %s: %w", t.Key, err)
			}
		} else if err != nil {
			return nil, err
		} else if _, err := s.UpdateType(ctx, tx, actor, t.Key, in); err != nil {
			return nil, fmt.Errorf("tipo %s: %w", t.Key, err)
		}
		res.Types = append(res.Types, t.Key)
	}
	for _, pp := range p.ProcessPatches {
		applied, err := s.applyPatch(ctx, tx, actor, pp)
		if err != nil {
			return nil, fmt.Errorf("processo %s: %w", pp.Process, err)
		}
		if applied {
			res.Processes = append(res.Processes, pp.Process)
		} else {
			res.Skipped = append(res.Skipped, pp.Process)
		}
	}
	return res, auditCfg(ctx, tx, actor, "review_config.import", "review_config", "", map[string]any{
		"teams": len(res.Teams), "templates": len(res.Templates), "types": len(res.Types), "processes": res.Processes})
}

// applyPatch aggiunge al processo ciò che manca e pubblica una nuova versione. Non fa nulla
// se il processo non esiste, se ha una bozza aperta o se c'è già tutto.
func (s *Service) applyPatch(ctx context.Context, tx pgx.Tx, actor engine.Actor, pp ProcessPatch) (bool, error) {
	adm := actor
	adm.Permissions = append(slices.Clone(actor.Permissions), engine.PermProcessAdmin)
	pi, err := s.eng.GetProcess(ctx, tx, adm, pp.Process)
	if errors.Is(err, engine.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if pi.Draft != nil || pi.Current == nil {
		return false, nil
	}
	raw, _ := json.Marshal(pi.Current)
	w, err := engine.DecodeDefinition(raw)
	if err != nil {
		return false, err
	}
	changed := false
	for _, g := range pp.Gates {
		if !slices.ContainsFunc(w.Gates, func(x engine.Gate) bool { return x.Key == g.Key }) {
			w.Gates = append(w.Gates, g)
			changed = true
		}
	}
	for _, tg := range pp.TransitionGates {
		for i := range w.Transitions {
			t := &w.Transitions[i]
			if t.System || !strings.EqualFold(t.To, tg.To) || slices.Contains(t.Gates, tg.Gate) {
				continue
			}
			t.Gates = append(t.Gates, tg.Gate)
			changed = true
		}
	}
	for _, r := range pp.Rules {
		if !slices.ContainsFunc(w.Rules, func(x engine.Rule) bool { return x.Key == r.Key }) {
			w.Rules = append(w.Rules, r)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	def, _ := json.Marshal(w)
	notes := pp.Notes
	if notes == "" {
		notes = "Valutazioni"
	}
	if _, err := s.eng.SaveDraft(ctx, tx, adm, pi.ID, def, notes); err != nil {
		return false, err
	}
	if _, problems, err := s.eng.Publish(ctx, tx, adm, pi.ID, notes); err != nil {
		return false, fmt.Errorf("%w %v", err, problems)
	}
	return true, nil
}

// Export restituisce la configurazione corrente come pacchetto (senza le modifiche ai processi,
// che stanno nell'export dei processi).
func (s *Service) Export(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Package, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	p := &Package{Format: PackageFormat, ExportedAt: time.Now().UTC().Format(time.RFC3339), Teams: []PackageTeam{}, Templates: []PackageTemplate{}, Types: []PackageType{}}
	teams, err := s.Teams(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	for _, t := range teams {
		if t.Archived {
			continue
		}
		esc := 0
		if t.EscalationDays != nil {
			esc = *t.EscalationDays
		}
		p.Teams = append(p.Teams, PackageTeam{Key: t.Key, Name: t.Name, Description: t.Description, Group: t.Group,
			SLADays: t.SLADays, ReminderDays: t.ReminderDays, EscalationDays: esc})
		if !slices.Contains(p.Groups, t.Group) {
			p.Groups = append(p.Groups, t.Group)
		}
	}
	rows, err := tx.Query(ctx, `select key, definition, notes from questionnaire_templates where status = 'published' order by key`)
	if err != nil {
		return nil, err
	}
	var key, notes string
	var def []byte
	if _, err := pgx.ForEachRow(rows, []any{&key, &def, &notes}, func() error {
		p.Templates = append(p.Templates, PackageTemplate{Key: key, Notes: notes, Definition: slices.Clone(def)})
		return nil
	}); err != nil {
		return nil, err
	}
	types, err := s.Types(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		if t.Archived {
			continue
		}
		var teamKey string
		for _, tm := range teams {
			if tm.ID == t.TeamID {
				teamKey = tm.Key
			}
		}
		p.Types = append(p.Types, PackageType{Key: t.Key, Name: t.Name, Description: t.Description, Team: teamKey, Scope: t.Scope,
			TemplateKey: t.TemplateKey, Mandatory: t.Mandatory, Deciders: t.Deciders, Outcomes: t.Outcomes, OwnerRoles: t.OwnerRoles})
	}
	return p, nil
}

// Provision configura il modulo in un tenant: tipo "review" nello spazio delle richieste,
// ruoli, processo della valutazione e, la prima volta, il pacchetto di partenza (team,
// modelli, tipi, regole e gate nei processi della domanda e dell'impatto). È idempotente
// e va eseguito dopo demand e architecture. Il responsabile dei team è l'amministratore,
// finché non se ne indica un altro.
func Provision(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	exec := func(sql string, args ...any) error { _, err := tx.Exec(ctx, sql, args...); return err }
	var typeID string
	err := tx.QueryRow(ctx, "select id from element_types where key = $1", TypeKey).Scan(&typeID)
	if errors.Is(err, pgx.ErrNoRows) {
		typeID = db.NewID()
		err = exec(`insert into element_types (tenant_id, id, key, name, module, extension, is_system) values (app_tenant(), $1, $2, 'Valutazione', 'strategy', 'review', true)`,
			typeID, TypeKey)
	}
	if err != nil {
		return err
	}
	if err := exec(`insert into space_types (tenant_id, space_id, type_id)
		select app_tenant(), s.id, $2 from spaces s where s.key = $1 on conflict do nothing`, SpaceKey, typeID); err != nil {
		return err
	}
	for _, r := range roles {
		if err := exec(`insert into stakeholder_roles (tenant_id, id, key, name) values (app_tenant(), $1, $2, $3) on conflict do nothing`,
			db.NewID(), r.key, r.name); err != nil {
			return err
		}
	}
	eng := engine.New()
	s := Register(eng)
	actor := engine.Actor{MemberID: adminMemberID, Permissions: []string{"*"}}
	var hasProcess bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from process_definitions where type_id = $1)`, typeID).Scan(&hasProcess); err != nil {
		return err
	}
	if !hasProcess {
		raw, err := starters.ReadFile("starter/review_standard.json")
		if err != nil {
			return err
		}
		b, err := engine.ParseBundle(raw)
		if err != nil {
			return err
		}
		if _, problems, err := eng.Import(ctx, tx, actor, b, true); err != nil {
			return fmt.Errorf("processo della valutazione: %w %v", err, problems)
		}
	}
	var configured bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from review_types)").Scan(&configured); err != nil {
		return err
	}
	if configured {
		return nil
	}
	raw, err := starters.ReadFile("starter/valutazioni_standard.json")
	if err != nil {
		return err
	}
	p, err := ParsePackage(raw)
	if err != nil {
		return err
	}
	if _, err := s.Import(ctx, tx, actor, p, true); err != nil {
		return fmt.Errorf("pacchetto delle valutazioni: %w", err)
	}
	return exec(`update review_teams set lead_id = $1 where lead_id is null`, adminMemberID)
}
