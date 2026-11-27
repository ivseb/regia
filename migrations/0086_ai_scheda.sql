-- AI, ondata 3: assistente nella scheda (replica/ai.md §2 punto 2).
--
-- Le bozze inserite dalla persona e i collegamenti accettati alla creazione passano dalle
-- proposte come quelle dell'assistente, ma appartengono a un'altra funzione: «bozze» o
-- «classificazione». La colonna la ricorda, così la decisione entra nel registro delle chiamate
-- con la funzione giusta (nulla = assistente o client MCP, come prima).
alter table ai_proposals add column feature text;
