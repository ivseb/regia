-- Controlli GRC vivi (ondata 3, proposta 4): l'efficacia di un controllo la propone il sistema,
-- da una regola dichiarativa sui fatti calcolati dal grafo (vulnerabilità oltre lo SLA, esiti
-- dei gate, rilasci senza SBOM, riesame degli accessi), e la conferma una persona con la nota.
-- Lo stato ufficiale (ext_control.effectiveness) cambia solo con la conferma.

-- Una regola per controllo: «efficace se…» e «a rischio se…» (la seconda prevale), nel linguaggio
-- di condizioni del motore; sources dice quali dati del grafo diventano evidenze automatiche.
create table grc_effectiveness_rules (
  tenant_id uuid not null,
  control_element_id uuid not null,
  name text not null default '' check (length(name) <= 200),
  effective_when text not null default '' check (length(effective_when) <= 2000),
  at_risk_when text not null default '' check (length(at_risk_when) <= 2000),
  sources text[] not null default '{}' check (sources <@ array['release','gate','sbom','rule']),
  active boolean not null default true,
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id, control_element_id),
  foreign key (tenant_id, control_element_id) references ext_control (tenant_id, element_id) on delete cascade,
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on grc_effectiveness_rules (tenant_id, updated_by);
select enable_tenant_rls('grc_effectiveness_rules');

-- Le decisioni su una proposta: confermata o non confermata, con la nota e i fatti di quel momento.
create table grc_effectiveness_decisions (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  control_element_id uuid not null,
  proposed text not null check (proposed in ('effective','at_risk')),
  reason jsonb not null default '[]',
  decision text not null check (decision in ('confirmed','not_confirmed')),
  previous text not null check (previous in ('not_assessed','effective','partial','ineffective')),
  resulting text not null check (resulting in ('not_assessed','effective','partial','ineffective')),
  note text not null default '' check (length(note) <= 10000),
  decided_by uuid,
  decided_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, control_element_id) references ext_control (tenant_id, element_id) on delete cascade,
  foreign key (tenant_id, decided_by) references members (tenant_id, id) on delete set null (decided_by)
);
create index on grc_effectiveness_decisions (tenant_id, control_element_id, decided_at desc);
create index on grc_effectiveness_decisions (tenant_id, decided_by);
select enable_tenant_rls('grc_effectiveness_decisions');
