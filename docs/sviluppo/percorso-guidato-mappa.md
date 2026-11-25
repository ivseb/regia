# Percorso guidato e mappa dei processi

Ondata «semplicità»: un amministratore nuovo deve capire a colpo d'occhio cosa configurare e come funzionano i processi, senza perdere profondità. Estende [Inizia da qui](primo-avvio.md), non lo duplica.

## Percorso guidato (`/configurazione/percorso`, tenant.admin)
- `GET /api/v1/avvio/percorso` (`internal/api/avvio_percorso.go`): sei tappe in ordine (persone, team, processi, valutazioni e approvazioni, spazi, moduli facoltativi). Ogni tappa ha `status` (done, partial, todo), i `counts` e le `issues` (incoerenze con un codice e un conteggio). Il progresso conta le tappe non facoltative e non spente; `next` è la prima da fare; `fresh` dice che l'organizzazione è appena nata (una persona, nessun team, nessun modello): la pagina propone prima la scelta del modello.
- Incoerenze calcolate dai dati: team senza membri o senza spazio di consegna, processi senza versione pubblicata, solo processi di partenza, team valutatori senza membri, tipi di valutazione che nessun regola apre, regole che aprono una valutazione archiviata o inesistente, approvazioni senza politica attiva, spazi senza responsabile. Ognuna ha il collegamento «Sistema» alla pagina giusta (`ISSUE_LINK` in `PercorsoGuidato.tsx`).
- Dove un modello aiuta (processi, valutazioni, moduli) c'è «Usa un modello», che apre i modelli di «Inizia da qui». Il percorso resta raggiungibile dopo la chiusura di «Inizia da qui» (ingresso in Configurazione e pulsante in «Inizia da qui»), e da lì si può riaprire o richiudere la pagina.
- Per aggiungere una tappa o un'incoerenza: la tappa e i codici in `avvio_percorso.go`, il tipo in `avvioApi.ts`, i testi `percorso.tappa.<chiave>.*` e `percorso.issue.<codice>` in it/ e en/, il collegamento in `PercorsoGuidato.tsx`.

## Mappa dei processi (`/mappa-processi`, sola lettura per chi è nel tenant)
- `GET /api/v1/mappa-processi` (`internal/api/mappa.go`): per ogni processo pubblicato e non archiviato, le fasi (percorso principale, poi quelle fuori percorso) con agganciati valutazioni, approvazioni, controlli e azioni, più team coinvolti e collegamenti verso altri tipi (valutazione per richiesta o per impatto, epic). Elenca anche i processi senza versione pubblicata.
- Come si legge una definizione: le regole `create` stanno sulla fase iniziale, `enter:X` e `exit:X` sulla fase X; i gate e le approvazioni dei passaggi sulla fase di arrivo; le approvazioni cercano la politica per tipo e fase, e se manca l'elemento è segnalato. `buildMap` è pura e ha il suo test (`mappa_test.go`).
- Le condizioni si mostrano in parole con `describeCondition` dell'editor dei processi. Chi ha `process.admin` apre dagli elementi la configurazione (editor del processo sulla regola, tipi di valutazione); per gli altri è solo lettura, senza collegamenti.
- Da telefono (375 px) le fasi sono un elenco verticale; da tablet in su una riga con le frecce.

## Test
`go test ./internal/api -run TestBuildMap`; e2e `web/e2e/semplicita-guida-admin.spec.ts` (con `ondata3-avvio.spec.ts`, che condivide il tenant «avvio»: la nuova spec lo legge soltanto).
