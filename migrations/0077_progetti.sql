-- Progetti (replica/progetti.md, passi 1 e 2): il progetto sopra le epiche, con il piano del PM.
-- La richiesta dice perché e quanto, il progetto come, quando e chi, l'epic cosa fa un team.
-- I tipi (program, project, work_package, plan_activity, milestone, deliverable, change_request)
-- li crea il provisioning del modulo dei progetti, che porta anche le richieste approvate dei
-- tenant esistenti nei loro progetti (regia tenant provision).

-- Impostazioni del tenant per i progetti (§8).
create table project_settings (
  tenant_id uuid primary key references tenants(id) on delete cascade,
  default_template text not null default 'mixed' check (default_template in ('agile','waterfall','mixed','contract')),
  -- La richiesta approvata diventa direttamente un progetto (decisione 1 del 2026-11-26).
  auto_from_request boolean not null default true,
  programs_enabled boolean not null default false,
  progress_method text not null default 'weighted_estimate' check (progress_method in ('weighted_estimate','equal')),
  updated_by uuid,
  updated_at timestamptz not null default now(),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on project_settings (tenant_id, updated_by);
select enable_tenant_rls('project_settings');

-- Ruoli del progetto: quali ruoli per oggetto (stakeholder_roles) usa il progetto, quali sono
-- obbligatori e quali passano dalla richiesta al progetto quando nasce.
create table project_roles (
  tenant_id uuid not null references tenants(id) on delete cascade,
  role_id uuid not null,
  required boolean not null default false,
  inherit_from_request boolean not null default false,
  ordinal integer not null default 0,
  primary key (tenant_id, role_id),
  foreign key (tenant_id, role_id) references stakeholder_roles (tenant_id, id) on delete cascade
);
select enable_tenant_rls('project_roles');

-- Il progetto: modello, programma e tolleranze (le tolleranze servono alle change request, passo 3).
alter table ext_project add column template text not null default 'mixed' check (template in ('agile','waterfall','mixed','contract'));
alter table ext_project add column program_id uuid;
alter table ext_project add column tolerance_days integer not null default 10 check (tolerance_days >= 0);
alter table ext_project add column tolerance_budget_pct numeric(5,2) not null default 5 check (tolerance_budget_pct >= 0);
alter table ext_project add constraint ext_project_program_fk
  foreign key (tenant_id, program_id) references elements (tenant_id, id) on delete set null (program_id);
create index on ext_project (tenant_id, program_id);

-- Richieste confluite nel progetto: una richiesta sta in un solo progetto, un progetto può
-- raccoglierne più d'una.
create table project_requests (
  tenant_id uuid not null,
  project_id uuid not null,
  request_id uuid not null,
  created_at timestamptz not null default now(),
  primary key (tenant_id, request_id),
  foreign key (tenant_id, project_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, request_id) references elements (tenant_id, id) on delete cascade
);
create index on project_requests (tenant_id, project_id);
select enable_tenant_rls('project_requests');

-- Fasi del piano: raggruppamento nel tempo, con un cancello di approvazione facoltativo per
-- passare alla successiva (decisione 2). Le date sono l'inviluppo delle voci della fase.
create table plan_phases (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  project_id uuid not null,
  name text not null check (length(name) between 1 and 200),
  ordinal integer not null default 0,
  gate_enabled boolean not null default false,
  gate_status text not null default 'open' check (gate_status in ('open','pending','approved','rejected')),
  gate_request_id uuid,
  closed_at timestamptz,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, project_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, gate_request_id) references approval_requests (tenant_id, id) on delete set null (gate_request_id)
);
create index on plan_phases (tenant_id, project_id, ordinal);
create index on plan_phases (tenant_id, gate_request_id);
select enable_tenant_rls('plan_phases');

