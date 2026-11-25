# Connettore GitHub e GitHub Enterprise Server

Regia legge da GitHub quello che le pipeline producono già (repository, Release con i loro allegati, deploy, esiti dei controlli) e lo collega alle applicazioni del catalogo. **Di norma non scrive su GitHub** (l'unica eccezione, facoltativa, è l'adozione guidata del §8: un branch e una pull request, mai il branch principale) e non esegue pipeline. Il disegno è in [replica/devsecops.md](../replica/devsecops.md) §2 e §4.

Dove: **Configurazione → GitHub** (serve il permesso «Amministrare l'organizzazione»).

## 1. Creare la GitHub App

Una GitHub App è il modo consigliato: i permessi sono minimi e in sola lettura, il token dura un'ora e si rinnova da solo, nessuna persona presta il proprio account. Il token personale va bene solo per le prove.

1. Su GitHub (o GitHub Enterprise Server): *Settings dell'organizzazione* → *Developer settings* → *GitHub Apps* → **New GitHub App**.
2. **Nome**: per esempio «Regia (lettura)». **Homepage URL**: l'indirizzo di Regia.
3. **Webhook**: lascialo disattivato per cominciare. Si attiva dopo, se GitHub riesce a raggiungere Regia (vedi il §4).
4. **Permessi del repository**, tutti **Read-only**:

   | Permesso | Serve per |
   |---|---|
   | Metadata | elenco dei repository (obbligatorio) |
   | Contents | Release e allegati, file degli ambienti nel repository GitOps |
   | Deployments | Deployments API |
   | Checks | check run dell'ultimo commit |

   Nessun permesso dell'organizzazione, nessun permesso in scrittura. La prova di connessione segnala sia i permessi mancanti sia quelli di scrittura che Regia non usa.
5. *Where can this GitHub App be installed?* **Only on this account**.
6. **Create GitHub App**. Annota l'**App ID** in cima alla pagina.
7. *Private keys* → **Generate a private key**: GitHub scarica un file `.pem`. È l'unica copia: va in Regia e poi si cancella dal computer.

## 2. Installarla

1. Nella pagina della App: **Install App** → l'organizzazione.
2. Scegli **All repositories**, oppure solo i repository delle applicazioni e il repository GitOps.
3. Dopo l'installazione l'indirizzo è `…/settings/installations/<numero>`: quel numero è l'**installation ID**.

## 3. Configurarla in Regia

**Configurazione → GitHub → Nuova integrazione.**

| Campo | Valore |
|---|---|
| URL base | `https://api.github.com` per github.com; per GitHub Enterprise Server l'indirizzo dell'istanza (`https://github.azienda.it`): Regia aggiunge `/api/v3` |
| Organizzazioni | una o più, separate da virgole |
| Autenticazione | **GitHub App**: App ID, installation ID, contenuto del file `.pem`. Oppure **Token** per le prove (permessi `repo` e `read:org`, o un fine-grained token in lettura) |
| Profilo di pipeline | dove la pipeline deposita i suoi artefatti (vedi il §5). Senza profilo Regia usa solo i dati base delle Release: tag, data, autore |
| Repository GitOps | `org/nome` del repository con `envs/<ambiente>/`; se vuoto valgono quelli del profilo |
| Intervallo | ogni quanti minuti leggere (da 5 a 1440, predefinito 30) |
| Controllo verdetto | il check run che decide l'esito di un commit (es. `Verdict`, anche come `ci / Verdict`); se vuoto vale quello del profilo, e senza nessuno conta l'insieme dei controlli |
| Ambiente dei rilasci | l'ambiente delle Release quando il registro non lo dice (predefinito `prod`) |

I segreti (chiave privata, token, segreto del webhook) sono cifrati con la chiave applicativa della cartella dati (`<data>/keys/secrets.key`) e **non tornano mai** dalle API: l'interfaccia dice solo se ci sono. Lasciando vuoto un campo segreto in una modifica, il valore salvato resta.

Poi:

1. **Prova la connessione.** Il messaggio dice con quale istanza e come ha parlato («Connessione riuscita con GitHub Enterprise Server 3.15 come GitHub App «Regia (lettura)». Organizzazione raggiungibile: acme.»), oppure cosa non va (credenziali rifiutate, organizzazione non visibile, certificato, rete).
2. **Repository → Scopri i repository.** Regia propone per ogni repository l'applicazione del catalogo con il nome o il codice più vicino, con il motivo (es. «il codice «CRM» è nel nome»). Le proposte si confermano una per una o in blocco; si può scegliere un'altra applicazione o ignorare un repository. **Solo i repository associati diventano servizi e si sincronizzano.** Il nome del servizio è quello del repository, modificabile.
3. **Sincronizzazioni → Sincronizza ora**, oppure si aspetta il job periodico.

### Rete interna e certificati

GitHub Enterprise Server sta spesso in una rete privata. Di default Regia chiama solo indirizzi pubblici, con lo stesso controllo dei webhook in uscita: l'indirizzo si verifica al momento della connessione, dopo il DNS, e i redirect (gli allegati delle Release) passano dallo stesso controllo.

- Per un GHES interno l'operatore imposta `REGIA_CONNECTOR_ALLOW_PRIVATE=true`. Da quel momento ogni integrazione può attivare **Rete interna**. Anche così restano bloccati gli indirizzi link-local, cioè i metadati del cloud.
- Se il certificato di GHES è emesso da una CA aziendale, incolla la CA (PEM) in **CA interna**.
- Regia non usa i proxy dalle variabili d'ambiente: la destinazione la decide la configurazione.

## 4. Webhook (facoltativi)

Il job periodico è il meccanismo principale, perché nelle reti aziendali i webhook spesso non arrivano. Se GitHub raggiunge Regia:

1. Nell'integrazione: **Genera il segreto del webhook**. Il segreto si mostra una volta; in alternativa lo si sceglie e lo si scrive nel campo.
2. Nella App (o in un webhook dell'organizzazione): *Webhook URL* = l'indirizzo mostrato nell'integrazione, `https://<Regia>/api/v1/devsecops/github/webhook/<id>`; *Webhook secret* = il segreto; eventi **Release**, **Deployment status**, **Check suite** e, per il repository GitOps, **Push**.

Regia verifica la firma `X-Hub-Signature-256`: senza firma, o con una firma errata, risponde `401`. Ogni consegna (`X-GitHub-Delivery`) vale una volta sola. L'evento avvia una sincronizzazione mirata: solo le Release, solo i deploy o solo i controlli di quel repository, oppure solo il repository GitOps.

## 5. Profili di pipeline

Un profilo è **configurazione, non codice**: dice dove una famiglia di template deposita i suoi artefatti e come tradurli negli oggetti di Regia. Si gestisce in **Configurazione → GitHub → Profili di pipeline**:

- si parte da un preset;
- si modifica il JSON nell'editor;
- **Valida** controlla il profilo senza salvarlo;
- ogni salvataggio con una definizione diversa crea una **nuova versione**;
- **Esporta** e **Importa un JSON** portano un profilo da un'istanza all'altra: un profilo con la stessa chiave riceve una nuova versione.

**Prova il profilo su un repository** legge da GitHub con la definizione dell'editor, anche non salvata, e mostra cosa ne uscirebbe: Release, allegati letti, controlli, CVE, SBOM, ambienti. Non salva niente.

Preset inclusi:

- **Template GitHub Actions + GitOps (Argo CD)**: workflow riusabili che allegano a ogni Release un registro di rilascio (`release-record.json`), gli esiti dei controlli (`ci-checks.json`), l'SBOM CycloneDX (`sbom.cdx.json`) e il report grype (`grype-report.json`); lo stato degli ambienti sta in `envs/<ambiente>/` di un repository GitOps letto da Argo CD.
- **Release GitHub con SBOM e vulnerabilità**: solo allegati in formato standard, più i check run e la Deployments API.

Struttura (estratto):

```json
{
  "format": "regia.pipeline-profile/v1",
  "release": {
    "environment": "prod",
    "tag_prefix": "v",
    "tag_filter": "v*",
    "record": {
      "asset": "release-record.json",
      "fields": {
        "version": "version",
        "digest": "digest",
        "change_number": "change",
        "approved_by": "gitops_pull_request.merged_by",
        "at": ["gitops_pull_request.merged_at", "finalized_at"]
      }
    },
    "checks": {
      "asset": "ci-checks.json",
      "sources": [
        { "kind": "map", "path": "ci_verdict" },
        { "kind": "list", "path": "check_runs", "name": "name", "outcome": "conclusion", "only_if_empty": true },
        { "kind": "single", "name": "sonar", "outcome": "sonar.quality_gate" }
      ],
      "outcomes": { "OK": "passed", "ERROR": "failed" }
    },
    "sbom": { "asset": "sbom.cdx.json", "format": "cyclonedx" },
    "vulnerabilities": { "asset": "grype-report.json", "format": "grype" }
  },
  "checks": { "verdict": "Verdict" },
  "deployments": { "disabled": true },
  "gitops": {
    "repositories": ["acme/shop-gitops"],
    "path": "envs/{environment}",
    "files": ["*.yaml", "*.yml"],
    "services": "services",
    "fields": {
      "version": ["annotations[\"ci/version\"]", "image.tag"],
      "image": ["image.repository", "image.name"],
      "commit": "annotations[\"ci/git-commit\"]"
    },
    "environment_names": { "production": "prod" }
  }
}
```

**Percorsi.** Sono semplici, senza un linguaggio:

| Forma | Esempio |
|---|---|
| chiavi separate da punti | `release.digest` |
| un elemento di un elenco | `checks[0].name` |
| una chiave con punti o barre | `annotations["ci/version"]` |
| un valore fisso | `=prod` |

Un elenco di percorsi vale per il primo che porta a un valore non vuoto.

**Campi del registro** (`release.record.fields`):
- versione, commit e artefatto: `version`, `commit`, `image`, `digest`, `environment`;
- change e persone: `change_number`, `change_title`, `requested_by`, `approved_by`, `approved_at`;
- riferimenti e data: `pull_request`, `at`;
- conteggi delle CVE al rilascio: `cves.critical`, `cves.high`, `cves.medium`, `cves.low`, `cves.fixable_critical`, `cves.fixable_high`. Se c'è il report delle vulnerabilità, i conteggi si calcolano da quello.

**Esiti dei controlli.**
- Fonti:
  - `map`: un oggetto nome → esito;
  - `list`: un elenco con i campi nome, esito, url, riassunto e modalità;
  - `single`: un solo controllo.
- Gli esiti si traducono in `passed`, `failed`, `skipped` e `not_run`, con la tabella `outcomes` del profilo prima di quella predefinita (`success` → `passed`, `failure` → `failed`…).

**Formati standard.**
- SBOM: CycloneDX e SPDX in JSON.
- Vulnerabilità: grype (il report completo o il solo elenco delle corrispondenze), trivy e il JSON del contratto.
- Una GHSA con una CVE equivalente si riporta come la CVE.
- Strato: `image` per i pacchetti di sistema, `app` per le dipendenze del codice.

**GitOps.**
- Per ogni ambiente, cioè ogni sottocartella di `envs/` oppure quelli elencati in `environments`, Regia legge i file che corrispondono a `files`.
- Con `services` un file contiene più servizi (nome → blocco). Senza, il file è un servizio con il nome del file.
- Un servizio si associa al repository per nome del servizio, nome del repository o nome dell'immagine.
- Ne esce un deploy `running` per ambiente, con l'istante dell'ultimo commit che ha toccato il file.
- I servizi senza repository associato compaiono tra gli avvisi della sincronizzazione.

## 6. Cosa legge, e quando

Il job controlla ogni minuto le integrazioni dovute e le esegue una alla volta. Per ogni integrazione gira una sola sincronizzazione per volta, anche con più processi. A ogni giro, per ogni repository associato, Regia legge:

| Cosa | Da dove | Ne esce |
|---|---|---|
| Release | le Release pubblicate, dalla più recente, e gli allegati indicati dal profilo | `Release`, `SBOM`, `VulnReport` |
| Deploy | Deployments API: ultimo stato di ogni deployment recente | `Deployment` (`pipeline`) |
| Controlli | check run dell'ultimo commit del branch predefinito, quando il verdetto (o tutto) è concluso | `CheckRun` |
| Ambienti | file di `envs/<ambiente>/` del repository GitOps | `Deployment` (`gitops`) |

Il giro completo legge anche l'elenco dei repository: nuovi, scomparsi, proposte aggiornate.

**Incrementale.** Regia tiene dei cursori e salva gli oggetti e i cursori di un repository nella stessa transazione, così un errore non perde dati e la volta dopo si riprova.

| Cursore | Effetto |
|---|---|
| Release già lette, con l'impronta degli allegati | un allegato aggiunto dopo la pubblicazione fa rileggere la Release |
| ultimo stato di ogni deployment | i deployment conclusi non si rileggono |
| commit dei controlli già riportati | i controlli di un commit si riportano una volta |
| commit del repository GitOps e sha di ogni file | si rileggono solo i file cambiati |

**Rileggi tutto** ignora i cursori; l'archiviazione riconosce i doppioni. Al primo giro si leggono al massimo le 50 Release più recenti per repository.

**Limiti di velocità.**
- Regia legge `X-RateLimit-*` da ogni risposta e lascia un margine.
- Davanti a un limite secondario (`Retry-After`) aspetta fino a un minuto.
- Oltre il minuto la sincronizzazione si ferma con lo stato «limite di velocità» e riprende dopo il reset.
- La pagina Sincronizzazioni mostra le richieste rimaste e l'ora del reset.

**Stato.** In Sincronizzazioni trovi:
- l'ultima esecuzione, la prossima e l'intervallo;
- le ultime 50 esecuzioni, con quante Release, SBOM, report, deploy e controlli sono arrivati, le richieste fatte a GitHub, gli errori per repository e gli avvisi (allegati mancanti, servizi GitOps senza repository).

## 7. Provarlo senza GitHub

```bash
make build
./bin/regia dev fake-github --addr 127.0.0.1:8122 --enterprise --key /tmp/finta-app.pem
./bin/regia serve --dev --http 127.0.0.1:8080        # in un altro terminale
```

Il finto GitHub ha un'organizzazione `esempio` con dati inventati: due servizi con Release complete, uno con la Deployments API, un repository GitOps e un repository che non corrisponde a nessuna applicazione. Il comando stampa:
- URL base;
- App ID e installation ID;
- il percorso della chiave privata;
- un token per le prove.

Con `--dev` la protezione SSRF è spenta, quindi `http://127.0.0.1` è ammesso.

## 8. Adozione guidata tramite pull request (facoltativa)

Dall'applicazione (scheda **Produzione → Pipeline e politica → Proponi la pipeline**, oppure da **Aderenza pipeline**) Regia genera i file per il repository e, se può, apre la pull request. Il disegno è in [replica/devsecops.md](../replica/devsecops.md) §3.4 e §7.

### Permessi facoltativi della GitHub App

Servono solo per **aprire la pull request**. Senza, tutto il resto funziona e i file si **scaricano** (un archivio zip) per aprirla a mano.

| Permesso | Livello | Serve per |
|---|---|---|
| Contents | Read and write | creare il branch `regia/pipeline-…` e scrivere i due file |
| Pull requests | Read and write | aprire la pull request e leggerne lo stato (aperta, unita, chiusa) |

- Si aggiungono dalle impostazioni della App; l'amministratore dell'organizzazione deve **accettare** i nuovi permessi dell'installazione.
- La prova di connessione li riconosce: dice se la App può aprire pull request (e non li segnala come «permessi di scrittura che Regia non usa»). Con un token personale non si può saperlo prima: si prova al momento di aprire la pull request.
- Se mancano, aprire la pull request dà un errore chiaro e la procedura offre il download.
- Regia **non fa mai commit sul branch principale**, non unisce e non cancella.

### Cosa genera: solo file generici

Nessun contenuto dei tuoi template è noto a Regia: i riferimenti li scrivi tu, nella sezione **adoption** del profilo di pipeline (Configurazione → Politica della pipeline → **Adozione**, oppure nel JSON del profilo).

```json
"adoption": {
  "workflow_path": ".github/workflows/regia-pipeline.yml",
  "config_path": ".github/pipeline.yml",
  "config_key": "checks",
  "params": [{ "key": "stack", "label": "Stack", "default": "node", "options": ["node", "java"] }],
  "templates": [
    { "key": "ci", "name": "Build e test", "default": true, "inherit_secrets": true,
      "uses": "organizzazione/pipeline/.github/workflows/ci.yml@v1",
      "with": { "stack": "{stack}", "application": "{application}" },
      "controls": ["test", "lint", "secrets"] },
    { "key": "sicurezza", "name": "Scansioni", "needs": ["ci"],
      "uses": "organizzazione/pipeline/.github/workflows/security.yml@v1",
      "controls": ["dependencies", "sast", "image"] }
  ]
}
```

- **`uses`** deve essere un workflow riusabile (`org/repo/.github/workflows/file.yml@versione`, oppure `./.github/workflows/file.yml`): qualunque altra cosa è rifiutata dalla validazione.
- **`with`**: i valori accettano `{application}`, `{service}`, `{repository}`, `{default_branch}` e i parametri della sezione `params`, scelti nella procedura.
- **`controls`**: i controlli della politica che il template esegue. Se la classe dell'applicazione ne richiede uno che nessun template scelto dichiara, la procedura avvisa.
- Il **workflow** generato contiene solo un job per template scelto che richiama il `uses:` (con `permissions: contents: read`); il **file dei controlli** (`.github/pipeline.yml` per default) elenca per ogni controllo della politica la modalità `off`, `warn` o `block` per la classe dell'applicazione. Chiave e percorsi sono configurabili.

### Procedura

1. **Cosa proporre**: template e parametri.
2. **Repository**: quello associato all'applicazione, o un altro già scoperto, oppure nessuno (solo file).
3. **Anteprima**: per ogni file lo stato (nuovo, da aggiornare, già uguale) e la differenza riga per riga con il branch predefinito.
4. **Apri la pull request** (con i permessi) oppure **Scarica i file**.

Una sola pull request di Regia può essere aperta per repository. Il suo stato si aggiorna da solo: ogni dieci minuti dal job del connettore, subito con il webhook **Pull request** (aggiungi l'evento alla App) e a richiesta da «Aggiorna lo stato». Quando la pull request è unita, gli esiti dei controlli arrivano con la prima esecuzione della pipeline e l'aderenza alla politica si aggiorna.

Provarla senza GitHub: `./bin/regia dev fake-github --enterprise --write` (il finto GitHub con i permessi in scrittura).
