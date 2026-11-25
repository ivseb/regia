package devsecops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/modules/grc"
)

// Evidenze automatiche dei controlli GRC (grc.md; devsecops.md §3.5). I rilasci che superano
// controlli in modalità block e le decisioni di gate superate diventano evidenze dei controlli
// GRC mappati da Configurazione → Gate di rilascio, con una scadenza.

const (
	CollectorRelease = "devsecops.release"
	CollectorGate    = "devsecops.gate"
	sourceGate       = "gate"
)

var checkNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._ -]{0,58}$`)

// EvidenceMapItem è una riga della mappatura: una sorgente (un controllo della pipeline o la
// decisione di gate superata) e il controllo GRC che ne riceve l'evidenza.
type EvidenceMapItem struct {
	Source         string `json:"source"` // "check:<nome>" | "gate"
	Control        string `json:"control"`
	ControlTitle   string `json:"control_title,omitempty"`
	ValidDays      *int   `json:"valid_days"`
	ProductionOnly bool   `json:"production_only"`
}

type controlMap struct {
	key       string
	validDays *int
}

// sysGRC è l'attore con cui si registrano le evidenze automatiche: la pipeline ha solo i
// permessi di invio, ma la mappatura l'ha decisa chi configura.
func sysGRC(actor engine.Actor) engine.Actor {
	return engine.Actor{AccountID: actor.AccountID, MemberID: actor.MemberID, IP: actor.IP, Permissions: []string{"*"}}
}

func (s *Service) mapped(ctx context.Context, tx pgx.Tx, sources []string, production bool) (map[string][]controlMap, error) {
	out := map[string][]controlMap{}
	if len(sources) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `select m.source, sp.key || '-' || e.number, m.valid_days
		from gate_evidence_map m join elements e on e.tenant_id = m.tenant_id and e.id = m.control_element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		where m.source = any($1) and ($2 or not m.production_only) order by m.source, e.number`, sources, production)
	if err != nil {
		return nil, err
	}
	var src, key string
	var days *int
	_, err = pgx.ForEachRow(rows, []any{&src, &key, &days}, func() error {
		out[src] = append(out[src], controlMap{key, days})
		return nil
	})
	return out, err
}

func (s *Service) appKey(ctx context.Context, tx pgx.Tx, id string) (string, error) {
	var k string
	err := tx.QueryRow(ctx, `select sp.key || '-' || e.number from elements e join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id where e.id = $1`, id).Scan(&k)
	return k, err
}

// record registra le evidenze di una sorgente raggruppando i controlli per durata di validità.
func (s *Service) recordEvidence(ctx context.Context, tx pgx.Tx, actor engine.Actor, collector, ref, title, summary, link, appKey string,
	at time.Time, maps []controlMap) error {
	groups := map[int][]string{}
	for _, m := range maps {
		d := 0
		if m.validDays != nil {
			d = *m.validDays
		}
		if !slices.Contains(groups[d], m.key) {
			groups[d] = append(groups[d], m.key)
		}
	}
	days := make([]int, 0, len(groups))
	for d := range groups {
		days = append(days, d)
	}
	slices.Sort(days)
	for _, d := range days {
		in := grc.AutoEvidence{Collector: collector, ExternalRef: ref, Title: title, Summary: summary, URL: link,
			Application: appKey, Controls: groups[d], OccurredAt: &at}
		if d > 0 {
			in.ExternalRef = fmt.Sprintf("%s:%dg", ref, d)
			in.ValidUntil = at.AddDate(0, 0, d).Format(time.DateOnly)
		}
		if len(in.ExternalRef) > 200 {
			in.ExternalRef = in.ExternalRef[:200]
		}
		_, err := s.grc.RecordAutomatic(ctx, tx, sysGRC(actor), in)
		var ve *engine.ValidationError
		if err != nil && !errors.As(err, &ve) {
			return err
		}
	}
	return nil
}

// releaseEvidence: i controlli block superati di un rilascio diventano evidenze dei controlli
// GRC mappati. Un'evidenza per rilascio e durata di validità; ripetere l'invio non la duplica.
func (s *Service) releaseEvidence(ctx context.Context, tx pgx.Tx, actor engine.Actor, appID, releaseID, service, version, env string, production bool,
	checks []contract.CheckResult, at time.Time, releaseURL, change string) ([]string, error) {
	if s.grc == nil || appID == "" {
		return nil, nil
	}
	passed := blockPassed(checks)
	sources := make([]string, len(passed))
	for i, n := range passed {
		sources[i] = "check:" + strings.ToLower(n)
	}
	m, err := s.mapped(ctx, tx, sources, production)
	if err != nil || len(m) == 0 {
		return nil, err
	}
	keys := []string{}
	var all []controlMap
	names := []string{}
	for i, src := range sources {
		if len(m[src]) > 0 {
			all = append(all, m[src]...)
			names = append(names, passed[i])
		}
	}
	for _, c := range all {
		if !slices.Contains(keys, c.key) {
			keys = append(keys, c.key)
		}
	}
	appKey, err := s.appKey(ctx, tx, appID)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(strings.Join(names, ",")))
	ref := "release:" + releaseID + ":" + hex.EncodeToString(sum[:4])
	summary := fmt.Sprintf("Il rilascio %s %s in %s del %s ha superato in modalità block i controlli: %s.", service, version, env, at.Format("02/01/2006"), strings.Join(names, ", "))
	if change != "" {
		summary += " Change " + change + "."
	}
	title := fmt.Sprintf("Rilascio %s %s in %s: controlli superati", service, version, env)
	if err := s.recordEvidence(ctx, tx, actor, CollectorRelease, ref, title, summary, cleanURL(releaseURL), appKey, at, all); err != nil {
		return nil, err
	}
	return keys, nil
}

// gateEvidence: una decisione di gate superata è un'evidenza dei controlli mappati su "gate".
func (s *Service) gateEvidence(ctx context.Context, tx pgx.Tx, actor engine.Actor, g *gateCtx, out *GateOutcome) error {
	if s.grc == nil {
		return nil
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return err
	}
	m, err := s.mapped(ctx, tx, []string{sourceGate}, st.isProduction(g.env))
	if err != nil || len(m[sourceGate]) == 0 {
		return err
	}
	rules := []string{}
	for _, r := range out.Reasons {
		if r.Label != "" && !slices.Contains(rules, r.Label) {
			rules = append(rules, r.Label)
		}
	}
	summary := fmt.Sprintf("Il gate di rilascio ha dato il via libera a %s in %s%s. Regole soddisfatte: %s.",
		g.app.Key, g.env, versionText(g.in.Version), strings.Join(rules, ", "))
	if g.req != nil {
		summary += " Richiesta " + g.req.Key + "."
	}
	title := fmt.Sprintf("Gate superato: %s in %s", g.app.Key, g.env)
	return s.recordEvidence(ctx, tx, actor, CollectorGate, "gate:"+out.DecisionID, title, summary, s.elementLink(g.app.Key, "gate"), g.app.Key, g.now, m[sourceGate])
}

func versionText(v string) string {
	if v == "" {
		return ""
	}
	return " (versione " + v + ")"
}

// ---------------------------------------------------------------------------
// Configurazione della mappatura

// EvidenceConfig è la mappatura con quanto serve a modificarla.
type EvidenceConfig struct {
	Items      []EvidenceMapItem `json:"items"`
	Controls   []ControlOption   `json:"controls"`
	CheckNames []string          `json:"check_names"`
	Enabled    bool              `json:"enabled"` // il modulo della conformità è attivo nel tenant
}

// ControlOption è un controllo GRC scegliibile.
type ControlOption struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

func (s *Service) evidenceConfig(ctx context.Context, tx pgx.Tx) (*EvidenceConfig, error) {
	cfg := &EvidenceConfig{Items: []EvidenceMapItem{}, Controls: []ControlOption{}, CheckNames: []string{}, Enabled: s.grc != nil}
	rows, err := tx.Query(ctx, `select m.source, sp.key || '-' || e.number, e.title, m.valid_days, m.production_only
		from gate_evidence_map m join elements e on e.tenant_id = m.tenant_id and e.id = m.control_element_id and e.deleted_at is null
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id order by m.source, e.number`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var it EvidenceMapItem
		if err := rows.Scan(&it.Source, &it.Control, &it.ControlTitle, &it.ValidDays, &it.ProductionOnly); err != nil {
			rows.Close()
			return nil, err
		}
		cfg.Items = append(cfg.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	crows, err := tx.Query(ctx, `select sp.key || '-' || e.number, e.title from elements e
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'control'
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id where e.deleted_at is null order by e.number limit 500`)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var c ControlOption
		if err := crows.Scan(&c.Key, &c.Title); err != nil {
			crows.Close()
			return nil, err
		}
		cfg.Controls = append(cfg.Controls, c)
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}
	nrows, err := tx.Query(ctx, `select distinct lower(c->>'name') from (
			select checks from releases order by released_at desc limit 300) r, jsonb_array_elements(r.checks) c
		union select distinct lower(c->>'name') from (select checks from check_runs order by at desc limit 300) k, jsonb_array_elements(k.checks) c
		order by 1 limit 100`)
	if err != nil {
		return nil, err
	}
	cfg.CheckNames, err = pgx.CollectRows(nrows, pgx.RowTo[string])
	return cfg, err
}

// SaveEvidenceMap sostituisce la mappatura.
func (s *Service) SaveEvidenceMap(ctx context.Context, tx pgx.Tx, actor engine.Actor, items []EvidenceMapItem) (*EvidenceConfig, error) {
	if !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	if len(items) > 300 {
		return nil, engine.InvalidCode("validation.devsecops.evidence_too_many", "items", "al massimo 300 righe")
	}
	type row struct {
		source, control string
		days            *int
		prod            bool
	}
	rows := []row{}
	seen := map[string]bool{}
	for i, it := range items {
		src := strings.ToLower(strings.TrimSpace(it.Source))
		if name, ok := strings.CutPrefix(src, "check:"); ok {
			name = strings.TrimSpace(name)
			if !checkNameRe.MatchString(name) {
				return nil, engine.InvalidValue("items", "riga %d: nome del controllo non valido", i+1)
			}
			src = "check:" + name
		} else if src != sourceGate {
			return nil, engine.InvalidParams("validation.devsecops.evidence_source", "items", map[string]any{"row": i + 1}, "riga %d: la sorgente è un controllo della pipeline o la decisione di gate", i+1)
		}
		if it.ValidDays != nil && (*it.ValidDays < 1 || *it.ValidDays > 3650) {
			return nil, engine.InvalidParams("validation.devsecops.evidence_validity", "items", map[string]any{"row": i + 1}, "riga %d: la validità va da 1 a 3650 giorni", i+1)
		}
		var id string
		err := tx.QueryRow(ctx, `select e.id::text from elements e join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
			join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'control'
			where e.deleted_at is null and sp.key || '-' || e.number = upper($1)`, strings.TrimSpace(it.Control)).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.NotFound("items", "", "riga %d: controllo %q inesistente", i+1, it.Control)
		}
		if err != nil {
			return nil, err
		}
		if seen[src+"|"+id] {
			continue
		}
		seen[src+"|"+id] = true
		rows = append(rows, row{src, id, it.ValidDays, it.ProductionOnly})
	}
	if _, err := tx.Exec(ctx, "delete from gate_evidence_map"); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if _, err := tx.Exec(ctx, `insert into gate_evidence_map (tenant_id, source, control_element_id, valid_days, production_only, created_by)
			values (app_tenant(), $1, $2, $3, $4, $5)`, r.source, r.control, r.days, r.prod, nullable(actor.MemberID)); err != nil {
			return nil, err
		}
	}
	if err := auditEvent(ctx, tx, actor, "devsecops.gate_evidence_map", "gate_evidence_map", "", map[string]any{"items": len(rows)}); err != nil {
		return nil, err
	}
	return s.evidenceConfig(ctx, tx)
}
