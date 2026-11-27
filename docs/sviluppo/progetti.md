# Progetti: piano del PM, modi di consegna, Panoramica, baseline, fornitori, programmi, budget e status report

Passi 1, 2, 3, 4 e 5 di `replica/progetti.md`. La richiesta dice perché e quanto, il progetto come, quando e chi, l'epic cosa fa un team. Le due viste si toccano nel work package.

- Backend: `internal/modules/projects` (modulo `projects`), motore del Gantt in `internal/modules/planning/graph.go`, epic dei work package in `internal/modules/work/epic.go`.
- API: `internal/api/projects.go`.
- Migrazioni: `migrations/0077_progetti.sql`, `migrations/0078_progetti_baseline.sql` (passo 3), `migrations/0079_progetti_fornitori.sql` (passo 4). Il passo 5 non ha migrazioni: usa `budgets`, `status_reports` ed `ext_project` come sono.
- Interfaccia: `web/src/projectsApi.ts`, `web/src/pages/progetti/` (passo 4: `SupplierPanel.tsx`, `ProgramOverview.tsx`), schede in `web/src/pages/ElementPage.tsx`.
- Testi: area `progetti` (`progetti.ts` schede, `progetti-pm.ts` Panoramica, tabelle ed elenco, `progetti-piano.ts` Gantt, `progetti-voci.ts` scheda della voce e impostazioni, `progetti-baseline.ts` baseline, tolleranze e change request, `progetti-fornitori.ts` SAL, deliverable e conflitti, `progetti-programmi.ts` programmi, `lavoro-progetti.ts` notifiche) ed `errors-progetti.ts`.
- Passo 5: `internal/modules/projects/incoming.go` («In arrivo dai progetti»), `tolerance.go` (tolleranze del progetto), `internal/modules/report/project.go` (status report del progetto), `finance.ProjectTotals`; interfaccia `web/src/pages/progetti/IncomingWork.tsx`, Panoramica (`BudgetBlock`, `ReportsBlock`), `StatusReportsTab kind="project"`, testi in `progetti-passo5.ts`.
- Test: `internal/itest/progetti_passo5_test.go` (passo 5), `web/e2e/progetti-passo5.spec.ts`, `internal/itest/progetti_test.go`, `internal/itest/progetti_baseline_test.go`, `internal/itest/progetti_fornitori_test.go`, `internal/modules/projects/baseline_test.go`, `internal/modules/projects/supplier_test.go`, `internal/modules/planning/graph_test.go`, `web/e2e/progetti*.spec.ts` (passo 4: `progetti-fornitori.spec.ts`).

## Dati

| tabella | cosa |
| --- | --- |
| `ext_project` | modello (`agile`, `waterfall`, `mixed`, `contract`), programma, tolleranze del progetto (facoltative: `null` = quelle del tenant) |
| `project_requests` | richieste confluite: una richiesta sta in un solo progetto, un progetto ne raccoglie più d'una |
| `plan_phases` | fasi del piano con `gate_enabled`, `gate_status` (`open`, `pending`, `approved`, `rejected`) e la richiesta di approvazione |
| `ext_work_package` | progetto, fase, `delivery_mode` (`agile`, `waterfall`, `supplier`), applicazione, team, fornitore (`ict_provider`), stima, peso manuale, impatto d'origine; passo 4: `supplier_updates` (il contatto del fornitore dichiara da sé), contratto (`contract_ref`, `contract_amount_cents`, `contract_currency`) |
| `ext_plan_activity` | attività di un work package a cascata, con stima e percentuale corrente |
| `ext_milestone` | c'era dallo schema di partenza senza uso: aggiunti progetto, fase, work package, flag contrattuale; `actual_date` è il giorno in cui è raggiunta, `baseline_date` è la data nella baseline in vigore |
| `ext_plan_progress` | avanzamento dichiarato storicizzato (voce, giorno, percentuale, nota, chi) |
| `project_settings`, `project_roles` | impostazioni del tenant (con le tolleranze `tolerance_days`, `tolerance_budget_pct`) e ruoli del progetto sopra `stakeholder_roles` |
| `plan_baselines`, `plan_baseline_items` | baseline immutabili: numero per progetto, inviluppo, budget aggregato, chi e quando, change request d'origine; voci con chiave, titolo, fase, date e stima |
| `ext_progress_claim` | SAL (tipo `progress_claim`, figlio del work package): numero, data di riferimento, percentuale complessiva e/o importo del periodo, `status` (`submitted`, `accepted`, `rejected`, `withdrawn`), chi dichiara, chi decide e quando, nota della decisione |
| `ext_deliverable` | deliverable (tipo `deliverable`, figlio del work package): scadenza in `elements.due_at`, milestone facoltativa, `status` (`pending`, `delivered`, `accepted`, `rejected`, `cancelled`), consegna e decisione |
| `plan_conflicts` | conflitti già segnalati al team owner: una riga per coppia di work package (a < b) sulla stessa applicazione; sparisce quando il conflitto si risolve |
| `ext_change_request` | change request: `status` (`draft`, `pending`, `approved`, `rejected`, `withdrawn`), `origin` (`tolerance`, `manual`), motivo, baseline di partenza e nuova, piano proposto (`proposal`), scostamenti (`deviations`, senza titoli), motivo del blocco |

