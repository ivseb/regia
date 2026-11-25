# Requisiti e gate di rilascio

## Requisiti, verifica, deroghe e gate di rilascio (sdlc.md §10, passi 5 e 6)

- **Matrice di una richiesta o di un impatto:**
  - `GET /api/v1/elements/{chiave}/requirements?team=&category=&status=`: colonne (impatti), righe (requisiti) con le celle allineate, riepilogo con i fatti e i requisiti mancanti, team e categorie per i filtri, `can.create`;
  - `GET /api/v1/elements/{chiave}/requirements.csv?…`: stessa matrice in CSV (`;` e BOM);
  - `POST /api/v1/elements/{chiave}/requirements` con `{"title", "text", "category", "scope": "project" | "application", "impact", "verification_method": "manual" | "evidence", "verification_check", "verification_criterion", "verifier_team" | "verifier_role"}`;
  - `POST /api/v1/elements/{chiave}/requirements/baseline` con `{"catalog"}` → `{"created": [...]}`.
- **Il requisito:**
  - `GET /api/v1/requirements/{chiave}`: dati e origine, celle con i permessi dell'attore (`ready`, `verify`, `exception`, `why_not`), verifiche firmate, deroghe, impostazioni;
  - `PATCH /api/v1/requirements/{chiave}`: categoria e criterio di verifica, solo da proposto o accettato (titolo e testo con `PATCH /api/v1/elements/{id}`);
  - `POST /api/v1/requirements/{chiave}/cells/{impatto}/ready` con `{"note"}`;
  - `POST /api/v1/requirements/{chiave}/cells/{impatto}/verify` con `{"outcome": "verified" | "rejected", "note", "links", "attachments": [id], "evidence_element"}`;
  - le fasi (Accetta, Non applicabile, Riapri) con `POST /api/v1/elements/{id}/transitions`.
- **Requisiti dai questionari:** una regola di derivazione `kind: "requirement"` crea un requisito vero con origine `questionnaire` (accanto a `prescription`, `catalog`, `manual`).
  - Durante la compilazione i requisiti sono «proposti» nella valutazione (tabella `review_requirement_proposals`, allineata a ogni salvataggio delle risposte: la proposta non più derivata sparisce se non è ancora diventata requisito).
  - Alla chiusura (`FromReview`, stesso gancio delle prescrizioni) con esito favorevole o con prescrizioni nascono i requisiti: di applicazione se la valutazione è di un impatto, di progetto se è della richiesta; verificatore il team della valutazione; relazione «nasce da» verso la valutazione; avvisi ai responsabili come per gli altri. Con esito non favorevole non nasce niente.
  - Idempotenza: una regola genera un solo requisito per valutazione (indice univoco su `review_id` + `rule_key`, `ext_requirement.rule_key`).
  - Le prescrizioni accettate restano a parte (origine `prescription`) e le prescrizioni già create dalle regole `requirement` prima di questa modifica non si convertono. Migrazione `0075`.
- **Inbox dei verificatori:** `GET /api/v1/requirements/inbox` → `{"items": [...]}`.
- **Deroghe:**
  - `POST /api/v1/requirements/{chiave}/exceptions` con `{"impacts", "reason", "risk_level", "expires_on", "compensating", "submit"}` (di default chiede subito l'approvazione);
  - `GET` e `PATCH /api/v1/exceptions/{chiave}` (modifica solo in bozza, chi l'ha chiesta);
  - decisioni con `POST /api/v1/approvals/steps/{id}/decision`; chi ha chiesto riceve 403.
- **Gate di rilascio:** `GET /api/v1/release-gate?application=CODICE&request=DEM-12` → `{"allowed", "application", "impacts": [{"impact", "request", "phase", "summary"}], "reasons", "checked_at"}`. Serve `release.read` (ruolo «Pipeline di rilascio»); con un token:
  ```bash
  curl -H "Authorization: Bearer myt_…" "https://<host>/api/v1/release-gate?application=FE&request=DEM-12"
  ```
  Ogni chiamata finisce nell'audit (`release_gate.check`).
- **Configurazione** (scrittura con `process.admin`, lettura con `element.read`):
  - `GET /api/v1/requirement-config`: cataloghi, impostazioni, politica delle deroghe, team valutatori, ruoli;
  - `PUT /api/v1/requirement-config/settings` con `{"risk_levels": [{"key", "name", "score"}], "max_exception_days", "expiry_notice_days"}`;
  - `PUT /api/v1/requirement-config/exception-policy` con `{"rules": [{"when": {"risk_lte": 2}, "steps": [{"approvers": [{"role": "requirement_verifier"}]}]}]}`;
  - cataloghi: `GET /api/v1/requirement-config/catalogs/{chiave}`, `PUT` e `DELETE …/draft`, `POST …/publish`, `POST /api/v1/requirement-config/catalogs/validate` con `{"definition"}`;
  - anteprima di una condizione: `POST /api/v1/requirement-config/preview` con `{"condition", "scope": "application" | "project"}`;
  - export e import: `GET /api/v1/requirement-config/export`, `POST /api/v1/requirement-config/import?publish=true` (formato `regia.requirements/v1`).
- **Regole di processo:** azioni `apply_baseline` (`ref` = catalogo), `sync_requirements` (sull'impatto), `requirement_stage` e `exception_stage`; controllo dei gate `requisiti_rilascio`.
- **Approvazioni:** soglie `risk_lte` e `risk_gt` nelle politiche; `approval.OpenSubjectWith` sceglie la regola sui fatti indicati; `approval.SeparateRequester(soggetto)` impedisce a chi chiede di decidere.
- **Job:** `regia serve` controlla ogni ora le scadenze delle deroghe (`requirements.RunExpiry`).
- **Pagine:** scheda `?scheda=requisiti` della richiesta e dell'impatto, `/requisiti` (da verificare), `/configurazione/requisiti`.
- **Dopo l'aggiornamento:** `regia tenant provision --slug <tenant>` crea tipi, ruoli, processi e, la prima volta, il pacchetto di partenza. Le richieste e gli impatti già avviati restano sulla loro versione di processo: senza migrazione non hanno la regola delle baseline né il gate di rilascio (le baseline si applicano anche a mano).
- **Dati di esempio:** `scripts/dev-demo-requirements.sh [url]`, richiamato da `dev-demo-data.sh`.
