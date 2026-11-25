-- Primo avvio guidato (usabilità, proposta 7B): cosa è stato portato nel tenant da un modello
-- per tipo di azienda e quali testi normativi aspettano ancora una verifica.
--
-- avvio_applicazioni: una riga per modello applicato (kind 'modello') e una per ogni oggetto che
-- il modello ha portato (processo, catalogo normativo, organo di governo). I processi di partenza
-- creati con il tenant non hanno riga: si riconoscono dalla chiave (internal/api/avvio.go).
-- Un catalogo nasce «da verificare» (unverified): finché una persona non lo conferma, l'interfaccia
-- lo segna come tale. restored_at ricorda l'ultimo ripristino di un processo al suo originale.
create table avvio_applicazioni (
  tenant_id uuid not null references tenants(id) on delete cascade,
  id uuid not null default gen_random_uuid(),
  model text not null check (model ~ '^[a-z][a-z0-9_]{0,40}$'),
  kind text not null check (kind in ('modello','processo','catalogo','organo')),
  key text not null check (key <> ''),
  applied_by uuid,
  applied_at timestamptz not null default now(),
  unverified boolean not null default false,
  verified_by uuid,
  verified_at timestamptz,
  restored_by uuid,
  restored_at timestamptz,
  primary key (tenant_id, id),
  foreign key (tenant_id, applied_by) references members (tenant_id, id) on delete set null (applied_by),
  foreign key (tenant_id, verified_by) references members (tenant_id, id) on delete set null (verified_by),
  foreign key (tenant_id, restored_by) references members (tenant_id, id) on delete set null (restored_by)
);
create unique index avvio_applicazioni_uq on avvio_applicazioni (tenant_id, kind, key);
create index on avvio_applicazioni (tenant_id, applied_by);
create index on avvio_applicazioni (tenant_id, verified_by);
create index on avvio_applicazioni (tenant_id, restored_by);
select enable_tenant_rls('avvio_applicazioni');

-- Lo stato della pagina «Inizia da qui»: chi l'ha richiusa e quando (un solo record per tenant).
create table avvio_stato (
  tenant_id uuid not null references tenants(id) on delete cascade,
  closed_by uuid,
  closed_at timestamptz not null default now(),
  primary key (tenant_id),
  foreign key (tenant_id, closed_by) references members (tenant_id, id) on delete set null (closed_by)
);
create index on avvio_stato (tenant_id, closed_by);
select enable_tenant_rls('avvio_stato');
