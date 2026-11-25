-- DevSecOps, fase 4: gate di rilascio e evidenze automatiche (devsecops.md §3.5 e §4).
--
-- La pipeline chiede il permesso prima del deploy (POST /api/v1/devsecops/gate); myTrack valuta
-- le regole dell'ambiente, registra la decisione (esito, motivi, audit, storia) e risponde
-- pass, warn o fail. Una decisione fail si può superare con un override motivato e approvato.
-- I rilasci con controlli block superati e le decisioni superate diventano evidenze
-- automatiche dei controlli GRC mappati da interfaccia.

-- ---------------------------------------------------------------------
-- Il permesso devsecops.gate entra nel ruolo di sistema "Pipeline" dei tenant esistenti
-- (i nuovi lo hanno da rbac.SystemRoles).
-- ---------------------------------------------------------------------
update roles set permissions = array_append(permissions, 'devsecops.gate')
  where key = 'pipeline' and not ('devsecops.gate' = any(permissions));

-- ---------------------------------------------------------------------
-- Regole del gate per ambiente. environment = nome dell'ambiente in minuscolo, oppure '*'
-- per tutti gli altri. mode: off (non valuta), warn (non blocca mai), block (blocca).
-- rules: [{"id", "kind", "enabled", "level": "block"|"warn", "label", "params": {...}}].
-- ---------------------------------------------------------------------
create table gate_policies (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  environment text not null check (length(environment) between 1 and 60),
  mode text not null default 'warn' check (mode in ('off','warn','block')),
  rules jsonb not null default '[]',
  override_enabled boolean not null default true,
  override_hours integer not null default 24 check (override_hours between 1 and 720),
  updated_by uuid,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create unique index gate_policies_env_uq on gate_policies (tenant_id, environment);
create index on gate_policies (tenant_id, updated_by);
select enable_tenant_rls('gate_policies');

-- ---------------------------------------------------------------------
-- Decisioni del gate: una per chiamata della pipeline (le identiche ripetute entro pochi
-- minuti valgono una volta). reasons: [{"rule", "kind", "status", "message", "url"}].
-- ---------------------------------------------------------------------
create table gate_decisions (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  application_element_id uuid not null,
  request_element_id uuid,
  service_id uuid,
  environment text not null,
  version text,
  commit text,
  change_number text,
  outcome text not null check (outcome in ('pass','warn','fail')),
  raw_outcome text not null check (raw_outcome in ('pass','warn','fail')),
  mode text not null check (mode in ('off','warn','block','none')),
  overridden boolean not null default false,
  override_id uuid,
  reasons jsonb not null default '[]',
  digest text not null,
  actor_member_id uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, application_element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, request_element_id) references elements (tenant_id, id) on delete set null (request_element_id),
  foreign key (tenant_id, service_id) references services (tenant_id, id) on delete set null (service_id),
  foreign key (tenant_id, actor_member_id) references members (tenant_id, id) on delete set null (actor_member_id)
);
create index on gate_decisions (tenant_id, application_element_id, created_at desc);
create index on gate_decisions (tenant_id, request_element_id, created_at desc);
create index on gate_decisions (tenant_id, created_at desc);
create index on gate_decisions (tenant_id, service_id);
create index on gate_decisions (tenant_id, actor_member_id);
select enable_tenant_rls('gate_decisions');

-- ---------------------------------------------------------------------
-- Override: una persona chiede di superare una decisione fail, con la motivazione; il motore
-- delle approvazioni (politica "@gate_override", sull'applicazione) la approva o la respinge.
-- Approvato, vale fino a expires_at per la stessa applicazione e lo stesso ambiente (e la
-- stessa versione, se la decisione ne aveva una).
-- ---------------------------------------------------------------------
create table gate_overrides (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  decision_id uuid not null,
  application_element_id uuid not null,
  request_element_id uuid,
  environment text not null,
  version text,
  reason text not null check (length(reason) between 1 and 4000),
  hours integer not null check (hours between 1 and 720),
  status text not null default 'pending' check (status in ('pending','approved','rejected','revoked')),
  approval_request_id uuid,
  requested_by uuid,
  requested_at timestamptz not null default now(),
  decided_at timestamptz,
  expires_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, decision_id) references gate_decisions (tenant_id, id) on delete cascade,
  foreign key (tenant_id, application_element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, request_element_id) references elements (tenant_id, id) on delete set null (request_element_id),
  foreign key (tenant_id, approval_request_id) references approval_requests (tenant_id, id) on delete set null (approval_request_id),
  foreign key (tenant_id, requested_by) references members (tenant_id, id) on delete set null (requested_by)
);
create index on gate_overrides (tenant_id, decision_id);
create index on gate_overrides (tenant_id, application_element_id, environment, status);
create index on gate_overrides (tenant_id, request_element_id);
create index on gate_overrides (tenant_id, approval_request_id);
create index on gate_overrides (tenant_id, requested_by);
select enable_tenant_rls('gate_overrides');

alter table gate_decisions
  add constraint gate_decisions_override_fk foreign key (tenant_id, override_id) references gate_overrides (tenant_id, id) on delete set null (override_id);
create index on gate_decisions (tenant_id, override_id);

-- Il soggetto delle richieste di approvazione si estende leggendo il vincolo attuale, per non
-- togliere i soggetti aggiunti da altre migrazioni.
do $$
declare def text;
begin
  select pg_get_constraintdef(oid) into def from pg_constraint
  where conname = 'approval_requests_subject_check' and conrelid = 'approval_requests'::regclass;
  if def is not null and position('gate_override' in def) = 0 then
    execute 'alter table approval_requests drop constraint approval_requests_subject_check';
    execute 'alter table approval_requests add constraint approval_requests_subject_check ' ||
      replace(def, '])', ', ''gate_override''::text])');
  end if;
end $$;

-- ---------------------------------------------------------------------
-- Mappatura "controllo della pipeline → controllo GRC". source = "check:<nome>" (un controllo
-- in modalità block superato da un rilascio) oppure "gate" (una decisione superata).
-- ---------------------------------------------------------------------
create table gate_evidence_map (
  tenant_id uuid not null references tenants(id) on delete cascade,
  source text not null check (length(source) between 1 and 120),
  control_element_id uuid not null,
  valid_days integer check (valid_days is null or valid_days between 1 and 3650),
  production_only boolean not null default true,
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, source, control_element_id),
  foreign key (tenant_id, control_element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on gate_evidence_map (tenant_id, control_element_id);
create index on gate_evidence_map (tenant_id, created_by);
select enable_tenant_rls('gate_evidence_map');
