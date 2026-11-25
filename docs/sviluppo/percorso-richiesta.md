# Percorso della richiesta e «Cosa faccio adesso»

## Percorso della richiesta
Ondata «semplicità»: chi amministra o usa Regia deve vedere subito dove si trova una richiesta, chi è coinvolto, che lavoro ne nasce e cosa succede dopo. Specifica: `replica/prossimi-passi.md`. Migrazioni: nessuna.

**Endpoint** (`internal/api/percorso.go`, rotta registrata da `percorsoRoutes` in `server.go`)
- `GET /api/v1/elements/{ref}/percorso`: sola lettura, una transazione con permessi e RLS di chi chiede. Vale per `business_request` e per `impact`; per gli altri tipi risponde 422 (`validation.percorso.of_request`).
- Risposta: `phases` (fatte, attuale, prossime; le fuori percorso restano marcate `off_path`), `reviews` (aperte e chiuse, con tipo e team), `approvals` in attesa, `impacts` per team, `delivery` (epic e avanzamento delle story, da `work.Service.DeliveryOf`), `next` (passaggi dalla fase attuale con motivi di blocco, valutazioni obbligatorie aperte e approvazioni che si aspettano), `triggers` (regole del processo `enter:<fase>` dei passaggi in avanti), `todo` («Cosa faccio adesso»).
- Tutto è calcolato dai dati configurati: le fasi e i passaggi vengono dal motore (`Phases`, `Available`), le regole dalla versione di processo in uso dall'elemento (`process_versions.definition`), e una regola con `when` sui fatti della richiesta si valuta con `ElementFacts` (`when_met`); la condizione propria dell'azione, valida per ogni impatto, resta in `action_when`. I tipi di valutazione citati dalle regole prendono il nome da `review_types`.
- `todo`: prima ciò che tocca a chi guarda (`decide`, `compile_review`, `exam_review`, `advance`, `unblock`, `create_epic`), poi ciò che si aspetta (`wait_approval`, `wait_review`, `wait_team`, `wait_work`), `nothing` se non c'è niente. Il client sceglie il testo dal `kind`; i nomi sono dati del tenant.

**Interfaccia**
- `web/src/pages/Percorso.tsx` (`PercorsoPanel`), inserito in `ElementPage.tsx` sopra le schede. Una sola richiesta (`web/src/percorsoApi.ts`).
- Sul telefono il pannello è una riga chiusa (fase e cosa tocca a te) che si apre al tocco, così la scheda sotto resta in vista; con spazio è sempre aperto. La linea delle fasi va a capo, nessuno scroll orizzontale.
- Testi in `i18n/{it,en}/percorso.ts`, errore in `errors-percorso.ts`.

## Spiegazione dei termini
`web/src/help.tsx`: `<Term k="impatto">impatto</Term>` sottolinea la parola e mostra una frase semplice al mouse, al focus da tastiera o al tocco (resta aperta finché non si tocca di nuovo o si preme Esc). È un pulsante, il testo è un `role="tooltip"` collegato con `aria-describedby`, ed è `fixed` e contenuto nello schermo. Il glossario è in `i18n/{it,en}/help.ts` (`help.term.<k>`): richiesta, fase, valutazione, impatto, epic, story, approvazione, regola, processo, versione. Un termine nuovo: aggiungerlo a `TERMS` in `help.tsx` e le due frasi nel catalogo.

## Test
`go test ./internal/api -run 'TestPhaseLine|TestNextSteps|TestBuildTodo'` (logica pura); `web/e2e/semplicita-percorso.spec.ts` (coerenza con valutazioni, impatti e fasi; pannello; termini da mouse e tastiera; impatto; telefono; inglese).
