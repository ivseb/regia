-- Ondata 2, «dal problema all'azione» (replica/usabilita.md, proposta 5).
-- Il collegamento tra un problema (vulnerabilità, motivo del gate, requisito, aderenza mancante) e il
-- lavoro creato per risolverlo. Il problema non è sempre un elemento (un finding, una decisione del
-- gate), quindi il legame preciso sta qui; nel grafo il lavoro è collegato all'applicazione o al
-- requisito con la relazione «raised_from», così lo si vede anche dalla scheda dell'elemento di lavoro.
create table problem_links (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  source_kind text not null check (source_kind in ('finding', 'gate_decision', 'requirement', 'adherence')),
  source_ref text not null check (length(source_ref) between 1 and 200),
  source_label text not null default '',
  application_id uuid,
  action text not null check (action in ('work_item', 'exception', 'override', 'adoption_pr')),
  target_element_id uuid,
  target_ref text not null default '',
  target_label text not null default '',
  note text not null default '',
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, application_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, target_element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on problem_links (tenant_id, source_kind, source_ref);
create index on problem_links (tenant_id, target_element_id) where target_element_id is not null;
create index on problem_links (tenant_id, application_id);
create index on problem_links (tenant_id, created_by);
select enable_tenant_rls('problem_links');

-- Vista di crisi (proposta 3): le decisioni dell'incidente si registrano nella cronologia come un
-- tipo di evento a sé, accanto alle comunicazioni.
alter table incident_events drop constraint if exists incident_events_kind_check;
alter table incident_events add constraint incident_events_kind_check
  check (kind in ('detected','classified','contained','resolved','update','communication','decision','notification','action','closed'));
