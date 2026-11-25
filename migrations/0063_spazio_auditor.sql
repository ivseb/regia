-- Spazio dell'auditor (ondata 3, C6): l'auditor lavora su un incarico (perimetro e periodo),
-- estrae campioni di evidenze, chiede evidenze al responsabile del controllo, commenta e apre
-- rilievi che chiude lui. Scrive solo queste tabelle: il resto della conformità resta in sola
-- lettura per lui (permesso grc.audit, dato al ruolo auditor).

-- Il ruolo auditor dei tenant esistenti: grc.audit in più, solo se ha già grc.read.
update roles set permissions = permissions || array['grc.audit']
  where key = 'auditor' and permissions @> array['grc.read'] and not permissions @> array['grc.audit'];

-- Incarico: norma facoltativa, periodo, e un perimetro di controlli e applicazioni (grc_audit_scope).
-- Senza controlli indicati il perimetro è tutti i controlli che coprono la norma (o tutti).
create table grc_audit_engagements (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  title text not null check (length(title) between 1 and 200),
  framework_key text check (framework_key is null or framework_key ~ '^[a-z][a-z0-9_]{0,62}$'),
  period_from date not null,
  period_to date not null,
  status text not null default 'open' check (status in ('open','closed')),
  note text not null default '' check (length(note) <= 10000),
  created_by uuid,
  created_at timestamptz not null default now(),
  closed_at timestamptz,
  primary key (tenant_id, id),
  check (period_to >= period_from),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on grc_audit_engagements (tenant_id, created_by);
create index on grc_audit_engagements (tenant_id, status, created_at desc);
select enable_tenant_rls('grc_audit_engagements');

create table grc_audit_scope (
  tenant_id uuid not null,
  engagement_id uuid not null,
  element_id uuid not null,
  kind text not null check (kind in ('control','application')),
  primary key (tenant_id, engagement_id, element_id),
  foreign key (tenant_id, engagement_id) references grc_audit_engagements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
);
create index on grc_audit_scope (tenant_id, element_id);
select enable_tenant_rls('grc_audit_scope');

-- Esame di un controllo nell'incarico: l'auditor lo segna come esaminato (con la conclusione).
create table grc_audit_reviews (
  tenant_id uuid not null,
  engagement_id uuid not null,
  control_element_id uuid not null,
  conclusion text not null default 'reviewed' check (conclusion in ('reviewed','to_clarify')),
  note text not null default '' check (length(note) <= 10000),
  reviewed_by uuid,
  reviewed_at timestamptz not null default now(),
  primary key (tenant_id, engagement_id, control_element_id),
  foreign key (tenant_id, engagement_id) references grc_audit_engagements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, control_element_id) references ext_control (tenant_id, element_id) on delete cascade,
  foreign key (tenant_id, reviewed_by) references members (tenant_id, id) on delete set null (reviewed_by)
);
create index on grc_audit_reviews (tenant_id, control_element_id);
create index on grc_audit_reviews (tenant_id, reviewed_by);
select enable_tenant_rls('grc_audit_reviews');

-- Campione: N evidenze del periodo estratte a caso con un seme registrato (stesso seme, stesso campione).
create table grc_audit_samples (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  engagement_id uuid not null,
  control_element_id uuid not null,
  seed bigint not null,
  size integer not null check (size between 1 and 200),
  population integer not null check (population >= 0),
  drawn_by uuid,
  drawn_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, engagement_id) references grc_audit_engagements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, control_element_id) references ext_control (tenant_id, element_id) on delete cascade,
  foreign key (tenant_id, drawn_by) references members (tenant_id, id) on delete set null (drawn_by)
);
create index on grc_audit_samples (tenant_id, engagement_id, control_element_id);
create index on grc_audit_samples (tenant_id, control_element_id);
create index on grc_audit_samples (tenant_id, drawn_by);
select enable_tenant_rls('grc_audit_samples');

create table grc_audit_sample_items (
  tenant_id uuid not null,
  sample_id uuid not null,
  position integer not null,
  evidence_element_id uuid not null,
  outcome text not null default 'pending' check (outcome in ('pending','compliant','non_compliant','to_clarify')),
  note text not null default '' check (length(note) <= 4000),
  assessed_by uuid,
  assessed_at timestamptz,
  primary key (tenant_id, sample_id, position),
  foreign key (tenant_id, sample_id) references grc_audit_samples (tenant_id, id) on delete cascade,
  foreign key (tenant_id, evidence_element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, assessed_by) references members (tenant_id, id) on delete set null (assessed_by)
);
create index on grc_audit_sample_items (tenant_id, evidence_element_id);
create index on grc_audit_sample_items (tenant_id, assessed_by);
select enable_tenant_rls('grc_audit_sample_items');

