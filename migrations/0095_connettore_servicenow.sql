-- Connettore ServiceNow in sola lettura (DevSecOps): incidenti e change request dei servizi
-- CMDB associati alle applicazioni del catalogo.
--
-- Riusa le tabelle generiche dei connettori (0025): integrations (kind «servicenow», config
-- senza segreti, secret_enc con password o segreto OAuth e segreto del webhook),
-- connector_repositories (un servizio o CI della CMDB per riga: external_id = sys_id,
-- description = classe CMDB, con proposta e conferma dell'associazione), connector_sync_state
-- e connector_sync_runs (stato e storico, cursori su sys_updated_on), inbound_deliveries
-- (consegne del webhook deduplicate).
--
-- Si conserva il minimo con il link al record: niente chiamante, assegnatario, commenti o
-- note di lavoro, che contengono dati personali.

alter table integrations drop constraint integrations_kind_check;
alter table integrations add constraint integrations_kind_check
  check (kind in ('github','gitlab','gitea','servicenow','bitbucket','azure_devops','jenkins','generic'));

-- =====================================================================
-- 1. Incidenti
-- =====================================================================

-- Un incidente ServiceNow delle priorità configurate su un servizio associato.
--
-- dora_status dice se conta nelle metriche DORA (tempo di ripristino e tasso di fallimento):
--   pending  in attesa di una persona (con «solo dopo conferma» non conta ancora);
--   counted  confermato: conta;
--   excluded non rilevante: non conta mai;
-- con la regola «contano da soli» dell'integrazione contano anche i pending. Un incidente
-- trasformato in incidente di Regia (regia_incident_element_id) conta una volta sola,
-- tramite quello di Regia.
create table servicenow_incidents (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  integration_id uuid not null,
  sys_id text not null check (length(sys_id) between 1 and 64),
  number text not null check (length(number) between 1 and 40),
  priority smallint not null check (priority between 1 and 5),
  state text not null default '',
  opened_at timestamptz not null,
  resolved_at timestamptz,
  closed_at timestamptz,
  short_description text not null default '' check (length(short_description) <= 300),
  ci_sys_id text not null default '',
  service_sys_id text not null default '',
  url text not null default '',
  repository_id uuid,                       -- il servizio associato (connector_repositories)
  application_element_id uuid,              -- l'applicazione del catalogo, dal servizio
  sys_updated_on timestamptz not null,
  dora_status text not null default 'pending' check (dora_status in ('pending', 'counted', 'excluded')),
  decided_by uuid,
  decided_at timestamptz,
  regia_incident_element_id uuid,
  first_seen_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, integration_id) references integrations (tenant_id, id) on delete cascade,
  foreign key (tenant_id, repository_id) references connector_repositories (tenant_id, id) on delete set null (repository_id),
  foreign key (tenant_id, application_element_id) references elements (tenant_id, id) on delete set null (application_element_id),
  foreign key (tenant_id, regia_incident_element_id) references elements (tenant_id, id) on delete set null (regia_incident_element_id),
  foreign key (tenant_id, decided_by) references members (tenant_id, id) on delete set null (decided_by)
);
create unique index servicenow_incidents_sys_uq on servicenow_incidents (tenant_id, integration_id, sys_id);
create index on servicenow_incidents (tenant_id, application_element_id, opened_at desc);
create index on servicenow_incidents (tenant_id, opened_at);
create index on servicenow_incidents (tenant_id, repository_id);
create index on servicenow_incidents (tenant_id, regia_incident_element_id);
create index on servicenow_incidents (tenant_id, decided_by);
create index on servicenow_incidents (tenant_id, upper(number));
select enable_tenant_rls('servicenow_incidents');

-- =====================================================================
-- 2. Change request
-- =====================================================================

-- Una change request dei servizi associati, o il cui numero è già noto a Regia (tabella
-- changes, per numero o alias). Il collegamento a changes, quindi a rilasci e richieste, si
-- fa per numero alla lettura: resta giusto anche quando il change di Regia nasce dopo.
-- approval è il campo approval di ServiceNow: not requested, requested, approved, rejected.
create table servicenow_changes (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  integration_id uuid not null,
  sys_id text not null check (length(sys_id) between 1 and 64),
  number text not null check (length(number) between 1 and 40),
  state text not null default '',
  approval text not null default '',
  type text not null default '',
  planned_start timestamptz,
  planned_end timestamptz,
  actual_start timestamptz,
  actual_end timestamptz,
  short_description text not null default '' check (length(short_description) <= 300),
  ci_sys_id text not null default '',
  service_sys_id text not null default '',
  url text not null default '',
  repository_id uuid,
  application_element_id uuid,
  sys_updated_on timestamptz not null,
  first_seen_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, integration_id) references integrations (tenant_id, id) on delete cascade,
  foreign key (tenant_id, repository_id) references connector_repositories (tenant_id, id) on delete set null (repository_id),
  foreign key (tenant_id, application_element_id) references elements (tenant_id, id) on delete set null (application_element_id)
);
create unique index servicenow_changes_sys_uq on servicenow_changes (tenant_id, integration_id, sys_id);
create index on servicenow_changes (tenant_id, upper(number));
create index on servicenow_changes (tenant_id, application_element_id, sys_updated_on desc);
create index on servicenow_changes (tenant_id, repository_id);
select enable_tenant_rls('servicenow_changes');
