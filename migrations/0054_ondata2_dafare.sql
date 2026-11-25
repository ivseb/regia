-- Ondata 2, proposta 1 «Da fare» (replica/usabilita.md): «Silenzia» un elemento nelle Attività.
-- Una riga per persona ed elemento: gli avvisi informativi (commenti, ruoli, fasi) di quell'elemento
-- non arrivano più (né in app né per email). Le notifiche che chiedono un'azione (approvazioni,
-- valutazioni, menzioni, requisiti, report) non si silenziano mai.
create table notification_silences (
  tenant_id uuid not null,
  member_id uuid not null,
  element_id uuid not null,
  created_at timestamptz not null default now(),
  primary key (tenant_id, member_id, element_id),
  foreign key (tenant_id, member_id) references members (tenant_id, id) on delete cascade,
  foreign key (tenant_id, element_id) references elements (tenant_id, id) on delete cascade
);
create index on notification_silences (tenant_id, element_id);
select enable_tenant_rls('notification_silences');
