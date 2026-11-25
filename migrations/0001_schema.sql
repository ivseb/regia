-- =====================================================================
-- myTrack: schema v2 (bozza da rivedere)
-- Postgres 17+ (target consigliato a runtime: 18). Multi-tenant, motore unico di elementi.
--
-- Convenzioni
-- * Tabelle del tenant: tenant_id uuid not null + primary key (tenant_id, id).
--   Le FK verso altre tabelle del tenant sono composite (tenant_id, x_id): un riferimento
--   tra tenant diversi è impossibile per costruzione.
-- * id: UUIDv7 generati dall'app (ordinati nel tempo, indici compatti); default di riserva gen_random_uuid().
-- * Tempi in timestamptz (UTC). Importi in centesimi + valuta. Durate in minuti.
-- * Row Level Security su ogni tabella del tenant (in fondo al file) come seconda barriera:
--   l'app imposta set_config('app.tenant_id', ..., true) all'inizio di ogni transazione.
-- * on delete esplicito ovunque; "set null (col)" per non annullare tenant_id nelle FK composite.
-- =====================================================================

create extension if not exists pgcrypto;
create extension if not exists unaccent;
create extension if not exists pg_trgm;

create or replace function app_tenant() returns uuid language sql stable as $$
  select nullif(current_setting('app.tenant_id', true), '')::uuid
$$;

create or replace function set_updated_at() returns trigger language plpgsql as $$
begin new.updated_at := now(); return new; end $$;

-- =====================================================================
-- 1. PIATTAFORMA (globale, fuori dai tenant)
-- =====================================================================

create table tenants (
  id uuid primary key default gen_random_uuid(),
  slug text not null unique check (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
  name text not null,
  status text not null default 'active' check (status in ('active','suspended','offboarding')),
  isolation text not null default 'shared' check (isolation in ('shared','dedicated')),
  data_region text not null default 'eu',
  default_locale text not null default 'it',
  default_timezone text not null default 'Europe/Rome',
  base_currency char(3) not null default 'EUR',  -- valuta di riferimento; le altre si convertono con exchange_rates
  settings jsonb not null default '{}',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

-- Identità globale: una persona può appartenere a più tenant (es. consulente su più clienti)
create table accounts (
  id uuid primary key default gen_random_uuid(),
  email text not null,
  password_hash text,                       -- argon2id; null per account solo SSO
  display_name text not null,
  mfa_secret_enc bytea,                     -- cifrato con chiave applicativa
  locale text not null default 'it',
  is_platform_admin boolean not null default false,
  last_login_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create unique index accounts_email_uq on accounts (lower(email));

create table sessions (
  id uuid primary key default gen_random_uuid(),
  account_id uuid not null references accounts(id) on delete cascade,
  token_hash bytea not null unique,         -- sha256 del token nel cookie
  active_tenant_id uuid references tenants(id) on delete set null,
  ip inet,
  user_agent text,
  expires_at timestamptz not null,
  created_at timestamptz not null default now()
);
create index on sessions (account_id);
create index on sessions (active_tenant_id);
create index on sessions (expires_at);

-- Coda di job su Postgres (SELECT ... FOR UPDATE SKIP LOCKED), nessun broker esterno
create table jobs (
  id bigint generated always as identity primary key,
  tenant_id uuid references tenants(id) on delete cascade,
  kind text not null,
  payload jsonb not null default '{}',
  status text not null default 'queued' check (status in ('queued','running','done','failed','dead')),
  priority smallint not null default 0,
  run_at timestamptz not null default now(),
  attempts integer not null default 0,
  max_attempts integer not null default 10,
  locked_until timestamptz,
  last_error text,
  dedupe_key text,
  created_at timestamptz not null default now()
);
create index jobs_ready_idx on jobs (priority desc, run_at) where status = 'queued';
create index on jobs (tenant_id);
create unique index jobs_dedupe_uq on jobs (kind, dedupe_key) where dedupe_key is not null and status in ('queued','running');

-- =====================================================================
-- 2. LIBRERIA NORMATIVA (globale + cataloghi privati dei tenant)
--    owner_tenant_id null = catalogo di sistema, in sola lettura per tutti.
-- =====================================================================

create table frameworks (
  id uuid primary key default gen_random_uuid(),
  owner_tenant_id uuid references tenants(id) on delete cascade,
  key text not null,                        -- 'dora', 'nis2', 'iso27001', 'gdpr', 'agid-mm'...
  name text not null,
  kind text not null check (kind in ('regulation','directive','standard','guideline','internal')),
  jurisdiction text,                        -- 'EU', 'IT', 'INT'
  publisher text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create unique index frameworks_key_uq on frameworks (coalesce(owner_tenant_id, '00000000-0000-0000-0000-000000000000'::uuid), key);
create index on frameworks (owner_tenant_id);

create table framework_versions (
  id uuid primary key default gen_random_uuid(),
  framework_id uuid not null references frameworks(id) on delete cascade,
  version text not null,
  effective_from date,
  source_url text,
  license_note text,                        -- es. ISO: solo identificativi e titoli
  status text not null default 'draft' check (status in ('draft','published','superseded')),
  created_at timestamptz not null default now(),
  unique (framework_id, version)
);

create table requirements (
  id uuid primary key default gen_random_uuid(),
  version_id uuid not null references framework_versions(id) on delete cascade,
  parent_id uuid references requirements(id) on delete cascade,
  ref text not null,                        -- 'Art. 28(3)', 'A.5.23'
  title text not null,
  body text,                                -- null quando la licenza non consente il testo
  kind text not null default 'requirement' check (kind in ('chapter','article','paragraph','requirement','control','annex')),
  ordinal integer not null default 0,
  metadata jsonb not null default '{}',     -- es. proprietà OSCAL
  unique (version_id, ref)
);
create index on requirements (parent_id);
create index on requirements (version_id, ordinal);

create table requirement_mappings (
  id uuid primary key default gen_random_uuid(),
  owner_tenant_id uuid references tenants(id) on delete cascade,
  from_requirement_id uuid not null references requirements(id) on delete cascade,
  to_requirement_id uuid not null references requirements(id) on delete cascade,
  relation text not null check (relation in ('equivalent','subset','superset','related')),
  source text,
  created_at timestamptz not null default now(),
  constraint no_self_map check (from_requirement_id <> to_requirement_id)
);
create index on requirement_mappings (from_requirement_id);
create index on requirement_mappings (to_requirement_id);
create index on requirement_mappings (owner_tenant_id);

-- Regole di notifica (scadenze come dati, mai nel codice)
create table notification_rules (
  id uuid primary key default gen_random_uuid(),
  version_id uuid not null references framework_versions(id) on delete cascade,
  key text not null,
  stage text not null,                      -- 'initial','intermediate','final','early_warning'...
  from_event text not null,                 -- 'detected','classified','previous_stage'
  deadline_minutes integer not null check (deadline_minutes > 0),
  description text,
  unique (version_id, key)
);

-- =====================================================================
-- 3. MEMBRI, GRUPPI, RUOLI (per tenant)
-- =====================================================================

create table members (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  account_id uuid references accounts(id) on delete set null,   -- null = account di servizio o anonimizzato
  display_name text not null,
  email text,
  member_type text not null default 'standard' check (member_type in ('standard','requester','guest','auditor','service')),  -- requester = solo portale richieste
  status text not null default 'active' check (status in ('invited','active','suspended','anonymized')),
  access_until timestamptz,                 -- accesso a tempo (auditor)
  weekly_hours numeric not null default 40 check (weekly_hours between 0 and 80),  -- base per la capacità
  timezone text,
  locale text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id)
);
create unique index members_account_uq on members (tenant_id, account_id) where account_id is not null;
create index on members (account_id);

create table groups (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  parent_id uuid,
  system_kind text check (system_kind in ('all_members')),
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, parent_id) references groups (tenant_id, id) on delete set null (parent_id)
);
create unique index groups_name_uq on groups (tenant_id, lower(name));
create index on groups (tenant_id, parent_id);

create table group_members (
  tenant_id uuid not null,
  group_id uuid not null,
  member_id uuid not null,
  primary key (tenant_id, group_id, member_id),
  foreign key (tenant_id, group_id) references groups (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on group_members (tenant_id, member_id);

create table roles (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  key text,                                 -- ruoli di sistema: 'tenant_admin','space_admin','editor','viewer','auditor'
  name text not null,
  permissions text[] not null default '{}', -- es. {'element.read','element.write','grc.evidence.approve'}
  created_at timestamptz not null default now(),
  primary key (tenant_id, id)
);
create unique index roles_key_uq on roles (tenant_id, key) where key is not null;

create table api_tokens (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  member_id uuid not null,
  name text not null,
  token_hash bytea not null unique,
  scopes text[] not null default '{}',
  expires_at timestamptz,
  last_used_at timestamptz,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on api_tokens (tenant_id, member_id);

-- =====================================================================
-- 4. SPAZI (contenitori: progetto, spazio knowledge, portafoglio, GRC, architettura)
-- =====================================================================

create table spaces (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  key text not null check (key ~ '^[A-Z][A-Z0-9_]{0,19}$'),   -- prefisso degli ID leggibili: OPS-42
  name text not null,
  kind text not null check (kind in ('project','knowledge','portfolio','compliance','architecture','service')),
  description text,
  lead_id uuid,
  archived boolean not null default false,
  next_number bigint not null default 1 check (next_number > 0),
  default_visibility_group_id uuid,
  settings jsonb not null default '{}',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, lead_id) references members (tenant_id, id) on delete set null (lead_id),
  foreign key (tenant_id, default_visibility_group_id) references groups (tenant_id, id) on delete set null (default_visibility_group_id)
);
create unique index spaces_key_uq on spaces (tenant_id, key);
create index on spaces (tenant_id, lead_id);
create index on spaces (tenant_id, default_visibility_group_id);
create trigger spaces_updated before update on spaces for each row execute function set_updated_at();

create table role_assignments (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  role_id uuid not null,
  member_id uuid,
  group_id uuid,
  space_id uuid,                            -- null = intero tenant
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, role_id) references roles (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, group_id) references groups (tenant_id, id) on delete cascade,
  foreign key (tenant_id, space_id) references spaces (tenant_id, id) on delete cascade,
  constraint ra_holder check ((member_id is null) <> (group_id is null))
);
create index on role_assignments (tenant_id, role_id);
create index on role_assignments (tenant_id, member_id);
create index on role_assignments (tenant_id, group_id);
create index on role_assignments (tenant_id, space_id);

-- =====================================================================
-- 5. MOTORE ELEMENTI: insiemi di valori, campi, tipi, workflow
-- =====================================================================

create table option_sets (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  kind text not null default 'enum' check (kind in ('enum','state','version')),
  created_at timestamptz not null default now(),
  primary key (tenant_id, id)
);

create table options (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  option_set_id uuid not null,
  name text not null,
  color text,
  ordinal integer not null default 0,
  archived boolean not null default false,
  category text check (category in ('todo','in_progress','done','cancelled')),  -- solo per stati
  release_date date,                        -- solo per versioni
  released boolean not null default false,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, option_set_id) references option_sets (tenant_id, id) on delete cascade
);
create unique index options_name_uq on options (tenant_id, option_set_id, lower(name));
create index on options (tenant_id, option_set_id, ordinal);

create table field_defs (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  key text not null check (key ~ '^[a-z][a-z0-9_]{0,62}$'),
  name text not null,
  data_type text not null check (data_type in (
    'string','text','integer','decimal','boolean','date','datetime','duration','money','url',
    'enum','state','version','member','group','element')),
  is_multi boolean not null default false,
  option_set_id uuid,
  settings jsonb not null default '{}',     -- es. tipi ammessi per 'element', valuta per 'money'
  aliases text[] not null default '{}',
  is_system boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, option_set_id) references option_sets (tenant_id, id) on delete restrict,
  constraint optionset_needed check ((data_type in ('enum','state','version')) = (option_set_id is not null)),
  constraint single_state check (data_type <> 'state' or not is_multi)
);
create unique index field_defs_key_uq on field_defs (tenant_id, key);
create index on field_defs (tenant_id, option_set_id);

create table workflows (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  version integer not null default 1,
  -- macchina a stati: {"states":[...],"transitions":[{"from","to","guard","roles","requires","actions"}]}
  definition jsonb not null default '{}',
  is_system boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id)
);

