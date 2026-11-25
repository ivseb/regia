-- Work management di base (piano, passi 10 e 18; replica/work.md).
-- Spazi dei team, epic/story/task/bug e sprint sono elementi del motore: qui servono solo
-- l'indice dell'ordinamento manuale, l'estensione dello sprint e la famiglia finanziaria
-- estesa a tutta la discendenza (le ore sui task diventano costi della richiesta).

-- Ordinamento manuale (board e backlog): indice frazionario per spazio, confrontato byte per
-- byte (collazione "C") così l'ordine non dipende dalla lingua del database.
create index elements_space_rank_idx on elements (tenant_id, space_id, (rank collate "C"), id)
  where deleted_at is null;

-- Spostare una card cambia solo il rank: non è una modifica dell'elemento, quindi "aggiornata"
-- (ordinamenti, liste, sincronizzazioni) non cambia. Il confronto completo si fa solo quando il
-- rank cambia davvero; ogni altra modifica aggiorna updated_at come prima.
create or replace function elements_set_updated_at() returns trigger language plpgsql as $$
begin
  if new.rank is distinct from old.rank
     and (to_jsonb(new) - 'rank' - 'updated_at' - 'search') = (to_jsonb(old) - 'rank' - 'updated_at' - 'search') then
    new.updated_at := old.updated_at;
  else
    new.updated_at := now();
  end if;
  return new;
end $$;
drop trigger elements_updated on elements;
create trigger elements_updated before update on elements for each row execute function elements_set_updated_at();

-- Sprint come elementi (architecture.md §12, punto 2): le date sono start_at e due_at
-- dell'elemento, l'appartenenza è la relazione "pianificato in". Qui l'obiettivo e ciò che
-- serve al burndown: l'impegno fotografato all'avvio e gli incompiuti spostati alla chiusura.
create table ext_sprint (
  tenant_id uuid not null,
  element_id uuid not null,
  goal text not null default '',
  started_at timestamptz,
  closed_at timestamptz,
  committed numeric check (committed >= 0),
  committed_unit text check (committed_unit in ('points', 'items')),
  carried_over uuid[] not null default '{}',
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
);
select enable_tenant_rls('ext_sprint');

-- Le ore e i costi di una richiesta comprendono ora tutta la discendenza: impatto → epic del
-- team → story → task (prima solo i figli diretti). union (non union all) scarta i doppioni
-- e ferma la ricorsione anche con dati incoerenti.
create or replace function finance_family(p uuid) returns table (id uuid) language sql stable as $$
  with recursive fam(id) as (
    select p
    union select x.element_id from ext_impact x where x.request_id = p
    union select e.id from elements e join fam f on e.parent_id = f.id where e.deleted_at is null
  )
  select id from fam
$$;
