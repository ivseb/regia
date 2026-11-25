# Rischi, conformità e crisi

## Rischi, fornitori ICT e incidenti (piano, passo 20)

Disegno in [replica/grc.md](../../replica/grc.md), sezione «Rischi, terze parti e incidenti»; migrazione `0029_grc_rischi.sql`; test in `internal/itest/grc_rischi_test.go`.

- **Rischi:**
  - registro: `GET /api/v1/risks?q=&fase=aperti|<fase>&livello=<fascia>&trattamento=&categoria=&oltre_soglia=true&titolare=<membro>&collegato=<chiave>` e lo stesso in CSV su `/api/v1/risks.csv`;
  - `POST /api/v1/risks` con `{"title", "description", "category", "matrix", "inherent_likelihood", "inherent_impact", "residual_likelihood", "residual_impact", "treatment": "mitigare|accettare|trasferire|evitare", "treatment_plan", "treatment_due", "review_due", "owner_id", "targets": [chiavi]}` (`risk.admin`);
  - `GET` e `PATCH /api/v1/risks/{chiave}` (il titolare aggiorna residuo e trattamento; 0 e 0 azzerano una valutazione);
  - collegamenti: `POST /api/v1/risks/{chiave}/links` con `{"target"}` (applicazione, asset, fornitore → `risk_of`; controllo → `mitigates`), `DELETE …/links/{chiave}`;
  - accettazione: `POST /api/v1/risks/{chiave}/acceptance` con `{"until", "comment"}`; le decisioni con `POST /api/v1/approvals/steps/{id}/decision`.
- **Fornitori e contratti:**
  - `GET /api/v1/ict/providers?q=&critici=true`, `POST` (`risk.admin`), `GET`/`PATCH /api/v1/ict/providers/{chiave}`;
  - applicazioni servite: `POST /api/v1/ict/providers/{chiave}/applications` con `{"application"}`, `DELETE …/applications/{chiave}`;
  - contratti: `POST /api/v1/ict/providers/{chiave}/contracts`, `PATCH` e `DELETE /api/v1/ict/contracts/{id}`, `GET /api/v1/ict/contracts?in_scadenza=true`;
  - registro delle informazioni: `GET /api/v1/ict/register.csv?columns=fornitore.nome,contratto.fine&grain=contract|provider` (vuoto = colonne configurate; catalogo in `GET /api/v1/risk-config`, `column_catalog`).
- **Incidenti:**
  - `GET /api/v1/incidents?q=&aperti=true&gravi=true`, `POST /api/v1/incidents` (`element.write` o `incident.manage`) con `{"title", "description", "detected_at" (ISO 8601), "clients_affected", "clients_affected_pct", "downtime_minutes", "data_loss": ["integrità", …], "economic_impact_cents", "member_states", "reputational", "recurring", "cause", "lead_id", "impacted": [chiavi]}`;
  - `GET` e `PATCH /api/v1/incidents/{chiave}` (`incident.manage`; se classificato si riclassifica);
  - `POST /api/v1/incidents/{chiave}/links` con `{"target"}` (applicazione o asset impattato, rischio concretizzato), `DELETE …/links/{chiave}`;
  - timeline: `POST /api/v1/incidents/{chiave}/events` con `{"kind", "text", "at"}`;
  - classificazione: `POST /api/v1/incidents/{chiave}/classify` (`?dry_run=true` = anteprima);
  - postmortem: `PUT /api/v1/incidents/{chiave}/postmortem` con `{"summary", "root_cause", "lessons"}`; azioni correttive: `POST /api/v1/incidents/{chiave}/actions` con `{"title", "body_text", "assignee_id", "team_id", "due"}` (crea un task).
