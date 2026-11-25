-- Indici per portare nel budget gli scenari del benchmark a 300.000 elementi (docs/benchmark.md,
-- sezione «Il budget»).

-- Code delle valutazioni (inbox): le più urgenti di un team e quelle da compilare, già nell'ordine
-- di lettura (scadenza, poi creazione: gli id degli elementi sono ordinati nel tempo). Con 3.000
-- valutazioni di un team e 1.170 in coda l'inbox ordinava e univa tutte le righe prima del limite.
create index if not exists ext_review_team_queue_idx on ext_review (tenant_id, team_id, due_on nulls last, element_id)
  where superseded_by is null and stage in ('submitted', 'exam', 'clarification');
create index if not exists ext_review_compile_queue_idx on ext_review (tenant_id, due_on nulls last, element_id)
  where superseded_by is null and stage in ('compile', 'clarification');

-- Chi ha un ruolo su qualche elemento (verificatore dei requisiti, responsabile della valutazione):
-- le inbox si chiedono a ogni pagina e partono da qui. Con 240.000 assegnazioni dell'attore
-- l'indice (tenant, persona) non basta: serve il ruolo davanti.
create index if not exists stakeholders_role_member_idx on stakeholders (tenant_id, role_id, member_id) where member_id is not null;
create index if not exists stakeholders_role_group_idx on stakeholders (tenant_id, role_id, group_id) where group_id is not null;
