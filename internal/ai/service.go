package ai

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/ai/tools"
	"regia/internal/engine"
	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/secretbox"
)

// Options configura il servizio.
type Options struct {
	DB  *db.DB
	Box *secretbox.Box
	// Kinds sono i tipi di fornitore registrati (Anthropic, compatibile OpenAI; il finto solo
	// in sviluppo e nei test).
	Kinds []Kind
	// Tools sono gli strumenti con i permessi della persona (internal/ai/tools).
	Tools *tools.Services
	// Dev toglie la protezione SSRF (http e indirizzi locali).
	Dev bool
	// AllowPrivate è il consenso dell'operatore alle reti interne (REGIA_CONNECTOR_ALLOW_PRIVATE).
	AllowPrivate bool
	Log          *slog.Logger
	// Timeout di una chiamata al modello (default 3 minuti).
	Timeout time.Duration
	Now     func() time.Time
}

// Service è il livello AI di tutti i tenant.
type Service struct {
	o     Options
	kinds map[string]Kind
	order []string
}

// New crea il servizio.
func New(o Options) *Service {
	if o.Box == nil {
		o.Box = secretbox.Random()
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 3 * time.Minute
	}
	s := &Service{o: o, kinds: map[string]Kind{}}
	for _, k := range o.Kinds {
		if _, dup := s.kinds[k.Key]; !dup {
			s.order = append(s.order, k.Key)
		}
		s.kinds[k.Key] = k
	}
	return s
}

// Tools restituisce gli strumenti (gli stessi del server MCP).
func (s *Service) Tools() *tools.Services { return s.o.Tools }

// httpClient prepara il client con la politica di rete dei connettori.
func (s *Service) httpClient(p *ProviderRow) (*http.Client, error) {
	policy := ghapi.Public
	switch {
	case s.o.Dev:
		policy = ghapi.Any
	case p.Local && s.o.AllowPrivate:
		policy = ghapi.Private
	}
	return ghapi.NewHTTPClient(policy, "", s.o.Timeout)
}

// Target è il modello scelto per una funzione: fornitore, modello, livello e capacità.
type Target struct {
	Feature  string
	Tier     string
	Provider *ProviderRow
	Kind     Kind
	Model    string
	// Tools: il modello sa chiamare strumenti (altrimenti solo riassunti e bozze).
	Tools bool
	// Mask: i dati personali vanno mascherati (fornitore esterno e mascheramento acceso).
	Mask bool
}

// Resolve sceglie il modello di una funzione: la funzione spostata su un modello preciso,
// altrimenti il suo livello (rapido senza modello = approfondito, e viceversa).
func (s *Service) Resolve(ctx context.Context, tx pgx.Tx, feature string) (*Target, Settings, error) {
	st, err := LoadSettings(ctx, tx)
	if err != nil {
		return nil, st, err
	}
	if !st.Enabled {
		return nil, st, ErrDisabled
	}
	f, ok := featureByKey(feature)
	if !ok {
		return nil, st, &Error{Status: 400, Code: "ai.feature_unknown", Msg: "funzione sconosciuta"}
	}
	t := &Target{Feature: feature, Tier: f.Tier}
	r := st.Routes[feature]
	var choice TierChoice
	switch {
	case r.ProviderID != "":
		choice, t.Tier = TierChoice{ProviderID: r.ProviderID, Model: r.Model}, ""
	default:
		if r.Tier != "" {
			t.Tier = r.Tier
		}
		choice = st.Deep
		if t.Tier == TierFast {
			choice = st.Fast
		}
		if choice.ProviderID == "" {
			if t.Tier == TierFast {
				choice = st.Deep
			} else {
				choice = st.Fast
			}
		}
	}
	if choice.ProviderID == "" || choice.Model == "" {
		return nil, st, ErrNotConfigured
	}
	p, err := s.provider(ctx, tx, choice.ProviderID)
	if err != nil {
		return nil, st, ErrNotConfigured
	}
	t.Provider, t.Model = p, choice.Model
	t.Kind = s.kinds[p.Kind]
	t.Tools = toolsFor(p, choice.Model)
	t.Mask = st.MaskPersonalData && !p.Local
	return t, st, nil
}

// CallMeta descrive la chiamata per il registro.
type CallMeta struct {
	TenantID       string
	Actor          engine.Actor
	ConversationID string
	ElementIDs     []string
}

