-- Identità aziendale: ingressi e gruppi governati dall'IdP (OIDC completo e SCIM 2.0).
-- Guida in docs/identita-aziendale.md, note di sviluppo in docs/sviluppo/identita-aziendale.md.

-- =====================================================================
-- 1. Impostazioni dell'organizzazione
-- =====================================================================
create table tenant_idp_settings (
  tenant_id uuid primary key references tenants(id) on delete cascade,
  -- Creazione del membro al primo accesso con l'OIDC dell'istanza.
  provisioning boolean not null default false,
  -- Domini email ammessi alla creazione (minuscoli, senza @). Senza dominio ammesso serve
  -- almeno un gruppo dell'IdP mappato in questa organizzazione.
  allowed_domains text[] not null default '{}',
  -- Nome del claim dell'ID token con i gruppi.
  groups_claim text not null default 'groups' check (groups_claim ~ '^[A-Za-z0-9_:./-]{1,100}$'),
  updated_at timestamptz not null default now(),
  updated_by uuid,
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
select enable_tenant_rls('tenant_idp_settings');

-- =====================================================================
-- 2. Gruppi dell'IdP e mappatura esplicita verso i gruppi di Regia
-- =====================================================================
-- external_id è il valore con cui l'IdP nomina il gruppo: il valore del claim (nome o
-- object ID) o, con SCIM, l'externalId (altrimenti il displayName).
create table idp_groups (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  external_id text not null check (length(external_id) between 1 and 300),
  display_name text not null default '',
  source text not null check (source in ('scim', 'oidc', 'manual')),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id)
);
create unique index idp_groups_external_uq on idp_groups (tenant_id, lower(external_id));
select enable_tenant_rls('idp_groups');

create table idp_group_members (
  tenant_id uuid not null,
  idp_group_id uuid not null,
  member_id uuid not null,
  primary key (tenant_id, idp_group_id, member_id),
  foreign key (tenant_id, idp_group_id) references idp_groups (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on idp_group_members (tenant_id, member_id);
select enable_tenant_rls('idp_group_members');

-- Un gruppo di Regia che è destinazione di almeno una mappatura è «governato dall'IdP»:
-- la sua appartenenza la decide solo l'IdP.
create table idp_group_mappings (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  idp_group_id uuid not null,
  group_id uuid not null,
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  unique (tenant_id, idp_group_id, group_id),
  foreign key (tenant_id, idp_group_id) references idp_groups (tenant_id, id) on delete cascade,
  foreign key (tenant_id, group_id) references groups (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on idp_group_mappings (tenant_id, group_id);
select enable_tenant_rls('idp_group_mappings');

-- =====================================================================
-- 3. Membri: origine, identità SCIM e chi ha deciso la sospensione
-- =====================================================================
alter table members add column origin text not null default 'admin' check (origin in ('admin', 'oidc', 'scim'));
alter table members add column scim_external_id text;
alter table members add column scim_user_name text;
-- Lo stato sospeso è uno solo (status = 'suspended'); qui si ricorda chi l'ha deciso.
alter table members add column suspended_source text check (suspended_source in ('admin', 'idp'));
update members set suspended_source = 'admin' where status = 'suspended';
alter table members add constraint members_suspended_source_ck
  check ((status = 'suspended') = (suspended_source is not null));
create unique index members_scim_user_uq on members (tenant_id, lower(scim_user_name)) where scim_user_name is not null;

-- L'anonimizzazione toglie anche gli identificativi dell'IdP.
create or replace function members_idp_anonymize() returns trigger language plpgsql as $$
begin
  if new.status = 'anonymized' and old.status is distinct from 'anonymized' then
    new.scim_external_id := null;
    new.scim_user_name := null;
  end if;
  return new;
end $$;
create trigger members_idp_anonymize before update of status on members
  for each row execute function members_idp_anonymize();

-- =====================================================================
-- 4. Riallineamento dei gruppi governati dall'IdP per un membro
-- =====================================================================
-- Il membro sta in un gruppo governato se e solo se sta in un gruppo dell'IdP mappato su di
-- esso. Gira sotto RLS nel tenant della transazione; restituisce i gruppi aggiunti e tolti.
create or replace function idp_reconcile(p_member uuid)
returns table (group_id uuid, added boolean)
language plpgsql as $$
begin
  return query
  with desired as (
    select distinct mp.group_id from idp_group_mappings mp
    join idp_group_members igm on igm.tenant_id = mp.tenant_id and igm.idp_group_id = mp.idp_group_id
    where igm.member_id = p_member
  ), removed as (
    delete from group_members gm
    where gm.member_id = p_member
      and gm.group_id in (select mp.group_id from idp_group_mappings mp)
      and gm.group_id not in (select d.group_id from desired d)
    returning gm.group_id
  ), inserted as (
    insert into group_members (tenant_id, group_id, member_id)
    select app_tenant(), d.group_id, p_member from desired d
    where not exists (select 1 from group_members x where x.group_id = d.group_id and x.member_id = p_member)
    returning group_members.group_id
  )
  select r.group_id, false from removed r
  union all
  select i.group_id, true from inserted i;
end $$;

-- =====================================================================
-- 5. Creazione al primo accesso: organizzazioni candidate
-- =====================================================================
-- Serve al login OIDC, prima di sapere il tenant: espone solo la configurazione delle
-- organizzazioni attive che hanno acceso la creazione, con le chiavi dei gruppi mappati.
create or replace function idp_provisioning_targets()
returns table (tenant_id uuid, allowed_domains text[], groups_claim text, mapped_keys text[])
language sql stable security definer set search_path = public, pg_temp as $$
  select s.tenant_id, s.allowed_domains, s.groups_claim,
         coalesce((select array_agg(distinct lower(g.external_id)) from idp_group_mappings mp
                   join idp_groups g on g.tenant_id = mp.tenant_id and g.id = mp.idp_group_id
                   where mp.tenant_id = s.tenant_id), '{}')
  from tenant_idp_settings s join tenants t on t.id = s.tenant_id
  where s.provisioning and t.status = 'active'
$$;
revoke all on function idp_provisioning_targets() from public;
grant execute on function idp_provisioning_targets() to mytrack_app;
