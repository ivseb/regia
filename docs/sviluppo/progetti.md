# Progetti: piano del PM, modi di consegna, Panoramica, baseline

Passi 1, 2 e 3 di `replica/progetti.md`. La richiesta dice perché e quanto, il progetto come, quando e chi, l'epic cosa fa un team. Le due viste si toccano nel work package.

- Backend: `internal/modules/projects` (modulo `projects`), motore del Gantt in `internal/modules/planning/graph.go`, epic dei work package in `internal/modules/work/epic.go`.
- API: `internal/api/projects.go`.
- Migrazioni: `migrations/0077_progetti.sql`, `migrations/0078_progetti_baseline.sql` (passo 3).
- Interfaccia: `web/src/projectsApi.ts`, `web/src/pages/progetti/`, schede in `web/src/pages/ElementPage.tsx`.
- Testi: area `progetti` (`progetti.ts` schede, `progetti-pm.ts` Panoramica, tabelle ed elenco, `progetti-piano.ts` Gantt, `progetti-voci.ts` scheda della voce e impostazioni, `progetti-baseline.ts` baseline, tolleranze e change request) ed `errors-progetti.ts`.
- Test: `internal/itest/progetti_test.go`, `internal/itest/progetti_baseline_test.go`, `internal/modules/projects/baseline_test.go`, `internal/modules/planning/graph_test.go`, `web/e2e/progetti*.spec.ts`.

## Dati

| tabella | cosa |
| --- | --- |
| `ext_project` | modello (`agile`, `waterfall`, `mixed`, `contract`), programma, tolleranze del progetto (facoltative: `null` = quelle del tenant) |
| `project_requests` | richieste confluite: una richiesta sta in un solo progetto, un progetto ne raccoglie più d'una |
| `plan_phases` | fasi del piano con `gate_enabled`, `gate_status` (`open`, `pending`, `approved`, `rejected`) e la richiesta di approvazione |
| `ext_work_package` | progetto, fase, `delivery_mode` (`agile`, `waterfall`, `supplier`), applicazione, team, fornitore (`ict_provider`), stima, peso manuale, impatto d'origine, `supplier_updates` (passo 4) |
| `ext_plan_activity` | attività di un work package a cascata, con stima e percentuale corrente |
| `ext_milestone` | c'era dallo schema di partenza senza uso: aggiunti progetto, fase, work package, flag contrattuale; `actual_date` è il giorno in cui è raggiunta, `baseline_date` è la data nella baseline in vigore |
| `ext_plan_progress` | avanzamento dichiarato storicizzato (voce, giorno, percentuale, nota, chi) |
| `project_settings`, `project_roles` | impostazioni del tenant (con le tolleranze `tolerance_days`, `tolerance_budget_pct`) e ruoli del progetto sopra `stakeholder_roles` |
| `plan_baselines`, `plan_baseline_items` | baseline immutabili: numero per progetto, inviluppo, budget aggregato, chi e quando, change request d'origine; voci con chiave, titolo, fase, date e stima |
| `ext_change_request` | change request: `status` (`draft`, `pending`, `approved`, `rejected`, `withdrawn`), `origin` (`tolerance`, `manual`), motivo, baseline di partenza e nuova, piano proposto (`proposal`), scostamenti (`deviations`, senza titoli), motivo del blocco |

- **Date:** stanno in `elements.start_at` / `due_at`; la milestone ha inizio e fine uguali.
- **Dipendenze:** sono relazioni `depends_on` con `attrs` `{"kind": "fs"|"ss"|"ff", "lag": n}`, con il ritardo in giorni lavorativi.
- **Gerarchia:** il work package è figlio del progetto, l'attività del work package. La milestone è figlia del suo work package, se ce l'ha, altrimenti del progetto.

## Tipi e provisioning

`projects.Provision` (dopo `work.Provision`) crea:
- lo spazio `PRJ` con i tipi `program`, `project`, `work_package`, `plan_activity`, `milestone`, `deliverable` e `change_request`. `deliverable` è solo un tipo, senza processo;
- i processi di partenza (`starter/*.json`);
- i ruoli per oggetto mancanti (`operations_manager` è nuovo);
- i ruoli del progetto di default: business owner, PM e sponsor obbligatori ed ereditati; PMO facoltativo ed ereditato; responsabile operations facoltativo;
- le politiche di approvazione «Cancello di fase» e «Change request» del tipo `project`, che fanno decidere lo sponsor.

