# Demand e richieste

## API della fetta verticale del demand
| metodo e percorso | cosa fa |
| --- | --- |
| `POST /api/v1/demand/requests` | nuova richiesta in bozza (richiedente = chi la crea) |
| `PATCH /api/v1/demand/requests/{id}` | dati della domanda: stime, driver, date, unità |
| `GET /api/v1/elements?q=…&cursor=…` | ricerca con il linguaggio di query, paginazione a cursore |
| `GET /api/v1/elements/{DEM-12 o id}` | dettaglio: elemento, stakeholder, passaggi disponibili, dati della domanda, approvazioni |
| `PATCH /api/v1/elements/{id}` | titolo, testo, campi personalizzati (con `version`) |
| `POST /api/v1/elements/{id}/transitions` | cambio di stato secondo il workflow |
| `GET /api/v1/elements/{id}/activity` | storia |
| `GET /api/v1/approvals/inbox` | passi che attendono la mia decisione |
| `POST /api/v1/approvals/steps/{id}/decision` | approved, approved_with_conditions, rejected, deferred |
| `GET/POST /api/v1/org-units`, `GET /api/v1/members` | organizzazione |

### Linguaggio di query (piano, passo 16)
Grammatica completa e strategia di prestazioni: [architecture.md §6](../../replica/architecture.md). In breve:
- **Condizioni** `campo: valore` in italiano o inglese.
  - Più valori in alternativa con la virgola o con `oppure`; valori tra virgolette.
  - Negazione con `-campo:`; `vuoto` e `presente`.
  - Operatori `>`, `>=`, `<`, `<=` e intervalli `a..b`.