create table element_types (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  key text not null check (key ~ '^[a-z][a-z0-9_]{0,62}$'),  -- 'task','bug','epic','page','risk','control','incident','application','ict_provider'...
  name text not null,
  module text not null check (module in ('work','knowledge','strategy','devsecops','grc','architecture','core')),
  extension text,                           -- tabella di estensione di dominio, es. 'risk' -> ext_risk
  icon text,
  color text,
  state_set_id uuid,                        -- insieme degli stati
  workflow_id uuid,
  is_system boolean not null default false,
  portal_enabled boolean not null default false,  -- apribile dai richiedenti del portale
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, state_set_id) references option_sets (tenant_id, id) on delete restrict,
  foreign key (tenant_id, workflow_id) references workflows (tenant_id, id) on delete set null (workflow_id)
);
create unique index element_types_key_uq on element_types (tenant_id, key);
create index on element_types (tenant_id, state_set_id);
create index on element_types (tenant_id, workflow_id);

create table space_types (
  tenant_id uuid not null,
  space_id uuid not null,
  type_id uuid not null,
  primary key (tenant_id, space_id, type_id),
  foreign key (tenant_id, space_id) references spaces (tenant_id, id) on delete cascade,
  foreign key (tenant_id, type_id) references element_types (tenant_id, id) on delete cascade
);
create index on space_types (tenant_id, type_id);

create table type_fields (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  type_id uuid not null,
  field_id uuid not null,
  space_id uuid,                            -- null = default del tipo; valorizzato = override per spazio
  required boolean not null default false,
  default_value jsonb,
  ordinal integer not null default 0,
  is_private boolean not null default false,
  show_in_list boolean not null default true,
  condition jsonb,                          -- campo visibile solo se un altro campo ha certi valori
  primary key (tenant_id, id),
  foreign key (tenant_id, type_id) references element_types (tenant_id, id) on delete cascade,
  foreign key (tenant_id, field_id) references field_defs (tenant_id, id) on delete cascade,
  foreign key (tenant_id, space_id) references spaces (tenant_id, id) on delete cascade
);
create unique index type_fields_uq on type_fields (tenant_id, type_id, field_id, coalesce(space_id, '00000000-0000-0000-0000-000000000000'::uuid));
create index on type_fields (tenant_id, field_id);
create index on type_fields (tenant_id, space_id);

-- =====================================================================
-- 6. ELEMENTI (nodi del grafo) e relazioni
-- =====================================================================

create table elements (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  space_id uuid not null,
  type_id uuid not null,
  number bigint,                            -- null finché è una bozza
  title text not null default '',
  body jsonb,                               -- documento strutturato (compatibile con un CRDT futuro)
  body_text text not null default '',       -- testo piano estratto dall'app, per la ricerca
  state_id uuid,
  assignee_id uuid,
  reporter_id uuid,
  parent_id uuid,                           -- unica gerarchia (epic > story > task, obiettivo > iniziativa...)
  rank text,                                -- indice frazionario per l'ordinamento manuale
  start_at timestamptz,
  due_at timestamptz,
  resolved_at timestamptz,
  is_draft boolean not null default false,
  draft_owner_id uuid,
  visibility_limited boolean not null default false,
  fields jsonb not null default '{}',       -- valori dei campi: fonte di verità {"<field_key>": valore | [valori]}
  version integer not null default 1,       -- lock ottimistico
  created_by uuid,
  updated_by uuid,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  deleted_at timestamptz,                   -- cancellazione logica (tracciabilità)
  search tsvector,
  primary key (tenant_id, id),
  foreign key (tenant_id, space_id) references spaces (tenant_id, id) on delete cascade,
  foreign key (tenant_id, type_id) references element_types (tenant_id, id) on delete restrict,
  foreign key (tenant_id, state_id) references options (tenant_id, id) on delete restrict,
  foreign key (tenant_id, assignee_id) references members (tenant_id, id) on delete set null (assignee_id),
  foreign key (tenant_id, reporter_id) references members (tenant_id, id) on delete set null (reporter_id),
  foreign key (tenant_id, parent_id) references elements (tenant_id, id) on delete set null (parent_id),
  foreign key (tenant_id, draft_owner_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by),
  constraint draft_number check (is_draft = (number is null)),
  constraint draft_owner check (not is_draft or draft_owner_id is not null),
  constraint no_self_parent check (parent_id is null or parent_id <> id)
);
create unique index elements_number_uq on elements (tenant_id, space_id, number) where number is not null;
-- indici per le viste più frequenti (tutti iniziano da tenant_id, come la policy RLS)
create index elements_list_idx on elements (tenant_id, space_id, updated_at desc) where deleted_at is null and not is_draft;
create index elements_type_state_idx on elements (tenant_id, type_id, state_id) where deleted_at is null;
create index elements_assignee_idx on elements (tenant_id, assignee_id) where deleted_at is null;
create index elements_parent_idx on elements (tenant_id, parent_id) where parent_id is not null;
create index elements_due_idx on elements (tenant_id, due_at) where due_at is not null and resolved_at is null;
create index on elements (tenant_id, reporter_id);
create index on elements (tenant_id, state_id);
create index on elements (tenant_id, draft_owner_id) where is_draft;
create index on elements (tenant_id, created_by);
create index on elements (tenant_id, updated_by);
create index on elements (tenant_id, created_at);
create index on elements (tenant_id, resolved_at);
create index elements_fields_idx on elements using gin (fields jsonb_path_ops);
create index elements_search_idx on elements using gin (search);
create index elements_title_trgm on elements using gin (title gin_trgm_ops);
create trigger elements_updated before update on elements for each row execute function set_updated_at();