Il modulo si accende e si spegne per tenant (`projects`, prefissi API `projects`, `plan-items`, `plan-phases`, `plan-baselines`, `change-requests`). Il modello «Persona sola / home lab» lo accende.

**Tenant esistenti:** `regia tenant provision --slug X` crea i tipi e porta nei progetti le richieste già approvate (migrazione dei dati del passo 1). Ogni impatto diventa un work package agile con applicazione, team, stima, date, dipendenze fine→inizio e uno stato che segue quello dell'impatto. L'impatto resta in `ext_impact` e il work package lo cita (`impact_id`), quindi nessun dato si perde. Una richiesta già in un progetto non si tocca.

## Nascita del progetto

- **Dalla richiesta:** la richiesta approvata diventa direttamente il progetto. Lo fa il gancio sui passaggi (`afterTransition`), se `project_settings.auto_from_request` è acceso, che è il predefinito.
  - «Approvata» vuol dire un'approvazione dello stage-gate concessa, oppure una fase del percorso principale uguale o successiva all'esito `approve` della politica (predefinito «Pianificazione»).
  - Passano titolo, descrizione, finestra, i ruoli con `inherit_from_request`, le fasi del modello predefinito e gli impatti come work package, nella fase «di realizzazione» del modello.
  - `POST /api/v1/demand/requests/{ref}/project` fa lo stesso a mano, ed è idempotente. `POST /api/v1/projects/{ref}/requests` fa confluire un'altra richiesta approvata in un progetto esistente.
- **Da zero:** `POST /api/v1/projects {title, template, start?, end?, no_phases?}`. Chi lo crea ne è il PM.

| modello | fasi di partenza (cancello) |
| --- | --- |
| agile | Realizzazione, Rilascio |
| misto (predefinito) | Analisi, Realizzazione, Collaudo, Rilascio |
| a cascata | Analisi, Disegno, Sviluppo, Test, Collaudo (tutte con cancello), Rilascio |
| commessa | Gara, Esecuzione, Accettazione |

## Piano e Gantt

- `planning.Task` ha `Deps []Dep` (tipizzate) e `Milestone`. Senza `Deps` valgono le `DependsOn` degli impatti come fine→inizio: il piano della richiesta non cambia.
- `planning.Scheduler` (`NewScheduler`, `Move`, `Auto`, `Violated`, `CheckCycle`) è il motore comune.
- Regole del successore rispetto al predecessore, in giorni lavorativi e solo in avanti:

| tipo | regola |
| --- | --- |
| fs | inizio ≥ fine + 1 + ritardo; una milestone può cadere il giorno stesso della fine |
| ss | inizio ≥ inizio + ritardo |
| ff | fine ≥ fine + ritardo, con la durata mantenuta |

- Le voci annullate non si muovono.
- Il Gantt del frontend rispecchia le stesse regole nell'anteprima del trascinamento.

| API | cosa |
| --- | --- |
| `GET /projects/{ref}/plan` | fasi (con inviluppo e avanzamento) e voci |
| `POST /projects/{ref}/items` | nuova voce: `kind`, `phase_id`, `parent_id` per le attività, `delivery_mode`, team, applicazione, fornitore, stima, date, `depends_on` |
| `PATCH /plan-items/{id}`, `DELETE /plan-items/{id}` | modifica e cancellazione logica (con le attività) |
| `PUT /plan-items/{id}/schedule` | spostamento con cascata |
| `PUT /plan-items/{id}/dependencies` | sostituisce le dipendenze; niente cicli né voci di altri progetti |
| `POST /projects/{ref}/plan/auto` | date alle voci che non ne hanno |
| `POST /projects/{ref}/phases`, `PATCH`/`DELETE /plan-phases/{id}` | fasi |

**Permessi:**
- modifica il piano chi ha `element.write` o il ruolo `project_manager` o `pmo` sul progetto;
- avanzamento e date della singola voce li aggiorna anche il suo responsabile: l'assegnatario, o il `team_lead` della voce o del work package.

