-- Collegamento commit → elemento. I connettori (GitHub, Gitea) leggono i commit del branch
-- predefinito e i titoli delle pull request unite; una chiave di elemento nel messaggio
-- («HL-12») lega il commit all'elemento. Il primo commit di un servizio tra due rilasci in
-- produzione è l'inizio del lead time DORA (nuova fonte «commit»).

create table element_commits (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,
  service_id uuid not null,
  repository text not null,
  sha text not null,
  title text not null default '',
  author text not null default '',
  committed_at timestamptz not null,
  url text not null default '',
  -- commit: letto dal branch predefinito; pull_request: commit di merge di una PR con la chiave nel titolo o nel branch
  source text not null default 'commit' check (source in ('commit','pull_request')),
  pr_number integer,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, service_id) references services (tenant_id, id) on delete cascade
);
create unique index element_commits_uq on element_commits (tenant_id, element_id, sha);
create index element_commits_element_idx on element_commits (tenant_id, element_id);
create index element_commits_service_idx on element_commits (tenant_id, service_id, committed_at);
select enable_tenant_rls('element_commits');

-- Nuova fonte per l'inizio del lead time DORA.
alter table dora_settings drop constraint dora_settings_lead_time_start_check;
alter table dora_settings add constraint dora_settings_lead_time_start_check
  check (lead_time_start in ('auto','commit','check_run','change','request'));
