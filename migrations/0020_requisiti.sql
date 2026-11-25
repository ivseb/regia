-- Requisiti, matrice di tracciabilità, verifica e deroghe (sdlc.md §6, §8; piano, passi 5 e 6).
--
-- Il requisito è un elemento "requirement" figlio della richiesta (ambito di progetto) o
-- dell'impatto (ambito di applicazione), con l'estensione ext_requirement. La cella della
-- matrice (requisito × impatto) estende la tabella applicability già presente nello schema:
-- un requisito di progetto ha una cella per ogni impatto della richiesta, ognuna con il suo
-- stato. Le verifiche sono firmate e immutabili; la deroga è un elemento "exception"
-- approvato con il motore delle approvazioni.
--
-- La libreria normativa globale (requirements, control_requirements) resta com'è: è il
-- catalogo GRC dei testi delle norme, che arriverà con il passo 20 del piano.

-- ---------------------------------------------------------------------
-- Cataloghi di requisiti: ogni riga è una versione di un catalogo (chiave), come i modelli
-- di questionario. Al massimo una bozza e una versione corrente; le pubblicate sono immutabili.
-- La definizione contiene le voci e le baseline per classe di applicazione.
-- ---------------------------------------------------------------------
create table requirement_catalogs (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  key text not null check (key ~ '^[a-z][a-z0-9_]{0,62}$'),
  version integer not null check (version > 0),
  status text not null default 'draft' check (status in ('draft','published','superseded')),
  name text not null,
  description text not null default '',
  definition jsonb not null,
  notes text not null default '',
  created_by uuid,
  created_at timestamptz not null default now(),
  published_by uuid,
  published_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by),
  foreign key (tenant_id, published_by) references members (tenant_id, id) on delete set null (published_by)
);
create unique index requirement_catalogs_version_uq on requirement_catalogs (tenant_id, key, version);
create unique index requirement_catalogs_one_draft on requirement_catalogs (tenant_id, key) where status = 'draft';
create unique index requirement_catalogs_one_published on requirement_catalogs (tenant_id, key) where status = 'published';
create index on requirement_catalogs (tenant_id, created_by);
create index on requirement_catalogs (tenant_id, published_by);
select enable_tenant_rls('requirement_catalogs');

create or replace function requirement_catalogs_immutable() returns trigger language plpgsql as $$
begin
  if old.status <> 'draft' then
    if new.definition is distinct from old.definition or new.name <> old.name or new.description <> old.description
       or new.key <> old.key or new.version <> old.version then
      raise exception 'la versione % del catalogo di requisiti è pubblicata e non si può modificare', old.version;
    end if;
    if not (old.status = new.status or (old.status = 'published' and new.status = 'superseded')) then
      raise exception 'passaggio di stato del catalogo non ammesso: % -> %', old.status, new.status;
    end if;
  end if;
  return new;
end $$;
create trigger requirement_catalogs_immutable before update on requirement_catalogs
  for each row execute function requirement_catalogs_immutable();

create or replace function requirement_catalogs_no_delete() returns trigger language plpgsql as $$
begin
  if old.status <> 'draft' then
    raise exception 'la versione % del catalogo di requisiti è pubblicata e non si può cancellare', old.version;
  end if;
  return old;
end $$;
create trigger requirement_catalogs_no_delete before delete on requirement_catalogs
  for each row when (pg_trigger_depth() = 0)
  execute function requirement_catalogs_no_delete();

