-- Linguaggio di query completo (piano, passo 16; architecture.md §6).

-- Ordinamento per inizio e fine pianificati e per titolo: indici nello stesso ordine della
-- query (valore, id), parziali sugli elementi vivi come gli altri indici delle liste.
-- Servono anche ai filtri "inizio:" e "fine:", risolti in id con l'indice.
create index elements_live_start_idx on elements (tenant_id, start_at, id)
  where deleted_at is null and not is_draft and start_at is not null;
create index elements_live_due_idx on elements (tenant_id, due_at, id)
  where deleted_at is null and not is_draft and due_at is not null;
create index elements_live_title_idx on elements (tenant_id, title, id)
  where deleted_at is null and not is_draft;

-- Ricerca per prefisso ("regis*", usata dalla palette dei comandi). Come fts_match
-- (migrazione 0006): gira come proprietario per usare l'indice GIN, filtra esplicitamente
-- sul tenant della transazione e restituisce solo id, che l'app rilegge con la RLS.
-- p_prefix è già una tsquery "parola:* & altra:*" composta dall'app con sole lettere e cifre.
create or replace function fts_match_prefix(p_query text, p_prefix text, p_limit int,
  p_types uuid[] default null, p_states uuid[] default null, p_spaces uuid[] default null, p_open boolean default null)
returns setof uuid
language plpgsql stable security definer set search_path = public, pg_temp as $$
declare
  t uuid := app_tenant();
  q text := 'to_tsquery(''simple'', unaccent($7))';
  sql text;
begin
  if t is null or coalesce(p_prefix, '') = '' then
    return;
  end if;
  if coalesce(p_query, '') <> '' then
    q := '(websearch_to_tsquery(''simple'', unaccent($2)) || websearch_to_tsquery(''italian'', unaccent($2))) && ' || q;
  end if;
  sql := 'select e.id from elements e where e.tenant_id = $1 and e.deleted_at is null and not e.is_draft'
      || ' and e.search @@ (' || q || ')';
  if p_types is not null then sql := sql || ' and e.type_id = any($3)'; end if;
  if p_states is not null then sql := sql || ' and e.state_id = any($4)'; end if;
  if p_spaces is not null then sql := sql || ' and e.space_id = any($5)'; end if;
  if p_open is not null then
    sql := sql || case when p_open then ' and e.resolved_at is null' else ' and e.resolved_at is not null' end;
  end if;
  sql := sql || ' limit $6';
  return query execute sql using t, p_query, p_types, p_states, p_spaces, least(greatest(p_limit, 1), 50000), p_prefix;
end $$;
revoke all on function fts_match_prefix(text, text, int, uuid[], uuid[], uuid[], boolean) from public;
grant execute on function fts_match_prefix(text, text, int, uuid[], uuid[], uuid[], boolean) to mytrack_app;

-- Filtri "scadenza:" e "desiderata:" (campi del modulo demand), risolti in id con l'indice.
create index ext_demand_deadline_idx on ext_demand (tenant_id, regulatory_deadline) where regulatory_deadline is not null;
create index ext_demand_desired_idx on ext_demand (tenant_id, desired_date) where desired_date is not null;
