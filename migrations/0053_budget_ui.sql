-- Interfaccia del budget gerarchico (replica/budget.md §6, passo 4): il periodo di competenza
-- di una richiesta. L'unità richiedente c'è già (ext_demand.requesting_unit_id).
alter table ext_demand add column budget_period_id uuid;
alter table ext_demand add constraint ext_demand_budget_period_fk
  foreign key (tenant_id, budget_period_id) references budget_periods (tenant_id, id) on delete set null (budget_period_id);
create index on ext_demand (tenant_id, budget_period_id);
