-- Assegnatari dei passi di approvazione attivi (benchmark del 2026-11-24).
-- Prima l'inbox risolveva "chi può decidere" per ogni passo in attesa del tenant
-- (1,96 s con 20.000 approvazioni aperte). Ora gli assegnatari si risolvono una volta,
-- quando un passo diventa attivo, e l'inbox è una ricerca per membro o gruppo.
-- Le deleghe restano risolte al momento della lettura (dipendono dalla data).

create table approval_step_assignees (
  tenant_id uuid not null,
  step_id uuid not null,
  member_id uuid,
  group_id uuid,
  body_id uuid,
  foreign key (tenant_id, step_id) references approval_steps (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, group_id) references groups (tenant_id, id) on delete cascade,
  foreign key (tenant_id, body_id) references governance_bodies (tenant_id, id) on delete cascade,
  constraint assignee_holder check (num_nonnulls(member_id, group_id, body_id) >= 1)
);
create index on approval_step_assignees (tenant_id, step_id);
create index on approval_step_assignees (tenant_id, member_id) where member_id is not null;
create index on approval_step_assignees (tenant_id, group_id) where group_id is not null;
create index on approval_step_assignees (tenant_id, body_id) where body_id is not null;
select enable_tenant_rls('approval_step_assignees');

-- Ricalcola gli assegnatari dei passi attivi (gruppo pendente con ordinale minimo) di una richiesta.
-- Il tenant serve per usare le chiavi primarie (tenant_id, id): dall'app arriva da app.tenant_id,
-- dai trigger e dalle migrazioni va passato esplicitamente.
create or replace function approval_refresh_assignees(p_request uuid, p_tenant uuid default null) returns void language plpgsql as $$
declare
  t uuid := coalesce(p_tenant, app_tenant());
  st text;
  active smallint;
begin
  if t is null then
    raise exception 'approval_refresh_assignees: tenant mancante';
  end if;
  select status into st from approval_requests where tenant_id = t and id = p_request;
  if st is null then
    return;
  end if;
  delete from approval_step_assignees a
  using approval_steps s
  where a.tenant_id = t and s.tenant_id = t and s.request_id = p_request and a.step_id = s.id;
  if st <> 'pending' then
    return;
  end if;
  select min(ordinal) into active from approval_steps where tenant_id = t and request_id = p_request and decision = 'pending';
  if active is null then
    return;
  end if;
  insert into approval_step_assignees (tenant_id, step_id, member_id, body_id)
    select s.tenant_id, s.id, s.approver_member_id, s.approver_body_id
    from approval_steps s
    where s.tenant_id = t and s.request_id = p_request and s.ordinal = active and s.decision = 'pending'
      and (s.approver_member_id is not null or s.approver_body_id is not null);
  insert into approval_step_assignees (tenant_id, step_id, member_id, group_id)
    select s.tenant_id, s.id, sh.member_id, sh.group_id
    from approval_steps s
    join approval_requests rq on rq.tenant_id = s.tenant_id and rq.id = s.request_id
    join stakeholder_roles r on r.tenant_id = s.tenant_id and r.key = s.approver_role_key
    join stakeholders sh on sh.tenant_id = s.tenant_id and sh.element_id = rq.element_id and sh.role_id = r.id
    where s.tenant_id = t and s.request_id = p_request and s.ordinal = active and s.decision = 'pending'
      and (sh.member_id is not null or sh.group_id is not null);
end $$;

-- Se cambiano gli stakeholder di un elemento con approvazioni aperte, si ricalcolano.
create or replace function stakeholders_refresh_approvals() returns trigger language plpgsql as $$
declare
  rec record;
  el uuid;
  ten uuid;
begin
  if tg_op = 'DELETE' then
    el := old.element_id; ten := old.tenant_id;
  else
    el := new.element_id; ten := new.tenant_id;
  end if;
  for rec in select id from approval_requests where tenant_id = ten and element_id = el and status = 'pending' loop
    perform approval_refresh_assignees(rec.id, ten);
  end loop;
  return null;
end $$;
create trigger stakeholders_approvals after insert or update or delete on stakeholders
  for each row execute function stakeholders_refresh_approvals();

-- Popola gli assegnatari delle richieste già aperte.
select approval_refresh_assignees(id, tenant_id) from approval_requests where status = 'pending';
