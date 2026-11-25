# API di push DevSecOps

Come una pipeline qualunque (Jenkins, GitLab CI, Azure DevOps, GitHub Actions, script fatti in casa) invia a Regia i suoi risultati. Regia **non** esegue build né scansioni: riceve ciò che la pipeline produce già e lo collega ad applicazioni, richieste e requisiti. Disegno in [replica/devsecops.md](../replica/devsecops.md).

## 1. Il token

1. Crea un membro tecnico (per esempio `ci@azienda.it`) con il ruolo **Pipeline** (i permessi `devsecops.write` e `devsecops.gate`), oppure usa il tuo profilo.
2. Da **Profilo → Token API** crea un token con il permesso «Inviare i dati delle pipeline» (`devsecops.write`) e, se la pipeline chiede il gate di rilascio, anche «Chiedere il gate di rilascio» (`devsecops.gate`). Durata massima 365 giorni.
3. Salvalo nei segreti della piattaforma di CI, per esempio `REGIA_TOKEN`, insieme all'indirizzo `REGIA_URL`.

Il token vale solo nel tenant in cui è stato creato. Ogni invio finisce nel registro di audit con l'id del token.

## 2. Regole comuni

- `POST` con `Content-Type: application/json` e `Authorization: Bearer myt_…`.
- Ogni corpo ha `"format": "regia.devsecops/v1"`; gli altri campi sono quelli del contratto (`internal/modules/devsecops/contract/contract.go`). I campi sconosciuti si ignorano: una pipeline più nuova non si rompe su un Regia più vecchio.
- Il servizio si indica sempre con `service`: `{"name", "repository", "provider", "url", "application"}`. `name` è univoco nel tenant; `application` è il **codice** dell'applicazione nel catalogo (es. `API-CLI`) o la sua chiave (es. `ARC-5`). Basta indicarlo una volta: poi `{"name": "clienti-api"}` è sufficiente. Un'associazione fatta da Configurazione → DevSecOps prevale su quella dichiarata.
- Limite: 32 MB per invio, anche compresso con `Content-Encoding: gzip` (64 MB decompressi).
- Le date sono RFC 3339 (`2026-11-24T10:00:00Z`). Ometterle vuol dire «adesso», ma rende l'invio non ripetibile: meglio indicarle.
- **Idempotenza:** lo stesso oggetto inviato due volte non crea duplicati. Risposta `202` se l'invio ha cambiato qualcosa, `200` se era già stato ricevuto identico. Il corpo è un riepilogo:

```json
{"format": "regia.devsecops/v1", "result": {"kind": "release", "id": "…", "service": "clienti-api", "environment": "prod",
 "production": true, "created": true, "changed": true, "change": "CHG0045120", "request": "DEM-12",
 "verified_requirements": [{"requirement": "DEM-31", "impact": "DEM-9", "check": "secret_scan"}], "summary": "clienti-api 5.3.1 in prod"}}
```

- Errori in formato RFC 9457 (`application/problem+json`): `401` token mancante o non valido, `403` permesso mancante (o token di sola lettura), `422` dati non validi (`field` indica quale), `413` invio troppo grande.

## 3. Gli invii

### Servizio — `POST /api/v1/devsecops/services`

Facoltativo: i servizi si creano da soli al primo dato. Serve per censire un repository prima che arrivi sulla pipeline.

```bash
curl -fsS -X POST "$REGIA_URL/api/v1/devsecops/services" \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $REGIA_TOKEN" \
  -d '{"format":"regia.devsecops/v1","name":"clienti-api","repository":"digitale/clienti-api","provider":"github",
       "url":"https://git.azienda.it/digitale/clienti-api","application":"API-CLI"}'
```

### Esito dei controlli — `POST /api/v1/devsecops/check-runs`

Uno per commit ed esecuzione (`run_id`). `mode`: `off`, `warn`, `block`; `outcome`: `passed`, `failed`, `skipped`, `not_run`. Un controllo bloccante fallito ma saltato da una persona ha `skip`.

