-- GRC, parte rischi, terze parti e incidenti (visione §7.4-7.5; piano, passo 20).
--
-- Le tabelle di base sono già nello schema (risk_matrices, ext_risk, ext_ict_provider,
-- ict_contracts, ext_incident, regulatory_notifications): qui si completano con ciò che
-- serve al modulo "risk". Tutto è configurazione del tenant: matrici, livelli, soglia di
-- attenzione, colonne del registro dei fornitori, schemi di classificazione degli incidenti
-- con i loro criteri (linguaggio di condizioni) e le scadenze delle notifiche normative.
-- Nessun testo di norma è nel database: solo riferimenti e riassunti scritti da noi.
--
-- Le regole di notifica globali (notification_rules, legate ai cataloghi normativi) restano
-- per i cataloghi della conformità; le notifiche generate dagli schemi del tenant hanno
-- rule_id vuoto e portano schema e tappa (scheme_key, stage_key).

-- ---------------------------------------------------------------------
-- Matrici di rischio: probabilità × impatto, fasce di punteggio con il tono del tema.
-- likelihood_levels / impact_levels: [{"value": 1, "name": "Rara", "description": "…"}, …]
-- score_bands: [{"key": "basso", "name": "Basso", "min": 1, "max": 4, "tone": "ok"}, …]
-- I toni sono quelli del tema (ok, neutral, accent, danger): nessun colore nel database.
-- ---------------------------------------------------------------------
alter table risk_matrices
  add column key text,
  add column description text not null default '',
  add column is_default boolean not null default false,
  -- Soglia di attenzione: un rischio residuo con punteggio maggiore è "oltre la soglia".
  add column appetite_score integer check (appetite_score >= 0),
  add column created_at timestamptz not null default now(),
  add column updated_at timestamptz not null default now(),
  add column updated_by uuid,
  add constraint risk_matrices_key_check check (key ~ '^[a-z][a-z0-9_]{0,62}$'),
  add foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by);
create unique index risk_matrices_key_uq on risk_matrices (tenant_id, key);
create unique index risk_matrices_one_default on risk_matrices (tenant_id) where is_default;
create index on risk_matrices (tenant_id, updated_by);

-- ---------------------------------------------------------------------
-- Rischio ICT (elemento "risk"): valutazione inerente e residua, trattamento con piano e
-- scadenza, accettazione approvata con il motore delle approvazioni (politica
-- "@risk_acceptance", soglie sul punteggio residuo).
-- ---------------------------------------------------------------------
alter table ext_risk
  add column category text not null default '',
  add column inherent_score integer,
  add column residual_score integer,
  add column treatment_plan text not null default '',
  add column treatment_due date,
  add column acceptance_stage text not null default 'none'
    check (acceptance_stage in ('none','pending','accepted','rejected','expired')),
  add column approval_request_id uuid,
  add column accepted_at timestamptz,
  add column accepted_until date,
  add column assessed_at timestamptz,
  add column review_notice_sent_at timestamptz,
  add column treatment_notice_sent_at timestamptz,
  add column created_at timestamptz not null default now(),
  add foreign key (tenant_id, approval_request_id) references approval_requests (tenant_id, id) on delete set null (approval_request_id);
create index on ext_risk (tenant_id, residual_score);
create index on ext_risk (tenant_id, treatment_due);
create index on ext_risk (tenant_id, approval_request_id);

-- Impostazioni del modulo: preavvisi e colonne del registro delle informazioni.
create table risk_settings (
  tenant_id uuid not null references tenants(id) on delete cascade,
  -- Colonne dell'export del registro: [{"key": "fornitore.nome", "label": "Fornitore"}, …]
  register_columns jsonb not null default '[]',
  contract_notice_days integer not null default 90 check (contract_notice_days between 0 and 730),
  review_notice_days integer not null default 14 check (review_notice_days between 0 and 180),
  -- Preavviso delle notifiche normative, in ore prima della scadenza.
  notification_warning_hours integer not null default 4 check (notification_warning_hours between 0 and 168),
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on risk_settings (tenant_id, updated_by);
select enable_tenant_rls('risk_settings');

-- ---------------------------------------------------------------------
-- Fornitori ICT e contratti (registro delle informazioni). Il fornitore è un elemento
-- "ict_provider"; le applicazioni servite sono relazioni "provided_by" (applicazione →
-- fornitore). La criticità del fornitore è un fatto calcolato, non una colonna.
-- ---------------------------------------------------------------------
alter table ext_ict_provider
  add column created_at timestamptz not null default now();

alter table ict_contracts
  add column service_type text not null default '',
  add column services text not null default '',
  add column functions text not null default '',
  add column supports_critical_function boolean not null default false,
  add column criticality text check (criticality in ('low','medium','high','critical')),
  add column data_locations text[] not null default '{}',
  add column processing_locations text[] not null default '{}',
  -- [{"name": "…", "country": "IT", "service": "…", "lei": "…"}]
  add column subcontractors jsonb not null default '[]',
  add column exit_strategy text not null default '',
  add column exit_tested_on date,
  add column governing_law text,
  add column auto_renewal boolean not null default false,
  add column expiry_notice_sent_at timestamptz,
  add column created_by uuid,
  add column updated_at timestamptz not null default now(),
  add constraint ict_contracts_dates check (end_date is null or start_date is null or end_date >= start_date),
  add foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by);
