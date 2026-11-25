-- Commenti, allegati, osservatori e notifiche (piano, passo 3).
-- Le tabelle esistono dalla 0001: qui si aggiunge solo quello che serve.

-- Commenti: la traccia "modificato" non usa updated_at, che cambia anche con l'eliminazione.
alter table comments add column edited_at timestamptz;

-- Allegati: il file su disco è indirizzato per sha256 e condiviso tra allegati con lo
-- stesso contenuto (deduplica), quindi storage_key non è più unico.
drop index if exists attachments_storage_uq;
create index on attachments (tenant_id, storage_key);

-- Notifiche: chi ha compiuto l'azione (per il testo "Sara Villa ti ha menzionato").
alter table notifications add column actor_id uuid;
alter table notifications add constraint notifications_actor_fk
  foreign key (tenant_id, actor_id) references members (tenant_id, id) on delete set null (actor_id);
create index on notifications (tenant_id, actor_id);

-- Preferenze per persona: tipi di evento silenziati in app e per email.
create table notification_prefs (
  tenant_id uuid not null,
  member_id uuid not null,
  muted_kinds text[] not null default '{}',
  email_muted_kinds text[] not null default '{}',
  updated_at timestamptz not null default now(),
  primary key (tenant_id, member_id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
select enable_tenant_rls('notification_prefs');

-- Outbox: tentativi e prossimo invio per il worker delle email (retry con attesa crescente).
alter table outbox_events add column attempts integer not null default 0;
alter table outbox_events add column next_attempt_at timestamptz;
alter table outbox_events add column last_error text;

-- Il richiedente del portale partecipa ai chiarimenti sulle proprie richieste.
update roles set permissions = array_append(permissions, 'comment.write')
where key = 'requester' and not ('comment.write' = any(permissions));
