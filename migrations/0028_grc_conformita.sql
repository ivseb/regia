-- Conformità (piano, passo 20; replica/grc.md §Conformità).
--
-- Cataloghi normativi del tenant versionati (frameworks, framework_versions, requirements con
-- owner_tenant_id = tenant), controlli interni (elemento "control" con ext_control) che coprono
-- requisiti di più normative (control_requirements), applicabilità a perimetri (tenant, entità
-- legale, applicazione, processo) decisa a mano o da regole sui fatti (applicability, estesa),
-- evidenze manuali o automatiche con validità e firma di chi le valida (ext_evidence, estesa) e
-- collegamenti tra i requisiti di progetto e i requisiti normativi (grc_requirement_links).
--
-- I testi delle norme non sono qui: i cataloghi sono dati, caricati dai pacchetti di partenza
-- del modulo o importati (OSCAL, CSV, JSON).

-- ---------------------------------------------------------------------
-- 1. Libreria normativa: i cataloghi privati del tenant si scrivono sotto RLS.
--    I cataloghi di sistema (owner_tenant_id null) restano in sola lettura.
-- ---------------------------------------------------------------------
alter table frameworks add column description text not null default '';
alter table frameworks add column starter text;                      -- pacchetto di partenza da cui nasce
alter table framework_versions add column notes text not null default '';
alter table framework_versions add column published_at timestamptz;
alter table framework_versions add column created_by uuid;           -- membro del tenant proprietario (tabella globale: senza FK)
alter table framework_versions add column published_by uuid;

create unique index framework_versions_one_draft on framework_versions (framework_id) where status = 'draft';
create unique index framework_versions_one_published on framework_versions (framework_id) where status = 'published';

create policy grc_fv_tenant_write on framework_versions for all
  using (exists (select 1 from frameworks f where f.id = framework_id and f.owner_tenant_id = (select app_tenant())))
  with check (exists (select 1 from frameworks f where f.id = framework_id and f.owner_tenant_id = (select app_tenant())));
create policy grc_req_tenant_write on requirements for all
  using (exists (select 1 from framework_versions v join frameworks f on f.id = v.framework_id
                 where v.id = version_id and f.owner_tenant_id = (select app_tenant())))
  with check (exists (select 1 from framework_versions v join frameworks f on f.id = v.framework_id
                      where v.id = version_id and f.owner_tenant_id = (select app_tenant())));

-- Una versione pubblicata non cambia: si crea una nuova versione in bozza.
create or replace function grc_framework_versions_immutable() returns trigger language plpgsql as $$
begin
  if tg_op = 'DELETE' then
    if old.status <> 'draft' then
      raise exception 'la versione % del catalogo è pubblicata e non si può cancellare', old.version;
    end if;
    return old;
  end if;
  if old.status <> 'draft' then
    if new.version <> old.version or new.framework_id <> old.framework_id then
      raise exception 'la versione % del catalogo è pubblicata e non si può modificare', old.version;
    end if;
    if not (old.status = new.status or (old.status = 'published' and new.status = 'superseded')) then
      raise exception 'passaggio di stato del catalogo non ammesso: % -> %', old.status, new.status;
    end if;
  end if;
  return new;
end $$;
create trigger grc_framework_versions_immutable before update or delete on framework_versions
  for each row when (pg_trigger_depth() = 0) execute function grc_framework_versions_immutable();

create or replace function grc_requirements_draft_only() returns trigger language plpgsql as $$
declare st text;
begin
  select status into st from framework_versions where id = coalesce(new.version_id, old.version_id);
  if st is distinct from 'draft' then
    raise exception 'la versione del catalogo è pubblicata: i requisiti non si modificano (crea una nuova versione)';
  end if;
  return coalesce(new, old);
end $$;
create trigger grc_requirements_draft_only before insert or update or delete on requirements
  for each row when (pg_trigger_depth() = 0) execute function grc_requirements_draft_only();

