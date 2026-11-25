-- Tempo e team (piano, passo 18; replica/work.md «Tempo e team»): timesheet settimanale con
-- invio e approvazione facoltativi e blocco dei periodi chiusi, membri dei team con
-- capacità e assenze, comandi in blocco con annullamento. Le ore restano in work_logs.

-- Capacità della persona nel team: ore a settimana (null = quelle della persona) e, con
-- allocation_pct già presente, la quota dedicata al team.
alter table team_members add column weekly_hours numeric check (weekly_hours between 0 and 80);

-- Assenze semplici: giorni interi, o poche ore al giorno. Riducono la capacità delle settimane.
create table member_absences (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  member_id uuid not null,
  from_date date not null,
  to_date date not null,
  kind text not null default 'ferie' check (kind in ('ferie', 'malattia', 'permesso', 'altro')),
  hours_per_day numeric check (hours_per_day > 0 and hours_per_day <= 24),  -- null = tutta la giornata
  note text,
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  check (to_date >= from_date and to_date - from_date <= 366),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on member_absences (tenant_id, member_id, from_date);
create index on member_absences (tenant_id, created_by);
select enable_tenant_rls('member_absences');

-- Regole del tempo, per tenant: approvazione del responsabile (facoltativa) e periodo chiuso.
create table time_settings (
  tenant_id uuid primary key references tenants(id) on delete cascade,
  require_approval boolean not null default false,
  approver_mode text not null default 'team_lead' check (approver_mode in ('team_lead', 'member', 'admin')),
  approver_member_id uuid,
  locked_until date,                                   -- fino a questo giorno (compreso) non si scrive più
  max_day_minutes integer not null default 960 check (max_day_minutes between 60 and 1440),
  updated_by uuid,
  updated_at timestamptz not null default now(),
  foreign key (tenant_id, approver_member_id) references members (tenant_id, id) on delete set null (approver_member_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on time_settings (tenant_id, approver_member_id);
create index on time_settings (tenant_id, updated_by);
select enable_tenant_rls('time_settings');

-- Settimane inviate: senza riga la settimana è in bozza. Inviata o approvata, le ore non
-- cambiano; respinta torna modificabile.
create table timesheets (
  tenant_id uuid not null,
  member_id uuid not null,
  week_start date not null check (extract(isodow from week_start) = 1),
  status text not null check (status in ('submitted', 'approved', 'rejected')),
  minutes integer not null default 0,
  approver_id uuid,                                    -- chi deve decidere (null = chiunque abbia i poteri)
  submitted_at timestamptz not null default now(),
  decided_by uuid,
  decided_at timestamptz,
  comment text,
  primary key (tenant_id, member_id, week_start),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, approver_id) references members (tenant_id, id) on delete set null (approver_id),
  foreign key (tenant_id, decided_by) references members (tenant_id, id) on delete set null (decided_by)
);
create index on timesheets (tenant_id, approver_id) where status = 'submitted';
create index on timesheets (tenant_id, status, week_start);
create index on timesheets (tenant_id, decided_by);
select enable_tenant_rls('timesheets');

-- Comandi in blocco: ciò che è cambiato, per annullare l'ultimo comando di una persona.
create table bulk_commands (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  member_id uuid not null,
  summary text not null,
  commands jsonb not null,
  changes jsonb not null default '[]',                 -- per elemento: valore precedente e nuovo
  created_at timestamptz not null default now(),
  undone_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on bulk_commands (tenant_id, member_id, created_at desc);
select enable_tenant_rls('bulk_commands');