- **Campi di sistema:** `tipo`, `fase` (o `stato`, anche `aperta`/`chiusa`), `spazio`, `chiave`, `numero`, `assegnatario`, `segnalatore`, `genitore`, `aggiornata`, `creata`, `risolta`, `inizio`, `fine`, `osservati: me`.
- **Campi dei moduli:** `team`, `applicazione`, `asset`, `richiesta`, `scadenza`, `desiderata`, `scadenza_vulnerabilità` (applicazioni: scadenza delle vulnerabilità da correggere; `<oggi` = scadute).
- **Relazioni** (ondata 3 dell'AI, `internal/engine/query_rel.go`):
  - `collegata: (sottoquery)` trova gli elementi collegati a quelli della sottoquery tra parentesi, con qualsiasi relazione e nei due versi; anche con chiavi (`collegata: DEM-12`), `presente` e `vuoto`, e negata (`-collegata: presente` = senza collegamenti);
  - al posto di `collegata` un tipo di relazione per chiave (`depends_on`, nel verso di chi parte) o per nome nel suo verso (`"dipende da"`, `"è prerequisito di"`, anche `dipende_da`);
  - una sottoquery vale anche per i campi che puntano a elementi: `genitore: (tipo: epic fase: aperta)`, `applicazione: (tipo: application criticità: Alta)`, i campi personalizzati di tipo elemento;
  - le parentesi aprono una sottoquery solo subito dopo `:` o `,` (nel testo libero restano parole); al massimo due livelli, niente ordinamento dentro;
  - **visibilità:** la sottoquery si risolve con le stesse regole della ricerca (`visibleWhere`, le regole di `CanRead` in SQL): trova solo elementi che la persona può leggere, e `presente` conta solo i collegamenti verso elementi leggibili, così una condizione non rivela un collegamento con un elemento nascosto; una chiave che la persona non vede vale come inesistente;
  - **limiti:** una sottoquery trova al massimo `engine.SubqueryMaxIDs` elementi (2000), oltre chiede di restringerla; la condizione usa gli indici `relations_out_idx` e `relations_in_idx`.
- **Campi personalizzati:** per chiave o alias.
- **Date:** `2026-11-24`, `oggi`, `ieri`, `settimana`, `mese`, `-7g`, `+30g`.
- **Testo libero:** ricerca full-text; `parola*` per i prefissi; una chiave come `DEM-12` trova l'elemento.
- **Ordinamento:** `ordina per: campo [asc|desc]` per qualunque campo ordinabile.
- **Errori:** 422 `query.invalid` con `pos` ed `end` (unità UTF-16).

Esempi:
- `tipo: business_request fase: Triage, Valutazione urgenza: Alta aggiornata: -30g -assegnatario: me fornitori ordina per: fase`
- `tipo: application collegata: (tipo: risk fase: aperta)` (applicazioni collegate a un rischio aperto)
- `tipo: application scadenza_vulnerabilità: <oggi` (applicazioni con vulnerabilità scadute)
- `tipo: business_request "dipende da": (tipo: business_request fase: aperta)`

| metodo e percorso | cosa fa |
| --- | --- |
| `GET /api/v1/query/suggest?q=…&pos=…` | autocompletamento alla posizione del cursore (unità UTF-16): intervallo da sostituire, proposte, primo errore |
| `GET /api/v1/query/fields` | campi interrogabili, con le opzioni degli elenchi (colonne della vista tabella) |

I moduli aggiungono campi con `eng.QueryField(engine.QueryField{...})`: nomi, sorgente `(element_id, v)` su colonne indicizzate, risoluzione dei nomi e suggerimenti (esempi in `internal/modules/architecture/query.go` e `internal/modules/demand/query.go`).

### Configurazione iniziale del demand (`demand.Provision`)
- spazio `DEM` e tipo `business_request` con lo stage-gate di `demand.md` §4;
- workflow con ruoli: il richiedente invia, il demand manager fa triage e valutazione, demand manager o PMO prioritizzano e chiedono l'approvazione;
- guardie: "stima presente" e "sponsor presente";
- gruppi "Demand management" e "PMO" e organo "Comitato investimenti";
- politica di approvazione con le soglie di default (fino a 50 k€ lo sponsor; fino a 250 k€ sponsor e poi PMO; oltre anche il comitato);
- esiti: approvata → Pianificazione, respinta → Respinta, rinviata → Prioritizzazione.

## Processi configurabili (sdlc.md §10)

- **API:**
  - `GET/POST /api/v1/processes`, `GET/PATCH /api/v1/processes/{chiave}`;
  - bozza: `PUT` e `DELETE` su `/api/v1/processes/{chiave}/draft`;
  - pubblicazione: `POST /api/v1/processes/{chiave}/publish`;
  - export e import: `GET /api/v1/processes/{chiave}/export?version=N`, `POST /api/v1/processes/import?publish=true`;
  - migrazione: `POST /api/v1/processes/{chiave}/migration/preview` e `POST /api/v1/processes/{chiave}/migration` con `phase_map`;
  - condizioni: `POST /api/v1/conditions/check` con `condition` ed eventualmente `element`;
  - editor visuale: `GET /api/v1/processes/{chiave}/meta` (ruoli, gruppi, controlli, politiche di approvazione per fase, fatti disponibili per il tipo con esempi, tipi di azione delle regole con lo schema), `POST /api/v1/processes/{chiave}/check` con `definition` (problemi della validazione, senza salvare), `GET /api/v1/process-catalog` (tipi di elemento e pacchetti di partenza).
- **Per i moduli:** `eng.DescribeFacts(tipo, ...)` dà nome, tipo e valori suggeriti ai fatti che il modulo fornisce; `eng.DescribeAction(schema)` descrive un tipo di azione delle regole; `eng.Starter(json)` registra un pacchetto di partenza offerto alla creazione di un processo.
- **Test del frontend:** `cd web && pnpm test` (modello dell'editor dei processi, con `node:test`).
- **Permesso:** `process.admin`, dato dal ruolo `process_admin` oppure dall'amministratore del tenant.
- **Pacchetto di partenza del demand:** `internal/modules/demand/starter/demand_standard.json`.
- **Dopo l'aggiornamento:** `regia tenant provision --slug <tenant>` porta i tenant esistenti sul processo.

## Architettura e impatti (sdlc.md §10, passo 2)

- **API del catalogo:**
  - `GET/POST /api/v1/architecture/assets`;
  - composizione: `POST /api/v1/architecture/assets/{id}/components`, `DELETE /api/v1/architecture/assets/{id}/components/{app}`;
  - applicazioni: `GET/POST /api/v1/architecture/applications`, `PATCH /api/v1/architecture/applications/{id}`;
  - import: `POST /api/v1/architecture/applications/import?dry_run=true`, con corpo `{"csv": "..."}`;
  - team: `GET/POST /api/v1/teams`, `PATCH /api/v1/teams/{id}`.
- **API dell'analisi tecnica:**
  - analisi: `GET /api/v1/demand/requests/{chiave}/analysis`;
  - asset citati: `POST /api/v1/demand/requests/{chiave}/assets`, `DELETE /api/v1/demand/requests/{chiave}/assets/{id}`;
  - impatti: `POST /api/v1/demand/requests/{chiave}/impacts`, `PATCH /api/v1/impacts/{id}`.
- **Permesso:** `architecture.write`, dato dal ruolo `architect` o dall'amministratore del tenant.
- **Dati di esempio:** `scripts/dev-demo-architecture.sh [url]`, che usa `scripts/demo-applicazioni.csv`.
- **Proxy:** `REGIA_TRUSTED_PROXIES`, con reti separate da virgole, indica da chi accettare `X-Forwarded-For`.

## Valutazioni con questionari (sdlc.md §10, passo 4)

- **Valutazioni di un elemento:**
  - `GET /api/v1/elements/{richiesta o impatto}/reviews`: valutazioni (anche degli impatti, per una richiesta), conteggi per i gate, tipi apribili dall'attore, impatti;
  - `POST /api/v1/elements/{richiesta o impatto}/reviews` con `{"type": "dpia_preliminare", "impact": "DEM-9"}` (l'impatto serve solo per i tipi con ambito impatto aperti dalla richiesta).
- **La valutazione:**
  - `GET /api/v1/reviews/{chiave}`: elemento, passaggi, fasi, questionario della sua versione, risposte con autore e origine, fatti, valutazione delle condizioni, assegnazioni, prescrizioni, esiti, permessi (`can`);
  - `PUT /api/v1/reviews/{chiave}/answers` con `{"answers": {"esposto_internet": true, "paesi": null}}`: salvataggio parziale, `null` cancella;
  - `GET /api/v1/reviews/{chiave}/history?question=…`: storia delle risposte;
  - `PUT /api/v1/reviews/{chiave}/assignments/{sezione}` con `{"member_id"}` (vuoto toglie l'assegnazione);
  - prescrizioni: `POST /api/v1/reviews/{chiave}/prescriptions` con `{"text", "category"}`, `PATCH …/prescriptions/{id}` con `{"status": "accepted" | "rejected" | "proposed", "text", "category"}`, `DELETE …/prescriptions/{id}` (solo quelle manuali);
  - esito: `POST /api/v1/reviews/{chiave}/decision` con `{"outcome", "note"}`;
  - i passaggi di fase sono quelli del motore: `POST /api/v1/elements/{id}/transitions`.
- **Inbox:** `GET /api/v1/reviews/inbox` → `{"compile": [...], "team": [...]}`, per scadenza.
- **Configurazione** (scrittura con `process.admin`, lettura con `element.read`):
  - `GET /api/v1/review-config`: team, tipi, modelli, gruppi, esiti di default;
  - team: `POST /api/v1/review-config/teams`, `PATCH /api/v1/review-config/teams/{id o chiave}` (`group_id` o `group`, `lead_id`, `sla_days`, `reminder_days`, `escalation_days` con 0 = mai, `archived`);
  - tipi: `POST /api/v1/review-config/types`, `PATCH /api/v1/review-config/types/{id o chiave}` (`team_id` o `team`, `scope`, `template_key`, `mandatory`, `outcomes`, `deciders`, `owner_roles`, `archived`);
  - modelli: `GET /api/v1/review-config/templates/{chiave}`, `GET …/versions/{n}`, `PUT` e `DELETE …/draft`, `POST …/publish`, `POST /api/v1/review-config/templates/validate` con `{"definition"}`;
  - export e import: `GET /api/v1/review-config/export`, `POST /api/v1/review-config/import?publish=true` (formato `regia.reviews/v1`).
- **Regole di processo:** azioni `open_review` (`ref` = tipo, `when` = condizione sui fatti di ogni impatto), `sync_reviews` (sull'impatto), `review_stage` (`ref` = `compile`, `submitted`, `exam`, `clarification`, `cancelled`).
- **SLA:** job nel processo ogni 15 minuti (`reviews.RunSLA`); i giorni sono lavorativi secondo `internal/platform/calendar`.
- **Eventi in tempo reale:** il salvataggio automatico delle risposte non manda l'evento a tutto il tenant.
- **Ruolo** «Valutatore» (`assessor`): `element.read` e `comment.write`.
- **Pacchetto di partenza:** `internal/modules/reviews/starter/` (`reviews.Provision`, anche con `regia tenant provision`).
- **Dati di esempio:** `scripts/dev-demo-reviews.sh [url]`, richiamato da `dev-demo-data.sh`.

### Editor visuale dei questionari (piano, passo 7)

Configurazione → Valutazioni → Modelli (`web/src/pages/questionnaireEditor/`).
- **Schede:** Struttura, Punteggio ed esiti, Regole, Anteprima (a fianco sopra 1280 px), Versioni, JSON (vista avanzata).
- **Struttura:** sezioni e domande trascinabili (con ↑ ↓ per tocco e tastiera), domanda a fisarmonica. Tipi: sì/no, scelta singola e multipla, testo, numero, data, allegato, **scala** (`scale`, intero tra `min` e `max`, default 1-5, etichette agli estremi) e **matrice** (`matrix`, righe in `rows`, colonne in `options`; la risposta è `{riga: colonna}`, nelle condizioni vale `riga.colonna`).
- **Mostra se:** costruttore a righe dell'editor dei processi (`ConditionBuilder`), con le domande precedenti come «fatti» e i dati della richiesta o dell'impatto da `GET /api/v1/review-config/template-facts` (richiede `process.admin`). Lo stesso vale per il salto di sezione (`visible_when` della sezione).
- **Punteggio:** `weight` della domanda (default 1, 0 la esclude), `score` di ogni scelta, `yes_score` e `no_score`, scala (valore meno il minimo, o il contrario con `reverse`), matrice (somma sulle righe). Il punteggio è la somma pesata sul massimo ottenibile dalle domande visibili; le domande senza risposta valgono zero. `scoring.thresholds` (`{min, outcome}`, percentuale e chiave dell'esito del tipo) fissa l'esito **proposto**: lo decide sempre il team (la pagina della valutazione lo mostra solo al team, con «Scegli questo»). `Evaluation` torna `scored`, `score`, `max_score`, `percent`, `proposed_outcome`; il calcolo è in `questionnaire.go` e, uguale, in `web/src/condition.ts` (stessi numeri nei test).
- **Regole:** le `derivations` hanno `kind` `prescription` (default) o `requirement`.
  - `prescription`: prescrizione proposta al team, che la accetta o la scarta; se accettata diventa un requisito con origine `prescription`.
  - `requirement`: **requisito proposto** (`requirement_proposals` nella vista e `evaluation.derived_requirements`), senza accettazione. Campi in più: `title` (se manca si ricava dal testo), `method` (`manual`, default, o `evidence` con `check` = chiave del controllo); la categoria di default è «Requisito». Alla chiusura della valutazione con esito favorevole o con prescrizioni diventa un requisito vero del modulo requisiti (origine `questionnaire`, vedi `requisiti.md`); con esito non favorevole non nasce niente.
  - **Transizione:** un questionario pubblicato non cambia (una versione resta immutabile): cambia il significato di `kind: requirement` per il codice. Le prescrizioni già create da quelle regole restano prescrizioni (nessuna migrazione dei dati); per le valutazioni ancora aperte le prescrizioni di quel tipo non ancora decise spariscono e al loro posto compare il requisito proposto, mentre se la prescrizione era già accettata o scartata la regola non propone anche il requisito.
- **Assegnazione:** `filled_by: "team"` su una sezione la riserva al team valutatore (e a chi ha la sezione assegnata a mano); vuoto = chi avvia il questionario e il team.
- **Versioni:** bozza salvata da sola, pubblicazione con «cosa cambia» in italiano (`describeDiff`), differenze tra due versioni qualsiasi, «Riprendi nella bozza», duplica come nuovo modello, esporta e importa JSON (`regia.questionnaire/1`, anche la sola definizione). Le valutazioni aperte restano sulla loro versione.
- **Validazione dal vivo:** i controlli del server (`POST …/templates/validate`) più quelli locali (`analyze`): domande o sezioni che non si vedono mai, condizioni su domande successive o sconosciute, sezioni vuote, soglie senza punteggio.
- **Test:** `internal/modules/reviews/scoring_test.go`, `internal/itest/questionari_test.go`, `web/test/questionnaire.test.ts`.

## Campi personalizzati e pagina di dettaglio (sdlc.md §10)

- **Configurazione** (`fields.admin` o `tenant.admin`, verificati dal motore):
  - `GET /api/v1/fields`: campi con opzioni, insieme condiviso, associazioni (`types`), `in_use`;
  - `GET /api/v1/fields/catalog`: tipi con le fasi, gruppi, insiemi di valori, valuta base, tipi di dato, colori;
  - `POST /api/v1/fields` con `{"key", "name", "data_type", "multi", "help", "default", "settings", "aliases", "options": [{"name", "color"}]}` oppure `"option_set_id"` per condividere un insieme;
  - `GET`, `PATCH`, `DELETE /api/v1/fields/{chiave o id}` (`PATCH` con `"archived": true` archivia; `DELETE` solo senza valori);
  - `PUT /api/v1/fields/{chiave}/options` con `{"options": [{"id", "name", "color", "archived"}]}`: l'elenco nell'ordine voluto;
  - `PUT /api/v1/fields/{chiave}/types/{tipo}` con `{"required", "required_from", "visible_when", "section", "ordinal"}`, `DELETE` per togliere l'associazione;
  - `PUT /api/v1/types/{tipo}/fields/order` con `{"fields": ["canale", ...]}`;
  - `GET /api/v1/fields/export`, `POST /api/v1/fields/import?dry_run=true` (formato `regia.fields/v1`) → `{"report": {"created", "updated", "associations", "warnings"}}`.
- **Uso:**
  - `GET /api/v1/types/{tipo}/fields`: campi del tipo per i form (opzioni, predefiniti, regole);
  - `GET /api/v1/elements/{chiave}` porta `custom_fields`: valore, `visible`, `required_now`, etichette dei riferimenti;
  - i valori si scrivono con `PATCH /api/v1/elements/{id}` (`{"version", "fields": {"canale": ["Email"], "golive": null}}`, `null` svuota) o alla creazione (`fields`);
  - `GET /api/v1/elements?q=…` restituisce anche `labels` (elementi e gruppi citati dai campi, per id);
  - `GET /api/v1/elements/{chiave}/links`: genitore, figli, relazioni;
  - `GET /api/v1/groups`: gruppi (per i campi di tipo gruppo).
- **Gate implicito:** i passaggi verso una fase in cui un campo visibile è obbligatorio rispondono 422 con «per passare a «Fase» compila: …»; `transitions[].blocked` lo riporta.
- **Ruolo** «Amministratore dei campi» (`fields_admin`): `element.read` e `fields.admin`.
- **Pagine:** `/configurazione/campi`, `/e/{chiave}` (alias `/richieste/{chiave}`, `?scheda=dettagli|analisi|valutazioni|piano|approvazioni|allegati`).
- **Dati di esempio:** `scripts/dev-demo-fields.sh [url]`, richiamato da `dev-demo-data.sh`.

## Status report, cruscotto e portale (sdlc.md §10, passi 13 e 14)

- **Status report:**
  - della richiesta: `GET /api/v1/demand/requests/{chiave}/status-reports` (storico dal più recente, periodicità, `can_edit`), `GET …/status-reports/prefill?period_end=` (precompilazione senza salvare), `POST …/status-reports` con `{"period_end"}` (bozza precompilata), `PUT …/status-reports/schedule` con `{"frequency": "" | weekly | biweekly | monthly | off, "next_due"}`;
  - il report: `GET`, `PATCH` (con `version`, semafori `green`/`amber`/`red`, `period_start`, `period_end`, `sections`), `DELETE` (solo bozze) su `/api/v1/status-reports/{id}`; `POST …/refresh` (ricalcola i dati), `POST …/submit`, `POST …/acknowledge`, `GET …/compare?with={id}` (vuoto = il precedente), `GET …/export.md`;
  - impostazioni: `GET/PUT /api/v1/report/settings` con `{frequency, reminder_days, phases, regulatory: [{label, query}]}` (scrittura con `portfolio.admin` o `tenant.admin`).
- **Cruscotto:** `GET /api/v1/dashboard/portfolio?unit=&driver=&from=&to=` (`element.read`; il dettaglio finanziario con `finance.read`); pannelli della persona: `GET/PUT /api/v1/dashboard/portfolio/layout` con `{"panels": [{"key", "visible"}]}`.
- **Portale:** `GET /api/v1/portal` (benvenuto, tipi con i campi, unità, bozze, le mie richieste); bozze: `POST /api/v1/portal/drafts`, `PUT` e `DELETE /api/v1/portal/drafts/{id}`, `POST …/{id}/submit` (crea e invia la richiesta); `GET /api/v1/portal/requests/{chiave}`; configurazione: `GET/PUT /api/v1/portal/settings` (`tenant.admin` o `portfolio.admin`). Servono `portal.submit` o `element.write`.
- **Campi di query nuovi:** `unita`, `driver`, `obbligatoria: sì`, `budget_approvato: presente`, `con_costi: presente`, `semaforo: rosso`, `prossimo_report: <oggi`, `valutatore: Sicurezza`, `scadenza_sla: <oggi`.
- **Notifiche:** tipo `report` (promemoria al PM con `event` `due`/`overdue`, invio con `submitted`).
- **Job:** `regia serve` controlla ogni ora periodicità e promemoria (`report.RunReminders`).
- **Pagine:** scheda «Stato» in `/e/{chiave}`, `/cruscotto`, `/portale`, `/portale/nuova`, `/portale/richieste/{chiave}`, `/configurazione/avanzamento`. I membri `requester` vedono solo il guscio del portale.
- **Dati di esempio:** `scripts/dev-demo-report.sh [url]`, richiamato da `dev-demo-data.sh`.