create or replace function elements_search_update() returns trigger language plpgsql as $$
begin
  new.search :=
    setweight(to_tsvector('simple',  unaccent(coalesce(new.title,''))), 'A') ||
    setweight(to_tsvector('italian', unaccent(coalesce(new.title,''))), 'A') ||
    setweight(to_tsvector('english', unaccent(coalesce(new.title,''))), 'A') ||
    setweight(to_tsvector('italian', unaccent(left(coalesce(new.body_text,''), 200000))), 'B') ||
    setweight(to_tsvector('english', unaccent(left(coalesce(new.body_text,''), 200000))), 'B');
  return new;
end $$;
create trigger elements_search before insert or update of title, body_text on elements
  for each row execute function elements_search_update();

-- Indice tipizzato dei valori dei campi, derivato da elements.fields nella stessa transazione.
-- Serve a filtrare e ordinare per qualunque campo senza DDL dinamico per tenant.
create table element_values (
  tenant_id uuid not null,
  element_id uuid not null,
  field_id uuid not null,
  ord smallint not null default 0,          -- posizione per i campi multi-valore
  v_text text,
  v_num numeric,
  v_ts timestamptz,
  v_ref uuid,                               -- option, member, group o element
  primary key (tenant_id, element_id, field_id, ord),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, field_id) references field_defs (tenant_id, id) on delete cascade,
  constraint one_value check (num_nonnulls(v_text, v_num, v_ts, v_ref) = 1)
);
create index element_values_ref_idx on element_values (tenant_id, field_id, v_ref) where v_ref is not null;
create index element_values_num_idx on element_values (tenant_id, field_id, v_num) where v_num is not null;
create index element_values_ts_idx on element_values (tenant_id, field_id, v_ts) where v_ts is not null;
create index element_values_text_idx on element_values (tenant_id, field_id, v_text) where v_text is not null;

create table element_visibility (
  tenant_id uuid not null,
  element_id uuid not null,
  member_id uuid,
  group_id uuid,
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, group_id) references groups (tenant_id, id) on delete cascade,
  constraint ev_holder check ((member_id is null) <> (group_id is null))
);
create unique index element_visibility_uq on element_visibility (tenant_id, element_id, coalesce(member_id, group_id));
create index on element_visibility (tenant_id, member_id);
create index on element_visibility (tenant_id, group_id);

create table relation_types (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  key text not null check (key ~ '^[a-z][a-z0-9_]{0,62}$'),  -- 'depends_on','relates','duplicates','provided_by','supports','mitigates','evidence_for'...
  outward_name text not null,
  inward_name text not null,
  directed boolean not null default true,
  source_type_ids uuid[],                   -- null = qualunque tipo
  target_type_ids uuid[],
  attrs_schema jsonb,                       -- attributi ammessi sulla relazione (es. criticità della dipendenza)
  is_system boolean not null default false,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id)
);
create unique index relation_types_key_uq on relation_types (tenant_id, key);

create table relations (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  type_id uuid not null,
  source_id uuid not null,
  target_id uuid not null,
  attrs jsonb not null default '{}',
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, type_id) references relation_types (tenant_id, id) on delete cascade,
  foreign key (tenant_id, source_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, target_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by),
  constraint no_self_relation check (source_id <> target_id)
);
create unique index relations_uq on relations (tenant_id, type_id, source_id, target_id);
create index relations_out_idx on relations (tenant_id, source_id, type_id);
create index relations_in_idx on relations (tenant_id, target_id, type_id);
create index on relations (tenant_id, created_by);

-- =====================================================================
-- 7. COLLABORAZIONE: commenti, reazioni, allegati, tag, preferiti, voti
-- =====================================================================

create table comments (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,
  author_id uuid,
  body jsonb not null,
  body_text text not null default '',
  pinned boolean not null default false,
  visibility_limited boolean not null default false,
  anchor jsonb,                             -- commento inline su una porzione del documento
  deleted_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, author_id) references members (tenant_id, id) on delete set null (author_id)
);
create index on comments (tenant_id, element_id, created_at);
create index on comments (tenant_id, author_id);
create trigger comments_updated before update on comments for each row execute function set_updated_at();

create table reactions (
  tenant_id uuid not null,
  comment_id uuid not null,
  member_id uuid not null,
  emoji text not null,
  created_at timestamptz not null default now(),
  primary key (tenant_id, comment_id, member_id, emoji),
  foreign key (tenant_id, comment_id) references comments (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on reactions (tenant_id, member_id);

create table attachments (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,
  comment_id uuid,
  author_id uuid,
  name text not null,
  mime_type text not null,
  size_bytes bigint not null check (size_bytes >= 0),
  sha256 bytea not null,                    -- integrità (le evidenze devono essere verificabili)
  storage_key text not null,
  encryption_key_id text,
  width integer,
  height integer,
  removed_at timestamptz,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, comment_id) references comments (tenant_id, id) on delete cascade,
  foreign key (tenant_id, author_id) references members (tenant_id, id) on delete set null (author_id)
);
create unique index attachments_storage_uq on attachments (tenant_id, storage_key);
create index on attachments (tenant_id, element_id);
create index on attachments (tenant_id, comment_id);
create index on attachments (tenant_id, author_id);

create table tags (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  color text,
  owner_id uuid,                            -- null = tag condiviso del tenant
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, owner_id) references members (tenant_id, id) on delete cascade
);
create unique index tags_name_uq on tags (tenant_id, coalesce(owner_id, '00000000-0000-0000-0000-000000000000'::uuid), lower(name));
create index on tags (tenant_id, owner_id);

create table element_tags (
  tenant_id uuid not null,
  element_id uuid not null,
  tag_id uuid not null,
  primary key (tenant_id, element_id, tag_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, tag_id) references tags (tenant_id, id) on delete cascade
);
create index on element_tags (tenant_id, tag_id);