-- ---------------------------------------------------------------------
-- 2. Controllo interno: elemento "control" dello spazio GRC.
-- ---------------------------------------------------------------------
create table ext_control (
  tenant_id uuid not null,
  element_id uuid not null,
  owner_id uuid,                            -- responsabile del controllo
  frequency text not null default 'annual'
    check (frequency in ('continuous','monthly','quarterly','semiannual','annual','biennial')),
  control_kind text not null default 'preventive' check (control_kind in ('preventive','detective','corrective')),
  automation text not null default 'manual' check (automation in ('manual','semi','automatic')),
  effectiveness text not null default 'not_assessed'
    check (effectiveness in ('not_assessed','effective','partial','ineffective')),
  effectiveness_note text not null default '' check (length(effectiveness_note) <= 10000),
  assessed_by uuid,
  assessed_at timestamptz,
  -- Raccolte automatiche a cui il controllo è iscritto (es. devsecops.release): le evidenze
  -- automatiche di quel collettore si collegano da sole. Le verifiche dei requisiti di progetto
  -- collegati ai requisiti normativi del controllo valgono sempre (grc_requirement_links).
  collectors text[] not null default '{}',
  created_at timestamptz not null default now(),
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, owner_id) references members (tenant_id, id) on delete set null (owner_id),
  foreign key (tenant_id, assessed_by) references members (tenant_id, id) on delete set null (assessed_by)
);
create index on ext_control (tenant_id, owner_id);
create index on ext_control (tenant_id, assessed_by);
select enable_tenant_rls('ext_control');

-- control_requirements esiste già (0001): il controllo → requisiti normativi coperti.
alter table control_requirements add column created_at timestamptz not null default now();
create index on control_requirements (tenant_id, control_element_id);

-- ---------------------------------------------------------------------
-- 3. Applicabilità a perimetri: requisito normativo o controllo × perimetro.
--    Le celle della matrice dei requisiti di progetto (requirement_element_id, 0020) restano.
-- ---------------------------------------------------------------------
alter table applicability
  add column control_element_id uuid,
  add column scope_kind text check (scope_kind in ('tenant','legal_entity','application','process')),
  add column target_org_unit_id uuid;
update applicability set scope_kind = case when target_element_id is null then 'tenant' else 'application' end
  where requirement_id is not null and scope_kind is null;
alter table applicability drop constraint applicability_requirement_kind;
alter table applicability
  add constraint applicability_subject check (num_nonnulls(requirement_id, requirement_element_id, control_element_id) = 1),
  add constraint applicability_scope check (requirement_element_id is not null or (scope_kind is not null and case scope_kind
      when 'tenant' then target_element_id is null and target_org_unit_id is null
      when 'legal_entity' then target_element_id is null and target_org_unit_id is not null
      else target_element_id is not null and target_org_unit_id is null end)),
  add constraint applicability_control_fk foreign key (tenant_id, control_element_id)
    references elements (tenant_id, id) on delete cascade,
  add constraint applicability_org_unit_fk foreign key (tenant_id, target_org_unit_id)
    references org_units (tenant_id, id) on delete cascade;
drop index applicability_uq;
create unique index applicability_grc_uq on applicability (tenant_id, coalesce(requirement_id, control_element_id), scope_kind,
  coalesce(target_element_id, target_org_unit_id, '00000000-0000-0000-0000-000000000000'::uuid))
  where requirement_element_id is null;
create index on applicability (tenant_id, control_element_id);
create index on applicability (tenant_id, target_org_unit_id);

