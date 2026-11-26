-- Progetti, passo 3 (replica/progetti.md §3 «Baseline e variazioni»): baseline come versioni
-- immutabili del piano, tolleranze del tenant e change request nel motore delle approvazioni.
-- La prima baseline la fissa il PM; le successive nascono solo da una change request approvata.

-- Tolleranze del tenant oltre cui una modifica del piano apre una change request (§8):
-- giorni lavorativi su una milestone (o sulla fine del progetto) e percentuale sul budget.
alter table project_settings add column tolerance_days integer not null default 10 check (tolerance_days between 0 and 365);
alter table project_settings add column tolerance_budget_pct numeric(5,2) not null default 5 check (tolerance_budget_pct between 0 and 100);

-- Le tolleranze del singolo progetto (0077) diventano facoltative: null = quelle del tenant.
-- Nessuna interfaccia le aveva mai impostate, quindi i valori presenti sono i predefiniti.
alter table ext_project alter column tolerance_days drop not null;
alter table ext_project alter column tolerance_days drop default;
alter table ext_project alter column tolerance_budget_pct drop not null;
alter table ext_project alter column tolerance_budget_pct drop default;
update ext_project set tolerance_days = null, tolerance_budget_pct = null;

-- Baseline: fotografia immutabile del piano (date, stime, budget), con chi e quando.
-- number cresce per progetto: la baseline in vigore è quella con il numero più alto.
create table plan_baselines (
  tenant_id uuid not null,
  id uuid not null default gen_random_uuid(),
  project_id uuid not null,
  number integer not null check (number > 0),
  note text not null default '' check (length(note) <= 2000),
  -- Change request approvata da cui nasce (null per la prima baseline). Senza vincolo: la
  -- baseline immutabile sopravvive alla change request, anche se l'elemento sparisce.
  change_request_id uuid,
  start_date date,
  end_date date,
  -- Budget delle richieste confluite al momento della baseline (importi aggregati).
  currency text,
  approved_cents bigint,
  forecast_cents bigint,
  created_by uuid,
  created_at timestamptz not null default now(),
  primary key (tenant_id, id),
  unique (tenant_id, project_id, number),
  foreign key (tenant_id, project_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, created_by) references members (tenant_id, id) on delete set null (created_by)
);
create index on plan_baselines (tenant_id, change_request_id);
create index on plan_baselines (tenant_id, created_by);
select enable_tenant_rls('plan_baselines');

-- Voci della baseline: chiave, titolo e fase come erano, date e stima.
create table plan_baseline_items (
  tenant_id uuid not null,
  baseline_id uuid not null,
  item_id uuid not null,
  kind text not null check (kind in ('work_package','plan_activity','milestone')),
  key text not null,
  title text not null,
  phase_id uuid,
  phase_name text,
  start_date date,
  end_date date,
  estimate_effort_days numeric,
  primary key (tenant_id, baseline_id, item_id),
  foreign key (tenant_id, baseline_id) references plan_baselines (tenant_id, id) on delete cascade,
  foreign key (tenant_id, item_id) references elements (tenant_id, id) on delete cascade
);
create index on plan_baseline_items (tenant_id, item_id);
select enable_tenant_rls('plan_baseline_items');

-- Immutabilità: nessuna modifica né cancellazione diretta. Restano ammesse solo quelle che
-- arrivano a cascata da un vincolo (cancellazione del tenant o del progetto, persona tolta).
create function plan_baseline_immutable() returns trigger language plpgsql as $$
begin
  if pg_trigger_depth() > 1 then
    return case when tg_op = 'DELETE' then old else new end;
  end if;
  raise exception 'la baseline è immutabile: per cambiarla serve una change request approvata'
    using errcode = 'check_violation';
end $$;
create trigger plan_baselines_immutable before update or delete on plan_baselines
  for each row execute function plan_baseline_immutable();
create trigger plan_baseline_items_immutable before update or delete on plan_baseline_items
  for each row execute function plan_baseline_immutable();

-- Change request: elemento di tipo change_request figlio del progetto. Nasce da sola quando una
-- modifica del piano supera le tolleranze, oppure la apre il PM; passa dal motore delle
-- approvazioni (soggetto change_request) e, approvata, crea la baseline con il piano proposto.
create table ext_change_request (
  tenant_id uuid not null,
  element_id uuid not null,
  project_id uuid not null,
  -- draft: da inviare (l'approvazione non è partita); pending: in approvazione.
  status text not null default 'draft' check (status in ('draft','pending','approved','rejected','withdrawn')),
  origin text not null check (origin in ('tolerance','manual')),
  reason text not null default '' check (length(reason) <= 4000),
  -- Baseline in vigore quando è nata e baseline creata all'approvazione.
  baseline_id uuid,
  new_baseline_id uuid,
  approval_request_id uuid,
  -- Perché l'approvazione non è partita: codice dell'errore e parametri.
  blocked_code text,
  blocked_params jsonb,
  -- Piano proposto (le voci con date e stime) e scostamenti oltre le tolleranze, senza titoli:
  -- i titoli si leggono dal piano con i limiti di visibilità di chi guarda.
  proposal jsonb not null default '[]'::jsonb,
  deviations jsonb not null default '[]'::jsonb,
  budget_pct numeric,
  tolerance_days integer not null,
  tolerance_budget_pct numeric(5,2) not null,
  decided_at timestamptz,
  primary key (tenant_id, element_id),
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, project_id) references elements (tenant_id, id) on delete cascade,
  foreign key (tenant_id, baseline_id) references plan_baselines (tenant_id, id) on delete set null (baseline_id),
  foreign key (tenant_id, new_baseline_id) references plan_baselines (tenant_id, id) on delete set null (new_baseline_id),
  foreign key (tenant_id, approval_request_id) references approval_requests (tenant_id, id) on delete set null (approval_request_id)
);
create index on ext_change_request (tenant_id, project_id, status);
create index on ext_change_request (tenant_id, baseline_id);
create index on ext_change_request (tenant_id, new_baseline_id);
create index on ext_change_request (tenant_id, approval_request_id);
select enable_tenant_rls('ext_change_request');

-- Change request nel motore delle approvazioni: nuovo soggetto, letto il vincolo attuale per
-- non togliere gli altri (come per phase_gate in 0077).
do $$
declare def text;
begin
  select pg_get_constraintdef(oid) into def from pg_constraint
  where conname = 'approval_requests_subject_check' and conrelid = 'approval_requests'::regclass;
  if def is not null and position('''change_request''' in def) = 0 then
    execute 'alter table approval_requests drop constraint approval_requests_subject_check';
    execute 'alter table approval_requests add constraint approval_requests_subject_check ' ||
      replace(def, '])', ', ''change_request''::text])');
  end if;
end $$;
