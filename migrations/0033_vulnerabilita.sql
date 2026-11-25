-- DevSecOps, fase 2: le vulnerabilità come lavoro governato (replica/devsecops.md §3.2).
--
-- I finding (migrazione 0024) restano la fonte: deduplicati per servizio, vulnerabilità e
-- componente. Qui si aggiungono la scadenza calcolata dalla politica di SLA, il ciclo di vita
-- (aperta, presa in carico, in correzione, eccezione, risolta, chiusa), il team che se ne
-- occupa, le eccezioni approvate e il registro dei passaggi. `status` (open, fixed, accepted,
-- false_positive) continua a dire se il finding c'è ancora nei report della versione in
-- produzione; `stage` dice cosa se ne sta facendo.

-- ---------------------------------------------------------------------
-- Politica del tenant: tabella degli SLA (giorni per gravità × criticità dell'applicazione),
-- fattori per la funzione critica DORA e per l'esposizione su Internet, regole di escalation.
-- ---------------------------------------------------------------------
create table vuln_policy (
  tenant_id uuid not null references tenants(id) on delete cascade,
  -- {"critical": {"critical": 3, "high": 7, "medium": 14, "low": 30, "none": 14}, "high": {...}, ...}
  -- giorni; un valore nullo o assente = nessuno SLA per quella combinazione
  matrix jsonb not null,
  -- percentuale applicata ai giorni se l'applicazione supporta una funzione critica DORA
  dora_percent integer not null default 50 check (dora_percent between 10 and 300),
  -- percentuale applicata se l'applicazione è esposta su Internet
  exposed_percent integer not null default 75 check (exposed_percent between 10 and 300),
  -- campo personalizzato dell'applicazione che dice l'esposizione e valori che valgono "esposta"
  exposure_field text,
  exposure_values text[] not null default '{}',
  soon_days integer not null default 7 check (soon_days between 1 and 90),
  auto_assign boolean not null default true,
  exception_notice_days integer not null default 14 check (exception_notice_days between 0 and 120),
  max_exception_days integer not null default 180 check (max_exception_days between 1 and 1095),
  -- [{"name": "Preavviso", "days": -7, "to": ["assignee", "team_lead"]}, ...]; days negativo = prima
  escalation jsonb not null default '[]',
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on vuln_policy (tenant_id, updated_by);
select enable_tenant_rls('vuln_policy');

-- ---------------------------------------------------------------------
-- Eccezioni (accettazione del rischio) con motivazione, scadenza e approvazione.
-- Una eccezione riguarda una sola applicazione e più vulnerabilità. L'approvazione passa dal
-- motore delle approvazioni (politica "@vuln_exception", richiesta sull'elemento applicazione).
-- Il collegamento al rischio del registro dei rischi è facoltativo.
-- ---------------------------------------------------------------------
create table vuln_exceptions (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  number integer not null,
  application_id uuid not null,
  reason text not null check (length(reason) between 1 and 4000),
  compensating text not null default '',
  until date not null,
  risk_score integer not null default 1,
  risk_element_id uuid,
  status text not null default 'pending' check (status in ('pending','approved','rejected','expired','revoked')),
  approval_request_id uuid,
  requested_by uuid,
  requested_at timestamptz not null default now(),
  decided_at timestamptz,
  closed_at timestamptz,
  notice_sent_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, application_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, risk_element_id) references elements (tenant_id, id) on delete set null (risk_element_id),
  foreign key (tenant_id, requested_by) references members (tenant_id, id) on delete set null (requested_by)
);
create unique index vuln_exceptions_number_uq on vuln_exceptions (tenant_id, number);
create index on vuln_exceptions (tenant_id, application_id, status);
create index on vuln_exceptions (tenant_id, status, until);
create index on vuln_exceptions (tenant_id, approval_request_id);
create index on vuln_exceptions (tenant_id, risk_element_id);
create index on vuln_exceptions (tenant_id, requested_by);
select enable_tenant_rls('vuln_exceptions');

-- ---------------------------------------------------------------------
-- Ciclo di vita dei finding.
-- ---------------------------------------------------------------------
alter table findings
  add column stage text not null default 'open'
    check (stage in ('open','taken','in_progress','exception','resolved','closed','false_positive')),
  add column sla_days integer,
  add column due_at timestamptz,
  add column team_id uuid,
  add column team_manual boolean not null default false,
  add column assignee_id uuid,
  add column taken_at timestamptz,
  add column closed_at timestamptz,
  add column closed_reason text check (closed_reason in ('auto','manual','false_positive')),
  -- ultimo passo di escalation già notificato (0 = nessuno): un avviso parte una volta sola
  add column notice_level integer not null default 0,
  add column exception_id uuid;
alter table findings
  add constraint findings_team_fk foreign key (tenant_id, team_id) references teams (tenant_id, id) on delete set null (team_id),
  add constraint findings_assignee_fk foreign key (tenant_id, assignee_id) references members (tenant_id, id) on delete set null (assignee_id),
  add constraint findings_exception_fk foreign key (tenant_id, exception_id) references vuln_exceptions (tenant_id, id) on delete set null (exception_id);

-- I finding già presenti seguono lo stato tecnico che avevano.
update findings set stage = 'closed', closed_at = last_seen, closed_reason = 'auto' where status = 'fixed';
update findings set stage = 'exception' where status = 'accepted';
update findings set stage = 'false_positive', closed_at = last_seen, closed_reason = 'false_positive' where status = 'false_positive';

create index findings_work_idx on findings (tenant_id, due_at) where stage in ('open','taken','in_progress');
create index on findings (tenant_id, stage, severity);
create index on findings (tenant_id, team_id);
create index on findings (tenant_id, assignee_id);
create index on findings (tenant_id, exception_id);
create index on findings (tenant_id, closed_at) where closed_at is not null;

create table vuln_exception_findings (
  tenant_id uuid not null,
  exception_id uuid not null,
  finding_id uuid not null,
  primary key (tenant_id, exception_id, finding_id),
  foreign key (tenant_id, exception_id) references vuln_exceptions (tenant_id, id) on delete cascade,
  foreign key (tenant_id, finding_id) references findings (tenant_id, id) on delete cascade
);
create index on vuln_exception_findings (tenant_id, finding_id);
select enable_tenant_rls('vuln_exception_findings');

-- ---------------------------------------------------------------------
-- Registro dei passaggi di una vulnerabilità: chi, quando, da quale stadio a quale.
-- ---------------------------------------------------------------------
create table vuln_events (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  finding_id uuid not null,
  at timestamptz not null default now(),
  actor_id uuid,
  kind text not null,
  from_stage text,
  to_stage text,
  note text not null default '',
  primary key (tenant_id, id),
  foreign key (tenant_id, finding_id) references findings (tenant_id, id) on delete cascade,
  foreign key (tenant_id, actor_id) references members (tenant_id, id) on delete set null (actor_id)
);
create index on vuln_events (tenant_id, finding_id, at desc);
create index on vuln_events (tenant_id, actor_id);
select enable_tenant_rls('vuln_events');

-- ---------------------------------------------------------------------
-- Approvazione delle eccezioni: nuovo soggetto delle richieste di approvazione. Il vincolo si
-- estende leggendo quello attuale, per non togliere i soggetti aggiunti da altre migrazioni.
-- ---------------------------------------------------------------------
do $$
declare def text;
begin
  select pg_get_constraintdef(oid) into def from pg_constraint
  where conname = 'approval_requests_subject_check' and conrelid = 'approval_requests'::regclass;
  if def is not null and position('vuln_exception' in def) = 0 then
    execute 'alter table approval_requests drop constraint approval_requests_subject_check';
    execute 'alter table approval_requests add constraint approval_requests_subject_check ' ||
      replace(def, '])', ', ''vuln_exception''::text])');
  end if;
end $$;
