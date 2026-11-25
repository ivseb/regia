-- Indici per le liste ordinate (benchmark del 2026-11-24 con 1M elementi per tenant).
-- Le liste ordinano per (updated_at, id) e paginano a cursore: servono indici che
-- abbiano quell'ordine dopo le colonne di filtro, così la pagina si legge senza ordinare.
-- Stesso predicato parziale delle query (deleted_at is null and not is_draft).
-- Lo stato implica il tipo (ogni tipo ha il suo insieme di stati): basta (state_id, ...).

create index elements_live_updated_idx on elements (tenant_id, updated_at, id)
  where deleted_at is null and not is_draft;
create index elements_type_updated_idx on elements (tenant_id, type_id, updated_at, id)
  where deleted_at is null and not is_draft;
create index elements_state_updated_idx on elements (tenant_id, state_id, updated_at, id)
  where deleted_at is null and not is_draft;

-- Sostituiti dai nuovi (stesse colonne iniziali).
drop index if exists elements_type_state_idx;
drop index if exists elements_tenant_id_state_id_idx;

-- Inbox delle approvazioni: richieste aperte in ordine di arrivo.
create index approval_requests_pending_at_idx on approval_requests (tenant_id, requested_at)
  where status = 'pending';
