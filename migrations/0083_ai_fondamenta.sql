-- AI, ondata 1: le fondamenta (replica/ai.md §3, §4, §6 punto 1, §7).
-- ai_settings e ai_calls esistono dalla 0001 (con la RLS per tenant); qui si aggiungono i
-- fornitori, i livelli «rapido» e «approfondito», le funzioni spostate, il budget in dollari,
-- la conservazione delle conversazioni, le conversazioni dell'assistente e le proposte da
-- confermare. La colonna ai_settings.provider della 0001 non si usa più: il fornitore sta in
-- ai_providers, e un tenant può averne più d'uno (es. Ollama locale per la wiki, Claude per l'analisi).

-- Fornitori del modello del tenant. La chiave è cifrata con la chiave applicativa legata al
-- fornitore (secretbox, contesto «ai-provider:<id>»), come i token dei connettori: non torna
-- mai indietro dall'API e non finisce nei log. no_tools_models: modelli che l'amministratore
-- segna come incapaci di chiamare strumenti (fanno solo riassunti e bozze). I prezzi sono in
-- dollari per milione di token; vuoti = listino noto del modello (Anthropic) oppure zero.
create table ai_providers (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  kind text not null check (kind in ('anthropic','openai_compat','fake')),
  name text not null check (length(name) between 1 and 80),
  base_url text check (base_url is null or length(base_url) <= 500),
  secret_enc bytea,
  no_tools_models text[] not null default '{}',
  price_input_usd numeric(10,4) check (price_input_usd is null or price_input_usd >= 0),
  price_output_usd numeric(10,4) check (price_output_usd is null or price_output_usd >= 0),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id)
);
select enable_tenant_rls('ai_providers');

-- Livelli, funzioni, budget, conservazione. routes: funzione → {"tier": "rapido"|"approfondito"}
-- oppure {"provider_id", "model"}; una funzione assente segue il suo livello predefinito.
-- mask_personal_data resta quella della 0001 (default true): al primo salvataggio il servizio
-- la imposta accesa per i tenant regolamentati e spenta per gli altri, poi decide l'amministratore.
alter table ai_settings
  add column monthly_budget_usd numeric(12,2) check (monthly_budget_usd is null or monthly_budget_usd > 0),
  add column retention_days integer not null default 90 check (retention_days between 1 and 3650),
  add column fast_provider_id uuid,
  add column fast_model text check (fast_model is null or length(fast_model) <= 200),
  add column deep_provider_id uuid,
  add column deep_model text check (deep_model is null or length(deep_model) <= 200),
  add column routes jsonb not null default '{}',
  add column budget_warned_month date,
  add column created_at timestamptz not null default now();
alter table ai_settings add constraint ai_settings_budget_tokens_check check (monthly_token_budget is null or monthly_token_budget > 0);
alter table ai_settings add foreign key (tenant_id, fast_provider_id) references ai_providers (tenant_id, id) on delete set null (fast_provider_id);
alter table ai_settings add foreign key (tenant_id, deep_provider_id) references ai_providers (tenant_id, id) on delete set null (deep_provider_id);

-- Registro delle chiamate: livello, costo stimato (dollari, al listino), durata, codice
-- dell'errore e conversazione. Il budget si calcola da qui.
alter table ai_calls
  add column tier text,
  add column cost_usd numeric(14,6) not null default 0,
  add column duration_ms integer,
  add column error_code text check (error_code is null or length(error_code) <= 80),
  add column conversation_id uuid;
create index on ai_calls (tenant_id, conversation_id);

-- Conversazioni con l'assistente: private della persona, cancellate con il membro e dopo
-- retention_days giorni dall'ultimo messaggio (job orario).
create table ai_conversations (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  member_id uuid not null,
  title text not null default '' check (length(title) <= 200),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on ai_conversations (tenant_id, member_id, updated_at desc);
create index on ai_conversations (tenant_id, updated_at);
select enable_tenant_rls('ai_conversations');

-- Messaggi: la domanda della persona e la risposta dell'assistente, con gli elementi citati
-- (refs) e le proposte. Gli scambi con gli strumenti non si conservano: si rifanno.
create table ai_messages (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  conversation_id uuid not null,
  seq integer not null,
  role text not null check (role in ('user','assistant')),
  content jsonb not null default '{}',
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  unique (tenant_id, conversation_id, seq),
  foreign key (tenant_id, conversation_id) references ai_conversations (tenant_id, id) on delete cascade
);
select enable_tenant_rls('ai_messages');

-- Proposte di scrittura sui dati condivisi (crea, aggiorna, collega, commenta, registra ore):
-- l'assistente le prepara, la persona le conferma o le rifiuta (replica/ai.md §1).
create table ai_proposals (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  conversation_id uuid,
  member_id uuid not null,
  tool text not null check (length(tool) <= 40),
  input jsonb not null default '{}',
  summary text not null default '' check (length(summary) <= 2000),
  status text not null default 'pending' check (status in ('pending','applied','rejected','failed')),
  result jsonb,
  error text check (error is null or length(error) <= 1000),
  created_at timestamptz not null default now(),
  decided_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, conversation_id) references ai_conversations (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on ai_proposals (tenant_id, member_id, status);
create index on ai_proposals (tenant_id, conversation_id);
select enable_tenant_rls('ai_proposals');
