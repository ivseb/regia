-- Motore di processo configurabile (replica/sdlc.md §1, §8; piano, passo 1).
-- Un processo governa un tipo di elemento; più processi per lo stesso tipo si
-- distinguono per priorità e condizione di applicabilità (unità, driver, importo...).
-- Le versioni pubblicate sono immutabili: ogni elemento resta sulla versione con cui
-- è partito, finché non viene migrato esplicitamente.

create table process_definitions (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  key text not null check (key ~ '^[a-z][a-z0-9_]{0,62}$'),
  name text not null,
  description text not null default '',
  type_id uuid not null,
  priority integer not null default 100,     -- più basso = valutato prima
  applies_when text not null default '',     -- condizione; vuota = sempre
  archived boolean not null default false,
  created_by uuid,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, type_id) references element_types (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create unique index process_definitions_key_uq on process_definitions (tenant_id, key);
create index on process_definitions (tenant_id, type_id, priority) where not archived;
select enable_tenant_rls('process_definitions');

create table process_versions (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  process_id uuid not null,
  number integer not null check (number > 0),
  status text not null default 'draft' check (status in ('draft','published','superseded')),
  definition jsonb not null,
  notes text not null default '',
  created_by uuid,
  created_at timestamptz not null default now(),
  published_by uuid,
  published_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, process_id) references process_definitions (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by),
  foreign key (tenant_id, published_by) references members (tenant_id, id) on delete set null (published_by)
);
create unique index process_versions_number_uq on process_versions (tenant_id, process_id, number);
-- Al massimo una bozza e una versione corrente per processo.
create unique index process_versions_one_draft on process_versions (tenant_id, process_id) where status = 'draft';
create unique index process_versions_one_published on process_versions (tenant_id, process_id) where status = 'published';
select enable_tenant_rls('process_versions');

-- Immutabilità: dopo la pubblicazione cambia solo lo stato (published -> superseded).
create or replace function process_versions_immutable() returns trigger language plpgsql as $$
begin
  if old.status <> 'draft' then
    if new.definition is distinct from old.definition or new.number <> old.number
       or new.process_id <> old.process_id or new.notes is distinct from old.notes then
      raise exception 'la versione % del processo è pubblicata e non si può modificare', old.number;
    end if;
    if not (old.status = new.status or (old.status = 'published' and new.status = 'superseded')) then
      raise exception 'passaggio di stato della versione non ammesso: % -> %', old.status, new.status;
    end if;
  end if;
  return new;
end $$;
create trigger process_versions_immutable before update on process_versions
  for each row execute function process_versions_immutable();

create or replace function process_versions_no_delete() returns trigger language plpgsql as $$
begin
  if old.status <> 'draft' then
    raise exception 'la versione % del processo è pubblicata e non si può cancellare', old.number;
  end if;
  return old;
end $$;
create trigger process_versions_no_delete before delete on process_versions
  for each row when (pg_trigger_depth() = 0)
  execute function process_versions_no_delete();

-- Versione del processo su cui si trova l'elemento (null = tipo senza processo).
alter table elements add column process_version_id uuid;
alter table elements add constraint elements_process_version_fk
  foreign key (tenant_id, process_version_id) references process_versions (tenant_id, id) on delete restrict;
create index elements_process_version_idx on elements (tenant_id, process_version_id, state_id)
  where process_version_id is not null and deleted_at is null;
