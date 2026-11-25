-- Import di progetti da altri strumenti: Jira, YouTrack, CSV generico (docs/import.md).
-- Riusa import_profiles e import_runs (0001, 0016) con il target "work_items": la procedura
-- guidata carica il file o si collega all'API, analizza, salva la mappatura, prova senza
-- salvare ed esegue in background a lotti. Un secondo import aggiorna invece di duplicare
-- (import_origins) e l'annullamento elimina ciò che l'import ha creato, se non è cambiato.

-- Profili di mappatura anche per gli elementi di lavoro.
alter table import_profiles drop constraint if exists import_profiles_target_check;
alter table import_profiles add constraint import_profiles_target_check
  check (target in ('cost_entries', 'budget_lines', 'work_logs', 'exchange_rates', 'rates', 'work_items'));

-- Esecuzioni: origine, analisi, avanzamento in background e report finale.
alter table import_runs add column source text;             -- jira_csv | youtrack_csv | csv | jira_api | youtrack_api
alter table import_runs add column analysis jsonb;          -- tipi, stati, utenti, campi e sprint trovati
alter table import_runs add column progress jsonb not null default '{}';
alter table import_runs add column report jsonb;            -- conteggi finali (e dell'annullamento)
alter table import_runs add column space_id uuid;           -- spazio di destinazione
alter table import_runs add column connection jsonb;        -- API: indirizzo, progetto, query (mai il token)
alter table import_runs add column started_at timestamptz;
alter table import_runs add column heartbeat_at timestamptz; -- un'esecuzione ferma da minuti è interrotta
alter table import_runs add constraint import_runs_space_fk
  foreign key (tenant_id, space_id) references spaces (tenant_id, id) on delete set null (space_id);
create index on import_runs (tenant_id, space_id);
create index import_runs_target_idx on import_runs (tenant_id, target, created_at desc);
-- Un solo import di elementi in esecuzione per tenant.
create unique index import_runs_one_running on import_runs (tenant_id) where target = 'work_items' and status = 'running';

-- I dati letti dal file o dall'API, normalizzati e compressi (gzip): servono ad analisi,
-- anteprima ed esecuzione senza ricaricare il file. Il token dell'API non si salva mai.
create table import_datasets (
  tenant_id uuid not null,
  run_id uuid not null,
  content bytea not null,
  size_bytes bigint not null,
  created_at timestamptz not null default now(),
  primary key (tenant_id, run_id),
  foreign key (tenant_id, run_id) references import_runs (tenant_id, id) on delete cascade
);
select enable_tenant_rls('import_datasets');

-- Chiave d'origine → oggetto di myTrack. È ciò che rende l'import idempotente: PROJ-123
-- importato una seconda volta aggiorna l'elemento invece di crearne un altro.
create table import_origins (
  tenant_id uuid not null,
  system text not null,              -- jira | youtrack | csv
  kind text not null check (kind in ('element', 'comment', 'work_log')),
  source_key text not null,          -- PROJ-123, PROJ-123#c:10001, PROJ-123#w:…
  target_id uuid not null,
  run_id uuid not null,              -- import che l'ha creato
  last_run_id uuid not null,         -- ultimo import che l'ha scritto
  written_version integer,           -- versione dell'elemento dopo l'ultima scrittura dell'import
  written_at timestamptz not null default now(),
  primary key (tenant_id, system, kind, source_key),
  foreign key (tenant_id, run_id) references import_runs (tenant_id, id) on delete cascade
);
create index on import_origins (tenant_id, run_id);
create index on import_origins (tenant_id, last_run_id);
create index on import_origins (tenant_id, target_id);
select enable_tenant_rls('import_origins');

-- Gli altri oggetti creati da un import (relazioni, campi, opzioni, spazio, team, membro
-- «importato», allegati): l'annullamento li elimina se nessuno li usa.
create table import_created (
  tenant_id uuid not null,
  run_id uuid not null,
  kind text not null check (kind in ('relation', 'field', 'option', 'space', 'team', 'member', 'relation_type', 'attachment')),
  target_id uuid not null,
  primary key (tenant_id, run_id, kind, target_id),
  foreign key (tenant_id, run_id) references import_runs (tenant_id, id) on delete cascade
);
select enable_tenant_rls('import_created');

-- Le date originali di aggiornamento: dentro una transazione dell'importatore
-- (app.import = on) updated_at resta quello scritto, invece di diventare now().
create or replace function elements_set_updated_at() returns trigger language plpgsql as $$
begin
  if current_setting('app.import', true) = 'on' then
    return new;
  end if;
  if new.rank is distinct from old.rank
     and (to_jsonb(new) - 'rank' - 'updated_at' - 'search') = (to_jsonb(old) - 'rank' - 'updated_at' - 'search') then
    new.updated_at := old.updated_at;
  else
    new.updated_at := now();
  end if;
  return new;
end $$;
