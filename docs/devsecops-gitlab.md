# Connettore GitLab (gitlab.com e self-managed)

Regia legge da GitLab quello che le pipeline producono già (progetti, Release con i loro link e artefatti, pipeline e job, Environments e Deployments) e lo collega alle applicazioni del catalogo. **Non scrive mai su GitLab** e non esegue pipeline. È il fratello del [connettore GitHub](devsecops-github.md): stesso contratto, stesso `Ingestor`, stessi profili di pipeline, stessa politica di rete. Il disegno è in [replica/devsecops.md](../replica/devsecops.md) §2 e §4.

Dove: **Configurazione → GitLab** (serve il permesso «Amministrare l'organizzazione»). Nella scheda dell'integrazione un elenco in cinque passi dice a che punto sei (token, prova, progetti scoperti, progetti associati, prima sincronizzazione) e indica il prossimo.

## 1. Il token

Il connettore usa un **token di accesso di gruppo** (consigliato) o **di progetto**. Un token personale funziona, ma lega l'integrazione a una persona: meglio evitarlo.

1. In GitLab: gruppo → *Impostazioni* → *Token di accesso* → **Aggiungi un nuovo token**.
2. **Ruolo**: `Reporter` basta per leggere progetti, pipeline, Release e Deployments.
3. **Scope**: solo `read_api`. Non servono `api`, `write_repository`, `read_repository` né altro.
4. **Scadenza**: mettila (GitLab la impone sui token di gruppo e di progetto). Regia avvisa quando mancano meno di 14 giorni.
5. Copia il token (`glpat-…`): GitLab non lo mostra più.

Con un token di **progetto** non si scoprono i progetti di un gruppo: nell'integrazione si indicano i progetti uno per uno (`gruppo/progetto`).

Il token è cifrato con la chiave applicativa della cartella dati (`<data>/keys/secrets.key`) e **non torna mai** dalle API: l'interfaccia dice solo se c'è. Lasciando vuoto il campo in una modifica, il valore salvato resta.

## 2. Configurarlo in Regia

**Configurazione → GitLab → Nuova integrazione.**

| Campo | Valore |
|---|---|
| Indirizzo di GitLab | `https://gitlab.com` oppure l'istanza (`https://git.azienda.it`, anche con un prefisso come `https://azienda.it/gitlab`): Regia aggiunge `/api/v4` |
| Che cosa leggere | uno o più **gruppi** (`acme`, `acme/piattaforma`: si leggono anche i sottogruppi) oppure **progetti** singoli (token di progetto) |
| Token | scope `read_api` |
| Profilo di pipeline | dove la pipeline lascia i suoi artefatti (vedi il §5). Senza profilo solo tag, data e autore delle Release; i deploy e i controlli si leggono lo stesso |
| Intervallo | ogni quanti minuti leggere (da 5 a 1440, predefinito 30) |
| Job «verdetto» | il job che decide l'esito di un commit; se vuoto conta l'insieme dei job |
| Ambiente dei rilasci | l'ambiente delle Release quando il profilo non lo dice (predefinito `prod`) |
| Job con gli artefatti | modelli dei nomi dei job di cui leggere gli artefatti (`dependency_scanning`, `container_*`); vuoto = tutti |

Poi:

1. **Prova la connessione.** Il messaggio dice con quale istanza e quale token ha parlato («Connessione riuscita con GitLab 17.5.1-ee con il token «Regia (lettura)». Gruppo raggiungibile: acme.»), mostra gli scope come etichette e segnala:
   - **scope mancanti**: senza `read_api` la prova fallisce e lo dice;
   - **scope in eccesso**: `api`, `write_repository`… funzionano ma sono un rischio inutile, e la prova li elenca;
   - token **revocato**, **scaduto** o **in scadenza**;
   - gruppo o progetto non visibile (404 spiegato), credenziali rifiutate (401), certificato TLS, rete.
   Sulle istanze precedenti alla 15.5 gli scope non si possono leggere: la prova va avanti con un avviso.
2. **Progetti → Scopri i progetti.** Regia legge i gruppi (anche i sottogruppi) e propone per ogni progetto l'applicazione del catalogo con il nome o il codice più vicino, con il motivo. Le proposte si confermano una per una o in blocco; si può scegliere un'altra applicazione o ignorare un progetto. **Solo i progetti associati diventano servizi e si sincronizzano.**
3. **Sincronizzazioni → Sincronizza ora**, oppure si aspetta il job periodico.

### Rete interna e certificati

Come per GitHub Enterprise Server: di default Regia chiama solo indirizzi pubblici (controllo sull'indirizzo a cui ci si connette davvero, dopo il DNS; stessa protezione SSRF dei webhook in uscita). Per un GitLab nella rete aziendale l'operatore imposta `REGIA_CONNECTOR_ALLOW_PRIVATE=true` e l'integrazione attiva **Rete interna**: reti private sì, link-local (metadati del cloud) mai. Una CA aziendale si incolla in **CA interna** (PEM). Regia non usa i proxy dalle variabili d'ambiente.

Il token parte solo verso l'istanza configurata: i link di paginazione che puntano altrove si rifiutano, un link di una Release verso un altro host si scarica senza credenziali, e il token si toglie dai redirect (gli artefatti stanno spesso su uno storage a parte).

## 3. Webhook (facoltativi)

Il job periodico è il meccanismo principale: nelle reti aziendali i webhook spesso non arrivano. Se GitLab raggiunge Regia:

1. Nell'integrazione: **Genera il token del webhook**. Si mostra una volta; in alternativa lo si sceglie (da 16 caratteri) e lo si scrive nel campo.
2. In GitLab (gruppo o progetto → *Impostazioni* → *Webhook*): *URL* = l'indirizzo mostrato nell'integrazione, `https://<Regia>/api/v1/devsecops/gitlab/webhook/<id>`; *Secret token* = il token; eventi **Pipeline**, **Deployment**, **Release** e **Tag push**.

GitLab non firma il corpo: manda il segreto in `X-Gitlab-Token` e Regia lo confronta in tempo costante. Senza token, o con un token errato, risponde `401`; un'integrazione senza token segreto risponde `404`. Ogni consegna vale una volta sola: si riconosce da `X-Gitlab-Event-UUID`, altrimenti da `Idempotency-Key`, altrimenti dall'impronta del corpo.

Che cosa avvia:

| Evento | Sincronizzazione mirata |
|---|---|
| Pipeline conclusa | i controlli del progetto; se è la pipeline di un tag, anche le Release (gli artefatti la completano) |
| Deployment | i deploy del progetto |
| Release, Tag push | le Release del progetto |

Le pipeline non ancora concluse, i progetti non associati e gli altri eventi si ignorano con un messaggio chiaro.

## 4. Cosa legge, e quando

Il job controlla ogni minuto le integrazioni dovute e le esegue una alla volta (lease nel database: anche con più processi gira una sola sincronizzazione per integrazione). Per ogni progetto associato:

| Cosa | Da dove | Ne esce |
|---|---|---|
| Release | `releases`, dalla più recente; allegati cercati nei **link** della Release e negli **artefatti dei job** della pipeline del tag | `Release`, `SBOM`, `VulnReport` |
| Deploy | `deployments` recenti e, per ogni ambiente rimasto fuori, l'ultimo deployment (`environments`) | `Deployment` (`pipeline`) |
| Controlli | i job della pipeline più recente del branch predefinito, quando è conclusa | `CheckRun`: ogni job è un controllo |

**Job come controlli.** `success` è `passed`; `failed` è `failed`, salvo i job con `allow_failure`, che passano con la nota «fallito, ma consentito»; `canceled` è `failed`; `skipped` e i job manuali mai avviati sono `skipped` e non bloccano il verdetto. Se è indicato un job «verdetto» decide quello, altrimenti l'insieme.

**Deploy.** `success` → `succeeded`; `failed` e `canceled` → `failed`; gli altri → `running`. La versione è il tag (senza il prefisso `v`); un deploy da branch ha per versione lo sha abbreviato. Con il profilo si possono cambiare i percorsi (`deployments.version`) e i nomi degli ambienti (`production` → `prod`).

**Incrementale e idempotente.** Per progetto Regia tiene dei cursori: le Release già lette con l'impronta dei link, lo stato già visto di ogni deployment, l'ultima pipeline del branch già riportata. Gli oggetti di un progetto e i suoi cursori si salvano nella stessa transazione. Una Release il cui tag ha la pipeline ancora in corso, o senza pipeline da meno di un giorno, **si riprova** finché gli artefatti non arrivano. «Rileggi tutto» ignora i cursori; l'Ingestor scarta i doppioni. Al primo giro si leggono al massimo le 50 Release più recenti per progetto.

**Paginazione.** Si segue l'intestazione `Link`: dove GitLab la offre si usa la **paginazione keyset** (elenco dei progetti di un gruppo, ordinato per id, regge migliaia di progetti); altrove, la paginazione a pagine.

**Limiti di velocità.** Regia legge `RateLimit-Limit/Remaining/Reset` quando l'istanza li manda (gitlab.com sì, molte istanze self-managed no) e lascia un margine. Davanti a un `429` aspetta `Retry-After` fino a un minuto; oltre, la sincronizzazione si ferma con lo stato «limite di velocità» e riprende dopo il reset.

**Stato.** In Sincronizzazioni: ultima e prossima esecuzione, ultime 50 esecuzioni con quanti oggetti sono arrivati (Release, SBOM, report, deploy e ambienti, pipeline e controlli, archivi di artefatti scaricati), le richieste fatte, gli errori per progetto e gli avvisi.

## 5. Profili di pipeline

I profili sono quelli di tutti i connettori (**Configurazione → GitLab → Profili di pipeline**, o quelli di GitHub: sono la stessa cosa e si condividono). Per GitLab c'è un preset nuovo.

**«GitLab CI con SBOM e scansioni»**:

```json
{
  "format": "regia.pipeline-profile/v1",
  "release": {
    "environment": "prod",
    "tag_prefix": "v",
    "tag_filter": "v*",
    "sbom": { "asset": "gl-sbom-*.cdx.json", "format": "auto" },
    "vulnerabilities": { "asset": "gl-*-scanning-report.json", "format": "auto" }
  },
  "checks": {},
  "deployments": { "environments": { "production": "prod" } }
}
```

- Gli allegati si nominano con i modelli `*` e `?`: `gl-sbom-*.cdx.json` e `gl-*-scanning-report.json` sono i nomi dei template di sicurezza di GitLab (Dependency Scanning, Container Scanning). Per altri strumenti si cambiano i nomi.
- **Dove li cerca.** Prima nei **link della Release** (un pacchetto generico, un file pubblicato con la Release); se non ci sono, negli **artefatti dei job della pipeline del tag**: scarica l'archivio dei job che hanno artefatti (al massimo 12 per Release, solo quelli riusciti, solo i job indicati nell'integrazione se li limiti) e prende i file dal nome corrispondente, in qualunque cartella. Gli artefatti scaduti si saltano.
- **Formati.** SBOM CycloneDX e SPDX; vulnerabilità nel formato **dei report di sicurezza di GitLab** (si traducono nel formato del contratto: CVE preferita come identificativo, GHSA come alias, correzione ricavata dalla soluzione, strato `image` per Container Scanning e `app` per le dipendenze), oppure grype e trivy come per GitHub.
- Il profilo salvato non cambia: la traduzione avviene in lettura.
- La sezione `gitops` dei profili GitHub non è letta dal connettore GitLab: gli ambienti vengono da Environments e Deployments. L'anteprima lo segnala.

**Prova il profilo su un progetto** (editor di un profilo): legge da GitLab con la definizione dell'editor, anche non salvata, e mostra cosa ne uscirebbe (Release, da dove sono stati letti i file, CVE, SBOM, controlli, deploy). Non salva niente.

## 6. Provarlo senza GitLab

```bash
make build
./bin/regia dev fake-gitlab --addr 127.0.0.1:8174                  # un finto GitLab
./bin/regia serve --dev --http 127.0.0.1:8080                       # in un altro terminale
```

Il finto GitLab ha un gruppo `esempio` con un sottogruppo `esempio/negozio` e dati inventati: `ordini-api` con una Release con gli allegati negli artefatti dei job (v1.3.0) e una con gli allegati nei link (v1.4.0), `pagamenti-api` con la pipeline fallita, un progetto senza applicazione, Environments e Deployments. Il comando stampa l'URL base, il gruppo e un token (`glpat-finto-sviluppo`, scope `read_api`). Con `--dev` la protezione SSRF è spenta, quindi `http://127.0.0.1` è ammesso.

## 7. Per chi sviluppa

- Codice: `internal/modules/devsecops/connectors/gitlab` (servizio, progetti, sincronizzazione, webhook, anteprima, preset, artefatti) e `…/gitlab/glapi` (client REST con la sola libreria standard).
- API: `/api/v1/devsecops/gitlab/` con `integrations[/{id}]`, `test`, `webhook-secret`, `projects`, `discover`, `projects/{id}` (PUT), `projects/accept`, `sync[?full=true]`, `runs`, `preview`, `presets` e il webhook `POST …/webhook/{integration}`.
- Dati: nessuna migrazione. Usa `integrations` (kind `gitlab`, già previsto dallo schema iniziale), `connector_repositories`, `connector_sync_state`, `connector_sync_runs`, `inbound_deliveries` e i profili di `0025_connettori.sql`.
- Test: `internal/itest/devsecops_gitlab_test.go` (con `internal/devtools/fakegitlab`) e `gitlab_test.go` nel package.
