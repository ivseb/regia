-- Ondata 3, proposta 9 «Cabina di portafoglio»: gli scenari. Uno scenario è una linea di taglio della
-- graduatoria con un nome: quante richieste stanno sopra la linea e con quale budget. Si confrontano
-- affiancati (budget residuo, team in sovraccarico, richieste dentro e fuori). item_keys è la fotografia
-- delle richieste sopra la linea al momento del salvataggio, per ritrovarle anche se la graduatoria cambia.
create table portfolio_scenarios (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  name text not null check (length(name) between 1 and 80),
  cut integer not null check (cut between 0 and 2000),
  budget_cents bigint check (budget_cents >= 0),
  item_keys text[] not null default '{}',
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create unique index portfolio_scenarios_name_uq on portfolio_scenarios (tenant_id, lower(name));
create index on portfolio_scenarios (tenant_id, created_by);
select enable_tenant_rls('portfolio_scenarios');
