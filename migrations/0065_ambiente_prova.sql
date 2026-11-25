-- Ambiente di prova e promuovi (usabilità, ondata 3, configurazione.md §5): un tenant di prova
-- collegato al principale, con una copia della configurazione, in cui si lavora con le pagine
-- normali; «Promuovi» porta le modifiche scelte nel principale.
--
-- ambiente_prova: una riga in ciascuno dei due tenant (la RLS vede solo il proprio): nel principale
-- (role 'principale') dice qual è la prova, nella prova (role 'prova') dice da quale principale
-- discende. with_sample: la prova ha ricevuto un campione anonimizzato di elementi.
create table ambiente_prova (
  tenant_id uuid not null references tenants(id) on delete cascade,
  role text not null check (role in ('principale','prova')),
  linked_tenant_id uuid not null references tenants(id) on delete cascade,
  with_sample boolean not null default false,
  -- Solo nella riga della prova: impronta di ogni oggetto di configurazione al momento della copia
  -- (e dopo ogni promozione), per proporre solo ciò che la prova ha cambiato.
  baseline jsonb not null default '{}',
  created_by uuid,
  created_at timestamptz not null default now(),
  refreshed_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on ambiente_prova (tenant_id, created_by);
create index on ambiente_prova (linked_tenant_id);
select enable_tenant_rls('ambiente_prova');

-- Storico delle promozioni, nel tenant principale: cosa è stato portato, da chi, quando.
-- items: [{"kind","key","name","change"}]; summary: conteggi per tipo di modifica.
create table ambiente_prova_promozioni (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  sandbox_tenant_id uuid not null,
  promoted_by uuid,
  promoted_at timestamptz not null default now(),
  items jsonb not null default '[]',
  summary jsonb not null default '{}',
  primary key (tenant_id, id),
  foreign key (tenant_id, promoted_by) references members (tenant_id, id) on delete set null (promoted_by)
);
create index on ambiente_prova_promozioni (tenant_id, promoted_by);
create index on ambiente_prova_promozioni (tenant_id, promoted_at desc);
select enable_tenant_rls('ambiente_prova_promozioni');
