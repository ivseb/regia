-- Nomi per lingua delle opzioni dei campi (anche degli insiemi di stati): {"en": "Open"}.
-- Il nome resta canonico (condizioni, processi e codice lo citano); l'etichetta è solo di visualizzazione.
alter table options add column labels jsonb not null default '{}';
