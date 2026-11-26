-- Ore dal timesheet via CSV (target «work_logs» dei profili di import, già ammesso da 0031).
-- Ogni ora importata ricorda l'esecuzione che l'ha creata (per annullarla) e un riferimento
-- della riga (numero del timesheet o impronta dei dati): reimportare lo stesso file non duplica.
alter table work_logs add column import_run_id uuid;
alter table work_logs add column source_ref text;
alter table work_logs add constraint work_logs_import_run_fk
  foreign key (tenant_id, import_run_id) references import_runs (tenant_id, id) on delete set null (import_run_id);
create index on work_logs (tenant_id, import_run_id);
create unique index work_logs_source_uq on work_logs (tenant_id, source_ref) where source_ref is not null;
