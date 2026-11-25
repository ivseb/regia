-- Board per team (ondata 3): la bozza delle colonne di un team, in attesa di «Pubblica».
-- La configurazione pubblicata resta nella vista di sistema «board:<spazio>» (tabella views):
-- le colonne proprie, i tipi di fase per colonna, il modello e l'ordinamento stanno nel suo JSON,
-- quindi i team esistenti non cambiano e senza bozza non c'è niente da migrare.
create table board_drafts (
  tenant_id uuid not null,
  space_id uuid not null,
  settings jsonb not null,
  updated_by uuid,
  updated_at timestamptz not null default now(),
  primary key (tenant_id, space_id),
  foreign key (tenant_id, space_id) references spaces (tenant_id, id) on delete cascade,
  foreign key (tenant_id, updated_by) references members (tenant_id, id) on delete set null (updated_by)
);
create index on board_drafts (tenant_id, updated_by);
select enable_tenant_rls('board_drafts');