create table watchers (
  tenant_id uuid not null,
  element_id uuid not null,
  member_id uuid not null,
  muted boolean not null default false,
  created_at timestamptz not null default now(),
  primary key (tenant_id, element_id, member_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on watchers (tenant_id, member_id);

create table votes (
  tenant_id uuid not null,
  element_id uuid not null,
  member_id uuid not null,
  created_at timestamptz not null default now(),
  primary key (tenant_id, element_id, member_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on votes (tenant_id, member_id);

-- =====================================================================
-- 8. VISTE, DASHBOARD, NOTIFICHE
-- =====================================================================

-- Una vista = query + rappresentazione: lista, tabella, board, timeline, calendario, grafo, report
create table views (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  kind text not null check (kind in ('list','table','board','timeline','calendar','graph','report')),
  query text not null default '',
  settings jsonb not null default '{}',     -- colonne, swimlane, WIP, raggruppamenti, metriche
  space_id uuid,
  owner_id uuid,
  shared boolean not null default false,
  system_key text,                          -- 'assigned_to_me', 'reported_by_me'...
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, space_id) references spaces (tenant_id, id) on delete cascade,
  foreign key (tenant_id, owner_id) references members (tenant_id, id) on delete cascade
);
create index on views (tenant_id, owner_id);
create index on views (tenant_id, space_id);
create unique index views_system_uq on views (tenant_id, system_key) where system_key is not null;

create table dashboards (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  owner_id uuid,
  space_id uuid,
  shared boolean not null default false,
  layout jsonb not null default '[]',       -- widget: tipo, posizione, vista o metrica di origine
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, owner_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, space_id) references spaces (tenant_id, id) on delete cascade
);
create index on dashboards (tenant_id, owner_id);
create index on dashboards (tenant_id, space_id);

create table notifications (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  member_id uuid not null,
  element_id uuid,
  kind text not null,
  payload jsonb not null default '{}',
  read_at timestamptz,
  emailed_at timestamptz,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
);
create index on notifications (tenant_id, member_id, created_at desc);
create index on notifications (tenant_id, member_id) where read_at is null;
create index on notifications (tenant_id, element_id);

-- =====================================================================
-- 9. STORIA E AUDIT (partizionate per mese; l'app crea le partizioni future)
-- =====================================================================

create sequence activities_id_seq;
create table activities (
  tenant_id uuid not null,
  id bigint not null default nextval('activities_id_seq'),
  element_id uuid not null,
  actor_id uuid,
  kind text not null,                       -- 'created','field','state','comment','relation','attachment','visibility'...
  field_key text,
  added jsonb,
  removed jsonb,
  created_at timestamptz not null default now(),
  primary key (tenant_id, created_at, id)
) partition by range (created_at);
create index activities_element_idx on activities (tenant_id, element_id, id);
create index activities_actor_idx on activities (tenant_id, actor_id, created_at);
create table activities_default partition of activities default;

-- Audit a prova di manomissione: catena di hash per tenant (hash = sha256(prev_hash || evento canonico))
create table audit_log (
  tenant_id uuid,                           -- null = evento di piattaforma
  seq bigint not null,
  at timestamptz not null default now(),
  actor_account_id uuid,
  actor_member_id uuid,
  action text not null,
  entity_type text not null,
  entity_id uuid,
  payload jsonb not null default '{}',
  ip inet,
  prev_hash bytea,
  hash bytea not null,
  primary key (at, seq)
) partition by range (at);
create unique index audit_log_seq_uq on audit_log (tenant_id, seq, at);
create index audit_log_entity_idx on audit_log (tenant_id, entity_type, entity_id);
create table audit_log_default partition of audit_log default;

-- Outbox per webhook ed eventi verso integrazioni (consegna almeno una volta)
create table outbox_events (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id bigint generated always as identity,
  kind text not null,
  payload jsonb not null,
  created_at timestamptz not null default now(),
  dispatched_at timestamptz,
  primary key (tenant_id, id)
);
create index outbox_pending_idx on outbox_events (created_at) where dispatched_at is null;

create table webhooks (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  url text not null,
  secret_enc bytea not null,                -- firma HMAC dei payload
  events text[] not null,
  active boolean not null default true,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id)
);

-- =====================================================================
-- 10. ESTENSIONI DI DOMINIO (tabelle 1:1 o 1:n sull'elemento)
-- =====================================================================

-- ---------- Knowledge ----------
create table page_versions (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,                 -- elemento di tipo 'page'
  version integer not null,
  title text not null,
  body jsonb not null,
  author_id uuid,
  approved_by uuid,                         -- documenti controllati
  approved_at timestamptz,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, author_id) references members (tenant_id, id) on delete set null (author_id),
  foreign key (tenant_id, approved_by) references members (tenant_id, id) on delete set null (approved_by),
  unique (tenant_id, element_id, version)
);
create index on page_versions (tenant_id, author_id);
create index on page_versions (tenant_id, approved_by);

create table page_locks (
  tenant_id uuid not null,
  element_id uuid not null,
  member_id uuid not null,
  expires_at timestamptz not null,
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on page_locks (tenant_id, member_id);

-- ---------- Architettura enterprise ----------
create table ext_application (
  tenant_id uuid not null,
  element_id uuid not null,
  criticality text check (criticality in ('low','medium','high','critical')),
  supports_critical_function boolean not null default false,   -- DORA: funzioni essenziali o importanti
  data_classification text,
  lifecycle text check (lifecycle in ('plan','build','run','phase_out','retired')),
  time_rating text check (time_rating in ('tolerate','invest','migrate','eliminate')),
  rto_minutes integer check (rto_minutes >= 0),
  rpo_minutes integer check (rpo_minutes >= 0),
  hosting text,
  attrs jsonb not null default '{}',
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
);

create table ext_ict_provider (
  tenant_id uuid not null,
  element_id uuid not null,
  legal_name text not null,
  lei text,                                 -- Legal Entity Identifier
  country text,
  provider_type text,
  is_intragroup boolean not null default false,
  substitutability text,
  attrs jsonb not null default '{}',        -- campi del registro delle informazioni DORA (da allineare agli ITS)
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
);
create index on ext_ict_provider (tenant_id, lei);

create table ict_contracts (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  provider_element_id uuid not null,
  reference text not null,
  start_date date,
  end_date date,
  notice_days integer,
  annual_cost_cents bigint,
  currency char(3),
  exit_plan_element_id uuid,                -- pagina con la strategia di uscita
  attrs jsonb not null default '{}',
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, provider_element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, exit_plan_element_id) references elements (tenant_id, id) on delete set null (exit_plan_element_id)
);
create index on ict_contracts (tenant_id, provider_element_id);
create index on ict_contracts (tenant_id, exit_plan_element_id);
create index on ict_contracts (tenant_id, end_date);

create table diagrams (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  space_id uuid not null,
  name text not null,
  kind text not null check (kind in ('c4_context','c4_container','capability_map','dependency','archimate','free')),
  layout jsonb not null default '{}',       -- nodi = id di elementi, archi = id di relazioni
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, space_id) references spaces (tenant_id, id) on delete cascade
);
create index on diagrams (tenant_id, space_id);

-- ---------- GRC ----------
create table tenant_frameworks (
  tenant_id uuid not null references tenants(id) on delete cascade,
  framework_version_id uuid not null references framework_versions(id) on delete restrict,
  status text not null default 'active' check (status in ('active','paused','retired')),
  activated_at timestamptz not null default now(),
  primary key (tenant_id, framework_version_id)
);
create index on tenant_frameworks (framework_version_id);

create table risk_matrices (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  likelihood_levels jsonb not null,
  impact_levels jsonb not null,
  score_bands jsonb not null,
  primary key (tenant_id, id)
);

create table ext_risk (
  tenant_id uuid not null,
  element_id uuid not null,
  matrix_id uuid not null,
  inherent_likelihood smallint,
  inherent_impact smallint,
  residual_likelihood smallint,
  residual_impact smallint,
  treatment text check (treatment in ('mitigate','accept','transfer','avoid')),
  review_due date,
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, matrix_id) references risk_matrices (tenant_id, id) on delete restrict
);
create index on ext_risk (tenant_id, matrix_id);
create index on ext_risk (tenant_id, review_due);

-- Controllo interno (elemento 'control') -> requisiti normativi coperti
create table control_requirements (
  tenant_id uuid not null,
  control_element_id uuid not null,
  requirement_id uuid not null references requirements(id) on delete cascade,
  coverage text not null default 'full' check (coverage in ('full','partial')),
  note text,
  primary key (tenant_id, control_element_id, requirement_id),
  foreign key (tenant_id, control_element_id) references elements (tenant_id, id) on delete cascade
);
create index on control_requirements (requirement_id);

-- Applicabilità di un requisito a un perimetro (elemento target: applicazione, entità, processo; null = tutto il tenant)
create table applicability (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  requirement_id uuid not null references requirements(id) on delete cascade,
  target_element_id uuid,
  status text not null check (status in ('applicable','not_applicable','under_review')),
  justification text,
  rule_key text,                            -- regola che l'ha calcolata, se automatica
  decided_by uuid,
  decided_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, target_element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, decided_by) references members (tenant_id, id) on delete set null (decided_by)
);
create unique index applicability_uq on applicability (tenant_id, requirement_id, coalesce(target_element_id, '00000000-0000-0000-0000-000000000000'::uuid));
create index on applicability (requirement_id);
create index on applicability (tenant_id, target_element_id);
create index on applicability (tenant_id, decided_by);

create table ext_evidence (
  tenant_id uuid not null,
  element_id uuid not null,
  source text not null check (source in ('manual','automatic')),
  collector text,                           -- es. 'deploy-approval', 'access-review', 'scan'
  valid_from timestamptz not null default now(),
  valid_until timestamptz,
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
);
create index on ext_evidence (tenant_id, valid_until);

create table ext_incident (
  tenant_id uuid not null,
  element_id uuid not null,
  detected_at timestamptz not null,
  classified_at timestamptz,
  contained_at timestamptz,
  resolved_at timestamptz,
  is_major boolean not null default false,
  classification jsonb not null default '{}', -- criteri per normativa
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
);
create index on ext_incident (tenant_id, detected_at);

create table regulatory_notifications (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  incident_element_id uuid not null,
  rule_id uuid not null references notification_rules(id) on delete restrict,
  due_at timestamptz not null,
  status text not null default 'pending' check (status in ('pending','draft','submitted','not_required')),
  submitted_at timestamptz,
  submitted_by uuid,
  payload jsonb not null default '{}',
  primary key (tenant_id, id),
  foreign key (tenant_id, incident_element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, submitted_by) references members (tenant_id, id) on delete set null (submitted_by)
);
create index on regulatory_notifications (tenant_id, incident_element_id);
create index on regulatory_notifications (rule_id);
create index on regulatory_notifications (tenant_id, submitted_by);
create index regulatory_notifications_due_idx on regulatory_notifications (tenant_id, due_at) where status in ('pending','draft');

-- ---------- DevSecOps ----------
create table integrations (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  kind text not null check (kind in ('github','gitlab','bitbucket','azure_devops','jenkins','generic')),
  name text not null,
  config jsonb not null default '{}',
  secret_enc bytea,
  active boolean not null default true,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id)
);