- **Date:** stanno in `elements.start_at` / `due_at`; la milestone ha inizio e fine uguali.
- **Dipendenze:** sono relazioni `depends_on` con `attrs` `{"kind": "fs"|"ss"|"ff", "lag": n}`, con il ritardo in giorni lavorativi.
- **Gerarchia:** il work package è figlio del progetto, l'attività del work package. La milestone è figlia del suo work package, se ce l'ha, altrimenti del progetto.

## Tipi e provisioning

`projects.Provision` (dopo `work.Provision`) crea:
- lo spazio `PRJ` con i tipi `program`, `project`, `work_package`, `plan_activity`, `milestone`, `deliverable`, `change_request` e `progress_claim` («SAL»). «Work package» e «SAL» sono i nomi di partenza, rinominabili in Configurazione → Tipi; la `0079` dà a tutti i tipi dei progetti il nome inglese (`system_type_label_en`);
- i processi di partenza (`starter/*.json`);
- i ruoli per oggetto mancanti (`operations_manager`; passo 4: `supplier_contact` «Contatto del fornitore», `program_manager`);
- i ruoli del progetto di default: business owner, PM e sponsor obbligatori ed ereditati; PMO facoltativo ed ereditato; responsabile operations facoltativo;
- le politiche di approvazione «Cancello di fase» e «Change request» del tipo `project`, che fanno decidere lo sponsor.

Il modulo si accende e si spegne per tenant (`projects`, prefissi API `projects`, `plan-items`, `plan-phases`, `plan-baselines`, `change-requests`, `progress-claims`, `deliverables`, `programs`). Il modello «Persona sola / home lab» lo accende.

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
- **Fornitore (passo 4):** `delivery_mode = supplier`, il fornitore del catalogo (`supplier_id`, tipo `ict_provider`), contratto, contatto del fornitore, SAL e deliverable: vedi «Fornitori» sotto.

**Import dal timesheet aziendale:** i profili CSV dei costi (`import_profiles`, target `cost_entries`) riconoscono l'elemento per chiave, quindi anche un'attività (`PRJ-12`); lo verifica il test.
- Le ore come `work_logs` non hanno ancora un target di import: il vincolo di `import_profiles.target` prevede `work_logs`, ma `finance/importcsv.go` gestisce solo `cost_entries`.
- L'import di progetti (`importer`) scrive ore solo sugli elementi che crea, epic, story, task e bug in uno spazio di team.
- Il passo minimo è un target `work_logs` nei profili CSV, con la colonna «elemento» per chiave, come per i costi.

## Numeri del progetto

`projects.Numbers` (e `computeNumbers` sul piano già caricato) è l'unica funzione dei numeri. La usano la Panoramica (`GET /projects/{ref}/overview`), l'elenco (`GET /projects`) e i fatti del tipo `project` (`avanzamento`, `work_package_aperti`, `milestone_in_ritardo`).