## Modi di consegna

- **Agile:** il processo del work package ha la regola «All'accettazione nasce l'epic» (`enter:Accettato`, quando `modo_consegna: agile`, azione `create_epic`).
  - `create_epic` vale anche sul `work_package`: crea l'epic nello spazio del team (lo spazio si crea se manca), figlio del work package, con l'assegnazione al team.
  - Un work package nato da un impatto usa l'epic dell'impatto.
  - A mano: `POST /plan-items/{id}/epic`. L'avanzamento sono le story chiuse degli epic (`work.ProgressOf`, `work.EpicsOfWorkPackage`).
- **A cascata:** attività con avanzamento dichiarato, `POST /plan-items/{id}/progress {percent, note, date?}`, storicizzato in `ext_plan_progress`. Lo storico si legge con `GET`.
  - L'avanzamento del work package è la media delle attività pesata sulle stime.
  - Le ore si registrano sull'attività (e sul work package) dal blocco «Ore lavorate» della colonna laterale (`WorkSections.tsx`), in `work_logs`. Registra l'assegnatario o chi ha `element.write`: lo spazio PRJ non ha un team.
- **Fornitore:** per ora solo `delivery_mode = supplier`, il fornitore del catalogo (`supplier_id`, tipo `ict_provider`) e l'avanzamento dichiarato dal PM. SAL, deliverable e accesso esterno sono il passo 4 (`supplier_updates` è già nel dato).

**Import dal timesheet aziendale:** i profili CSV dei costi (`import_profiles`, target `cost_entries`) riconoscono l'elemento per chiave, quindi anche un'attività (`PRJ-12`); lo verifica il test.
- Le ore come `work_logs` non hanno ancora un target di import: il vincolo di `import_profiles.target` prevede `work_logs`, ma `finance/importcsv.go` gestisce solo `cost_entries`.
- L'import di progetti (`importer`) scrive ore solo sugli elementi che crea, epic, story, task e bug in uno spazio di team.
- Il passo minimo è un target `work_logs` nei profili CSV, con la colonna «elemento» per chiave, come per i costi.

## Numeri del progetto

`projects.Numbers` (e `computeNumbers` sul piano già caricato) è l'unica funzione dei numeri. La usano la Panoramica (`GET /projects/{ref}/overview`), l'elenco (`GET /projects`) e i fatti del tipo `project` (`avanzamento`, `work_package_aperti`, `milestone_in_ritardo`).

- **Avanzamento:** media dei work package non annullati, pesata sul peso manuale o, senza, sulla stima in giorni. Un work package senza stima pesa la media delle stime degli altri. Con `progress_method = equal` pesano tutti uguale. Una voce chiusa vale 100.
- **Date:** previsione (inviluppo), baseline in vigore, effettive (primo avvio, ultima chiusura).
- **Baseline:** scostamento della fine in giorni lavorativi (`variance_days`) e in percentuale della durata di baseline (`variance_pct`); milestone oltre la tolleranza (`over_tolerance`), fine oltre la tolleranza (`end_over_tolerance`); scostamento del costo (forecast, altrimenti approvato) rispetto alla baseline (`budget_variance_pct`), solo a chi vede il budget.
- **Milestone e ritardi:** prossima milestone; work package e milestone in ritardo, in giorni lavorativi.
- **Dipendenze bloccate:** `waiting` se la voce doveva partire e il predecessore non è chiuso, `violated` se le date non rispettano la dipendenza.
- **Altro:** change request da inviare o in approvazione (figlie del progetto), rischi collegati al progetto o alle sue richieste, ruoli obbligatori mancanti, margine sulla scadenza normativa (o desiderata) delle richieste.
- **Budget:** quello delle richieste confluite (`finance.RequestTotals`, solo aggregati), finché il passo 5 non lo sposta sul progetto.
- **Semaforo:** rosso con una milestone in ritardo, la previsione oltre la scadenza o uno scostamento oltre le tolleranze (date o budget); giallo con work package in ritardo, dipendenze bloccate o ruoli mancanti.

