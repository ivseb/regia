-- Catalogo del portale (replica/usabilita/lavoro.md §1): le voci che il richiedente vede come carte,
-- ognuna con il suo modulo a passi (pubblicato e bozza di modifica) e le regole di instradamento.

create table portal_entries (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  key text not null check (key ~ '^[a-z][a-z0-9_-]{0,62}$'),
  label text not null check (length(label) between 1 and 80),
  description text not null default '' check (length(description) <= 400),
  category text not null default '' check (length(category) <= 60),
  icon text not null default 'doc' check (length(icon) <= 30),
  type_key text not null,
  position integer not null default 0,
  active boolean not null default true,
  -- Visibilità: se indicato, la voce la vedono solo i membri del gruppo (e dei suoi sotto-gruppi).
  group_id uuid,
  -- Modulo pubblicato: {"intro","title_label","desc_label","steps":[{"preset","title","intro","fields":[{"key","std","label","help","required","visible_when"}]}],
  --                    "routes":[{"when","role","team_id","group_id","priority","label"}]}
  form jsonb not null default '{}',
  -- Modifiche non ancora pubblicate (nulla se non ce ne sono).
  draft jsonb,
  published_at timestamptz,
  updated_by uuid,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, group_id) references groups (tenant_id, id) on delete set null (group_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create unique index portal_entries_key_uq on portal_entries (tenant_id, key);
create index on portal_entries (tenant_id, position);
create index on portal_entries (tenant_id, group_id);
create index on portal_entries (tenant_id, updated_by);
select enable_tenant_rls('portal_entries');

-- Una sola bozza per persona e voce: la voce di un tenant senza catalogo è la chiave del tipo.
alter table portal_drafts add column entry_key text;
update portal_drafts set entry_key = type_key;
alter table portal_drafts alter column entry_key set not null;
delete from portal_drafts d using portal_drafts n
  where d.tenant_id = n.tenant_id and d.member_id = n.member_id and d.entry_key = n.entry_key
    and (d.updated_at, d.id) < (n.updated_at, n.id);
create unique index portal_drafts_entry_uq on portal_drafts (tenant_id, member_id, entry_key);
