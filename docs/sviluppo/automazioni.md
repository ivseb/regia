# Automazioni

Le regole dei processi (`engine.Rule`, editor «Quando… allora…», vedi [regole-frase.md](regole-frase.md)) sono automazioni vere, come quelle di Jira Automation o i workflow di YouTrack. Oltre alla creazione e alle fasi, reagiscono al cambio di un campo, a una data dell'elemento e al calendario. Fanno anche più cose: impostano campi, mandano notifiche, commentano, creano elementi collegati e chiamano webhook.

Il formato salvato resta quello di sempre: `{key, name, on, when, actions[]}`. Cambiano solo i valori ammessi per `on` e i tipi di azione. Le regole già pubblicate non cambiano. Codice: `internal/engine/automation*.go`, migrazione `0100_automazioni.sql`.

## Eventi (`on`)

| Valore | Quando scatta |
|---|---|
| `create` | l'elemento nasce |
| `enter:<fase>` / `exit:<fase>` | entra nella fase o ne esce |
| `change:<campo>` | cambia un campo personalizzato (per chiave) o il titolo (`change:titolo`) |
| `at:<data>:<±giorni>` | N giorni prima (negativo) o dopo (positivo) una data dell'elemento; `0` = il giorno stesso |
| `every:day` | ogni giorno |
| `every:week:<1-7>` | ogni settimana, nel giorno indicato (1 = lunedì … 7 = domenica) |

**Cambio di campo.**
- Scatta solo se il valore salvato cambia davvero: riscrivere lo stesso valore non fa scattare nulla.
- Nella condizione `when` valgono i fatti di sempre, con il valore nuovo, più `prima.<campo>` con il valore precedente (e `prima.<campo>.<sotto>`, per esempio la valuta). Esempio: `prima.priorita: bassa priorita: alta`.
- Si vedono i cambi fatti con `engine.Update`, cioè i campi personalizzati e il titolo dalla scheda, dall'API e dalle azioni delle regole.
- Restano fuori le scritture dirette dei moduli: assegnatario, date della pianificazione, genitore, rango.

**Date per `at:`** (`Service.DateSources`):
- `scadenza` e `inizio`: `due_at` e `start_at` dell'elemento;
- `campo.<chiave>`: i campi data o data e ora del tipo;
- quelle che i moduli registrano con `DescribeDate(tipo, chiave, nome, sql)`, dove la query restituisce `element_id` e `at`. Oggi il GRC registra `evidenza.scadenza` sulle evidenze e `evidenze.prossima_scadenza` sui controlli (la prossima scadenza delle evidenze valide). Esempio: `at:evidenze.prossima_scadenza:-30` sul processo dei controlli.

**Elementi coinvolti** dagli eventi a tempo e periodici: quelli della versione del processo che porta la regola, non cancellati, non in bozza e non chiusi (categoria della fase diversa da `done` e `cancelled`).

## Azioni nuove (`actions[].kind`)

