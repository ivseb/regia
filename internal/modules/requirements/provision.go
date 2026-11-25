package requirements

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
	"regia/internal/engine/approval"
	"regia/internal/modules/reviews"
	"regia/internal/platform/db"
)

//go:embed starter/*.json
var starters embed.FS

// PackageFormat identifica il formato di export e import della configurazione dei requisiti.
const PackageFormat = "regia.requirements/v1"

var types = []struct{ key, name, ext string }{
	{TypeKey, "Requisito", "requirement"},
	{ExceptionType, "Deroga", "exception"},
}

var roles = []struct{ key, name string }{
	{RoleOwner, "Responsabile del requisito"},
	{RoleVerifier, "Verificatore del requisito"},
	{RoleRequester, "Richiedente della deroga"},
}

// Package è la configurazione dei requisiti in JSON: impostazioni delle deroghe, politica
// di approvazione, cataloghi e modifiche ai processi. È il formato del pacchetto di
// partenza e dell'export.
type Package struct {
	Format          string           `json:"format"`
	Name            string           `json:"name,omitempty"`
	Description     string           `json:"description,omitempty"`
	ExportedAt      string           `json:"exported_at,omitempty"`
	Settings        *Settings        `json:"settings,omitempty"`
	ExceptionPolicy []approval.Rule  `json:"exception_policy,omitempty"`
	Catalogs        []PackageCatalog `json:"catalogs"`
	ProcessPatches  []ProcessPatch   `json:"process_patches,omitempty"`
}

// PackageCatalog è un catalogo nel pacchetto.
type PackageCatalog struct {
	Key        string          `json:"key"`
	Notes      string          `json:"notes,omitempty"`
	Definition json.RawMessage `json:"definition"`
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
	Catalogs  []string `json:"catalogs"`
	Processes []string `json:"processes"`
	Skipped   []string `json:"skipped"`
	Settings  bool     `json:"settings"`
	Policy    bool     `json:"policy"`
}