create table repositories (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  integration_id uuid not null,
  external_id text not null,
  full_name text not null,
  application_element_id uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, integration_id) references integrations (tenant_id, id) on delete cascade,
  foreign key (tenant_id, application_element_id) references elements (tenant_id, id) on delete set null (application_element_id)
);
create unique index repositories_ext_uq on repositories (tenant_id, integration_id, external_id);
create index on repositories (tenant_id, application_element_id);

-- Consegne webhook ricevute: idempotenza (le piattaforme ritrasmettono)
create table inbound_deliveries (
  tenant_id uuid not null,
  integration_id uuid not null,
  delivery_id text not null,
  received_at timestamptz not null default now(),
  primary key (tenant_id, integration_id, delivery_id),
  foreign key (tenant_id, integration_id) references integrations (tenant_id, id) on delete cascade
);

create table environments (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  is_production boolean not null default false,
  primary key (tenant_id, id)
);
create unique index environments_name_uq on environments (tenant_id, lower(name));

create table deployments (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  application_element_id uuid not null,
  environment_id uuid not null,
  version text not null,
  status text not null check (status in ('running','succeeded','failed','rolled_back')),
  started_at timestamptz not null,
  finished_at timestamptz,
  caused_incident_element_id uuid,          -- per il tasso di fallimento delle modifiche
  change_element_ids uuid[] not null default '{}',
  external_ref text,
  primary key (tenant_id, id),
  foreign key (tenant_id, application_element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, environment_id) references environments (tenant_id, id) on delete restrict,
  foreign key (tenant_id, caused_incident_element_id) references elements (tenant_id, id) on delete set null (caused_incident_element_id)
);
create index deployments_app_idx on deployments (tenant_id, application_element_id, started_at desc);
create index on deployments (tenant_id, environment_id, started_at desc);
create index on deployments (tenant_id, caused_incident_element_id);

create table components (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  purl text not null,                       -- package URL (CycloneDX/SPDX)
  name text not null,
  version text,
  license text,
  primary key (tenant_id, id)
);
create unique index components_purl_uq on components (tenant_id, purl);

create table component_usages (
  tenant_id uuid not null,
  application_element_id uuid not null,
  component_id uuid not null,
  first_seen timestamptz not null default now(),
  last_seen timestamptz not null default now(),
  primary key (tenant_id, application_element_id, component_id),
  foreign key (tenant_id, application_element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, component_id) references components (tenant_id, id) on delete cascade
);
create index on component_usages (tenant_id, component_id);

-- Risultati grezzi degli scanner (SARIF, SCA); la vulnerabilità da gestire è un elemento
create table findings (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  fingerprint text not null,                -- deduplica tra scansioni
  source text not null,
  rule_ref text,
  cve text,
  severity text not null check (severity in ('info','low','medium','high','critical')),
  application_element_id uuid,
  component_id uuid,
  vulnerability_element_id uuid,
  status text not null default 'open' check (status in ('open','fixed','false_positive','accepted')),
  first_seen timestamptz not null default now(),
  last_seen timestamptz not null default now(),
  details jsonb not null default '{}',
  primary key (tenant_id, id),
  foreign key (tenant_id, application_element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, component_id) references components (tenant_id, id) on delete set null (component_id),
  foreign key (tenant_id, vulnerability_element_id) references elements (tenant_id, id) on delete set null (vulnerability_element_id)
);
create unique index findings_fp_uq on findings (tenant_id, fingerprint);
create index on findings (tenant_id, application_element_id, status);
create index on findings (tenant_id, component_id);
create index on findings (tenant_id, vulnerability_element_id);
create index on findings (tenant_id, cve) where cve is not null;

-- Serie temporali: metriche DORA DevOps, key result, KPI (partizionate per mese)
create table metric_points (
  tenant_id uuid not null,
  metric_key text not null,                 -- 'deploy_frequency','lead_time_minutes','change_failure_rate','time_to_restore_minutes','kr:<id>'
  subject_element_id uuid,                  -- applicazione, team, obiettivo...
  at timestamptz not null,
  value double precision not null,
  dims jsonb not null default '{}',
  primary key (tenant_id, metric_key, at, subject_element_id)
) partition by range (at);
create index metric_points_subject_idx on metric_points (tenant_id, subject_element_id, metric_key, at);
create table metric_points_default partition of metric_points default;

-- ---------- Demand e portafoglio (vedi demand.md) ----------
-- Gli item (idea, business_request, portfolio, program, project, work_package, deliverable,
-- milestone, change_request, benefit) sono elementi del motore unico; qui le estensioni.

create table org_units (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  parent_id uuid,
  kind text not null default 'business_unit' check (kind in ('legal_entity','business_unit','department','cost_center')),
  code text,                                -- codice centro di costo / contabile
  manager_id uuid,
  archived boolean not null default false,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, parent_id) references org_units (tenant_id, id) on delete set null (parent_id),
  foreign key (tenant_id, manager_id) references members (tenant_id, id) on delete set null (manager_id)
);
create unique index org_units_code_uq on org_units (tenant_id, code) where code is not null;
create index on org_units (tenant_id, parent_id);
create index on org_units (tenant_id, manager_id);

create table teams (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  org_unit_id uuid,
  lead_id uuid,
  delivery_space_id uuid,                   -- spazio dove nascono gli epic assegnati
  weekly_capacity_hours numeric,
  archived boolean not null default false,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, org_unit_id) references org_units (tenant_id, id) on delete set null (org_unit_id),
  foreign key (tenant_id, lead_id) references members (tenant_id, id) on delete set null (lead_id),
  foreign key (tenant_id, delivery_space_id) references spaces (tenant_id, id) on delete set null (delivery_space_id)
);
create unique index teams_name_uq on teams (tenant_id, lower(name));
create index on teams (tenant_id, org_unit_id);
create index on teams (tenant_id, lead_id);
create index on teams (tenant_id, delivery_space_id);

create table team_members (
  tenant_id uuid not null,
  team_id uuid not null,
  member_id uuid not null,
  role_key text,                            -- es. 'backend','frontend','analyst'
  allocation_pct smallint not null default 100 check (allocation_pct between 0 and 100),
  valid_from date,
  valid_to date,
  primary key (tenant_id, team_id, member_id),
  foreign key (tenant_id, team_id) references teams (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on team_members (tenant_id, member_id);

-- Ruoli sugli oggetti: sponsor, business owner, richiedente, demand manager, PMO, PM, architetto, finanza, compliance...
create table stakeholder_roles (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  key text not null check (key ~ '^[a-z][a-z0-9_]{0,62}$'),
  name text not null,
  permissions text[] not null default '{}', -- permessi concessi sull'oggetto a chi ha il ruolo
  raci jsonb not null default '{}',         -- {"<fase>": "R"|"A"|"C"|"I"}
  primary key (tenant_id, id)
);
create unique index stakeholder_roles_key_uq on stakeholder_roles (tenant_id, key);

create table stakeholders (
  tenant_id uuid not null,
  element_id uuid not null,
  role_id uuid not null,
  member_id uuid,
  group_id uuid,
  org_unit_id uuid,                         -- es. unità richiedente
  created_at timestamptz not null default now(),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, role_id) references stakeholder_roles (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, group_id) references groups (tenant_id, id) on delete cascade,
  foreign key (tenant_id, org_unit_id) references org_units (tenant_id, id) on delete cascade,
  constraint sh_holder check (num_nonnulls(member_id, group_id, org_unit_id) = 1)
);
create unique index stakeholders_uq on stakeholders (tenant_id, element_id, role_id, coalesce(member_id, group_id, org_unit_id));
create index on stakeholders (tenant_id, role_id);
create index on stakeholders (tenant_id, member_id);
create index on stakeholders (tenant_id, group_id);
create index on stakeholders (tenant_id, org_unit_id);

create table ext_demand (
  tenant_id uuid not null,
  element_id uuid not null,
  requesting_unit_id uuid,
  driver text check (driver in ('strategic','regulatory','operational','efficiency','risk')),
  is_mandatory boolean not null default false,  -- obbligo normativo: resta in testa alla graduatoria
  regulatory_deadline date,
  desired_date date,
  estimate_low_cents bigint check (estimate_low_cents >= 0),
  estimate_high_cents bigint,
  estimate_effort_days numeric,
  currency char(3) not null default 'EUR',
  score numeric,                            -- punteggio dell'ultima scorecard
  rank integer,                             -- posizione in graduatoria
  merged_into_element_id uuid,              -- demand accorpata
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, requesting_unit_id) references org_units (tenant_id, id) on delete set null (requesting_unit_id),
  foreign key (tenant_id, merged_into_element_id) references elements (tenant_id, id) on delete set null (merged_into_element_id),
  constraint estimate_range check (estimate_high_cents is null or estimate_low_cents is null or estimate_high_cents >= estimate_low_cents)
);
create index on ext_demand (tenant_id, requesting_unit_id);
create index on ext_demand (tenant_id, merged_into_element_id);
create index on ext_demand (tenant_id, rank) where rank is not null;

