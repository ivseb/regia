-- Automazioni: regole dei processi a tempo e periodiche (docs/sviluppo/automazioni.md).
--
-- Esecuzioni delle regole a tempo («30 giorni prima della scadenza») e periodiche («ogni
-- lunedì»): una riga per regola, elemento e occorrenza. L'unicità è la garanzia di
-- idempotenza: un'occorrenza scatta una volta sola, anche se il lavoratore riparte o se il
-- processo esce con una versione nuova che tiene la stessa chiave della regola.
-- occurrence: la data calcolata (eventi «at:») o il giorno del giro (eventi «every:»).
create table rule_runs (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  process_id uuid not null,
  version_id uuid not null,
  rule_key text not null check (length(rule_key) <= 64),
  element_id uuid not null,
  occurrence text not null check (length(occurrence) <= 40),
  event text not null check (length(event) <= 200),
  status text not null default 'ok' check (status in ('ok', 'error', 'skipped')),
  note text not null default '' check (length(note) <= 1000),
  fired_at timestamptz not null default now(),
  primary key (tenant_id, id),
  unique (tenant_id, process_id, rule_key, element_id, occurrence),
  foreign key (tenant_id, process_id) references process_definitions (tenant_id, id) on delete cascade,
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
);
create index on rule_runs (tenant_id, process_id, rule_key, fired_at desc);
create index on rule_runs (tenant_id, element_id);
select enable_tenant_rls('rule_runs');

-- Impostazioni delle automazioni del tenant (una riga; senza riga valgono i predefiniti):
--   enabled      le regole a tempo e periodiche girano (quelle sugli eventi girano sempre);
--   run_hour     ora del tenant (fuso tenants.default_timezone) da cui scattano le occorrenze del giorno;
--   max_per_run  quante esecuzioni al massimo per giro: le altre passano al giro dopo.
create table automation_settings (
  tenant_id uuid not null,
  enabled boolean not null default true,
  run_hour integer not null default 8 check (run_hour between 0 and 23),
  max_per_run integer not null default 200 check (max_per_run between 1 and 5000),
  updated_at timestamptz not null default now(),
  primary key (tenant_id)
);
select enable_tenant_rls('automation_settings');
