-- Knowledge (piano, passo 19; replica/knowledge.md).
-- Le pagine sono elementi di tipo "page" negli spazi di kind "knowledge": gerarchia con
-- parent_id, ordinamento manuale con rank, testo in elements.body ({"md": ...}) e testo piano
-- in body_text per la ricerca. Qui ci sono solo ciò che il motore non ha già: l'accesso per
-- spazio, l'estensione della pagina, i riferimenti citati nel testo e i template.

-- ---------------------------------------------------------------------
-- 1. Accesso per spazio
-- Uno spazio con settings.access = 'restricted' si legge e si scrive solo con un ruolo
-- assegnato sullo spazio (role_assignments.space_id), diretto o tramite gruppo, oppure con il
-- ruolo di amministratore del tenant ("*"). Uno spazio aperto segue i permessi del tenant, e i
-- ruoli sullo spazio li estendono (es. un gruppo che scrive solo in DOC).
-- ---------------------------------------------------------------------
create index spaces_restricted_idx on spaces (tenant_id) where settings->>'access' = 'restricted';

-- Permessi del membro sullo spazio: quelli dei ruoli assegnati sullo spazio, più "*" se il
-- membro è amministratore del tenant. Gira sotto RLS (security invoker).
create or replace function space_access(p_space uuid, p_member uuid) returns text[] language sql stable as $$
  select coalesce(array_agg(distinct p), '{}') from (
    select unnest(r.permissions) as p
    from role_assignments ra
    join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
    where ra.space_id = p_space
      and (ra.member_id = p_member or ra.group_id in (select gm.group_id from group_members gm where gm.member_id = p_member))
    union all
    select '*'
    from role_assignments ra
    join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
    where ra.space_id is null and '*' = any(r.permissions)
      and (ra.member_id = p_member or ra.group_id in (select gm.group_id from group_members gm where gm.member_id = p_member))
  ) x
$$;

create or replace function space_restricted(p_space uuid) returns boolean language sql stable as $$
  select coalesce((select settings->>'access' = 'restricted' from spaces where id = p_space), false)
$$;

-- Regola dello spazio per leggere un elemento: 1 = concesso dallo spazio, -1 = negato (spazio
-- riservato senza ruolo), 0 = valgono i permessi del tenant. Usata dalle notifiche.
create or replace function element_space_read(p_element uuid, p_member uuid) returns smallint language sql stable as $$
  select case
    when x.perms && array['*', 'element.read', 'element.write'] then 1::smallint
    when x.restricted then -1::smallint
    else 0::smallint end
  from (select space_restricted(e.space_id) as restricted, space_access(e.space_id, p_member) as perms
        from elements e where e.id = p_element) x
$$;

-- ---------------------------------------------------------------------
-- 2. Pagine
-- ---------------------------------------------------------------------
create table kb_pages (
  tenant_id uuid not null,
  element_id uuid not null,
  version integer not null default 1 check (version > 0),  -- ultima versione in page_versions
  template_key text,
  -- Documento controllato: ciclo di approvazione (processo "page_controlled"), revisione periodica.
  controlled boolean not null default false,
  review_months integer check (review_months between 1 and 60),
  next_review date,
  reminded_for date,                        -- scadenza per cui è già partito il promemoria
  approved_version integer,                 -- versione approvata in vigore (resta leggibile)
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
);
create index kb_pages_review_idx on kb_pages (tenant_id, next_review) where controlled and next_review is not null;
select enable_tenant_rls('kb_pages');

-- Nota della versione (chi e quando ci sono già: author_id, created_at).
alter table page_versions add column note text not null default '';
create index page_versions_element_idx on page_versions (tenant_id, element_id, version desc);

-- Chi tiene il blocco e da quando (expires_at c'è già).
alter table page_locks add column acquired_at timestamptz not null default now();

-- Elementi citati nel testo delle pagine (DEM-12, WEB-3): servono a «Documenti collegati».
create table kb_refs (
  tenant_id uuid not null,
  page_id uuid not null,
  target_id uuid not null,
  primary key (tenant_id, page_id, target_id),
  foreign key (tenant_id, page_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, target_id) references elements (tenant_id, id) on delete cascade
);
create index kb_refs_target_idx on kb_refs (tenant_id, target_id);
select enable_tenant_rls('kb_refs');

-- ---------------------------------------------------------------------
-- 3. Template di pagina: space_id null = disponibili in tutti gli spazi (pacchetto di
-- partenza), altrimenti solo nello spazio.
-- ---------------------------------------------------------------------
create table kb_templates (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  space_id uuid,
  key text not null check (key ~ '^[a-z][a-z0-9_]{0,62}$'),
  name text not null,
  description text not null default '',
  body text not null default '',
  controlled boolean not null default false,  -- la pagina nasce come documento controllato
  review_months integer check (review_months between 1 and 60),
  ordinal integer not null default 0,
  archived boolean not null default false,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, space_id) references spaces (tenant_id, id) on delete cascade
);
create unique index kb_templates_key_uq on kb_templates (tenant_id, coalesce(space_id, '00000000-0000-0000-0000-000000000000'::uuid), key);
create index kb_templates_space_idx on kb_templates (tenant_id, space_id);
create trigger kb_templates_updated before update on kb_templates for each row execute function set_updated_at();
select enable_tenant_rls('kb_templates');

-- Albero delle pagine: figli nell'ordine manuale (rank) per spazio e genitore.
create index elements_space_parent_rank_idx on elements (tenant_id, space_id, parent_id, (rank collate "C"))
  where deleted_at is null;

-- Approvazione dei documenti controllati: soggetto proprio, con la separazione dei compiti
-- (chi invia in revisione non decide).
alter table approval_requests drop constraint if exists approval_requests_subject_check;
alter table approval_requests add constraint approval_requests_subject_check
  check (subject in ('stage_gate','budget','budget_transfer','change_request','assignment','exception','kb_document'));
