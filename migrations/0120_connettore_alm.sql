-- Connettore OpenText ALM/Quality Center in sola lettura (DevSecOps) e tabelle generiche dei
-- test: test set, test pianificati nel set, esecuzioni e difetti.
--
-- Le tabelle dei test sono generiche: origin dice da dove viene la riga.
--   alm     il connettore in lettura (oggi);
--   native  il test management di Regia (fase successiva);
--   import  lo storico migrato da ALM (fase successiva).
-- Per le righe esterne source_key è la chiave stabile nella fonte (per ALM
-- «<integrazione>/<dominio>/<progetto>/<id>»): rileggere aggiorna, non duplica.
--
-- Il collegamento al change è per numero (change_number, in maiuscolo), fatto alla lettura con
-- la tabella changes (numero o alias), come per ServiceNow: resta giusto anche quando il change
-- di Regia nasce dopo. Gli aggregati per test set e per change si calcolano da test_set_items
-- (stato attuale di ogni test pianificato) e da test_defects.
--
-- Riusa le tabelle generiche dei connettori (0025): integrations (kind «alm»),
-- connector_repositories (un'unità ALM per riga: progetto, cartella dei test set o valore di un
-- campo, con proposta e conferma dell'applicazione), connector_sync_state (cursori su
-- last-modified per progetto ed entità) e connector_sync_runs.
--
-- Dati personali: il nome di chi ha eseguito un test (tester) si conserva solo se chi configura
-- l'integrazione lo chiede; di default resta vuoto.

alter table integrations drop constraint integrations_kind_check;
alter table integrations add constraint integrations_kind_check
  check (kind in ('github','gitlab','gitea','servicenow','alm','bitbucket','azure_devops','jenkins','generic'));

-- =====================================================================
-- 1. Test set
-- =====================================================================

create table test_sets (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  origin text not null check (origin in ('alm', 'native', 'import')),
  integration_id uuid,                        -- l'integrazione che l'ha letto (origine alm)
  source_key text not null check (length(source_key) between 1 and 300),
  external_id text not null default '' check (length(external_id) <= 64),
  project text not null default '' check (length(project) <= 200),   -- dominio/progetto
  name text not null check (length(name) between 1 and 300),
  folder text not null default '' check (length(folder) <= 500),
  status text not null default '' check (length(status) <= 40),
  cycle_key text not null default '' check (length(cycle_key) <= 300),
  cycle text not null default '' check (length(cycle) <= 200),
  release_key text not null default '' check (length(release_key) <= 300),
  release text not null default '' check (length(release) <= 200),
  change_number text not null default '' check (length(change_number) <= 120),
  repository_id uuid,                         -- l'unità (connector_repositories)
  application_element_id uuid,                -- l'applicazione, dall'unità confermata
  url text not null default '' check (length(url) <= 1000),
  source_updated_at timestamptz,
  first_seen_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, integration_id) references integrations (tenant_id, id) on delete cascade,
  foreign key (tenant_id, repository_id) references connector_repositories (tenant_id, id) on delete set null (repository_id),
  foreign key (tenant_id, application_element_id) references elements (tenant_id, id) on delete set null (application_element_id)
);
create unique index test_sets_source_uq on test_sets (tenant_id, origin, source_key);
create index on test_sets (tenant_id, change_number) where change_number <> '';
create index on test_sets (tenant_id, integration_id, cycle_key);
create index on test_sets (tenant_id, integration_id, release_key);
create index on test_sets (tenant_id, repository_id);
create index on test_sets (tenant_id, application_element_id);
select enable_tenant_rls('test_sets');

-- =====================================================================
-- 2. Test pianificati nel set (istanze di test di ALM)
-- =====================================================================

