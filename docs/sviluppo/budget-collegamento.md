# Budget di progetto collegato a un nodo, dall'interfaccia

Disegno: `replica/budget.md` §2 («Collegamento con i budget di progetto»). API: `docs/budget-api.md`, sezione «Budget di progetto».

## Cosa c'è

- **Campo «Attinge da»** (`web/src/pages/budget/BudgetSource.tsx`, componente `BudgetSource`), in due posti di `web/src/pages/Finance.tsx`:
  - nel dettaglio del budget: scegliendo un nodo si collega subito (`PUT /finance/budgets/{id}/allocation`); «Scollega» chiede conferma;
  - in «Nuovo budget»: il nodo si sceglie prima di creare la bozza (`allocation_id` nella creazione). Le opzioni compaiono quando la chiave dell'elemento è scritta per intero.
- **Numeri prima dell'invio:** stanziato del nodo, impegnato dagli altri budget approvati, disponibile, quanto il budget ne userebbe (le righe senza mese e quelle dei mesi del periodo del nodo, calcolate sulle righe in modifica) e quanto resterebbe. Se supera il disponibile compare l'avviso `link-exceeds`. Una previsione a finire non impegna e non avvisa. Il controllo vero resta quello del servizio, all'invio e all'approvazione.
- **Solo lettura** senza `finance.write`: il nodo scelto e i suoi numeri; se il nodo è nascosto dalla RLS, «Nodo non visibile».
- **Dal lato del nodo:** nell'albero (`BudgetTree.tsx`), i nodi di un elemento hanno la scheda «Budget di progetto» (`NodeBudgets`): elemento con link, versione e tipo, stato (bozza, inviato, approvato), totale e impegnato.

## Dati

- `GET /budget/element-allocations` (vedi l'API) è l'unico endpoint nuovo o cambiato; le opzioni vengono dal database con la RLS dell'utente, quindi un utente vede solo i nodi delle sue aree e il percorso si ferma al primo antenato nascosto (un responsabile di reparto vede «Sviluppo», non «Direzione IT»).
- Si propongono solo i nodi dell'elemento del budget, in periodi dello stesso esercizio, non archiviati e non chiusi (resta anche il nodo già collegato, se il periodo si è chiuso).
- `NodeBudget` nel tree ha ora `element_key`, `element_title` e `committed_cents`. Nessuna migrazione.

## Test

- Go: `go test ./internal/itest -run 'TestBudgetOpzioniCollegamento|TestBudgetImpegno'` (opzioni, percorso per profilo, area estranea, esercizio estraneo, budget collegato e approvato, scollegamento).
- e2e: `web/e2e/lacune-budget.spec.ts` (creazione con nodo, residuo e avviso, scollegamento con conferma e annullamento, ricollegamento, scheda del nodo, invio rifiutato oltre il nodo, 375 px).

## Non fatto

- «Attinge da» nella scheda della richiesta (`RequestBudget`) e nella pagina di un progetto che non sia la pagina Finanza.
- Spostare un budget approvato da un nodo a un altro con un solo passaggio (si scollega e si ricollega; il servizio ricontrolla lo stanziamento).