-- Richieste di evidenza al responsabile del controllo, con scadenza e risposta (testo e link).
create table grc_audit_requests (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  engagement_id uuid not null,
  control_element_id uuid not null,
  evidence_element_id uuid,
  title text not null check (length(title) between 1 and 200),
  body text not null default '' check (length(body) <= 10000),
  due_date date,
  assignee_id uuid,
  status text not null default 'open' check (status in ('open','answered','closed')),
  response text not null default '' check (length(response) <= 10000),
  response_url text check (response_url is null or length(response_url) <= 2000),
  response_evidence_id uuid,
  requested_by uuid,
  requested_at timestamptz not null default now(),
  answered_by uuid,
  answered_at timestamptz,
  closed_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, engagement_id) references grc_audit_engagements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, control_element_id) references ext_control (tenant_id, element_id) on delete cascade,
  foreign key (tenant_id, evidence_element_id) references elements (tenant_id, id) on delete set null (evidence_element_id),
  foreign key (tenant_id, response_evidence_id) references elements (tenant_id, id) on delete set null (response_evidence_id),
  foreign key (tenant_id, assignee_id) references members (tenant_id, id) on delete set null (assignee_id),
  foreign key (tenant_id, requested_by) references members (tenant_id, id) on delete set null (requested_by),
  foreign key (tenant_id, answered_by) references members (tenant_id, id) on delete set null (answered_by)
);
create index on grc_audit_requests (tenant_id, engagement_id, status);
create index on grc_audit_requests (tenant_id, control_element_id);
create index on grc_audit_requests (tenant_id, assignee_id, status);
create index on grc_audit_requests (tenant_id, evidence_element_id);
create index on grc_audit_requests (tenant_id, response_evidence_id);
create index on grc_audit_requests (tenant_id, requested_by);
create index on grc_audit_requests (tenant_id, answered_by);
select enable_tenant_rls('grc_audit_requests');

-- Commenti in linea sul controllo o su una sua evidenza.
create table grc_audit_comments (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  control_element_id uuid not null,
  evidence_element_id uuid,
  engagement_id uuid,
  body text not null check (length(body) between 1 and 4000),
  author_id uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, control_element_id) references ext_control (tenant_id, element_id) on delete cascade,
  foreign key (tenant_id, evidence_element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, engagement_id) references grc_audit_engagements (tenant_id, id) on delete set null (engagement_id),
  foreign key (tenant_id, author_id) references members (tenant_id, id) on delete set null (author_id)
);
create index on grc_audit_comments (tenant_id, control_element_id, created_at);
create index on grc_audit_comments (tenant_id, evidence_element_id);
create index on grc_audit_comments (tenant_id, engagement_id);
create index on grc_audit_comments (tenant_id, author_id);
select enable_tenant_rls('grc_audit_comments');

-- Rilievi: aperti dall'auditor su un controllo, piano d'azione del responsabile, chiusura dell'auditor.
create table grc_audit_findings (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  engagement_id uuid not null,
  control_element_id uuid not null,
  severity text not null check (severity in ('low','medium','high','critical')),
  title text not null check (length(title) between 1 and 200),
  description text not null default '' check (length(description) <= 10000),
  status text not null default 'open' check (status in ('open','plan_proposed','closed')),
  action_plan text not null default '' check (length(action_plan) <= 10000),
  action_due date,
  plan_by uuid,
  plan_at timestamptz,
  closure_note text not null default '' check (length(closure_note) <= 10000),
  opened_by uuid,
  opened_at timestamptz not null default now(),
  closed_by uuid,
  closed_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, engagement_id) references grc_audit_engagements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, control_element_id) references ext_control (tenant_id, element_id) on delete cascade,
  foreign key (tenant_id, plan_by) references members (tenant_id, id) on delete set null (plan_by),
  foreign key (tenant_id, opened_by) references members (tenant_id, id) on delete set null (opened_by),
  foreign key (tenant_id, closed_by) references members (tenant_id, id) on delete set null (closed_by)
);
create index on grc_audit_findings (tenant_id, engagement_id, status);
create index on grc_audit_findings (tenant_id, control_element_id, status);
create index on grc_audit_findings (tenant_id, plan_by);
create index on grc_audit_findings (tenant_id, opened_by);
create index on grc_audit_findings (tenant_id, closed_by);
select enable_tenant_rls('grc_audit_findings');
