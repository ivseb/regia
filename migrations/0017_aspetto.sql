-- Aspetto dell'interfaccia: tema (direzione visiva) e font, scelti dall'utente o dal tenant.
-- I valori ammessi li decide l'applicazione (internal/api/appearance.go): qui si controlla
-- solo il formato della chiave, così un tema nuovo non richiede una migrazione.

-- Preferenze personali dell'account, valide in tutti i tenant.
-- Forma: {"appearance": {"theme": "petrolio", "font": "plex"}}; una chiave assente vale
-- "come il default dell'organizzazione".
alter table accounts add column preferences jsonb not null default '{}'
  check (jsonb_typeof(preferences) = 'object');

-- Default del tenant, scelto da chi ha tenant.admin: vale per chi non ha scelto altro.
-- null = default di prodotto (Atlante, Atkinson Hyperlegible).
create table tenant_appearance (
  tenant_id uuid not null references tenants(id) on delete cascade,
  theme text check (theme ~ '^[a-z][a-z0-9-]{0,31}$'),
  font text check (font ~ '^[a-z][a-z0-9-]{0,31}$'),
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on tenant_appearance (tenant_id, updated_by);
select enable_tenant_rls('tenant_appearance');
