-- AI, ondata 5: GRC assistito (replica/ai.md §2 punto 5).
--
-- 1. Collegamento evidenza → controllo: oltre al legame, il grado di copertura dichiarato da chi
--    collega (piena o parziale), una nota (il motivo) e da dove viene (a mano o da una proposta
--    dell'assistente accettata dalla persona). La copertura calcolata non cambia: un'evidenza
--    collegata vale come prima; il grado resta come informazione per chi verifica.
-- 2. Storia delle bozze delle notifiche normative: ogni bozza salvata dalla bozza dell'assistente
--    (e, da lì in poi, anche quelle salvate a mano) diventa una versione con il testo, i campi del
--    modello DORA e chi l'ha scritta. La notifica non si invia da Regia: si esporta.

alter table grc_evidence_controls
  add column coverage text check (coverage in ('full','partial')),
  add column note text check (note is null or length(note) <= 2000),
  add column via text not null default 'manual' check (via in ('manual','ai')),
  add column linked_by uuid;
alter table grc_evidence_controls
  add foreign key (tenant_id, linked_by) references members (tenant_id, id) on delete set null (linked_by);

create table regulatory_notification_drafts (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  notification_id uuid not null,
  version integer not null check (version >= 1),
  content text not null check (length(content) <= 50000),
  -- Campi del modello (numero, nome, valore, stato, fonte), così come la persona li ha salvati.
  fields jsonb not null default '[]',
  -- assistente: bozza preparata dall'assistente e rivista dalla persona; persona: salvata a mano.
  source text not null check (source in ('assistente','persona')),
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  unique (tenant_id, notification_id, version),
  foreign key (tenant_id, notification_id) references regulatory_notifications (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on regulatory_notification_drafts (tenant_id, notification_id, version desc);
select enable_tenant_rls('regulatory_notification_drafts');
