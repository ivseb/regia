-- Knowledge, rifiniture (replica/knowledge.md): commenti in linea sul testo, conferma di
-- lettura dei documenti controllati. Le pagine restano elementi; qui ci sono solo i dati nuovi.

-- ---------------------------------------------------------------------
-- 1. Commenti in linea
-- Un thread è ancorato a una frase della pagina: il testo citato (quote) con un po' di
-- contesto prima e dopo (prefix, suffix), la posizione nel testo di ancoraggio (pos) e quale
-- delle occorrenze uguali è (nth). A ogni salvataggio l'ancora si ricalcola; se il testo non
-- c'è più il thread diventa orfano ma resta visibile. pos null = il server non ha potuto
-- verificare la frase (selezione che attraversa elementi particolari): l'ancora la
-- ritrova solo il browser.
-- ---------------------------------------------------------------------
create table kb_threads (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  page_id uuid not null,
  quote text not null check (char_length(quote) between 1 and 1000),
  prefix text not null default '' check (char_length(prefix) <= 200),
  suffix text not null default '' check (char_length(suffix) <= 200),
  pos integer,
  nth integer not null default 0 check (nth >= 0),
  anchored_version integer not null default 1,
  orphan boolean not null default false,
  status text not null default 'open' check (status in ('open', 'resolved')),
  resolved_by uuid,
  resolved_at timestamptz,
  created_by uuid,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, page_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by),
  foreign key (tenant_id, resolved_by) references members (tenant_id, id) on delete set null (resolved_by)
);
create index kb_threads_page_idx on kb_threads (tenant_id, page_id, status);
create index kb_threads_open_idx on kb_threads (tenant_id, created_at desc) where status = 'open';
create trigger kb_threads_updated before update on kb_threads for each row execute function set_updated_at();
select enable_tenant_rls('kb_threads');

create table kb_thread_comments (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  thread_id uuid not null,
  author_id uuid,
  body text not null check (char_length(body) between 1 and 20000),
  mentions jsonb not null default '[]',
  created_at timestamptz not null default now(),
  edited_at timestamptz,
  deleted_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, thread_id) references kb_threads (tenant_id, id) on delete cascade,
  foreign key (tenant_id, author_id) references members (tenant_id, id) on delete set null (author_id)
);
create index kb_thread_comments_idx on kb_thread_comments (tenant_id, thread_id, created_at);
select enable_tenant_rls('kb_thread_comments');

-- ---------------------------------------------------------------------
-- 2. Conferma di lettura
-- Una campagna dice chi deve leggere (persone, gruppi, ruoli), entro quando e quale versione.
-- Le persone che ne fanno parte si calcolano dal pubblico (kb_read_targets) e si
-- aggiornano quando cambiano gruppi e ruoli. Le conferme (kb_read_confirms) sono evidenze:
-- non si modificano né si cancellano.
-- ---------------------------------------------------------------------
create table kb_read_campaigns (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  page_id uuid not null,
  name text not null check (char_length(name) between 1 and 200),
  version integer not null check (version > 0),         -- versione da leggere
  due_date date,
  status text not null default 'active' check (status in ('active', 'closed')),
  -- Una nuova versione approvata passa la campagna alla nuova versione e la riapre.
  reopen_on_approval boolean not null default true,
  renew_days integer not null default 14 check (renew_days between 1 and 180),
  remind_days integer not null default 3 check (remind_days between 0 and 30),
  reminded_for date,                                    -- scadenza per cui è già partito il promemoria
  overdue_reminded_on date,
  manual_reminded_at timestamptz,
  created_by uuid,
  created_at timestamptz not null default now(),
  closed_at timestamptz,
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, page_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index kb_read_campaigns_page_idx on kb_read_campaigns (tenant_id, page_id);
create index kb_read_campaigns_active_idx on kb_read_campaigns (tenant_id, due_date) where status = 'active';
create trigger kb_read_campaigns_updated before update on kb_read_campaigns for each row execute function set_updated_at();
select enable_tenant_rls('kb_read_campaigns');

create table kb_read_audience (
  tenant_id uuid not null,
  campaign_id uuid not null,
  kind text not null check (kind in ('member', 'group', 'role')),
  ref_id uuid not null,
  primary key (tenant_id, campaign_id, kind, ref_id),
  foreign key (tenant_id, campaign_id) references kb_read_campaigns (tenant_id, id) on delete cascade
);
select enable_tenant_rls('kb_read_audience');

create table kb_read_targets (
  tenant_id uuid not null,
  campaign_id uuid not null,
  member_id uuid not null,
  added_at timestamptz not null default now(),
  primary key (tenant_id, campaign_id, member_id),
  foreign key (tenant_id, campaign_id) references kb_read_campaigns (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index kb_read_targets_member_idx on kb_read_targets (tenant_id, member_id);
select enable_tenant_rls('kb_read_targets');

create table kb_read_confirms (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  campaign_id uuid not null,
  member_id uuid not null,
  version integer not null,
  confirmed_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, campaign_id) references kb_read_campaigns (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  unique (tenant_id, campaign_id, member_id, version)
);
create index kb_read_confirms_campaign_idx on kb_read_confirms (tenant_id, campaign_id, member_id);
select enable_tenant_rls('kb_read_confirms');
