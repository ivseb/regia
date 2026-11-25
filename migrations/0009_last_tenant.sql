-- Al login si rientra nell'ultimo tenant usato (prima era il primo in ordine di nome,
-- spesso vuoto per chi lavora su più clienti). Si aggiorna al cambio di tenant.
alter table accounts add column last_tenant_id uuid references tenants(id) on delete set null;
