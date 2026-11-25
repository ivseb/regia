# Cabina di portafoglio

Pagina `/cabina` (ondata 3, proposta 9): graduatoria, budget, capacità dei team, roadmap, scenari e pacchetto della seduta insieme. Il codice sta in `web/src/pages/Cabina.tsx`, `CabinaSeduta.tsx` e `cabina-calc.ts` (calcoli puri, provati in `web/test/cabina-calc.test.ts`).

## Una chiamata sola

`GET /api/v1/portfolio/cabina?unit=&period=&budget_cents=` (`internal/api/cabina.go`) restituisce tutto: la graduatoria di sempre (`ranking`), il plafond dell'esercizio (`plafond`), per ogni richiesta sponsor, unità, rischio di esecuzione e sforzo per team (`rows`), le finestre della roadmap (`windows`), la capacità settimanale dei team e il carico del resto del piano (`weeks`, `teams`, da `planning.PortfolioLoad`) e gli scenari. Nessuna chiamata per riga.

La linea di taglio, il budget residuo, i team in sovraccarico e la fine prevista si ricalcolano nel browser (`evalCut`) mentre si sposta la linea: il carico di un team in una settimana è `base + somma degli sforzi delle richieste sopra la linea`, contro la capacità (festività e assenze già tolte dal server).

## Budget

L'origine, in ordine: stanziamento dell'area e del periodo (`allocation`), esercizio collegato (`fiscal_year`), valore a mano (`manual`), simulazione (`simulation`). Se nulla di tutto questo c'è e l'esercizio in corso ha un plafond, la base è il suo residuo (`plafond`) senza che nessuno lo attivi; «Collega la graduatoria al plafond» lo rende stabile. «Aggiungi/Togli k€» e il campo del budget sono simulazioni nella pagina, non salvano nulla.

## Scenari

Tabella `portfolio_scenarios` (migrazione 0059, RLS come le vicine). Uno scenario è nome + linea di taglio + budget (+ le chiavi delle richieste dentro al salvataggio). Il confronto rivaluta ogni linea sulla graduatoria di oggi. `POST/DELETE /api/v1/portfolio/scenarios` richiede `portfolio.admin`; al massimo 20 per tenant, un nome già usato sostituisce lo scenario.

## Pacchetto della seduta

`GET /api/v1/portfolio/sedute/{id}/pacchetto`: l'ordine del giorno con importo, punteggio, posizione in graduatoria, esito delle valutazioni e requisiti verificati. `POST .../verbale` crea la pagina in Knowledge (`knowledge.CreatePage`, primo spazio scrivibile o `space`) e la collega con la relazione «documenta» (`knowledge.Link`) alle richieste decise; per seduta e giorno la pagina è una sola (rigenerare aggiunge solo i collegamenti). Il verbale è un testo di partenza: l'approvazione ufficiale resta quella della seduta (Governance). «Stampa il pacchetto» usa `#print-root` e `web/src/cabina-print.css`.

## Telefono

Sotto i 768 px `RoadmapChart` (anche nella pagina Roadmap) diventa una lista per mese, senza scorrimento orizzontale; i controlli della cabina hanno bersagli da 44 px.

## Non c'è ancora

Il «rischio di esecuzione» è derivato (oltre la scadenza normativa, non pianificata, senza stima, non valutata), non un campo del rischio del registro. La proposta di spostamento guarda un team alla volta e non tiene conto delle dipendenze tra impatti.