```bash
curl -fsS -X POST "$REGIA_URL/api/v1/devsecops/check-runs" \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $REGIA_TOKEN" \
  -d "{\"format\":\"regia.devsecops/v1\",\"service\":{\"name\":\"clienti-api\",\"application\":\"API-CLI\"},
       \"commit\":\"$GIT_COMMIT\",\"ref\":\"main\",\"run_id\":\"$BUILD_ID\",\"url\":\"$BUILD_URL\",\"verdict\":\"passed\",
       \"at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",
       \"checks\":[{\"name\":\"test\",\"mode\":\"block\",\"outcome\":\"passed\"},
                   {\"name\":\"secrets\",\"mode\":\"block\",\"outcome\":\"passed\"},
                   {\"name\":\"cves\",\"mode\":\"block\",\"outcome\":\"passed\"},
                   {\"name\":\"image\",\"mode\":\"block\",\"outcome\":\"failed\",\"skip\":{\"by\":\"m.rossi\",\"reason\":\"immagine base senza correzione\"}}]}"
```

Se `verdict` manca, si calcola: `failed` se un controllo `block` è fallito senza eccezione.

### SBOM — `POST /api/v1/devsecops/sboms`

Il documento CycloneDX (JSON, 1.4-1.6) o SPDX (JSON, 2.2-2.3) va in `document` così com'è; il formato si riconosce da solo (o `"sbom_format": "cyclonedx" | "spdx"`). Si tengono purl, nome, versione e licenza dei componenti, non il documento. Senza `version` serve il `digest` di un artefatto già rilasciato.

```bash
syft "$IMAGE" -o cyclonedx-json > sbom.cdx.json
jq -n --slurpfile doc sbom.cdx.json --arg v "$VERSION" --arg d "$DIGEST" \
  '{format:"regia.devsecops/v1", service:{name:"clienti-api"}, version:$v, digest:$d, document:$doc[0]}' \
| gzip | curl -fsS -X POST "$REGIA_URL/api/v1/devsecops/sboms" \
  -H 'Content-Type: application/json' -H 'Content-Encoding: gzip' -H "Authorization: Bearer $REGIA_TOKEN" --data-binary @-
```

### Vulnerabilità — `POST /api/v1/devsecops/vuln-reports`

Due modi:
- `report`: l'output JSON di **grype** (`-o json`) o di **trivy** (`--format json`) così com'è;
- `vulnerabilities`: l'elenco del contratto (`id`, `aliases`, `package`, `purl`, `version`, `fixed_in`, `severity`, `layer`: `image` o `app`).

Una vulnerabilità vale una volta per servizio e componente, qualunque sia lo scanner; un GHSA con una CVE correlata vale come la CVE. Il controllo giornaliero della versione in produzione si invia allo stesso modo: diventa «CVE di oggi».

```bash
grype "$IMAGE" -o json > grype.json
jq -n --slurpfile r grype.json --arg v "$VERSION" --arg d "$DIGEST" \
  '{format:"regia.devsecops/v1", service:{name:"clienti-api"}, version:$v, digest:$d, scanner:"grype",
    scanned_at:(now|todate), report:$r[0]}' \
| curl -fsS -X POST "$REGIA_URL/api/v1/devsecops/vuln-reports" \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $REGIA_TOKEN" --data-binary @-
```

### Rilascio — `POST /api/v1/devsecops/releases`

Il registro del rilascio in un ambiente (di solito la produzione). Uno per servizio, ambiente e versione: lo stesso rilascio inviato di nuovo aggiorna la riga. Crea anche il deploy nell'ambiente.

