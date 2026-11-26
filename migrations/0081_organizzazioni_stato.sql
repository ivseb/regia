-- Organizzazioni sospese: account_memberships espone anche lo stato del tenant, così il codice può
-- decidere (e nominare l'organizzazione nel messaggio) invece di nascondere i tenant sospesi.
-- Restano esclusi i membri non attivi o scaduti. Chi la usa deve filtrare su status = 'active'
-- per sapere dove l'account può lavorare.
drop function if exists account_memberships(uuid);
create function account_memberships(p_account uuid)
returns table (tenant_id uuid, slug text, name text, member_id uuid, member_type text, status text)
language sql stable security definer set search_path = public, pg_temp as $$
  select t.id, t.slug, t.name, m.id, m.member_type, t.status
  from members m join tenants t on t.id = m.tenant_id
  where m.account_id = p_account and m.status = 'active'
    and (m.access_until is null or m.access_until > now())
  order by t.name
$$;
revoke all on function account_memberships(uuid) from public;
grant execute on function account_memberships(uuid) to mytrack_app;
