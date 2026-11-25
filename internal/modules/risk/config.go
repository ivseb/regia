package risk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/platform/db"
)

// Configurazione del modulo: matrici di rischio, impostazioni (preavvisi e colonne del
// registro), politica di accettazione, schemi di classificazione degli incidenti. Si
// esporta e si importa come pacchetto JSON "regia.risk/v1" (anche il pacchetto di partenza).

// PackageFormat identifica il formato del pacchetto di configurazione.
const PackageFormat = "regia.risk/v1"

var keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Toni del tema per le fasce di rischio (mai colori: li decide il tema attivo). L'arancione
// resta agli obblighi normativi, quindi non è tra i toni ammessi.
var tones = []string{"ok", "neutral", "accent", "danger"}

// Level è un livello di probabilità o di impatto.
type Level struct {
	Value       int    `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Band è una fascia di punteggio (probabilità × impatto) con il suo livello.
type Band struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Min  int    `json:"min"`
	Max  int    `json:"max"`
	Tone string `json:"tone"`
}

// Matrix è una matrice di rischio.
type Matrix struct {
	ID          string  `json:"id,omitempty"`
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Default     bool    `json:"default"`
	Appetite    *int    `json:"appetite_score"`
	Likelihood  []Level `json:"likelihood"`
	Impact      []Level `json:"impact"`
	Bands       []Band  `json:"bands"`
}

// MaxScore è il punteggio massimo della matrice.
func (m *Matrix) MaxScore() int { return len(m.Likelihood) * len(m.Impact) }

// Band restituisce la fascia di un punteggio (nil se fuori dalle fasce).
func (m *Matrix) Band(score int) *Band {
	for i := range m.Bands {
		if score >= m.Bands[i].Min && score <= m.Bands[i].Max {
			return &m.Bands[i]
		}
	}
	return nil
}

// Over dice se il punteggio supera la soglia di attenzione.
func (m *Matrix) Over(score int) bool { return m.Appetite != nil && score > *m.Appetite }

func validLevels(field string, ls []Level) error {
	if len(ls) < 2 || len(ls) > 7 {
		return engine.Required(field, "servono da 2 a 7 livelli")
	}
	for i := range ls {
		ls[i].Name = strings.TrimSpace(ls[i].Name)
		ls[i].Description = strings.TrimSpace(ls[i].Description)
		if ls[i].Value != i+1 {
			return engine.InvalidParams("validation.risk.levels_order", field, map[string]any{"n": len(ls)}, "i valori dei livelli vanno da 1 a %d, in ordine", len(ls))
		}
		if ls[i].Name == "" || len(ls[i].Name) > 60 || len(ls[i].Description) > 500 {
			return engine.TooLong(field, 60, "il livello %d vuole un nome (massimo 60 caratteri)", i+1)
		}
	}
	return nil
}

// Validate controlla la matrice: livelli consecutivi, fasce contigue che coprono tutti i
// punteggi da 1 al massimo, toni del tema, soglia nel campo dei punteggi.
func (m *Matrix) Validate() error {
	m.Name, m.Description = strings.TrimSpace(m.Name), strings.TrimSpace(m.Description)
	if !keyRe.MatchString(m.Key) {
		return engine.InvalidValue("key", "chiave della matrice non valida (minuscole, cifre e trattini bassi)")
	}
	if m.Name == "" || len(m.Name) > 120 {
		return engine.TooLong("name", 120, "il nome della matrice è obbligatorio (massimo 120 caratteri)")
	}
	if err := validLevels("likelihood", m.Likelihood); err != nil {
		return err
	}
	if err := validLevels("impact", m.Impact); err != nil {
		return err
	}
	if len(m.Bands) < 2 || len(m.Bands) > 6 {
		return engine.Required("bands", "servono da 2 a 6 fasce di punteggio")
	}
	next, seen := 1, map[string]bool{}
	for i := range m.Bands {
		b := &m.Bands[i]
		b.Name = strings.TrimSpace(b.Name)
		if !keyRe.MatchString(b.Key) || seen[b.Key] {
			return engine.InvalidValue("bands", "chiave della fascia %q non valida o ripetuta", b.Key)
		}
		seen[b.Key] = true
		if b.Name == "" || len(b.Name) > 60 {
			return engine.InvalidParams("validation.risk.band_name_missing", "bands", map[string]any{"key": b.Key}, "la fascia %s vuole un nome", b.Key)
		}
		if !slices.Contains(tones, b.Tone) {
			return engine.InvalidValue("bands", "tono della fascia %s non ammesso (%s)", b.Key, strings.Join(tones, ", "))
		}
		if b.Min != next || b.Max < b.Min {
			return engine.InvalidParams("validation.risk.band_gap", "bands", map[string]any{"key": b.Key, "start": next}, "le fasce vanno in ordine, senza buchi né sovrapposizioni: la fascia %s deve iniziare da %d", b.Key, next)
		}
		next = b.Max + 1
	}
	if next-1 != m.MaxScore() {
		return engine.InvalidParams("validation.risk.band_last", "bands", map[string]any{"max": m.MaxScore()}, "l'ultima fascia deve arrivare al punteggio massimo %d", m.MaxScore())
	}
	if m.Appetite != nil && (*m.Appetite < 0 || *m.Appetite > m.MaxScore()) {
		return engine.InvalidParams("validation.risk.appetite_range", "appetite_score", map[string]any{"max": m.MaxScore()}, "la soglia di attenzione è tra 0 e %d", m.MaxScore())
	}
	return nil
}

const selectMatrix = `select id::text, coalesce(key, ''), name, description, is_default, appetite_score, likelihood_levels, impact_levels, score_bands from risk_matrices`

func scanMatrix(row pgx.Row) (*Matrix, error) {
	m := &Matrix{}
	var l, i, b []byte
	if err := row.Scan(&m.ID, &m.Key, &m.Name, &m.Description, &m.Default, &m.Appetite, &l, &i, &b); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	if err := errors.Join(json.Unmarshal(l, &m.Likelihood), json.Unmarshal(i, &m.Impact), json.Unmarshal(b, &m.Bands)); err != nil {
		return nil, err
	}
	return m, nil
}

// Matrices elenca le matrici del tenant, la predefinita per prima.
func (s *Service) Matrices(ctx context.Context, tx pgx.Tx) ([]*Matrix, error) {
	rows, err := tx.Query(ctx, selectMatrix+` order by is_default desc, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Matrix{}
	for rows.Next() {
		m, err := scanMatrix(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func matrixByID(ctx context.Context, tx pgx.Tx, id string) (*Matrix, error) {
	return scanMatrix(tx.QueryRow(ctx, selectMatrix+` where id = $1`, id))
}

// matrixFor trova la matrice per chiave o id; vuoto = la predefinita.
func matrixFor(ctx context.Context, tx pgx.Tx, ref string) (*Matrix, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		m, err := scanMatrix(tx.QueryRow(ctx, selectMatrix+` order by is_default desc, name limit 1`))
		if errors.Is(err, engine.ErrNotFound) {
			return nil, engine.InvalidCode("validation.risk.matrix_missing", "matrix", "nessuna matrice di rischio configurata")
		}
		return m, err
	}
	m, err := scanMatrix(tx.QueryRow(ctx, selectMatrix+` where key = $1 or id::text = $1`, ref))
	if errors.Is(err, engine.ErrNotFound) {
		return nil, engine.NotFound("matrix", "", "matrice %q inesistente", ref)
	}
	return m, err
}

// SaveMatrix crea o sostituisce la matrice con la chiave indicata (risk.admin). I rischi che
// la usano devono restare nei nuovi livelli; i punteggi salvati si ricalcolano.
func (s *Service) SaveMatrix(ctx context.Context, tx pgx.Tx, actor engine.Actor, m Matrix) (*Matrix, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	var id string
	err := tx.QueryRow(ctx, "select id::text from risk_matrices where key = $1", m.Key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		id = ""
	} else if err != nil {
		return nil, err
	}
	if id != "" {
		var out int
		if err := tx.QueryRow(ctx, `select count(*) from ext_risk where matrix_id = $1 and (
			greatest(coalesce(inherent_likelihood, 0), coalesce(residual_likelihood, 0)) > $2 or
			greatest(coalesce(inherent_impact, 0), coalesce(residual_impact, 0)) > $3)`, id, len(m.Likelihood), len(m.Impact)).Scan(&out); err != nil {
			return nil, err
		}
		if out > 0 {
			return nil, engine.InvalidParams("validation.risk.matrix_levels_in_use", "likelihood", map[string]any{"n": out}, "%d rischi usano livelli che la nuova matrice non ha: rivalutali prima", out)
		}
	}
	var hasDefault bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from risk_matrices where is_default and key is distinct from $1)", m.Key).Scan(&hasDefault); err != nil {
		return nil, err
	}
	if !hasDefault {
		m.Default = true // la prima matrice (o l'unica) è la predefinita
	}
	if m.Default {
		if _, err := tx.Exec(ctx, "update risk_matrices set is_default = false where is_default and key is distinct from $1", m.Key); err != nil {
			return nil, err
		}
	}
	l, _ := json.Marshal(m.Likelihood)
	im, _ := json.Marshal(m.Impact)
	b, _ := json.Marshal(m.Bands)
	if id == "" {
		id = db.NewID()
		_, err = tx.Exec(ctx, `insert into risk_matrices (tenant_id, id, key, name, description, is_default, appetite_score, likelihood_levels, impact_levels, score_bands, updated_by)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, id, m.Key, m.Name, m.Description, m.Default, m.Appetite, l, im, b, nullable(actor.MemberID))
	} else {
		_, err = tx.Exec(ctx, `update risk_matrices set name = $2, description = $3, is_default = $4, appetite_score = $5, likelihood_levels = $6,
			impact_levels = $7, score_bands = $8, updated_by = $9, updated_at = now() where id = $1`,
			id, m.Name, m.Description, m.Default, m.Appetite, l, im, b, nullable(actor.MemberID))
	}
	if err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "risk_matrix.save", "risk_matrix", id, map[string]any{"key": m.Key,
		"likelihood": len(m.Likelihood), "impact": len(m.Impact), "bands": len(m.Bands), "appetite": m.Appetite}); err != nil {
		return nil, err
	}
	return matrixByID(ctx, tx, id)
}

// ---------------------------------------------------------------------------
// Impostazioni

// Column è una colonna dell'export del registro delle informazioni.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// Settings sono le impostazioni del modulo.
type Settings struct {
	RegisterColumns          []Column `json:"register_columns"`
	ContractNoticeDays       int      `json:"contract_notice_days"`
	ReviewNoticeDays         int      `json:"review_notice_days"`
	NotificationWarningHours int      `json:"notification_warning_hours"`
}

func loadSettings(ctx context.Context, tx pgx.Tx) (*Settings, error) {
	st := &Settings{RegisterColumns: defaultColumns(), ContractNoticeDays: 90, ReviewNoticeDays: 14, NotificationWarningHours: 4}
	var raw []byte
	err := tx.QueryRow(ctx, `select register_columns, contract_notice_days, review_notice_days, notification_warning_hours from risk_settings`).
		Scan(&raw, &st.ContractNoticeDays, &st.ReviewNoticeDays, &st.NotificationWarningHours)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	var cols []Column
	if err := json.Unmarshal(raw, &cols); err != nil {
		return nil, err
	}
	if len(cols) > 0 {
		st.RegisterColumns = cols
	}
	return st, nil
}

// UpdateSettings salva le impostazioni (risk.admin).
func (s *Service) UpdateSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in Settings) (*Settings, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	cols, err := validColumns(in.RegisterColumns)
	if err != nil {
		return nil, err
	}
	if in.ContractNoticeDays < 0 || in.ContractNoticeDays > 730 {
		return nil, engine.OutOfRange("contract_notice_days", 0, 730, "preavviso dei contratti tra 0 e 730 giorni")
	}
	if in.ReviewNoticeDays < 0 || in.ReviewNoticeDays > 180 {
		return nil, engine.OutOfRange("review_notice_days", 0, 180, "preavviso delle revisioni tra 0 e 180 giorni")
	}
	if in.NotificationWarningHours < 0 || in.NotificationWarningHours > 168 {
		return nil, engine.OutOfRange("notification_warning_hours", 0, 168, "preavviso delle notifiche tra 0 e 168 ore")
	}
	raw, _ := json.Marshal(cols)
	if _, err := tx.Exec(ctx, `insert into risk_settings (tenant_id, register_columns, contract_notice_days, review_notice_days, notification_warning_hours, updated_by)
		values (app_tenant(), $1, $2, $3, $4, $5)
		on conflict (tenant_id) do update set register_columns = excluded.register_columns, contract_notice_days = excluded.contract_notice_days,
		  review_notice_days = excluded.review_notice_days, notification_warning_hours = excluded.notification_warning_hours,
		  updated_by = excluded.updated_by, updated_at = now()`,
		raw, in.ContractNoticeDays, in.ReviewNoticeDays, in.NotificationWarningHours, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "risk_settings.update", "risk_settings", "", map[string]any{"columns": len(cols),
		"contract_notice_days": in.ContractNoticeDays, "review_notice_days": in.ReviewNoticeDays}); err != nil {
		return nil, err
	}
	return loadSettings(ctx, tx)
}

func validColumns(in []Column) ([]Column, error) {
	if len(in) == 0 {
		return defaultColumns(), nil
	}
	if len(in) > 80 {
		return nil, engine.InvalidCode("validation.risk.columns_too_many", "register_columns", "al massimo 80 colonne")
	}
	out := make([]Column, 0, len(in))
	seen := map[string]bool{}
	for _, c := range in {
		info, ok := columnCatalog[c.Key]
		if !ok {
			return nil, engine.InvalidValue("register_columns", "colonna %q sconosciuta", c.Key)
		}
		if seen[c.Key] {
			return nil, engine.Duplicate("register_columns", "colonna %q ripetuta", c.Key)
		}
		seen[c.Key] = true
		c.Label = strings.TrimSpace(c.Label)
		if c.Label == "" {
			c.Label = info.Label
		}
		if len(c.Label) > 120 || strings.ContainsAny(c.Label, "\r\n") {
			return nil, engine.InvalidValue("register_columns", "intestazione della colonna %s non valida", c.Key)
		}
		out = append(out, c)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Politica di accettazione (approval_policies, stage "@risk_acceptance")

// Policy è la politica di accettazione: regole in ordine, vince la prima con la soglia vera.
type Policy struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Rules []approval.Rule `json:"rules"`
}

// GetPolicy legge la politica di accettazione del rischio.
func (s *Service) GetPolicy(ctx context.Context, tx pgx.Tx) (*Policy, error) {
	p := &Policy{Rules: []approval.Rule{}}
	var raw []byte
	err := tx.QueryRow(ctx, `select id::text, name, rules from approval_policies where stage = $1 and active order by type_id nulls last limit 1`,
		PolicyAcceptance).Scan(&p.ID, &p.Name, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	var pol approval.Policy
	if err := json.Unmarshal(raw, &pol); err != nil {
		return nil, err
	}
	if pol.Rules != nil {
		p.Rules = pol.Rules
	}
	return p, nil
}

// UpdatePolicy salva le regole della politica (risk.admin): solo soglie sul punteggio
// residuo, ogni regola con almeno un passo e approvatori esistenti.
func (s *Service) UpdatePolicy(ctx context.Context, tx pgx.Tx, actor engine.Actor, rules []approval.Rule) (*Policy, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if len(rules) == 0 || len(rules) > 20 {
		return nil, engine.Required("rules", "servono da 1 a 20 regole")
	}
	for i, r := range rules {
		if r.When.AmountGT != nil || r.When.AmountLTE != nil || r.When.Driver != "" {
			return nil, engine.InvalidParams("validation.risk.acceptance_rule_thresholds", "rules", map[string]any{"rule": i + 1}, "regola %d: per l'accettazione valgono solo le soglie risk_lte e risk_gt", i+1)
		}
		if len(r.Steps) == 0 {
			return nil, engine.InvalidParams("validation.policy.rule_no_step", "rules", map[string]any{"rule": i + 1}, "regola %d: serve almeno un passo di approvazione", i+1)
		}
		for j, st := range r.Steps {
			if len(st.Approvers) == 0 {
				return nil, engine.InvalidParams("validation.policy.step_no_approvers", "rules", map[string]any{"rule": i + 1, "step": j + 1}, "regola %d, passo %d: senza approvatori", i+1, j+1)
			}
			for _, a := range st.Approvers {
				n := 0
				for _, v := range []string{a.Role, a.Body, a.Member} {
					if v != "" {
						n++
					}
				}
				if n != 1 {
					return nil, engine.InvalidParams("validation.policy.step_approver_kind", "rules", map[string]any{"rule": i + 1, "step": j + 1}, "regola %d, passo %d: ogni approvatore è un ruolo, un organo o un membro", i+1, j+1)
				}
				var ok bool
				var err error
				switch {
				case a.Role != "":
					err = tx.QueryRow(ctx, "select exists (select 1 from stakeholder_roles where key = $1)", a.Role).Scan(&ok)
				case a.Body != "":
					err = tx.QueryRow(ctx, "select exists (select 1 from governance_bodies where kind = $1 and not archived)", a.Body).Scan(&ok)
				default:
					err = tx.QueryRow(ctx, "select exists (select 1 from members where id::text = $1 and status = 'active')", a.Member).Scan(&ok)
				}
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, engine.NotFound("rules", "", "regola %d, passo %d: approvatore %s%s%s inesistente", i+1, j+1, a.Role, a.Body, a.Member)
				}
			}
		}
	}
	raw, _ := json.Marshal(map[string]any{"rules": rules})
	tag, err := tx.Exec(ctx, `update approval_policies set rules = $2 where stage = $1 and active`, PolicyAcceptance, raw)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `insert into approval_policies (tenant_id, id, name, type_id, stage, rules)
			values (app_tenant(), $1, 'Accettazione del rischio', (select id from element_types where key = $2), $3, $4)`,
			db.NewID(), RiskType, PolicyAcceptance, raw); err != nil {
			return nil, err
		}
	}
	if err := audited(ctx, tx, actor, "risk_policy.update", "approval_policy", "", map[string]any{"rules": len(rules)}); err != nil {
		return nil, err
	}
	return s.GetPolicy(ctx, tx)
}

// ---------------------------------------------------------------------------
// Schemi di classificazione degli incidenti

// Criterion è un criterio di uno schema: una condizione sui fatti dell'incidente.
type Criterion struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Condition string `json:"condition"`
	// Primary: criterio principale (non conta tra i secondari).
	Primary bool   `json:"primary,omitempty"`
	Help    string `json:"help,omitempty"`
}

// Due è un termine di una tappa: tante ore o giorni da un momento dell'incidente.
type Due struct {
	From  string `json:"from"` // detected | classified | stage:<chiave>
	Hours int    `json:"hours,omitempty"`
	Days  int    `json:"days,omitempty"`
}

func (d Due) duration() time.Duration {
	return time.Duration(d.Hours)*time.Hour + time.Duration(d.Days)*24*time.Hour
}

// Stage è una tappa di notifica: la scadenza è il primo dei suoi termini.
type Stage struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Due  []Due  `json:"due"`
	Hint string `json:"hint,omitempty"`
}

// Scheme è uno schema di classificazione normativa (es. DORA, NIS2).
type Scheme struct {
	ID          string      `json:"id,omitempty"`
	Key         string      `json:"key"`
	Name        string      `json:"name"`
	OutcomeName string      `json:"outcome_name"`
	Reference   string      `json:"reference,omitempty"`
	Description string      `json:"description,omitempty"`
	Active      bool        `json:"active"`
	Ordinal     int         `json:"ordinal"`
	Criteria    []Criterion `json:"criteria"`
	When        string      `json:"when"`
	Stages      []Stage     `json:"stages"`
}

type schemeDef struct {
	Criteria []Criterion `json:"criteria"`
	When     string      `json:"when"`
	Stages   []Stage     `json:"stages"`
}

// Validate controlla lo schema: chiavi, condizioni analizzabili, termini delle tappe che
// rimandano a momenti o a tappe precedenti.
func (sc *Scheme) Validate() error {
	sc.Name, sc.OutcomeName = strings.TrimSpace(sc.Name), strings.TrimSpace(sc.OutcomeName)
	sc.Reference, sc.Description = strings.TrimSpace(sc.Reference), strings.TrimSpace(sc.Description)
	if !keyRe.MatchString(sc.Key) {
		return engine.InvalidValue("key", "chiave dello schema non valida")
	}
	if sc.Name == "" || len(sc.Name) > 120 || sc.OutcomeName == "" || len(sc.OutcomeName) > 60 {
		return engine.TooLong("name", 120, "nome dello schema (massimo 120 caratteri) ed esito (massimo 60) obbligatori")
	}
	if len(sc.Reference) > 500 || len(sc.Description) > 4000 {
		return engine.InvalidCode("validation.risk.reference_too_long", "reference", "riferimento o descrizione troppo lunghi")
	}
	if len(sc.Criteria) == 0 || len(sc.Criteria) > 30 {
		return engine.Required("criteria", "servono da 1 a 30 criteri")
	}
	seen := map[string]bool{}
	for i := range sc.Criteria {
		c := &sc.Criteria[i]
		c.Name, c.Condition, c.Help = strings.TrimSpace(c.Name), strings.TrimSpace(c.Condition), strings.TrimSpace(c.Help)
		if !keyRe.MatchString(c.Key) || seen[c.Key] {
			return engine.InvalidValue("criteria", "chiave del criterio %q non valida o ripetuta", c.Key)
		}
		seen[c.Key] = true
		if c.Name == "" || c.Condition == "" {
			return engine.InvalidParams("validation.risk.criterion_incomplete", "criteria", map[string]any{"key": c.Key}, "il criterio %s vuole un nome e una condizione", c.Key)
		}
		if _, err := engine.ParseCondition(c.Condition); err != nil {
			return engine.InvalidParams("validation.risk.criterion_error", "criteria", map[string]any{"key": c.Key, "detail": err.Error()}, "criterio %s: %v", c.Key, err)
		}
	}
	sc.When = strings.TrimSpace(sc.When)
	if sc.When == "" {
		return engine.Required("when", "serve la regola dell'esito (es. criteri_soddisfatti: >=1)")
	}
	if _, err := engine.ParseCondition(sc.When); err != nil {
		return engine.InvalidParams("validation.risk.outcome_rule_error", "when", map[string]any{"detail": err.Error()}, "regola dell'esito: %v", err)
	}
	if len(sc.Stages) > 10 {
		return engine.InvalidCode("validation.risk.stages_too_many", "stages", "al massimo 10 tappe di notifica")
	}
	stageSeen := map[string]bool{}
	for i := range sc.Stages {
		st := &sc.Stages[i]
		st.Name, st.Hint = strings.TrimSpace(st.Name), strings.TrimSpace(st.Hint)
		if !keyRe.MatchString(st.Key) || stageSeen[st.Key] {
			return engine.InvalidValue("stages", "chiave della tappa %q non valida o ripetuta", st.Key)
		}
		if st.Name == "" || len(st.Name) > 120 || len(st.Hint) > 2000 {
			return engine.TooLong("stages", 120, "la tappa %s vuole un nome (massimo 120 caratteri)", st.Key)
		}
		if len(st.Due) == 0 || len(st.Due) > 4 {
			return engine.InvalidParams("validation.risk.stage_terms_count", "stages", map[string]any{"key": st.Key}, "la tappa %s vuole da 1 a 4 termini", st.Key)
		}
		for _, d := range st.Due {
			ref, isStage := strings.CutPrefix(d.From, "stage:")
			switch {
			case d.From == "detected" || d.From == "classified":
			case isStage && stageSeen[ref]:
			default:
				return engine.InvalidParams("validation.risk.stage_term_from", "stages", map[string]any{"key": st.Key, "from": d.From}, "tappa %s: il termine parte da detected, classified o stage:<tappa precedente> (non %q)", st.Key, d.From)
			}
			if d.Hours < 0 || d.Days < 0 || d.duration() <= 0 || d.duration() > 400*24*time.Hour {
				return engine.InvalidParams("validation.risk.stage_term_duration", "stages", map[string]any{"key": st.Key}, "tappa %s: termine in ore o giorni, positivo e al massimo 400 giorni", st.Key)
			}
		}
		stageSeen[st.Key] = true
	}
	return nil
}

const selectScheme = `select id::text, key, name, outcome_name, reference, description, active, ordinal, definition from incident_schemes`

func scanSchemes(rows pgx.Rows) ([]Scheme, error) {
	defer rows.Close()
	out := []Scheme{}
	for rows.Next() {
		var sc Scheme
		var raw []byte
		if err := rows.Scan(&sc.ID, &sc.Key, &sc.Name, &sc.OutcomeName, &sc.Reference, &sc.Description, &sc.Active, &sc.Ordinal, &raw); err != nil {
			return nil, err
		}
		var d schemeDef
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, err
		}
		sc.Criteria, sc.When, sc.Stages = d.Criteria, d.When, d.Stages
		if sc.Stages == nil {
			sc.Stages = []Stage{}
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// Schemes elenca gli schemi del tenant (anche quelli non attivi).
func (s *Service) Schemes(ctx context.Context, tx pgx.Tx, onlyActive bool) ([]Scheme, error) {
	q := selectScheme
	if onlyActive {
		q += " where active"
	}
	rows, err := tx.Query(ctx, q+" order by ordinal, key")
	if err != nil {
		return nil, err
	}
	return scanSchemes(rows)
}

func canConfigureSchemes(actor engine.Actor) bool {
	return actor.Has(PermAdmin) || actor.Has(PermIncident)
}

// SaveScheme crea o sostituisce lo schema con la chiave indicata (risk.admin o
// incident.manage). Le notifiche già generate non cambiano: le scadenze nuove valgono per le
// classificazioni successive.
func (s *Service) SaveScheme(ctx context.Context, tx pgx.Tx, actor engine.Actor, sc Scheme) (*Scheme, error) {
	if !canConfigureSchemes(actor) {
		return nil, engine.ErrForbidden
	}
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	def, _ := json.Marshal(schemeDef{Criteria: sc.Criteria, When: sc.When, Stages: sc.Stages})
	var id string
	err := tx.QueryRow(ctx, `insert into incident_schemes (tenant_id, id, key, name, outcome_name, reference, description, active, ordinal, definition, updated_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		on conflict (tenant_id, key) do update set name = excluded.name, outcome_name = excluded.outcome_name, reference = excluded.reference,
		  description = excluded.description, active = excluded.active, ordinal = excluded.ordinal, definition = excluded.definition,
		  updated_by = excluded.updated_by, updated_at = now()
		returning id::text`, db.NewID(), sc.Key, sc.Name, sc.OutcomeName, sc.Reference, sc.Description, sc.Active, sc.Ordinal, def,
		nullable(actor.MemberID)).Scan(&id)
	if err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "incident_scheme.save", "incident_scheme", id, map[string]any{"key": sc.Key,
		"criteria": len(sc.Criteria), "stages": len(sc.Stages), "active": sc.Active}); err != nil {
		return nil, err
	}
	sc.ID = id
	return &sc, nil
}

// ---------------------------------------------------------------------------
// Pacchetto di configurazione

// Package è la configurazione in JSON (pacchetto di partenza, export e import).
type Package struct {
	Format           string          `json:"format"`
	Name             string          `json:"name,omitempty"`
	Description      string          `json:"description,omitempty"`
	ExportedAt       string          `json:"exported_at,omitempty"`
	Matrices         []Matrix        `json:"matrices"`
	Settings         *Settings       `json:"settings,omitempty"`
	AcceptancePolicy []approval.Rule `json:"acceptance_policy,omitempty"`
	Schemes          []Scheme        `json:"schemes"`
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
	Matrices []string `json:"matrices"`
	Schemes  []string `json:"schemes"`
	Settings bool     `json:"settings"`
	Policy   bool     `json:"policy"`
}

// Import porta un pacchetto nel tenant (risk.admin): ogni matrice e schema per chiave,
// impostazioni e politica se presenti.
func (s *Service) Import(ctx context.Context, tx pgx.Tx, actor engine.Actor, p *Package) (*ImportResult, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	res := &ImportResult{Matrices: []string{}, Schemes: []string{}}
	for _, m := range p.Matrices {
		if _, err := s.SaveMatrix(ctx, tx, actor, m); err != nil {
			return nil, fmt.Errorf("matrice %s: %w", m.Key, err)
		}
		res.Matrices = append(res.Matrices, m.Key)
	}
	if p.Settings != nil {
		if _, err := s.UpdateSettings(ctx, tx, actor, *p.Settings); err != nil {
			return nil, fmt.Errorf("impostazioni: %w", err)
		}
		res.Settings = true
	}
	if len(p.AcceptancePolicy) > 0 {
		if _, err := s.UpdatePolicy(ctx, tx, actor, p.AcceptancePolicy); err != nil {
			return nil, fmt.Errorf("politica di accettazione: %w", err)
		}
		res.Policy = true
	}
	for _, sc := range p.Schemes {
		if _, err := s.SaveScheme(ctx, tx, actor, sc); err != nil {
			return nil, fmt.Errorf("schema %s: %w", sc.Key, err)
		}
		res.Schemes = append(res.Schemes, sc.Key)
	}
	return res, audited(ctx, tx, actor, "risk_config.import", "risk_config", "", map[string]any{
		"matrices": res.Matrices, "schemes": res.Schemes, "settings": res.Settings, "policy": res.Policy})
}

// Export restituisce la configurazione corrente come pacchetto (risk.admin).
func (s *Service) Export(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Package, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	ms, err := s.Matrices(ctx, tx)
	if err != nil {
		return nil, err
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	pol, err := s.GetPolicy(ctx, tx)
	if err != nil {
		return nil, err
	}
	scs, err := s.Schemes(ctx, tx, false)
	if err != nil {
		return nil, err
	}
	p := &Package{Format: PackageFormat, ExportedAt: s.now().Format(time.RFC3339), Settings: st, AcceptancePolicy: pol.Rules, Matrices: []Matrix{}, Schemes: []Scheme{}}
	for _, m := range ms {
		x := *m
		x.ID = ""
		p.Matrices = append(p.Matrices, x)
	}
	for _, sc := range scs {
		sc.ID = ""
		p.Schemes = append(p.Schemes, sc)
	}
	return p, nil
}

// Config è la pagina di configurazione.
type Config struct {
	Matrices      []*Matrix    `json:"matrices"`
	Settings      *Settings    `json:"settings"`
	Policy        *Policy      `json:"policy"`
	Schemes       []Scheme     `json:"schemes"`
	Columns       []ColumnInfo `json:"column_catalog"`
	Tones         []string     `json:"tones"`
	CanEdit       bool         `json:"can_edit"`
	CanEditScheme bool         `json:"can_edit_schemes"`
}

// GetConfig restituisce la configurazione (lettura per chi legge gli elementi).
func (s *Service) GetConfig(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Config, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	ms, err := s.Matrices(ctx, tx)
	if err != nil {
		return nil, err
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	pol, err := s.GetPolicy(ctx, tx)
	if err != nil {
		return nil, err
	}
	scs, err := s.Schemes(ctx, tx, false)
	if err != nil {
		return nil, err
	}
	return &Config{Matrices: ms, Settings: st, Policy: pol, Schemes: scs, Columns: ColumnCatalog(), Tones: tones,
		CanEdit: actor.Has(PermAdmin), CanEditScheme: canConfigureSchemes(actor)}, nil
}