// Import porta un pacchetto nel tenant: impostazioni e politica (se presenti), ogni catalogo
// come bozza (pubblicata con publish), modifiche ai processi come nuova versione (saltate se
// il processo ha una bozza in lavorazione, per non sovrascriverla).
func (s *Service) Import(ctx context.Context, tx pgx.Tx, actor engine.Actor, p *Package, publish bool) (*ImportResult, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	res := &ImportResult{Catalogs: []string{}, Processes: []string{}, Skipped: []string{}}
	if p.Settings != nil {
		if _, err := s.UpdateSettings(ctx, tx, actor, *p.Settings); err != nil {
			return nil, fmt.Errorf("impostazioni: %w", err)
		}
		res.Settings = true
	}
	if len(p.ExceptionPolicy) > 0 {
		if _, err := s.UpdatePolicy(ctx, tx, actor, p.ExceptionPolicy); err != nil {
			return nil, fmt.Errorf("politica delle deroghe: %w", err)
		}
		res.Policy = true
	}
	for _, c := range p.Catalogs {
		if !keyRe.MatchString(c.Key) {
			return nil, engine.InvalidValue("catalogs", "chiave del catalogo %q non valida", c.Key)
		}
		d, err := DecodeDefinition(c.Definition)
		if err != nil {
			return nil, fmt.Errorf("catalogo %s: %w", c.Key, err)
		}
		notes := c.Notes
		if notes == "" {
			notes = "Importato"
		}
		if err := saveDraft(ctx, tx, actor, c.Key, d, notes); err != nil {
			return nil, err
		}
		if publish {
			if _, err := s.Publish(ctx, tx, actor, c.Key, ""); err != nil {
				return nil, fmt.Errorf("catalogo %s: %w", c.Key, err)
			}
		}
		res.Catalogs = append(res.Catalogs, c.Key)
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
	return res, audited(ctx, tx, actor, "requirement_config.import", "requirement_config", "", map[string]any{
		"catalogs": res.Catalogs, "processes": res.Processes, "settings": res.Settings, "policy": res.Policy})
}

// Export restituisce la configurazione corrente come pacchetto (senza le modifiche ai
// processi, che stanno nell'export dei processi).
func (s *Service) Export(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Package, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	pol, err := s.GetPolicy(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	p := &Package{Format: PackageFormat, ExportedAt: time.Now().UTC().Format(time.RFC3339), Settings: st, ExceptionPolicy: pol.Rules,
		Catalogs: []PackageCatalog{}}
	rows, err := tx.Query(ctx, `select key, definition, notes from requirement_catalogs where status = 'published' order by key`)
	if err != nil {
		return nil, err
	}
	var key, notes string
	var def []byte
	if _, err := pgx.ForEachRow(rows, []any{&key, &def, &notes}, func() error {
		p.Catalogs = append(p.Catalogs, PackageCatalog{Key: key, Notes: notes, Definition: slices.Clone(def)})
		return nil
	}); err != nil {
		return nil, err
	}
	return p, nil
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
		notes = "Requisiti"
	}
	if _, err := s.eng.SaveDraft(ctx, tx, adm, pi.ID, def, notes); err != nil {
		return false, err
	}
	if _, problems, err := s.eng.Publish(ctx, tx, adm, pi.ID, notes); err != nil {
		return false, fmt.Errorf("%w %v", err, problems)
	}
	return true, nil
}

// Provision configura il modulo in un tenant: tipi "requirement" ed "exception" nello spazio
// delle richieste, ruoli, relazione "nasce da", processi del requisito e della deroga e, la
// prima volta, il pacchetto di partenza (impostazioni, politica delle deroghe, catalogo con
// le baseline, regola delle baseline sulla domanda e gate di rilascio sull'impatto). È
// idempotente e va eseguito dopo demand, architecture e reviews.
func Provision(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	exec := func(sql string, args ...any) error { _, err := tx.Exec(ctx, sql, args...); return err }
	for _, t := range types {
		var typeID string
		err := tx.QueryRow(ctx, "select id from element_types where key = $1", t.key).Scan(&typeID)
		if errors.Is(err, pgx.ErrNoRows) {
			typeID = db.NewID()
			err = exec(`insert into element_types (tenant_id, id, key, name, module, extension, is_system) values (app_tenant(), $1, $2, $3, 'strategy', $4, true)`,
				typeID, t.key, t.name, t.ext)
		}
		if err != nil {
			return err
		}
		if err := exec(`insert into space_types (tenant_id, space_id, type_id)
			select app_tenant(), s.id, $2 from spaces s where s.key = $1 on conflict do nothing`, SpaceKey, typeID); err != nil {
			return err
		}
	}
	for _, r := range roles {
		if err := exec(`insert into stakeholder_roles (tenant_id, id, key, name) values (app_tenant(), $1, $2, $3) on conflict do nothing`,
			db.NewID(), r.key, r.name); err != nil {
			return err
		}
	}
	if err := exec(`insert into relation_types (tenant_id, id, key, outward_name, inward_name, is_system)
		values (app_tenant(), $1, $2, 'nasce da', 'ha generato', true) on conflict do nothing`, db.NewID(), RelOrigin); err != nil {
		return err
	}
	// Un motore con tutti i moduli che le versioni dei processi citano (azioni e controlli),
	// così la pubblicazione delle modifiche valida i riferimenti.
	eng := engine.New()
	ap := approval.Register(eng)
	reviews.Register(eng)
	s := Register(eng, ap)
	actor := engine.Actor{MemberID: adminMemberID, Permissions: []string{"*"}}
	for _, f := range []string{"requirement_standard.json", "exception_standard.json"} {
		raw, err := starters.ReadFile("starter/" + f)
		if err != nil {
			return err
		}
		b, err := engine.ParseBundle(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		var has bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from process_definitions p join element_types t on t.tenant_id = p.tenant_id and t.id = p.type_id
			where t.key = $1)`, b.Type).Scan(&has); err != nil {
			return err
		}
		if has {
			continue
		}
		if _, problems, err := eng.Import(ctx, tx, actor, b, true); err != nil {
			return fmt.Errorf("processo %s: %w %v", f, err, problems)
		}
	}
	var configured bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from requirement_catalogs) or exists (select 1 from requirement_settings)").Scan(&configured); err != nil {
		return err
	}
	if configured {
		return nil
	}
	raw, err := starters.ReadFile("starter/requisiti_standard.json")
	if err != nil {
		return err
	}
	p, err := ParsePackage(raw)
	if err != nil {
		return err
	}
	if _, err := s.Import(ctx, tx, actor, p, true); err != nil {
		return fmt.Errorf("pacchetto dei requisiti: %w", err)
	}
	return nil
}