- `change_number`: il numero di change. Si collega alla richiesta di Regia se coincide con una chiave (`DEM-12`) o con il campo della richiesta scelto in Configurazione → DevSecOps.
- `change_aliases`: numeri provvisori dello stesso change; il change con quel numero prende il numero definitivo e il provvisorio resta come alias.
- `cves_at_release`: i conteggi al momento del rilascio (altrimenti valgono quelli del primo report della versione).
- `checks`: gli esiti dei controlli del commit rilasciato (altrimenti l'ultimo esito ricevuto per `commit`).

```bash
curl -fsS -X POST "$REGIA_URL/api/v1/devsecops/releases" \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $REGIA_TOKEN" \
  -d "{\"format\":\"regia.devsecops/v1\",\"service\":{\"name\":\"clienti-api\",\"application\":\"API-CLI\"},
       \"version\":\"$VERSION\",\"commit\":\"$GIT_COMMIT\",\"image\":\"$IMAGE\",\"digest\":\"$DIGEST\",\"environment\":\"prod\",
       \"change_number\":\"$CHANGE\",\"requested_by\":\"$REQUESTER\",\"approved_by\":\"$APPROVER\",
       \"approved_at\":\"$APPROVED_AT\",\"release_url\":\"$RELEASE_URL\",\"at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",
       \"cves_at_release\":{\"critical\":0,\"high\":1,\"medium\":4,\"low\":2,\"fixable_critical\":0,\"fixable_high\":0}}"
```

**Requisiti verificati dall'evidenza.** Un requisito con criterio «da evidenza» e una chiave di controllo si verifica da solo quando arriva un rilascio dell'applicazione con quel controllo in `block` superato senza eccezioni. La chiave può essere il nome del controllo (`secrets`), `release.<nome>` (`release.secrets`) o una chiave del catalogo di partenza: `secret_scan` (= `secrets`) e `sca_block` (= `cves`, `sca`, `dependencies`). Per i controlli delle vulnerabilità (`cves`, `sca`, `dependencies`, `vulnerabilities`, `image`) serve anche zero CVE critiche o alte correggibili in `cves_at_release`, se dichiarate. Se il change è collegato a una richiesta si verificano gli impatti di quella richiesta, altrimenti tutti gli impatti aperti dell'applicazione. Le verifiche sono firmate (metodo `automatic`, raccoglitore `devsecops`) e il riepilogo le elenca in `verified_requirements`.

### Deploy — `POST /api/v1/devsecops/deployments`

Una versione in esecuzione (o tentata) in un ambiente. Con `external_ref` (es. l'id della sincronizzazione di Argo CD) gli aggiornamenti di stato dello stesso deploy aggiornano la riga; senza, il deploy è identificato da servizio, ambiente, versione e istante. `status`: `running`, `succeeded`, `failed`, `rolled_back`.

```bash
curl -fsS -X POST "$REGIA_URL/api/v1/devsecops/deployments" \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $REGIA_TOKEN" \
  -d "{\"format\":\"regia.devsecops/v1\",\"service\":{\"name\":\"clienti-api\"},\"environment\":\"staging\",
       \"version\":\"$VERSION\",\"status\":\"succeeded\",\"source\":\"gitops\",\"external_ref\":\"argocd/clienti-api-staging/$SYNC_ID\",
       \"at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
```

Gli ambienti si creano al primo utilizzo; `prod` e `production` sono di produzione (configurabile). La versione in produzione di un servizio è l'ultimo deploy riuscito (o in corso) in un ambiente di produzione.

## 4. Uno step di pipeline completo

Al rilascio in produzione: SBOM, scansione, rilascio. Con `set -e` un errore di Regia ferma lo step: se Regia non deve fermare il rilascio, aggiungi `|| echo "Regia non raggiungibile"` alle chiamate.

```bash
set -euo pipefail
syft "$IMAGE" -o cyclonedx-json > sbom.cdx.json
grype "sbom:sbom.cdx.json" -o json > grype.json
post() { curl -fsS -X POST "$REGIA_URL/api/v1/devsecops/$1" -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $REGIA_TOKEN" --data-binary @-; }
svc='{"name":"clienti-api","application":"API-CLI"}'
jq -n --argjson s "$svc" --slurpfile d sbom.cdx.json --arg v "$VERSION" \
  '{format:"regia.devsecops/v1", service:$s, version:$v, document:$d[0]}' | post sboms
jq -n --argjson s "$svc" --slurpfile r grype.json --arg v "$VERSION" \
  '{format:"regia.devsecops/v1", service:$s, version:$v, scanner:"grype", scanned_at:(now|todate), report:$r[0]}' | post vuln-reports
jq -n --argjson s "$svc" --arg v "$VERSION" --arg c "$GIT_COMMIT" --arg ch "$CHANGE" \
  '{format:"regia.devsecops/v1", service:$s, version:$v, commit:$c, environment:"prod", change_number:$ch, at:(now|todate)}' | post releases
```

Prima del rilascio la pipeline chiede il via libera al **gate di rilascio** (§ 4 bis). Esiste anche l'interrogazione più semplice dei soli requisiti (`GET /api/v1/release-gate?application=API-CLI&request=DEM-12`, permesso `release.read`): vedi [sviluppo/requisiti.md](sviluppo/requisiti.md), «Requisiti, verifica, deroghe e gate di rilascio».

## 4 bis. Il gate di rilascio

Prima del deploy la pipeline chiede a Regia se può rilasciare. Regia valuta le **regole dell'ambiente** (Configurazione → Gate di rilascio), registra la decisione (esito, motivi, audit, storia dell'applicazione e della richiesta) e risponde `pass`, `warn` o `fail` con i motivi leggibili e i link alle pagine.

- **Permesso:** `devsecops.gate`, solo nel ruolo di sistema «Pipeline» (e in un token creato apposta). Un token con il solo `devsecops.write` riceve `403`.
- **Domanda:** `POST /api/v1/devsecops/gate`, JSON con `"format": "regia.devsecops/v1"`:

| campo | significato |
|---|---|
| `application` | codice del catalogo (`API-CLI`) o chiave (`ARC-3`); si può omettere se c'è `service` già associato |
| `service` | nome del servizio (facoltativo): limita le regole sulle CVE e sui controlli a quel servizio |
| `environment` | ambiente di destinazione (`prod`, `staging`…): **obbligatorio** |
| `change` | numero di change (`CHG0045120`) o chiave della richiesta; si collega alla richiesta come per i rilasci |
| `request` | chiave della richiesta di Regia (`DEM-12`), se la conosci già |
| `version`, `commit` | versione e commit che si rilasciano: scelgono la scansione e i controlli da valutare |

- **Risposta:** `200` con `pass` o `warn`, `409` con `fail` (stesso corpo: `curl -f` ferma lo step da solo). `422` se manca qualcosa o l'applicazione non esiste, `403` senza permesso.

```json
{"format": "regia.devsecops/v1", "result": "fail", "raw_result": "fail", "mode": "block", "environment": "prod",
 "application": {"key": "ARC-5", "code": "PAY", "title": "Modulo pagamenti", "url": "https://regia.azienda.it/e/ARC-5?scheda=gate"},
 "request": {"key": "DEM-9", "title": "Portale clienti B2B", "url": "https://regia.azienda.it/e/DEM-9?scheda=gate"},
 "version": "2.5.1", "summary": "Il rilascio è bloccato: 2 regole non soddisfatte.",
 "reasons": [
   {"rule": "3f9a1c2b", "kind": "vulnerabilities", "label": "Vulnerabilità", "status": "fail",
    "message": "pagamenti-gateway 2.5.1: 1 CVE critiche o alte con correzione disponibile.", "url": "https://regia.azienda.it/e/ARC-5?scheda=produzione"},
   {"rule": "a07d55e1", "kind": "checks", "label": "Controlli della pipeline", "status": "pass", "message": "pagamenti-gateway (0000000038ef): superati secrets, test."}],
 "decision_id": "01a2…", "url": "https://regia.azienda.it/e/ARC-5?scheda=gate", "checked_at": "2026-11-24T10:00:00Z"}
```

I link sono assoluti se l'organizzazione ha un indirizzo pubblico (`REGIA_BASE_URL`), altrimenti percorsi (`/e/ARC-5?scheda=gate`).

**Esiti.** Ogni regola dà `pass`, `warn` (regola con livello «avvisa» non soddisfatta) o `fail` (regola che blocca non soddisfatta). L'esito è il peggiore. Per l'ambiente la modalità può essere `warn` (un `fail` diventa `warn`: non blocca mai, ideale per cominciare), `block` oppure `off`; un ambiente senza regole passa con `mode: "none"`. `raw_result` è l'esito prima della modalità e dell'override.

**Regole possibili**, per ambiente: requisiti di progetto verificati o in deroga valida; nessuna CVE critica o alta (con o senza correzione, con una soglia tollerata) nell'ultima scansione della versione; change approvato (la richiesta è in una delle fasi scelte); finestra di rilascio (giorni, orari, fuso, blocchi del calendario); controlli della pipeline superati nell'ultimo esito del commit; condizioni libere nel linguaggio dei processi, sui dati dell'applicazione o della richiesta.

**Idempotenza.** La stessa domanda con lo stesso esito entro cinque minuti vale una decisione sola.

**Override.** Un rilascio bloccato si supera solo con una motivazione e l'approvazione di chi è indicato in Configurazione → Gate di rilascio (chi lo chiede non lo approva). Approvato, vale per la stessa applicazione, lo stesso ambiente e la stessa versione, per le ore scelte (al massimo quelle dell'ambiente): il gate risponde `pass` con `"overridden": true` e i motivi superati marcati. Da interfaccia: scheda «Gate di rilascio» dell'applicazione.

