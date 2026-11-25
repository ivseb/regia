# Primo avvio guidato (proposta 7B)

Pagina `/configurazione/avvio` («Inizia da qui»), solo per `tenant.admin`. Tre schede: **Passi**, **Modelli**, **Cosa è applicato**. Codice: `internal/api/avvio*.go`, `web/src/pages/Avvio.tsx`, `web/src/pages/AvvioStrip.tsx`, migrazione `0061_primo_avvio.sql`.

## Passi (checklist calcolata)
Otto passi (organizzazione e lingua, moduli, persone, team, processo della richiesta, organi di governo, norme, prima richiesta). Lo stato lo calcola `computeAvvio` da ciò che c'è nel tenant: nessuna spunta manuale. Un passo di un modulo spento vale «non serve». La striscia in testa alle pagine resta finché non è tutto fatto; a passi completi si può richiudere (`avvio_stato`) e riaprire dalla pagina.

## Modelli per tipo di azienda
`avvioCatalogo` (`avvio_modelli.go`) elenca i modelli. Un modello **non duplica** niente: rilancia le `Provision` dei moduli (idempotenti), importa gli starter dei cataloghi (`grc.ImportStarter`), accende i moduli e aggiunge gli organi di governo tipici. Disponibile: `banca_assicurazione` (DORA, NIS2, quattro comitati, tutti i moduli). `software_house`, `pmi`, `pa`, `consulente` hanno la struttura ma sono «in arrivo» (`Available: false`: anteprima e applicazione rifiutate con `validation.avvio.model_unavailable`).

L'**anteprima** esegue il modello in un punto di salvataggio (`tx.Begin`) e lo annulla: l'elenco è quello vero, non una descrizione scritta a parte. Ogni voce è «nuovo» o «già presente»; applicare due volte non crea doppioni.

I testi normativi dei cataloghi di partenza sono segnati **«da verificare»** (badge e nota) finché un amministratore non li conferma (`POST /avvio/applicato/cataloghi/{key}/verifica`, riga `catalogo` con `verified_at`). Vale per ogni framework con `frameworks.starter`, anche se importato a mano.

## Cosa è applicato e ripristino
`avvio_applicazioni` ricorda i modelli applicati, gli oggetti che hanno portato e le verifiche. I processi di partenza si riconoscono dalla chiave (registro `engine.Starter`), senza riga. Per ognuno si confronta la versione pubblicata con l'**originale**, cioè l'ultima versione creata dalla stessa transazione di provisioning (stesso `created_at`, `baseline` in `avvio_applicato.go`): non il file nudo, perché i moduli lo integrano alla creazione (gate delle valutazioni e dei requisiti). Se differisce è «modificato» e `Ripristina` (con anteprima della differenza e `from_version` contro le pubblicazioni concorrenti) salva l'originale come bozza e lo pubblica come nuova versione; le richieste già avviate restano sulla loro versione. Limite noto: se un modulo viene provisionato in un secondo momento, le sue integrazioni non rientrano nell'originale.

## Stati vuoti
`web/src/views/ListaVuota.tsx` (liste di richieste, rischi, incidenti, controlli, documenti, applicazioni, asset, impatti: appare se non esiste nemmeno un elemento del tipo), più piccoli interventi in Board, Rischi, Conformità e Documenti. Testi in `lavoro-vuoti.ts`.

## Prova
`internal/itest/avvio_test.go` e `web/e2e/ondata3-avvio.spec.ts` (tenant `avvio` creato da `scripts/dev-seed.sh`, account `avvio@demo.test`).