## Cancelli di fase

- Il cancello si accende per fase (`PATCH /plan-phases/{id} {gate_enabled}`); è acceso di default nel modello a cascata.
- Con un cancello acceso e non approvato, le voci delle fasi successive non passano a una fase `in_progress` o `done`: errore `validation.projects.gate_closed`, dal gancio sui passaggi.
- `POST /plan-phases/{id}/gate` apre l'approvazione (soggetto `phase_gate`, politica «Cancello di fase»). L'esito torna con `OnClose`: approvato apre le fasi successive, respinto le blocca, rimandato torna da chiedere.

## Baseline e change request (passo 3)

- **Baseline:** `POST /projects/{ref}/baselines {note}` fissa la prima (chi pianifica il progetto); con una baseline già presente risponde `validation.projects.baseline_exists`. Le successive nascono solo da una change request approvata. `GET /projects/{ref}/baselines` le elenca, `GET /plan-baselines/{id}` ne dà le voci.
  - Immutabili: un trigger (`plan_baseline_immutable`) rifiuta `update` e `delete` diretti; passano solo quelli a cascata da un vincolo (cancellazione del tenant o degli elementi, copia dell'ambiente di prova).
  - Il piano (`GET /projects/{ref}/plan`) porta `baseline` (in vigore), `tolerance` e, per voce, `in_baseline`, `baseline_start`, `baseline_end`, `variance_days`. Il Gantt le disegna in trasparenza sotto le barre.
- **Tolleranze:** del tenant in `project_settings` (Configurazione → Progetti; predefinite +10 giorni lavorativi, +5% sul budget), sovrascrivibili per progetto in `ext_project` (per ora senza interfaccia).
- **Change request automatica:** dopo ogni spostamento salvato (`saveChanges`: trascinamento, cascata, pianificazione automatica), se c'è una baseline, nessuna change request aperta e una milestone non chiusa o la fine del progetto sono oltre la tolleranza in giorni, nasce un elemento `change_request` figlio del progetto con il piano proposto (la fotografia del piano) e gli scostamenti. Il budget oltre la tolleranza fa solo diventare rosso il semaforo: la change request la apre il PM.
- **A mano:** `POST /projects/{ref}/change-requests {title?, reason}`; una sola aperta per progetto (`validation.projects.cr_open`).
- **Approvazione:** come il cancello di fase, nel motore delle approvazioni (soggetto `change_request` sul progetto, politica «Change request»). Se non parte (nessuna politica, manca lo sponsor), la change request resta «Da inviare» con codice e messaggio in `blocked`, e lo spostamento va comunque a buon fine. `POST /change-requests/{id}/submit {reason?}` la invia, `POST /change-requests/{id}/withdraw` la ritira.
  - Esito con `OnClose`: approvata, il piano proposto diventa la baseline successiva (e si ricontrolla il piano attuale, che può essersi mosso intanto); respinta, la baseline resta; rimandata, torna da inviare.
  - Le fasi del processo di partenza (`change_request_standard.json`: Da inviare, In approvazione, Approvata, Respinta, Ritirata) le muove il modulo; a mano si può solo ritirare.
- **Visibilità:** baseline e change request seguono quella del singolo elemento come il piano: le voci che l'attore non legge arrivano senza titolo, le change request che non legge non compaiono, il budget della baseline compare solo a chi vede quello del progetto.

## Punti di aggancio per i passi 4-5

- **Fornitori:** `supplier_updates` sul work package; SAL e deliverable (tipo `deliverable`) arriveranno al passo 4.
- **Conflitti tra progetti:** `ext_work_package.application_id` è indicizzato per il carico del componente.
- **Budget e status report:** restano sulla richiesta. `BudgetFunc` del servizio è il punto da spostare sul progetto.
- **Roadmap e «In arrivo dai progetti»:** non ancora; i work package proposti sono quelli nella fase «Proposto».

## Nota sulla visibilità

Il piano, la baseline e le change request applicano i limiti del singolo elemento (`visibility.go`): una voce non leggibile resta con date, avanzamento e dipendenze ma senza titolo, persone e collegamenti. I ruoli del §5 (fornitore esterno) arrivano al passo 4.