create table ext_milestone (
  tenant_id uuid not null,
  element_id uuid not null,
  baseline_date date,
  forecast_date date,
  actual_date date,
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
);

create table scorecards (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  method text not null default 'weighted' check (method in ('weighted','wsjf')),
  criteria jsonb not null,                  -- [{"key","name","weight","scale":[...]}]
  active boolean not null default true,
  primary key (tenant_id, id)
);

create table scorecard_results (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,
  scorecard_id uuid not null,
  values jsonb not null,
  total numeric not null,
  scored_by uuid,
  scored_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, scorecard_id) references scorecards (tenant_id, id) on delete restrict,
  foreign key (tenant_id, scored_by) references members (tenant_id, id) on delete set null (scored_by)
);
create index on scorecard_results (tenant_id, element_id, scored_at desc);
create index on scorecard_results (tenant_id, scorecard_id);
create index on scorecard_results (tenant_id, scored_by);

-- Governance
create table governance_bodies (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  kind text not null check (kind in ('investment_committee','steering','architecture_board','cab','other')),
  chair_id uuid,
  approval_limit_cents bigint,              -- competenza massima (null = illimitata)
  quorum smallint,
  archived boolean not null default false,
  primary key (tenant_id, id),
  foreign key (tenant_id, chair_id) references members (tenant_id, id) on delete set null (chair_id)
);
create index on governance_bodies (tenant_id, chair_id);

create table governance_body_members (
  tenant_id uuid not null,
  body_id uuid not null,
  member_id uuid not null,
  voting boolean not null default true,
  primary key (tenant_id, body_id, member_id),
  foreign key (tenant_id, body_id) references governance_bodies (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on governance_body_members (tenant_id, member_id);

create table meetings (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  body_id uuid not null,
  scheduled_at timestamptz not null,
  status text not null default 'planned' check (status in ('planned','held','cancelled')),
  minutes_element_id uuid,                  -- verbale (pagina Knowledge)
  primary key (tenant_id, id),
  foreign key (tenant_id, body_id) references governance_bodies (tenant_id, id) on delete cascade,
  foreign key (tenant_id, minutes_element_id) references elements (tenant_id, id) on delete set null (minutes_element_id)
);
create index on meetings (tenant_id, body_id, scheduled_at);
create index on meetings (tenant_id, minutes_element_id);

create table meeting_items (
  tenant_id uuid not null,
  meeting_id uuid not null,
  element_id uuid not null,
  ordinal integer not null default 0,
  outcome text check (outcome in ('approved','approved_with_conditions','rejected','deferred','informational')),
  note text,
  primary key (tenant_id, meeting_id, element_id),
  foreign key (tenant_id, meeting_id) references meetings (tenant_id, id) on delete cascade,
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
);
create index on meeting_items (tenant_id, element_id);

-- Approvazioni multi-livello con soglie
create table approval_policies (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  type_id uuid,                             -- tipo di elemento (null = tutti)
  stage text not null,                      -- fase del ciclo di vita a cui si applica
  -- regole: [{"when": {"amount_gte": 5000000, "driver": "regulatory"}, "steps": [{"mode":"sequential","approvers":[{"role":"sponsor"},{"body":"<id>"}]}]}]
  rules jsonb not null,
  active boolean not null default true,
  primary key (tenant_id, id),
  foreign key (tenant_id, type_id) references element_types (tenant_id, id) on delete cascade
);
create index on approval_policies (tenant_id, type_id, stage);

create table approval_requests (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,
  policy_id uuid,
  stage text not null,
  subject text not null default 'stage_gate' check (subject in ('stage_gate','budget','budget_transfer','change_request','assignment')),
  status text not null default 'pending' check (status in ('pending','approved','approved_with_conditions','rejected','deferred','cancelled')),
  amount_cents bigint,
  requested_by uuid,
  requested_at timestamptz not null default now(),
  closed_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, policy_id) references approval_policies (tenant_id, id) on delete set null (policy_id),
  foreign key (tenant_id, requested_by) references members (tenant_id, id) on delete set null (requested_by)
);
create index on approval_requests (tenant_id, element_id);
create index on approval_requests (tenant_id, policy_id);
create index on approval_requests (tenant_id, requested_by);
create index approval_requests_open_idx on approval_requests (tenant_id, status) where status = 'pending';

create table approval_steps (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  request_id uuid not null,
  ordinal smallint not null,
  parallel_group smallint not null default 0,  -- passi con lo stesso gruppo vanno in parallelo
  approver_member_id uuid,
  approver_body_id uuid,
  approver_role_key text,                   -- risolto sugli stakeholder dell'elemento
  decision text not null default 'pending' check (decision in ('pending','approved','approved_with_conditions','rejected','deferred','skipped')),
  conditions text,
  comment text,
  decided_by uuid,
  decided_at timestamptz,
  meeting_id uuid,                          -- decisione presa in seduta
  primary key (tenant_id, id),
  foreign key (tenant_id, request_id) references approval_requests (tenant_id, id) on delete cascade,
  foreign key (tenant_id, approver_member_id) references members (tenant_id, id) on delete set null (approver_member_id),
  foreign key (tenant_id, approver_body_id) references governance_bodies (tenant_id, id) on delete set null (approver_body_id),
  foreign key (tenant_id, decided_by) references members (tenant_id, id) on delete set null (decided_by),
  foreign key (tenant_id, meeting_id) references meetings (tenant_id, id) on delete set null (meeting_id),
  constraint step_approver check (num_nonnulls(approver_member_id, approver_body_id, approver_role_key) >= 1)
);
create index on approval_steps (tenant_id, request_id, ordinal);
create index approval_steps_inbox_idx on approval_steps (tenant_id, approver_member_id) where decision = 'pending';
create index on approval_steps (tenant_id, approver_body_id);
create index on approval_steps (tenant_id, decided_by);
create index on approval_steps (tenant_id, meeting_id);

create table delegations (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  from_member_id uuid not null,
  to_member_id uuid not null,
  scope text not null default 'approvals' check (scope in ('approvals','all')),
  valid_from timestamptz not null,
  valid_to timestamptz not null,
  primary key (tenant_id, id),
  foreign key (tenant_id, from_member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, to_member_id) references members (tenant_id, id) on delete cascade,
  constraint delegation_dates check (valid_to > valid_from),
  constraint no_self_delegation check (from_member_id <> to_member_id)
);
create index on delegations (tenant_id, from_member_id, valid_from, valid_to);
create index on delegations (tenant_id, to_member_id);

-- Assegnazione ai team di delivery
create table assignments (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,                 -- progetto / iniziativa / work package
  team_id uuid not null,
  status text not null default 'proposed' check (status in ('proposed','clarification','accepted','rejected','active','done','cancelled')),
  scope_note text,
  estimate_effort_days numeric,
  estimate_cost_cents bigint,
  delivery_element_id uuid,                 -- epic creato nello spazio del team
  proposed_by uuid,
  responded_by uuid,
  responded_at timestamptz,
  response_note text,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, team_id) references teams (tenant_id, id) on delete restrict,
  foreign key (tenant_id, delivery_element_id) references elements (tenant_id, id) on delete set null (delivery_element_id),
  foreign key (tenant_id, proposed_by) references members (tenant_id, id) on delete set null (proposed_by),
  foreign key (tenant_id, responded_by) references members (tenant_id, id) on delete set null (responded_by)
);
create unique index assignments_active_uq on assignments (tenant_id, element_id, team_id) where status not in ('rejected','cancelled');
create index on assignments (tenant_id, team_id, status);
create index on assignments (tenant_id, delivery_element_id);
create index on assignments (tenant_id, proposed_by);
create index on assignments (tenant_id, responded_by);

-- Budget
create table fiscal_years (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  start_date date not null,
  end_date date not null,
  base_currency char(3) not null default 'EUR',
  status text not null default 'open' check (status in ('planning','open','closed')),
  primary key (tenant_id, id),
  constraint fy_dates check (end_date > start_date)
);
create unique index fiscal_years_name_uq on fiscal_years (tenant_id, name);

create table cost_categories (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  parent_id uuid,
  nature text not null check (nature in ('capex','opex')),
  code text,
  primary key (tenant_id, id),
  foreign key (tenant_id, parent_id) references cost_categories (tenant_id, id) on delete set null (parent_id)
);
create index on cost_categories (tenant_id, parent_id);

