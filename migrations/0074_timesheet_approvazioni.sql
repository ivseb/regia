-- Timesheet nel motore delle approvazioni.
-- La settimana inviata apre una richiesta di approvazione con soggetto «timesheet»: sta in
-- «Le mie approvazioni» accanto alle altre, vale la delega a tempo, e l'esito (approva /
-- respingi con motivo) è lo stesso dall'inbox e dalla pagina del timesheet.
--
-- 1. La richiesta non ha un elemento: element_id diventa facoltativo (obbligatorio per tutti
--    i soggetti tranne «timesheet») e la settimana si indica con persona e lunedì.
-- 2. Chi approva è un insieme («uno qualsiasi basta»): approval_step_members, scritto
--    all'invio e mai più ricalcolato. Se le regole del tempo cambiano, le settimane già inviate
--    restano sugli approvatori di quando sono partite.
-- 3. Le settimane già inviate prima di questa migrazione ricevono la richiesta qui sotto.

alter table approval_requests alter column element_id drop not null;
alter table approval_requests add column timesheet_member_id uuid;
alter table approval_requests add column timesheet_week date;
alter table approval_requests add foreign key (tenant_id, timesheet_member_id) references members (tenant_id, id) on delete cascade;
alter table approval_requests add constraint approval_requests_subject_element check (
  case when subject = 'timesheet'
       then element_id is null and timesheet_member_id is not null and timesheet_week is not null
       else element_id is not null end);
-- Una sola richiesta aperta per persona e settimana; serve anche a trovarla dal timesheet.
create unique index approval_requests_timesheet_open_uq on approval_requests (tenant_id, timesheet_member_id, timesheet_week)
  where subject = 'timesheet' and status = 'pending';
create index approval_requests_timesheet_idx on approval_requests (tenant_id, timesheet_member_id, timesheet_week)
  where subject = 'timesheet';

-- Il nuovo soggetto: il vincolo si estende leggendo quello attuale, per non togliere gli altri.
do $$
declare def text;
begin
  select pg_get_constraintdef(oid) into def from pg_constraint
  where conname = 'approval_requests_subject_check' and conrelid = 'approval_requests'::regclass;
  if def is not null and position('timesheet' in def) = 0 then
    execute 'alter table approval_requests drop constraint approval_requests_subject_check';
    execute 'alter table approval_requests add constraint approval_requests_subject_check ' ||
      replace(def, '])', ', ''timesheet''::text])');
  end if;
end $$;

-- Passo con un insieme di approvatori (approval_step_members) invece di una persona, un ruolo o un organo.
alter table approval_steps add column approver_pool boolean not null default false;
alter table approval_steps drop constraint step_approver;
alter table approval_steps add constraint step_approver check (approver_pool or num_nonnulls(approver_member_id, approver_body_id, approver_role_key) >= 1);

create table approval_step_members (
  tenant_id uuid not null,
  step_id uuid not null,
  member_id uuid not null,
  primary key (tenant_id, step_id, member_id),
  foreign key (tenant_id, step_id) references approval_steps (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on approval_step_members (tenant_id, member_id);
select enable_tenant_rls('approval_step_members');

-- Gli assegnatari del passo attivo comprendono l'insieme.
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
  insert into approval_step_assignees (tenant_id, step_id, member_id)
    select s.tenant_id, s.id, pm.member_id
    from approval_steps s
    join approval_step_members pm on pm.tenant_id = s.tenant_id and pm.step_id = s.id
    where s.tenant_id = t and s.request_id = p_request and s.ordinal = active and s.decision = 'pending' and s.approver_pool;
end $$;

-- Apre la richiesta della settimana inviata con gli approvatori indicati dal chiamante
-- (scelti con le regole del tempo di quel momento). Annulla una richiesta rimasta aperta.
create or replace function timesheet_open_request(p_tenant uuid, p_member uuid, p_week date, p_pool uuid[]) returns uuid language plpgsql as $$
declare
  rq uuid := gen_random_uuid();
  st uuid := gen_random_uuid();
  old record;
begin
  for old in select id from approval_requests
    where tenant_id = p_tenant and subject = 'timesheet' and timesheet_member_id = p_member and timesheet_week = p_week and status = 'pending' loop
    update approval_steps set decision = 'skipped' where tenant_id = p_tenant and request_id = old.id and decision = 'pending';
    update approval_requests set status = 'cancelled', closed_at = now() where tenant_id = p_tenant and id = old.id;
    perform approval_refresh_assignees(old.id, p_tenant);
  end loop;
  insert into approval_requests (tenant_id, id, element_id, stage, subject, requested_by, timesheet_member_id, timesheet_week)
    values (p_tenant, rq, null, 'timesheet', 'timesheet', p_member, p_member, p_week);
  insert into approval_steps (tenant_id, id, request_id, ordinal, parallel_group, approver_pool) values (p_tenant, st, rq, 1, 1, true);
  insert into approval_step_members (tenant_id, step_id, member_id)
    select distinct p_tenant, st, m from unnest(p_pool) m where m <> p_member;
  perform approval_refresh_assignees(rq, p_tenant);
  return rq;
end $$;

-- Settimane già inviate: stesse regole di sempre (responsabile del team, persona scelta,
-- amministratori del tempo), con i ruoli assegnati direttamente o tramite un gruppo.
do $$
declare
  w record;
  pool uuid[];
begin
  for w in select t.tenant_id, t.member_id, t.week_start,
                  coalesce(s.approver_mode, 'team_lead') as mode, s.approver_member_id
           from timesheets t
           left join time_settings s on s.tenant_id = t.tenant_id
           where t.status = 'submitted'
             and not exists (select 1 from approval_requests rq where rq.tenant_id = t.tenant_id and rq.subject = 'timesheet'
                             and rq.timesheet_member_id = t.member_id and rq.timesheet_week = t.week_start and rq.status = 'pending') loop
    select coalesce(array_agg(distinct x.m), '{}') into pool from (
      select w.approver_member_id as m where w.mode = 'member' and w.approver_member_id is not null
      union
      select t2.lead_id from team_members tmm
        join teams t2 on t2.tenant_id = tmm.tenant_id and t2.id = tmm.team_id and not t2.archived
        where w.mode = 'team_lead' and tmm.tenant_id = w.tenant_id and tmm.member_id = w.member_id and t2.lead_id is not null
      union
      select ra.member_id from role_assignments ra join roles ro on ro.tenant_id = ra.tenant_id and ro.id = ra.role_id
        where ra.tenant_id = w.tenant_id and ra.space_id is null and ra.member_id is not null
          and ro.permissions && array['*', 'tenant.admin', 'finance.write']
      union
      select gm.member_id from role_assignments ra join roles ro on ro.tenant_id = ra.tenant_id and ro.id = ra.role_id
        join group_members gm on gm.tenant_id = ra.tenant_id and gm.group_id = ra.group_id
        where ra.tenant_id = w.tenant_id and ra.space_id is null
          and ro.permissions && array['*', 'tenant.admin', 'finance.write']
    ) x join members mm on mm.tenant_id = w.tenant_id and mm.id = x.m and mm.status = 'active' where x.m <> w.member_id;
    perform timesheet_open_request(w.tenant_id, w.member_id, w.week_start, pool);
  end loop;
end $$;
