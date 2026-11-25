# Procedura guidata «Coinvolgi un team»

Ondata «semplicità» (specifica: `replica/prossimi-passi.md`). Nessuna area aziendale è cablata: ogni azienda dice quale team coinvolgere, quando e cosa fa. La procedura scrive da sola ciò che altrimenti richiederebbe cinque pagine di configurazione.

**Ingressi.** Pulsante «Coinvolgi un team» nella pagina Team (`TeamsPage.tsx`) e nella configurazione delle valutazioni (`ReviewConfig.tsx`); pagina `/configurazione/coinvolgi` (`web/src/pages/Coinvolgi.tsx`, chiamate in `coinvolgiApi.ts`).

**Quattro schermate** (in colonna anche a 375 px)
1. Quale team: uno esistente (selettore) o uno nuovo, che si crea con il suo spazio di delivery come dalla pagina dei team.
2. Quando: il processo (solo quelli di un tipo di richiesta), la fase d'ingresso (o «appena la richiesta viene creata») e, facoltativa, la condizione, con il costruttore di sempre (`ConditionBuilder`).
3. Cosa fa: **parere** (con «obbligatorio» sì/no), **lavoro**, o tutti e due.
4. Conferma: anteprima in frasi semplici; alla conferma si scrive e compaiono i link «Vedi la regola» e «Modifica il questionario».

**API** (`internal/api/coinvolgi.go`, solo `tenant.admin`)
- `POST /api/v1/coinvolgi/anteprima` e `POST /api/v1/coinvolgi`, stesso corpo: `team_id` oppure `new_team`, `process`, `phase` (vuoto = alla creazione), `when`, `review`, `work`, `mandatory`.
- L'anteprima esegue **lo stesso codice** della conferma dentro una transazione che poi annulla: non può divergere da ciò che si scrive. Risponde con `team`, `review` (chiave e nome del tipo, modello, team valutatore nuovo o no), `work`, `rule`, `version`, `warnings` (`group_empty`: il gruppo del team valutatore è nuovo e vuoto) e `applied`.
- Alla conferma, in una sola transazione: eventuale team nuovo; per il parere il team valutatore (si riusa quello con lo stesso nome, altrimenti si crea con un gruppo omonimo, vuoto), un modello di questionario minimo (due domande: «accettabile?» e note) pubblicato, il tipo di valutazione (ambito richiesta); poi la regola `coinvolgi_<team>` aggiunta alla definizione pubblicata, salvata come bozza e pubblicata come nuova versione; infine l'audit `coinvolgi.apply`. Si usano le funzioni di sempre (`SaveDraft`/`Publish` del motore, `CreateTeam`/`CreateType`/`SaveTemplateDraft`/`PublishTemplate` delle valutazioni).
- Rifiuti: niente da fare (né parere né lavoro), processo che non è di una richiesta, fase inesistente, condizione non valida, **bozza aperta nel processo** (non si pubblica il lavoro di qualcun altro). Le richieste già avviate restano sulla loro versione.

**Nuova azione delle regole: `create_impact`** (modulo architettura, `impact_action.go`). Sulla richiesta apre un impatto per il team indicato (`ref` = nome del team) sulle applicazioni che la richiesta tocca (quelle che compongono o usano gli asset logici citati) e che non hanno già un impatto: prima quelle di cui il team è proprietario, se non ce ne sono tutte. Un impatto esiste sempre su una applicazione (`ext_impact.application_id` è obbligatorio, nessuna migrazione): se la richiesta non tocca applicazioni l'azione lo scrive nella storia e non crea niente. L'impatto accettato dal team genera l'epic con la regola `create_epic` del modulo del lavoro, e l'avanzamento risale alla richiesta. Il controllo di pubblicazione verifica che il team esista. Nell'editor delle regole compare come «Crea l'impatto sul team».

**Testi**: `configurazione-coinvolgi.ts` e `errors-coinvolgi.ts` (it/en); testi del server (questionario, nome della regola) in `internal/api/coinvolgi.go`, nella lingua di chi conferma.

**Test**: `internal/itest/coinvolgi_test.go` (anteprima che non scrive, conferma, regola che scatta su una richiesta nuova con parere e impatto, richiesta già avviata intatta, bozza che blocca, solo amministratore) e `web/e2e/semplicita-coinvolgi.spec.ts`.
