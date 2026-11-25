-- Valutazione e priorità del portafoglio, organi di governance e sedute
-- (demand.md §6-7; piano, passi 8 e 9).

-- ---------------------------------------------------------------------
-- Scorecard versionate, come i processi: ogni riga di scorecards è una versione di
-- una scorecard (chiave). I punteggi (scorecard_results) puntano alla versione con cui
-- sono stati dati e restano legati a quella anche dopo una nuova pubblicazione.
-- ---------------------------------------------------------------------
alter table scorecards add column key text not null default 'portafoglio' check (key ~ '^[a-z][a-z0-9_]{0,62}$');
alter table scorecards add column version integer not null default 1 check (version > 0);
alter table scorecards add column status text not null default 'published' check (status in ('draft','published','superseded'));
alter table scorecards add column description text not null default '';
alter table scorecards add column type_id uuid;                       -- tipo valutato (null = tutti)
alter table scorecards add column scorer_roles text[] not null default '{demand_manager,pmo}';
alter table scorecards add column notes text not null default '';
alter table scorecards add column created_by uuid;
alter table scorecards add column created_at timestamptz not null default now();
alter table scorecards add column published_by uuid;
alter table scorecards add column published_at timestamptz;
alter table scorecards add constraint scorecards_type_fk
  foreign key (tenant_id, type_id) references element_types (tenant_id, id) on delete cascade;
alter table scorecards add constraint scorecards_created_by_fk
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by);
alter table scorecards add constraint scorecards_published_by_fk
  foreign key (tenant_id, published_by) references members (tenant_id, id) on delete set null (published_by);
create unique index scorecards_version_uq on scorecards (tenant_id, key, version);
create unique index scorecards_one_draft on scorecards (tenant_id, key) where status = 'draft';
create unique index scorecards_one_published on scorecards (tenant_id, key) where status = 'published';
create index on scorecards (tenant_id, type_id) where status = 'published';
create index on scorecards (tenant_id, created_by);
create index on scorecards (tenant_id, published_by);

-- Dopo la pubblicazione cambia solo lo stato (published -> superseded).
create or replace function scorecards_immutable() returns trigger language plpgsql as $$
begin
  if old.status <> 'draft' then
    if new.criteria is distinct from old.criteria or new.name <> old.name or new.method <> old.method
       or new.key <> old.key or new.version <> old.version or new.scorer_roles is distinct from old.scorer_roles
       or new.description <> old.description or new.type_id is distinct from old.type_id then
      raise exception 'la versione % della scorecard è pubblicata e non si può modificare', old.version;
    end if;
    if not (old.status = new.status or (old.status = 'published' and new.status = 'superseded')) then
      raise exception 'passaggio di stato della scorecard non ammesso: % -> %', old.status, new.status;
    end if;
  end if;
  return new;
end $$;
create trigger scorecards_immutable before update on scorecards
  for each row execute function scorecards_immutable();

create or replace function scorecards_no_delete() returns trigger language plpgsql as $$
begin
  if old.status <> 'draft' then
    raise exception 'la versione % della scorecard è pubblicata e non si può cancellare', old.version;
  end if;
  return old;
end $$;
create trigger scorecards_no_delete before delete on scorecards
  for each row when (pg_trigger_depth() = 0)
  execute function scorecards_no_delete();