- **Avanzamento:** work package del fornitore: ultimo SAL accettato (percentuale complessiva, oppure importi accettati fin lì sul valore del contratto, al massimo 100), poi i deliverable accettati sul totale, poi la percentuale dichiarata (`progress_source` `claims` | `deliverables` | `declared`). Progetto: media dei work package non annullati, pesata sul peso manuale o, senza, sulla stima in giorni. Un work package senza stima pesa la media delle stime degli altri. Con `progress_method = equal` pesano tutti uguale. Una voce chiusa vale 100.
- **Date:** previsione (inviluppo), baseline in vigore, effettive (primo avvio, ultima chiusura).
- **Baseline:** scostamento della fine in giorni lavorativi (`variance_days`) e in percentuale della durata di baseline (`variance_pct`); milestone oltre la tolleranza (`over_tolerance`), fine oltre la tolleranza (`end_over_tolerance`); scostamento del costo (forecast, altrimenti approvato) rispetto alla baseline (`budget_variance_pct`), solo a chi vede il budget.
- **Milestone e ritardi:** prossima milestone; work package e milestone in ritardo, in giorni lavorativi.
- **Dipendenze bloccate:** `waiting` se la voce doveva partire e il predecessore non è chiuso, `violated` se le date non rispettano la dipendenza.
- **Fornitori e componenti condivisi:** SAL da accettare (`claims_pending`), conflitti con altri progetti (`conflicts`, con i limiti di visibilità del piano).
- **Altro:** change request da inviare o in approvazione (figlie del progetto), rischi collegati al progetto o alle sue richieste, ruoli obbligatori mancanti, margine sulla scadenza normativa (o desiderata) delle richieste.
- **Budget (passo 5):** il budget del progetto, vedi «Budget del progetto» sotto.
- **Perimetro (passo 5):** work package e milestone nati dopo la baseline in vigore (`scope_added`) e quelli della baseline poi annullati o tolti dal piano (`scope_removed`). Le attività no: sono il dettaglio di un work package a cascata.
- **Semaforo:** rosso con una milestone in ritardo, la previsione oltre la scadenza o uno scostamento oltre le tolleranze (date o budget); giallo con work package in ritardo, dipendenze bloccate, conflitti con altri progetti, perimetro cambiato (`scope_changed`) o ruoli mancanti.

## Cancelli di fase

- Il cancello si accende per fase (`PATCH /plan-phases/{id} {gate_enabled}`); è acceso di default nel modello a cascata.
- Con un cancello acceso e non approvato, le voci delle fasi successive non passano a una fase `in_progress` o `done`: errore `validation.projects.gate_closed`, dal gancio sui passaggi.
- `POST /plan-phases/{id}/gate` apre l'approvazione (soggetto `phase_gate`, politica «Cancello di fase»). L'esito torna con `OnClose`: approvato apre le fasi successive, respinto le blocca, rimandato torna da chiedere.

## Baseline e change request (passo 3)

