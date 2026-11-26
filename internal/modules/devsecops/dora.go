package devsecops

// Metriche DORA delle quattro chiavi (devsecops.md §3.6, fase 5a).
//
// Come si calcolano, dai dati che Regia ha già:
//
//   - Frequenza di rilascio: deploy riusciti negli ambienti di produzione, per settimana.
//   - Lead time delle modifiche: dall'inizio della modifica al deploy in produzione, mediana in
//     giorni. L'inizio è, in ordine, il primo commit legato a un elemento
//     (element_commits: letto dai connettori) tra il rilascio precedente e questo, poi il primo esito
//     dei controlli del commit rilasciato (la pipeline parte al push), poi la creazione del change; il tenant può fissare la fonte (impostazione lead_time_start).
//   - Tasso di fallimento: deploy di produzione falliti sul totale dei tentativi. Un deploy è
//     fallito se ha stato failed o rolled_back, se un incidente sull'applicazione (o sul suo
//     asset) è nato entro N ore dopo il deploy, se un rollback (si torna alla versione
//     precedente) o un hotfix (versione, titolo del change o PR con le parole configurate) lo
//     seguono entro N ore, o se è collegato a mano a un incidente.
//   - Tempo di ripristino: dagli incidenti collegati ad applicazioni o asset, da rilevato a
//     risolto, mediana in ore.
//
// Per servizio, applicazione, asset, team e tenant, su settimana, mese e trimestre di
// calendario (UTC). I punti stanno in metric_points (chiavi dora.*) e li ricalcola un job.

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Chiavi delle metriche (senza il prefisso "dora." dei punti salvati).
const (
	MetricFrequency = "deploy_frequency"    // deploy per settimana
	MetricLeadTime  = "lead_time_days"      // mediana, giorni
	MetricFailure   = "change_failure_rate" // percentuale
	MetricRestore   = "mttr_hours"          // mediana, ore
	metricLevel     = "level"               // livello complessivo, 0 (low) … 3 (elite)
	pointPrefix     = "dora."
	doraRelImpacts  = "incident_impacts"
)

// Livelli, dal peggiore al migliore (l'indice è il valore salvato in dora.level).
var Levels = []string{"low", "medium", "high", "elite"}

// MetricDef descrive una metrica per l'interfaccia.
type MetricDef struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Unit         string `json:"unit"`
	HigherBetter bool   `json:"higher_better"`
	Hint         string `json:"hint"`
}

var Metrics = []MetricDef{
	{MetricFrequency, "Frequenza di rilascio", "rilasci a settimana", true, "Deploy riusciti in produzione"},
	{MetricLeadTime, "Lead time delle modifiche", "giorni", false, "Dall'inizio della modifica al deploy in produzione (mediana)"},
	{MetricFailure, "Tasso di fallimento", "%", false, "Deploy in produzione seguiti da incidente, rollback o hotfix"},
	{MetricRestore, "Tempo di ripristino", "ore", false, "Dall'incidente al servizio ripristinato (mediana)"},
}

// Ladder: soglie di una metrica. Per la frequenza si è a quel livello da quel valore in su
// (>=); per le altre fino a quel valore (<=). Sotto "medium" il livello è low.
type Ladder struct {
	Elite  float64 `json:"elite"`
	High   float64 `json:"high"`
	Medium float64 `json:"medium"`
}

// DefaultThresholds: soglie del rapporto DORA, adattate alle unità di Regia.
func DefaultThresholds() map[string]Ladder {
	return map[string]Ladder{
		MetricFrequency: {Elite: 7, High: 1, Medium: 0.25},
		MetricLeadTime:  {Elite: 1, High: 7, Medium: 30},
		MetricFailure:   {Elite: 5, High: 10, Medium: 15},
		MetricRestore:   {Elite: 1, High: 24, Medium: 168},
	}
}

// LevelOf classifica un valore: indice in Levels.
func LevelOf(metric string, v float64, l Ladder) int {
	if metric == MetricFrequency {
		switch {
		case v >= l.Elite:
			return 3
		case v >= l.High:
			return 2
		case v >= l.Medium:
			return 1
		}
		return 0
	}
	switch {
	case v <= l.Elite:
		return 3
	case v <= l.High:
		return 2
	case v <= l.Medium:
		return 1
	}
	return 0
}

// OverallLevel: media dei livelli delle metriche con dati, per difetto. -1 se non ce n'è.
func OverallLevel(levels map[string]int) int {
	sum, n := 0, 0
	for _, l := range levels {
		sum += l
		n++
	}
	if n == 0 {
		return -1
	}
	return sum / n
}

// DoraSettings sono le impostazioni del tenant.
type DoraSettings struct {
	FailureWindowHours int               `json:"failure_window_hours"`
	HotfixWords        []string          `json:"hotfix_words"`
	LeadTimeStart      string            `json:"lead_time_start"` // auto | commit | check_run | change | request
	Thresholds         map[string]Ladder `json:"thresholds"`
	FactDays           int               `json:"fact_days"`
	UpdatedAt          *time.Time        `json:"updated_at"`
	Default            bool              `json:"default"`
}

func defaultDoraSettings() DoraSettings {
	return DoraSettings{FailureWindowHours: 24, HotfixWords: []string{"hotfix", "rollback", "revert"}, LeadTimeStart: "auto",
		Thresholds: DefaultThresholds(), FactDays: 30, Default: true}
}

