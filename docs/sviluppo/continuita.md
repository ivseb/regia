# Continuità operativa

BIA per capability, RTO/RPO richiesti contro dichiarati, incoerenze lungo il grafo, piani di continuità e test di resilienza. Serve alla continuità chiesta da DORA (capo II, art. 11-12; capo IV, art. 24-25) e da ISO 22301, partendo dal grafo che c'è già: capability → applicazioni → dipendenze → fornitori ICT. Guida per chi lo usa: [docs/continuita.md](../continuita.md).

Codice:
- `internal/modules/continuity`: modello, incoerenze, BIA, test, fatti, problemi, evidenze;
- `internal/api/continuity.go`;
- migrazione `0110_continuita.sql`;
- pagine in `web/src/pages/continuita/`, client `web/src/continuityApi.ts`, testi `web/src/i18n/{it,en}/continuita.ts`.

## Modello (motore unico + estensioni)
- **BIA:** elemento di tipo `bia` nello spazio `ARC`, una per capability, con estensione `ext_bia`.
  - **Processo di partenza** (`bia_standard`): Bozza → In revisione → Approvata, più Archiviata.
    - «Invia in revisione» e «Approva» hanno il gate `bia_completa`: la griglia è piena e RTO e RPO ci sono.
    - «Approva» è un passaggio di sistema: lo esegue `POST /continuity/bia/{rif}/approve`, che chiede `continuity.approve`.
    - «Aggiorna» riporta in Bozza una BIA approvata.
  - **Approvazione:** entrare in «Approvata» (aggancio `OnTransition`) congela la versione in `bia_versions`: istantanea, autore, data e versione della configurazione. Il valore in vigore è sempre l'ultima versione approvata. Una bozza successiva non lo cambia.
  - **Proposta:** l'MTPD proposto è il primo orizzonte in cui una categoria raggiunge il suo livello «intollerabile». L'RTO proposto è l'orizzonte precedente. La funzione si propone critica se l'MTPD sta entro `critical_mtpd_minutes`. La persona conferma o corregge, e la proposta resta visibile.
- **RTO/RPO dichiarati dell'applicazione:** `ext_application.rto_minutes` e `rpo_minutes`, che esistevano già.
  - **Il richiesto è calcolato, non salvato:** il minimo fra le versioni approvate delle BIA delle capability che l'applicazione supporta (relazione `supports`), comprese quelle che le contengono (`parent_id`). La risposta dice da quale BIA viene.
  - **Funzione critica richiesta:** almeno una di quelle BIA è critica.
- **Fornitore:** `ict_contracts.rto_minutes` e `rpo_minutes`, l'impegno contrattuale. Vale il valore peggiore fra i contratti attivi (fine nulla o futura).
- **Piano di continuità:** pagina della Knowledge resa documento controllato, collegata a capability o applicazione con la relazione `continuity_plan_for` (pagina → elemento). È coperta se la pagina ha una versione approvata (`kb_pages.approved_version`).
- **Test di resilienza:** elemento `resilience_test` nello spazio `ARC`, con estensione `ext_resilience_test`.
  - **Oggetto:** un'applicazione o una capability.
  - **Processo di partenza** (`resilience_test_standard`): Pianificato → In esecuzione → Eseguito → Chiuso, più Annullato. Il gate `esito_test` chiede l'esito per arrivare a «Eseguito».
  - **Entrando in «Eseguito»** nasce l'evidenza automatica `grc.RecordAutomatic`:
    - collettore `continuity.test`, riferimento esterno uguale alla chiave del test;
    - va sull'applicazione (per una capability, sul tenant);
    - si collega ai controlli iscritti al collettore (`ext_control.collectors`).

    Senza modulo GRC configurato il test si chiude lo stesso, e `evidence_note` dice perché manca l'evidenza.
- **Configurazione del tenant, versionata:** `continuity_settings` (config jsonb e `version`) e `continuity_settings_versions` (storico).
  - **Pacchetto di partenza:**
    - categorie economico, normativo, reputazionale, clienti, su 5 livelli, con intollerabile = 4;
    - orizzonti 1 h, 4 h, 1 g, 3 g, 1 settimana;
    - tipi di test: tabletop, failover, ripristino da backup, scenario;
    - `test_max_age_days` 365, `bia_review_months` 12, `critical_mtpd_minutes` 4320.
