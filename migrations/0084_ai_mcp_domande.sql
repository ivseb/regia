-- AI, ondata 2: server MCP, domande sul grafo, cache del prompt (replica/ai.md §2 punti 3 e 4, §6 punto 2).
--
-- mcp_enabled: il server MCP ha un interruttore suo, spento di default e indipendente da
-- «enabled». Con l'MCP il modello è quello della persona (Claude Desktop, Claude Code, un IDE):
-- Regia non chiama nessun fornitore e non può mascherare i dati personali, perché li legge il
-- client. Per questo l'amministratore lo accende a parte, anche senza un fornitore configurato.
alter table ai_settings add column mcp_enabled boolean not null default false;

-- Token letti dalla cache del prompt e scritti nella cache (Anthropic). input_tokens resta il
-- totale dell'input; il costo stimato tiene conto dei prezzi diversi delle due parti.
alter table ai_calls
  add column cache_read_tokens integer not null default 0,
  add column cache_write_tokens integer not null default 0;

-- Il registro racconta anche gli strumenti chiamati da un client MCP (funzione «mcp»,
-- fornitore «client», modello «strumento:<nome>») e le proposte rimaste in sospeso: una
-- proposta senza conversazione viene dall'MCP e scade con la conservazione del tenant.
create index on ai_proposals (tenant_id, created_at) where conversation_id is null;
