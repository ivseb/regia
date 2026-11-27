-- AI, ondata 6: adattatori nativi (replica/ai.md §3 e §6 punto 6).
--
-- Tipi di fornitore nuovi accanto ad Anthropic e al compatibile OpenAI: OpenAI (Responses API),
-- Google Gemini, Ollama (API nativa) e Anthropic via AWS Bedrock e via Google Vertex per tenere
-- i dati nella regione UE. Per i cloud servono la regione e, per Vertex, il progetto: non sono
-- segreti e restano in chiaro. Le credenziali (chiave API di Bedrock o chiavi d'accesso AWS, chiave
-- JSON del service account Google) vanno in secret_enc come le altre chiavi: cifrate con la chiave
-- applicativa legata al fornitore, mai restituite né scritte nei log.

alter table ai_providers drop constraint ai_providers_kind_check;
alter table ai_providers add constraint ai_providers_kind_check check (kind in
  ('anthropic','anthropic_bedrock','anthropic_vertex','openai','gemini','ollama','openai_compat','fake'));

alter table ai_providers
  add column region text check (region is null or region ~ '^[a-z0-9-]{2,40}$'),
  add column project_id text check (project_id is null or project_id ~ '^[a-z][a-z0-9-]{4,28}[a-z0-9]$');