**Evidenze GRC.** Se il tenant ha mappato i controlli della pipeline sui controlli di conformità, un rilascio con un controllo `block` superato (e una decisione di gate superata, non per override) registra da sola un'evidenza con scadenza; ripetere l'invio non la duplica.

Esempio, **prima del deploy in produzione** (si ferma lo step se il gate dice `fail`):

```bash
curl -sS -o gate.json -w '%{http_code}' -X POST "$REGIA_URL/api/v1/devsecops/gate" \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $REGIA_TOKEN" \
  -d "{\"format\":\"regia.devsecops/v1\",\"application\":\"API-CLI\",\"service\":\"clienti-api\",\"environment\":\"prod\",
       \"version\":\"$VERSION\",\"commit\":\"$GIT_COMMIT\",\"change\":\"$CHANGE\"}" > code.txt || true
jq -r '"Gate: \(.result) — \(.summary)", (.reasons[] | select(.status != "pass") | "  - \(.label): \(.message) \(.url // "")")' gate.json
[ "$(cat code.txt)" = "200" ] || { echo "Rilascio bloccato dal gate di Regia"; exit 1; }
```

Con `curl -fsS` (senza `-o`) lo step si ferma al primo `409`, ma non stampa i motivi: per averli serve la forma sopra. Se Regia non deve fermare il rilascio quando non risponde, ignora gli errori di rete (`code.txt` vuoto) e fermati solo su `409`.

