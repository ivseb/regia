-- Progetti, passo 6 (replica/progetti.md §3, §6): dipendenze tra progetti e contratto del
-- fornitore dal registro ICT.

-- Dipendenza tra progetti: il progetto (o una sua voce) ha bisogno di una milestone di un altro
-- progetto entro una data. La data che serve è need_by; senza, vale l'inizio della voce che
-- dipende. Se la milestone è prevista oltre quella data (o la data è passata e la milestone non
-- è raggiunta) il progetto dipendente lo segnala: semaforo giallo e avviso ai suoi PM.
-- alerted_end ricorda la data della milestone già segnalata, così l'avviso parte una volta per
-- slittamento; torna vuota quando la dipendenza rientra.
create table project_dependencies (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  project_id uuid not null,
  item_id uuid,
  milestone_id uuid not null,
  need_by date,
  note text not null default '' check (length(note) <= 500),
  alerted_end date,
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, project_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, item_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, milestone_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create unique index project_dependencies_uq on project_dependencies (tenant_id, project_id, coalesce(item_id, project_id), milestone_id);
create index on project_dependencies (tenant_id, milestone_id);
create index on project_dependencies (tenant_id, item_id);
create index on project_dependencies (tenant_id, created_by);
select enable_tenant_rls('project_dependencies');

-- Contratto del fornitore: il work package si collega a un contratto del registro ICT invece di
-- riscriverne riferimento e importo. Facoltativo: riferimento e importo già scritti sul work
-- package restano e, se ci sono, valgono loro.
alter table ext_work_package add column contract_id uuid;
alter table ext_work_package add foreign key (tenant_id, contract_id) references ict_contracts (tenant_id, id) on delete set null (contract_id);
create index on ext_work_package (tenant_id, contract_id);
