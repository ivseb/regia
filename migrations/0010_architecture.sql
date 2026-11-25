-- Asset logici, applicazioni e impatti (sdlc.md §3-4; piano, passo 2).
-- Asset logico e applicazione sono elementi dello spazio di architettura; l'impatto è
-- un elemento figlio della business request, uno per applicazione toccata.

-- Applicazione: codice del catalogo (CMDB) per gli import, team owner.
alter table ext_application add column code text;
alter table ext_application add column owner_team_id uuid;
alter table ext_application add constraint ext_application_team_fk
  foreign key (tenant_id, owner_team_id) references teams (tenant_id, id) on delete set null (owner_team_id);
create unique index ext_application_code_uq on ext_application (tenant_id, lower(code)) where code is not null;
create index on ext_application (tenant_id, owner_team_id);
-- Il ciclo di vita lo decide il processo del tipo "application": la colonna resta per compatibilità.
comment on column ext_application.lifecycle is 'non usata: il ciclo di vita è la fase del processo';

-- Asset logico: ciò che il business conosce e cita (prodotto, servizio, canale...).
create table ext_logical_asset (
  tenant_id uuid not null,
  element_id uuid not null,
  kind text not null default 'product' check (kind in ('product','service','channel','capability','other')),
  owner_unit_id uuid,
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, owner_unit_id) references org_units (tenant_id, id) on delete set null (owner_unit_id)
);
create index on ext_logical_asset (tenant_id, owner_unit_id);
select enable_tenant_rls('ext_logical_asset');

-- Impatto: perimetro di un team su una business request per una applicazione.
create table ext_impact (
  tenant_id uuid not null,
  element_id uuid not null,
  request_id uuid not null,
  application_id uuid not null,
  team_id uuid,
  change_kind text not null default 'change' check (change_kind in ('new','change','config','decommission','none')),
  technical_note text not null default '',
  estimate_effort_days numeric check (estimate_effort_days >= 0),
  estimate_cost_cents bigint check (estimate_cost_cents >= 0),
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, request_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, application_id) references elements (tenant_id, id) on delete restrict,
  foreign key (tenant_id, team_id) references teams (tenant_id, id) on delete set null (team_id)
);
-- Un impatto per applicazione e richiesta.
create unique index ext_impact_request_app_uq on ext_impact (tenant_id, request_id, application_id);
create index on ext_impact (tenant_id, application_id);
create index on ext_impact (tenant_id, team_id);
select enable_tenant_rls('ext_impact');