- **Permessi:** `continuity.write` (BIA, test, piani), `continuity.approve` (approva la BIA), `continuity.admin` (configurazione e preparazione del modulo). Il ruolo di sistema è `continuity_manager` (Responsabile della continuità).
- **Modulo `continuity`:** prefisso API `continuity`, con il modulo spento le API rispondono 404.
  - È il primo modulo **spento di default** (`defaultOff` in `internal/api/modules.go`): senza una riga in `tenant_modules` è spento, sia per le organizzazioni esistenti sia per le nuove.
  - Si accende in Configurazione → Moduli. Il modello di avvio banca/assicurazione lo accende già.
- **Preparazione:** accendere il modulo (`PUT /tenant/modules`) lo prepara anche: tipi, processi, relazione, ruolo e configurazione. `POST /continuity/setup` e `regia tenant provision` fanno lo stesso e servono come riserva.

## Incoerenze (calcolate a ogni lettura)
Codici (`Issue.code`):

| code | quando | gravità |
|---|---|---|
| `rto_over_required` | RTO dichiarato > richiesto | alta |
| `rpo_over_required` | RPO dichiarato > richiesto | alta |
| `rto_missing` | applicazione con un richiesto ma senza RTO dichiarato | alta |
| `dependency_slower` | dipendenza diretta (applicazione o fornitore) con RTO > RTO effettivo dell'applicazione (dichiarato, o richiesto se manca) | alta |
| `dependency_rto_missing` | dipendenza diretta senza RTO, quando l'applicazione ha un RTO effettivo | media |
| `critical_not_declared` | BIA critica ma `supports_critical_function` spento | media |
| `plan_missing` | funzione critica (capability con BIA critica, o applicazione critica) senza piano approvato | alta |
| `test_missing` | funzione critica senza test chiuso entro `test_max_age_days` | alta |
| `test_failed` | ultimo test eseguito «non superato» | alta |
| `bia_missing` | capability con applicazioni critiche e nessuna BIA approvata | media |
| `bia_review_due` | BIA approvata più vecchia di `bia_review_months` | media |

Il riferimento del problema (`Issue.ref`) è `<code>:<id oggetto>[:<id altro>]`. Va in `problems` con il tipo `continuity`: `ProblemActions kind="continuity" ref=…`. I rilievi di un test hanno ref `finding:<id test>:<indice>`.

Fatti per le condizioni:
- **Applicazione:**
  - `rto_richiesto_minuti`, `rpo_richiesto_minuti`;
  - `rto_incoerente` (sì/no);
  - `continuita_problemi`;
  - `ultimo_test_giorni`;
  - `piano_continuita` (sì/no);
  - `funzione_critica_bia` (sì/no).
- **Capability:** `bia_critica`, `mtpd_minuti`, `rto_richiesto_minuti`, `bia_giorni`.

## API (`/api/v1/continuity`)
Tutte rispondono JSON. Gli errori usano il Problem standard. I minuti sono interi o `null`, le date `AAAA-MM-GG`.

Tipi comuni:
```ts
type Ref = { id: string; key: string; title: string };
type Config = {
  categories: { key: string; name: string; intolerable: number; levels: { value: number; label: string }[] }[];
  horizons: { key: string; name: string; minutes: number }[];
  test_types: { key: string; name: string; description: string }[];
  test_max_age_days: number; bia_review_months: number; critical_mtpd_minutes: number;
};
type Issue = {
  code: string; ref: string; severity: "high" | "medium";
  application: Ref | null; capability: Ref | null;
  other: (Ref & { kind: "application" | "provider" }) | null;   // dipendenza o fornitore
  declared: number | null; required: number | null; other_value: number | null; // minuti
  days: number | null;            // giorni dall'ultimo test o dalla BIA
  source: Ref | null;             // BIA da cui viene il richiesto
  team: { id: string; name: string } | null;
  action: { kind: "set_rto" | "set_rpo" | "set_critical" | "set_provider_rto" | "write_plan" | "plan_test" | "start_bia" | "review_bia" | "open_test"; target: string };
};
type Can = { write: boolean; approve: boolean; admin: boolean };
```

