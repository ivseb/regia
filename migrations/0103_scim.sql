-- SCIM 2.0 per utenti e gruppi: un token dedicato per organizzazione.

create table scim_tokens (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  name text not null check (length(name) between 1 and 80),
  token_hash bytea not null unique,
  hint text not null default '',
  created_by uuid,
  created_at timestamptz not null default now(),
  last_used_at timestamptz,
  revoked_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
select enable_tenant_rls('scim_tokens');

-- Risoluzione di un token SCIM presentato in "Authorization: Bearer": avviene prima di
-- sapere il tenant. Vale solo se non è revocato e l'organizzazione è attiva; aggiorna
-- l'ultimo uso al più una volta al minuto.
create or replace function scim_token_use(p_hash bytea)
returns table (tenant_id uuid, token_id uuid)
language plpgsql volatile security definer set search_path = public, pg_temp as $$
begin
  return query
    update scim_tokens tk set last_used_at = now()
    from tenants t
    where tk.token_hash = p_hash and tk.revoked_at is null and t.id = tk.tenant_id and t.status = 'active'
      and (tk.last_used_at is null or tk.last_used_at < now() - interval '1 minute')
    returning tk.tenant_id, tk.id;
  if found then
    return;
  end if;
  return query
    select tk.tenant_id, tk.id from scim_tokens tk join tenants t on t.id = tk.tenant_id
    where tk.token_hash = p_hash and tk.revoked_at is null and t.status = 'active';
end $$;
revoke all on function scim_token_use(bytea) from public;
grant execute on function scim_token_use(bytea) to mytrack_app;
