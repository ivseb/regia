-- Amministrazione di persone e accessi da interfaccia, MFA con TOTP e webhook in uscita
-- (piano, passo 15; architecture.md §4).

-- =====================================================================
-- 1. MFA locale (TOTP, RFC 6238) per gli account con password
-- =====================================================================

-- accounts.mfa_secret_enc esiste dallo schema iniziale: segreto attivo, cifrato con la
-- chiave applicativa (<data>/keys/secrets.key, AES-256-GCM, legato all'id dell'account).
alter table accounts add column mfa_enabled_at timestamptz;
-- Segreto in attivazione: diventa attivo solo dopo la conferma con un codice valido.
alter table accounts add column mfa_pending_enc bytea;
-- Ultimo intervallo di 30 secondi accettato: lo stesso codice non vale due volte.
alter table accounts add column mfa_last_step bigint;

-- Codici di recupero monouso: si salva solo l'HMAC con la chiave applicativa.
create table mfa_recovery_codes (
  account_id uuid not null references accounts(id) on delete cascade,
  code_hash bytea not null,
  used_at timestamptz,
  created_at timestamptz not null default now(),
  primary key (account_id, code_hash)
);

-- Secondo passo del login: dopo la password e prima della sessione. Il token sta in un
-- cookie HttpOnly; nel database solo lo sha256. 'setup' = attivazione obbligatoria
-- per la politica del tenant.
create table mfa_challenges (
  token_hash bytea primary key,
  account_id uuid not null references accounts(id) on delete cascade,
  purpose text not null check (purpose in ('verify', 'setup')),
  attempts integer not null default 0,
  ip inet,
  user_agent text,
  expires_at timestamptz not null,
  created_at timestamptz not null default now()
);
create index on mfa_challenges (account_id);
create index on mfa_challenges (expires_at);

-- La sessione ricorda se è passata dal secondo fattore (mostrato nel profilo e nell'elenco membri).
alter table sessions add column mfa boolean not null default false;

-- Politica di sicurezza del tenant.
create table tenant_security (
  tenant_id uuid primary key references tenants(id) on delete cascade,
  -- off | admins (chi ha tenant.admin) | all. Non vale per chi entra con l'SSO.
  mfa_policy text not null default 'off' check (mfa_policy in ('off', 'admins', 'all')),
  updated_at timestamptz not null default now(),
  updated_by uuid,
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
select enable_tenant_rls('tenant_security');

-- L'account deve usare l'MFA? Sì se in almeno un tenant attivo di cui è membro attivo la
-- politica è "all", o "admins" ed è amministratore (ruolo con * o tenant.admin, anche
-- tramite gruppo). Serve al login, prima di sapere il tenant: funzione dedicata.
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
            select gm.group_id from group_members gm where gm.tenant_id = m.tenant_id and gm.member_id = m.id))))))
$$;
revoke all on function account_mfa_required(uuid) from public;
grant execute on function account_mfa_required(uuid) to mytrack_app;

-- =====================================================================
-- 2. Inviti e reimpostazione della password
-- =====================================================================

