# Valutazione, budget e governo

## Valutazione, graduatoria e governance (sdlc.md §10, passi 8 e 9)

- **Scorecard** (versionate come i processi):
  - `GET /api/v1/scorecards`, `GET /api/v1/scorecards/{chiave}`: versione corrente, bozza, storia delle versioni con il numero di punteggi;
  - bozza: `PUT /api/v1/scorecards/{chiave}/draft` con `{"definition": {...}, "notes": "..."}`, `DELETE` per scartarla; gli errori di validazione tornano in `problems`;
  - pubblicazione: `POST /api/v1/scorecards/{chiave}/publish` con `{"notes": "..."}`.
- **Valutazione della richiesta:**
  - `GET /api/v1/demand/requests/{chiave}/evaluation`: scorecard corrente, `can_score`, punteggio, valori proposti, storia;
  - `POST /api/v1/demand/requests/{chiave}/evaluation` con `{"version_id", "values": {"<criterio>": {"value", "note"}}, "comment"}`: un voto per ogni criterio; risponde con il totale 0-100.
- **Graduatoria:**
  - `GET /api/v1/portfolio/ranking?budget_cents=` (il parametro simula un budget senza salvarlo);
  - `GET /api/v1/portfolio/ranking.csv?budget_cents=`: export con separatore `;` e BOM per Excel;
  - `PUT /api/v1/portfolio/ranking/{chiave}` con `{"position": 2, "reason": "..."}`; `position: null` torna all'ordine automatico;
  - `PUT /api/v1/portfolio/settings`: `mandatory_first`, `budget_cents` (o `clear_budget`), `estimate_basis` (`low`, `mid`, `high`), `excluded_phases`, `rank_roles`.
- **Governance:**
  - organi: `GET/POST /api/v1/governance/bodies`, `PATCH /api/v1/governance/bodies/{id}` (nome, tipo, presidente, segretario, quorum, membri con `voting`);
  - sedute: `GET /api/v1/governance/meetings?from=AAAA-MM-GG&to=…&body=…`, `POST /api/v1/governance/meetings` (`body_id`, `scheduled_at` RFC 3339, `location`, `link`, `propose`), `GET/PATCH /api/v1/governance/meetings/{id}`;
  - stato: `POST /api/v1/governance/meetings/{id}/status` con `convened`, `held` o `cancelled`;
  - ordine del giorno: `POST /api/v1/governance/meetings/{id}/items` con `{"element": "DEM-12"}`, `DELETE /api/v1/governance/meetings/{id}/items/{chiave}`;
  - presenze: `PUT /api/v1/governance/meetings/{id}/attendance` con `{"attendance": [{"member_id", "present"}]}`;
  - decisione: `POST /api/v1/governance/meetings/{id}/items/{chiave}/decision` con `decision` (`approved`, `approved_with_conditions`, `rejected`, `deferred`, `informational`), `note`, `conditions`;
  - verbale: `PUT /api/v1/governance/meetings/{id}/minutes` con `{"text"}`, `POST /api/v1/governance/meetings/{id}/minutes/approve`.
