-- Accesso unico OIDC e token API personali (piano, passo 15).

-- Identità esterne collegate a un account. Dal primo collegamento vale la coppia
-- (issuer, sub): l'email serve solo a trovare l'account la prima volta.
-- Tabella globale come accounts e sessions: nessun tenant_id, nessuna RLS per tenant.
create table account_identities (
  id uuid primary key default gen_random_uuid(),
  account_id uuid not null references accounts(id) on delete cascade,
  issuer text not null,
  subject text not null,
  created_at timestamptz not null default now(),
  last_login_at timestamptz,
  unique (issuer, subject),
  -- Un account ha al più un'identità per IdP: un secondo sub con la stessa email non si collega.
  unique (account_id, issuer)
);

-- Metodo con cui è nata la sessione ('password' o 'oidc'), mostrato nel profilo.
alter table sessions add column auth_method text not null default 'password'
  check (auth_method in ('password', 'oidc'));

-- Token API: si mostra all'utente solo l'inizio, per riconoscerlo nell'elenco.
alter table api_tokens add column hint text not null default '';

-- Risoluzione di un token presentato in "Authorization: Bearer": avviene prima di sapere
-- il tenant, quindi passa da una funzione dedicata (come account_memberships).
-- Restituisce il token solo se non è scaduto e se membro e tenant sono attivi;
-- aggiorna l'ultimo uso al più una volta al minuto, per non scrivere a ogni richiesta.
create or replace function api_token_use(p_hash bytea)
returns table (tenant_id uuid, token_id uuid, member_id uuid, account_id uuid, member_type text,
               slug text, name text, scopes text[])
language plpgsql volatile security definer set search_path = public, pg_temp as $$
begin
  return query
    update api_tokens tk set last_used_at = now()
    from members m, tenants t
    where tk.token_hash = p_hash
      and m.tenant_id = tk.tenant_id and m.id = tk.member_id
      and t.id = tk.tenant_id
      and tk.expires_at is not null and tk.expires_at > now()
      and m.status = 'active' and m.account_id is not null
      and (m.access_until is null or m.access_until > now())
      and t.status = 'active'
      and (tk.last_used_at is null or tk.last_used_at < now() - interval '1 minute')
    returning tk.tenant_id, tk.id, tk.member_id, m.account_id, m.member_type, t.slug, t.name, tk.scopes;
  if found then
    return;
  end if;
  -- Già usato nell'ultimo minuto: stessa verifica, senza aggiornare.
  return query
    select tk.tenant_id, tk.id, tk.member_id, m.account_id, m.member_type, t.slug, t.name, tk.scopes
    from api_tokens tk
    join members m on m.tenant_id = tk.tenant_id and m.id = tk.member_id
    join tenants t on t.id = tk.tenant_id
    where tk.token_hash = p_hash
      and tk.expires_at is not null and tk.expires_at > now()
      and m.status = 'active' and m.account_id is not null
      and (m.access_until is null or m.access_until > now())
      and t.status = 'active';
end $$;
revoke all on function api_token_use(bytea) from public;
grant execute on function api_token_use(bytea) to mytrack_app;
