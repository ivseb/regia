# Connettore ALM/Quality Center (in sola lettura)

OpenText ALM/Quality Center è lo strumento dei test. Regia lo sostituirà **per gradi**:
1. **oggi:** legge da ALM test set, istanze di test, esecuzioni e difetti, e li usa nella certificazione del rilascio, nel gate e nelle evidenze GRC;
2. **poi:** il test management di Regia;
3. **infine:** la migrazione dello storico da ALM.

Le tre fasi usano le stesse tabelle dei test, distinte da un'origine (`alm`, `native`, `import`). Il passaggio da ALM a Regia non cambia la certificazione, il gate o le evidenze.

**Regia non scrive mai su ALM:** niente test, esecuzioni, difetti o commenti. Le uniche chiamate che non sono letture aprono e chiudono la sessione.

Dove: **Configurazione → Integrazioni → ALM / Quality Center**. Serve il permesso «Amministrare l'organizzazione».

## 1. Cosa legge, e cosa no

Per ogni progetto configurato (dominio e progetto):

| Collezione | Campi letti | Perché |
|---|---|---|
| `releases`, `release-cycles` | `id`, `name`, `parent-id`, il campo del numero di change se sta lì | nomi di release e ciclo, numero di change |
| `test-set-folders` | `id`, `name`, `parent-id` | cartella del test set, applicazione dalla cartella |
| `test-sets` | `id`, `name`, `parent-id`, `status`, `last-modified`, il campo del numero di change e quello dell'applicazione | il test set e il suo change |
| `test-instances` | `id`, `cycle-id`, `test-id`, `status`, `exec-date`, `exec-time`, `assign-rcyc`, `last-modified` | lo stato attuale di ogni test pianificato |
| `tests` | `id`, `name` | il nome del test (solo quelli che Regia non conosce) |
| `runs` | `id`, `name`, `cycle-id`, `testcycl-id`, `test-id`, `status`, `execution-date`, `execution-time`, `duration`, `last-modified` | lo storico delle esecuzioni |
| `defects` | `id`, `name`, `status`, `severity`, `detected-in-rel`, `detected-in-rcyc`, `last-modified` | i difetti aperti del change |
| `defect-links` | `first-endpoint-id`, `second-endpoint-id`, `second-endpoint-type` | a quale esecuzione, istanza o test set è legato un difetto |

