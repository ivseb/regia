-- DevSecOps, fase 5a: metriche DORA delle quattro chiavi (devsecops.md §3.6 e §4).
--
-- Frequenza di rilascio, lead time delle modifiche, tasso di fallimento delle modifiche e
-- tempo di ripristino si calcolano dai dati che ci sono già (deploy in produzione, rilasci,
-- change, esiti dei controlli, incidenti del modulo risk) e si salvano come punti in
-- metric_points, per settimana, mese e trimestre. Qui: le impostazioni per tenant e la
-- forma dei punti.

-- ---------------------------------------------------------------------
-- Impostazioni: finestra del fallimento, parole di hotfix e rollback, da dove parte il lead
-- time, soglie dei livelli (elite, high, medium, low). thresholds vuoto = soglie predefinite.
-- ---------------------------------------------------------------------
create table dora_settings (
  tenant_id uuid not null references tenants(id) on delete cascade,
  -- Un deploy è fallito se un incidente sull'applicazione, un rollback o un hotfix lo
  -- seguono entro questo numero di ore.
  failure_window_hours integer not null default 24 check (failure_window_hours between 1 and 720),
  hotfix_words text[] not null default '{hotfix,rollback,revert}',
  -- auto | check_run | change | request (vedi internal/modules/devsecops/dora.go)
  lead_time_start text not null default 'auto' check (lead_time_start in ('auto','check_run','change','request')),
  -- {"deploy_frequency": {"elite": 7, "high": 1, "medium": 0.25}, "lead_time_days": {...},
  --  "change_failure_rate": {...}, "mttr_hours": {...}}
  thresholds jsonb not null default '{}',
  -- Giorni su cui si calcolano i fatti delle condizioni (frequenza_rilascio, ...).
  fact_days integer not null default 30 check (fact_days between 7 and 365),
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on dora_settings (tenant_id, updated_by);
select enable_tenant_rls('dora_settings');

-- ---------------------------------------------------------------------
-- metric_points: un punto è un valore di una metrica per un soggetto e un periodo. Il
-- soggetto può essere un'applicazione, un asset, un team, un servizio o il tenant (non
-- sono tutti elementi), quindi la chiave si allarga con ambito, soggetto e periodo.
-- La tabella non è ancora usata da altri moduli.
-- ---------------------------------------------------------------------
alter table metric_points drop constraint metric_points_pkey;
alter table metric_points
  add column scope text not null default 'element',
  add column subject_id uuid,
  add column period text not null default 'point';
update metric_points set subject_id = coalesce(subject_element_id, tenant_id);
alter table metric_points alter column subject_id set not null;
alter table metric_points alter column subject_element_id drop not null;
alter table metric_points add primary key (tenant_id, metric_key, scope, period, subject_id, at);
create index metric_points_series_idx on metric_points (tenant_id, scope, period, metric_key, at desc);
