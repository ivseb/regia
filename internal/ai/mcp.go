package ai

import (
	"context"
	"crypto/sha256"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/ai/tools"
	"regia/internal/engine"
	"regia/internal/platform/db"
)

// Server MCP (replica/ai.md §2 punto 4, ondata 2): i pezzi del livello AI che usa il server in
// internal/ai/mcpserver. Il modello è quello della persona (Claude Desktop, Claude Code, un
// IDE): Regia non chiama nessun fornitore, espone gli strumenti con i permessi della persona e
// registra ogni chiamata in ai_calls con funzione «mcp» e fornitore «client», senza token né
// costo. Le scritture restano proposte, che la persona conferma con lo strumento «conferma».

// FeatureMCP è la funzione del registro per le chiamate dei client MCP.
const FeatureMCP = "mcp"

// KindClient è il «fornitore» delle chiamate MCP: il modello è del client della persona.
const KindClient = "client"

// MCPEnabled dice se il server MCP è acceso nel tenant (interruttore suo, spento di default).
func (s *Service) MCPEnabled(ctx context.Context, tx pgx.Tx) (bool, error) {
	st, err := LoadSettings(ctx, tx)
	return st.MCPEnabled, err
}

// MCPCall è una chiamata di strumento da un client MCP, per il registro.
type MCPCall struct {
	TenantID string
	MemberID string
	Tool     string
	Input    []byte
	// Outcome: ok, error o refused (strumento non consentito).
	Outcome    string
	ErrorCode  string
	ElementIDs []string
	Duration   time.Duration
}

// RecordMCP scrive la chiamata in ai_calls: l'impronta è quella dell'input, mai il contenuto.
func (s *Service) RecordMCP(ctx context.Context, c MCPCall) {
	h := sha256.Sum256(append([]byte(c.Tool+"\x00"), c.Input...))
	s.record(ctx, c.TenantID, callRecord{MemberID: c.MemberID, Feature: FeatureMCP, Kind: KindClient, Model: "strumento:" + c.Tool,
		Outcome: c.Outcome, ErrorCode: c.ErrorCode, PromptHash: h[:], ElementIDs: c.ElementIDs, Duration: c.Duration})
}

// ProposeMCP salva una proposta di scrittura preparata da un client MCP (senza conversazione):
// la persona la conferma o la rifiuta con gli strumenti «conferma» e «rifiuta», o la lascia
// scadere con la conservazione del tenant.
func (s *Service) ProposeMCP(ctx context.Context, tenantID string, actor engine.Actor, tool string, res *tools.Result) (string, error) {
	return s.saveProposal(ctx, tenantID, actor, "", tool, res)
}

// PendingMCP sono le proposte dei client MCP della persona ancora da decidere, le più recenti prima.
func (s *Service) PendingMCP(ctx context.Context, tenantID string, actor engine.Actor) ([]ProposalView, error) {
	var out []ProposalView
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		props, err := queryProposals(ctx, tx, `conversation_id is null and feature is null and member_id = $1 and status = 'pending'
			order by created_at desc limit 20`, actor.MemberID)
		for _, p := range props {
			out = append(out, p)
		}
		return err
	})
	slices.SortFunc(out, func(a, b ProposalView) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, err
}

// ToolError è il testo di un errore di strumento da mostrare al modello (senza dettagli interni).
func ToolError(err error) string { return toolError(err) }
