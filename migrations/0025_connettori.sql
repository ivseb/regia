-- Connettori DevSecOps, fase 1 (replica/devsecops.md §2 e §4): connettore GitHub e GitHub
-- Enterprise Server, profili di pipeline versionati, repository scoperti con l'associazione
-- alle applicazioni del catalogo, stato delle sincronizzazioni.
--
-- La configurazione dell'integrazione sta in integrations (schema iniziale): kind 'github',
-- config jsonb (URL base, organizzazioni, modalità di autenticazione, intervallo, profilo),
-- secret_enc (token, chiave privata della App e segreto del webhook, cifrati con la chiave
-- applicativa, mai restituiti dalle API).

alter table integrations add column updated_at timestamptz not null default now();

-- =====================================================================
-- 1. Profili di pipeline: configurazione, non codice
-- =====================================================================

-- Un profilo spiega dove una famiglia di template deposita i suoi artefatti (allegati delle
-- Release, cartella degli ambienti del repository GitOps) e come leggerli. Ogni salvataggio
-- è una nuova versione; la sincronizzazione usa la versione corrente.
create table pipeline_profiles (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  key text not null check (key ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
  name text not null check (length(name) between 1 and 120),
  description text not null default '',
  preset text,                              -- preset di partenza, se nato da uno
  current_version integer not null default 1,
  created_by uuid,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create unique index pipeline_profiles_key_uq on pipeline_profiles (tenant_id, key);
create index on pipeline_profiles (tenant_id, created_by);
select enable_tenant_rls('pipeline_profiles');

create table pipeline_profile_versions (
  tenant_id uuid not null,
  profile_id uuid not null,
  version integer not null check (version >= 1),
  definition jsonb not null,
  note text not null default '',
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, profile_id, version),
  foreign key (tenant_id, profile_id) references pipeline_profiles (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on pipeline_profile_versions (tenant_id, created_by);
select enable_tenant_rls('pipeline_profile_versions');

-- =====================================================================
-- 2. Repository scoperti e associazione alle applicazioni
-- =====================================================================

-- Una riga per repository letto dall'organizzazione. La proposta di associazione (per nome
-- o codice dell'applicazione) diventa effettiva solo dopo la conferma da interfaccia:
-- solo i repository "confirmed" diventano servizi del contratto e si sincronizzano.
create table connector_repositories (
  tenant_id uuid not null,
  id uuid not null,
  integration_id uuid not null,
  external_id text not null,                -- id numerico del repository su GitHub
  full_name text not null,                  -- org/nome
  name text not null,
  default_branch text not null default 'main',
  html_url text not null default '',
  description text not null default '',
  archived boolean not null default false,
  private boolean not null default false,
  missing_since timestamptz,                -- non più restituito dall'organizzazione
  mapping_status text not null default 'none' check (mapping_status in ('none', 'suggested', 'confirmed', 'ignored')),
  application_element_id uuid,              -- associazione confermata
  suggested_element_id uuid,                -- proposta
  suggestion_score real,
  suggestion_reason text,
  service_name text not null,               -- nome del servizio nel contratto (di solito il nome del repository)
  cursors jsonb not null default '{}',      -- cursori della sincronizzazione incrementale
  last_synced_at timestamptz,
  last_error text,
  confirmed_by uuid,
  confirmed_at timestamptz,
  discovered_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, integration_id) references integrations (tenant_id, id) on delete cascade,
  foreign key (tenant_id, application_element_id) references elements (tenant_id, id) on delete set null (application_element_id),
  foreign key (tenant_id, suggested_element_id) references elements (tenant_id, id) on delete set null (suggested_element_id),
  foreign key (tenant_id, confirmed_by) references members (tenant_id, id) on delete set null (confirmed_by)
);
create unique index connector_repositories_ext_uq on connector_repositories (tenant_id, integration_id, external_id);
create index on connector_repositories (tenant_id, application_element_id);
create index on connector_repositories (tenant_id, suggested_element_id);
create index on connector_repositories (tenant_id, confirmed_by);
create index on connector_repositories (tenant_id, lower(full_name));
select enable_tenant_rls('connector_repositories');

-- =====================================================================
-- 3. Stato e storico delle sincronizzazioni
-- =====================================================================

-- Una riga per integrazione: prossima esecuzione, lease (una sola sincronizzazione alla
-- volta anche con più processi), ultimo esito, limiti di velocità visti, cursori del
-- repository GitOps (uno per integrazione, condiviso dai servizi).
create table connector_sync_state (
  tenant_id uuid not null,
  integration_id uuid not null,
  next_run_at timestamptz,
  lease_until timestamptz,
  last_started_at timestamptz,
  last_finished_at timestamptz,
  last_status text check (last_status in ('running', 'ok', 'partial', 'error', 'rate_limited')),
  last_error text,
  rate_limit jsonb,
  cursors jsonb not null default '{}',
  primary key (tenant_id, integration_id),
  foreign key (tenant_id, integration_id) references integrations (tenant_id, id) on delete cascade
);
select enable_tenant_rls('connector_sync_state');

create table connector_sync_runs (
  tenant_id uuid not null,
  id uuid not null,
  integration_id uuid not null,
  trigger text not null check (trigger in ('schedule', 'manual', 'webhook')),
  scope text not null default '',
  status text not null check (status in ('running', 'ok', 'partial', 'error', 'rate_limited')),
  stats jsonb not null default '{}',
  error text,
  started_at timestamptz not null default now(),
  finished_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, integration_id) references integrations (tenant_id, id) on delete cascade
);
create index on connector_sync_runs (tenant_id, integration_id, started_at desc);
select enable_tenant_rls('connector_sync_runs');

-- Il webhook in ingresso arriva senza sessione né tenant: questa funzione dice solo a quale
-- tenant appartiene un'integrazione attiva di quel tipo; la firma si verifica poi sotto RLS.
create or replace function connector_integration_tenant(p_id uuid, p_kind text) returns uuid
language sql stable security definer set search_path = public, pg_temp as $$
  select tenant_id from integrations where id = p_id and kind = p_kind and active
$$;
revoke all on function connector_integration_tenant(uuid, text) from public;
grant execute on function connector_integration_tenant(uuid, text) to mytrack_app;
