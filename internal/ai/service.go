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
	"regia/internal/engine/notify"
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
	// GRC: i moduli del GRC assistito (ondata 5), con i permessi della persona.
	GRC GRCServices
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
	// TierEffort e RouteEffort: il ragionamento configurato per il livello e per la funzione
	// (vuoto = automatico); CallStream sceglie con effortFor.
	TierEffort  string
	RouteEffort string
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
	t.RouteEffort = r.Reasoning
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
	t.Provider, t.Model, t.TierEffort = p, choice.Model, choice.Reasoning
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
	return s.CallStream(ctx, t, req, meta, nil)
}

// CallStream è Call con il testo restituito mentre arriva: onText riceve i pezzi in ordine.
// Con un adattatore che non sa farlo (Streamer), onText riceve tutto il testo alla fine.
func (s *Service) CallStream(ctx context.Context, t *Target, req Request, meta CallMeta, onText func(string)) (*Response, error) {
	req.Model = t.Model
	req.Deep = t.Tier != TierFast
	req.Effort = effortFor(t.RouteEffort, t.TierEffort, req.Deep, req.Brief)
	req.MaxTokens = thinkingHeadroom(req.MaxTokens, req.Effort != EffortLow)
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
	// Con un budget: i token dell'input si contano prima (API del fornitore o stima) e la
	// chiamata non parte se da sola porterebbe oltre il limite.
	// Prima la stima locale: il conteggio del fornitore è un giro in più a ogni chiamata (e a
	// ogni passo dell'assistente), quindi si chiede solo quando la stima, raddoppiata per
	// prudenza, arriva vicino al limite.
	if bud.State != BudgetNone {
		in := EstimateTokens(req)
		if over(bud, Cost(t.Provider, t.Model, 2*in, 0), 2*in) {
			in = s.countTokens(cctx, prov, req)
		}
		if over(bud, Cost(t.Provider, t.Model, in, 0), in) {
			rec.Outcome, rec.ErrorCode, rec.In = "refused", "budget", in
			s.record(ctx, meta.TenantID, rec)
			return nil, ErrBudgetCall
		}
	}
	start := s.o.Now()
	var resp *Response
	if st, ok := prov.(Streamer); ok && onText != nil {
		resp, err = st.ChatStream(cctx, req, onText)
	} else {
		resp, err = prov.Chat(cctx, req)
		if err == nil && onText != nil && resp.Message.Text != "" {
			onText(resp.Message.Text)
		}
	}
	rec.Duration = s.o.Now().Sub(start)
	if err != nil {
		// Nel log il motivo dato dal fornitore (mai il prompt): il registro ha solo il codice.
		s.o.Log.Warn("chiamata al modello non riuscita", "funzione", t.Feature, "fornitore", t.Provider.Kind,
			"modello", t.Model, "codice", ErrorCode(err), "errore", clipRunes(err.Error(), 500))
		rec.Outcome, rec.ErrorCode = "error", ErrorCode(err)
		s.record(ctx, meta.TenantID, rec)
		return nil, err
	}
	rec.In, rec.Out, rec.CacheRead, rec.CacheWrite = resp.InputTokens, resp.OutputTokens, resp.CacheReadTokens, resp.CacheWriteTokens
	if resp.Model != "" {
		rec.Model = resp.Model
	}
	rec.Cost = CostCached(t.Provider, t.Model, resp.InputTokens, resp.CacheReadTokens, resp.CacheWriteTokens, resp.OutputTokens)
	rec.Outcome = "ok"
	if resp.Stop == StopRefusal {
		rec.Outcome, rec.ErrorCode = "refused", "model_refusal"
	}
	s.record(ctx, meta.TenantID, rec)
	s.warnBudget(ctx, meta)
	return resp, nil
}

// countTokens conta i token dell'input: con l'API del fornitore se l'adattatore la offre
// (TokenCounter), altrimenti, o se il conteggio non riesce, con la stima.
func (s *Service) countTokens(ctx context.Context, prov Provider, req Request) int64 {
	if tc, ok := prov.(TokenCounter); ok {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		n, err := tc.CountTokens(cctx, req)
		if err == nil && n > 0 {
			return n
		}
		s.o.Log.Debug("conteggio dei token non riuscito, uso la stima", "errore", err)
	}
	return EstimateTokens(req)
}

// over dice se una chiamata con quel costo e quei token d'input supererebbe il budget del mese.
func over(b Budget, cost float64, in int64) bool {
	if b.LimitUSD != nil && *b.LimitUSD > 0 && b.SpentUSD+cost > *b.LimitUSD {
		return true
	}
	return b.LimitTokens != nil && *b.LimitTokens > 0 && b.Tokens+in > *b.LimitTokens
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
	In, Out, CacheRead, CacheWrite       int64
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
			cost_usd, element_ids, prompt_sha256, outcome, error_code, duration_ms, conversation_id, cache_read_tokens, cache_write_tokens)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
			nullable(r.MemberID), r.Feature, r.Kind, r.Model, nullable(r.Tier), r.In, r.Out, r.Cost, ids, r.PromptHash,
			r.Outcome, nullable(r.ErrorCode), r.Duration.Milliseconds(), nullable(r.ConversationID), r.CacheRead, r.CacheWrite)
		return err
	})
	if err != nil {
		s.o.Log.Warn("registro delle chiamate AI", "errore", err, "funzione", r.Feature, "esito", r.Outcome)
	}
}

// warnBudget: al primo superamento dell'80% nel mese resta una voce nel registro di audit e
// chi amministra l'organizzazione riceve una notifica (l'avviso si vede anche in
// Configurazione → AI e nel pannello Assistente).
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
		if err := audit.Append(ctx, tx, audit.Event{Action: "ai.budget.warning", EntityType: "ai_settings",
			Payload: map[string]any{"percent": int(b.Percent), "month": b.Month}}); err != nil {
			return err
		}
		// Notifica (e email, se attive) a chi amministra l'organizzazione, una volta al mese.
		rows, err := tx.Query(ctx, notify.AdminsSQL)
		if err != nil {
			return err
		}
		admins, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		_, err = notify.EmitDirect(ctx, tx, notify.KindAI, admins, map[string]any{"event": "budget_warning", "percent": int(b.Percent), "month": b.Month})
		return err
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
	// Features: le funzioni della scheda pronte (AI accesa e modello del loro livello), per
	// mostrare solo i pulsanti che funzionano. Senza AI è vuoto e la scheda non cambia.
	Features map[string]bool `json:"features"`
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
	out.Features = map[string]bool{}
	if st.Enabled {
		for _, f := range append(CardFeatures[:len(CardFeatures):len(CardFeatures)], FeatureWiki, FeatureGRC) {
			if ft, _, ferr := s.Resolve(ctx, tx, f); ferr == nil && ft != nil && (f != FeatureWiki || ft.Tools) {
				out.Features[f] = true
			} else if ferr != nil && !errors.As(ferr, &ae) {
				return out, ferr
			}
		}
	}
	if out.Budget, err = s.budget(ctx, tx); err != nil {
		return out, err
	}
	return out, nil
}