create unique index ict_contracts_reference_uq on ict_contracts (tenant_id, lower(reference));
create index on ict_contracts (tenant_id, created_by);

create table ict_contract_applications (
  tenant_id uuid not null,
  contract_id uuid not null,
  application_id uuid not null,
  primary key (tenant_id, contract_id, application_id),
  foreign key (tenant_id, contract_id) references ict_contracts (tenant_id, id) on delete cascade,
  foreign key (tenant_id, application_id) references elements (tenant_id, id) on delete cascade
);
create index on ict_contract_applications (tenant_id, application_id);
select enable_tenant_rls('ict_contract_applications');

-- ---------------------------------------------------------------------
-- Incidenti (elemento "incident"): criteri di impatto, classificazione per schema,
-- timeline degli eventi, postmortem.
-- ---------------------------------------------------------------------
alter table ext_incident
  add column clients_affected integer check (clients_affected >= 0),
  add column clients_affected_pct numeric(6,2) check (clients_affected_pct between 0 and 100),
  add column downtime_minutes integer check (downtime_minutes >= 0),
  -- availability | integrity | confidentiality | authenticity
  add column data_loss text[] not null default '{}',
  add column economic_impact_cents bigint check (economic_impact_cents >= 0),
  add column member_states integer check (member_states >= 0),
  add column reputational boolean not null default false,
  add column cause text,
  add column recurring boolean not null default false,
  -- {"summary": "…", "root_cause": "…", "lessons": "…"}
  add column postmortem jsonb,
  add column postmortem_at timestamptz,
  add column postmortem_by uuid,
  add column created_at timestamptz not null default now(),
  add foreign key (tenant_id, postmortem_by) references members (tenant_id, id) on delete set null (postmortem_by);
create index on ext_incident (tenant_id, postmortem_by);

create table incident_events (
  tenant_id uuid not null,
  id uuid not null,
  incident_id uuid not null,
  at timestamptz not null,
  kind text not null check (kind in ('detected','classified','contained','resolved','update','communication','notification','action','closed')),
  text text not null default '',
  system boolean not null default false,
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, incident_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on incident_events (tenant_id, incident_id, at);
create index on incident_events (tenant_id, created_by);
select enable_tenant_rls('incident_events');

-- Schemi di classificazione normativa (per esempio DORA e NIS2), del tenant.
-- definition: {"criteria": [{"key", "name", "condition", "primary"}], "when": "<condizione>",
--              "stages": [{"key", "name", "due": [{"from": "detected|classified|stage:<chiave>", "hours"|"days": N}], "hint"}]}
create table incident_schemes (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null,
  key text not null check (key ~ '^[a-z][a-z0-9_]{0,62}$'),
  name text not null,
  outcome_name text not null,               -- es. "grave", "significativo"
  reference text not null default '',       -- riferimento alla norma, mai il testo
  description text not null default '',
  active boolean not null default true,
  ordinal integer not null default 0,
  definition jsonb not null,
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create unique index incident_schemes_key_uq on incident_schemes (tenant_id, key);
create index on incident_schemes (tenant_id, updated_by);
select enable_tenant_rls('incident_schemes');

-- Notifiche normative generate dagli schemi del tenant: una per tappa, con bozza,
-- contenuto, invio registrato a mano. due_at vuota = in attesa della tappa precedente.
alter table regulatory_notifications
  alter column rule_id drop not null,
  alter column due_at drop not null,
  add column scheme_key text,
  add column scheme_name text,
  add column stage_key text,
  add column stage_name text,
  add column ordinal integer not null default 0,
  add column content text not null default '',
  add column reference text,
  add column channel text,
  add column note text,
  add column due_rule jsonb,
  add column reminder_sent_at timestamptz,
  add column overdue_sent_at timestamptz,
  add column created_at timestamptz not null default now(),
  add column updated_at timestamptz not null default now();
create unique index regulatory_notifications_stage_uq on regulatory_notifications (tenant_id, incident_element_id, scheme_key, stage_key)
  where scheme_key is not null;

-- ---------------------------------------------------------------------
-- Approvazione dell'accettazione del rischio: nuovo soggetto delle richieste di
-- approvazione. Il vincolo si estende leggendo quello attuale, per non togliere i
-- soggetti aggiunti da altre migrazioni.
-- ---------------------------------------------------------------------
do $$
declare def text;
begin
  select pg_get_constraintdef(oid) into def from pg_constraint
  where conname = 'approval_requests_subject_check' and conrelid = 'approval_requests'::regclass;
  if def is not null and position('risk_acceptance' in def) = 0 then
    execute 'alter table approval_requests drop constraint approval_requests_subject_check';
    execute 'alter table approval_requests add constraint approval_requests_subject_check ' ||
      replace(def, '])', ', ''risk_acceptance''::text])');
  end if;
end $$;