-- Work package: un pezzo di lavoro con un solo responsabile e un solo modo di consegna.
-- Le date stanno in elements.start_at / due_at, le dipendenze nelle relazioni depends_on
-- (attributi kind = fs | ss | ff e lag in giorni lavorativi).
create table ext_work_package (
  tenant_id uuid not null,
  element_id uuid not null,
  project_id uuid not null,
  phase_id uuid,
  delivery_mode text not null default 'agile' check (delivery_mode in ('agile','waterfall','supplier')),
  application_id uuid,
  team_id uuid,
  supplier_id uuid,                         -- fornitore del catalogo (ict_provider)
  -- Passo 4: SAL inseriti dal referente del fornitore (utente esterno) invece che dal PM.
  supplier_updates boolean not null default false,
  estimate_effort_days numeric check (estimate_effort_days >= 0),
  weight numeric check (weight > 0),        -- peso impostato a mano; null = la stima
  impact_id uuid,                           -- impatto della richiesta da cui nasce
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, project_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, phase_id) references plan_phases (tenant_id, id) on delete set null (phase_id),
  foreign key (tenant_id, application_id) references elements (tenant_id, id) on delete set null (application_id),
  foreign key (tenant_id, team_id) references teams (tenant_id, id) on delete set null (team_id),
  foreign key (tenant_id, supplier_id) references elements (tenant_id, id) on delete set null (supplier_id),
  foreign key (tenant_id, impact_id) references elements (tenant_id, id) on delete set null (impact_id)
);
create index on ext_work_package (tenant_id, project_id);
create index on ext_work_package (tenant_id, phase_id);
create index on ext_work_package (tenant_id, application_id);
create index on ext_work_package (tenant_id, team_id);
create index on ext_work_package (tenant_id, supplier_id);
create unique index ext_work_package_impact_uq on ext_work_package (tenant_id, impact_id) where impact_id is not null;
select enable_tenant_rls('ext_work_package');

-- Attività: passo dentro un work package a cascata, figlio del work package.
create table ext_plan_activity (
  tenant_id uuid not null,
  element_id uuid not null,
  project_id uuid not null,
  work_package_id uuid not null,
  estimate_effort_days numeric check (estimate_effort_days >= 0),
  percent numeric(5,2) not null default 0 check (percent between 0 and 100),
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, project_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, work_package_id) references elements (tenant_id, id) on delete cascade
);
create index on ext_plan_activity (tenant_id, project_id);
create index on ext_plan_activity (tenant_id, work_package_id);
select enable_tenant_rls('ext_plan_activity');

-- Milestone: la tabella c'è dallo schema di partenza (0001, con la RLS) e non era usata. Durata
-- zero: la data prevista è elements.due_at, actual_date è il giorno in cui è stata raggiunta;
-- baseline_date la fissa il passo 3. Si aggiungono progetto, fase, work package e il flag contrattuale.
alter table ext_milestone add column project_id uuid;
alter table ext_milestone add column phase_id uuid;
alter table ext_milestone add column work_package_id uuid;
alter table ext_milestone add column contractual boolean not null default false;
alter table ext_milestone add constraint ext_milestone_project_fk
  foreign key (tenant_id, project_id) references elements (tenant_id, id) on delete cascade;
alter table ext_milestone add constraint ext_milestone_phase_fk
  foreign key (tenant_id, phase_id) references plan_phases (tenant_id, id) on delete set null (phase_id);
alter table ext_milestone add constraint ext_milestone_work_package_fk
  foreign key (tenant_id, work_package_id) references elements (tenant_id, id) on delete set null (work_package_id);
create index on ext_milestone (tenant_id, project_id);
create index on ext_milestone (tenant_id, phase_id);
create index on ext_milestone (tenant_id, work_package_id);

-- Avanzamento dichiarato, storicizzato: attività a cascata e work package senza epic.
-- Serve a ricostruire l'andamento nel tempo; la percentuale corrente è l'ultima riga.
create table ext_plan_progress (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  element_id uuid not null,
  recorded_on date not null default current_date,
  percent numeric(5,2) not null check (percent between 0 and 100),
  note text not null default '' check (length(note) <= 2000),
  member_id uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete set null (member_id)
);
create index on ext_plan_progress (tenant_id, element_id, created_at desc);
create index on ext_plan_progress (tenant_id, member_id);
select enable_tenant_rls('ext_plan_progress');

-- Cancelli di fase nel motore delle approvazioni: nuovo soggetto, letto il vincolo attuale
-- per non togliere gli altri.
do $$
declare def text;
begin
  select pg_get_constraintdef(oid) into def from pg_constraint
  where conname = 'approval_requests_subject_check' and conrelid = 'approval_requests'::regclass;
  if def is not null and position('phase_gate' in def) = 0 then
    execute 'alter table approval_requests drop constraint approval_requests_subject_check';
    execute 'alter table approval_requests add constraint approval_requests_subject_check ' ||
      replace(def, '])', ', ''phase_gate''::text])');
  end if;
end $$;
