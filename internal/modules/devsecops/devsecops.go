// Package devsecops collega a Regia ciò che le pipeline producono (devsecops.md, fase 1;
// piano, passo 21): servizi associati alle applicazioni del catalogo, esiti dei controlli,
// rilasci con il numero di change, deploy per ambiente, SBOM e vulnerabilità. Regia non
// esegue pipeline né scansioni: riceve gli oggetti del contratto neutro (sottopacchetto
// contract) dall'API di push e dai connettori, e li trasforma in un pannello per asset, in
// fatti per le condizioni dei processi e in evidenze per i requisiti.
package devsecops

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/architecture"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/modules/grc"
	"regia/internal/modules/requirements"
	"regia/internal/platform/audit"
)

// Permessi: l'invio dei dati è devsecops.write (ruolo "Pipeline"); la lettura è element.read;
// la configurazione (ambienti, giorni, associazioni) è del catalogo, architecture.write, o
// dell'amministratore del tenant.
const (
	PermWrite = "devsecops.write"
	PermRead  = "element.read"
)

// Service implementa contract.Ingestor e contract.ServiceIngestor.
type Service struct {
	eng *engine.Service
	req *requirements.Service
	Now func() time.Time
	// Gate di rilascio (gate_*.go): conformità per le evidenze automatiche e motore delle
	// approvazioni per gli override; si collegano con SetGate. PublicURL (REGIA_BASE_URL)
	// rende assoluti i link delle risposte del gate.
	grc       *grc.Service
	ap        *approval.Service
	PublicURL string
}

var (
	_ contract.Ingestor        = (*Service)(nil)
	_ contract.ServiceIngestor = (*Service)(nil)
)

// Register collega il modulo al motore: fatti sull'applicazione e sulla richiesta. req può
// essere nil (niente verifica automatica dei requisiti).
func Register(eng *engine.Service, req *requirements.Service) *Service {
	s := &Service{eng: eng, req: req, Now: time.Now}
	eng.Facts(architecture.AppType, s.appFacts)
	eng.Facts(architecture.RequestType, s.requestFacts)
	eng.DescribeFacts(architecture.AppType,
		engine.FactInfo{Key: "cve_critiche_produzione", Name: "CVE critiche in produzione (da correggere, non in eccezione, sulla versione in produzione)", Kind: "number", Example: "0"},
		engine.FactInfo{Key: "cve_alte_produzione", Name: "CVE alte in produzione (stessa definizione delle critiche)", Kind: "number", Example: "2"},
		engine.FactInfo{Key: "ultimo_rilascio_giorni", Name: "Giorni dall'ultimo rilascio in produzione", Kind: "number", Example: "12"},
		engine.FactInfo{Key: "servizi", Name: "Servizi (repository) dell'applicazione", Kind: "number", Example: "3"},
		engine.FactInfo{Key: "servizi_fuori_pipeline", Name: "Servizi non ancora sulla pipeline", Kind: "number", Example: "1"},
		engine.FactInfo{Key: "runtime_fuori_supporto", Name: "Runtime fuori supporto in produzione", Kind: "bool", Values: []string{"sì", "no"}},
	)
	s.registerDoraFacts(eng, architecture.AppType)
	eng.DescribeFacts(architecture.RequestType,
		engine.FactInfo{Key: "rilasci", Name: "Rilasci collegati (numero di change)", Kind: "number", Example: "2"},
		engine.FactInfo{Key: "rilasci_produzione", Name: "Rilasci collegati in produzione", Kind: "number", Example: "1"},
		engine.FactInfo{Key: "rilasciata", Name: "Almeno un rilascio in produzione", Kind: "bool", Values: []string{"sì", "no"}},
		engine.FactInfo{Key: "ultimo_rilascio", Name: "Data dell'ultimo rilascio collegato", Kind: "date", Example: "2026-11-20"},
		engine.FactInfo{Key: "change", Name: "Numeri di change", Kind: "text", Example: "CHG0012345"},
		engine.FactInfo{Key: "servizi_rilasciati", Name: "Servizi rilasciati", Kind: "text", Example: "orders-api"},
	)
	s.registerVuln(eng)        // vulnerabilità governate (vulnfacts.go)
	s.registerPolicyFacts(eng) // politica della pipeline (adherence.go, fase 3)
	return s
}

func (s *Service) now() time.Time { return s.Now().UTC() }

// CanConfigure: chi gestisce il catalogo (o l'amministratore) configura il modulo.
func CanConfigure(a engine.Actor) bool {
	return a.Has(architecture.PermWrite) || a.Has("tenant.admin")
}

// ---------------------------------------------------------------------------
// Impostazioni

// RuntimeEOL è la fine del supporto di un ciclo di un runtime (es. node 18 → 2025-04-30).
// Si confronta con i componenti della SBOM della versione in produzione: nome uguale (o
// alias noto) e versione uguale o che comincia con "<version>.".
type RuntimeEOL struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	EndOfSupport string `json:"end_of_support"` // AAAA-MM-GG
}

type Settings struct {
	ProductionEnvs []string     `json:"production_envs"`
	StaleDays      int          `json:"stale_days"`
	ChangeField    string       `json:"change_field"`
	Runtimes       []RuntimeEOL `json:"runtimes"`
	UpdatedAt      *time.Time   `json:"updated_at"`
	Default        bool         `json:"default"` // nessuna impostazione salvata: valgono i default
}

