# Processi guidati (ondata 3, proposta 8)

Configurare un processo senza sapere come, e niente più JSON o sintassi nelle schermate.

## Editor dei processi (`web/src/pages/processEditor/`)

- **Diagramma adattivo** (`Editor.tsx`, `Diagram.tsx`): la scala si adatta al riquadro (da 35 % a 200 %); pulsanti −, +, «Adatta» (torna all'adattamento), «100 %» (dimensioni reali) e «Centra sulla fase». Selezionando una fase che non si vede per intero, il riquadro scorre da solo fino a lei, quindi anche la fase appena creata è visibile. Sul telefono resta l'elenco dei passi (`PhaseList`).
- **«Aggiungi un passo»** (`AddStep.tsx`, `addStep` in `model.ts`): dopo quale fase, nome, chi lo fa avanzare (ruoli da menu), condizione facoltativa (`ConditionBuilder`) e gate facoltativo, tutto nello stesso riquadro. `addStep` crea la fase, il passaggio che ci entra e sposta sul passo nuovo i passaggi che prima andavano dalla fase scelta alla successiva (con le loro regole), così il passo non si può saltare.
- **Parità di priorità** (`priorityTies` in `model.ts`, `Settings` in `Processes.tsx`): se un altro processo dello stesso tipo ha la stessa priorità, avviso «quale deve vincere?» (vale per tutti gli elementi / stessa condizione / condizioni che possono valere insieme). Il backend (`UpdateProcess`) rifiuta con `validation.process.priority_required` il passaggio a condizione vuota senza priorità, e con `validation.process.priority_negative` una priorità negativa.

## Elenco dei processi (`web/src/pages/Processes.tsx`)

- La condizione è una frase («Solo se …», con i nomi dei dati del tipo), mai il codice.
- **Mappa dei processi**: schede con miniatura del diagramma (`Miniatura.tsx`), versione, bozza, stato di migrazione («12 richieste sono ancora su v2 …») e avviso di parità. Sul telefono è la vista predefinita e unica; sul resto si alterna con l'elenco. Il conteggio per versione viene già dall'API (`versions[].elements`): nessuna modifica al backend per questo.

## Rischi (`web/src/pages/RiskConfig.tsx`, `riskConfig/`)

- **Accettazione del rischio**: per ogni regola i passi e, in ogni passo, gli approvatori (ruolo, organo o persona) come righe e menu (`Politica.tsx`).
- **Incidenti**: ogni tappa ha righe «entro N ore/giorni da rilevamento / classificazione / scadenza di un'altra tappa» (`Scadenze.tsx`) e una linea del tempo; criteri ed esito, in lettura, sono frasi.
- Il formato verso l'API non cambia. Il testo grezzo (`a + b → c`, `detected + 24h`) sta solo in «Avanzato», chiuso, e si applica uscendo dal campo. Funzioni pure in `riskConfig/model.ts`.

## Valutazioni

`ReviewConfig.tsx` mostrava già esiti e ruoli come righe e menu (nessun testo con `|` o virgole): nulla da cambiare.

## Test

`web/test/processiGuidati.test.ts` (modello), `internal/itest/process_editor_test.go` (priorità obbligatoria), `web/e2e/ondata3-processi.spec.ts` (percorsi e 375 px).