- `GET /settings` → `{ ready, version, config: Config, updated_by: string|null, updated_at: string|null, can: Can }`
- `PUT /settings` `{ version, config }` → stessa forma; 409 se `version` è cambiata (`continuity.admin`).
- `GET /settings/versions` → `{ items: [{ version, by: string|null, at, config }] }`
- `POST /setup` → `{ ready: true }`: prepara il modulo nel tenant (`continuity.admin` o `tenant.admin`).
- `GET /overview?capability=<rif>&team=<id>&code=<code>` → `{ ready, summary: { issues, high, critical_functions, bia_approved, bia_missing, tests_due, plans_missing }, issues: Issue[], can: Can }`
- `GET /bia` → `{ items: [{ capability: Ref & { code: string|null; parent_id: string|null; depth: number }, bia: BiaBrief|null, applications: number, critical_applications: number }], can: Can }`
  - `BiaBrief = { id, key, state, state_category, critical: boolean|null, mtpd_minutes, rto_minutes, rpo_minutes, approved_version: number|null, approved_at: string|null, review_due: string|null }`. I valori sono quelli approvati in vigore; senza approvazione, quelli della bozza.
- `POST /bia` `{ capability: <rif> }` → `BiaView` (201); 409 `validation.continuity.bia_exists` se la capability ha già una BIA non archiviata.
- `GET /bia/{rif}` → `BiaView`:
```ts
type BiaView = { id, key, title, state, state_category, capability: Ref,
  impacts: Record<string /*categoria*/, Record<string /*orizzonte*/, number>>,
  critical: boolean | null, critical_reason: string, mtpd_minutes, rto_minutes, rpo_minutes, notes: string,
  proposal: { mtpd_minutes: number|null; rto_minutes: number|null; critical: boolean; category: string|null; horizon: string|null } | null,
  settings_version: number, config: Config, config_changed: boolean,
  approved: { version, at, by: string|null, mtpd_minutes, rto_minutes, rpo_minutes, critical: boolean } | null,
  versions: { version, at, by: string|null, mtpd_minutes, rto_minutes, rpo_minutes, critical: boolean, settings_version: number }[],
  applications: (Ref & { rto_minutes: number|null; rpo_minutes: number|null; rto_ok: boolean|null; rpo_ok: boolean|null })[],
  can: { edit: boolean; approve: boolean } }
```
- `PUT /bia/{rif}` (solo in Bozza) `{ impacts?, critical?, critical_reason?, mtpd_minutes?, rto_minutes?, rpo_minutes?, notes? }` → `BiaView`. Si possono mandare solo alcuni campi; `null` cancella un valore.
- `POST /bia/{rif}/approve` `{ comment? }` → `BiaView` (`continuity.approve`).
- `GET /applications/{rif}` → `{ application: Ref, declared: { rto, rpo }, required: { rto, rpo, source: Ref|null }, critical_declared, critical_required, dependencies: (Ref & { kind: "application"|"provider"; rto_minutes: number|null; slower: boolean })[], plans: PlanRef[], last_test: TestBrief|null, issues: Issue[], can: Can }`
- `GET /capabilities/{rif}` → `{ capability: Ref, bia: BiaBrief|null, applications: (Ref & { rto_minutes; rpo_minutes })[], plans: PlanRef[], last_test: TestBrief|null, issues: Issue[], can: Can }`
- `GET /graph` → `{ nodes: Record<id, { rto_declared: number|null; rto_required: number|null; critical: boolean }>, edges: { dependent: string; dependency: string; incoherent: boolean }[] }`. Gli id sono quelli di `GET /architecture/graph`; sono compresi i fornitori collegati con `provided_by`.
- **Piani:**
  - `GET /plans` → `{ items: PlanRef[] }`, con `PlanRef = { page: Ref, target: Ref & { type: string }, approved: boolean, approved_version: number|null }`;
  - `POST /plans` `{ page: <rif pagina>, target: <rif capability o applicazione> }` → `PlanRef` (201);
  - `DELETE /plans?page=&target=` → 204.