- **Notifiche normative:** `GET /api/v1/regulatory-notifications` (da inviare, per scadenza), `PUT /api/v1/regulatory-notifications/{id}/draft` con `{"content"}`, `POST …/{id}/submit` con `{"content", "submitted_at", "reference", "channel", "note"}`, `POST …/{id}/not-required` con `{"note"}`.
- **Cruscotto:** `GET /api/v1/risk/dashboard` (heatmap della matrice predefinita, rischi oltre la soglia e scaduti, fornitori critici, contratti in scadenza, incidenti aperti, notifiche da inviare).
- **Configurazione** (lettura con `element.read`; scrittura con `risk.admin`, gli schemi anche con `incident.manage`):
  - `GET /api/v1/risk-config`; `PUT /api/v1/risk-config/matrices/{chiave}`; `PUT /api/v1/risk-config/settings` con `{"register_columns": [{"key", "label"}], "contract_notice_days", "review_notice_days", "notification_warning_hours"}`; `PUT /api/v1/risk-config/acceptance-policy` con `{"rules"}`; `PUT /api/v1/risk-config/schemes/{chiave}`; `POST /api/v1/risk-config/schemes/preview` con `{"scheme", "incident"}`;
  - export e import: `GET /api/v1/risk-config/export`, `POST /api/v1/risk-config/import` (formato `regia.risk/v1`).
- **Fatti per le condizioni:** sul rischio `punteggio_inerente`, `livello_residuo`, `oltre_soglia`, `trattamento`, `controlli`, `funzione_critica`…; sull'incidente `clienti_coinvolti`, `fermo_minuti`, `perdita_dati`, `funzione_critica`, `grave`, `classificazione`, `notifiche_aperte`, `postmortem`…; sul fornitore `critico`, `funzioni_critiche`, `paese`, `scadenza_contratto`; sull'applicazione `fornitori_ict`, `rischi_oltre_soglia`. Elenco completo nell'editor dei processi.
- **Regole di processo:** azioni `risk_acceptance` (`submitted`, `accepted`, `reopened`) e `incident_stage` (`detected`, `classified`, `resolved`, `closed`).
- **Notifiche:** tipo `risk` (eventi `risk_accepted`, `risk_rejected`, `risk_acceptance_expired`, `risk_review_due`, `risk_treatment_due`, `contract_expiring`, `incident_classified`, `notification_due`, `notification_overdue`).
- **Job:** `regia serve` controlla ogni 15 minuti contratti, revisioni, trattamenti, accettazioni e notifiche normative (`risk.RunDeadlines`).
- **Pagine:** `/rischi` (schede cruscotto, rischi, fornitori ICT, incidenti, configurazione) nel menu Governo; scheda «Dettagli» di `/e/RIS-…`, `/e/INC-…`, `/e/FOR-…`.
- **Ruoli:** «Risk manager» (`risk.admin`) e «Incident manager» (`incident.manage`); i membri vanno anche nei gruppi «Risk management» e «Incident management», titolari dei ruoli sugli elementi che abilitano i passaggi.
- **Dopo l'aggiornamento:** `regia tenant provision --slug <tenant>` crea spazi, tipi, ruoli, gruppi, relazioni, processi e, la prima volta, il pacchetto di configurazione.
- **Dati di esempio:** `scripts/dev-demo-risk.sh [url]`, richiamato da `dev-demo-data.sh` (fornitori con nomi inventati). Persone: `rischi@`, `rischi2@` (Risk manager) e `incidenti@demo.test` (Incident manager).

## Conformità (piano, passo 20; replica/grc.md)