-- Regole di applicabilità: un insieme di requisiti di una normativa (tutti o alcuni
-- riferimenti) oppure di controlli si applica ai perimetri i cui fatti soddisfano la
-- condizione. Un requisito citato da una regola attiva non vale più per tutto il tenant.
create table grc_applicability_rules (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  key text not null check (key ~ '^[a-z][a-z0-9_]{0,62}$'),
  name text not null check (length(name) between 1 and 200),
  description text not null default '' check (length(description) <= 2000),
  framework_key text,
  refs text[] not null default '{}',
  control_ids uuid[] not null default '{}',
  scope_kind text not null check (scope_kind in ('legal_entity','application','process')),
  condition text not null default '' check (length(condition) <= 2000),
  active boolean not null default true,
  ordinal integer not null default 0,
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  constraint grc_rule_target check ((framework_key is null) <> (cardinality(control_ids) = 0)),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create unique index grc_applicability_rules_key_uq on grc_applicability_rules (tenant_id, key);
create index on grc_applicability_rules (tenant_id, updated_by);
select enable_tenant_rls('grc_applicability_rules');

-- ---------------------------------------------------------------------
-- 4. Evidenze: manuali (allegato, link, pagina Knowledge, nota) o automatiche (rilasci,
--    collettori esterni). La validazione firma il contenuto con chi valida e quando.
-- ---------------------------------------------------------------------
alter table ext_evidence
  add column kind text not null default 'note'
    check (kind in ('attachment','link','knowledge_page','note','release','automatic')),
  add column url text check (url is null or length(url) <= 2000),
  add column page_element_id uuid,
  add column scope_kind text not null default 'tenant' check (scope_kind in ('tenant','legal_entity','application','process')),
  add column target_element_id uuid,
  add column target_org_unit_id uuid,
  add column external_ref text check (external_ref is null or length(external_ref) <= 200),
  add column status text not null default 'draft' check (status in ('draft','valid','rejected','revoked')),
  add column status_note text not null default '' check (length(status_note) <= 2000),
  add column validated_by uuid,
  add column validated_at timestamptz,
  add column signature text check (signature is null or signature ~ '^[0-9a-f]{64}$'),
  add column created_by uuid,
  add column created_at timestamptz not null default now(),
  add constraint ext_evidence_scope check (case scope_kind
      when 'tenant' then target_element_id is null and target_org_unit_id is null
      when 'legal_entity' then target_element_id is null and target_org_unit_id is not null
      else target_element_id is not null and target_org_unit_id is null end),
  add constraint ext_evidence_signed check (status <> 'valid' or signature is not null),
  add constraint ext_evidence_page_fk foreign key (tenant_id, page_element_id)
    references elements (tenant_id, id) on delete set null (page_element_id),
  add constraint ext_evidence_target_fk foreign key (tenant_id, target_element_id)
    references elements (tenant_id, id) on delete cascade,
  add constraint ext_evidence_org_unit_fk foreign key (tenant_id, target_org_unit_id)
    references org_units (tenant_id, id) on delete cascade,
  add constraint ext_evidence_validated_by_fk foreign key (tenant_id, validated_by)
    references members (tenant_id, id) on delete set null (validated_by),
  add constraint ext_evidence_created_by_fk foreign key (tenant_id, created_by)
    references members (tenant_id, id) on delete set null (created_by);
create unique index ext_evidence_external_uq on ext_evidence (tenant_id, collector, external_ref) where external_ref is not null;
create index on ext_evidence (tenant_id, page_element_id);
create index on ext_evidence (tenant_id, target_element_id);
create index on ext_evidence (tenant_id, target_org_unit_id);
create index on ext_evidence (tenant_id, validated_by);
create index on ext_evidence (tenant_id, created_by);

-- Un'evidenza vale per uno o più controlli: si carica una volta sola.
create table grc_evidence_controls (
  tenant_id uuid not null,
  evidence_element_id uuid not null,
  control_element_id uuid not null,
  created_at timestamptz not null default now(),
  primary key (tenant_id, evidence_element_id, control_element_id),
  foreign key (tenant_id, evidence_element_id) references ext_evidence (tenant_id, element_id) on delete cascade,
  foreign key (tenant_id, control_element_id) references ext_control (tenant_id, element_id) on delete cascade
);
create index on grc_evidence_controls (tenant_id, control_element_id);
select enable_tenant_rls('grc_evidence_controls');

-- ---------------------------------------------------------------------
-- 5. Requisiti di progetto (ext_requirement, 0020) → requisiti normativi, per chiave della
--    normativa e riferimento: valgono anche dopo una nuova versione del catalogo. Le voci dei
--    cataloghi dei requisiti possono già dichiararli (campo "regulatory" della voce).
-- ---------------------------------------------------------------------
create table grc_requirement_links (
  tenant_id uuid not null,
  requirement_element_id uuid not null,
  framework_key text not null check (framework_key ~ '^[a-z][a-z0-9_]{0,62}$'),
  ref text not null check (length(ref) between 1 and 64),
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, requirement_element_id, framework_key, ref),
  foreign key (tenant_id, requirement_element_id) references ext_requirement (tenant_id, element_id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on grc_requirement_links (tenant_id, framework_key, ref);
create index on grc_requirement_links (tenant_id, created_by);
select enable_tenant_rls('grc_requirement_links');

-- ---------------------------------------------------------------------
-- 6. Impostazioni del tenant.
-- ---------------------------------------------------------------------
create table grc_settings (
  tenant_id uuid not null references tenants(id) on delete cascade,
  expiry_notice_days integer not null default 30 check (expiry_notice_days between 0 and 365),
  default_validity_days integer not null default 365 check (default_validity_days between 1 and 1825),
  -- Tipi di elemento che fanno da perimetro "processo" (l'architettura enterprise li fornirà).
  process_types text[] not null default '{business_process}',
  recomputed_at timestamptz,
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on grc_settings (tenant_id, updated_by);
select enable_tenant_rls('grc_settings');