- **Test:**
  - `GET /tests?year=AAAA` → `{ year, rows: [{ subject: Ref & { kind: "application"|"capability" }, critical: boolean, required_rto: number|null, tests: TestBrief[], last_done: TestBrief|null, planned_in_year: boolean }], types: Config["test_types"], can: Can }`. Le righe sono le funzioni critiche (capability con BIA critica e applicazioni critiche) più tutto ciò che ha un test nell'anno.
  - `TestBrief = { id, key, title, state, state_category, type: string, planned_on: string|null, executed_on: string|null, outcome: "passed"|"passed_with_findings"|"failed"|null }`
  - `POST /tests` `{ subject: <rif>, type, planned_on, title? }` → `TestView` (201);
  - `GET /tests/{rif}` → `TestView = TestBrief & { subject: Ref & { kind }, type_name: string, rto_measured_minutes, rpo_measured_minutes, notes: string, findings: { text: string; ref: string; work: { key: string; title: string; state: string } | null }[], required: { rto, rpo }, declared: { rto, rpo }, rto_ok: boolean|null, rpo_ok: boolean|null, evidence: { key: string } | null, evidence_note: string, can: { edit: boolean } }`;
  - `PUT /tests/{rif}` `{ type?, planned_on?, executed_on?, rto_measured_minutes?, rpo_measured_minutes?, outcome?, findings?: string[], notes? }` → `TestView`.
- **Contratti dei fornitori:** `rto_minutes` e `rpo_minutes` si aggiungono all'input e alla vista dei contratti (`/api/v1/ict/...contracts`).

## Interfaccia
- **Architettura → Continuità** (`/architettura/continuita`):
  - riepilogo, poi il «cosa manca» con le incoerenze raggruppate per gravità;
  - ogni riga ha la frase, l'azione diretta e `ProblemActions` per creare il lavoro nel team;
  - si può filtrare per capability, team e tipo.
- **BIA:**
  - elenco `/architettura/continuita/bia`;
  - la BIA si apre in `/e/<chiave>` con la sezione «Impatto nel tempo»: griglia orizzonti × categorie, proposta, valori, versioni, applicazioni.
- **Test:**
  - piano annuale `/architettura/continuita/test?anno=`, da cui «Pianifica» crea un test dalla riga;
  - il test si apre in `/e/<chiave>` con la sezione «Esito».
- **Mappa:** con l'interruttore «Continuità» ogni nodo mostra l'RTO dichiarato e il richiesto, e gli archi incoerenti diventano rossi.
- **Blocco «Continuità»** nella sezione Architettura dell'applicazione e nella pagina della capability.
- **Campi RTO/RPO** nel contratto del fornitore.
- **Configurazione → Continuità** (`/configurazione/continuita`): categorie e livelli, orizzonti, soglie, tipi di test, storico delle versioni, controlli GRC iscritti al collettore `continuity.test`.

## Test e dati di esempio
- `internal/modules/continuity/continuity_test.go`: proposta della BIA, validazione della configurazione, incoerenze sul modello in memoria;
- `internal/itest/continuita_test.go`: preparazione, BIA e versione congelata, richiesto ereditato, incoerenze, problemi e lavoro, piani, test con evidenza e rilievi, fatti, configurazione, permessi, modulo spento;
- `web/test/continuita.test.ts` (`node --test`): minuti, proposta lato client, frasi delle incoerenze;
- e2e `web/e2e/continuita.spec.ts`, anche a 375 px;
- `scripts/dev-demo-continuita.sh` (lo richiama `dev-demo-data.sh`): BIA approvata dei bonifici, RTO dichiarati con una catena incoerente, un test pianificato.

## Limiti noti (primo giro)
- **Incoerenze:** solo sulle dipendenze dirette. La catena completa si vede sulla mappa.
- **Fatti:** quelli dell'applicazione caricano tutto il modello della continuità. Su tenant molto grandi un conteggio delle condizioni su tutte le applicazioni costa.
- **Controlli iscritti:** `GET /grc/controls` non restituisce `collectors`, quindi la configurazione legge il dettaglio dei controlli, fino a 200.
- **Fuori da questo giro:**
  - l'AI che propone la griglia;
  - un controllo GRC di partenza già mappato sugli articoli DORA;
  - il catalogo ISO 22301;
  - l'esportazione per l'autorità.
