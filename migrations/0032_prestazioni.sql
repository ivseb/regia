-- Indici per il benchmark esteso (docs/benchmark.md, 2026-11-24).

-- Voci aperte di uno spazio per tipo: epic della board, backlog, sprint.
create index if not exists elements_open_space_type_idx on elements (tenant_id, space_id, type_id)
  where deleted_at is null and not is_draft and resolved_at is null;

-- Intervalli sui campi personalizzati numerici e RLS. Gli operatori di confronto su numeric
-- non sono LEAKPROOF: con la RLS Postgres non può usarli come condizione dell'indice
-- (tenant, campo, v_num) e legge tutti i valori del campo (84 ms con 120.000 valori, mentre
-- la lista ne vuole 2.000). Come fts_match, la funzione gira come proprietario ma filtra sul
-- tenant della transazione e restituisce solo id: la query dell'app li rilegge con RLS.
create or replace function field_num_match(p_field uuid, p_lo numeric, p_lo_incl boolean, p_hi numeric, p_hi_incl boolean)
returns setof uuid
language plpgsql stable security definer set search_path = public, pg_temp as $$
declare
  t uuid := app_tenant();
  sql text;
begin
  if t is null then
    return;
  end if;
  sql := 'select v.element_id from element_values v where v.tenant_id = $1 and v.field_id = $2 and v.v_num is not null';
  if p_lo is not null then
    sql := sql || ' and v.v_num >= $3' || case when p_lo_incl then '' else ' and v.v_num <> $3' end;
  end if;
  if p_hi is not null then
    sql := sql || ' and v.v_num <= $4' || case when p_hi_incl then '' else ' and v.v_num <> $4' end;
  end if;
  return query execute sql using t, p_field, p_lo, p_hi;
end $$;
revoke all on function field_num_match(uuid, numeric, boolean, numeric, boolean) from public;
grant execute on function field_num_match(uuid, numeric, boolean, numeric, boolean) to mytrack_app;
