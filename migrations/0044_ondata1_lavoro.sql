-- Ondata 1 (area lavoro): ogni organo di governo decide se i suoi membri possono decidere «in linea»
-- (dall'elenco o dalla scheda, senza aprire il pacchetto della richiesta) oppure se prima va aperto
-- il pacchetto. Predefinito: sì. Serve alla futura pagina «Da fare» con le decisioni in linea.
alter table governance_bodies add column inline_decision boolean not null default true;
