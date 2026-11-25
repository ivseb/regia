-- Budget e costi (demand.md §9-10; piano, passi 11 e 12).
-- Le tabelle esistono dalla 0001: qui si aggiungono le colonne che servono al modulo
-- finance, gli aggregati mantenuti dai trigger e le impostazioni del tenant.

-- ---------------------------------------------------------------------
-- Struttura: esercizi, categorie, fonti di finanziamento
-- ---------------------------------------------------------------------
-- Plafond dell'esercizio: il budget disponibile per il portafoglio (può alimentare la graduatoria).
alter table fiscal_years add column envelope_cents bigint check (envelope_cents >= 0);
create index fiscal_years_dates_idx on fiscal_years (tenant_id, start_date);

-- Categorie: natura CAPEX/OPEX (già presente) e provenienza interna o esterna.
alter table cost_categories add column sourcing text not null default 'internal' check (sourcing in ('internal','external'));
alter table cost_categories add column active boolean not null default true;
create unique index cost_categories_name_uq on cost_categories (tenant_id, lower(name));
create unique index cost_categories_code_uq on cost_categories (tenant_id, lower(code)) where code is not null;

alter table funding_sources add column active boolean not null default true;
create unique index funding_sources_name_uq on funding_sources (tenant_id, lower(name));

-- ---------------------------------------------------------------------
-- Versioni del budget: requested (proposto) → approved; revised (revisione di un approvato);
-- forecast (previsione a finire, senza approvazione). Le versioni approvate non si toccano:
-- si crea una revisione, che all'approvazione sostituisce (superseded) la precedente.
-- ---------------------------------------------------------------------
alter table budgets add column note text not null default '';
alter table budgets add column based_on_id uuid;                  -- versione da cui nasce la revisione
alter table budgets add column created_by uuid;
alter table budgets add column submitted_at timestamptz;
alter table budgets add column decision_note text;                -- motivo di un rifiuto o rinvio
alter table budgets add constraint budgets_based_on_fk
  foreign key (tenant_id, based_on_id) references budgets (tenant_id, id) on delete set null (based_on_id);
alter table budgets add constraint budgets_created_by_fk
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by);
create index on budgets (tenant_id, based_on_id);
create index on budgets (tenant_id, created_by);
create index budgets_element_idx on budgets (tenant_id, element_id, fiscal_year_id, version desc);
-- Una sola versione in lavorazione e una sola approvata per elemento ed esercizio (e una previsione corrente).
create unique index budgets_open_uq on budgets (tenant_id, element_id, fiscal_year_id, (kind = 'forecast'))
  where status in ('draft','submitted');
create unique index budgets_current_uq on budgets (tenant_id, element_id, fiscal_year_id, (kind = 'forecast'))
  where status = 'approved';

alter table budget_lines add constraint budget_lines_amount_positive check (amount_cents >= 0);

-- Trasferimenti: chi ha deciso, quando e perché.
alter table budget_transfers add column decided_by uuid;
alter table budget_transfers add column decided_at timestamptz;
alter table budget_transfers add column decision_note text;
alter table budget_transfers add constraint budget_transfers_decided_by_fk
  foreign key (tenant_id, decided_by) references members (tenant_id, id) on delete set null (decided_by);
create index on budget_transfers (tenant_id, decided_by);
create index budget_transfers_status_idx on budget_transfers (tenant_id, status, created_at desc);

-- ---------------------------------------------------------------------
-- Costi
-- ---------------------------------------------------------------------
alter table rates add column note text not null default '';
alter table rates add column created_by uuid;
alter table rates add column created_at timestamptz not null default now();
alter table rates add constraint rates_created_by_fk
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by);
create index on rates (tenant_id, created_by);

alter table cost_entries add column description text not null default '';
alter table cost_entries add column supplier text;                -- fornitore come testo (fattura importata)
alter table cost_entries add column fx_rate numeric(18,8) not null default 1 check (fx_rate > 0);  -- cambio usato
alter table cost_entries add column updated_at timestamptz not null default now();
create index cost_entries_date_idx on cost_entries (tenant_id, entry_date desc, id);

-- Import: duplicati riconosciuti e anteprima delle prime righe.
alter table import_profiles add column created_by uuid;
alter table import_profiles add column updated_at timestamptz not null default now();
alter table import_profiles add constraint import_profiles_created_by_fk
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by);
create index on import_profiles (tenant_id, created_by);
alter table import_runs add column rows_duplicate integer not null default 0;
alter table import_runs add column preview jsonb not null default '[]';
alter table import_runs add column mapping jsonb;                 -- mappatura usata (anche senza profilo salvato)

