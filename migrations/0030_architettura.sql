-- Architettura enterprise (piano, passo 20; replica/architettura-ea.md): capability di
-- business, dipendenze tra applicazioni con attributi, tecnologie con fine supporto,
-- valutazione TIME, impostazioni del grafo e diagrammi salvati come viste del grafo.

-- Capability di business: elemento dello spazio di architettura. La gerarchia usa
-- elements.parent_id (una capability figlia ha per genitore la capability che la contiene).
create table ext_capability (
  tenant_id uuid not null,
  element_id uuid not null,
  code text,
  criticality text check (criticality in ('low','medium','high','critical')),
  owner_unit_id uuid,
  position integer not null default 0,
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, owner_unit_id) references org_units (tenant_id, id) on delete set null (owner_unit_id)
);
create unique index ext_capability_code_uq on ext_capability (tenant_id, lower(code)) where code is not null;
create index on ext_capability (tenant_id, owner_unit_id);
select enable_tenant_rls('ext_capability');

-- Tipi di relazione dell'architettura.
-- impact_direction dice chi dipende da chi, per l'analisi d'impatto e per i fatti:
--   forward = l'origine dipende dalla destinazione (A chiama le API di B: A dipende da B);
--   reverse = la destinazione dipende dall'origine (A supporta la capability C: C dipende da A);
--   none    = la relazione non propaga impatti.
-- ea_category raggruppa i tipi nell'interfaccia: dipendenze tra applicazioni, collegamenti
-- alle capability, composizione degli asset. null = tipo che l'architettura non gestisce.
alter table relation_types add column impact_direction text not null default 'forward'
  check (impact_direction in ('forward','reverse','none'));
alter table relation_types add column ea_category text check (ea_category in ('dependency','capability','composition'));
alter table relation_types add column archived boolean not null default false;
create index relation_types_ea_idx on relation_types (tenant_id, ea_category) where ea_category is not null;

-- Tecnologie (linguaggi, framework, prodotti, database) con le date di fine supporto.
create table technologies (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null check (length(name) between 1 and 120),
  version text not null default '' check (length(version) <= 60),
  vendor text not null default '' check (length(vendor) <= 120),
  category text not null default '' check (length(category) <= 60),
  end_of_support date,                      -- fine del supporto ordinario (patch, aggiornamenti)
  end_of_life date,                         -- fine vita: nessun supporto, nemmeno esteso
  notes text not null default '',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id)
);
create unique index technologies_name_uq on technologies (tenant_id, lower(name), lower(version));
create index on technologies (tenant_id, end_of_support);
select enable_tenant_rls('technologies');

create table application_technologies (
  tenant_id uuid not null,
  application_id uuid not null,
  technology_id uuid not null,
  primary key (tenant_id, application_id, technology_id),
  foreign key (tenant_id, application_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, technology_id) references technologies (tenant_id, id) on delete cascade
);
create index on application_technologies (tenant_id, technology_id);
select enable_tenant_rls('application_technologies');

-- Motivazione della valutazione TIME (la valutazione è ext_application.time_rating).
alter table ext_application add column time_note text not null default '';
alter table ext_application add column time_rated_at timestamptz;

-- Impostazioni dell'architettura per tenant: tipi che sono nodi del grafo, preavviso
-- sulla fine supporto, soglia dei duplicati di capability.
create table ea_settings (
  tenant_id uuid primary key references tenants(id) on delete cascade,
  node_types text[] not null default '{application,logical_asset,capability,ict_provider}',
  eol_warning_days integer not null default 180 check (eol_warning_days between 0 and 3650),
  duplicate_min integer not null default 2 check (duplicate_min between 2 and 50),
  updated_at timestamptz not null default now()
);
select enable_tenant_rls('ea_settings');

-- Diagrammi: viste salvate del grafo. layout = nodi (id di elementi) con le posizioni
-- fissate dall'utente e annotazioni; nomi, dati e archi si leggono sempre dal grafo.
alter table diagrams add column description text not null default '';
alter table diagrams add column version integer not null default 1;
alter table diagrams add column created_by uuid;
alter table diagrams add column updated_by uuid;
alter table diagrams add constraint diagrams_created_by_fk
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by);
alter table diagrams add constraint diagrams_updated_by_fk
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by);
create index on diagrams (tenant_id, created_by);
create index on diagrams (tenant_id, updated_by);