-- I punteggi sono storia: non si modificano né si cancellano (salvo a cascata con l'elemento).
alter table scorecard_results add column comment text not null default '';
create or replace function scorecard_results_immutable() returns trigger language plpgsql as $$
begin
  raise exception 'i punteggi registrati non si modificano: se ne registra uno nuovo';
end $$;
create trigger scorecard_results_no_update before update on scorecard_results
  for each row when (pg_trigger_depth() = 0)
  execute function scorecard_results_immutable();
create trigger scorecard_results_no_delete before delete on scorecard_results
  for each row when (pg_trigger_depth() = 0)
  execute function scorecard_results_immutable();

-- Punteggio corrente della domanda e versione della scorecard con cui è stato dato.
alter table ext_demand add column score_scorecard_id uuid;
alter table ext_demand add column scored_at timestamptz;
alter table ext_demand add constraint ext_demand_score_scorecard_fk
  foreign key (tenant_id, score_scorecard_id) references scorecards (tenant_id, id) on delete set null (score_scorecard_id);
create index on ext_demand (tenant_id, score_scorecard_id);

-- Ordine manuale della graduatoria: rank è la posizione forzata (null = ordine automatico),
-- dentro il gruppo degli obblighi normativi o delle altre richieste se gli obblighi stanno in testa.
comment on column ext_demand.rank is 'posizione forzata in graduatoria (null = automatica)';
alter table ext_demand add column rank_reason text;
alter table ext_demand add column rank_set_by uuid;
alter table ext_demand add column rank_set_at timestamptz;
alter table ext_demand add constraint ext_demand_rank_positive check (rank is null or rank > 0);
alter table ext_demand add constraint ext_demand_rank_set_by_fk
  foreign key (tenant_id, rank_set_by) references members (tenant_id, id) on delete set null (rank_set_by);
create index on ext_demand (tenant_id, rank_set_by);

-- Impostazioni della graduatoria del portafoglio, una riga per tenant.
create table portfolio_settings (
  tenant_id uuid not null references tenants(id) on delete cascade,
  mandatory_first boolean not null default true,          -- obblighi normativi in testa
  budget_cents bigint check (budget_cents >= 0),          -- budget disponibile (linea di taglio)
  estimate_basis text not null default 'high' check (estimate_basis in ('low','mid','high')),
  excluded_phases text[] not null default '{Bozza}',     -- fasi aperte escluse dalla graduatoria
  rank_roles text[] not null default '{pmo}',            -- ruoli sulla richiesta che forzano l'ordine
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on portfolio_settings (tenant_id, updated_by);
select enable_tenant_rls('portfolio_settings');

-- ---------------------------------------------------------------------
-- Organi di governance: il tipo diventa una chiave libera (i pacchetti di partenza usano
-- investment_committee, steering, architecture_board, cab), con presidente e segretario.
-- ---------------------------------------------------------------------
alter table governance_bodies drop constraint governance_bodies_kind_check;
alter table governance_bodies add constraint governance_bodies_kind_format check (kind ~ '^[a-z][a-z0-9_]{0,40}$');
alter table governance_bodies add column description text not null default '';
alter table governance_bodies add column secretary_id uuid;
alter table governance_bodies add constraint governance_bodies_secretary_fk
  foreign key (tenant_id, secretary_id) references members (tenant_id, id) on delete set null (secretary_id);
alter table governance_bodies add constraint governance_bodies_quorum_positive check (quorum is null or quorum > 0);
create index on governance_bodies (tenant_id, secretary_id);
create index on governance_bodies (tenant_id, kind) where not archived;

-- Sedute: pianificata → convocata → svolta → verbalizzata (oppure annullata).
alter table meetings drop constraint meetings_status_check;
alter table meetings add constraint meetings_status_check check (status in ('planned','convened','held','minuted','cancelled'));
alter table meetings add column title text not null default '';
alter table meetings add column location text not null default '';
alter table meetings add column link text not null default '';
alter table meetings add column convened_at timestamptz;
alter table meetings add column held_at timestamptz;
alter table meetings add column minutes_text text;                 -- bozza o verbale approvato
alter table meetings add column minutes_approved_at timestamptz;
alter table meetings add column minutes_approved_by uuid;
alter table meetings add column minutes_hash text;                 -- sha256 del verbale approvato
alter table meetings add column created_by uuid;
alter table meetings add column created_at timestamptz not null default now();
alter table meetings add constraint meetings_minutes_by_fk
  foreign key (tenant_id, minutes_approved_by) references members (tenant_id, id) on delete set null (minutes_approved_by);
alter table meetings add constraint meetings_created_by_fk
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by);
alter table meetings add constraint meetings_minuted check (status <> 'minuted' or (minutes_approved_at is not null and minutes_text is not null));
create index on meetings (tenant_id, scheduled_at);
create index on meetings (tenant_id, minutes_approved_by);
create index on meetings (tenant_id, created_by);

-- Punti dell'ordine del giorno: decisione, condizioni, chi l'ha registrata e il passo chiuso.
alter table meeting_items add column step_id uuid;
alter table meeting_items add column conditions text;
alter table meeting_items add column decided_by uuid;
alter table meeting_items add column decided_at timestamptz;
alter table meeting_items add column proposed boolean not null default false;  -- proposto dal sistema
alter table meeting_items add constraint meeting_items_step_fk
  foreign key (tenant_id, step_id) references approval_steps (tenant_id, id) on delete set null (step_id);
alter table meeting_items add constraint meeting_items_decided_by_fk
  foreign key (tenant_id, decided_by) references members (tenant_id, id) on delete set null (decided_by);
create index on meeting_items (tenant_id, step_id);
create index on meeting_items (tenant_id, decided_by);

-- Presenze: fotografia dei membri dell'organo alla seduta (il diritto di voto può cambiare dopo).
create table meeting_attendance (
  tenant_id uuid not null,
  meeting_id uuid not null,
  member_id uuid not null,
  voting boolean not null default true,
  present boolean not null default false,
  primary key (tenant_id, meeting_id, member_id),
  foreign key (tenant_id, meeting_id) references meetings (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on meeting_attendance (tenant_id, member_id);
select enable_tenant_rls('meeting_attendance');

-- Verbale immutabile: una seduta verbalizzata non cambia più, né lei né i suoi punti e presenze.
create or replace function meetings_immutable() returns trigger language plpgsql as $$
begin
  if old.status = 'minuted' then
    raise exception 'la seduta è verbalizzata: il verbale approvato non si può modificare';
  end if;
  if tg_op = 'DELETE' then
    return old;
  end if;
  return new;
end $$;
create trigger meetings_immutable before update or delete on meetings
  for each row when (pg_trigger_depth() = 0)
  execute function meetings_immutable();

create or replace function meeting_children_immutable() returns trigger language plpgsql as $$
declare
  st text;
  mid uuid;
  ten uuid;
begin
  if tg_op = 'DELETE' then
    mid := old.meeting_id; ten := old.tenant_id;
  else
    mid := new.meeting_id; ten := new.tenant_id;
  end if;
  select status into st from meetings where tenant_id = ten and id = mid;
  if st = 'minuted' then
    raise exception 'la seduta è verbalizzata: punti e presenze non si possono modificare';
  end if;
  if tg_op = 'DELETE' then
    return old;
  end if;
  return new;
end $$;
create trigger meeting_items_immutable before insert or update or delete on meeting_items
  for each row when (pg_trigger_depth() = 0)
  execute function meeting_children_immutable();
create trigger meeting_attendance_immutable before insert or update or delete on meeting_attendance
  for each row when (pg_trigger_depth() = 0)
  execute function meeting_children_immutable();