-- Aggregati per elemento, mese e categoria: li vede chi non ha finance.read.
-- Si aggiornano nella stessa transazione delle voci, con trigger per istruzione
-- (un import di migliaia di righe fa un solo upsert per gruppo).
create or replace function cost_rollups_apply() returns trigger language plpgsql as $$
begin
  if tg_op in ('UPDATE', 'DELETE') then
    insert into cost_rollups as r (tenant_id, element_id, period, cost_category_id, kind, amount_base_cents, capitalizable_base_cents, updated_at)
      select o.tenant_id, o.element_id, date_trunc('month', o.entry_date)::date, o.cost_category_id, o.kind,
             -sum(o.amount_base_cents), -sum(case when o.capitalizable then o.amount_base_cents else 0 end), now()
      from old_rows o group by 1, 2, 3, 4, 5
    on conflict (tenant_id, element_id, period, cost_category_id, kind) do update
      set amount_base_cents = r.amount_base_cents + excluded.amount_base_cents,
          capitalizable_base_cents = r.capitalizable_base_cents + excluded.capitalizable_base_cents, updated_at = now();
  end if;
  if tg_op in ('INSERT', 'UPDATE') then
    insert into cost_rollups as r (tenant_id, element_id, period, cost_category_id, kind, amount_base_cents, capitalizable_base_cents, updated_at)
      select n.tenant_id, n.element_id, date_trunc('month', n.entry_date)::date, n.cost_category_id, n.kind,
             sum(n.amount_base_cents), sum(case when n.capitalizable then n.amount_base_cents else 0 end), now()
      from new_rows n group by 1, 2, 3, 4, 5
    on conflict (tenant_id, element_id, period, cost_category_id, kind) do update
      set amount_base_cents = r.amount_base_cents + excluded.amount_base_cents,
          capitalizable_base_cents = r.capitalizable_base_cents + excluded.capitalizable_base_cents, updated_at = now();
  end if;
  return null;
end $$;
create trigger cost_rollups_ins after insert on cost_entries referencing new table as new_rows
  for each statement execute function cost_rollups_apply();
create trigger cost_rollups_upd after update on cost_entries referencing old table as old_rows new table as new_rows
  for each statement execute function cost_rollups_apply();
create trigger cost_rollups_del after delete on cost_entries referencing old table as old_rows
  for each statement execute function cost_rollups_apply();

-- Elementi i cui costi contano per un elemento: lui, i suoi impatti (business request) e i
-- figli diretti. Gira con i permessi di chi chiama: la RLS filtra il tenant.
create or replace function finance_family(p uuid) returns table (id uuid) language sql stable as $$
  select p
  union select x.element_id from ext_impact x where x.request_id = p
  union select e.id from elements e where e.parent_id = p and e.deleted_at is null
$$;

-- ---------------------------------------------------------------------
-- Earned value per progetto: percentuale di completamento dichiarata (metodo percent_complete).
-- ---------------------------------------------------------------------
alter table ext_project add column percent_complete numeric(5,2) not null default 0 check (percent_complete between 0 and 100);
alter table ext_project add column updated_by uuid;
alter table ext_project add column updated_at timestamptz not null default now();
alter table ext_project add constraint ext_project_updated_by_fk
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by);
create index on ext_project (tenant_id, updated_by);
create index ext_project_enabled_idx on ext_project (tenant_id) where ev_enabled;
alter table ev_snapshots add column manual boolean not null default false;  -- percentuale indicata a mano per un periodo passato

-- ---------------------------------------------------------------------
-- Impostazioni del modulo per tenant
-- ---------------------------------------------------------------------
create table finance_settings (
  tenant_id uuid not null references tenants(id) on delete cascade,
  labor_category_id uuid,                     -- categoria dei costi calcolati dalle ore
  portfolio_fiscal_year_id uuid,              -- esercizio che alimenta il budget della graduatoria
  portfolio_basis text not null default 'residual' check (portfolio_basis in ('envelope','residual')),
  alert_thresholds integer[] not null default '{80,100}',  -- soglie di consumo del budget (%)
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, labor_category_id) references cost_categories (tenant_id, id) on delete set null (labor_category_id),
  foreign key (tenant_id, portfolio_fiscal_year_id) references fiscal_years (tenant_id, id) on delete set null (portfolio_fiscal_year_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on finance_settings (tenant_id, labor_category_id);
create index on finance_settings (tenant_id, portfolio_fiscal_year_id);
create index on finance_settings (tenant_id, updated_by);
select enable_tenant_rls('finance_settings');
