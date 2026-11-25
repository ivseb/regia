# DevSecOps

## DevSecOps, fase 1 (piano, passo 21)

Disegno in [replica/devsecops.md](../../replica/devsecops.md) (stato in §7); migrazione `0024_devsecops.sql`; test in `internal/itest/devsecops_test.go`.

- **Contratto neutro:** `internal/modules/devsecops/contract`. Il modulo `devsecops.Service` implementa `contract.Ingestor` e `contract.ServiceIngestor`: lo usano l'API di push e i connettori (`devsecops.Register(eng, requisiti)` in `cmd/regia/serve.go`, passato con `api.Options.Devsecops`).
- **API di push:** `POST /api/v1/devsecops/services|check-runs|releases|deployments|sboms|vuln-reports` con `"format": "regia.devsecops/v1"`, fino a 32 MB (anche gzip), `202` se cambia qualcosa e `200` se già ricevuto. Permesso `devsecops.write`, ruolo di sistema «Pipeline». Esempi `curl` e step di pipeline in [devsecops-api.md](../devsecops-api.md).
- **Cosa fa:** servizi associati alle applicazioni (codice o chiave; l'associazione dall'interfaccia prevale), ambienti creati al primo uso, change collegati alla richiesta per chiave o campo configurato (alias per i numeri provvisori), SBOM CycloneDX e SPDX in `components`/`sbom_components`/`component_usages`, vulnerabilità deduplicate per servizio e componente in `findings` con i report in `vuln_reports`, CVE al rilascio e di oggi, verifica automatica dei requisiti da evidenza (`requirements.VerifyFromEvidence`).
- **Fatti:** sull'applicazione `cve_critiche_produzione`, `cve_alte_produzione`, `ultimo_rilascio_giorni`, `servizi`, `servizi_fuori_pipeline`, `runtime_fuori_supporto`; sulla richiesta `rilasci`, `rilasci_produzione`, `rilasciata`, `ultimo_rilascio`, `change`, `servizi_rilasciati`.
- **Letture:** `GET /api/v1/devsecops/overview`, `GET /api/v1/devsecops/elements/{chiave}/panel`, `GET /api/v1/devsecops/services/{id}`, `GET /api/v1/devsecops/config` (`element.read`).
- **Configurazione** (`architecture.write` o `tenant.admin`): `PUT /api/v1/devsecops/config/settings` con `{"production_envs", "stale_days", "change_field", "runtimes": [{"name", "version", "end_of_support"}]}`, `PATCH /api/v1/devsecops/environments/{id}` con `{"production", "ordinal"}`, `PATCH /api/v1/devsecops/services/{id}` con `{"application"}`, `PATCH /api/v1/devsecops/changes/{id}` con `{"request", "number", "title"}`; `POST /api/v1/devsecops/services` da una sessione censisce un servizio non ancora sulla pipeline.
- **Pagine:** scheda «Produzione» dell'asset logico e «Servizi e rilasci» dell'applicazione (`/e/ARC-3?scheda=produzione`), `/devsecops` (Governo), `/configurazione/devsecops`.
- **Dati di esempio:** `scripts/dev-demo-devsecops.sh [url]`, richiamato da `dev-demo-data.sh`: nove servizi delle applicazioni demo con sei settimane di rilasci, staging e preprod, SBOM, CVE inventate, due servizi non ancora sulla pipeline. Verifica da sola il requisito «Segreti fuori dal codice sorgente» del portale B2B lasciato da verificare da `dev-demo-requirements.sh`.
- **Dopo l'aggiornamento:** `regia tenant provision --slug <tenant>` aggiunge il ruolo «Pipeline» ai tenant esistenti.

## DevSecOps, fase 2: vulnerabilità governate (piano, passo 21)

Disegno in [replica/devsecops.md](../../replica/devsecops.md) §3.2 (stato in §7); migrazione `0033_vulnerabilita.sql`; codice in `internal/modules/devsecops` (`vuln*.go`), API in `internal/api/devsecops_vuln.go`, interfaccia in `web/src/pages/Vulnerabilities.tsx` e `VulnerabilityConfig.tsx`; test `internal/itest/devsecops_vuln_test.go`.

- **API** (`/api/v1/devsecops/vuln/...`, lettura con `element.read`, azioni con `element.write` o chi configura il catalogo):
  - `GET dashboard` (filtri `team`, `application`, `severity`, `q`, `mine`), `GET items` (più `stage` = open, un singolo stadio, closed, all; `due` = overdue, soon, nosla; `sort`, `limit`, `offset`), `GET items/{id}` con storia ed eccezioni;
  - `POST bulk` con `{"ids", "action": take|start|assign|resolve|reopen|false_positive, "team", "assignee", "note"}` (fino a 1000);
  - `GET|POST exceptions`, `PATCH exceptions/{id}` con `{"risk"}`, `POST exceptions/{id}/revoke`, `GET risks`;
  - configurazione (`architecture.write` o `tenant.admin`): `GET config`, `PUT config/policy`, `POST config/policy/preview`, `PUT config/exception-policy`.
