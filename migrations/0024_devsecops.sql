-- DevSecOps, fase 1 (devsecops.md §3.1 e §4; piano, passo 21).
--
-- myTrack non esegue pipeline: riceve dal contratto neutro (internal/modules/devsecops/contract)
-- servizi, esiti dei controlli, rilasci, deploy, SBOM e report delle vulnerabilità, e li collega
-- alle applicazioni del catalogo, alle richieste (numero di change) e ai requisiti.
-- Riusa le tabelle della migrazione 0001 (environments, deployments, components,
-- component_usages, findings) e aggiunge servizi, change, controlli, rilasci, SBOM e report.

-- ---------------------------------------------------------------------
-- Impostazioni del tenant: nomi degli ambienti di produzione, giorni oltre i quali un
-- servizio è "non rilasciato", campo della richiesta con il numero di change, fine supporto
-- dei runtime riconosciuti nelle SBOM.
-- ---------------------------------------------------------------------
create table devsecops_settings (
  tenant_id uuid not null references tenants(id) on delete cascade,
  production_envs text[] not null default '{prod,production}',
  stale_days integer not null default 30 check (stale_days between 1 and 3650),
  change_field text,
  -- [{"name": "node", "version": "18", "end_of_support": "2025-04-30"}, ...]
  runtimes jsonb not null default '[]',
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on devsecops_settings (tenant_id, updated_by);
select enable_tenant_rls('devsecops_settings');

-- ---------------------------------------------------------------------
-- Servizio: un repository o un componente rilasciabile. Un'applicazione può averne più di
-- uno. L'associazione all'applicazione arriva dalla pipeline (codice o chiave dichiarati) o
-- dall'interfaccia; quella dall'interfaccia prevale. first_seen_at nullo = servizio censito
-- ma non ancora sulla pipeline (la migrazione ha così un numero).
-- ---------------------------------------------------------------------
create table services (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null check (length(name) between 1 and 200),
  repository text,
  provider text check (provider in ('github','gitlab','azure_devops','bitbucket','other')),
  url text,
  repository_id uuid,
  application_element_id uuid,
  declared_application text,
  mapping_source text check (mapping_source in ('pipeline','manual')),
  first_seen_at timestamptz,
  last_seen_at timestamptz,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, repository_id) references repositories (tenant_id, id) on delete set null (repository_id),
  foreign key (tenant_id, application_element_id) references elements (tenant_id, id) on delete set null (application_element_id)
);
create unique index services_name_uq on services (tenant_id, lower(name));
create index on services (tenant_id, application_element_id);
create index on services (tenant_id, repository_id);
select enable_tenant_rls('services');

-- Ambienti: ordine di visualizzazione (sviluppo → produzione) e data di creazione.
-- Si creano al primo utilizzo; is_production dalle impostazioni, modificabile.
alter table environments add column ordinal integer not null default 100;
alter table environments add column created_at timestamptz not null default now();

-- ---------------------------------------------------------------------
-- Change: il numero del rilascio (ServiceNow, Jira, la chiave di una richiesta...). Si
-- collega alla richiesta di myTrack per chiave (DEM-12) o per un campo configurato. Un
-- numero provvisorio sostituito da quello definitivo resta tra gli alias.
-- ---------------------------------------------------------------------
create table changes (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  number text not null check (length(number) between 1 and 120),
  aliases text[] not null default '{}',
  title text not null default '',
  request_element_id uuid,
  request_source text check (request_source in ('key','field','manual')),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, request_element_id) references elements (tenant_id, id) on delete set null (request_element_id)
);
create unique index changes_number_uq on changes (tenant_id, number);
create index changes_aliases_idx on changes using gin (aliases);
create index on changes (tenant_id, request_element_id);
select enable_tenant_rls('changes');

-- ---------------------------------------------------------------------
-- Esiti dei controlli di un commit (il "verdict" della pipeline).
-- ---------------------------------------------------------------------
create table check_runs (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  service_id uuid not null,
  commit text not null,
  ref text,
  run_id text not null default '',
  url text,
  verdict text not null check (verdict in ('passed','failed')),
  checks jsonb not null default '[]',
  at timestamptz not null,
  received_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, service_id) references services (tenant_id, id) on delete cascade
);
create unique index check_runs_uq on check_runs (tenant_id, service_id, commit, run_id);
create index on check_runs (tenant_id, service_id, at desc);
select enable_tenant_rls('check_runs');

-- ---------------------------------------------------------------------
-- Registro di rilascio normalizzato: un rilascio per servizio, ambiente e versione (lo stesso
-- rilascio ricevuto due volte aggiorna la riga). CVE al rilascio e controlli del commit
-- rilasciato come li dichiara la pipeline (o dall'ultimo esito dei controlli del commit).
-- ---------------------------------------------------------------------
create table releases (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  service_id uuid not null,
  environment_id uuid not null,
  version text not null check (length(version) between 1 and 200),
  commit text,
  image text,
  digest text,
  change_id uuid,
  change_number text,
  requested_by text,
  approved_by text,
  approved_at timestamptz,
  pull_request text,
  release_url text,
  released_at timestamptz not null,
  cves_at_release jsonb,
  checks jsonb not null default '[]',
  checks_source text check (checks_source in ('release','check_run')),
  payload_sha256 text not null,
  received_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, service_id) references services (tenant_id, id) on delete cascade,
  foreign key (tenant_id, environment_id) references environments (tenant_id, id) on delete restrict,
  foreign key (tenant_id, change_id) references changes (tenant_id, id) on delete set null (change_id)
);
create unique index releases_uq on releases (tenant_id, service_id, environment_id, lower(version));
create index on releases (tenant_id, service_id, released_at desc);
create index on releases (tenant_id, environment_id);
create index on releases (tenant_id, change_id);
create index on releases (tenant_id, released_at desc);
select enable_tenant_rls('releases');

