-- DevSecOps, fase 3 (replica/devsecops.md §3.3 e §3.4): politica centrale della pipeline,
-- versionata, e adozione guidata tramite pull request.
--
--  * pipeline_policy / pipeline_policy_versions: una politica per tenant. Ogni salvataggio con
--    una definizione diversa è una nuova versione; la definizione (controlli, classi di
--    applicazione e modalità off/warn/block per ogni coppia) è un documento JSON validato
--    dal modulo devsecops (policy.go).
--  * pipeline_adoptions: le pull request aperte da myTrack per allineare un repository, con
--    lo stato sincronizzato (aperta, unita, chiusa) e la versione della politica con cui
--    sono state generate.

create table pipeline_policy (
  tenant_id uuid primary key references tenants(id) on delete cascade,
  current_version integer not null default 1,
  updated_at timestamptz not null default now()
);
select enable_tenant_rls('pipeline_policy');

create table pipeline_policy_versions (
  tenant_id uuid not null references tenants(id) on delete cascade,
  version integer not null check (version >= 1),
  definition jsonb not null,
  note text not null default '',
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, version),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on pipeline_policy_versions (tenant_id, created_by);
select enable_tenant_rls('pipeline_policy_versions');

create table pipeline_adoptions (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  application_element_id uuid,
  integration_id uuid,
  repository text not null,                 -- org/nome
  base_branch text not null,
  branch text not null,
  pr_number integer not null,
  pr_url text not null,
  title text not null,
  state text not null default 'open' check (state in ('open', 'merged', 'closed')),
  policy_version integer,
  class_key text,
  files jsonb not null default '[]',        -- [{path, status}]
  last_error text,
  created_by uuid,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  synced_at timestamptz,
  closed_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, application_element_id) references elements (tenant_id, id) on delete set null (application_element_id),
  foreign key (tenant_id, integration_id) references integrations (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create unique index pipeline_adoptions_pr_uq on pipeline_adoptions (tenant_id, integration_id, repository, pr_number);
create index on pipeline_adoptions (tenant_id, application_element_id);
create index on pipeline_adoptions (tenant_id, integration_id);
create index on pipeline_adoptions (tenant_id, created_by);
create index pipeline_adoptions_open_idx on pipeline_adoptions (tenant_id, synced_at) where state = 'open';
select enable_tenant_rls('pipeline_adoptions');
