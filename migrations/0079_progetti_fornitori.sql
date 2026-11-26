-- Progetti, passo 4 (replica/progetti.md §3 «Modo di consegna», «Componenti condivisi», §7, §12):
-- work package affidati a un fornitore con contratto, deliverable e SAL; avviso di conflitto
-- quando due progetti pianificano lo stesso componente nello stesso periodo; nomi inglesi dei
-- tipi dei progetti. Il programma non ha tabelle nuove: i progetti lo citano in ext_project.program_id.

-- Contratto del work package affidato al fornitore: riferimento (ordine, contratto quadro) e
-- importo, su cui si misurano i SAL dichiarati a importo.
alter table ext_work_package add column contract_ref text check (length(contract_ref) <= 200);
alter table ext_work_package add column contract_amount_cents bigint check (contract_amount_cents >= 0);
alter table ext_work_package add column contract_currency text not null default 'EUR' check (contract_currency ~ '^[A-Z]{3}$');

-- SAL (stato di avanzamento lavori): elemento di tipo progress_claim figlio del work package.
-- Lo dichiara il referente del fornitore (se il work package lo prevede, supplier_updates) o il
-- PM; lo accetta o lo respinge il PM. Percentuale complessiva raggiunta oppure importo del SAL:
-- almeno uno dei due. Solo i SAL accettati fanno salire l'avanzamento.
create table ext_progress_claim (
  tenant_id uuid not null,
  element_id uuid not null,
  project_id uuid not null,
  work_package_id uuid not null,
  number integer not null check (number > 0),
  period_end date,
  percent numeric(5,2) check (percent between 0 and 100),
  amount_cents bigint check (amount_cents >= 0),
  status text not null default 'submitted' check (status in ('submitted','accepted','rejected','withdrawn')),
  note text not null default '' check (length(note) <= 2000),
  submitted_by uuid,
  decided_by uuid,
  decided_at timestamptz,
  decision_note text not null default '' check (length(decision_note) <= 2000),
  check (percent is not null or amount_cents is not null),
  primary key (tenant_id, element_id),
  unique (tenant_id, work_package_id, number),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, project_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, work_package_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, submitted_by) references members (tenant_id, id) on delete set null (submitted_by),
  foreign key (tenant_id, decided_by) references members (tenant_id, id) on delete set null (decided_by)
);
create index on ext_progress_claim (tenant_id, project_id);
create index on ext_progress_claim (tenant_id, submitted_by);
create index on ext_progress_claim (tenant_id, decided_by);
select enable_tenant_rls('ext_progress_claim');

-- Deliverable: cosa si consegna e si accetta (il tipo c'era dal passo 1, senza dati). Figlio del
-- work package; la scadenza è elements.due_at; facoltativamente legato a una milestone (§7).
create table ext_deliverable (
  tenant_id uuid not null,
  element_id uuid not null,
  project_id uuid not null,
  work_package_id uuid not null,
  milestone_id uuid,
  status text not null default 'pending' check (status in ('pending','delivered','accepted','rejected','cancelled')),
  delivered_by uuid,
  delivered_at timestamptz,
  delivery_note text not null default '' check (length(delivery_note) <= 2000),
  decided_by uuid,
  decided_at timestamptz,
  decision_note text not null default '' check (length(decision_note) <= 2000),
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, project_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, work_package_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, milestone_id) references elements (tenant_id, id) on delete set null (milestone_id),
  foreign key (tenant_id, delivered_by) references members (tenant_id, id) on delete set null (delivered_by),
  foreign key (tenant_id, decided_by) references members (tenant_id, id) on delete set null (decided_by)
);
create index on ext_deliverable (tenant_id, project_id);
create index on ext_deliverable (tenant_id, work_package_id);
create index on ext_deliverable (tenant_id, milestone_id);
create index on ext_deliverable (tenant_id, delivered_by);
create index on ext_deliverable (tenant_id, decided_by);
select enable_tenant_rls('ext_deliverable');

-- Conflitti già segnalati al team owner del componente: una riga per coppia di work package
-- (a < b) sulla stessa applicazione nello stesso periodo. Sparisce quando il conflitto si
-- risolve, così uno nuovo si segnala di nuovo.
create table plan_conflicts (
  tenant_id uuid not null,
  work_package_a uuid not null,
  work_package_b uuid not null,
  application_id uuid not null,
  notified_at timestamptz not null default now(),
  primary key (tenant_id, work_package_a, work_package_b),
  check (work_package_a < work_package_b),
  foreign key (tenant_id, work_package_a) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, work_package_b) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, application_id) references elements (tenant_id, id) on delete cascade
);
create index on plan_conflicts (tenant_id, work_package_b);
create index on plan_conflicts (tenant_id, application_id);
select enable_tenant_rls('plan_conflicts');

-- Lessico (§12): i tipi dei progetti hanno anche il nome inglese, come gli altri tipi di
-- sistema (0052). «Work package» e «SAL» sono i nomi di partenza, rinominabili in
-- Configurazione → Tipi.
create or replace function system_type_label_en(k text) returns jsonb language sql immutable as $$
  select case k
    when 'business_request' then '{"name": "Request", "plural": "Requests"}'
    when 'impact' then '{"name": "Impact", "plural": "Impacts"}'
    when 'application' then '{"name": "Application", "plural": "Applications"}'
    when 'logical_asset' then '{"name": "Logical asset", "plural": "Logical assets"}'
    when 'page' then '{"name": "Document", "plural": "Documents"}'
    when 'capability' then '{"name": "Business capability", "plural": "Business capabilities"}'
    when 'risk' then '{"name": "ICT risk", "plural": "Risks"}'
    when 'incident' then '{"name": "Incident", "plural": "Incidents"}'
    when 'ict_provider' then '{"name": "ICT provider", "plural": "ICT providers"}'
    when 'control' then '{"name": "Control", "plural": "Controls"}'
    when 'evidence' then '{"name": "Evidence", "plural": "Evidence"}'
    when 'requirement' then '{"name": "Requirement", "plural": "Requirements"}'
    when 'exception' then '{"name": "Waiver", "plural": "Waivers"}'
    when 'review' then '{"name": "Review", "plural": "Reviews"}'
    when 'epic' then '{"name": "Epic", "plural": "Epics"}'
    when 'story' then '{"name": "Story", "plural": "Stories"}'
    when 'task' then '{"name": "Task", "plural": "Tasks"}'
    when 'bug' then '{"name": "Bug", "plural": "Bugs"}'
    when 'sprint' then '{"name": "Sprint", "plural": "Sprints"}'
    when 'program' then '{"name": "Program", "plural": "Programs"}'
    when 'project' then '{"name": "Project", "plural": "Projects"}'
    when 'work_package' then '{"name": "Work package", "plural": "Work packages"}'
    when 'plan_activity' then '{"name": "Plan activity", "plural": "Plan activities"}'
    when 'milestone' then '{"name": "Milestone", "plural": "Milestones"}'
    when 'deliverable' then '{"name": "Deliverable", "plural": "Deliverables"}'
    when 'change_request' then '{"name": "Change request", "plural": "Change requests"}'
    when 'progress_claim' then '{"name": "Progress claim", "plural": "Progress claims"}'
    else null end::jsonb
$$;

update element_types
set labels = labels || jsonb_build_object('en', system_type_label_en(key))
where is_system and not labels ? 'en' and system_type_label_en(key) is not null;
