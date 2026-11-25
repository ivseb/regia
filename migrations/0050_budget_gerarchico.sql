-- Budget gerarchico (replica/budget.md, passi 1-3): organizzazione con ruoli nell'unità,
-- periodi di budget, stanziamenti a cascata con registro dei movimenti immutabile,
-- fine periodo con politiche, trasferimenti tra nodi, visibilità per area con la RLS.

-- ---------------------------------------------------------------------
-- 1. Impostazioni del tenant (budget.md §7), con i predefiniti
-- ---------------------------------------------------------------------
alter table finance_settings add column period_granularity text not null default 'year'
  check (period_granularity in ('year','semester','quarter','month'));
alter table finance_settings add column end_policy text not null default 'return'
  check (end_policy in ('return','carry','lapse'));
alter table finance_settings add column transfer_threshold_cents bigint check (transfer_threshold_cents > 0);  -- null = nessuna soglia
alter table finance_settings add column transfer_body_kind text not null default 'investment_committee'
  check (transfer_body_kind in ('investment_committee','steering','architecture_board','cab','other'));
alter table finance_settings add column free_allocation boolean not null default true;
alter table finance_settings add column warn_days integer not null default 30 check (warn_days between 0 and 365);

-- ---------------------------------------------------------------------
-- 2. Organizzazione: persone nelle unità, con il ruolo
-- ---------------------------------------------------------------------
create table org_unit_members (
  tenant_id uuid not null references tenants(id) on delete cascade,
  org_unit_id uuid not null,
  member_id uuid not null,
  role text not null default 'member' check (role in ('manager','finance_ref','member')),
  created_at timestamptz not null default now(),
  primary key (tenant_id, org_unit_id, member_id),
  foreign key (tenant_id, org_unit_id) references org_units (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on org_unit_members (tenant_id, member_id);
select enable_tenant_rls('org_unit_members');

-- Un'unità non può essere sotto sé stessa né sotto un suo discendente.
create or replace function org_units_no_cycle() returns trigger language plpgsql as $$
begin
  if new.parent_id is null then return new; end if;
  if new.parent_id = new.id then
    raise exception 'un''unità non può essere figlia di sé stessa' using errcode = '23514';
  end if;
  if exists (
    with recursive up(id, parent_id) as (
      select o.id, o.parent_id from org_units o where o.tenant_id = new.tenant_id and o.id = new.parent_id
      union
      select o.id, o.parent_id from org_units o join up on o.id = up.parent_id where o.tenant_id = new.tenant_id
    ) select 1 from up where id = new.id) then
    raise exception 'lo spostamento creerebbe un ciclo nell''organizzazione' using errcode = '23514';
  end if;
  return new;
end $$;
create trigger org_units_no_cycle before insert or update of parent_id on org_units
  for each row execute function org_units_no_cycle();

-- Membro corrente (impostato dall'API insieme al tenant). Senza, nessuna visibilità per area.
create or replace function app_member() returns uuid language sql stable as $$
  select nullif(current_setting('app.member_id', true), '')::uuid $$;

-- L'unità e tutte quelle sotto (security invoker: la RLS filtra il tenant).
create or replace function org_subtree(p_unit uuid) returns setof uuid language sql stable as $$
  with recursive t(id) as (
    select p_unit
    union
    select o.id from org_units o join t on o.parent_id = t.id
  ) select id from t $$;

-- Unità di cui il membro è responsabile (ruolo manager o org_units.manager_id) e quelle sotto.
create or replace function managed_org_units(p_member uuid) returns setof uuid language sql stable as $$
  with recursive roots(id) as (
    select m.org_unit_id from org_unit_members m where m.member_id = p_member and m.role = 'manager'
    union
    select u.id from org_units u where u.manager_id = p_member
  ), t(id) as (
    select id from roots
    union
    select o.id from org_units o join t on o.parent_id = t.id
  ) select id from t $$;

-- Sottoalbero visibile a un membro: unità di cui è responsabile o referente finanziario e
-- tutte quelle sotto (budget.md §5). Un semplice membro dell'unità non vede importi.
create or replace function visible_org_units(p_member uuid) returns setof uuid language sql stable as $$
  with recursive roots(id) as (
    select m.org_unit_id from org_unit_members m where m.member_id = p_member and m.role in ('manager','finance_ref')
    union
    select u.id from org_units u where u.manager_id = p_member
  ), t(id) as (
    select id from roots
    union
    select o.id from org_units o join t on o.parent_id = t.id
  ) select id from t $$;

-- Elementi di cui il membro è responsabile o PM (anche tramite gruppo, con l'ereditarietà).
create or replace function managed_elements(p_member uuid) returns setof uuid language sql stable as $$
  select e.id from elements e where e.assignee_id = p_member and e.deleted_at is null
  union
  select sh.element_id from stakeholders sh
  join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
  where r.key in ('project_manager', 'sponsor')
    and (sh.member_id = p_member or sh.group_id in (select member_groups(p_member))) $$;

-- ---------------------------------------------------------------------
-- 3. Periodi di budget
-- ---------------------------------------------------------------------
create table budget_periods (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  fiscal_year_id uuid not null,
  name text not null,
  ordinal integer not null,
  start_date date not null,
  end_date date not null,
  status text not null default 'planning' check (status in ('planning','open','closing','closed')),
  closed_at timestamptz,
  closed_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, fiscal_year_id) references fiscal_years (tenant_id, id),
  foreign key (tenant_id, closed_by) references members (tenant_id, id) on delete set null (closed_by),
  constraint budget_period_dates check (end_date > start_date)
);
create unique index budget_periods_start_uq on budget_periods (tenant_id, fiscal_year_id, start_date);
create index on budget_periods (tenant_id, fiscal_year_id, ordinal);
create index on budget_periods (tenant_id, closed_by);
select enable_tenant_rls('budget_periods');

-- ---------------------------------------------------------------------
-- 4. Stanziamenti: nodi dell'albero di un periodo
-- ---------------------------------------------------------------------
create table budget_allocations (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  period_id uuid not null,
  parent_id uuid,
  kind text not null check (kind in ('root','unit','element')),
  org_unit_id uuid,
  element_id uuid,
  name text not null default '',                -- etichetta della radice o del nodo
  nature text check (nature in ('capex','opex')),
  funding_source_id uuid,
  cost_category_id uuid,
  end_policy text check (end_policy in ('return','carry','lapse')),   -- eccezione per nodo
  note text not null default '',
  archived boolean not null default false,
  -- unità del nodo e dei suoi antenati: serve alla visibilità per area (mantenuto dal trigger)
  unit_path uuid[] not null default '{}',
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, period_id) references budget_periods (tenant_id, id),
  foreign key (tenant_id, parent_id) references budget_allocations (tenant_id, id),
  foreign key (tenant_id, org_unit_id) references org_units (tenant_id, id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id),
  foreign key (tenant_id, funding_source_id) references funding_sources (tenant_id, id),
  foreign key (tenant_id, cost_category_id) references cost_categories (tenant_id, id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by),
  constraint allocation_shape check (
    (kind = 'root' and org_unit_id is null and element_id is null and parent_id is null)
    or (kind = 'unit' and org_unit_id is not null and element_id is null and parent_id is not null)
    or (kind = 'element' and element_id is not null and org_unit_id is null and parent_id is not null))
);
create unique index budget_alloc_root_uq on budget_allocations (tenant_id, period_id) where kind = 'root';
create unique index budget_alloc_target_uq on budget_allocations (tenant_id, period_id,
  coalesce(org_unit_id, '00000000-0000-0000-0000-000000000000'::uuid),
  coalesce(element_id, '00000000-0000-0000-0000-000000000000'::uuid),
  coalesce(nature, ''), coalesce(funding_source_id, '00000000-0000-0000-0000-000000000000'::uuid)) where kind <> 'root';
create index on budget_allocations (tenant_id, period_id, parent_id);
create index on budget_allocations (tenant_id, parent_id);
create index on budget_allocations (tenant_id, org_unit_id);
create index on budget_allocations (tenant_id, element_id);
create index on budget_allocations (tenant_id, funding_source_id);
create index on budget_allocations (tenant_id, cost_category_id);
create index on budget_allocations (tenant_id, created_by);
create index budget_alloc_path_idx on budget_allocations using gin (unit_path);

-- Il percorso delle unità si ricava dal padre: nessuno lo scrive a mano. Gira con i
-- permessi del proprietario e filtra il tenant a mano, perché chi scrive potrebbe non
-- vedere il padre (la visibilità per area applicata dalla RLS lo nasconderebbe).
create or replace function budget_alloc_path() returns trigger language plpgsql
security definer set search_path = public, pg_temp as $$
declare pp uuid[];
begin
  if new.parent_id is null then
    new.unit_path := '{}';
  else
    select a.unit_path into pp from budget_allocations a where a.tenant_id = new.tenant_id and a.id = new.parent_id;
    new.unit_path := coalesce(pp, '{}') || case when new.org_unit_id is not null then array[new.org_unit_id] else '{}'::uuid[] end;
  end if;
  return new;
end $$;
create trigger budget_alloc_path before insert on budget_allocations
  for each row execute function budget_alloc_path();

-- Un nodo non cambia padre, periodo, unità o elemento dopo la creazione.
create or replace function budget_alloc_frozen() returns trigger language plpgsql as $$
begin
  if new.period_id is distinct from old.period_id or new.parent_id is distinct from old.parent_id
     or new.kind is distinct from old.kind or new.org_unit_id is distinct from old.org_unit_id
     or new.element_id is distinct from old.element_id then
    raise exception 'periodo, padre e destinatario di uno stanziamento non cambiano' using errcode = '23514';
  end if;
  return new;
end $$;
create trigger budget_alloc_frozen before update on budget_allocations
  for each row execute function budget_alloc_frozen();

select enable_tenant_rls('budget_allocations');
-- Visibilità per area (budget.md §5): finanza tutto; responsabile o referente finanziario di
-- un'unità, il suo sottoalbero; PM o responsabile di un elemento, il nodo dell'elemento;
-- gli altri nulla. Restrittiva: si somma all'isolamento per tenant.
create policy alloc_visible on budget_allocations as restrictive
  using ((select app_finance())
    or unit_path && (select coalesce(array_agg(u), '{}'::uuid[]) from visible_org_units((select app_member())) u)
    or element_id in (select managed_elements((select app_member()))))
  with check ((select app_finance())
    or unit_path && (select coalesce(array_agg(u), '{}'::uuid[]) from visible_org_units((select app_member())) u)
    or element_id in (select managed_elements((select app_member()))));

-- ---------------------------------------------------------------------
-- 5. Registro dei movimenti: immutabile. L'importo di un nodo è la somma dei suoi movimenti.
-- ---------------------------------------------------------------------
create table budget_closures (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  period_id uuid not null,
  next_period_id uuid,
  status text not null default 'confirmed' check (status in ('confirmed','undone')),
  summary jsonb not null default '{}',
  created_by uuid,
  created_at timestamptz not null default now(),
  undone_by uuid,
  undone_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, period_id) references budget_periods (tenant_id, id),
  foreign key (tenant_id, next_period_id) references budget_periods (tenant_id, id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by),
  foreign key (tenant_id, undone_by) references members (tenant_id, id) on delete set null (undone_by)
);
create index on budget_closures (tenant_id, period_id);
create index on budget_closures (tenant_id, next_period_id);
create index on budget_closures (tenant_id, created_by);
create index on budget_closures (tenant_id, undone_by);
select enable_tenant_rls('budget_closures');
create policy finance_only on budget_closures as restrictive using ((select app_finance())) with check ((select app_finance()));

create table allocation_transfers (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  period_id uuid not null,
  from_allocation_id uuid not null,
  to_allocation_id uuid not null,
  amount_cents bigint not null check (amount_cents > 0),
  reason text not null,
  status text not null default 'pending' check (status in ('pending','approved','rejected')),
  approval_kind text not null default 'none' check (approval_kind in ('none','manager','body')),
  approver_member_id uuid,
  approval_request_id uuid,
  created_by uuid,
  created_at timestamptz not null default now(),
  decided_by uuid,
  decided_at timestamptz,
  decision_note text,
  primary key (tenant_id, id),
  foreign key (tenant_id, period_id) references budget_periods (tenant_id, id),
  foreign key (tenant_id, from_allocation_id) references budget_allocations (tenant_id, id),
  foreign key (tenant_id, to_allocation_id) references budget_allocations (tenant_id, id),
  foreign key (tenant_id, approver_member_id) references members (tenant_id, id) on delete set null (approver_member_id),
  foreign key (tenant_id, approval_request_id) references approval_requests (tenant_id, id) on delete set null (approval_request_id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by),
  foreign key (tenant_id, decided_by) references members (tenant_id, id) on delete set null (decided_by),
  constraint alloc_transfer_distinct check (from_allocation_id <> to_allocation_id)
);
create index on allocation_transfers (tenant_id, period_id, status);
create index on allocation_transfers (tenant_id, from_allocation_id);
create index on allocation_transfers (tenant_id, to_allocation_id);
create index on allocation_transfers (tenant_id, approver_member_id);
create index on allocation_transfers (tenant_id, approval_request_id);
create index on allocation_transfers (tenant_id, created_by);
create index on allocation_transfers (tenant_id, decided_by);
select enable_tenant_rls('allocation_transfers');
create policy alloc_visible on allocation_transfers as restrictive
  using ((select app_finance())
    or exists (select 1 from budget_allocations a where a.tenant_id = allocation_transfers.tenant_id and a.id = allocation_transfers.from_allocation_id)
    or exists (select 1 from budget_allocations a where a.tenant_id = allocation_transfers.tenant_id and a.id = allocation_transfers.to_allocation_id))
  with check ((select app_finance())
    or exists (select 1 from budget_allocations a where a.tenant_id = allocation_transfers.tenant_id and a.id = allocation_transfers.from_allocation_id)
    or exists (select 1 from budget_allocations a where a.tenant_id = allocation_transfers.tenant_id and a.id = allocation_transfers.to_allocation_id));

create table budget_movements (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  period_id uuid not null,
  allocation_id uuid not null,
  kind text not null check (kind in ('initial','allocate','transfer','return','carry','lapse','adjust')),
  amount_cents bigint not null check (amount_cents <> 0),   -- con segno: effetto sull'importo del nodo
  counterpart_id uuid,                      -- l'altro nodo (padre, altro nodo del trasferimento, nodo del periodo successivo)
  batch_id uuid not null,                   -- le righe di una stessa operazione
  closure_id uuid,
  transfer_id uuid,
  approval_request_id uuid,
  reason text not null default '',
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, period_id) references budget_periods (tenant_id, id),
  foreign key (tenant_id, allocation_id) references budget_allocations (tenant_id, id),
  foreign key (tenant_id, closure_id) references budget_closures (tenant_id, id),
  foreign key (tenant_id, transfer_id) references allocation_transfers (tenant_id, id),
  foreign key (tenant_id, approval_request_id) references approval_requests (tenant_id, id) on delete set null (approval_request_id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on budget_movements (tenant_id, allocation_id, created_at);
create index on budget_movements (tenant_id, period_id, kind);
create index on budget_movements (tenant_id, batch_id);
create index on budget_movements (tenant_id, closure_id);
create index on budget_movements (tenant_id, transfer_id);
create index on budget_movements (tenant_id, counterpart_id);
create index on budget_movements (tenant_id, approval_request_id);
create index on budget_movements (tenant_id, created_by);
select enable_tenant_rls('budget_movements');
create policy alloc_visible on budget_movements as restrictive
  using ((select app_finance())
    or exists (select 1 from budget_allocations a where a.tenant_id = budget_movements.tenant_id and a.id = budget_movements.allocation_id))
  with check ((select app_finance())
    or exists (select 1 from budget_allocations a where a.tenant_id = budget_movements.tenant_id and a.id = budget_movements.allocation_id));

-- Immutabile: il ruolo applicativo non può modificare né cancellare, e nemmeno il proprietario
-- può modificare (una correzione è un nuovo movimento di segno opposto).
revoke update, delete on budget_movements from mytrack_app;
create or replace function budget_movements_immutable() returns trigger language plpgsql as $$
begin
  raise exception 'i movimenti di budget non si modificano: si registra un movimento di rettifica' using errcode = '23514';
end $$;
create trigger budget_movements_immutable before update on budget_movements
  for each row execute function budget_movements_immutable();

-- Serializzazione per albero: chi scrive movimenti di un periodo blocca il periodo, così due
-- richieste concorrenti sullo stesso albero si mettono in fila (il servizio lo fa già prima di
-- leggere; qui è la rete di sicurezza).
create or replace function budget_movements_lock() returns trigger language plpgsql
security definer set search_path = public, pg_temp as $$
begin
  perform 1 from budget_periods p where p.tenant_id = new.tenant_id and p.id = new.period_id for update;
  return new;
end $$;
create trigger budget_movements_lock before insert on budget_movements
  for each row execute function budget_movements_lock();

-- Rete di sicurezza della regola «la somma dei figli non supera il padre»: a fine transazione,
-- per ogni nodo toccato, importo non negativo e figli entro il padre. Il servizio controlla
-- prima e dà messaggi chiari; questo copre ogni altro percorso di scrittura.
create or replace function budget_movements_check() returns trigger language plpgsql
security definer set search_path = public, pg_temp as $$
declare amount bigint; parent uuid; kids bigint; pamount bigint;
begin
  select coalesce(sum(m.amount_cents), 0) into amount from budget_movements m where m.tenant_id = new.tenant_id and m.allocation_id = new.allocation_id;
  if amount < 0 then
    raise exception 'lo stanziamento non può diventare negativo' using errcode = '23514';
  end if;
  select coalesce(sum(m.amount_cents), 0) into kids from budget_movements m
    join budget_allocations c on c.tenant_id = m.tenant_id and c.id = m.allocation_id
    where c.tenant_id = new.tenant_id and c.parent_id = new.allocation_id;
  if kids > amount then
    raise exception 'la somma dei figli supera lo stanziamento del nodo' using errcode = '23514';
  end if;
  select a.parent_id into parent from budget_allocations a where a.tenant_id = new.tenant_id and a.id = new.allocation_id;
  if parent is not null then
    select coalesce(sum(m.amount_cents), 0) into pamount from budget_movements m where m.tenant_id = new.tenant_id and m.allocation_id = parent;
    select coalesce(sum(m.amount_cents), 0) into kids from budget_movements m
      join budget_allocations c on c.tenant_id = m.tenant_id and c.id = m.allocation_id
      where c.tenant_id = new.tenant_id and c.parent_id = parent;
    if kids > pamount then
      raise exception 'la somma dei figli supera lo stanziamento del padre' using errcode = '23514';
    end if;
  end if;
  return null;
end $$;
create constraint trigger budget_movements_check after insert on budget_movements
  deferrable initially deferred for each row execute function budget_movements_check();

-- ---------------------------------------------------------------------
-- 6. Collegamento con i budget di progetto
-- ---------------------------------------------------------------------
alter table budgets add column allocation_id uuid;
alter table budgets add constraint budgets_allocation_fk
  foreign key (tenant_id, allocation_id) references budget_allocations (tenant_id, id) on delete set null (allocation_id);
create index on budgets (tenant_id, allocation_id);

-- ---------------------------------------------------------------------
-- 7. Il vecchio plafond diventa la radice (un periodo per esercizio, granularità «anno»)
-- ---------------------------------------------------------------------
insert into budget_periods (tenant_id, id, fiscal_year_id, name, ordinal, start_date, end_date, status)
  select f.tenant_id, gen_random_uuid(), f.id, f.name, 1, f.start_date, f.end_date,
         case f.status when 'closed' then 'closed' when 'planning' then 'planning' else 'open' end
  from fiscal_years f where f.envelope_cents is not null
    and not exists (select 1 from budget_periods x where x.tenant_id = f.tenant_id and x.fiscal_year_id = f.id);

insert into budget_allocations (tenant_id, id, period_id, kind, name)
  select p.tenant_id, gen_random_uuid(), p.id, 'root', 'Plafond ' || p.name
  from budget_periods p
  where not exists (select 1 from budget_allocations r where r.tenant_id = p.tenant_id and r.period_id = p.id and r.kind = 'root');

insert into budget_movements (tenant_id, id, period_id, allocation_id, kind, amount_cents, batch_id, reason)
  select a.tenant_id, gen_random_uuid(), a.period_id, a.id, 'initial', f.envelope_cents, gen_random_uuid(), 'Plafond dell''esercizio (migrazione)'
  from budget_allocations a
  join budget_periods p on p.tenant_id = a.tenant_id and p.id = a.period_id
  join fiscal_years f on f.tenant_id = p.tenant_id and f.id = p.fiscal_year_id
  where a.kind = 'root' and f.envelope_cents > 0
    and not exists (select 1 from budget_movements x where x.tenant_id = a.tenant_id and x.allocation_id = a.id);