create table funding_sources (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,                       -- es. budget ordinario, fondi PNRR, contributi
  code text,
  primary key (tenant_id, id)
);

create table exchange_rates (
  tenant_id uuid not null references tenants(id) on delete cascade,
  currency char(3) not null,
  valid_on date not null,
  rate_to_base numeric(18,8) not null check (rate_to_base > 0),
  primary key (tenant_id, currency, valid_on)
);

create table budgets (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,                 -- portafoglio, programma o progetto
  fiscal_year_id uuid not null,
  kind text not null check (kind in ('requested','approved','revised','forecast')),
  version integer not null default 1,
  status text not null default 'draft' check (status in ('draft','submitted','approved','superseded')),
  approval_request_id uuid,
  approved_by uuid,
  approved_at timestamptz,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, fiscal_year_id) references fiscal_years (tenant_id, id) on delete restrict,
  foreign key (tenant_id, approval_request_id) references approval_requests (tenant_id, id) on delete set null (approval_request_id),
  foreign key (tenant_id, approved_by) references members (tenant_id, id) on delete set null (approved_by),
  unique (tenant_id, element_id, fiscal_year_id, kind, version)
);
create index on budgets (tenant_id, fiscal_year_id);
create index on budgets (tenant_id, approval_request_id);
create index on budgets (tenant_id, approved_by);

create table budget_lines (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  budget_id uuid not null,
  cost_category_id uuid not null,
  org_unit_id uuid,                         -- centro di costo
  funding_source_id uuid,
  period date,                              -- primo giorno del mese; null = intero esercizio
  amount_cents bigint not null,
  currency char(3) not null default 'EUR',
  note text,
  primary key (tenant_id, id),
  foreign key (tenant_id, budget_id) references budgets (tenant_id, id) on delete cascade,
  foreign key (tenant_id, cost_category_id) references cost_categories (tenant_id, id) on delete restrict,
  foreign key (tenant_id, org_unit_id) references org_units (tenant_id, id) on delete restrict,
  foreign key (tenant_id, funding_source_id) references funding_sources (tenant_id, id) on delete restrict
);
create index on budget_lines (tenant_id, budget_id);
create index on budget_lines (tenant_id, cost_category_id);
create index on budget_lines (tenant_id, org_unit_id);
create index on budget_lines (tenant_id, funding_source_id);

create table budget_transfers (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  from_line_id uuid not null,
  to_line_id uuid not null,
  amount_cents bigint not null check (amount_cents > 0),
  reason text not null,
  status text not null default 'pending' check (status in ('pending','approved','rejected')),
  approval_request_id uuid,
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, from_line_id) references budget_lines (tenant_id, id) on delete restrict,
  foreign key (tenant_id, to_line_id) references budget_lines (tenant_id, id) on delete restrict,
  foreign key (tenant_id, approval_request_id) references approval_requests (tenant_id, id) on delete set null (approval_request_id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by),
  constraint no_self_transfer check (from_line_id <> to_line_id)
);
create index on budget_transfers (tenant_id, from_line_id);
create index on budget_transfers (tenant_id, to_line_id);
create index on budget_transfers (tenant_id, approval_request_id);
create index on budget_transfers (tenant_id, created_by);

-- Import da CSV/Excel (fatture, ordini, budget, ore, cambi). L'ERP arriverà come connettore che scrive negli stessi run.
create table import_profiles (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null,
  target text not null check (target in ('cost_entries','budget_lines','work_logs','exchange_rates','rates')),
  mapping jsonb not null,                   -- colonna del file -> campo, trasformazioni, formati di data e numero
  options jsonb not null default '{}',      -- separatore, codifica, riga d'intestazione
  created_at timestamptz not null default now(),
  primary key (tenant_id, id)
);
create unique index import_profiles_name_uq on import_profiles (tenant_id, lower(name));