// Call fa una chiamata al modello del target: controlla il budget prima e la registra in
// ai_calls dopo, qualunque sia l'esito. Il registro ha l'hash del prompt, non il contenuto.
func (s *Service) Call(ctx context.Context, t *Target, req Request, meta CallMeta) (*Response, error) {
	req.Model = t.Model
	req.Deep = t.Tier != TierFast
	if !t.Tools {
		req.Tools = nil
	}
	hash := promptHash(req)
	rec := callRecord{Feature: t.Feature, Kind: t.Provider.Kind, Model: t.Model, Tier: t.Tier, PromptHash: hash,
		ElementIDs: meta.ElementIDs, ConversationID: meta.ConversationID, MemberID: meta.Actor.MemberID}
	// Budget: oltre il 100% la chiamata non parte (e resta nel registro come rifiutata).
	var bud Budget
	if err := s.o.DB.InTenant(ctx, meta.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		bud, err = s.budget(ctx, tx)
		return err
	}); err != nil {
		return nil, err
	}
	if bud.State == BudgetBlocked {
		rec.Outcome, rec.ErrorCode = "refused", "budget"
		s.record(ctx, meta.TenantID, rec)
		return nil, ErrBudget
	}
	prov, _, err := s.instance(t.Provider)
	if err != nil {
		rec.Outcome, rec.ErrorCode = "error", ErrorCode(err)
		s.record(ctx, meta.TenantID, rec)
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, s.o.Timeout)
	defer cancel()
	start := s.o.Now()
	resp, err := prov.Chat(cctx, req)
	rec.Duration = s.o.Now().Sub(start)
	if err != nil {
		rec.Outcome, rec.ErrorCode = "error", ErrorCode(err)
		s.record(ctx, meta.TenantID, rec)
		return nil, err
	}
	rec.In, rec.Out = resp.InputTokens, resp.OutputTokens
	if resp.Model != "" {
		rec.Model = resp.Model
	}
	rec.Cost = Cost(t.Provider, t.Model, resp.InputTokens, resp.OutputTokens)
	rec.Outcome = "ok"
	if resp.Stop == StopRefusal {
		rec.Outcome, rec.ErrorCode = "refused", "model_refusal"
	}
	s.record(ctx, meta.TenantID, rec)
	s.warnBudget(ctx, meta)
	return resp, nil
}

// promptHash è lo SHA-256 della richiesta (modello, istruzioni, messaggi, nomi degli strumenti).
func promptHash(req Request) []byte {
	type shape struct {
		Model    string    `json:"model"`
		System   string    `json:"system"`
		Messages []Message `json:"messages"`
		Tools    []string  `json:"tools"`
	}
	sh := shape{Model: req.Model, System: req.System, Messages: req.Messages}
	for _, t := range req.Tools {
		sh.Tools = append(sh.Tools, t.Name)
	}
	b, _ := json.Marshal(sh)
	h := sha256.Sum256(b)
	return h[:]
}

type callRecord struct {
	MemberID, Feature, Kind, Model, Tier string
	In, Out                              int64
	Cost                                 float64
	Outcome, ErrorCode                   string
	PromptHash                           []byte
	ElementIDs                           []string
	ConversationID                       string
	Duration                             time.Duration
}

// record scrive la riga del registro in una transazione sua: resta anche se il resto fallisce.
func (s *Service) record(ctx context.Context, tenantID string, r callRecord) {
	ids := r.ElementIDs
	if ids == nil {
		ids = []string{}
	}
	if len(ids) > 200 {
		ids = ids[:200]
	}
	err := s.o.DB.InTenant(context.WithoutCancel(ctx), tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into ai_calls (tenant_id, member_id, feature, provider, model, tier, input_tokens, output_tokens,
			cost_usd, element_ids, prompt_sha256, outcome, error_code, duration_ms, conversation_id)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			nullable(r.MemberID), r.Feature, r.Kind, r.Model, nullable(r.Tier), r.In, r.Out, r.Cost, ids, r.PromptHash,
			r.Outcome, nullable(r.ErrorCode), r.Duration.Milliseconds(), nullable(r.ConversationID))
		return err
	})
	if err != nil {
		s.o.Log.Warn("registro delle chiamate AI", "errore", err, "funzione", r.Feature, "esito", r.Outcome)
	}
}

// warnBudget: al primo superamento dell'80% nel mese resta una voce nel registro di audit
// (l'avviso si vede in Configurazione → AI e nel pannello Assistente).
func (s *Service) warnBudget(ctx context.Context, meta CallMeta) {
	err := s.o.DB.InTenant(context.WithoutCancel(ctx), meta.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		b, err := s.budget(ctx, tx)
		if err != nil || b.State == BudgetOK || b.State == BudgetNone {
			return err
		}
		tag, err := tx.Exec(ctx, `update ai_settings set budget_warned_month = $1::date
			where tenant_id = app_tenant() and (budget_warned_month is null or budget_warned_month < $1::date)`, b.MonthStart)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: "ai.budget.warning", EntityType: "ai_settings",
			Payload: map[string]any{"percent": int(b.Percent), "month": b.Month}})
	})
	if err != nil {
		s.o.Log.Warn("avviso del budget AI", "errore", err)
	}
}

// Status è ciò che serve all'interfaccia per mostrare (o no) l'assistente.
type Status struct {
	Enabled bool   `json:"enabled"`
	Ready   bool   `json:"ready"`
	Tools   bool   `json:"tools"`
	Model   string `json:"model,omitempty"`
	Masked  bool   `json:"masked"`
	Budget  Budget `json:"budget"`
	// RetentionDays: per quanti giorni restano le conversazioni.
	RetentionDays int `json:"retention_days"`
}

// StatusFor dice se l'assistente è usabile nel tenant.
func (s *Service) StatusFor(ctx context.Context, tx pgx.Tx) (Status, error) {
	t, st, err := s.Resolve(ctx, tx, "assistente")
	out := Status{Enabled: st.Enabled, RetentionDays: st.RetentionDays}
	var ae *Error
	if err != nil && !errors.As(err, &ae) {
		return out, err
	}
	if t != nil {
		out.Ready, out.Tools, out.Model, out.Masked = true, t.Tools, t.Model, t.Mask
	}
	if out.Budget, err = s.budget(ctx, tx); err != nil {
		return out, err
	}
	return out, nil
}
