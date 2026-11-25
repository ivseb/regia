# Ambiente di prova e promuovi (ondata 3, configurazione §5)

Pagina `/configurazione/prova` («Ambiente di prova», solo `tenant.admin`) e barra «Ambiente di prova» nel tenant di prova. Codice: `internal/api/prova*.go`, `web/src/pages/Prova.tsx`, `web/src/pages/ProvaBar.tsx`, `web/src/provaApi.ts`, migrazione `0065_ambiente_prova.sql`.

## Modello
La prova è un **tenant vero**, `<slug>-prova`, creato con `tenant.Create` sullo stesso account dell'amministratore (compare nel menu delle organizzazioni). Niente secondo database. `ambiente_prova` ha una riga in ciascun tenant (la RLS vede solo la propria): nel principale `role = 'principale'`, nella prova `role = 'prova'`; `ambiente_prova_promozioni` è lo storico, nel principale. Ogni lettura e scrittura si fa dopo `set_config('app.tenant_id', …)` sul tenant giusto, dentro una sola transazione: la RLS vale sempre.

## Cosa si copia
`provaTables` (`prova_copy.go`), nell'ordine delle chiavi esterne: insiemi di valori e opzioni, flussi, tipi, campi, gruppi (non di sistema), spazi, campi dei tipi, tipi di relazione, ruoli degli stakeholder, organi di governo, politiche di approvazione, team e tipi di valutazione, modelli di questionario, processi (solo la versione pubblicata), gate, moduli accesi, cataloghi di requisiti, scorecard e le impostazioni dei moduli. Gli **id restano gli stessi** (la chiave è `(tenant_id, id)`): i riferimenti interni e quelli dentro i JSON non si rimappano, e un oggetto nuovo si promuove con lo stesso id. Si rimappano solo il tenant, le persone (tutte → l'amministratore della prova) e il gruppo «tutti i membri». Le tabelle di versioni immutabili (cataloghi, modelli di questionario) non si cancellano: la copia aggiunge una versione se il contenuto differisce.

Con «campione» si copiano fino a 6 elementi per tipo, solo dei tipi senza estensione di dominio, con titolo generico e senza testi, valori dei campi, persone né collegamenti.

**Non si copia**: persone, ruoli e permessi, elementi e documenti veri (salvo il campione), importi e budget, regole di applicabilità e di efficacia dei controlli (modulo GRC), integrazioni e token.

## Confronto e promozione
`snapshot` legge sei tipi di oggetto (campi con opzioni, tipi con i loro campi, processi, gate, organi, cataloghi) e ne fa un'impronta. Alla creazione e dopo ogni promozione la **baseline** (impronte, in `ambiente_prova.baseline` della prova) ricorda lo stato di partenza: si propone ciò che la prova ha cambiato rispetto a lei. Se anche il principale è cambiato dopo la copia l'oggetto è «conflitto» (non preselezionato). Ogni voce è aggiunta, modificata o tolta (i tolti diventano archiviati) con «Cosa cambia»; per i processi si confrontano fasi, passaggi, gate e regole.

`POST /api/v1/prova/promuovi` rilegge il confronto, applica nell'ordine campi → tipi → processi → gate → organi → cataloghi, tutto in una transazione (se un oggetto non è valido nel principale non cambia niente). I processi passano da `Export` della prova e `Import(publish)` del principale: nuova versione pubblicata, le richieste già avviate restano sulla loro e si migrano come al solito. Audit `sandbox.promote` in entrambi i tenant, riga di storico nel principale.

«Ricrea la prova» svuota la prova (elementi e configurazione) e la ricopia dal principale.

## Errori
Codici `validation.sandbox.*` (`errors-prova.ts`): `not_linked`, `exists`, `in_sandbox`, `slug_taken`, `nothing_selected`, `item_unknown`, `dependency`.

## Prova
`internal/itest/prova_test.go` (crea, cambia processo, campo, tipo, organo e gate nella prova, il principale non cambia, diff, promozione selettiva, audit, nessuna fuga, ricrea) e `web/e2e/ondata3-prova.spec.ts` (tenant `prova`, account `prova@demo.test`, creato da `scripts/dev-seed.sh`).