-- ---------------------------------------------------------------------
-- Deploy: la tabella della migrazione 0001 si estende con il servizio. L'applicazione diventa
-- facoltativa (un servizio può non essere ancora associato) e segue l'associazione del servizio.
-- Ogni rilascio crea anche il suo deploy (source = 'release', external_ref = 'release:<id>').
-- ---------------------------------------------------------------------
alter table deployments alter column application_element_id drop not null;
alter table deployments
  add column service_id uuid,
  add column digest text,
  add column source text check (source in ('gitops','pipeline','manual','release')),
  add column release_id uuid,
  add column received_at timestamptz not null default now();
alter table deployments
  add constraint deployments_service_fk foreign key (tenant_id, service_id) references services (tenant_id, id) on delete cascade,
  add constraint deployments_release_fk foreign key (tenant_id, release_id) references releases (tenant_id, id) on delete set null (release_id);
create index deployments_service_env_idx on deployments (tenant_id, service_id, environment_id, started_at desc);
create unique index deployments_ref_uq on deployments (tenant_id, service_id, external_ref)
  where service_id is not null and external_ref is not null;
create unique index deployments_natural_uq on deployments (tenant_id, service_id, environment_id, version, started_at)
  where service_id is not null and external_ref is null;
create index on deployments (tenant_id, release_id);

-- ---------------------------------------------------------------------
-- SBOM: una per servizio, versione, digest e formato; i componenti vanno in components
-- (per purl) e sbom_components; component_usages resta l'uso per applicazione.
-- ---------------------------------------------------------------------
create table sboms (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  service_id uuid not null,
  version text not null,
  digest text not null default '',
  format text not null check (format in ('cyclonedx','spdx')),
  spec_version text,
  document_sha256 text not null,
  components integer not null default 0,
  received_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, service_id) references services (tenant_id, id) on delete cascade
);
create unique index sboms_uq on sboms (tenant_id, service_id, lower(version), digest, format);
create index on sboms (tenant_id, service_id, received_at desc);
select enable_tenant_rls('sboms');

create table sbom_components (
  tenant_id uuid not null,
  sbom_id uuid not null,
  component_id uuid not null,
  primary key (tenant_id, sbom_id, component_id),
  foreign key (tenant_id, sbom_id) references sboms (tenant_id, id) on delete cascade,
  foreign key (tenant_id, component_id) references components (tenant_id, id) on delete cascade
);
create index on sbom_components (tenant_id, component_id);
select enable_tenant_rls('sbom_components');

-- ---------------------------------------------------------------------
-- Vulnerabilità: findings (0001) deduplicati per servizio, vulnerabilità e componente
-- (fingerprint); ogni report di scansione elenca i finding che ha trovato. "CVE di oggi" =
-- l'ultimo report per scanner della versione in produzione; "al rilascio" = quanto dichiara
-- il rilascio, altrimenti il primo report di quella versione.
-- ---------------------------------------------------------------------
alter table findings
  add column service_id uuid,
  add column package text,
  add column installed_version text,
  add column fixed_in text,
  add column layer text check (layer in ('image','app')),
  add column aliases text[] not null default '{}',
  add column url text;
alter table findings
  add constraint findings_service_fk foreign key (tenant_id, service_id) references services (tenant_id, id) on delete cascade;
create index on findings (tenant_id, service_id, status);

create table vuln_reports (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  service_id uuid not null,
  version text not null,
  digest text not null default '',
  scanner text not null,
  scanned_at timestamptz not null,
  -- {"critical", "high", "medium", "low", "unknown", "fixable_critical", "fixable_high",
  --  "image": {"critical", "high"}, "app": {"critical", "high"}}
  counts jsonb not null,
  total integer not null default 0,
  received_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, service_id) references services (tenant_id, id) on delete cascade
);
create unique index vuln_reports_uq on vuln_reports (tenant_id, service_id, lower(version), digest, scanner, scanned_at);
create index on vuln_reports (tenant_id, service_id, lower(version), scanned_at desc);
select enable_tenant_rls('vuln_reports');

create table vuln_report_findings (
  tenant_id uuid not null,
  report_id uuid not null,
  finding_id uuid not null,
  primary key (tenant_id, report_id, finding_id),
  foreign key (tenant_id, report_id) references vuln_reports (tenant_id, id) on delete cascade,
  foreign key (tenant_id, finding_id) references findings (tenant_id, id) on delete cascade
);
create index on vuln_report_findings (tenant_id, finding_id);
select enable_tenant_rls('vuln_report_findings');
