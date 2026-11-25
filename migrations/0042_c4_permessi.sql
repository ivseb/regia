-- Permessi lungo la gerarchia dei gruppi (piano, passo 15 "Da decidere") e gruppi negli
-- organi di governance. I diagrammi C4 non richiedono modifiche allo schema: il tipo
-- c4_context/c4_container esiste già, e c4_component si aggiunge qui.

-- ---------------------------------------------------------------------
-- 1. Opzione del tenant
-- ---------------------------------------------------------------------
-- group_inheritance: i ruoli assegnati a un gruppo valgono anche per i membri dei sottogruppi
-- (a livello di tenant, sugli oggetti come stakeholder e sugli spazi). Attiva per i nuovi
-- tenant (anche senza riga: vale il default); i tenant esistenti restano com'erano.
alter table tenant_security add column group_inheritance boolean not null default false;
insert into tenant_security (tenant_id, mfa_policy, group_inheritance)
  select t.id, 'off', false from tenants t where not exists (select 1 from tenant_security s where s.tenant_id = t.id);
alter table tenant_security alter column group_inheritance set default true;

-- ---------------------------------------------------------------------
-- 2. Funzioni di calcolo (security invoker: girano sotto RLS, quindi per il tenant corrente)
-- ---------------------------------------------------------------------
create or replace function group_inheritance_on() returns boolean language sql stable as $$
  select coalesce((select group_inheritance from tenant_security limit 1), true)
$$;

-- Gruppi di un membro: quelli diretti e, con l'opzione attiva, tutti gli antenati
-- (un ruolo del gruppo padre vale per i membri del figlio).
create or replace function member_groups(p_member uuid) returns setof uuid language sql stable as $$
  with recursive on_(v) as (select group_inheritance_on()),
  g(id) as (
    select gm.group_id from group_members gm where gm.member_id = p_member
    union
    select p.parent_id from groups p join g on p.id = g.id
    where p.parent_id is not null and (select v from on_)
  ) select id from g
$$;

-- Membri di un gruppo: diretti e, con l'opzione attiva, dei sottogruppi a ogni livello.
create or replace function group_effective_members(p_group uuid) returns setof uuid language sql stable as $$
  with recursive on_(v) as (select group_inheritance_on()),
  g(id) as (
    select p_group
    union
    select c.id from groups c join g on c.parent_id = g.id where (select v from on_)
  ) select distinct gm.member_id from group_members gm join g on g.id = gm.group_id
$$;

-- Il membro è nel gruppo (direttamente o, con l'opzione, in un suo sottogruppo)?
create or replace function member_in_group(p_member uuid, p_group uuid) returns boolean language sql stable as $$
  select p_group in (select member_groups(p_member))
$$;

-- Permessi del membro sullo spazio: usa i gruppi effettivi.
create or replace function space_access(p_space uuid, p_member uuid) returns text[] language sql stable as $$
  select coalesce(array_agg(distinct p), '{}') from (
    select unnest(r.permissions) as p
    from role_assignments ra
    join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
    where ra.space_id = p_space
      and (ra.member_id = p_member or ra.group_id in (select member_groups(p_member)))
    union all
    select '*'
    from role_assignments ra
    join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
    where ra.space_id is null and '*' = any(r.permissions)
      and (ra.member_id = p_member or ra.group_id in (select member_groups(p_member)))
  ) x
$$;

-- MFA: l'amministratore può esserlo anche per un gruppo antenato. La funzione gira prima di
-- conoscere il tenant (security definer): legge l'opzione del tenant del membro.
create or replace function account_mfa_required(p_account uuid) returns boolean
language sql stable security definer set search_path = public, pg_temp as $$
  select exists (
    select 1
    from members m
    join tenants t on t.id = m.tenant_id and t.status = 'active'
    join tenant_security ts on ts.tenant_id = m.tenant_id
    where m.account_id = p_account and m.status = 'active'
      and (ts.mfa_policy = 'all' or (ts.mfa_policy = 'admins' and exists (
        select 1 from role_assignments ra
        join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
        where ra.tenant_id = m.tenant_id and ra.space_id is null
          and r.permissions && array['*', 'tenant.admin']
          and (ra.member_id = m.id or ra.group_id in (
            with recursive g(id) as (
              select gm.group_id from group_members gm where gm.tenant_id = m.tenant_id and gm.member_id = m.id
              union
              select p.parent_id from groups p join g on p.tenant_id = m.tenant_id and p.id = g.id
              where p.parent_id is not null and ts.group_inheritance
            ) select id from g)))))
  )
$$;

-- ---------------------------------------------------------------------
-- 3. Gruppi negli organi di governance
-- ---------------------------------------------------------------------
-- Un organo può avere gruppi tra i membri: i membri effettivi si calcolano al momento
-- (seduta, convocazioni, quorum). voting vale per tutti i membri del gruppo.
create table governance_body_groups (
  tenant_id uuid not null,
  body_id uuid not null,
  group_id uuid not null,
  voting boolean not null default true,
  created_at timestamptz not null default now(),
  primary key (tenant_id, body_id, group_id),
  foreign key (tenant_id, body_id) references governance_bodies (tenant_id, id) on delete cascade,
  foreign key (tenant_id, group_id) references groups (tenant_id, id) on delete cascade
);
create index on governance_body_groups (tenant_id, group_id);
select enable_tenant_rls('governance_body_groups');

-- Membri effettivi di un organo: diretti più quelli dei gruppi. Chi è dentro in più modi
-- vota se vota in almeno uno; via_group è il primo gruppo da cui arriva (null = diretto).
create or replace function body_effective_members(p_body uuid)
returns table (member_id uuid, voting boolean, via_group uuid) language sql stable as $$
  select x.member_id, bool_or(x.voting), (array_agg(x.via_group order by x.ord))[1]
  from (
    select bm.member_id, bm.voting, null::uuid as via_group, 0 as ord
    from governance_body_members bm where bm.body_id = p_body
    union all
    select m.member_id, bg.voting, bg.group_id, 1
    from governance_body_groups bg, lateral group_effective_members(bg.group_id) m(member_id)
    where bg.body_id = p_body
  ) x group by x.member_id
$$;

-- ---------------------------------------------------------------------
-- 4. Diagrammi C4: terzo livello (componenti)
-- ---------------------------------------------------------------------
alter table diagrams drop constraint diagrams_kind_check;
alter table diagrams add constraint diagrams_kind_check
  check (kind in ('c4_context','c4_container','c4_component','capability_map','dependency','archimate','free'));
