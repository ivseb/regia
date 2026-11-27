-- Registro di audit: la chiave primaria (at, seq) valeva per tutti i tenant insieme, mentre
-- la numerazione (seq) è per tenant. Due tenant che scrivevano lo stesso numero nello stesso
-- microsecondo (per esempio due organizzazioni create insieme: le loro catene partono da 1) si
-- scontravano sulla chiave e la seconda transazione falliva con un errore 500.
--
-- L'unicità giusta è quella della catena: (tenant, seq), con at perché la tabella è
-- partizionata per data. «nulls not distinct» la fa valere anche per la catena di piattaforma
-- (tenant_id nullo). La catena di hash e il lock per tenant di audit_append non cambiano.
alter table audit_log drop constraint audit_log_pkey;
drop index audit_log_seq_uq;
create unique index audit_log_seq_uq on audit_log (tenant_id, seq, at) nulls not distinct;