create table import_runs (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  profile_id uuid,
  target text not null,
  file_name text not null,
  file_sha256 bytea not null,
  status text not null default 'pending' check (status in ('pending','validating','preview','running','done','failed','rolled_back')),
  rows_total integer,
  rows_ok integer,
  rows_error integer,
  errors jsonb not null default '[]',       -- [{"row":12,"field":"importo","message":"..."}]
  dry_run boolean not null default true,    -- prima si valida e si mostra l'anteprima
  created_by uuid,
  created_at timestamptz not null default now(),
  finished_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, profile_id) references import_profiles (tenant_id, id) on delete set null (profile_id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on import_runs (tenant_id, created_at desc);
create index on import_runs (tenant_id, profile_id);
create index on import_runs (tenant_id, created_by);
create index on import_runs (tenant_id, file_sha256);

-- Costi
create table rates (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  member_id uuid,                           -- tariffa personale
  role_key text,                            -- tariffa per ruolo
  provider_element_id uuid,                 -- tariffa contrattuale di un fornitore
  hourly_cents bigint not null check (hourly_cents >= 0),
  currency char(3) not null default 'EUR',
  valid_from date not null,
  valid_to date,
  primary key (tenant_id, id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, provider_element_id) references elements (tenant_id, id) on delete cascade,
  constraint rate_subject check (num_nonnulls(member_id, role_key, provider_element_id) >= 1),
  constraint rate_dates check (valid_to is null or valid_to >= valid_from)
);
create index on rates (tenant_id, member_id, valid_from);
create index on rates (tenant_id, role_key, valid_from);
create index on rates (tenant_id, provider_element_id);

-- Ore registrate (usate anche dal Work management e dai timesheet)
create table work_logs (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,
  member_id uuid not null,
  work_date date not null,
  minutes integer not null check (minutes > 0),
  role_key text,                            -- ruolo con cui si è lavorato (fotografato), per la tariffa di ruolo
  activity_type text,
  capitalizable boolean not null default false,
  text text,
  created_by uuid,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete restrict,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on work_logs (tenant_id, element_id, work_date);
create index on work_logs (tenant_id, member_id, work_date);
create index on work_logs (tenant_id, created_by);

create table cost_entries (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,                 -- progetto o work package (si somma verso l'alto)
  cost_category_id uuid not null,
  org_unit_id uuid,
  source text not null check (source in ('work_log','invoice','purchase_order','recurring','manual','import')),
  source_ref text,                          -- es. id del work_log, numero fattura
  work_log_id uuid,
  entry_date date not null,
  kind text not null default 'actual' check (kind in ('committed','actual')),
  amount_cents bigint not null,
  currency char(3) not null default 'EUR',
  amount_base_cents bigint not null,        -- convertito nella valuta base al cambio del giorno
  capitalizable boolean not null default false,
  provider_element_id uuid,
  rate_id uuid,                             -- tariffa applicata (persona > ruolo > fornitore)
  import_run_id uuid,
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete restrict,
  foreign key (tenant_id, rate_id) references rates (tenant_id, id) on delete restrict,
  foreign key (tenant_id, import_run_id) references import_runs (tenant_id, id) on delete set null (import_run_id),
  foreign key (tenant_id, cost_category_id) references cost_categories (tenant_id, id) on delete restrict,
  foreign key (tenant_id, org_unit_id) references org_units (tenant_id, id) on delete restrict,
  foreign key (tenant_id, work_log_id) references work_logs (tenant_id, id) on delete cascade,
  foreign key (tenant_id, provider_element_id) references elements (tenant_id, id) on delete set null (provider_element_id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create unique index cost_entries_worklog_uq on cost_entries (tenant_id, work_log_id) where work_log_id is not null;
create index cost_entries_element_idx on cost_entries (tenant_id, element_id, entry_date);
create index on cost_entries (tenant_id, cost_category_id, entry_date);
create index on cost_entries (tenant_id, org_unit_id, entry_date);
create index on cost_entries (tenant_id, provider_element_id);
create index on cost_entries (tenant_id, created_by);
create index on cost_entries (tenant_id, rate_id);
create index on cost_entries (tenant_id, import_run_id);
-- reimportare lo stesso file non duplica le voci
create unique index cost_entries_source_uq on cost_entries (tenant_id, source, source_ref) where source_ref is not null and source <> 'work_log';

-- Aggregati dei costi per elemento, mese e categoria: è ciò che vede chi non è finanza o PMO.
-- Aggiornati dal worker a ogni nuova voce; mai a livello di persona.
create table cost_rollups (
  tenant_id uuid not null,
  element_id uuid not null,
  period date not null,                     -- primo giorno del mese
  cost_category_id uuid not null,
  kind text not null check (kind in ('committed','actual')),
  amount_base_cents bigint not null default 0,
  capitalizable_base_cents bigint not null default 0,
  updated_at timestamptz not null default now(),
  primary key (tenant_id, element_id, period, cost_category_id, kind),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, cost_category_id) references cost_categories (tenant_id, id) on delete restrict
);
create index on cost_rollups (tenant_id, cost_category_id, period);

-- Earned value: opzionale, attivato per singolo progetto
create table ext_project (
  tenant_id uuid not null,
  element_id uuid not null,
  ev_enabled boolean not null default false,
  ev_method text check (ev_method in ('percent_complete','milestones','work_packages')),
  baseline_budget_id uuid,                  -- budget approvato che fa da PV (BAC = suo totale)
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, baseline_budget_id) references budgets (tenant_id, id) on delete set null (baseline_budget_id),
  constraint ev_method_when_enabled check (not ev_enabled or ev_method is not null)
);
create index on ext_project (tenant_id, baseline_budget_id);

create table ev_snapshots (
  tenant_id uuid not null,
  element_id uuid not null,
  period_end date not null,
  bac_cents bigint not null,
  pv_cents bigint not null,
  ev_cents bigint not null,
  ac_cents bigint not null,
  percent_complete numeric(5,2) not null check (percent_complete between 0 and 100),
  cpi numeric generated always as (case when ac_cents > 0 then ev_cents::numeric / ac_cents end) stored,
  spi numeric generated always as (case when pv_cents > 0 then ev_cents::numeric / pv_cents end) stored,
  computed_at timestamptz not null default now(),
  primary key (tenant_id, element_id, period_end),
  foreign key (tenant_id, element_id) references ext_project (tenant_id, element_id) on delete cascade
);

-- Capacità: si chiede per ruolo in valutazione, si assegna a persone in pianificazione
create table resource_requests (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,                 -- demand, progetto o work package
  team_id uuid,                             -- null finché non si sceglie il team
  assignment_id uuid,
  role_key text not null,                   -- es. 'backend', 'analyst'
  period_start date not null,
  period_end date not null,
  hours numeric not null check (hours > 0),
  status text not null default 'draft' check (status in ('draft','requested','approved','fulfilled','cancelled')),
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, team_id) references teams (tenant_id, id) on delete set null (team_id),
  foreign key (tenant_id, assignment_id) references assignments (tenant_id, id) on delete set null (assignment_id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by),
  constraint rr_period check (period_end >= period_start)
);
create index on resource_requests (tenant_id, element_id);
create index on resource_requests (tenant_id, team_id, role_key, period_start);
create index on resource_requests (tenant_id, assignment_id);
create index on resource_requests (tenant_id, created_by);

create table resource_allocations (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  request_id uuid,                          -- la richiesta per ruolo che soddisfa (se c'è)
  element_id uuid not null,
  member_id uuid not null,
  period_start date not null,
  period_end date not null,
  hours numeric not null check (hours > 0),
  status text not null default 'planned' check (status in ('tentative','planned','confirmed','released')),
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, request_id) references resource_requests (tenant_id, id) on delete set null (request_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  constraint ra_period check (period_end >= period_start)
);
create index on resource_allocations (tenant_id, member_id, period_start);
create index on resource_allocations (tenant_id, element_id);
create index on resource_allocations (tenant_id, request_id);

-- Avanzamento
create table status_reports (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,
  period_end date not null,
  overall text not null check (overall in ('green','amber','red')),
  schedule text check (schedule in ('green','amber','red')),
  cost text check (cost in ('green','amber','red')),
  scope text check (scope in ('green','amber','red')),
  risk text check (risk in ('green','amber','red')),
  summary jsonb not null default '{}',      -- avanzamento, prossimi passi, decisioni richieste
  author_id uuid,
  submitted_at timestamptz,
  acknowledged_by uuid,
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, author_id) references members (tenant_id, id) on delete set null (author_id),
  foreign key (tenant_id, acknowledged_by) references members (tenant_id, id) on delete set null (acknowledged_by),
  unique (tenant_id, element_id, period_end)
);
create index on status_reports (tenant_id, author_id);
create index on status_reports (tenant_id, acknowledged_by);

-- =====================================================================
-- 11. AI (opzionale, per tenant)
-- =====================================================================

create table ai_settings (
  tenant_id uuid primary key references tenants(id) on delete cascade,
  enabled boolean not null default false,
  provider text check (provider in ('anthropic','bedrock','vertex','local')),
  model text,                               -- default 'claude-opus-5-5' per i provider Claude
  region text,
  features text[] not null default '{}',
  mask_personal_data boolean not null default true,
  monthly_token_budget bigint,
  secret_enc bytea,
  updated_at timestamptz not null default now()
);

create table ai_calls (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  member_id uuid,
  feature text not null,
  provider text not null,
  model text not null,
  input_tokens integer,
  output_tokens integer,
  element_ids uuid[] not null default '{}',
  prompt_sha256 bytea,                      -- di default si registra l'hash, non il contenuto
  outcome text check (outcome in ('ok','refused','error','rejected_by_user','accepted_by_user')),
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete set null (member_id)
);
create index on ai_calls (tenant_id, created_at);
create index on ai_calls (tenant_id, member_id);

-- Vettori per la ricerca semantica: creati solo se pgvector è disponibile (incluso nel pacchetto)
do $$
begin
  if exists (select 1 from pg_available_extensions where name = 'vector') then
    create extension if not exists vector;
    execute $sql$
      create table embeddings (
        tenant_id uuid not null,
        element_id uuid not null,
        chunk integer not null default 0,
        model text not null,
        embedding vector(1024) not null,     -- dimensione legata al modello scelto
        content_sha256 bytea not null,
        created_at timestamptz not null default now(),
        primary key (tenant_id, element_id, chunk, model),
        foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
      )$sql$;
    execute 'create index embeddings_hnsw on embeddings using hnsw (embedding vector_cosine_ops)';
  end if;
end $$;

-- =====================================================================
-- 12. ROW LEVEL SECURITY
-- Seconda barriera dopo i controlli applicativi. L'app si connette con un ruolo
-- senza BYPASSRLS; i job di piattaforma usano un ruolo dedicato.
-- =====================================================================

do $$
declare t record;
begin
  for t in
    select c.table_name
    from information_schema.columns c
    join information_schema.tables tb on tb.table_name = c.table_name and tb.table_schema = c.table_schema
    where c.table_schema = 'public' and c.column_name = 'tenant_id'
      and tb.table_type = 'BASE TABLE'
      and c.table_name not in ('jobs','audit_log')              -- gestite dal ruolo di piattaforma
      and c.table_name not like '%\_default' escape '\'         -- le partizioni ereditano dal padre
  loop
    execute format('alter table %I enable row level security', t.table_name);
    execute format('alter table %I force row level security', t.table_name);
    execute format(
      'create policy tenant_isolation on %I using (tenant_id = (select app_tenant())) with check (tenant_id = (select app_tenant()))',
      t.table_name);
  end loop;
end $$;

-- Libreria normativa: i cataloghi di sistema (owner null) sono leggibili da tutti; quelli privati solo dal proprio tenant
alter table frameworks enable row level security;
create policy frameworks_read on frameworks for select using (owner_tenant_id is null or owner_tenant_id = (select app_tenant()));
create policy frameworks_write on frameworks for all using (owner_tenant_id = (select app_tenant())) with check (owner_tenant_id = (select app_tenant()));
alter table requirement_mappings enable row level security;
create policy mappings_read on requirement_mappings for select using (owner_tenant_id is null or owner_tenant_id = (select app_tenant()));
create policy mappings_write on requirement_mappings for all using (owner_tenant_id = (select app_tenant())) with check (owner_tenant_id = (select app_tenant()));
-- Versioni, requisiti e regole seguono la visibilità del framework a cui appartengono
create or replace function framework_visible(fw uuid) returns boolean language sql stable as $$
  select exists (select 1 from frameworks f where f.id = fw)   -- la policy di frameworks filtra già per tenant
$$;
alter table framework_versions enable row level security;
create policy fv_read on framework_versions for select using (framework_visible(framework_id));
alter table requirements enable row level security;
create policy req_read on requirements for select using (
  exists (select 1 from framework_versions v where v.id = version_id));
alter table notification_rules enable row level security;
create policy nr_read on notification_rules for select using (
  exists (select 1 from framework_versions v where v.id = version_id));

-- Riservatezza dei costi del personale (decisione 2026-11-24): tariffe e voci di costo da ore
-- sono visibili solo a chi ha app.finance = 'on' (finanza, PMO, worker). Policy RESTRICTIVE:
-- si sommano all'isolamento per tenant, non lo sostituiscono. Gli altri usano cost_rollups.
create or replace function app_finance() returns boolean language sql stable as $$
  select coalesce(current_setting('app.finance', true), '') = 'on' $$;
create policy finance_only on rates as restrictive using ((select app_finance())) with check ((select app_finance()));
create policy finance_only on cost_entries as restrictive using ((select app_finance())) with check ((select app_finance()));