// defaultRuntimes: cicli pubblici con la fine del supporto della comunità; il tenant li
// modifica da Configurazione → DevSecOps.
var defaultRuntimes = []RuntimeEOL{
	{"node", "14", "2023-04-30"}, {"node", "16", "2023-09-11"}, {"node", "18", "2025-04-30"}, {"node", "20", "2026-04-30"},
	{"python", "3.7", "2023-06-27"}, {"python", "3.8", "2024-10-07"}, {"python", "3.9", "2025-10-31"},
	{"php", "8.0", "2023-11-26"}, {"php", "8.1", "2025-12-31"},
	{"dotnet", "6", "2024-11-12"}, {"dotnet", "7", "2024-05-14"},
}

func loadSettings(ctx context.Context, tx pgx.Tx) (Settings, error) {
	st := Settings{ProductionEnvs: []string{"prod", "production"}, StaleDays: 30, Runtimes: slices.Clone(defaultRuntimes), Default: true}
	var raw []byte
	var field *string
	err := tx.QueryRow(ctx, `select production_envs, stale_days, change_field, runtimes, updated_at from devsecops_settings`).
		Scan(&st.ProductionEnvs, &st.StaleDays, &field, &raw, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.Default = false
	if field != nil {
		st.ChangeField = *field
	}
	st.Runtimes = []RuntimeEOL{}
	if err := json.Unmarshal(raw, &st.Runtimes); err != nil {
		return st, err
	}
	return st, nil
}

func (st Settings) isProduction(env string) bool {
	return slices.Contains(st.ProductionEnvs, strings.ToLower(env))
}

// SaveSettings salva le impostazioni; i nomi degli ambienti di produzione valgono per gli
// ambienti creati da qui in poi (quelli esistenti si cambiano uno per uno).
func (s *Service) SaveSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in Settings) (Settings, error) {
	if !CanConfigure(actor) {
		return Settings{}, engine.ErrForbidden
	}
	envs := []string{}
	for _, e := range in.ProductionEnvs {
		e = normEnv(e)
		if e != "" && !slices.Contains(envs, e) {
			envs = append(envs, e)
		}
	}
	if len(envs) == 0 {
		return Settings{}, engine.Required("production_envs", "serve almeno un nome di ambiente di produzione")
	}
	if in.StaleDays < 1 || in.StaleDays > 3650 {
		return Settings{}, engine.OutOfRange("stale_days", 1, 3650, "i giorni vanno da 1 a 3650")
	}
	field := strings.TrimSpace(in.ChangeField)
	if field != "" {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from field_defs where key = $1)", field).Scan(&ok); err != nil {
			return Settings{}, err
		}
		if !ok {
			return Settings{}, engine.NotFound("change_field", "field", "campo personalizzato inesistente: %s", field)
		}
	}
	rts := []RuntimeEOL{}
	for i, r := range in.Runtimes {
		r.Name, r.Version, r.EndOfSupport = strings.ToLower(strings.TrimSpace(r.Name)), strings.TrimSpace(r.Version), strings.TrimSpace(r.EndOfSupport)
		if r.Name == "" && r.Version == "" && r.EndOfSupport == "" {
			continue
		}
		if r.Name == "" || r.Version == "" || len(r.Name) > 60 || len(r.Version) > 30 {
			return Settings{}, engine.InvalidParams("validation.devsecops.runtime_incomplete", "runtimes", map[string]any{"row": i + 1}, "riga %d: servono nome e versione del runtime", i+1)
		}
		if _, err := time.Parse(time.DateOnly, r.EndOfSupport); err != nil {
			return Settings{}, engine.DateInvalid("runtimes", "riga %d: data di fine supporto non valida (AAAA-MM-GG)", i+1)
		}
		rts = append(rts, r)
	}
	if len(rts) > 500 {
		return Settings{}, engine.InvalidCode("validation.devsecops.runtimes_too_many", "runtimes", "al massimo 500 righe")
	}
	raw, _ := json.Marshal(rts)
	if _, err := tx.Exec(ctx, `insert into devsecops_settings (tenant_id, production_envs, stale_days, change_field, runtimes, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, $4, $5, now())
		on conflict (tenant_id) do update set production_envs = excluded.production_envs, stale_days = excluded.stale_days,
		change_field = excluded.change_field, runtimes = excluded.runtimes, updated_by = excluded.updated_by, updated_at = now()`,
		envs, in.StaleDays, nullable(field), raw, nullable(actor.MemberID)); err != nil {
		return Settings{}, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "devsecops.settings", EntityType: "devsecops_settings",
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"production_envs": envs, "stale_days": in.StaleDays, "change_field": field, "runtimes": len(rts)}}); err != nil {
		return Settings{}, err
	}
	return loadSettings(ctx, tx)
}

// ---------------------------------------------------------------------------
// Utilità

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	// Non si spezza un carattere UTF-8 a metà.
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

func normEnv(s string) string { return clip(strings.ToLower(s), 60) }

func auditEvent(ctx context.Context, tx pgx.Tx, actor engine.Actor, action, entityType, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: entityType, EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload})
}

func lock(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx, "select pg_advisory_xact_lock(hashtextextended($1, 21))", key)
	return err
}
