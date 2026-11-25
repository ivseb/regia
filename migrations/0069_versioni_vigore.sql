-- «Tutto ciò che si configura evolve nel tempo»: data di entrata in vigore e di fine delle versioni
-- dei processi, regola di transizione e guide allegate (ondata versioni).
--
--  * effective_from: da quando la versione è quella «in vigore» per le richieste nuove (le versioni
--    esistenti valgono dalla pubblicazione); una data futura dà lo stato «scheduled» (programmata).
--  * effective_to: data di fine facoltativa, modificabile anche dopo la pubblicazione.
--  * deprecated_at: da quando una versione è stata sostituita da una più nuova (entrata in vigore).
--  * transition: regola di transizione decisa dall'amministratore, json
--      {"default": "resta", "phases": {"<fase>": "auto" | "resta" | "manuale"}}
--    usata all'arrivo della data per migrare le richieste ancora in fase iniziale.
alter table process_versions
  add column effective_from timestamptz,
  add column effective_to timestamptz,
  add column deprecated_at timestamptz,
  add column transition jsonb;

alter table process_versions drop constraint process_versions_status_check;
alter table process_versions add constraint process_versions_status_check
  check (status in ('draft','scheduled','published','superseded'));

-- Le versioni già pubblicate valgono dalla pubblicazione; le sostituite sono deprecate da quando
-- è entrata in vigore la successiva.
update process_versions set effective_from = published_at where published_at is not null;
update process_versions v set deprecated_at = (
    select min(n.effective_from) from process_versions n
    where n.tenant_id = v.tenant_id and n.process_id = v.process_id and n.number > v.number and n.status <> 'draft')
  where v.status = 'superseded';

-- Al massimo una versione programmata per processo.
create unique index process_versions_one_scheduled on process_versions (tenant_id, process_id) where status = 'scheduled';

create or replace function process_versions_immutable() returns trigger language plpgsql as $$
begin
  if old.status <> 'draft' then
    if new.definition is distinct from old.definition or new.number <> old.number
       or new.process_id <> old.process_id or new.notes is distinct from old.notes then
      raise exception 'la versione % del processo è pubblicata e non si può modificare', old.number;
    end if;
    if not (old.status = new.status
            or (old.status = 'published' and new.status = 'superseded')
            or (old.status = 'scheduled' and new.status in ('published', 'superseded'))) then
      raise exception 'passaggio di stato della versione non ammesso: % -> %', old.status, new.status;
    end if;
  end if;
  return new;
end $$;

-- Guide allegate a un processo (phase null) o a una sua fase: un documento, un link o una pagina Knowledge,
-- visibili a chi lavora sulla richiesta.
create table process_guides (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  process_id uuid not null,
  phase text,                               -- null = guida dell'intero processo
  kind text not null check (kind in ('document', 'link', 'knowledge')),
  title text not null check (length(title) between 1 and 200),
  target text not null check (length(target) between 1 and 2000), -- indirizzo, oppure chiave della pagina Knowledge
  position integer not null default 0,
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, process_id) references process_definitions (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on process_guides (tenant_id, process_id, phase, position);
create index on process_guides (tenant_id, created_by);
select enable_tenant_rls('process_guides');
