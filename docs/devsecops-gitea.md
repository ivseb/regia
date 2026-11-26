# Connettore Gitea (self-hosted, anche in rete locale)

Regia legge da Gitea (e da Forgejo, che ne ha la stessa API) quello che le pipeline producono già (repository, release con i loro allegati, stati dei commit) e lo collega alle applicazioni del catalogo. **Non scrive mai su Gitea** e non esegue pipeline. È il fratello dei connettori [GitHub](devsecops-github.md) e [GitLab](devsecops-gitlab.md): stesso contratto, stesso `Ingestor`, stessi profili di pipeline, stessa politica di rete. Serve al pilota «repository pubblici su GitHub e quelli di un Gitea locale» nella stessa piattaforma.

Dove: **Configurazione → Gitea** (serve il permesso «Amministrare l'organizzazione»). Nella scheda dell'integrazione un elenco in cinque passi dice a che punto sei (token, prova, repository scoperti, repository associati, prima sincronizzazione) e indica il prossimo.

## 1. Il token su Gitea

Il connettore usa un **token di accesso** di un utente. Meglio un **utente di servizio** (per esempio `regia`) che vede solo i repository da leggere, che il token di una persona.

1. In Gitea, con l'utente di servizio: *Impostazioni* → *Applicazioni* → **Genera nuovo token**.
2. **Permessi**, tutti in sola lettura:
   - `repository`: *lettura* (repository, release, tag, stati dei commit);
   - `organization`: *lettura* (per elencare i repository di un'organizzazione);
   - `user`: *lettura* (la prova di connessione legge `/user`).
   Non servono scritture né altro.
3. Copia il token: Gitea non lo mostra più. Gitea non ha scadenza obbligatoria: se la imposti, ricordati di rinnovarlo.
4. Aggiungi l'utente di servizio, con accesso in lettura, ai repository o all'organizzazione da leggere (i repository privati si vedono solo così).

Gitea **non permette di leggere i permessi di un token**: la prova di connessione verifica che il token funzioni (`/user`) e che i proprietari siano raggiungibili, ma non può dire se i permessi sono troppo larghi. Il token è cifrato con la chiave applicativa della cartella dati (`<data>/keys/secrets.key`) e **non torna mai** dalle API: l'interfaccia dice solo se c'è. Lasciando vuoto il campo in una modifica, il valore salvato resta. Nei messaggi di errore e nei log il token non compare.

## 2. Configurarlo in Regia

**Configurazione → Gitea → Nuova integrazione.**

| Campo | Valore |
|---|---|
| Indirizzo di Gitea | `https://gitea.azienda.it`, un prefisso come `https://azienda.it/gitea`, oppure `http://192.168.1.20:3000` per un Gitea in rete locale (serve **Rete interna**). Regia aggiunge `/api/v1` |
| Che cosa leggere | da 1 a **20 proprietari** (organizzazioni o utenti: `acme`, `mario`) oppure **repository** singoli (`acme/ordini`, fino a 100) |
| Token | token di accesso in sola lettura |
| Profilo di pipeline | dove la pipeline lascia i suoi allegati (§5). Senza profilo solo tag, data e autore delle release; i controlli si leggono lo stesso |
| Intervallo | ogni quanti minuti leggere (da 5 a 1440, predefinito 30) |
| Controllo «verdetto» | il controllo (contesto dello stato del commit) che decide l'esito; se vuoto conta l'insieme dei controlli |
| Ambiente dei rilasci | l'ambiente delle release quando il profilo non lo dice (predefinito `prod`) |

Poi:

1. **Prova la connessione.** Il messaggio dice con quale istanza e con quale utente ha parlato («Connessione riuscita con Gitea 1.22.3 come «regia». Proprietario raggiungibile: acme.») e segnala credenziali rifiutate (401), proprietario o repository non visibile (404 spiegato), certificato TLS, rete.
2. **Repository → Scopri i repository.** Regia legge i proprietari (un'organizzazione con `/orgs/{org}/repos`, altrimenti un utente con `/users/{user}/repos`, con paginazione `page`/`limit`; i fork e, di norma, gli archiviati si saltano) e propone per ogni repository l'applicazione del catalogo con il nome o il codice più vicino, con il motivo. Le proposte si confermano una per una o in blocco; si può scegliere un'altra applicazione o ignorare un repository. **Solo i repository associati diventano servizi e si sincronizzano.**
3. **Sincronizzazioni → Sincronizza ora**, oppure si aspetta il job periodico.

### Rete interna, http e certificati

Come per GitHub Enterprise Server e GitLab: di default Regia chiama solo indirizzi pubblici in https (controllo sull'indirizzo a cui ci si connette davvero, dopo il DNS; stessa protezione SSRF dei webhook in uscita). Per un Gitea nella rete aziendale o di casa l'operatore imposta `REGIA_CONNECTOR_ALLOW_PRIVATE=true` e l'integrazione attiva **Rete interna**: reti private sì, **anche con `http`** (il token viaggia in chiaro sulla tua rete: valuta se è accettabile), link-local (metadati del cloud) mai. Una CA aziendale si incolla in **CA interna** (PEM). Regia non usa i proxy dalle variabili d'ambiente.

Il token parte solo verso l'istanza configurata: l'allegato di una release con un indirizzo su un altro host non si scarica, e il token si toglie dai redirect.

## 3. Webhook (facoltativi)

Il job periodico è il meccanismo principale: nelle reti aziendali i webhook spesso non arrivano. Se Gitea raggiunge Regia:

1. Nell'integrazione: **Genera il segreto del webhook**. Si mostra una volta; in alternativa lo si sceglie (da 16 caratteri) e lo si scrive nel campo.
2. In Gitea (repository o organizzazione → *Impostazioni* → *Webhook* → *Aggiungi webhook* → *Gitea*): *URL di destinazione* = l'indirizzo mostrato nell'integrazione, `https://<Regia>/api/v1/devsecops/gitea/webhook/<id>`; *Metodo* `POST`, *Tipo di contenuto* `application/json`; *Segreto* = il segreto; eventi personalizzati **Push**, **Release**, **Pull request** (e **Creazione**, per i tag).

Gitea firma il corpo con HMAC-SHA256 del segreto e manda l'esadecimale in `X-Gitea-Signature` (Regia accetta anche `X-Hub-Signature-256` con il prefisso `sha256=`): Regia ricalcola la firma e la confronta in tempo costante. Senza firma, o con una firma errata, risponde `401`; un'integrazione senza segreto risponde `404`. Ogni consegna vale una volta sola: si riconosce da `X-Gitea-Delivery`, altrimenti da `Idempotency-Key`, altrimenti dall'impronta del corpo.

Che cosa avvia:

| Evento | Sincronizzazione mirata |
|---|---|
| Push di un tag, Creazione di un tag, Release (non eliminata) | le release del repository |
| Push sul branch predefinito, Pull request unita nel branch predefinito | i controlli del repository |

Gli altri eventi, i repository non associati e i push su altri branch si ignorano con un messaggio chiaro.

## 4. Cosa legge, e quando

Il job controlla ogni minuto le integrazioni dovute e le esegue una alla volta (lease nel database: anche con più processi gira una sola sincronizzazione per integrazione). Per ogni repository associato:

| Cosa | Da dove | Ne esce |
|---|---|---|
| Release | `releases` (bozze escluse; le pre-release solo se il profilo le include), dalla più recente; il commit dal tag; gli **allegati** nominati dal profilo | `Release`, `SBOM`, `VulnReport` |
| Controlli | gli stati del commit in cima al branch predefinito (`/commits/{branch}/status`), un controllo per contesto: li scrive Gitea Actions per ogni job, o qualunque CI | `CheckRun` |

**Gitea non ha «deployment»**: i deploy arrivano da altre fonti (API di push della pipeline, o GitOps). La sincronizzazione è incrementale (cursori per repository, nella stessa transazione degli oggetti) e idempotente: rileggere lo stesso dato non produce niente di nuovo. **Rileggi tutto** ignora i cursori; l'Ingestor scarta i doppioni.

## 5. Il profilo di pipeline

Il preset **«Gitea Actions con SBOM e scansioni»** (Profili → crea dal preset) legge gli allegati `sbom.cdx.json` (CycloneDX) e `vulnerabilities.json` (grype o trivy), con tag `v*` e ambiente `prod`. Un workflow di rilascio di Gitea Actions che crea la release e carica quei due file basta. Gli allegati si nominano con i modelli `*` e `?`, come per gli altri connettori; la scheda **Profili** permette di provare il profilo su un repository senza salvare nulla (anteprima).

Se un repository pubblica più tag per versione (chart e applicazione), alla scoperta Regia chiede quale è il rilascio vero e salva la risposta come filtro dei tag del repository nel profilo, creandolo se manca: vedi «Più tag per versione» in `devsecops-github.md`.

## 6. Permessi, audit e limiti

- Lettura delle integrazioni, scoperta, sincronizzazione e anteprima: `tenant.admin` (anche con un token API); modifiche della configurazione solo con una sessione.
- Ogni creazione, modifica, cancellazione, prova, scoperta, associazione e sincronizzazione è nel registro di audit (`devsecops.gitea.*`), senza segreti.
- Al massimo 20 integrazioni Gitea per organizzazione, 20 proprietari e 100 repository singoli per integrazione.
- Gitea di norma non ha limiti di velocità; un `429` con `Retry-After` si rispetta, altrimenti la sincronizzazione si ferma con lo stato «limite di velocità» e riprende dopo.