-- status è lo stato attuale del test nel set (in ALM: lo stato dell'ultima esecuzione):
--   passed, failed, blocked, not_completed, no_run (non eseguito), na (non applicabile).
create table test_set_items (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  test_set_id uuid not null,
  source_key text not null check (length(source_key) between 1 and 300),
  test_key text not null default '' check (length(test_key) <= 300),
  test_name text not null default '' check (length(test_name) <= 300),
  status text not null default 'no_run' check (status in ('passed', 'failed', 'blocked', 'not_completed', 'no_run', 'na')),
  raw_status text not null default '' check (length(raw_status) <= 60),
  last_run_at timestamptz,
  source_updated_at timestamptz,
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, test_set_id) references test_sets (tenant_id, id) on delete cascade
);
create unique index test_set_items_source_uq on test_set_items (tenant_id, test_set_id, source_key);
create index on test_set_items (tenant_id, source_key);
select enable_tenant_rls('test_set_items');

-- =====================================================================
-- 3. Esecuzioni
-- =====================================================================

create table test_runs (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  origin text not null check (origin in ('alm', 'native', 'import')),
  integration_id uuid,
  source_key text not null check (length(source_key) between 1 and 300),
  test_set_id uuid,
  item_id uuid,
  test_key text not null default '' check (length(test_key) <= 300),
  name text not null default '' check (length(name) <= 300),
  status text not null check (status in ('passed', 'failed', 'blocked', 'not_completed', 'no_run', 'na')),
  raw_status text not null default '' check (length(raw_status) <= 60),
  executed_at timestamptz,
  duration_seconds integer check (duration_seconds is null or duration_seconds >= 0),
  environment text not null default '' check (length(environment) <= 120),
  tester text check (tester is null or length(tester) <= 120),   -- solo se configurato
  url text not null default '' check (length(url) <= 1000),
  source_updated_at timestamptz,
  first_seen_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, integration_id) references integrations (tenant_id, id) on delete cascade,
  foreign key (tenant_id, test_set_id) references test_sets (tenant_id, id) on delete set null (test_set_id),
  foreign key (tenant_id, item_id) references test_set_items (tenant_id, id) on delete set null (item_id)
);
create unique index test_runs_source_uq on test_runs (tenant_id, origin, source_key);
create index on test_runs (tenant_id, test_set_id, executed_at desc);
create index on test_runs (tenant_id, item_id);
create index on test_runs (tenant_id, integration_id);
select enable_tenant_rls('test_runs');

-- =====================================================================
-- 4. Difetti
-- =====================================================================

-- severity è normalizzata (critical, high, medium, low, '' se sconosciuta); raw_severity è il
-- valore della fonte. open dipende dagli stati chiusi configurati nell'integrazione.
-- test_set_ids: i test set collegati (dai link del difetto a esecuzioni, istanze o set);
-- change_numbers: i change dei test set collegati o del ciclo/release di rilevazione.
create table test_defects (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  origin text not null check (origin in ('alm', 'native', 'import')),
  integration_id uuid,
  source_key text not null check (length(source_key) between 1 and 300),
  external_id text not null default '' check (length(external_id) <= 64),
  project text not null default '' check (length(project) <= 200),
  summary text not null default '' check (length(summary) <= 300),
  status text not null default '' check (length(status) <= 60),
  open boolean not null default true,
  severity text not null default '' check (severity in ('critical', 'high', 'medium', 'low', '')),
  raw_severity text not null default '' check (length(raw_severity) <= 60),
  detected_cycle_key text not null default '' check (length(detected_cycle_key) <= 300),
  detected_release_key text not null default '' check (length(detected_release_key) <= 300),
  test_set_ids uuid[] not null default '{}',
  change_numbers text[] not null default '{}',
  url text not null default '' check (length(url) <= 1000),
  source_updated_at timestamptz,
  first_seen_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, integration_id) references integrations (tenant_id, id) on delete cascade
);
create unique index test_defects_source_uq on test_defects (tenant_id, origin, source_key);
create index test_defects_changes_idx on test_defects using gin (change_numbers);
create index on test_defects (tenant_id, integration_id);
select enable_tenant_rls('test_defects');
