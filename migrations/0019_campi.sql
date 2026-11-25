-- Campi personalizzati gestibili da interfaccia (richiesta dell'utente del 2026-11-24, sdlc.md §10).
-- Le tabelle field_defs, type_fields, option_sets, options ed element_values esistono dalla
-- migrazione 0001: qui si aggiungono solo le colonne che servono alla configurazione.

-- Definizione del campo: aiuto per chi compila, valore predefinito (nella forma salvata in
-- elements.fields) e archiviazione. Un campo archiviato non si propone più nei form e non si
-- modifica, ma i valori restano negli elementi e nell'indice (si possono ancora cercare).
alter table field_defs add column help text not null default '';
alter table field_defs add column default_value jsonb;
alter table field_defs add column archived boolean not null default false;

-- Associazione al tipo: sezione (gruppo nel dettaglio e nei form), visibilità condizionale
-- nel linguaggio di condizioni (es. "driver: normativo") e fase del processo da cui il campo
-- diventa obbligatorio (gate implicito sui passaggi verso quella fase e le successive).
alter table type_fields add column section text not null default '';
alter table type_fields add column visible_when text not null default '';
alter table type_fields add column required_from text not null default '';

-- I form leggono le associazioni del tipo nell'ordine scelto.
create index type_fields_type_ord_idx on type_fields (tenant_id, type_id, ordinal) where space_id is null;

-- "Il campo è in uso?" e "in quanti elementi?" (amministrazione dei campi: un campo con
-- valori non si elimina, si archivia) senza scorrere l'indice di tutti i valori.
create index element_values_field_idx on element_values (tenant_id, field_id, element_id);

-- L'aiuto del campo di esempio "Benefici attesi" (prima era scritto nel form della nuova
-- richiesta, ora i campi del form vengono dalla configurazione).
update field_defs set help = 'Risparmi, ricavi, riduzione del rischio: meglio se misurabili.'
where key = 'benefici_attesi' and help = '';