func loadDoraSettings(ctx context.Context, tx pgx.Tx) (DoraSettings, error) {
	st := defaultDoraSettings()
	var raw []byte
	err := tx.QueryRow(ctx, `select failure_window_hours, hotfix_words, lead_time_start, thresholds, fact_days, updated_at from dora_settings`).
		Scan(&st.FailureWindowHours, &st.HotfixWords, &st.LeadTimeStart, &raw, &st.FactDays, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.Default = false
	custom := map[string]Ladder{}
	if err := json.Unmarshal(raw, &custom); err != nil {
		return st, err
	}
	for k, v := range custom {
		if _, ok := st.Thresholds[k]; ok {
			st.Thresholds[k] = v
		}
	}
	return st, nil
}

// normalize controlla e pulisce le impostazioni proposte.
func (in DoraSettings) normalize() (DoraSettings, error) {
	out := in
	if out.FailureWindowHours < 1 || out.FailureWindowHours > 720 {
		return out, engine.OutOfRange("failure_window_hours", 1, 720, "la finestra va da 1 a 720 ore")
	}
	if out.FactDays < 7 || out.FactDays > 365 {
		return out, engine.OutOfRange("fact_days", 7, 365, "i giorni dei fatti vanno da 7 a 365")
	}
	if !slices.Contains([]string{"auto", "commit", "check_run", "change", "request"}, out.LeadTimeStart) {
		return out, engine.InvalidValue("lead_time_start", "inizio del lead time non valido")
	}
	words := []string{}
	for _, w := range in.HotfixWords {
		w = strings.ToLower(strings.TrimSpace(w))
		if w != "" && len(w) <= 40 && !slices.Contains(words, w) {
			words = append(words, w)
		}
	}
	if len(words) > 20 {
		return out, engine.InvalidCode("validation.devsecops.hotfix_words_too_many", "hotfix_words", "al massimo 20 parole")
	}
	out.HotfixWords = words
	th := DefaultThresholds()
	for k := range th {
		l, ok := in.Thresholds[k]
		if !ok {
			continue
		}
		th[k] = l
	}
	for _, m := range Metrics {
		l := th[m.Key]
		for _, v := range []float64{l.Elite, l.High, l.Medium} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e6 {
				return out, engine.InvalidParams("validation.devsecops.dora_thresholds_invalid", "thresholds", map[string]any{"metric": m.Name}, "%s: soglie non valide", m.Name)
			}
		}
		ok := l.Elite < l.High && l.High < l.Medium
		if m.HigherBetter {
			ok = l.Elite > l.High && l.High > l.Medium && l.Medium > 0
		}
		if !ok {
			if m.HigherBetter {
				return out, engine.InvalidParams("validation.devsecops.dora_thresholds_descending", "thresholds", map[string]any{"metric": m.Name}, "%s: le soglie devono scendere da elite a medium (e restare sopra zero)", m.Name)
			}
			return out, engine.InvalidParams("validation.devsecops.dora_thresholds_ascending", "thresholds", map[string]any{"metric": m.Name}, "%s: le soglie devono salire da elite a medium", m.Name)
		}
	}
	out.Thresholds = th
	return out, nil
}

// DoraSettings restituisce le impostazioni del tenant (readable da chi legge gli elementi).
func (s *Service) DoraSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor) (DoraSettings, error) {
	if !actor.Has(PermRead) {
		return DoraSettings{}, engine.ErrForbidden
	}
	return loadDoraSettings(ctx, tx)
}

// SaveDoraSettings salva le impostazioni e ricalcola i punti (le soglie cambiano i livelli, la
// finestra e le parole cambiano i fallimenti).
func (s *Service) SaveDoraSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in DoraSettings) (DoraSettings, error) {
	if !CanConfigure(actor) {
		return DoraSettings{}, engine.ErrForbidden
	}
	in, err := in.normalize()
	if err != nil {
		return DoraSettings{}, err
	}
	raw, _ := json.Marshal(in.Thresholds)
	if _, err := tx.Exec(ctx, `insert into dora_settings (tenant_id, failure_window_hours, hotfix_words, lead_time_start, thresholds, fact_days, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, now())
		on conflict (tenant_id) do update set failure_window_hours = excluded.failure_window_hours, hotfix_words = excluded.hotfix_words,
		lead_time_start = excluded.lead_time_start, thresholds = excluded.thresholds, fact_days = excluded.fact_days,
		updated_by = excluded.updated_by, updated_at = now()`,
		in.FailureWindowHours, in.HotfixWords, in.LeadTimeStart, raw, in.FactDays, nullable(actor.MemberID)); err != nil {
		return DoraSettings{}, err
	}
	if err := auditEvent(ctx, tx, actor, "devsecops.dora_settings", "dora_settings", "", map[string]any{
		"failure_window_hours": in.FailureWindowHours, "hotfix_words": in.HotfixWords, "lead_time_start": in.LeadTimeStart,
		"thresholds": in.Thresholds, "fact_days": in.FactDays}); err != nil {
		return DoraSettings{}, err
	}
	if _, err := s.recompute(ctx, tx, true); err != nil {
		return DoraSettings{}, err
	}
	return loadDoraSettings(ctx, tx)
}