| Kind | Parametri | Effetto |
|---|---|---|
| `set_field` | `params.field` (chiave o `titolo`), `params.mode` = `value` (predefinito) \| `today` \| `clear`, `params.value` (modello), `params.days` | imposta il campo: un valore da modello, oggi ± N giorni, oppure lo svuota |
| `notify` | `params.message` (modello) e almeno un destinatario tra `member` (id), `group` (nome), `role` (ruolo sull'elemento) e `params.followers = "true"` | notifica di tipo `automation` |
| `add_comment` | `params.text` (modello) | commento sull'elemento: l'autore è chi ha causato l'evento, oppure nessuno («Automazione») |
| `create_linked` | `params.type`, `params.title` (modello), `params.relation`, `params.direction` = `out` (predefinito) \| `in`, `params.space` (vuoto = lo spazio dell'elemento) | crea un elemento collegato con la relazione; l'elemento nuovo avvia il suo processo |
| `webhook` | `params.webhook` (nome di un webhook configurato) | manda l'evento `automation.rule` solo a quel webhook |

**Set_field.** Il valore passa per la stessa normalizzazione dell'interfaccia:
- le opzioni si scrivono per nome;
- i booleani come «sì» e «no»;
- per i campi multipli i valori si separano con la virgola.

**Create_linked.** Crea solo tipi senza tabella di estensione (`element_types.extension` nullo). Le richieste, i rischi o gli incidenti hanno dati del modulo che nascono dalla loro pagina. La validazione lo rifiuta alla pubblicazione e l'editor non li offre.

**Webhook.**
- L'URL, il segreto e la protezione SSRF sono quelli già configurati dall'amministratore del tenant: le regole non contengono URL.
- L'evento va nell'outbox con `webhook_id`. Il lavoratore dei webhook lo consegna solo a quel webhook, senza guardare iscrizioni e filtro.
- Il campo `data` del payload contiene regola, chiave, evento, versione e, se c'è, l'occorrenza.
- `automation.rule` non è nel catalogo degli eventi a cui ci si iscrive.

**Modelli di testo** (`engine.Render`):
- segnaposto fissi: `{{titolo}}`, `{{chiave}}`, `{{fase}}`, `{{tipo}}`, `{{spazio}}`, `{{data}}` (oggi nel fuso del tenant), `{{regola}}`, `{{link}}` (`/e/<chiave>`);
- in più `{{campo.<chiave>}}`, `{{prima.<chiave>}}` e qualunque fatto dell'elemento, per esempio `{{ruolo.sponsor}}`;
- sono segnaposto e basta, senza logica: uno sconosciuto diventa vuoto.

**«Aggiungi requisito»** non è un'azione a sé. Lo coprono `apply_baseline` (anche una baseline di un solo requisito) e `create_linked`.

## Garanzie

**Idempotenza.** `rule_runs` ha una riga per processo, regola, elemento e occorrenza, ed è unica. Il lavoratore prenota la riga prima di eseguire: se c'è già, l'occorrenza è già stata fatta.
- L'occorrenza è la data calcolata per `at:` e il giorno del giro per `every:`.
- Se la data dell'elemento si sposta, l'occorrenza cambia e la regola scatta di nuovo.
- Una versione nuova che tiene la stessa chiave non riesegue le occorrenze già fatte.

**Recupero.**
- Un'occorrenza scatta se è caduta negli ultimi 7 giorni (lavoratore fermo, server spento) e non prima del giorno di pubblicazione della versione, così una regola nuova non scatta su date passate.
- Le occorrenze di oggi scattano dal primo giro dopo `run_hour`.
- La condizione si valuta una volta, all'occorrenza. Se è falsa, la riga resta `skipped`: le righe `skipped` si cancellano dopo 30 giorni.

**Nessun ciclo** (`ruleChain` nel contesto):
- una modifica fatta da un'azione di regola (campo impostato, elemento creato) è a sua volta un evento, ma le sue regole scattano solo al primo livello;
- le modifiche fatte da quelle regole non fanno scattare altro;
- una regola non riscatta mai dentro la propria catena.

**Tetto.** `automation_settings.max_per_run` (predefinito 200) limita le prenotazioni per giro e per tenant. Il resto passa al giro successivo, ogni 15 minuti, e nulla si perde.

**Errori.**
- Le azioni nuove girano in un savepoint. Un valore rifiutato o un riferimento sparito lascia nella storia la nota «non applicata: …» invece di bloccare la modifica di chi ha causato l'evento.
- Nei giri a tempo ogni occorrenza è una transazione a sé. Se un'azione fallisce, gli effetti si annullano e la riga va in `error` con il motivo.

**Storia e audit.**
- La voce `rule` della storia porta `rule` (nome), `key`, `event`, `process_id`, `version` e, per i giri, `occurrence` e `system`.
- Le modifiche fatte da una regola hanno `rule`, `rule_version` e `process_id` nel payload dell'audit (`element.update`, `element.create`).
- Ogni esecuzione a tempo scrive `automation.run`, con l'esito.

**Permessi.**
- La regola agisce con l'autorità della versione pubblicata, cioè di chi l'ha pubblicata con `process.admin`, non con i permessi di chi causa l'evento. Così fa già `assign_role`.
- Nei giri a tempo l'attore è «Automazione»: nessun membro, con i permessi del motore.
- Le notifiche arrivano solo a chi può leggere l'elemento (`notify.Emit`).
- I webhook sono solo quelli dell'amministratore del tenant.
- La creazione rispetta i tipi ammessi nello spazio.

**Fuso orario.** Vale `tenants.default_timezone`, predefinito Europe/Rome. «Oggi» è il giorno in quel fuso.
- Una data senza ora (salvata a mezzanotte UTC) vale quel giorno, cioè da mezzanotte ora del tenant.
- Una data e ora si porta nel fuso del tenant.

## Impostazioni e API (process.admin)

- `GET|PUT /api/v1/automation/settings`: `{enabled, run_hour, max_per_run}`. Spente, le regole a tempo e periodiche non girano; quelle sugli eventi girano sempre.
- `POST /api/v1/rules/preview` `{type, on, condition, days}`: anteprima dell'editor.
  - Per `at:` conta gli elementi con un'occorrenza tra oggi e oggi + `days` e la condizione vera oggi.
  - Per `every:` conta quelli del prossimo giro (`from`).
  - Per gli altri eventi è come `/conditions/count`.
- `GET /api/v1/processes/{ref}/rules/{rule}/runs`: ultime esecuzioni di una regola a tempo, senza le `skipped`.
- `GET /api/v1/processes/{ref}/meta` porta anche `fields`, `dates`, `types`, `relations`, `webhooks`, `spaces` e `templates` per l'editor.
- Lavoratore: `engine.RunAutomations`, ogni 15 minuti, avviato in `cmd/regia/serve.go`.

## Validazione

Nuovi codici `validation.rule.*`:
- `field_unknown`: il campo di `change:` non è del tipo;
- `date_unknown`: la data di `at:` non è del tipo;
- `event_invalid`: anche per i formati nuovi sbagliati.

I parametri delle azioni nuove si controllano come quelli dei moduli (`param_required` e `param_invalid`).

## Fuori, per ora

- Cambi fatti fuori da `engine.Update`.
- Un'azione «cambia fase».
- Rami se/altrimenti, attese e catene con ritardi.
- Condizioni su più elementi.
- Pianificazione al minuto.
- Una pagina del registro delle esecuzioni.
- «Esegui ora».
- Creazione collegata di tipi con dati del modulo.