**Letture e configurazione** (sessione o token con `element.read`; la configurazione richiede `architecture.write` o `tenant.admin`):
- `GET /api/v1/devsecops/gate/config`: regole per ambiente, approvatori, mappatura delle evidenze, fasi della richiesta, fatti per le condizioni;
- `PUT /api/v1/devsecops/gate/policies/{ambiente}` (`*` = tutti gli altri) e `DELETE`; `PUT /api/v1/devsecops/gate/approvers`; `PUT /api/v1/devsecops/gate/evidence-map`;
- `POST /api/v1/devsecops/gate/preview`: la stessa domanda del gate (più `policy` per provare una bozza) senza registrare nulla;
- `GET /api/v1/devsecops/gate/decisions?result=fail&limit=20`; `GET /api/v1/devsecops/elements/{ARC-3|DEM-12}/gate` (scheda dell'applicazione o della richiesta);
- `POST /api/v1/devsecops/gate/overrides` (`decision`, `reason`, `hours`) e `POST /api/v1/devsecops/gate/overrides/{id}/revoke`.

## 5. Letture (interfaccia)

Con una sessione o un token con `element.read`:
- `GET /api/v1/devsecops/overview`: tre numeri con il confronto a 7 giorni, riepilogo per asset, vulnerabilità critiche in produzione (prima le applicazioni DORA), ultimi rilasci;
- `GET /api/v1/devsecops/elements/{ARC-3}/panel`: pannello di un asset logico o di un'applicazione;
- `GET /api/v1/devsecops/services/{id}`: un servizio con lo storico dei rilasci;
- `GET /api/v1/devsecops/config`: impostazioni, ambienti, servizi, change.

Configurazione (`architecture.write` o `tenant.admin`): `PUT /api/v1/devsecops/config/settings`, `PATCH /api/v1/devsecops/environments/{id}`, `PATCH /api/v1/devsecops/services/{id}` con `{"application": "API-CLI" | ""}`, `PATCH /api/v1/devsecops/changes/{id}` con `{"request": "DEM-12", "number": "CHG…"}`.
