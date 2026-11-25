-- Avanzamento: status report, dashboard di portafoglio e portale del richiedente
-- (demand.md §11, §14 D01, D11, D12, §16 decisione 7; piano, passi 13 e 14).

-- ---------------------------------------------------------------------
-- Status report (la tabella c'è dalla 0001): bozza o inviato, periodo, fotografia dei
-- dati usati per la precompilazione, versione per le modifiche concorrenti.
-- Un report inviato non si modifica più (trigger): si scrive quello del periodo dopo.
-- ---------------------------------------------------------------------
alter table status_reports add column period_start date;
alter table status_reports add column status text not null default 'submitted' check (status in ('draft','submitted'));
alter table status_reports alter column status set default 'draft';
-- Fotografia dei dati al momento della stesura (Gantt, budget, valutazioni, impatti):
-- resta com'era anche se i dati cambiano, così il confronto tra report è onesto.
alter table status_reports add column data jsonb not null default '{}';
alter table status_reports add column version integer not null default 1;
alter table status_reports add column created_at timestamptz not null default now();
alter table status_reports add column updated_at timestamptz not null default now();
alter table status_reports add column acknowledged_at timestamptz;
alter table status_reports add constraint status_reports_period check (period_start is null or period_start <= period_end);
create index status_reports_element_idx on status_reports (tenant_id, element_id, period_end desc);

create or replace function status_reports_immutable() returns trigger language plpgsql as $$
begin
  if old.status = 'submitted' then
    -- Dopo l'invio cambia solo la presa visione.
    if new.overall is distinct from old.overall or new.schedule is distinct from old.schedule or new.cost is distinct from old.cost
       or new.scope is distinct from old.scope or new.risk is distinct from old.risk or new.summary is distinct from old.summary
       or new.data is distinct from old.data or new.period_end is distinct from old.period_end
       or new.period_start is distinct from old.period_start or new.status is distinct from old.status then
      raise exception 'lo status report è stato inviato e non si può modificare';
    end if;
  end if;
  return new;
end $$;
create trigger status_reports_immutable before update on status_reports
  for each row execute function status_reports_immutable();

-- ---------------------------------------------------------------------
-- Impostazioni del tenant: periodicità di default, fasi in cui il report è atteso,
-- promemoria, indicatori normativi del cruscotto (query con etichetta).
-- ---------------------------------------------------------------------
create table report_settings (
  tenant_id uuid not null references tenants(id) on delete cascade,
  frequency text not null default 'biweekly' check (frequency in ('weekly','biweekly','monthly','off')),
  reminder_days integer not null default 2 check (reminder_days between 0 and 30),
  phases text[] not null default '{Pianificazione,"In esecuzione"}',
  -- Indicatori normativi del cruscotto: [{"label": "DORA", "query": "DORA"}], in arancione.
  regulatory jsonb not null default '[{"label":"DORA","query":"DORA"},{"label":"NIS2","query":"NIS2"}]',
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on report_settings (tenant_id, updated_by);
select enable_tenant_rls('report_settings');

-- Periodicità della singola richiesta e stato dell'ultimo report (per il cruscotto e
-- il campo di query "semaforo:"). frequency null = quella del tenant.
create table status_report_schedules (
  tenant_id uuid not null,
  element_id uuid not null,
  frequency text check (frequency in ('weekly','biweekly','monthly','off')),
  next_due date,
  reminded_for date,                        -- scadenza per cui è già partito il promemoria
  last_report_id uuid,
  last_overall text check (last_overall in ('green','amber','red')),
  last_period_end date,
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, last_report_id) references status_reports (tenant_id, id) on delete set null (last_report_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on status_report_schedules (tenant_id, last_report_id);
create index on status_report_schedules (tenant_id, updated_by);
create index status_report_schedules_due_idx on status_report_schedules (tenant_id, next_due) where next_due is not null;
create index status_report_schedules_rag_idx on status_report_schedules (tenant_id, last_overall) where last_overall is not null;
select enable_tenant_rls('status_report_schedules');

-- ---------------------------------------------------------------------
-- Cruscotto: un pannello per persona (dashboards.layout), uno per nome.
-- ---------------------------------------------------------------------
create unique index dashboards_owner_name_uq on dashboards (tenant_id, owner_id, name) where owner_id is not null;

-- Tempi di attraversamento delle fasi: i cambi di fase nella storia.
create index activities_state_idx on activities (tenant_id, element_id, created_at) where kind = 'state';

-- Campi di query del cruscotto (unità, driver, budget, costi, semaforo, valutatore, scadenza dello SLA).
create index ext_demand_driver_idx on ext_demand (tenant_id, (md5('driver:' || driver)::uuid)) where driver is not null;

-- ---------------------------------------------------------------------
-- Portale del richiedente: testo di benvenuto, tipi esposti con i campi mostrati,
-- spiegazione delle fasi in parole semplici; bozze del modulo guidato.
-- ---------------------------------------------------------------------
create table portal_settings (
  tenant_id uuid not null references tenants(id) on delete cascade,
  welcome text not null default '' check (length(welcome) <= 4000),
  -- [{"type": "business_request", "label": "...", "description": "...", "fields": ["urgenza"], "standard": ["driver", "desired_date"]}]
  types jsonb not null default '[]',
  -- {"Triage": "Stiamo verificando che la richiesta sia completa"}: sostituisce la descrizione della fase.
  phase_texts jsonb not null default '{}',
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on portal_settings (tenant_id, updated_by);
select enable_tenant_rls('portal_settings');

create table portal_drafts (
  tenant_id uuid not null,
  id uuid not null,
  member_id uuid not null,
  type_key text not null,
  title text not null default '' check (length(title) <= 500),
  data jsonb not null default '{}',
  step integer not null default 0 check (step between 0 and 20),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on portal_drafts (tenant_id, member_id, updated_at desc);
select enable_tenant_rls('portal_drafts');
