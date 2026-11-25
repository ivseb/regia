-- Moduli accendibili per tenant (usabilità, sezione 6 punto 5).
-- Una riga per modulo spento o acceso esplicitamente; l'assenza di una riga vale «acceso»,
-- così tutti i tenant esistenti restano com'erano. I dati dei moduli spenti non si toccano:
-- spegnere un modulo lo nasconde (menu, ⌘K, cruscotto) e fa rispondere 404 alle sue API.
-- I valori ammessi li decide l'applicazione (internal/api/modules.go): qui si controlla solo
-- il formato della chiave, così un modulo nuovo non richiede una migrazione.
create table tenant_modules (
  tenant_id uuid not null references tenants(id) on delete cascade,
  module text not null check (module ~ '^[a-z][a-z0-9_]{0,31}$'),
  enabled boolean not null default true,
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id, module),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on tenant_modules (tenant_id, updated_by);
select enable_tenant_rls('tenant_modules');
