-- Viste salvate (piano, passo 16): query, colonne, ordinamento, raggruppamento e modalità di
-- una lista, personali o condivise con un gruppo o con tutto il tenant, con preferiti nel
-- menu e vista predefinita per pagina (per utente e per tenant).
-- Le viste di sistema (board dei team, system_key valorizzato) non hanno pagina e restano
-- fuori da questo elenco.

alter table views add column page text check (page ~ '^[a-z0-9_-]{1,40}$');   -- 'richieste', 'applicazioni'...
alter table views add column scope text not null default 'private' check (scope in ('private', 'group', 'tenant'));
alter table views add column group_id uuid;
alter table views add constraint views_group_fk
  foreign key (tenant_id, group_id) references groups (tenant_id, id) on delete cascade;
alter table views add constraint views_scope_group_ck check ((scope = 'group') = (group_id is not null));
-- settings (JSON): cols (colonne), widths (larghezze), group (raggruppamento), mode (lista|tabella).

create index views_page_idx on views (tenant_id, page) where page is not null;
create index views_group_idx on views (tenant_id, group_id) where group_id is not null;

-- Preferiti: compaiono nel menu laterale, con il contatore dei risultati.
create table view_favorites (
  tenant_id uuid not null,
  member_id uuid not null,
  view_id uuid not null,
  position integer not null default 0,
  created_at timestamptz not null default now(),
  primary key (tenant_id, member_id, view_id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, view_id) references views (tenant_id, id) on delete cascade
);
select enable_tenant_rls('view_favorites');

-- Vista predefinita di una pagina: member_id nullo = predefinita del tenant.
create table view_defaults (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  page text not null check (page ~ '^[a-z0-9_-]{1,40}$'),
  member_id uuid,
  view_id uuid not null,
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, view_id) references views (tenant_id, id) on delete cascade
);
create unique index view_defaults_uq on view_defaults (tenant_id, page, coalesce(member_id, '00000000-0000-0000-0000-000000000000'::uuid));
create index on view_defaults (tenant_id, view_id);
select enable_tenant_rls('view_defaults');
