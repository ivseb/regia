-- Continuità operativa (docs/sviluppo/continuita.md): BIA per capability con versioni approvate,
-- test di resilienza, configurazione versionata del tenant, impegni di ripristino nei contratti
-- dei fornitori ICT. RTO e RPO dichiarati dell'applicazione esistono già (ext_application).

-- BIA: elemento di tipo «bia» nello spazio di architettura, una per capability. I valori in
-- bozza stanno qui; quelli in vigore sono l'ultima riga approvata di bia_versions.
create table ext_bia (
  tenant_id uuid not null,
  element_id uuid not null,
  capability_id uuid not null,
  impacts jsonb not null default '{}',          -- {categoria: {orizzonte: livello}}
  critical boolean,                             -- funzione critica o importante (DORA)
  critical_reason text not null default '',
  mtpd_minutes integer check (mtpd_minutes >= 0),
  rto_minutes integer check (rto_minutes >= 0),
  rpo_minutes integer check (rpo_minutes >= 0),
  notes text not null default '',
  settings_version integer not null default 1,  -- versione della configurazione usata
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, capability_id) references elements (tenant_id, id) on delete cascade
);
create index on ext_bia (tenant_id, capability_id);
select enable_tenant_rls('ext_bia');

-- Versioni approvate della BIA: istantanea congelata all'ingresso in «Approvata».
create table bia_versions (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  bia_id uuid not null,
  version integer not null check (version >= 1),
  snapshot jsonb not null,
  critical boolean not null default false,
  mtpd_minutes integer,
  rto_minutes integer,
  rpo_minutes integer,
  settings_version integer not null,
  approved_by uuid,
  approved_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, bia_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, approved_by) references members (tenant_id, id) on delete set null (approved_by)
);
create unique index bia_versions_uq on bia_versions (tenant_id, bia_id, version);
create index on bia_versions (tenant_id, approved_by);
select enable_tenant_rls('bia_versions');

-- Test di resilienza: elemento «resilience_test» su un'applicazione o una capability.
create table ext_resilience_test (
  tenant_id uuid not null,
  element_id uuid not null,
  subject_id uuid not null,
  test_type text not null,
  planned_on date,
  executed_on date,
  rto_measured_minutes integer check (rto_measured_minutes >= 0),
  rpo_measured_minutes integer check (rpo_measured_minutes >= 0),
  outcome text check (outcome in ('passed', 'passed_with_findings', 'failed')),
  findings jsonb not null default '[]',         -- rilievi: elenco di testi
  notes text not null default '',
  evidence_id uuid,
  evidence_note text not null default '',
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, subject_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, evidence_id) references elements (tenant_id, id) on delete set null (evidence_id)
);
create index on ext_resilience_test (tenant_id, subject_id);
create index on ext_resilience_test (tenant_id, evidence_id);
create index on ext_resilience_test (tenant_id, planned_on);
select enable_tenant_rls('ext_resilience_test');

-- Configurazione della continuità per tenant (scale della BIA, orizzonti, tipi di test, soglie),
-- versionata: ogni salvataggio aggiunge una riga allo storico.
create table continuity_settings (
  tenant_id uuid primary key references tenants(id) on delete cascade,
  version integer not null default 1 check (version >= 1),
  config jsonb not null,
  updated_by uuid,
  updated_at timestamptz not null default now(),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on continuity_settings (tenant_id, updated_by);
select enable_tenant_rls('continuity_settings');

create table continuity_settings_versions (
  tenant_id uuid not null references tenants(id) on delete cascade,
  version integer not null check (version >= 1),
  config jsonb not null,
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, version),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on continuity_settings_versions (tenant_id, created_by);
select enable_tenant_rls('continuity_settings_versions');

-- Impegno contrattuale di ripristino del fornitore ICT.
alter table ict_contracts add column rto_minutes integer check (rto_minutes >= 0);
alter table ict_contracts add column rpo_minutes integer check (rpo_minutes >= 0);

-- Dal problema all'azione: le incoerenze della continuità sono un tipo di problema.
alter table problem_links drop constraint if exists problem_links_source_kind_check;
alter table problem_links add constraint problem_links_source_kind_check
  check (source_kind in ('finding', 'gate_decision', 'requirement', 'adherence', 'continuity'));
