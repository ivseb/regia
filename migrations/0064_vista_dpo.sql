-- Ondata 3, «Vista DPO» (replica/usabilita/governo.md, C8): registro dei trattamenti (art. 30 GDPR, da
-- verificare), DPIA (art. 35, da verificare) e il collegamento delle violazioni dei dati agli incidenti
-- esistenti. Le violazioni NON hanno una tabella propria: sono gli incidenti che toccano dati personali;
-- qui stanno solo il segno messo a mano dal DPO e i trattamenti coinvolti.

create table privacy_treatments (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  name text not null check (length(name) between 1 and 120),
  purpose text not null default '' check (length(purpose) <= 4000),
  -- Base giuridica (art. 6 GDPR); vuota = da indicare.
  legal_basis text check (legal_basis in ('consent','contract','legal_obligation','vital_interests','public_task','legitimate_interests')),
  legal_basis_note text not null default '' check (length(legal_basis_note) <= 2000),
  data_subjects text[] not null default '{}',
  data_categories text[] not null default '{}',
  -- Criteri che rendono necessaria la DPIA (art. 35): categorie particolari, larga scala, profilazione, giudizio del DPO.
  special_categories boolean not null default false,
  large_scale boolean not null default false,
  profiling boolean not null default false,
  high_risk boolean not null default false,
  recipients text not null default '' check (length(recipients) <= 2000),
  transfers_extra_eu boolean not null default false,
  transfer_details text not null default '' check (length(transfer_details) <= 2000),
  retention text not null default '' check (length(retention) <= 1000),
  owner_id uuid,
  status text not null default 'active' check (status in ('active','archived')),
  created_by uuid,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, owner_id) references members (tenant_id, id) on delete set null (owner_id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create unique index privacy_treatments_name_uq on privacy_treatments (tenant_id, lower(name));
create index on privacy_treatments (tenant_id, owner_id);
create index on privacy_treatments (tenant_id, created_by);
select enable_tenant_rls('privacy_treatments');

-- Le applicazioni (elementi del catalogo) che supportano un trattamento.
create table privacy_treatment_apps (
  tenant_id uuid not null,
  treatment_id uuid not null,
  application_id uuid not null,
  primary key (tenant_id, treatment_id, application_id),
  foreign key (tenant_id, treatment_id) references privacy_treatments (tenant_id, id) on delete cascade,
  foreign key (tenant_id, application_id) references elements (tenant_id, id) on delete cascade
);
create index on privacy_treatment_apps (tenant_id, application_id);
select enable_tenant_rls('privacy_treatment_apps');

-- La DPIA di un trattamento (una sola). review_id: la valutazione «DPIA preliminare» già compilata
-- con il questionario delle valutazioni, riusata invece di duplicarla.
create table privacy_dpias (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  treatment_id uuid not null,
  status text not null default 'draft' check (status in ('draft','in_review','approved')),
  description text not null default '' check (length(description) <= 10000),
  necessity text not null default '' check (length(necessity) <= 10000),
  risk_assessment text not null default '' check (length(risk_assessment) <= 10000),
  dpo_opinion text not null default '' check (length(dpo_opinion) <= 10000),
  review_id uuid,
  submitted_at timestamptz,
  approved_by uuid,
  approved_at timestamptz,
  created_by uuid,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, treatment_id) references privacy_treatments (tenant_id, id) on delete cascade,
  foreign key (tenant_id, review_id) references elements (tenant_id, id) on delete set null (review_id),
  foreign key (tenant_id, approved_by) references members (tenant_id, id) on delete set null (approved_by),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create unique index privacy_dpias_treatment_uq on privacy_dpias (tenant_id, treatment_id);
create index on privacy_dpias (tenant_id, review_id);
create index on privacy_dpias (tenant_id, approved_by);
create index on privacy_dpias (tenant_id, created_by);
select enable_tenant_rls('privacy_dpias');

-- Rischi del registro rischi e controlli GRC collegati alla DPIA.
create table privacy_dpia_risks (
  tenant_id uuid not null,
  dpia_id uuid not null,
  risk_id uuid not null,
  primary key (tenant_id, dpia_id, risk_id),
  foreign key (tenant_id, dpia_id) references privacy_dpias (tenant_id, id) on delete cascade,
  foreign key (tenant_id, risk_id) references elements (tenant_id, id) on delete cascade
);
create index on privacy_dpia_risks (tenant_id, risk_id);
select enable_tenant_rls('privacy_dpia_risks');

create table privacy_dpia_controls (
  tenant_id uuid not null,
  dpia_id uuid not null,
  control_id uuid not null,
  primary key (tenant_id, dpia_id, control_id),
  foreign key (tenant_id, dpia_id) references privacy_dpias (tenant_id, id) on delete cascade,
  foreign key (tenant_id, control_id) references elements (tenant_id, id) on delete cascade
);
create index on privacy_dpia_controls (tenant_id, control_id);
select enable_tenant_rls('privacy_dpia_controls');

-- Violazioni dei dati: il segno del DPO su un incidente (personal = true: tocca dati personali;
-- false: escluso anche se i criteri automatici lo proporrebbero) e i trattamenti coinvolti.
create table privacy_incident_marks (
  tenant_id uuid not null,
  incident_id uuid not null,
  personal boolean not null,
  note text not null default '' check (length(note) <= 2000),
  -- Notifica all'autorità (art. 33): quando è partita, oppure perché non serve (art. 33, par. 1: rischio improbabile).
  notified_at timestamptz,
  notified_by uuid,
  not_required boolean not null default false,
  not_required_reason text not null default '' check (length(not_required_reason) <= 2000),
  marked_by uuid,
  marked_at timestamptz not null default now(),
  primary key (tenant_id, incident_id),
  foreign key (tenant_id, notified_by) references members (tenant_id, id) on delete set null (notified_by),
  foreign key (tenant_id, incident_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, marked_by) references members (tenant_id, id) on delete set null (marked_by)
);
create index on privacy_incident_marks (tenant_id, marked_by);
create index on privacy_incident_marks (tenant_id, notified_by);
select enable_tenant_rls('privacy_incident_marks');

create table privacy_incident_treatments (
  tenant_id uuid not null,
  incident_id uuid not null,
  treatment_id uuid not null,
  primary key (tenant_id, incident_id, treatment_id),
  foreign key (tenant_id, incident_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, treatment_id) references privacy_treatments (tenant_id, id) on delete cascade
);
create index on privacy_incident_treatments (tenant_id, treatment_id);
select enable_tenant_rls('privacy_incident_treatments');
