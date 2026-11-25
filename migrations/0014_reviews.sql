-- Valutazioni con questionari (sdlc.md §2, §4-5, §8; piano, passo 4).
-- Team valutatori, tipi di valutazione, modelli di questionario versionati, la
-- valutazione (elemento "review" figlio della richiesta o dell'impatto), la storia
-- delle risposte, l'assegnazione delle sezioni e le prescrizioni proposte.

-- ---------------------------------------------------------------------
-- Team valutatori: un gruppo esistente, un responsabile, SLA ed escalation.
-- ---------------------------------------------------------------------
create table review_teams (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  key text not null check (key ~ '^[a-z][a-z0-9_]{0,62}$'),
  name text not null check (length(name) between 1 and 120),
  description text not null default '',
  group_id uuid not null,
  lead_id uuid,
  -- SLA in giorni lavorativi dall'invio al team (calendario: internal/platform/calendar).
  sla_days integer not null default 5 check (sla_days between 1 and 250),
  -- Sollecito al team N giorni lavorativi prima della scadenza (0 = il giorno stesso).
  reminder_days integer not null default 1 check (reminder_days between 0 and 60),
  -- Escalation al responsabile dopo N giorni lavorativi di ritardo (null = mai).
  escalation_days integer check (escalation_days between 1 and 120),
  archived boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, group_id) references groups (tenant_id, id) on delete restrict,
  foreign key (tenant_id, lead_id) references members (tenant_id, id) on delete set null (lead_id)
);
create unique index review_teams_key_uq on review_teams (tenant_id, key);
create unique index review_teams_name_uq on review_teams (tenant_id, lower(name));
create index on review_teams (tenant_id, group_id);
create index on review_teams (tenant_id, lead_id);
select enable_tenant_rls('review_teams');

-- ---------------------------------------------------------------------
-- Modelli di questionario: ogni riga è una versione di un modello (chiave), come le
-- scorecard. Al massimo una bozza e una versione corrente; le pubblicate sono immutabili.
-- ---------------------------------------------------------------------
create table questionnaire_templates (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  key text not null check (key ~ '^[a-z][a-z0-9_]{0,62}$'),
  version integer not null check (version > 0),
  status text not null default 'draft' check (status in ('draft','published','superseded')),
  name text not null,
  description text not null default '',
  definition jsonb not null,
  notes text not null default '',
  created_by uuid,
  created_at timestamptz not null default now(),
  published_by uuid,
  published_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by),
  foreign key (tenant_id, published_by) references members (tenant_id, id) on delete set null (published_by)
);
create unique index questionnaire_templates_version_uq on questionnaire_templates (tenant_id, key, version);
create unique index questionnaire_templates_one_draft on questionnaire_templates (tenant_id, key) where status = 'draft';
create unique index questionnaire_templates_one_published on questionnaire_templates (tenant_id, key) where status = 'published';
create index on questionnaire_templates (tenant_id, created_by);
create index on questionnaire_templates (tenant_id, published_by);
select enable_tenant_rls('questionnaire_templates');

create or replace function questionnaire_templates_immutable() returns trigger language plpgsql as $$
begin
  if old.status <> 'draft' then
    if new.definition is distinct from old.definition or new.name <> old.name or new.description <> old.description
       or new.key <> old.key or new.version <> old.version then
      raise exception 'la versione % del modello di questionario è pubblicata e non si può modificare', old.version;
    end if;
    if not (old.status = new.status or (old.status = 'published' and new.status = 'superseded')) then
      raise exception 'passaggio di stato del modello non ammesso: % -> %', old.status, new.status;
    end if;
  end if;
  return new;
end $$;
create trigger questionnaire_templates_immutable before update on questionnaire_templates
  for each row execute function questionnaire_templates_immutable();

create or replace function questionnaire_templates_no_delete() returns trigger language plpgsql as $$
begin
  if old.status <> 'draft' then
    raise exception 'la versione % del modello di questionario è pubblicata e non si può cancellare', old.version;
  end if;
  return old;
end $$;
create trigger questionnaire_templates_no_delete before delete on questionnaire_templates
  for each row when (pg_trigger_depth() = 0)
  execute function questionnaire_templates_no_delete();

-- ---------------------------------------------------------------------
-- Tipi di valutazione: team, ambito, modello, obbligatorietà, esiti, chi decide.
-- ---------------------------------------------------------------------
create table review_types (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  key text not null check (key ~ '^[a-z][a-z0-9_]{0,62}$'),
  name text not null check (length(name) between 1 and 120),
  description text not null default '',
  team_id uuid not null,
  scope text not null check (scope in ('request','impact')),
  template_key text not null,
  -- Obbligatoria: finché è aperta conta in valutazioni_obbligatorie_aperte (gate).
  mandatory boolean not null default true,
  -- Esiti ammessi: [{"key","label","polarity": positive|conditional|negative, "requires_prescriptions"}].
  outcomes jsonb not null,
  -- Chi decide l'esito: i membri del team oppure solo il responsabile.
  deciders text not null default 'team' check (deciders in ('team','lead')),
  -- Ruoli che compilano: i titolari del primo ruolo presente sull'impatto o sulla richiesta.
  owner_roles text[] not null default '{project_manager,demand_manager}',
  archived boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, team_id) references review_teams (tenant_id, id) on delete restrict
);
create unique index review_types_key_uq on review_types (tenant_id, key);
create index on review_types (tenant_id, team_id);
select enable_tenant_rls('review_types');