- **Non legge:** passi delle esecuzioni, allegati, descrizioni, commenti, requisiti, Test Plan oltre al nome dei test.
- **Chi ha eseguito il test** (`owner` dell'esecuzione) è un dato personale: di default **non si legge**. Si conserva solo se chi configura attiva «Conserva il nome di chi esegue». Se la si spegne, i nomi già letti si cancellano.

## 2. L'accesso ad ALM

Regia prova da sola il metodo giusto per la versione. La prova di connessione dice quale ha funzionato.

### API key (consigliata, da ALM 12.60)
1. In **Site Administration → API Key Management** crea una chiave per un utente di servizio. Danne una per ogni strumento.
2. Copia il **client id** e il **segreto**. Il segreto si vede solo alla creazione.
3. L'utente collegato alla chiave deve vedere i progetti in **sola lettura** (gruppo Viewer o equivalente).

Regia entra con `authentication-point/alm-authenticate`, usando il client id come utente e il segreto come password: è il metodo consigliato da ALM 15.00. Se ALM lo rifiuta (versioni da 12.60 a 14.x), riprova con `rest/oauth2/login`.

### Utente di servizio
Utente e password di un utente ALM, con sola lettura sui progetti. Regia entra con `authentication-point/alm-authenticate` (da ALM 12.00).

### Sessione e licenze
Dopo l'accesso Regia apre una sessione REST (`rest/site-session`) e la chiude alla fine di ogni lettura o prova. **Una sessione aperta occupa una licenza di ALM.** Per questo l'intervallo minimo è 10 minuti (predefinito 60).

Da ALM 16.00 le chiamate che non sono letture vogliono l'intestazione `X-XSRF-TOKEN`: Regia la manda sempre, se ALM ha dato il cookie.

## 3. Configurarlo in Regia

| Campo | Predefinito | Note |
|---|---|---|
| Indirizzo | — | quello di `qcbin`, per esempio `https://alm.azienda.it/qcbin`. Se manca, Regia aggiunge `/qcbin` |
| Accesso | API key | oppure utente di servizio. I segreti sono cifrati e non tornano mai indietro |
| Progetti | — | uno o più, dominio e progetto **come in ALM** (maiuscole comprese) |
| Numero di change | campo del test set `user-01` | vedi §4 |
| Espressione del numero | `[A-Za-z]{2,10}[0-9]{4,12}` | estrae il numero dal valore: «CHG0045120 - regressione» diventa `CHG0045120` |
| Applicazione | progetto | vedi §5 |
| Stati chiusi dei difetti | Closed, Rejected | gli altri stati contano come aperti. Cambiarli vale subito anche per i difetti già letti |
| Fuso del server | Europe/Rome | le date di ALM non hanno fuso: sono nell'ora del server |
| Prima lettura | 90 giorni indietro | da 1 a 1095 |
| Intervallo | 60 minuti | da 10 minuti a 24 ore |
| Rete interna, proxy, CA interna | spenti | come gli altri connettori (§8) |

Il nome dei campi utente (`user-01`, `user-template-03`…) si vede in ALM in **Customization → Project Entities**: è il nome del campo, non l'etichetta.

## 4. Dove sta il numero di change

Il numero di change collega i test al change di Regia, quindi a rilasci, richiesta, certificazione e gate. Il collegamento è **per numero o alias** del change (tabella `changes`), come per ServiceNow, e si fa alla lettura: resta giusto anche se il change di Regia nasce dopo.

| Scelta | Cosa succede |
|---|---|
| **Campo del test set** (predefinito) | ogni test set porta il numero in un suo campo, per esempio `user-01` |
| **Nome del test set** | il numero si cerca nel nome con l'espressione, per esempio «CHG0045120 regressione» |
| **Campo del ciclo** | tutti i test set del ciclo vanno al numero scritto nel ciclo. Il ciclo di un test set è quello delle sue istanze (`assign-rcyc`) |
| **Campo della release** | tutti i test set della release vanno al numero scritto nella release |

Se si cambia questa scelta, la lettura successiva riparte dalla finestra iniziale con la regola nuova.

## 5. Applicazioni

Regia propone a quale applicazione del catalogo corrisponde ogni **unità** ALM. Una persona conferma la proposta, come per i repository e i servizi CMDB.

| Scelta | Unità |
|---|---|
| **Progetto** (predefinita) | un progetto ALM = un'applicazione. Nel nome i trattini bassi valgono come spazi |
| **Cartella** | le cartelle di primo livello del Test Lab |
| **Campo del test set** | i valori di un campo (per esempio `user-02`) con il codice o il nome dell'applicazione |

I test set di un'unità non confermata si leggono lo stesso e compaiono nella certificazione, ma senza applicazione. Confermare, cambiare o togliere un'associazione aggiorna subito anche i test set già letti.

## 6. Quando legge

- Ogni intervallo, e con **Sincronizza ora**. **Rileggi tutto** riparte dalla finestra iniziale.
- In modo incrementale: per ogni progetto e collezione un cursore su `last-modified`, salvato insieme ai dati. Il filtro è «dal cursore in poi»: le entità con la stessa ora si rileggono, senza doppioni.
- Un test set citato da istanze o esecuzioni recenti, ma modificato prima della finestra, si legge per id.
- Al massimo 5000 entità per collezione e progetto a ogni lettura; il resto alla successiva.

## 7. Effetti: certificazione, gate, evidenza

### Certificazione del rilascio
Nella sezione **Test** compare una riga per ogni test set del change, con origine **ALM**. Ogni riga dà:
- i test pianificati, superati, falliti, bloccati e non eseguiti, con il link ad ALM;
- l'applicazione (o il progetto), il ciclo o la release.

Sotto ci sono i **difetti aperti per gravità** collegati al change, con l'elenco. Un difetto è del change quando è legato a un test set del change, a una sua istanza o a una sua esecuzione. Con il numero sul ciclo o sulla release conta anche il ciclo o la release di rilevazione.

I numeri contano lo **stato attuale** di ogni test pianificato, cioè l'esito dell'ultima esecuzione. «Eseguiti» = superati + falliti + non completati. I non applicabili (N/A) non contano nella percentuale.

La gravità di ALM si normalizza così: «5-Urgent» è critica; «4-Very High» e «3-High» sono alte; «2-Medium» è media; «1-Low» è bassa. Si riconoscono anche le parole (bloccante, alta, media, bassa). Nella pagina resta anche il valore originale.

### Regola del gate «Test del change»
In **Configurazione → Gate di rilascio**, per ambiente:

| Parametro | Esempio | |
|---|---|---|
| Falliti tollerati | 0 | |
| Bloccati tollerati | vuoto | vuoto = non contano |
| Eseguiti almeno | 100% | sul totale senza i non applicabili |
| Difetti aperti, dalla gravità | alta | critica, alta, media, bassa, oppure non contano |
| Difetti aperti tollerati | 0 | |
| Senza test il gate fallisce | sì | |

Contano i test set del change dell'applicazione del rilascio, più quelli senza applicazione: un change con più applicazioni non blocca un'applicazione per i test di un'altra.

### Evidenza GRC
Se in **Gate di rilascio → Evidenze** un controllo è mappato sulla sorgente **Test del change** (`tests`), generando la certificazione nasce anche un'evidenza «Esito dei test del change …» con i numeri. Il collettore è `devsecops.tests`.

## 8. Rete, proxy e certificati

Come per ServiceNow:
- **Rete interna:** un ALM nella rete aziendale vuole «Rete interna», che l'operatore deve consentire con `REGIA_CONNECTOR_ALLOW_PRIVATE`.
- **Proxy:** «Usa il proxy» passa dal proxy in uscita dell'operatore (`REGIA_CONNECTOR_PROXY`).
- **CA interna:** un certificato firmato dalla CA aziendale vuole la CA nel campo «Certificato della CA».

Se davanti ad ALM c'è un accesso SSO che risponde con una pagina HTML, la prova di connessione lo dice: serve un percorso di `qcbin` raggiungibile senza SSO, o l'API key.

## 9. Provarlo

**Senza ALM:** `regia dev fake-alm` avvia un finto ALM con dati inventati, nel dominio `QUALITA` con i progetti `Ordini_online` e `Pagamenti`.
- Ci sono test set dei change `CHG0045120` e `CHG0045121`, un test fallito, uno bloccato, uno non eseguito e due difetti.
- Avvia Regia con `--dev`, che accetta `http://127.0.0.1`.
- Indirizzo `http://127.0.0.1:8191/qcbin`. API key: client id `regia-finto`, segreto `finto-segreto-sviluppo`. Oppure utente `regia.lettura`, password `finta-password-sviluppo`.
- Numero di change nel campo `user-01`. Applicazione dal progetto, dalla cartella di primo livello o dal campo `user-02`.
- `--legacy` simula ALM da 12.60 a 14.x, dove l'API key entra solo da `rest/oauth2/login`.

Per vedere la certificazione serve il change in Regia: lo crea un rilascio inviato dalla pipeline con quel numero di change.

## 10. Permessi, audit e limiti

- **Permessi:**
  - integrazioni, unità, sincronizzazioni: `tenant.admin`, anche con un token API; le modifiche della configurazione solo con una sessione;
  - test set letti: chi legge le applicazioni (`element.read`).
- **Audit.** Ogni creazione, modifica, cancellazione, prova, scoperta, associazione e sincronizzazione è nel registro di audit (`devsecops.alm.*`), senza segreti.
- **Limiti:**
  - al massimo 5 integrazioni ALM per organizzazione e 50 progetti per integrazione;
  - 5000 entità per collezione e progetto a ogni lettura.
- **Link ad ALM:** sono nel formato `td://progetto.dominio.server/qcbin/…` che ALM copia con «Copia URL». Si aprono con il client di ALM o con ALM Client Launcher.
