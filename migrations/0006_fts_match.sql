-- Ricerca full-text e RLS (benchmark del 2026-11-24).
-- L'operatore @@ non è LEAKPROOF: con la RLS attiva Postgres non può usarlo in un indice
-- prima della policy del tenant, quindi ignora l'indice GIN e filtra tutte le righe del
-- tenant (3,5 s su 1M elementi per un termine assente). Questa funzione gira come
-- proprietario (fuori dalla RLS) ma filtra esplicitamente sul tenant della transazione,
-- e restituisce solo id: la query dell'app li rilegge con RLS, visibilità e altri filtri.
-- La query è composta con i soli filtri presenti (tipi, stati, spazi, aperti/chiusi) ed
-- eseguita con EXECUTE: ogni chiamata ha il suo piano, e Postgres può combinare l'indice
-- GIN con quelli btree in un'unica scansione bitmap.
create or replace function fts_match(p_query text, p_limit int,
  p_types uuid[] default null, p_states uuid[] default null, p_spaces uuid[] default null, p_open boolean default null)
returns setof uuid
language plpgsql stable security definer set search_path = public, pg_temp as $$
declare
  t uuid := app_tenant();
  sql text;
begin
  if t is null then
    return;
  end if;
  sql := 'select e.id from elements e where e.tenant_id = $1 and e.deleted_at is null and not e.is_draft'
      || ' and e.search @@ (websearch_to_tsquery(''simple'', unaccent($2)) || websearch_to_tsquery(''italian'', unaccent($2)))';
  if p_types is not null then sql := sql || ' and e.type_id = any($3)'; end if;
  if p_states is not null then sql := sql || ' and e.state_id = any($4)'; end if;
  if p_spaces is not null then sql := sql || ' and e.space_id = any($5)'; end if;
  if p_open is not null then
    sql := sql || case when p_open then ' and e.resolved_at is null' else ' and e.resolved_at is not null' end;
  end if;
  sql := sql || ' limit $6';
  return query execute sql using t, p_query, p_types, p_states, p_spaces, least(greatest(p_limit, 1), 50000);
end $$;
revoke all on function fts_match(text, int, uuid[], uuid[], uuid[], boolean) from public;
grant execute on function fts_match(text, int, uuid[], uuid[], uuid[], boolean) to mytrack_app;
