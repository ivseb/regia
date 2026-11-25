# Lavoro

## Pianificazione e tempo reale

- **API:**
  - piano: `GET /api/v1/demand/requests/{chiave}/plan`;
  - piano automatico: `POST /api/v1/demand/requests/{chiave}/plan/auto`, con `{"replan": true, "from": "AAAA-MM-GG"}`;
  - spostamenti: `PUT /api/v1/impacts/{id}/schedule`, con `{start, end, cascade}`, e `PUT /api/v1/demand/requests/{chiave}/schedule`;
  - roadmap: `GET /api/v1/demand/roadmap?closed=true`;
  - carico: `GET /api/v1/teams/load?from=…&to=…`.
- **Eventi:** `GET /api/v1/events` (SSE). Il server lo chiude allo spegnimento (`Server.Close`).
- **Dati di esempio:** `scripts/dev-demo-planning.sh [url]`.

## Commenti, allegati e notifiche (sdlc.md §10, passo 3)

- **API dei commenti:**
  - `GET/POST /api/v1/elements/{chiave o id}/comments`, con `{"body": "markdown"}`;
  - `PATCH /api/v1/comments/{id}` (solo l'autore) e `DELETE /api/v1/comments/{id}` (autore o amministratore).
- **API degli allegati:**
  - elenco: `GET /api/v1/elements/{chiave o id}/attachments`, con anche `max_bytes`;
  - caricamento: `POST /api/v1/elements/{chiave o id}/attachments` in `multipart/form-data`, campo `file` e facoltativo `comment_id`;
  - download: `GET /api/v1/attachments/{id}/content`, con `?download=1` per forzare il salvataggio anche delle immagini;
  - eliminazione logica: `DELETE /api/v1/attachments/{id}`.
- **API degli osservatori:** `GET`, `PUT` (segui) e `DELETE` (non seguire) su `/api/v1/elements/{chiave o id}/watch`.
- **API delle notifiche:**
  - elenco: `GET /api/v1/notifications?unread=true&limit=50&cursor=…`;
  - non lette: `GET /api/v1/notifications/count`;
  - lettura: `POST /api/v1/notifications/{id}/read` e `POST /api/v1/notifications/read-all`;
  - preferenze: `GET/PUT /api/v1/me/notification-prefs`, con `{"kinds": [{"kind": "comment", "in_app": true, "email": false}]}`.
- **Tipi di notifica:** `mention`, `comment`, `role`, `phase`, `approval`, `referent`.
- **Permessi:** `comment.write` per commentare (ora anche nel ruolo del richiedente); `comment.write` o `element.write` per allegare; leggere l'elemento basta per leggere commenti e allegati.
- **File:** in `<data>/files`, per sha256 e con deduplica. Vanno inclusi nel backup insieme al database.

## Work management e assegnazione ai team (piano, passi 10 e 18)

Disegno e decisioni: [replica/work.md](../../replica/work.md).

- **API degli spazi:** `GET /api/v1/work/spaces` (spazi dei team e team senza spazio, con la chiave proposta), `POST /api/v1/work/spaces` con `{"team_id", "key"}` (`architecture.write` o `tenant.admin`). Un team creato con `POST /api/v1/teams` riceve subito il suo spazio.
- **API della board:**
  - `GET /api/v1/work/spaces/{chiave}/board?q=&sprint=&swimlane=`: colonne con conteggi e WIP, swimlane, card compatte, persone, epic, priorità, sprint (`sprint` vuoto = quello attivo, `tutti`, o la chiave); gzip se accettato;
  - *Board paginata:* senza sprint (`sprint=tutti`, o nessuno sprint attivo, o team a flusso continuo) la risposta ha `paged: true` e `page_size`: `cards` contiene le prime 50 card di ogni colonna nell'ordine della board e `columns[].count` è il totale esatto (anche per i filtri `q` e per le card nascoste in `hidden`). Le successive: `?col=<indice>&after=<id dell'ultima card ricevuta>&limit=` (al massimo 200), solo le card di quella colonna; se la card `after` non è più nella colonna, 422 `errors.validation.board.cursor` (la pagina si ricarica). Con uno sprint la board resta una lettura completa (`BoardLimit`). Implementazione in `internal/modules/work/board_paged.go`: un group by per (tipo, fase) dà i totali, una classifica con `row_number()` per colonna (solo id, senza i join di dettaglio) dà la pagina, e i dettagli (padre, epic, figli) si leggono solo per gli id della pagina. La posizione si trova per id, non per scarto: spostamenti e nuove card tra una pagina e l'altra non producono doppioni. La board di team (`board_team.go`) usa lo stesso `GetBoard`. In `Board.tsx` il pulsante «Mostra altre (N)» sta in fondo alla colonna (nell'intestazione con le swimlane); le pagine caricate si aggiungono alla cache e, con la board paginata, uno spostamento riuscito non la ricarica (i totali delle colonne si aggiornano in locale).
  - `PUT /api/v1/work/spaces/{chiave}/board/settings` con `{columns: [{name, phases, wip}], swimlane, types, done_days, custom}`;
  - `POST /api/v1/work/spaces/{chiave}/board/move` con `{element, column, comment, after, before, lane, swimlane}`: passaggio di fase, posizione e swimlane in una transazione; 422 con il motivo se un gate lo blocca.
- **API degli elementi:** `POST /api/v1/work/spaces/{chiave}/items` con `{type, title, parent, assignee_id, sprint, fields, top}`; `PUT /api/v1/work/elements/{rif}/rank` con `{after, before}`; `PUT …/assignee` con `{member_id, version}`; `PUT …/sprint` con `{sprint, after, before}` (vuoto = backlog).
- **API degli sprint:** `GET /api/v1/work/spaces/{chiave}/backlog?q=`; `POST /api/v1/work/spaces/{chiave}/sprints` con `{title, goal, start, end}`; `PATCH /api/v1/work/sprints/{rif}`; `POST …/start`; `POST …/close` con `{"move_to": "next" | "backlog" | chiave}`; `GET …/burndown`.
- **API delle ore:** `GET` e `POST /api/v1/work/elements/{rif}/time` con `{duration | hours, date, text}` (il costo lo calcola `finance.CostWorkLogs` in una transazione di sistema separata); `DELETE /api/v1/work/time/{id}` (solo le proprie, o `finance.write`).
- **Altro:** `GET /api/v1/work/me` (il mio lavoro), `GET /api/v1/work/elements/{rif}/delivery` (epic dei team e avanzamento per richiesta, impatto ed epic; sprint per gli elementi), `POST /api/v1/work/impacts/{rif}/epic` (crea l'epic se manca).
- **Regole di processo:** azioni `create_epic` (impatto o richiesta), `sprint_start`, `sprint_close` (`ref` = `next` o `backlog`). Fatti: `avanzamento`, `epic`, `epic_aperti` su impatto e richiesta; `assegnatario`, `figli`, `figli_aperti`, `sprint`, `ore_registrate` sugli elementi di lavoro; `inizio`, `fine`, `sprint_attivi`, `elementi`, `elementi_aperti` sullo sprint.
- **Query:** campo `sprint:` (chiave o titolo dello sprint).
- **Etichette (tag) nelle liste:** ricerca (`GET /elements`), backlog, «Il mio lavoro» e card della board portano `tags: [{id, name, color}]` per ogni elemento, con una sola query aggregata per risposta (`engine.TagsOf`, `internal/engine/tags_view.go`; le condivise del tenant più le personali di chi guarda). Campo di query `etichetta:` (anche `etichette`, `tag`, `label`; il nome deve esistere, altrimenti 422 come per gli altri riferimenti), definito in `work.Register`. Interfaccia: `web/src/tags.tsx` (`TagChips`: chip compatti, «+N» oltre 3, 2 nelle card e nelle righe strette), colonna «Etichette» selezionabile nelle tabelle; senza la colonna i chip stanno sotto il titolo. Il clic su un chip aggiunge `etichetta:nome` alla query della lista, del backlog o della board (`withLabel` in `query.tsx`); in «Il mio lavoro», che non ha la query, sono solo testo. Il raggruppamento per fase nelle tabelle e in «Il mio lavoro» usa `useStateName()`.
- **Pacchetti di partenza:** `internal/modules/work/starter/` (`work.Provision`, anche con `regia tenant provision`), con la regola dell'epic aggiunta al processo degli impatti.
- **Pagine:** `/delivery/board?spazio=WEB&sprint=&q=&corsie=assignee`, `/delivery/backlog?spazio=WEB`, `/delivery/mio-lavoro`; nel dettaglio di ogni elemento i blocchi «Epic dei team», «Lavoro» e «Ore lavorate» (`web/src/pages/WorkSections.tsx`); colonna «Spazio di delivery» nella scheda Team.
- **Dati di esempio:** `scripts/dev-demo-work.sh [url]`, richiamato da `dev-demo-data.sh`: spazi WEB e INT, epic dagli impatti del Portale clienti B2B, story, task e bug, Sprint 12 attivo e Sprint 13 pianificato, ore con tariffe personali. La storia degli stati nasce il giorno in cui si caricano i dati: il burndown mostra la linea piatta fino a oggi e il calo di oggi.
- **Tempo e team** (migrazione `0038_tempo_team.sql`, `internal/modules/work/{timesheet,timereport,bulk,teams}.go`, rotte in `internal/api/work_tempo.go`):
  - *Timesheet:* `GET /api/v1/work/timesheet?week=&member=`, `PUT …/timesheet/cells` con `{week, cells: [{element, date, duration}]}` (una cella è la somma delle ore della persona sull'elemento in quel giorno; `0` la svuota), `POST …/copy`, `…/submit`, `…/withdraw`, `…/decide` con `{member, week, approve, comment}`, `…/reopen`, `GET …/pending`. Regole del tenant in `GET/PUT /api/v1/work/time/settings` (`require_approval`, `approver_mode` = `team_lead` | `member` | `admin`, `locked_until`, `max_day_minutes`). Le durate si leggono come «1h30», «1,5», «45m», «1:30», «1g» (`work.ParseMinutes`).
  - *Report:* `GET /api/v1/work/time/report?from=&to=&group=persona,spazio&person=&team=&space=&epic=&request=&element=&format=csv` (dimensioni: `person`, `team`, `space`, `epic`, `request`, `element`, `week`, `month`); `GET …/time/records` con gli stessi filtri porta ai singoli numeri. Chi non ha `finance.read` o `tenant.admin` vede le proprie ore e quelle dei team che guida; i costi arrivano da una seconda transazione con `app.finance`.
  - *Comandi in blocco:* `POST /api/v1/work/bulk` con `{ids, commands, dry_run, all_or_nothing, text}` (comandi `phase`, `assign`, `field`, `labels`, `sprint`, `delete`; applicati nell'ordine assegna, campi, etichette, sprint, fase); ogni elemento è un punto di salvataggio, quelli bloccati restano com'erano con il motivo; `GET …/bulk/last` e `POST …/bulk/undo` per l'ultimo comando; `GET …/bulk/context` per il completamento. La barra e la selezione sono in `web/src/pages/BulkBar.tsx` (`useSelection`, `SelectBox`, `BulkBar`: basta una lista di id) e il linguaggio in `web/src/bulkParse.ts`.
  - *Team:* `GET /api/v1/work/teams`, `PUT …/teams/{id}/members` con `{member_id, lead, role, allocation_pct, weekly_hours | clear_hours}`, `DELETE …/members/{persona}`; assenze in `GET/POST/DELETE /api/v1/work/absences`. La capacità del team (somma di ore per allocazione) si riflette in `teams.weekly_capacity_hours`, che il carico dei team già legge, meno le assenze per settimana (`planning/absences.go`).
  - *Pagine:* `/tempo` (timesheet, `?settimana=&persona=`), `/tempo/report`, `/tempo/team`; la selezione multipla è su board, backlog e «Il mio lavoro».
  - *Dati di esempio:* `scripts/dev-demo-tempo.sh [url]`, richiamato da `dev-demo-data.sh`.
  - *Approvazione nel motore e notifiche* (migrazione `0074`, `timesheet_approval.go`, vedi `timesheet-approvazioni.md`): la settimana inviata apre una richiesta con soggetto `timesheet` (senza elemento), compare in «Le mie approvazioni» e si decide da lì (`POST /approvals/steps/{id}/decision`) o da `…/decide` con lo stesso effetto; kind di notifica `timesheet` (invio raggruppato, esito con motivo, riapertura).

## Import da Jira, YouTrack e CSV (piano, passo 18)

Procedura in Configurazione → Import (solo amministratori); cosa si porta e come esportare in `docs/import.md`. Migrazione `0031_import.sql`, modulo `internal/modules/importer`, rotte in `internal/api/imports.go`.

- Caricamento: `POST /api/v1/imports/upload` (multipart: `source` = `jira_csv` | `youtrack_csv` | `csv`, `delimiter` facoltativo, poi `file`; fino a 512 MB e 200.000 righe) oppure `POST /api/v1/imports/connect` con `{source: jira_api|youtrack_api, base_url, deployment, email, token, project, query, limit}` (sola lettura; il token resta in memoria).
- `GET /imports` (ultime esecuzioni), `GET /imports/catalog` (spazi, campi, membri, tipi di relazione), `GET /imports/{id}` (analisi, mappatura, avanzamento, report).
- Mappatura: `PUT /imports/{id}/mapping`, `GET /imports/{id}/suggest`; profili `GET/POST /import-profiles`, `PUT/DELETE /import-profiles/{id}`, `POST /imports/{id}/profile/{pid}`.
- `POST /imports/{id}/preview` (prova senza salvare, errori riga per riga), `POST /imports/{id}/start` (202, in background a lotti), `POST /imports/{id}/rollback`. Un solo import `running` per tenant (indice parziale).
- I dati letti stanno compressi in `import_datasets`; la chiave d'origine in `import_origins` e nel campo «origine»: un secondo import aggiorna invece di duplicare. La storia degli stati non si porta.
- Test: `go test ./internal/itest -run TestImport -v` (fixture anonime in `internal/itest/testdata/import`; `TestImportVolume` stampa il tempo di 5.000 righe, `REGIA_IMPORT_VOLUME=50000` per il carico pieno).

## Viste salvate, filtri visuali e liste uniformi (piano, passo 16)

- **Codice:** `internal/modules/views` (servizio) e `internal/api/views.go` (rotte `/api/v1/views`); migrazione `0039_viste.sql`; frontend in `web/src/views/` (`ElementList`, `ElementTable`, `FilterChips`, `ViewsBar`, `FavoriteViews`, `filters.ts`, `lists.ts`).
- **Vista** (`views`, solo quelle con `page`; le viste di sistema della board restano fuori): nome, modalità (`list`, `table`), query, impostazioni (`cols`, `widths`, `group`). Condivisione: `private`, `group` (un gruppo di cui si fa parte) o `tenant` (solo `tenant.admin`). Può cambiarla chi l'ha creata, o un amministratore se è condivisa; le viste personali degli altri non si vedono mai, nemmeno all'amministratore. Preferiti (`view_favorites`) e predefinita per pagina (`view_defaults`, per utente e per tenant; quella del tenant solo se la vista è condivisa con tutti).
- **Preferiti nel menu:** `GET /views/favorites` conta i risultati con lo stesso motore di ricerca (tetto 99, "99+"); una query diventata non valida dà "—" senza rompere il menu. Il contatore si aggiorna con gli eventi in tempo reale (`live.ts` invalida le query).
- **Indirizzo:** `?q=` query, `?v=` vista salvata, `?g=` raggruppamento (`nessuno` = nessuno), `?vista=` lista o tabella. "Copia il link" mette sempre anche la query, quindi funziona pure per chi non vede la vista.
- **Chip = query:** `views/filters.ts` scompone la query in clausole (chip), ordinamento e testo libero e la ricompone senza perdere nulla; il pannello di un chip usa le proposte di `/query/suggest` (le stesse dell'autocompletamento, con il contesto del tipo). Test: `web/test/filters.test.ts`.
- **Liste:** `views/lists.ts` elenca le pagine che usano lo stesso componente: richieste, impatti, applicazioni, asset, rischi, incidenti, controlli, documenti (rotta `/elenco/$lista`). Per aggiungerne una basta una voce lì. La tabella (`ElementTable`) accetta `selection` (facoltativa) per la selezione multipla: le azioni in blocco le gestisce chi la passa.
- **Raggruppamento:** lato client sulla pagina caricata (con un campo scelto si caricano tutte le pagine, fino a 1000 elementi), per fase, tipo, assegnatario, segnalatore, spazio e campi personalizzati a scelta, booleani o persona; sezioni comprimibili con il conteggio.
- **Dati di esempio:** `scripts/dev-demo-viste.sh [url]`, richiamato da `dev-demo-data.sh`.

## «Da fare» e quasi vittorie dell'area lavoro (ondata 2; migrazione 0054)
- **Pagina** `/da-fare` (`web/src/pages/DaFare.tsx`, client `web/src/dafareApi.ts`, testi nell'area `dafare`). Prima voce del menu «Personale»; il campanello conta solo ciò che chiede un'azione e porta qui. Le vecchie code (`/approvazioni`, `/valutazioni`, `/requisiti`) restano raggiungibili con una striscia che rimanda a «Da fare» (`pages/TodoBanner.tsx`).
- **API** (`internal/api/dafare.go`). `GET /api/v1/todo` raccoglie, dalle fonti già usate altrove, le sezioni: decisioni (inbox delle approvazioni con tipo, organo, importo, punteggio, posizione in graduatoria, budget e linea di taglio da `rankingScoped`, valutazioni obbligatorie aperte, requisiti da accettare), valutazioni (`reviews.InboxN`), verifiche (`requirements.InboxN`), impatti del team (referente sull'impatto, del team o membro; solo nelle fasi «Da analizzare», «In analisi», «Stimato», con i passaggi disponibili e la capacità libera da `planning.Load`), tempo (settimane da inviare, dal venerdì per la corrente, e da approvare), status report in scadenza, menzioni non lette, prossima seduta degli organi della persona. `GET /api/v1/todo/count` dà solo i numeri (menu e campanello). Le azioni usano le API esistenti: decisione del passo (`POST /approvals/steps/{id}/decision`), passaggi di fase, `PATCH /impacts/{id}` per la stima, timesheet.
- **Decisione in linea** solo se `inline_decision` dell'organo (migrazione 0044) è vero; altrimenti «Apri il pacchetto». «Approva» ha la nota facoltativa; «Con condizioni», «Rinvia» e «Respingi» chiedono il testo e si inviano con Invio.
- **Oggi** (seconda fase): lavoro assegnato per scadenza con timer per elemento (in `localStorage`, `regia.timer.<membro>`; «Ferma» registra le ore con `POST /work/elements/{ref}/time`), totale del giorno dal timesheet e «Invia timesheet».
- **Attività** (`/notifiche`, `pages/Notifications.tsx`): gli avvisi sono raggruppati per elemento e per giorno («3 passaggi, ultimo: Accettato»). «Silenzia» (`PUT/DELETE /api/v1/me/silenced/{ref}`, `GET /api/v1/me/silenced`, tabella `notification_silences`) tace gli avvisi informativi (`notify.Informational`: commenti, ruoli, fasi) di un elemento; le notifiche che chiedono un'azione non si silenziano.
- **Richieste.** `PUT /work/elements/{ref}/assignee` e i comandi in blocco (`assign`, `phase`, `field`, `labels`) valgono anche per le richieste; `GET /work/bulk/context?type=business_request` dà le fasi e i campi delle richieste (nessuno sprint, nessuna eliminazione). Lista richieste: colonna «Responsabile» con «Prendi in carico», selezione multipla con la barra dei comandi.
- **Portale.** `GET /api/v1/portal` porta `my_unit` (da `org_unit_members`) e non elenca la radice dell'albero se ha unità sotto; `POST /portal/drafts` senza id riprende la bozza esistente dello stesso tipo (una per persona e tipo); `GET /api/v1/portal/similar?q=` cerca le richieste aperte e leggibili con parole in comune col titolo.
- **Backlog.** Creazione rapida in fondo a ogni sprint (titolo e Invio) e selettore dello sprint nel dialogo «Nuovo elemento».
- **Test.** `internal/itest/ondata2_dafare_test.go`, `internal/itest/ondata2_lavoro_test.go`, `web/e2e/ondata2-dafare.spec.ts`.
