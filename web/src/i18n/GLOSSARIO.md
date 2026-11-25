# Glossario e stile dell'interfaccia in inglese

Per chi traduce i testi (passo 2 di `replica/lingua.md`). L'italiano è la sorgente; l'inglese segue questo glossario. Il vocabolario italiano fisso è in `replica/design.md`, sezione «Vocabolario e lingua».

## Termini

| italiano | inglese | note |
| --- | --- | --- |
| elemento | item | il termine generico per tutto ciò che si traccia; mai «card» né «object» |
| richiesta | request | la domanda che entra nel portafoglio; mai «business request» |
| Story, Epic | Story, Epic | restano uguali |
| Governance | Governance | nome della sezione di menu e delle sue pagine |
| organo | board (governance body) | «board» da solo è anche la lavagna del lavoro: nelle pagine di governance scrivere «governance board» al primo uso |
| graduatoria | ranking | |
| plafond dell'esercizio | fiscal-year budget | |
| impatto | impact | solo per l'analisi d'impatto, non come nome di un elemento |
| Solo se… | Only if… | condizioni di processi e regole |
| fase | stage | del processo di un elemento; non «status» né «state» |
| passaggio | transition | da una fase all'altra |
| regola | rule | le regole dei processi e dell'applicabilità |
| deroga | waiver | non «exception» |
| prescrizione | directive | |

Un termine nuovo va aggiunto prima qui e nella tabella di `replica/design.md`, poi usato.

I nomi dei **tipi** (richiesta, rischio, incidente…) non sono testi del prodotto: sono dati del tenant. Si leggono sempre da `useTypeNames()` (`web/src/typesApi.tsx`), che li chiede all'API nella lingua della persona. Non scriverli a mano nel catalogo.

Sigle e nomi propri non si traducono: DORA, NIS2, ISO 27001, GDPR, TOTP, SSO, OIDC, WCAG, Regia, GitHub, GitLab, Story, Epic, WIP, ID.

## Stile

- **Inglese americano** (organization, color, canceled, license), non britannico.
- **Frasi brevi**, voce attiva, seconda persona («Your organization requires…», non «The organization requires…»). Niente «please».
- **Maiuscole solo a inizio frase**, anche nei pulsanti, nelle intestazioni, nelle etichette e nelle voci di menu: «Create token», «Save appearance», non «Create Token». Le eccezioni sono i nomi propri e le sigle.
- **Pulsanti: un verbo all'imperativo**, senza punto: «Save», «Revoke», «Accept invitation». Non «Click to save».
- **Messaggi di errore: dicono che cosa è andato storto e che cosa fare**, in una o due frasi, con il punto: «The link has expired. Ask for a new one.»
- **Puntini di sospensione** «…» (un solo carattere) per le azioni in corso: «Saving…», «Loading…».
- **Virgolette**: nell'inglese le curve “ ” dove l'italiano ha «caporali»; negli attributi e nel codice restano quelle dritte.
- **Date, numeri, importi e durate non si scrivono nel catalogo**: si passano come parametro, già formattati con `fmtDate`, `fmtNumber`, `fmtCurrency`, `fmtDuration` (`web/src/i18n/format.ts`).
- **Plurali**: una voce con le forme `one` e `other` (e `zero` se serve), parametro `{count}`. In inglese «1 day», «2 days»; non scrivere «day(s)».
- **Genere e articoli**: l'italiano concorda («Nuova richiesta», «Nuovo rischio»); in inglese il testo non cambia. Per i titoli con il nome del tipo c'è `newLabel()` in `typesApi.tsx`, che lo sa fare nelle due lingue.
- **Non spezzare le frasi** in più voci da incollare nel codice: l'ordine delle parole cambia da una lingua all'altra. Una frase intera con segnaposto `{nome}`; se dentro c'è del JSX, il componente `<Trans>` (`web/src/i18n/Trans.tsx`).

## Esempi

| italiano | inglese |
| --- | --- |
| Accedi | Sign in |
| Il link non è valido, è scaduto o è già stato usato. | The link isn't valid, has expired, or has already been used. |
| Nessun token in questa organizzazione. | No tokens in this organization. |
| {count} giorni | `{ one: "{count} day", other: "{count} days" }` |
| Salvataggio… | Saving… |