- **Permessi:**
  - `portfolio.admin` (ruolo `portfolio_manager` "Governo del portafoglio", o l'amministratore): scorecard, impostazioni della graduatoria, organi;
  - votano i ruoli sulla richiesta indicati dalla scorecard (default `demand_manager`, `pmo`); forzano l'ordine i ruoli di `rank_roles` (default `pmo`);
  - sedute: le gestiscono presidente, segretario o `portfolio.admin`; le decisioni le registrano presidente o segretario; il verbale lo approva il presidente.
- **Pacchetto di partenza:** `internal/modules/portfolio/starter/scorecard_portafoglio.json` (`portfolio.Provision`, anche con `regia tenant provision`).
- **Dati di esempio:** `scripts/dev-demo-governance.sh [url]`, richiamato da `dev-demo-data.sh`.

## Budget e costi (sdlc.md §10, passi 11 e 12)

- **Della richiesta** (aggregati, per chi vede la richiesta):
  - totali: `GET /api/v1/demand/requests/{chiave}/finance`, con preventivo, budget approvato e in approvazione, consuntivo, impegnato, previsione a finire, consumo, per categoria e per mese, `can_see_details`;
  - earned value: `GET/PUT /api/v1/demand/requests/{chiave}/ev` con `{ev_enabled, ev_method, percent_complete, baseline_budget_id}`; fotografia: `POST /api/v1/demand/requests/{chiave}/ev/snapshots` con `{as_of, percent_complete}` (vuoto = oggi, con il metodo).
- **Pagina Finanza** (`/api/v1/finance/*`, permesso finance.read; le scritture anche finance.write):
  - `GET /overview`: impostazioni, esercizi con totali, categorie, fonti, campi dell'import; `PUT /settings` (`labor_category_id`, `portfolio_fiscal_year_id` o `clear_portfolio`, `portfolio_basis` `envelope`/`residual`, `alert_thresholds`);
  - esercizi: `POST /fiscal-years`, `PATCH /fiscal-years/{id}` (`name`, `start_date`, `end_date`, `status`, `envelope_cents` o `clear_envelope`);
  - categorie e fonti: `POST /categories`, `PATCH /categories/{id}`, `POST /funding-sources`;
  - cambi: `GET /exchange-rates?currency=`, `PUT /exchange-rates` con `{currency, valid_on, rate_to_base}`;
  - budget: `GET /budgets?fiscal_year=&element=&all=true`, `POST /budgets` con `{element, fiscal_year_id, kind, note, lines}` (`kind` = `requested`, `revised`, `forecast`; per revisione e previsione senza righe si copia la corrente), `GET /budgets/{id}`, `PUT /budgets/{id}/lines` con `{lines, note}`, `POST /budgets/{id}/submit`, `DELETE /budgets/{id}` (solo bozze);
  - trasferimenti: `GET /transfers?fiscal_year=`, `POST /transfers` con `{from_line_id, to_line_id, amount_cents, reason}`;
  - voci: `GET /costs?element=&category=&kind=&source=&from=&to=&q=&cursor=&limit=` (con i totali della selezione), `POST /costs`, `DELETE /costs/{id}` (non quelle da ore);
  - ore: `GET /work-logs?element=&member=&from=&to=`, `POST /work-logs` con `{element, member_id, work_date, hours, role_key, capitalizable, text}`, `DELETE /work-logs/{id}`, `POST /work-logs/recompute`;
  - tariffe: `GET /rates`, `POST /rates`, `PATCH /rates/{id}` con `{member_id | role_key, hourly_cents, currency, valid_from, valid_to, note}`, `DELETE /rates/{id}`;
  - import (costi e ore): `GET/POST /import-profiles`, `PUT/DELETE /import-profiles/{id}` con `{name, target, mapping: {columns, delimiter, date_format, decimal, defaults}}` (`target` = `cost_entries`, il default, o `work_logs`; di un profilo esistente non cambia); `GET /import-profiles?target=` e `GET /imports?target=` filtrano per destinazione; `GET /imports/{id}`; prova `POST /imports` con `{profile_id | mapping (+ target), file_name, csv}`; conferma `POST /imports/{id}/confirm` con `{csv, skip_errors}`; annullamento `POST /imports/{id}/rollback`. Corpo fino a 16 MB, al massimo 50.000 righe. Le ore dal CSV (migrazione 0080, `finance/importore.go`) hanno `work_logs.import_run_id` e `source_ref` (indice univoco parziale) e seguono le regole delle ore normali: elemento visibile, persona del tenant, periodo e settimana del timesheet aperti, ruolo del team fotografato; il costo si calcola subito se c'è la tariffa;
  - progetti con earned value: `GET /projects`.
- **Approvazioni:** budget e trasferimenti passano dal motore delle approvazioni (politiche `@budget` e `@budget_transfer` in `approval_policies`, create da `finance.Provision`); si decidono con `POST /api/v1/approvals/steps/{id}/decision`.
- **Budget gerarchico** (`/api/v1/org/*`, `/api/v1/budget/*`; migrazione 0050): unità con persone e ruoli, periodi, stanziamenti a cascata con registro dei movimenti immutabile, visibilità per area con la RLS, ripartizione, fine periodo e trasferimenti. Le transazioni portano il membro (`db.TenantOpts.Member` → `app.member_id`). Tutte le API in [budget-api.md](../budget-api.md).
- **Job:** `regia serve` aggiorna ogni giorno le fotografie dell'earned value e manda gli avvisi di fine periodo del budget (`finance.RunDaily`).
- **Dopo l'aggiornamento:** `regia tenant provision --slug <tenant>` crea categorie, fonte, impostazioni e politiche nei tenant esistenti.
- **Dati di esempio:** `scripts/dev-demo-finance.sh [url]`, richiamato da `dev-demo-data.sh`.

## Ondata 1 del governo (replica/usabilita.md, sezione 4; migrazione 0045)
- **Codice di catalogo come chiave.** Il codice di un'applicazione o di una capability (`APP-MOB`) risolve l'elemento come la chiave (`ARC-8`): `GET /api/v1/elements/APP-MOB`, `/e/APP-MOB`, `?q=APP-MOB` (in testa ai risultati) e la ricerca del catalogo applicazioni, che accetta anche la chiave. Nelle viste di architettura il codice è in evidenza e la chiave sta piccola accanto (`web/src/catalogId.tsx`).
- **Risoluzione dell'incidente.** `PATCH /api/v1/incidents/{chiave}` con `resolved_at` (la prima volta) porta l'incidente alla fase la cui regola `enter:` esegue `incident_stage: resolved`, se da lì c'è un passaggio diretto e libero (`engine.EntryPhaseWithAction`, `engine.AtOrBeyondPhase`). Altrimenti la risposta porta `phase_move: {status: "choose", options}` e l'interfaccia chiede in quale fase portarlo. Gli script demo (`dev-demo-dora.sh`) passano da «Classificato» come farebbe una persona.
- **Causa: fornitore terzo.** `cause_provider` (chiave del fornitore ICT) in `PATCH`/`POST /incidents`; colonna `ext_incident.cause_provider_id` (migrazione 0045). Con un'altra causa il fornitore si toglie.
- **Copertura pesata.** `weighted_percent` accanto a `percent` in ogni conteggio di conformità: critica 4, alta 3, media 2, bassa 1 (criticità dell'applicazione del perimetro), il resto 2.

## Sintesi di scheda (ondata 2, proposta 2; nessuna migrazione)
- **Richiesta «decisione prima».** `GET /api/v1/elements/{ref}/summary` (`internal/api/scheda.go`): una sola richiesta con decisione in attesa, punteggio, posizione in graduatoria e linea di taglio (`rankingScoped`, la stessa della pagina Graduatoria), area richiedente con stanziamento e residuo del budget gerarchico, valutazioni aperte, requisiti (da accettare, da realizzare, da verificare, con `Coverage`), rischi collegati e «cosa manca» con chi deve agire. Solo codici e conteggi: i testi sono del client (`scheda.action.<kind>`).
- **Applicazione 360.** `GET /api/v1/architecture/applications/{ref}/360`: responsabili, dipendenze a un livello, richieste e impatti aperti, vulnerabilità e SLA (`VulnDashboard`), ultimi rilasci, gate, aderenza alla politica, controlli e conformità, rischi, incidenti, documenti. Un blocco non leggibile o di un modulo spento è `null`/assente; letture e RLS sono quelle di chi chiede.
- **Interfaccia.** Scheda «Sintesi» (`web/src/pages/Scheda.tsx`, `schedaApi.ts`) predefinita per richieste e applicazioni dopo le regole già decise (decisione in attesa → approvazioni; incidenti → Dettagli; controlli → Conformità; ruoli); un link a `#commenti` apre l'attività. Sul telefono la colonna principale precede i campi. Test: `internal/itest/scheda_test.go`, `web/e2e/ondata2-scheda.spec.ts`.