- **Baseline:** `POST /projects/{ref}/baselines {note}` fissa la prima (chi pianifica il progetto); con una baseline già presente risponde `validation.projects.baseline_exists`. Le successive nascono solo da una change request approvata. `GET /projects/{ref}/baselines` le elenca, `GET /plan-baselines/{id}` ne dà le voci.
  - Immutabili: un trigger (`plan_baseline_immutable`) rifiuta `update` e `delete` diretti; passano solo quelli a cascata da un vincolo (cancellazione del tenant o degli elementi, copia dell'ambiente di prova).
  - Il piano (`GET /projects/{ref}/plan`) porta `baseline` (in vigore), `tolerance` e, per voce, `in_baseline`, `baseline_start`, `baseline_end`, `variance_days`. Il Gantt le disegna in trasparenza sotto le barre.
- **Tolleranze:** del tenant in `project_settings` (Configurazione → Progetti; predefinite +10 giorni lavorativi, +5% sul budget), sovrascrivibili per progetto in `ext_project` (passo 5: `PUT /projects/{ref}/tolerance {days, budget_pct}`, `null` torna a quella del tenant, dalla scheda «Piano»). Le tolleranze del progetto le cambiano l'amministratore, il governo del portafoglio (`portfolio.admin`) e chi ha sul progetto il ruolo `pmo` o `sponsor`, non il PM: il piano porta `project_tolerance` e `can_edit_tolerance`. Dopo il salvataggio si ricontrolla il piano.
- **Change request automatica:** se c'è una baseline e nessuna change request aperta, nasce un elemento `change_request` figlio del progetto con il piano proposto (la fotografia del piano) e gli scostamenti quando:
  - una milestone non chiusa o la fine del progetto vanno oltre la tolleranza in giorni, dopo ogni spostamento salvato (`saveChanges`: trascinamento, cascata, pianificazione automatica);
  - (passo 5) il costo del progetto (previsione, altrimenti approvato) supera la tolleranza sul budget della baseline: il controllo parte da `finance.Service.Changed`, chiamata quando si approva una versione di budget, si pubblica una previsione o si approva un trasferimento, sul progetto o su una sua richiesta (`projects.BudgetChanged`). Scostamento `budget` con `pct`;
  - (passo 5) si aggiunge o si toglie un work package o una milestone rispetto alla baseline: creazione, cancellazione, passaggio a una fase annullata. Scostamenti `scope_added` e `scope_removed`;
  - (passo 5) si salvano tolleranze del progetto più strette.
  - Il titolo dice cosa è uscito: «Scostamento oltre le tolleranze», «Budget oltre la tolleranza», «Variazione del perimetro».
- **A mano:** `POST /projects/{ref}/change-requests {title?, reason}`; una sola aperta per progetto (`validation.projects.cr_open`).
- **Approvazione:** come il cancello di fase, nel motore delle approvazioni (soggetto `change_request` sul progetto, politica «Change request»). Se non parte (nessuna politica, manca lo sponsor), la change request resta «Da inviare» con codice e messaggio in `blocked`, e lo spostamento va comunque a buon fine. `POST /change-requests/{id}/submit {reason?}` la invia, `POST /change-requests/{id}/withdraw` la ritira: da inviare oppure (passo 5) in approvazione. In approvazione si annulla anche la richiesta nel motore delle approvazioni (`cancelled`, passi `skipped`), che sparisce dalla posta di chi doveva decidere; la baseline resta. La change request porta `can_withdraw`. Errore `validation.projects.cr_withdraw` se non è più ritirabile.
  - Esito con `OnClose`: approvata, il piano proposto diventa la baseline successiva (e si ricontrolla il piano attuale, che può essersi mosso intanto); respinta, la baseline resta; rimandata, torna da inviare.
  - Le fasi del processo di partenza (`change_request_standard.json`: Da inviare, In approvazione, Approvata, Respinta, Ritirata) le muove il modulo; a mano si può solo ritirare.
- **Visibilità:** baseline e change request seguono quella del singolo elemento come il piano: le voci che l'attore non legge arrivano senza titolo, le change request che non legge non compaiono, il budget della baseline compare solo a chi vede quello del progetto.

## Fornitori (passo 4)

- **Chi fa cosa** (decisione 3 del 2026-11-26: il fornitore entra come utente esterno oppure no, per work package):
  - chi pianifica il progetto imposta contratto, contatto (`supplier_contact` sul work package) e `supplier_updates`, crea i deliverable, accetta o respinge SAL e consegne;
  - con `supplier_updates` acceso il contatto dichiara i SAL e consegna i deliverable; spento, lo fa il PM, che può anche inserire e accettare insieme un SAL ricevuto fuori da Regia (`accept: true`).
  - Il contatto del fornitore può essere un membro senza permessi sul tenant: legge il work package come stakeholder, e il ruolo passa ai SAL e ai deliverable del work package (`shareContacts`), quindi vede solo quelli. Aggiorna date e stima del suo work package come il referente del team (`canUpdateItem`).
- **SAL:** percentuale complessiva (non scende sotto l'ultima accettata) e/o importo del periodo; uno solo da accettare alla volta. Respingere chiede il motivo. Il SAL accettato entra nello storico dell'avanzamento del work package (`ext_plan_progress`, nota «SAL n»). Il processo di partenza (`progress_claim_standard.json`: Dichiarato, Accettato, Respinto, Ritirato) lo muove il modulo; a mano si ritira solo un SAL dichiarato.
- **Deliverable:** da consegnare → consegnato → accettato, oppure respinto (torna da consegnare, con il motivo). Processo `deliverable_standard.json`; a mano solo «Annulla».
- **Importi** (contratto, SAL, totale accettato): solo a chi pianifica il progetto e al contatto del fornitore.
- **Avvisi:** SAL dichiarato e deliverable consegnato arrivano ai PM del progetto (notifica `project`, eventi `claim_submitted`, `deliverable_delivered`).

| API | cosa |
| --- | --- |
| `GET /plan-items/{id}/supplier` | contratto, contatti, SAL, deliverable, avanzamento, permessi (`can_edit`, `can_declare`, `amounts_visible`) |
| `PUT /plan-items/{id}/supplier` | `supplier_updates`, `contract_ref`, `contract_amount_cents` (o `clear_amount`), `contract_currency`, `contact_id` ("" lo toglie) |
| `POST /plan-items/{id}/claims` | nuovo SAL `{percent?, amount_cents?, period_end?, note?, accept?}` |
| `POST /progress-claims/{id}/decision` | `{outcome: accepted \| rejected, note}` |
| `POST /plan-items/{id}/deliverables` | nuovo deliverable `{title, due?, milestone_id?}` |
| `POST /deliverables/{id}/deliver`, `POST /deliverables/{id}/decision` | consegna e decisione |

## Componenti condivisi (passo 4)

- **Conflitto:** due work package aperti con date, di progetti diversi e aperti, sulla stessa applicazione e con periodi sovrapposti (`conflictsSQL`). Il piano li porta sul work package (`conflicts`), la Panoramica nei numeri; l'altro progetto non leggibile diventa «un altro progetto».
- **Avviso al team owner:** dopo ogni spostamento salvato, la creazione di un work package e il cambio di applicazione (`notifyConflicts`), il referente e i membri del team owner dell'applicazione (`ext_application.owner_team_id`) ricevono una notifica `project` (evento `conflict`) sull'applicazione, una volta per coppia (`plan_conflicts`). Il payload porta le chiavi dei progetti, non i titoli. Quando il conflitto si risolve la coppia si dimentica.

## Programmi (passo 4)

- Il programma è un elemento `program` dello spazio PRJ; i progetti lo citano in `ext_project.program_id`. Lo crea chi scrive gli elementi (ne diventa `program_manager`); un progetto entra o esce dal programma con `PUT /projects/{ref}/program {program}` (chi pianifica il progetto o governa il programma).
- **Numeri:** `computeNumbers` sulle voci di tutti i progetti leggibili messe in un solo piano, con il metodo e le tolleranze del tenant; il semaforo è il peggiore tra quello aggregato e quelli dei progetti (che contano anche baseline e budget); il budget è la somma di quelli dei progetti nella stessa valuta.
- **Roadmap:** per progetto date previste e di baseline, scostamento, milestone con data di baseline, contrattuale e ritardo.

| API | cosa |
| --- | --- |
| `GET /programs`, `POST /programs` | elenco con i numeri, nuovo programma `{title, body_text?}` |
| `GET /programs/{ref}` | numeri aggregati, progetti con numeri e milestone, `can_edit` |
| `PUT /projects/{ref}/program` | mette il progetto nel programma (`""` lo toglie) |

## Budget del progetto (passo 5)

- Il budget del progetto è costruito sulla finanza: i budget con `element_id` del progetto (si creano in Finanza con la chiave del progetto, «Attinge da» compreso) più quelli delle richieste confluite; consuntivo e impegnato vengono da `cost_rollups` sull'unione delle famiglie (`finance_family`) di progetto e richieste, contati una volta sola. Lo calcola `finance.ProjectTotals`, che il server passa al modulo come `BudgetFunc(ctx, tx, actor, projectID, requestIDs)`.
- `Numbers.budget`: previsto (`approved_cents`), in approvazione (`pending_cents`), impegnato, speso, previsione a finire, consumo, e l'elenco delle versioni in vigore (`budgets`, con la chiave dell'elemento). `budget_visible` dice se l'attore vede il budget anche quando non c'è ancora.
- **Chi lo vede:** `finance.read`, chi pianifica il progetto, chi ha sul progetto un ruolo tra sponsor, business owner, PMO, program manager e responsabile operations, il program manager o lo sponsor del programma, chi legge una delle richieste confluite (lo stesso criterio di prima). Il team e il contatto del fornitore no.
- **Panoramica:** blocco «Budget e costi» con i numeri, le versioni, «Apri in Finanza» e, per chi ha `finance.read`, il campo «Attinge da» di ogni budget (`RequestBudgetSource` sull'elemento del progetto).
- **Programma:** il budget di ogni progetto nella tabella (`ProgramProject.budget`, solo a chi lo vede) e la somma nei numeri del programma, come prima.
- La baseline fotografa il budget del progetto così calcolato (prima solo quello delle richieste).

## Status report del progetto (passo 5)

- È lo status report della richiesta (`status_reports`, `internal/modules/report`): bozza, invio, presa visione, confronto, export. La fotografia (`data`) per il progetto viene dai numeri del progetto: il server collega `report.ProjectSnapshot` a `projects.Numbers` (`projectStatusSnapshot` in `internal/api/projects.go`), con gli occhi dell'autore. `data.project` porta semaforo e motivi, avanzamento, work package e milestone, baseline e scostamenti, perimetro, ritardi, voci oltre la tolleranza, bloccate, SAL da accettare, conflitti, change request aperte, rischi e ruoli mancanti; `schedule.planned_pct` è l'avanzamento atteso (`projects.PlannedPct`).
- **Semafori proposti** (`suggestProject`): tempi da milestone in ritardo, scadenza superata, tolleranze e avanzamento contro atteso; costi dalla tolleranza sul budget e poi dalle regole della richiesta; perimetro dalle voci aggiunte o tolte e dalle change request in corso; rischi da rischi aperti, conflitti e ruoli mancanti. Sintesi, fatte, prossime, rischi e decisioni sono precompilati (`prefillProjectText`); il PM corregge e commenta.
- **Chi:** scrivono il PM, il PMO e chi ha `element.write`; prendono visione sponsor e PMO; all'invio avvisa chi segue il progetto (gli stakeholder, quindi lo sponsor), notifica `report` evento `submitted`.
- **Visibilità alla lettura** (`redactProject`): le voci che chi legge non può leggere arrivano senza titolo; chi non vede il budget del progetto riceve `cost.hidden` senza importi.
- **API:** `GET|POST /projects/{ref}/status-reports`, `GET .../prefill`, `PUT .../schedule`, e il singolo report su `/project-reports/{id}` (`PATCH`, `DELETE`, `/refresh`, `/submit`, `/acknowledge`, `/compare`, `/export.md`), prefisso del modulo dei progetti: funziona anche con la domanda spenta. Gli stessi handler della richiesta.
- **Promemoria:** il report è atteso per i progetti in una fase `in_progress`; le richieste confluite in un progetto non lo chiedono più (lo chiede il progetto).
- **Interfaccia:** scheda «Stato» del progetto (`StatusReportsTab kind="project"`), con i numeri del progetto nel blocco dei dati; in Panoramica il blocco «Status report» con gli ultimi tre inviati, la bozza in corso e la prossima scadenza.

## «In arrivo dai progetti» (passo 5)

- `GET /projects/incoming?team=<id>|space=<chiave>&weeks=8` (da 1 a 52): work package con quel team e attività di quei work package, aperti, di progetti aperti, con date che toccano le prossime settimane. Il work package nella fase iniziale è `proposed` (da accettare); quello agile accettato porta i suoi epic. Le voci che l'attore non legge non compaiono e si contano in `restricted`; il progetto e il work package non leggibili restano senza titolo.
- **Interfaccia:** nel backlog del team (colonna a destra), con la finestra di 4, 8 o 12 settimane, se il modulo dei progetti è acceso e lo spazio è di un team.

## Ancora aperto

- **Carico del componente in Architettura:** i conflitti stanno nel piano e nella notifica; la scheda dell'applicazione non mostra ancora i progetti che la toccano (`conflictsOf` e l'indice su `ext_work_package.application_id` sono il punto di partenza).
- **Riepilogo sulla richiesta:** la scheda «Stato» della richiesta confluita mostra ancora i suoi report; non rimanda a quelli del progetto.

## Nota sulla visibilità

Il piano, la baseline, le change request, i conflitti e il programma applicano i limiti del singolo elemento (`visibility.go`): una voce non leggibile resta con date, avanzamento e dipendenze ma senza titolo, persone e collegamenti. Il fornitore esterno vede il suo work package, i suoi SAL e i suoi deliverable perché ne è stakeholder.