-- ---------------------------------------------------------------------
-- La valutazione: elemento "review" figlio della richiesta (ambito richiesta) o
-- dell'impatto (ambito impatto). Resta legata alla versione del modello con cui è nata.
-- ---------------------------------------------------------------------
create table ext_review (
  tenant_id uuid not null,
  element_id uuid not null,
  review_type_id uuid not null,
  team_id uuid not null,
  template_id uuid not null,
  request_id uuid not null,
  impact_id uuid,
  mandatory boolean not null,
  -- Momento del ciclo, mosso dalle azioni review_stage del processo della valutazione:
  -- compile (si compila), submitted (inviata, SLA in corso), exam (il team esamina),
  -- clarification (chiarimenti: si torna a compilare, SLA sospeso), concluded, cancelled.
  stage text not null default 'compile' check (stage in ('compile','submitted','exam','clarification','concluded','cancelled')),
  -- Risposte correnti (la storia completa è in questionnaire_answers): chiave -> {"v","by","at","src"}.
  answers jsonb not null default '{}',
  required_total integer not null default 0,
  required_missing integer not null default 0,
  submitted_at timestamptz,
  due_on date,
  paused_on date,
  reminded_at timestamptz,
  overdue_notified_at timestamptz,
  escalated_at timestamptz,
  outcome text,
  outcome_label text,
  outcome_polarity text check (outcome_polarity in ('positive','conditional','negative')),
  decision_note text not null default '',
  decided_at timestamptz,
  decided_by uuid,
  superseded_by uuid,
  opened_by_rule text,
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, review_type_id) references review_types (tenant_id, id) on delete restrict,
  foreign key (tenant_id, team_id) references review_teams (tenant_id, id) on delete restrict,
  foreign key (tenant_id, template_id) references questionnaire_templates (tenant_id, id) on delete restrict,
  foreign key (tenant_id, request_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, impact_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, decided_by) references members (tenant_id, id) on delete set null (decided_by),
  foreign key (tenant_id, superseded_by) references elements (tenant_id, id) on delete set null (superseded_by)
);
create index on ext_review (tenant_id, request_id);
create index on ext_review (tenant_id, impact_id);
create index on ext_review (tenant_id, review_type_id);
create index on ext_review (tenant_id, team_id, stage);
create index on ext_review (tenant_id, template_id);
create index on ext_review (tenant_id, decided_by);
create index on ext_review (tenant_id, superseded_by);
-- Il job degli SLA cerca solo le valutazioni con lo SLA in corso.
create index ext_review_sla_idx on ext_review (due_on) where stage in ('submitted','exam');
-- Una sola valutazione attiva per tipo e oggetto (richiesta o impatto).
create unique index ext_review_active_uq on ext_review (tenant_id, review_type_id, coalesce(impact_id, request_id))
  where superseded_by is null and stage <> 'cancelled';
select enable_tenant_rls('ext_review');

-- Storia delle risposte: chi ha risposto cosa e quando. Solo inserimenti.
create table questionnaire_answers (
  tenant_id uuid not null,
  id bigint generated always as identity,
  review_id uuid not null,
  question_key text not null,
  value jsonb,                               -- null = risposta cancellata
  source text not null default 'manual' check (source in ('manual','prefill')),
  answered_by uuid,
  answered_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, review_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, answered_by) references members (tenant_id, id) on delete set null (answered_by)
);
create index on questionnaire_answers (tenant_id, review_id, question_key, id);
create index on questionnaire_answers (tenant_id, answered_by);
select enable_tenant_rls('questionnaire_answers');

create or replace function questionnaire_answers_immutable() returns trigger language plpgsql as $$
begin
  raise exception 'la storia delle risposte non si modifica: si registra una nuova risposta';
end $$;
create trigger questionnaire_answers_no_update before update on questionnaire_answers
  for each row when (pg_trigger_depth() = 0)
  execute function questionnaire_answers_immutable();
create trigger questionnaire_answers_no_delete before delete on questionnaire_answers
  for each row when (pg_trigger_depth() = 0)
  execute function questionnaire_answers_immutable();

-- Sezioni assegnate alle persone.
create table questionnaire_assignments (
  tenant_id uuid not null,
  review_id uuid not null,
  section_key text not null,
  member_id uuid not null,
  assigned_by uuid,
  assigned_at timestamptz not null default now(),
  primary key (tenant_id, review_id, section_key),
  foreign key (tenant_id, review_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, assigned_by) references members (tenant_id, id) on delete set null (assigned_by)
);
create index on questionnaire_assignments (tenant_id, member_id);
create index on questionnaire_assignments (tenant_id, assigned_by);
select enable_tenant_rls('questionnaire_assignments');

-- Prescrizioni: proposte dalle regole di derivazione del modello o aggiunte a mano dal
-- team. Al passo 5 diventeranno requisiti.
create table review_prescriptions (
  tenant_id uuid not null,
  id uuid not null,
  review_id uuid not null,
  source text not null check (source in ('rule','manual')),
  rule_key text,
  text text not null check (length(text) between 1 and 2000),
  category text not null default '',
  status text not null default 'proposed' check (status in ('proposed','accepted','rejected')),
  created_by uuid,
  created_at timestamptz not null default now(),
  decided_by uuid,
  decided_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, review_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by),
  foreign key (tenant_id, decided_by) references members (tenant_id, id) on delete set null (decided_by)
);
create index on review_prescriptions (tenant_id, review_id);
create index on review_prescriptions (tenant_id, created_by);
create index on review_prescriptions (tenant_id, decided_by);
create unique index review_prescriptions_rule_uq on review_prescriptions (tenant_id, review_id, rule_key) where source = 'rule';
select enable_tenant_rls('review_prescriptions');