-- Invito: link monouso a scadenza. Il membro resta "invited" (non entra, non compare nei
-- selettori) finché non accetta. new_account: l'account è nato con l'invito e chi accetta
-- sceglie nome e password; un account esistente si limita ad attivare l'appartenenza.
create table invitations (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  member_id uuid not null,
  token_hash bytea not null unique,
  new_account boolean not null,
  created_by uuid,
  expires_at timestamptz not null,
  accepted_at timestamptz,
  revoked_at timestamptz,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on invitations (tenant_id, member_id);
create index on invitations (tenant_id, created_by);
select enable_tenant_rls('invitations');

-- Il link arriva senza sessione né tenant: questa funzione dice solo a quale tenant
-- appartiene un invito ancora valido; il resto si legge sotto RLS.
create or replace function invitation_tenant(p_hash bytea) returns uuid
language sql stable security definer set search_path = public, pg_temp as $$
  select tenant_id from invitations
  where token_hash = p_hash and accepted_at is null and revoked_at is null and expires_at > now()
$$;
revoke all on function invitation_tenant(bytea) from public;
grant execute on function invitation_tenant(bytea) to mytrack_app;

-- Reimpostazione della password: tabella globale come accounts (la password è dell'account).
-- issued_tenant_id è il tenant dell'amministratore che l'ha emessa (non è un dato del tenant).
create table password_resets (
  id uuid primary key,
  account_id uuid not null references accounts(id) on delete cascade,
  token_hash bytea not null unique,
  issued_tenant_id uuid references tenants(id) on delete cascade,
  issued_by uuid,
  expires_at timestamptz not null,
  used_at timestamptz,
  created_at timestamptz not null default now()
);
create index on password_resets (account_id);
create index on password_resets (issued_tenant_id);

-- In quanti altri tenant l'account è membro (qualsiasi stato tranne anonimizzato)?
-- Un amministratore di tenant può reimpostare password e MFA solo degli account che
-- appartengono esclusivamente al suo tenant: altrimenti prenderebbe il controllo di un
-- accesso che vale anche altrove.
create or replace function account_other_memberships(p_account uuid, p_tenant uuid) returns integer
language sql stable security definer set search_path = public, pg_temp as $$
  select count(*)::integer from members
  where account_id = p_account and tenant_id <> p_tenant and status <> 'anonymized'
$$;
revoke all on function account_other_memberships(uuid, uuid) from public;
grant execute on function account_other_memberships(uuid, uuid) to mytrack_app;

-- =====================================================================
-- 3. Ruoli personalizzati
-- =====================================================================
alter table roles add column description text not null default '';
alter table roles add constraint roles_name_len check (length(name) between 1 and 80);
create unique index roles_name_uq on roles (tenant_id, lower(name));

-- =====================================================================
-- 4. Audit: ricerca con filtri e verifica della catena dall'app
-- =====================================================================
create or replace function audit_search(
  p_before bigint, p_limit int, p_action text, p_entity_type text, p_entity uuid,
  p_actor uuid, p_from timestamptz, p_to timestamptz)
returns table (seq bigint, at timestamptz, actor_account_id uuid, actor_member_id uuid, action text, entity_type text, entity_id uuid, payload jsonb)
language sql stable security definer set search_path = public, pg_temp as $$
  select a.seq, a.at, a.actor_account_id, a.actor_member_id, a.action, a.entity_type, a.entity_id, a.payload
  from audit_log a
  where app_tenant() is not null and a.tenant_id = app_tenant()
    and (p_before is null or a.seq < p_before)
    and (p_action is null or a.action = p_action or a.action like p_action || '.%')
    and (p_entity_type is null or a.entity_type = p_entity_type)
    and (p_entity is null or a.entity_id = p_entity)
    and (p_actor is null or a.actor_member_id = p_actor)
    and (p_from is null or a.at >= p_from)
    and (p_to is null or a.at < p_to)
  order by a.seq desc
  limit least(greatest(p_limit, 1), 500)
$$;
revoke all on function audit_search(bigint, int, text, text, uuid, uuid, timestamptz, timestamptz) from public;
grant execute on function audit_search(bigint, int, text, text, uuid, uuid, timestamptz, timestamptz) to mytrack_app;

-- Stato della catena del tenant corrente: numero di eventi, primo seq rotto (null = integra),
-- ultimo seq e la sua hash. Ricalcola tutta la catena con audit_verify.
create or replace function audit_chain_status()
returns table (entries bigint, broken_seq bigint, last_seq bigint, last_hash text)
language plpgsql stable security definer set search_path = public, pg_temp as $$
declare t uuid := app_tenant();
begin
  if t is null then
    return;
  end if;
  return query
    select (select count(*) from audit_log where tenant_id = t),
           audit_verify(t),
           l.seq, encode(l.hash, 'hex')
    from (select 1) one
    left join lateral (select a.seq, a.hash from audit_log a where a.tenant_id = t order by a.seq desc limit 1) l on true;
end $$;
revoke all on function audit_chain_status() from public;
grant execute on function audit_chain_status() to mytrack_app;

-- =====================================================================
-- 5. Webhook in uscita
-- =====================================================================
-- webhooks esiste dallo schema iniziale (url, secret_enc, events, active).
alter table webhooks add column name text not null default '';
-- Filtro nel linguaggio delle condizioni, valutato sull'elemento dell'evento (vuoto = tutti).
alter table webhooks add column filter text not null default '';
alter table webhooks add column secret_hint text not null default '';
alter table webhooks add column created_by uuid;
alter table webhooks add column updated_at timestamptz not null default now();
alter table webhooks add constraint webhooks_created_by_fk
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by);
create index on webhooks (tenant_id, created_by);

-- Consegne: una riga per evento e webhook. body è il JSON esatto che si firma e si invia
-- (anche nei reinvii, con lo stesso id di evento).
create table webhook_deliveries (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  webhook_id uuid not null,
  event_id uuid not null,
  event text not null,
  body text not null,
  status text not null default 'pending' check (status in ('pending', 'delivered', 'failed')),
  attempts integer not null default 0,
  next_attempt_at timestamptz not null default now(),
  last_attempt_at timestamptz,
  response_status integer,
  last_error text,
  duration_ms integer,
  redelivery_of uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, webhook_id) references webhooks (tenant_id, id) on delete cascade
);
create index webhook_deliveries_due_idx on webhook_deliveries (next_attempt_at) where status = 'pending';
create index on webhook_deliveries (tenant_id, webhook_id, created_at desc);
select enable_tenant_rls('webhook_deliveries');

-- Gli eventi nascono dalla storia degli elementi, nella stessa transazione: un trigger
-- scrive nell'outbox solo se il tenant ha un webhook attivo per quell'evento.
create or replace function activities_webhook() returns trigger language plpgsql as $$
declare
  ev text;
begin
  ev := case
    when new.kind = 'created' then 'element.created'
    when new.kind = 'state' then 'element.phase_changed'
    when new.kind = 'comment' and new.field_key = 'added' then 'comment.created'
    when new.kind = 'attachment' and new.field_key = 'added' then 'attachment.added'
    when new.kind = 'approval' and new.field_key = 'decision' then 'approval.decided'
    when new.kind = 'review' and new.field_key = 'decided' then 'review.completed'
    when new.kind = 'meeting' and new.field_key = 'decision' then 'governance.decision'
  end;
  if ev is null then
    return null;
  end if;
  if exists (select 1 from webhooks w where w.tenant_id = new.tenant_id and w.active and ev = any (w.events)) then
    insert into outbox_events (tenant_id, kind, payload)
    values (new.tenant_id, 'webhook', jsonb_build_object(
      'event', ev, 'element_id', new.element_id, 'actor_id', new.actor_id, 'activity_id', new.id,
      'added', new.added, 'removed', new.removed, 'at', new.created_at));
  end if;
  return null;
end $$;
create trigger activities_webhook after insert on activities for each row execute function activities_webhook();
