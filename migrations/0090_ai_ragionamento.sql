-- Ragionamento configurabile e riassunto delle conversazioni lunghe.
--
-- Ragionamento per livello (vuoto = automatico: poco al rapido, molto all'approfondito); quello
-- per funzione sta in routes, accanto al livello o al modello scelto. Ogni adattatore lo
-- traduce nel parametro del suo fornitore (effort, reasoning effort, thinkingLevel).
alter table ai_settings
  add column fast_reasoning text not null default '' check (fast_reasoning in ('', 'low', 'medium', 'high')),
  add column deep_reasoning text not null default '' check (deep_reasoning in ('', 'low', 'medium', 'high'));

-- Conversazioni lunghe: i messaggi più vecchi della finestra che si rimanda al modello si
-- riassumono (funzione «riassunti»), e il riassunto va al posto loro. summary_upto è quanti
-- messaggi, dall'inizio, il riassunto copre.
alter table ai_conversations
  add column summary text not null default '' check (length(summary) <= 8000),
  add column summary_upto integer not null default 0 check (summary_upto >= 0);
