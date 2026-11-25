-- Tipi di elemento configurabili per tenant: nome plurale, descrizione, nomi per lingua e
-- archiviazione. La RLS di element_types resta quella della migrazione 0001.
alter table element_types add column if not exists plural_name text;
alter table element_types add column if not exists description text not null default '';
alter table element_types add column if not exists labels jsonb not null default '{}'
  check (jsonb_typeof(labels) = 'object');
alter table element_types add column if not exists archived_at timestamptz;

-- Il tipo «page» si chiamava «Pagina»; nell'interfaccia è «documento» (replica/design.md,
-- «Vocabolario e lingua»). Si cambia solo dove il nome è ancora quello di origine.
update element_types set name = 'Documento' where key = 'page' and name = 'Pagina';

-- Plurali dei tipi di sistema (restano modificabili): servono a ⌘K, ai filtri e alle liste.
-- Una funzione li assegna anche ai tipi creati dopo, dai pacchetti dei moduli.
create or replace function default_type_plural(k text) returns text language sql immutable as $$
  select case k
    when 'business_request' then 'Richieste'
    when 'impact' then 'Impatti'
    when 'application' then 'Applicazioni'
    when 'logical_asset' then 'Asset logici'
    when 'page' then 'Documenti'
    when 'capability' then 'Capability di business'
    when 'risk' then 'Rischi'
    when 'incident' then 'Incidenti'
    when 'ict_provider' then 'Fornitori ICT'
    when 'control' then 'Controlli'
    when 'evidence' then 'Evidenze'
    when 'requirement' then 'Requisiti'
    when 'review' then 'Valutazioni'
    when 'epic' then 'Epic'
    when 'story' then 'Story'
    when 'task' then 'Task'
    when 'bug' then 'Bug'
    when 'sprint' then 'Sprint'
    else null end
$$;

create or replace function element_types_plural() returns trigger language plpgsql as $$
begin
  if new.plural_name is null and new.is_system then
    new.plural_name := default_type_plural(new.key);
  end if;
  return new;
end $$;

drop trigger if exists element_types_plural_tg on element_types;
create trigger element_types_plural_tg before insert on element_types
  for each row execute function element_types_plural();

update element_types set plural_name = default_type_plural(key) where plural_name is null and is_system;
