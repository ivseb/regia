-- Ordinamento per numero sull'intero tenant ("ordina per: numero"), benchmark del 2026-11-24.
create index elements_live_number_idx on elements (tenant_id, number, id)
  where deleted_at is null and not is_draft;
