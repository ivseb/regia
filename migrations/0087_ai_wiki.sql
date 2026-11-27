-- AI, ondata 4: la wiki personale («wikiLLM», replica/ai.md §2 punto 1 e §7).
--
-- Ogni persona ha le sue pagine in Markdown, che l'assistente scrive senza conferma pagina per
-- pagina e la persona corregge a mano. Ogni modifica è una revisione con l'istantanea completa
-- (titolo, testo, pagina tolta o no), l'autore («assistente» o «persona») e le righe aggiunte e
-- tolte: annullare una modifica vuol dire tornare all'istantanea precedente, con una revisione nuova.
--
-- Privacy: oltre all'isolamento per tenant (enable_tenant_rls) ogni tabella ha politiche
-- restrittive sul membro (app.member_id, impostato dall'API e dal lavoro in background con il
-- membro della wiki): solo la persona legge e scrive le sue pagine; una pagina condivisa si legge
-- anche dagli altri membri, ma resta modificabile solo da chi la possiede. La wiki si cancella con
-- il membro (chiavi esterne in cascata) e quando il membro viene anonimizzato (trigger).

create table wiki_pages (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  member_id uuid not null,
  title text not null check (length(title) between 1 and 120),
  body text not null default '' check (length(body) <= 50000),
  version integer not null default 1 check (version >= 1),
  -- Chi ha fatto l'ultima modifica: la persona o l'assistente.
  updated_by text not null default 'persona' check (updated_by in ('persona','assistente')),
  shared boolean not null default false,
  -- Chiave della pagina Knowledge creata promuovendo questa (l'ultima).
  promoted_key text check (promoted_key is null or length(promoted_key) <= 40),
  deleted_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
-- Il titolo identifica la pagina dentro la wiki della persona (l'assistente la nomina così).
create unique index wiki_pages_title_uq on wiki_pages (tenant_id, member_id, lower(title)) where deleted_at is null;
create index on wiki_pages (tenant_id, member_id, updated_at desc);
create index on wiki_pages (tenant_id, updated_at) where shared and deleted_at is null;
select enable_tenant_rls('wiki_pages');
create policy wiki_pages_read on wiki_pages as restrictive for select
  using (member_id = (select app_member()) or (shared and deleted_at is null and (select app_member()) is not null));
create policy wiki_pages_insert on wiki_pages as restrictive for insert with check (member_id = (select app_member()));
create policy wiki_pages_update on wiki_pages as restrictive for update
  using (member_id = (select app_member())) with check (member_id = (select app_member()));
create policy wiki_pages_delete on wiki_pages as restrictive for delete using (member_id = (select app_member()));

-- Storia: una riga per modifica, con l'istantanea dopo la modifica. member_id è il proprietario
-- della pagina (per la RLS: la storia non si condivide).
create table wiki_revisions (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  page_id uuid not null,
  member_id uuid not null,
  version integer not null check (version >= 1),
  op text not null check (op in ('crea','modifica','elimina','ripristina','annulla')),
  author text not null check (author in ('persona','assistente')),
  title text not null check (length(title) between 1 and 120),
  body text not null default '' check (length(body) <= 50000),
  deleted boolean not null default false,
  note text not null default '' check (length(note) <= 500),
  added integer not null default 0,
  removed integer not null default 0,
  -- Per «annulla» e «ripristina»: la versione a cui si è tornati (0 = prima della creazione).
  restored_version integer,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  unique (tenant_id, page_id, version),
  foreign key (tenant_id, page_id) references wiki_pages (tenant_id, id) on delete cascade,
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on wiki_revisions (tenant_id, member_id, created_at desc);
select enable_tenant_rls('wiki_revisions');
create policy wiki_revisions_owner on wiki_revisions as restrictive for all
  using (member_id = (select app_member())) with check (member_id = (select app_member()));

-- Fonti aggiunte a mano («aggiungi alla wiki» da una scheda o da una risposta dell'assistente):
-- l'aggiornamento successivo le legge (con i permessi della persona) e le segna come usate.
create table wiki_sources (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  member_id uuid not null,
  kind text not null check (kind in ('elemento','risposta')),
  element_id uuid,
  page_id uuid,
  text text not null default '' check (length(text) <= 8000),
  note text not null default '' check (length(note) <= 500),
  created_at timestamptz not null default now(),
  used_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, page_id) references wiki_pages (tenant_id, id) on delete set null (page_id)
);
create index on wiki_sources (tenant_id, member_id) where used_at is null;
select enable_tenant_rls('wiki_sources');
create policy wiki_sources_owner on wiki_sources as restrictive for all
  using (member_id = (select app_member())) with check (member_id = (select app_member()));

-- Stato dell'aggiornamento con l'AI, per persona. auto: la persona lascia che l'assistente tenga
-- aggiornata la wiki (spento finché non lo accende: è lei a decidere di spendere il budget).
-- requested_at: «Aggiorna adesso»; running_until: un aggiornamento è in corso (scade da solo).
create table wiki_state (
  tenant_id uuid not null,
  member_id uuid not null,
  auto boolean not null default false,
  requested_at timestamptz,
  running_until timestamptz,
  last_run_at timestamptz,
  last_outcome text check (last_outcome is null or last_outcome in ('ok','nulla','errore')),
  last_error text check (last_error is null or length(last_error) <= 80),
  last_pages integer not null default 0,
  last_note text not null default '' check (length(last_note) <= 1000),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, member_id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade
);
create index on wiki_state (last_run_at) where auto;
select enable_tenant_rls('wiki_state');
create policy wiki_state_owner on wiki_state as restrictive for all
  using (member_id = (select app_member())) with check (member_id = (select app_member()));

-- Il membro anonimizzato perde la wiki (i dati personali non restano). La funzione gira con i
-- diritti del proprietario dello schema: le politiche sul membro non la fermano.
create or replace function wiki_forget_member() returns trigger language plpgsql
security definer set search_path = public, pg_temp as $$
begin
  if new.status = 'anonymized' and old.status is distinct from 'anonymized' then
    delete from wiki_sources where tenant_id = new.tenant_id and member_id = new.id;
    delete from wiki_pages where tenant_id = new.tenant_id and member_id = new.id;
    delete from wiki_state where tenant_id = new.tenant_id and member_id = new.id;
  end if;
  return new;
end $$;
revoke all on function wiki_forget_member() from public;
create trigger members_wiki_forget after update of status on members
  for each row execute function wiki_forget_member();