-- ---------------------------------------------------------------------
-- Impostazioni del tenant: livelli di rischio residuo delle deroghe, durata massima,
-- preavviso della scadenza. La politica di approvazione delle deroghe sta in
-- approval_policies (stage '@exception'), con soglie sul punteggio di rischio.
-- ---------------------------------------------------------------------
create table requirement_settings (
  tenant_id uuid not null references tenants(id) on delete cascade,
  -- [{"key": "basso", "name": "Basso", "score": 1}, ...] in ordine crescente di punteggio.
  risk_levels jsonb not null,
  max_exception_days integer not null default 365 check (max_exception_days between 1 and 1095),
  expiry_notice_days integer not null default 14 check (expiry_notice_days between 0 and 120),
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on requirement_settings (tenant_id, updated_by);
select enable_tenant_rls('requirement_settings');

-- ---------------------------------------------------------------------
-- Il requisito. Il testo è il corpo dell'elemento (body_text), il titolo è il titolo.
-- ---------------------------------------------------------------------
create table ext_requirement (
  tenant_id uuid not null,
  element_id uuid not null,
  request_id uuid not null,
  impact_id uuid,
  scope text not null check (scope in ('project','application')),
  -- Origine: prescrizione accettata di una valutazione, voce di catalogo (baseline), inserimento manuale.
  origin text not null check (origin in ('prescription','catalog','manual')),
  review_id uuid,
  prescription_id uuid,
  catalog_key text,
  catalog_version integer,
  catalog_item_key text,
  baseline_key text,
  category text not null default '',
  -- Criterio di verifica: manuale (firma di chi verifica) o automatico da evidenza
  -- (devsecops.md §3.5: il controllo indicato in verification_check, quando arriverà l'integrazione).
  verification_method text not null default 'manual' check (verification_method in ('manual','evidence')),
  verification_check text,
  verification_criterion text not null default '',
  -- Chi verifica: un team valutatore (il suo gruppo) oppure un ruolo sulla richiesta o sull'impatto.
  verifier_team_id uuid,
  verifier_role text,
  -- Momento del ciclo, mosso dalle azioni requirement_stage del processo del requisito.
  stage text not null default 'proposed'
    check (stage in ('proposed','accepted','implementing','to_verify','verified','waived','not_applicable')),
  created_at timestamptz not null default now(),
  primary key (tenant_id, element_id),
  constraint ext_requirement_scope check ((scope = 'project') = (impact_id is null)),
  constraint ext_requirement_verifier check (verifier_team_id is not null or verifier_role is not null),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, request_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, impact_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, review_id) references elements (tenant_id, id) on delete set null (review_id),
  foreign key (tenant_id, prescription_id) references review_prescriptions (tenant_id, id) on delete set null (prescription_id),
  foreign key (tenant_id, verifier_team_id) references review_teams (tenant_id, id) on delete restrict
);
create index on ext_requirement (tenant_id, request_id);
create index on ext_requirement (tenant_id, impact_id);
create index on ext_requirement (tenant_id, review_id);
create index on ext_requirement (tenant_id, verifier_team_id);
-- Una prescrizione genera un solo requisito; una voce di catalogo una sola volta per oggetto.
create unique index ext_requirement_prescription_uq on ext_requirement (tenant_id, prescription_id) where prescription_id is not null;
create unique index ext_requirement_catalog_uq on ext_requirement (tenant_id, coalesce(impact_id, request_id), catalog_key, catalog_item_key)
  where origin = 'catalog';
select enable_tenant_rls('ext_requirement');

-- ---------------------------------------------------------------------
-- Matrice di tracciabilità: estensione di applicability. Una riga per requisito e impatto
-- (requirement_element_id + target_element_id), con lo stato della cella:
--   status   = applicable | not_applicable (con justification e decided_by già presenti)
--   progress = open (da realizzare) | to_verify | verified | waived (coperta da deroga)
-- Le righe della libreria normativa (requirement_id) restano possibili e non hanno progress.
-- ---------------------------------------------------------------------
alter table applicability alter column requirement_id drop not null;
alter table applicability
  add column requirement_element_id uuid,
  add column request_id uuid,
  add column progress text check (progress in ('open','to_verify','verified','waived')),
  add column implemented_by uuid,
  add column implemented_at timestamptz,
  add column implementation_note text not null default '',
  add column verified_by uuid,
  add column verified_at timestamptz,
  add column last_verification_id uuid,
  add column exception_id uuid,
  add column updated_at timestamptz not null default now();
alter table applicability
  add constraint applicability_requirement_kind check ((requirement_id is null) <> (requirement_element_id is null)),
  add constraint applicability_cell check (requirement_element_id is null
    or (target_element_id is not null and request_id is not null and progress is not null)),
  add constraint applicability_requirement_element_fk foreign key (tenant_id, requirement_element_id)
    references elements (tenant_id, id) on delete cascade,
  add constraint applicability_request_fk foreign key (tenant_id, request_id)
    references elements (tenant_id, id) on delete cascade,
  add constraint applicability_implemented_by_fk foreign key (tenant_id, implemented_by)
    references members (tenant_id, id) on delete set null (implemented_by),
  add constraint applicability_verified_by_fk foreign key (tenant_id, verified_by)
    references members (tenant_id, id) on delete set null (verified_by),
  add constraint applicability_exception_fk foreign key (tenant_id, exception_id)
    references elements (tenant_id, id) on delete set null (exception_id);
create unique index applicability_cell_uq on applicability (tenant_id, requirement_element_id, target_element_id)
  where requirement_element_id is not null;
create index applicability_request_idx on applicability (tenant_id, request_id) where requirement_element_id is not null;
create index applicability_to_verify_idx on applicability (tenant_id, requirement_element_id) where progress = 'to_verify';
create index on applicability (tenant_id, implemented_by);
create index on applicability (tenant_id, verified_by);
create index on applicability (tenant_id, exception_id);

-- ---------------------------------------------------------------------
-- Verifiche: esito, evidenze (nota, link, allegati del requisito, un elemento) e firma.
-- La firma è lo SHA-256 del contenuto verificato e di chi lo firma; le righe non si
-- modificano né si cancellano.
-- ---------------------------------------------------------------------
create table requirement_verifications (
  tenant_id uuid not null,
  id uuid not null,
  requirement_id uuid not null,
  impact_id uuid not null,
  outcome text not null check (outcome in ('verified','rejected')),
  method text not null default 'manual' check (method in ('manual','automatic')),
  collector text,
  note text not null default '' check (length(note) <= 10000),
  links text[] not null default '{}',
  attachment_ids uuid[] not null default '{}',
  evidence_element_id uuid,
  verified_by uuid,
  verified_at timestamptz not null default now(),
  signature text not null check (signature ~ '^[0-9a-f]{64}$'),
  primary key (tenant_id, id),
  foreign key (tenant_id, requirement_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, impact_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, evidence_element_id) references elements (tenant_id, id) on delete set null (evidence_element_id),
  foreign key (tenant_id, verified_by) references members (tenant_id, id) on delete set null (verified_by)
);
create index on requirement_verifications (tenant_id, requirement_id, verified_at);
create index on requirement_verifications (tenant_id, impact_id);
create index on requirement_verifications (tenant_id, evidence_element_id);
create index on requirement_verifications (tenant_id, verified_by);
select enable_tenant_rls('requirement_verifications');

create or replace function requirement_verifications_immutable() returns trigger language plpgsql as $$
begin
  raise exception 'le verifiche firmate non si modificano: se ne registra una nuova';
end $$;
create trigger requirement_verifications_no_update before update on requirement_verifications
  for each row when (pg_trigger_depth() = 0)
  execute function requirement_verifications_immutable();
create trigger requirement_verifications_no_delete before delete on requirement_verifications
  for each row when (pg_trigger_depth() = 0)
  execute function requirement_verifications_immutable();

-- ---------------------------------------------------------------------
-- Deroga: elemento "exception" figlio del requisito. Copre le celle degli impatti indicati
-- finché è in vigore; alla scadenza le celle tornano da realizzare.
-- ---------------------------------------------------------------------
create table ext_exception (
  tenant_id uuid not null,
  element_id uuid not null,
  requirement_id uuid not null,
  request_id uuid not null,
  impact_ids uuid[] not null check (cardinality(impact_ids) > 0),
  reason text not null check (length(reason) between 1 and 10000),
  risk_level text not null,
  risk_score integer not null check (risk_score between 0 and 100),
  compensating text not null default '' check (length(compensating) <= 10000),
  expires_on date not null,
  stage text not null default 'draft' check (stage in ('draft','pending','approved','rejected','expired','revoked')),
  approval_request_id uuid,
  requested_by uuid,
  approved_at timestamptz,
  closed_at timestamptz,
  notice_sent_at timestamptz,
  created_at timestamptz not null default now(),
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, requirement_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, request_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, approval_request_id) references approval_requests (tenant_id, id) on delete set null (approval_request_id),
  foreign key (tenant_id, requested_by) references members (tenant_id, id) on delete set null (requested_by)
);
create index on ext_exception (tenant_id, requirement_id);
create index on ext_exception (tenant_id, request_id);
create index on ext_exception (tenant_id, approval_request_id);
create index on ext_exception (tenant_id, requested_by);
-- Il job delle scadenze cerca solo le deroghe in vigore.
create index ext_exception_expiry_idx on ext_exception (expires_on) where stage = 'approved';
select enable_tenant_rls('ext_exception');

-- Le richieste di approvazione hanno un soggetto in più: la deroga.
alter table approval_requests drop constraint if exists approval_requests_subject_check;
alter table approval_requests add constraint approval_requests_subject_check
  check (subject in ('stage_gate','budget','budget_transfer','change_request','assignment','exception'));
