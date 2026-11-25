-- Ruolo applicativo, permessi e funzioni privilegiate (SECURITY DEFINER).
-- Il proprietario dello schema (mytrack_owner) deve essere superuser o avere BYPASSRLS.
-- In modalità esterna il ruolo mytrack_app può essere creato prima dall'amministratore del DB.

do $$
begin
  if not exists (select 1 from pg_roles where rolname = 'mytrack_app') then
    create role mytrack_app login;
  end if;
end $$;

grant usage on schema public to mytrack_app;
grant select, insert, update, delete on all tables in schema public to mytrack_app;
grant usage, select on all sequences in schema public to mytrack_app;
alter default privileges in schema public grant select, insert, update, delete on tables to mytrack_app;
alter default privileges in schema public grant usage, select on sequences to mytrack_app;

-- Tabelle di piattaforma: nessun accesso diretto dal ruolo applicativo.
revoke all on jobs, audit_log, audit_log_default from mytrack_app;
-- I tenant si creano e si modificano solo dal ruolo di piattaforma.
revoke insert, update, delete on tenants from mytrack_app;

-- Per le migrazioni future: attiva e forza la RLS di una tabella del tenant.
create or replace function enable_tenant_rls(tbl regclass) returns void language plpgsql as $$
begin
  execute format('alter table %s enable row level security', tbl);
  execute format('alter table %s force row level security', tbl);
  execute format('create policy tenant_isolation on %s using (tenant_id = (select app_tenant())) with check (tenant_id = (select app_tenant()))', tbl);
end $$;

-- ---------------------------------------------------------------------
-- Audit con catena di hash per tenant.
-- hash = sha256(prev_hash || evento canonico). L'evento canonico è il testo di un
-- jsonb (chiavi in ordine deterministico); il tempo è in microsecondi epoch.
-- La catena è serializzata per tenant con un advisory lock di transazione.
-- ---------------------------------------------------------------------
create or replace function audit_canonical(
  p_tenant uuid, p_seq bigint, p_at timestamptz, p_actor_account uuid, p_actor_member uuid,
  p_action text, p_entity_type text, p_entity_id uuid, p_payload jsonb
) returns bytea language sql immutable as $$
  select convert_to(jsonb_build_object(
    'tenant', p_tenant, 'seq', p_seq, 'at', (extract(epoch from p_at) * 1000000)::bigint,
    'actor_account', p_actor_account, 'actor_member', p_actor_member,
    'action', p_action, 'entity_type', p_entity_type, 'entity_id', p_entity_id,
    'payload', p_payload)::text, 'UTF8')
$$;

create or replace function audit_append(
  p_action text, p_entity_type text, p_entity_id uuid, p_payload jsonb,
  p_actor_account uuid, p_actor_member uuid, p_ip inet
) returns bigint
language plpgsql security definer set search_path = public, pg_temp as $$
declare
  t uuid := app_tenant();
  last_seq bigint;
  last_hash bytea;
  ts timestamptz := clock_timestamp();
  new_seq bigint;
begin
  perform pg_advisory_xact_lock(hashtextextended('mytrack.audit:' || coalesce(t::text, 'platform'), 0));
  if t is null then
    select seq, hash into last_seq, last_hash from audit_log where tenant_id is null order by seq desc limit 1;
  else
    select seq, hash into last_seq, last_hash from audit_log where tenant_id = t order by seq desc limit 1;
  end if;
  new_seq := coalesce(last_seq, 0) + 1;
  insert into audit_log (tenant_id, seq, at, actor_account_id, actor_member_id, action, entity_type, entity_id, payload, ip, prev_hash, hash)
  values (t, new_seq, ts, p_actor_account, p_actor_member, p_action, p_entity_type, p_entity_id, coalesce(p_payload, '{}'), p_ip, last_hash,
          sha256(coalesce(last_hash, ''::bytea) || audit_canonical(t, new_seq, ts, p_actor_account, p_actor_member, p_action, p_entity_type, p_entity_id, coalesce(p_payload, '{}'))));
  return new_seq;
end $$;
revoke all on function audit_append(text, text, uuid, jsonb, uuid, uuid, inet) from public;
grant execute on function audit_append(text, text, uuid, jsonb, uuid, uuid, inet) to mytrack_app;

-- Verifica della catena (solo ruolo di piattaforma): restituisce il primo seq rotto, null se integra.
create or replace function audit_verify(p_tenant uuid) returns bigint language plpgsql as $$
declare
  r record;
  prev bytea := null;
  expected_seq bigint := 1;
begin
  for r in select * from audit_log where tenant_id is not distinct from p_tenant order by seq loop
    if r.seq <> expected_seq
       or r.prev_hash is distinct from prev
       or r.hash <> sha256(coalesce(prev, ''::bytea) || audit_canonical(r.tenant_id, r.seq, r.at, r.actor_account_id, r.actor_member_id, r.action, r.entity_type, r.entity_id, r.payload)) then
      return r.seq;
    end if;
    prev := r.hash;
    expected_seq := expected_seq + 1;
  end loop;
  return null;
end $$;
revoke all on function audit_verify(uuid) from public;

-- Lettura dell'audit del tenant corrente (admin tenant, auditor): paginazione per seq decrescente.
create or replace function audit_page(p_before bigint, p_limit int)
returns table (seq bigint, at timestamptz, actor_account_id uuid, actor_member_id uuid, action text, entity_type text, entity_id uuid, payload jsonb)
language sql stable security definer set search_path = public, pg_temp as $$
  select a.seq, a.at, a.actor_account_id, a.actor_member_id, a.action, a.entity_type, a.entity_id, a.payload
  from audit_log a
  where app_tenant() is not null and a.tenant_id = app_tenant()
    and (p_before is null or a.seq < p_before)
  order by a.seq desc
  limit least(greatest(p_limit, 1), 500)
$$;
revoke all on function audit_page(bigint, int) from public;
grant execute on function audit_page(bigint, int) to mytrack_app;

-- ---------------------------------------------------------------------
-- Tenant di un account: serve al login, prima di sapere quale tenant usare.
-- Restituisce solo i tenant attivi in cui l'account è membro attivo.
-- ---------------------------------------------------------------------
create or replace function account_memberships(p_account uuid)
returns table (tenant_id uuid, slug text, name text, member_id uuid, member_type text)
language sql stable security definer set search_path = public, pg_temp as $$
  select t.id, t.slug, t.name, m.id, m.member_type
  from members m join tenants t on t.id = m.tenant_id
  where m.account_id = p_account and m.status = 'active' and t.status = 'active'
    and (m.access_until is null or m.access_until > now())
  order by t.name
$$;
revoke all on function account_memberships(uuid) from public;
grant execute on function account_memberships(uuid) to mytrack_app;