- **Migrazione:** `0028_grc_conformita.sql` (cataloghi privati del tenant sotto RLS, `control_requirements`, applicabilità estesa, `ext_evidence` estesa, `grc_requirement_links`).
- **Modulo:** `internal/modules/grc` (cataloghi, OSCAL/CSV, controlli, applicabilità, evidenze, copertura, pacchetto per l'audit, collegamenti). Rotte in `internal/api/grc.go` sotto `/api/v1/grc/...`: `dashboard`, `library`, `frameworks/{key}` (+ `coverage`, `export`, `draft`, `publish`), `starters/{key}`, `mappings`, `rules`, `scopes/{kind}/{target}`, `controls`, `evidence`, `project-requirements/{ref}/links`, `audit-pack`.
- **Permessi:** `grc.admin`, `grc.read`, `grc.collect` (`internal/platform/rbac/grc.go`); ruoli Compliance officer e Raccolta evidenze, Auditor esteso in sola lettura.
- **Pagine:** `/conformita` (Governo → Conformità), scheda controllo e scheda evidenza in `/e/{chiave}`.
- **Dopo l'aggiornamento:** `regia tenant provision --slug <tenant>` crea tipi, ruoli, processi e il pacchetto di partenza.
- **Dati di esempio:** `scripts/dev-demo-grc.sh [url]`, richiamato da `dev-demo-data.sh`; `dev-seed.sh` crea `conformita@demo.test` (Compliance officer).
- **Prova:** `go test ./internal/itest -run GRCConformita`.

## Ondata 2: vista di crisi e dal problema all'azione (migrazione 0056)
Specifica: `replica/usabilita.md`, proposte 3 e 5. Test: `internal/itest/ondata2_crisi_test.go`, `web/e2e/ondata2-crisi.spec.ts`. Testi nell'area `crisi` del catalogo.
- **Vista di crisi** (`/crisi/<chiave>`, `web/src/pages/IncidentCrisis.tsx`): si apre dalla banda dell'incidente («Apri la vista di crisi»). Progettata prima per il telefono: orologio delle notifiche con conto alla rovescia, prossima azione, barra d'azione fissa in basso (da 44 px; su schermo largo diventa una riga sotto l'intestazione) con «Registra» (decisione, comunicazione, aggiornamento, contenimento) e il comando della prossima azione. Sotto: impatto sulle applicazioni, fornitori, decisioni e comunicazioni, bozza della notifica, cronologia unica.
- **API.** `GET /api/v1/incidents/{ref}/crisis` (chi legge l'incidente) risponde con l'incidente e: `next` (classify, notify, contain, update, resolve, close, postmortem, none; un incidente aperto senza aggiornamenti da 4 ore chiede `update`), `applications` (ultimo rilascio, sospetto se in produzione nelle 24 ore prima della rilevazione; vulnerabilità aperte, oltre lo SLA, critiche; rischi aperti; altri incidenti; documenti collegati; applicazioni che ne dipendono dal grafo), `providers` (causa e fornitori delle applicazioni, con criticità e fine contratto), `timeline` (eventi, rilasci e CVE critiche prima della rilevazione, scadenze future come segnaposto), `decisions`, `communications`, `draft` (campi della notifica dai dati classificati, con `missing` per i campi obbligatori). Il nuovo tipo di evento `decision` si registra con `POST /incidents/{ref}/events` (`incident.manage`). Codice in `internal/modules/risk/crisis.go`.
- **Bozza della notifica.** I campi sono dati (`draft.fields`: chiave, tipo, valore, mancante, obbligatorio); il testo lo compone l'interfaccia nella lingua della persona e si salva con l'API delle notifiche già esistente (`PUT /regulatory-notifications/{id}/draft`, invio con `…/submit`). Resta il disclaimer sul testo normativo da verificare.
- **Dal problema all'azione** (`internal/modules/problems`, `internal/api/problems.go`, `web/src/pages/ProblemActions.tsx`): sotto una vulnerabilità (dettaglio), un blocco del gate («Motivi»), un requisito non soddisfatto (scheda «Verifica») e un'aderenza mancante (pagina Aderenza) compare «Lavoro e azioni collegate».
  - `GET /api/v1/problems/context?kind=finding|gate_decision|requirement|adherence&ref=` dà applicazione, team (il titolare dell'applicazione, o quello dell'impatto), titolo, descrizione e motivo proposti nella lingua della persona, e i collegamenti.
  - `POST /api/v1/problems/work-items` (`element.write`) crea un `task` nello spazio del team (un team senza spazio lo riceve al momento), lo collega all'applicazione e al requisito con la relazione `raised_from` («nasce da» / «ha generato», creata al primo uso) e scrive la riga di `problem_links`. Il problema non si tocca.
  - `POST /api/v1/problems/links` registra un'azione svolta altrove (`exception`, `override`, `adoption_pr`) con il riferimento di ciò che è stato creato.
  - `GET /api/v1/elements/{ref}/problems` dà i problemi da cui nasce un elemento di lavoro.
  - I collegamenti letti dagli altri moduli compaiono senza registrare nulla: eccezioni sulla vulnerabilità (`vuln_exception_findings`), override del gate, deroghe del requisito e pull request di adozione dell'applicazione. «Chiedi eccezione» sulla vulnerabilità apre il modulo con il motivo già avviato e usa l'API delle eccezioni esistente.
