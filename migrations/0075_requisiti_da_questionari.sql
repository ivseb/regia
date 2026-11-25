-- Requisiti veri dalle regole dei questionari (derivazioni con kind "requirement").
-- Le regole non producono più prescrizioni da accettare: durante la compilazione
-- restano "requisiti proposti" della valutazione; alla chiusura (esito favorevole o con
-- prescrizioni) diventano requisiti del modulo requisiti con origine "questionnaire".
-- Le prescrizioni già create dalle regole non si toccano.

create table review_requirement_proposals (
  tenant_id uuid not null,
  review_id uuid not null,
  rule_key text not null,
  title text not null default '',
  text text not null check (length(text) between 1 and 2000),
  category text not null default '',
  method text not null default 'manual' check (method in ('manual','evidence')),
  check_key text,
  created_at timestamptz not null default now(),
  primary key (tenant_id, review_id, rule_key),
  foreign key (tenant_id, review_id) references elements (tenant_id, id) on delete cascade
);
select enable_tenant_rls('review_requirement_proposals');

alter table ext_requirement drop constraint ext_requirement_origin_check;
alter table ext_requirement add constraint ext_requirement_origin_check
  check (origin in ('prescription','catalog','manual','questionnaire'));
alter table ext_requirement add column rule_key text;
-- Una regola genera un solo requisito per valutazione (riaprire e richiudere non duplica).
create unique index ext_requirement_rule_uq on ext_requirement (tenant_id, review_id, rule_key)
  where origin = 'questionnaire' and review_id is not null;
