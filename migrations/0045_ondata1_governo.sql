-- Ondata 1 del governo (replica/usabilita.md, sezione 4).
-- «Causa: fornitore terzo» come scelta del fornitore: l'incidente punta al fornitore ICT che
-- l'ha causato, così il rischio di concentrazione e il registro DORA lo possono contare.
alter table ext_incident add column if not exists cause_provider_id uuid;
alter table ext_incident drop constraint if exists ext_incident_cause_provider_fk;
alter table ext_incident add constraint ext_incident_cause_provider_fk
  foreign key (tenant_id, cause_provider_id) references elements (tenant_id, id) on delete set null (cause_provider_id);
create index if not exists ext_incident_cause_provider_idx on ext_incident (tenant_id, cause_provider_id) where cause_provider_id is not null;
