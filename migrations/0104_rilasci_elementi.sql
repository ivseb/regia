-- Rilasci con elementi, note di rilascio, chiusura con le parole chiave e certificazione del
-- rilascio (docs/sviluppo/commit-elementi.md).

-- Impostazioni del tenant per i rilasci: chiusura degli elementi con «fixes HL-12» (spenta
-- per default) e sezioni della certificazione del rilascio, nell'ordine in cui compaiono.
create table devsecops_release_settings (
  tenant_id uuid not null references tenants(id) on delete cascade,
  close_on_keywords boolean not null default false,
  cert_sections text[] not null default '{change,releases,items,tests,sonar,security,gate}',
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on devsecops_release_settings (tenant_id, updated_by);
select enable_tenant_rls('devsecops_release_settings');

-- Eccezione per spazio: forza la chiusura con le parole chiave a sì o a no (senza riga lo
-- spazio eredita l'impostazione del tenant).
create table devsecops_space_close (
  tenant_id uuid not null references tenants(id) on delete cascade,
  space_id uuid not null,
  close_on_keywords boolean not null,
  updated_at timestamptz not null default now(),
  primary key (tenant_id, space_id),
  foreign key (tenant_id, space_id) references spaces (tenant_id, id) on delete cascade
);
select enable_tenant_rls('devsecops_space_close');

-- Certificazione del rilascio: istantanee generate per numero di change. Ogni generazione è
-- una versione nuova; le precedenti restano. L'hash è sul JSON dell'istantanea.
create table release_certifications (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  change_id uuid not null,
  version integer not null check (version >= 1),
  snapshot jsonb not null,
  sha256 text not null,
  evidence_id uuid,
  generated_by uuid,
  generated_at timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, change_id) references changes (tenant_id, id) on delete cascade,
  foreign key (tenant_id, generated_by) references members (tenant_id, id) on delete set null (generated_by)
);
create unique index release_certifications_uq on release_certifications (tenant_id, change_id, version);
create index on release_certifications (tenant_id, generated_by);
select enable_tenant_rls('release_certifications');