- **Politica** (`vuln_policy`, una riga per tenant, i valori di partenza stanno nel codice): giorni per gravità × criticità, fattori DORA ed esposizione, campo dell'esposizione, giorni di «in scadenza», durata massima e preavviso delle eccezioni, passi di escalation. La scadenza è `prima rilevazione + giorni` (`findings.due_at`) e si ricalcola a ogni report e a ogni modifica della politica, con un'unica query (`dueCalc` in `vulnpolicy.go`) usata anche dall'anteprima.
- **Ciclo di vita:** `findings.stage` (open, taken, in_progress, exception, resolved, closed, false_positive) accanto a `findings.status`, che resta lo stato tecnico (presenza nei report). `refreshFindings` chiama `syncVulnLifecycle`: chiude da sole le sparite, riapre le ricomparse, ricalcola scadenze e team (`owner_team_id` dell'applicazione, salvo un team scelto a mano). Ogni passaggio finisce in `vuln_events`.
- **Eccezioni:** `vuln_exceptions` (una per applicazione), approvazione sul motore (`@vuln_exception`, soggetto `vuln_exception`, richiesta sull'elemento applicazione, soglie `risk_lte`/`risk_gt` sulla gravità più alta: 1 bassa … 4 critica, separazione dei compiti). Collegamento facoltativo a un rischio con la relazione `risk_of` (rischio → applicazione).
- **Job:** `dsoSvc.RunVulnerabilities` ogni 15 minuti (`serve.go`): ricalcolo, eccezioni scadute e avvisi, escalation. Notifiche `KindVulnerability` (`notify/vuln.go`) sull'applicazione.
- **Fatti:** `vuln_scadute`, `vuln_in_scadenza`, `vuln_eccezioni` sull'applicazione e sulla richiesta (applicazioni degli impatti).
- **Pagine:** `/devsecops/vulnerabilita`, `/configurazione/vulnerabilita`. Dati di esempio: `scripts/dev-demo-vulnerabilita.sh [url]`, richiamato da `dev-demo-data.sh`.

## DevSecOps, connettore GitHub e profili di pipeline (piano, passo 21)

Guida per chi configura in [devsecops-github.md](../devsecops-github.md); stato in [replica/devsecops.md](../../replica/devsecops.md) §7; migrazione `0025_connettori.sql`; test in `internal/itest/devsecops_github_test.go` e nei package `internal/modules/devsecops/connectors/...`.

- **Codice:**
  - `connectors/github/ghapi`: client REST con la sola libreria standard (JWT della App, token di installazione, paginazione, limiti di velocità, politica di rete);
  - `connectors/profile`: profili, percorsi, formati standard, file GitOps, senza rete;
  - `connectors/github`: integrazioni, scoperta e proposte, sincronizzazione, webhook, anteprima.
- **Collegamento:** gli oggetti vanno a `contract.Ingestor` (il modulo devsecops) passato da `serve.go` (`devsecopsGitHub`). In `api.New` senza `Options.GitHub` il servizio si crea senza job e usa `s.dso`. Il job parte con `ghSvc.Run(ctx, time.Minute)`.
- **API** (`tenant.admin`; le modifiche della configurazione solo da una sessione):
  - `/api/v1/devsecops/github/integrations[/{id}]` con `test`, `webhook-secret`, `repositories`, `discover`, `repositories/{repo}` (PUT `{"status","application_id","service_name"}`), `repositories/accept`, `sync[?full=true]`, `runs`, `preview`;
  - `/api/v1/devsecops/pipeline-profiles` con `import`, `validate`, `{id}`, `{id}/versions/{n}`, `{id}/export`;
  - webhook `POST /api/v1/devsecops/github/webhook/{id}` senza sessione, con la firma.
- **Interfaccia:** `/configurazione/integrazioni/github` (schede Integrazioni, Repository, Profili di pipeline, Sincronizzazioni).
- **Finto GitHub:** `regia dev fake-github [--addr 127.0.0.1:8122] [--enterprise] [--key file.pem] [--token …]` con un'organizzazione `esempio` inventata. Regia va avviato con `--dev`, che accetta `http://127.0.0.1`. Gli stessi dati (`fakegithub.Demo`) e le stesse fixture servono ai test.

## DevSecOps, gate di rilascio (piano, passo 21, fase 4)

- **Migrazione:** `0035_gate_rilascio.sql` (`gate_policies`, `gate_decisions`, `gate_overrides`, `gate_evidence_map`; aggiunge `devsecops.gate` al ruolo «Pipeline» dei tenant esistenti e il soggetto `gate_override` alle approvazioni).
- **Modulo:** `internal/modules/devsecops`, file `gate_policy.go` (regole, validazione, approvatori), `gate_eval.go` (valutazione e registrazione), `gate_override.go`, `gate_evidence.go` (evidenze GRC e mappatura), `gate_views.go` (configurazione, pannello, decisioni). Rotte in `internal/api/devsecops_gate.go`. `devsecops.Service.SetGate(grc, approvals)` collega conformità e approvazioni (`gateInit`); `PublicURL` rende assoluti i link.
- **Permesso:** `devsecops.gate` (ruolo «Pipeline»); non è di sola lettura perché ogni chiamata registra una decisione. Le letture usano `element.read`, la configurazione `architecture.write` o `tenant.admin`, chiedere un override `element.write`.
- **Regole:** per ambiente (`*` = tutti gli altri) con modalità `off`, `warn`, `block` e un elenco di regole (`requirements`, `vulnerabilities`, `change_approved`, `window`, `checks`, `condition`), ciascuna con livello `block` o `warn`. `requirements` riusa `requirements.GateCheck` (il gate dei requisiti senza audit, estratto da `ReleaseGate`); `condition` valuta il linguaggio dei processi su `engine.ElementFacts` (elenco dei fatti per l'editor: `engine.TypeFacts`).
- **Decisioni:** una riga per chiamata (le identiche entro cinque minuti valgono una), con esito, esito grezzo, modalità, motivi, audit `devsecops.gate` e voce `gate` nella storia dell'applicazione e della richiesta.
- **Override:** politica di approvazione `@gate_override` (un approvatore per passo, membro o ruolo sull'applicazione), richiesta sull'applicazione, separazione dei compiti. Approvato vale per applicazione, ambiente e versione fino a `expires_at`.
- **Evidenze GRC:** `gate_evidence_map` («check:<nome>» o «gate» → controllo, validità in giorni, solo produzione). Un rilascio con controlli `block` superati e una decisione superata (non per override) chiamano `grc.RecordAutomatic` (collettori `devsecops.release` e `devsecops.gate`, idempotenti).
- **Interfaccia:** Configurazione → Gate di rilascio (`web/src/pages/GateConfig.tsx`, anteprima «cosa succederebbe ora» con la bozza non salvata), scheda «Gate di rilascio» dell'applicazione e della richiesta e ultimi esiti in `/devsecops` (`GatePanel.tsx`), API in `web/src/gateApi.ts`.
- **Dati di esempio:** `scripts/dev-demo-gate.sh [url]`, richiamato da `dev-demo-data.sh`.
- **Prova:** `go test ./internal/itest -run GateRilascio`.

## DevSecOps, metriche DORA delle quattro chiavi (piano, passo 21, fase 5a)

Cruscotto `/devsecops/metriche` («Metriche di consegna», in Governo). Migrazione `0036_metriche_dora.sql`; codice in `internal/modules/devsecops/dora*.go`, rotte in `internal/api/devsecops_dora.go`, interfaccia in `web/src/pages/Dora.tsx` e `web/src/doraApi.ts`. Il disegno e le approssimazioni stanno in `replica/devsecops.md` §7.

- **Punti:** in `metric_points` (chiavi `dora.deploy_frequency`, `dora.lead_time_days`, `dora.change_failure_rate`, `dora.mttr_hours`, `dora.level`), con ambito (`tenant`, `team`, `application`, `asset`, `service`), soggetto e periodo (`week`, `month`, `quarter`, di calendario in UTC). La chiave primaria si allarga con `scope`, `subject_id` e `period`.
- **Ricalcolo:** job `RunDora` ogni ora (periodo in corso e precedente, tutta la finestra una volta al giorno e all'avvio) e `POST /devsecops/dora/recompute` (catalogo o amministratore) per tutta la finestra: 26 settimane, 12 mesi, 8 trimestri.
- **API:** `GET /devsecops/dora?period=&scope=&subject=&compare=` (cruscotto: numeri dell'ultimo periodo concluso, serie, confronto), `GET /devsecops/dora/drilldown?period=&scope=&subject=&at=` (rilasci e incidenti, ricalcolati dal dato grezzo), `GET|PUT /devsecops/dora/settings`, `POST /devsecops/dora/preview` (effetto delle impostazioni proposte sui livelli, senza salvare).
- **Impostazioni** (`dora_settings`): finestra del fallimento in ore, parole di hotfix e rollback, inizio del lead time, soglie dei livelli, giorni dei fatti. Si modificano da «Soglie e criteri» nella pagina, con l'anteprima.
- **Fatti** dell'applicazione: `frequenza_rilascio`, `lead_time_giorni`, `change_failure_rate`, `mttr_ore`, sugli ultimi `fact_days` giorni.
- **Riquadro** `DoraCard` nella scheda «Servizi e rilasci» dell'applicazione, in «Produzione» dell'asset e nel team (Architettura → Team → «Consegna»).
- **Dati di esempio:** `scripts/dev-demo-dora.sh [url]`, richiamato da `dev-demo-data.sh` (54 rilasci in più su sei settimane, un deploy annullato, un ritorno alla versione precedente, tre incidenti collegati).
- **Prova:** `go test ./internal/itest -run DevsecopsDora`.

## DevSecOps, fase 3: politica della pipeline e adozione guidata (piano, passo 21)

Disegno e stato in [replica/devsecops.md](../../replica/devsecops.md) §3.3, §3.4 e §7; guida alla GitHub App e alle pull request in [devsecops-github.md](../devsecops-github.md) §8. Migrazione `0034_politica_pipeline.sql`.

- **Politica** (`element.read` per leggere, `architecture.write` o `tenant.admin` per modificare): `GET|PUT /api/v1/devsecops/policy`, `POST /policy/impact` (anteprima, non salva), `GET /policy/diff?from=&to=`, `GET /policy/versions/{n}`.
- **Aderenza:** `GET /api/v1/devsecops/adherence` (portafoglio) e `GET /api/v1/devsecops/elements/{chiave}/pipeline` (applicazione o asset, con l'elenco «cosa manca» e le pull request).
- **Adozione** (stessi permessi della modifica): `GET /applications/{id}/adoption`, `GET /adoption/repositories?q=`, `POST /adoption/preview|download|pull-request`, `POST /applications/{id}/adoption/sync`.
- **Fatti** dell'applicazione: `aderenza_pipeline`, `controlli_mancanti`, `controlli_mancanti_elenco`.
- **Pagine:** `/configurazione/devsecops/politica` (matrice, versioni, adozione), `/devsecops/aderenza`, sezione «Pipeline e politica» della scheda Produzione.
- **Dati di esempio:** `scripts/dev-demo-politica.sh [url]`, richiamato da `dev-demo-data.sh`. Per provare anche le pull request: `./bin/regia dev fake-github --enterprise --write` e `FINTO_GH_URL=http://127.0.0.1:8122 scripts/dev-demo-politica.sh`.
- **Prova:** `go test ./internal/itest -run PipelinePolicy`; il finto GitHub ha le scritture (`internal/devtools/fakegithub/pulls.go`), provate in `go test ./internal/devtools/...`.

## DevSecOps, connettore GitLab (piano, passo 21, fase 5b)

Guida per chi configura in [devsecops-gitlab.md](../devsecops-gitlab.md); stato in [replica/devsecops.md](../../replica/devsecops.md) §7. Nessuna migrazione: usa le tabelle generiche dei connettori (`0025_connettori.sql`) e `integrations` con kind `gitlab`. Test in `internal/itest/devsecops_gitlab_test.go` e nel package `internal/modules/devsecops/connectors/gitlab`.

- **Codice:** `connectors/gitlab` (integrazioni, progetti, sincronizzazione, webhook, anteprima, preset, artefatti) e `connectors/gitlab/glapi` (client REST v4 con la sola libreria standard). Riusa, senza modificarli, `ghapi.Policy` e `ghapi.NewHTTPClient` (SSRF e CA interna), `ghconn.Suggest`, `ghconn.ValidateDefinition` e i tipi di anteprima; i profili sono quelli di tutti i connettori.
- **Collegamento:** gli oggetti vanno a `contract.Ingestor` passato da `serve.go` (`devsecopsGitLab`). In `api.New` senza `Options.GitLab` il servizio si crea senza job. Il job parte con `glSvc.Run(ctx, time.Minute)`.
- **API:** `/api/v1/devsecops/gitlab/integrations[/{id}]` con `test`, `webhook-secret`, `projects`, `discover`, `projects/{project}` (PUT), `projects/accept`, `sync[?full=true]`, `runs`, `preview`; `presets`; webhook `POST /api/v1/devsecops/gitlab/webhook/{id}` senza sessione, con `X-Gitlab-Token`.
- **Interfaccia:** `/configurazione/integrazioni/gitlab` (schede Integrazioni, Progetti, Profili di pipeline, Sincronizzazioni), con il percorso guidato in cinque passi.
- **Finto GitLab:** `regia dev fake-gitlab [--addr 127.0.0.1:8174] [--token …]` con un gruppo `esempio` inventato; con `--dev`. Gli stessi dati (`fakegitlab.Demo`) servono ai test.
