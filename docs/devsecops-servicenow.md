# Connettore ServiceNow (in sola lettura)

ServiceNow resta il sistema degli incidenti, delle change e della CMDB. Regia **lo integra e non lo sostituisce**:
- legge gli incidenti più gravi e le change request dei servizi CMDB associati alle applicazioni del catalogo;
- li usa per le metriche DORA, per la proposta di un incidente di Regia (da classificare DORA/NIS2) e per il gate di rilascio.

**Regia non scrive mai su ServiceNow:** niente incidenti creati, niente note, niente cambi di stato. Quando serve è ServiceNow a chiamare Regia, per esempio un flow che interroga il gate prima del CAB (§9).

Dove: **Configurazione → Integrazioni → ServiceNow**. Serve il permesso «Amministrare l'organizzazione». Nella scheda dell'integrazione un elenco in cinque passi dice a che punto sei e indica il prossimo:
1. credenziali;
2. prova;
3. servizi scoperti;
4. servizi associati;
5. prima sincronizzazione.

## 1. Cosa legge, e cosa no

| Tabella | Campi letti | Perché |
|---|---|---|
| la classe CMDB scelta (predefinita `cmdb_ci_service`) | `sys_id`, `name`, `sys_class_name`, il campo del codice se indicato | scoperta dei servizi da associare alle applicazioni |
| `incident` | `sys_id`, `number`, `priority`, `state`, `opened_at`, `resolved_at`, `closed_at`, `short_description`, `cmdb_ci`, `business_service`, `sys_updated_on`, `sys_created_on` | tempo di ripristino, tasso di fallimento, proposta di incidente |
| `change_request` | `sys_id`, `number`, `state`, `approval`, `type`, `start_date`, `end_date`, `work_start`, `work_end`, `short_description`, `cmdb_ci`, `business_service`, `sys_updated_on` | collegamento a rilasci e richieste, gate |

Regia chiede solo questi campi (`sysparm_fields`).
- **Non legge:** chiamante, assegnatario, gruppi, commenti, note di lavoro, allegati. Contengono dati personali e a Regia non servono.
- **Conserva:** il minimo, con il link al record in ServiceNow.
- **Titolo breve:** si conserva fino a 300 caratteri. Se nella tua organizzazione contiene nomi di persone, è un dato da tenere presente nel registro dei trattamenti.

## 2. L'utente su ServiceNow

### Utente di servizio, autenticazione Basic (predefinita, ogni release)

1. *Utenti* (`sys_user`) → **Nuovo**:
   - un utente dedicato, per esempio `regia.lettura`;
   - **Accesso solo ai web service** (*Web service access only*): non entra dall'interfaccia;
   - **Utente di integrazione interno** (*Internal Integration User*), dove c'è;
   - una password lunga.
2. **Ruoli, solo lettura.** L'utente deve poter **leggere** `incident`, `change_request` e la classe CMDB scelta, con i campi del §1. Ci sono due strade:
   - **ruolo dedicato (meglio):** per esempio `u_regia_lettura`, con le ACL di lettura (`read`) sulle tre tabelle e sui loro campi;
   - **ruoli già esistenti** che danno la lettura (spesso `itil`), più **`snc_read_only`**, che blocca qualunque scrittura all'utente.

   Verifica con l'amministratore della piattaforma quale ruolo usa la tua azienda e se un criterio di accesso alla REST API (*REST API Access Policy*) chiede un ruolo in più.
3. Le ACL che mancano **non sempre danno errore**: ServiceNow toglie in silenzio le righe che l'utente non può leggere. La prova di connessione ti avvisa se una tabella risponde senza record.

Regia non può verificare che l'utente sia davvero in sola lettura: è una responsabilità di chi lo crea. La password è cifrata con la chiave applicativa della cartella dati e **non torna mai** dalle API: l'interfaccia dice solo se c'è. Se lasci vuoto il campo in una modifica, il valore salvato resta. La password non compare nei messaggi né nei log.

### OAuth 2.0 client credentials (da Washington DC)

Il *client credentials grant* in ingresso c'è da Washington DC, ma è spento finché non si accende.
1. Un amministratore imposta la proprietà di sistema `glide.oauth.inbound.client.credential.grant_type.enabled` a `true`.
2. *System OAuth* → *Application Registry* → **Nuovo** → *Crea un endpoint OAuth API per client esterni*.
3. Nell'applicazione scegli l'**utente dell'applicazione OAuth**: è l'utente di servizio del paragrafo sopra. Il token ha i suoi ruoli, quindi valgono le stesse regole di sola lettura.
4. Copia *Client ID* e *Client Secret* in Regia.

Regia chiede il token a `/oauth_token.do` e lo riusa fino a poco prima della scadenza, che è la *durata del token di accesso* dell'applicazione. Se ServiceNow nega il token, il messaggio ricorda la proprietà, l'utente dell'applicazione e le credenziali.

## 3. Configurarlo in Regia

**Configurazione → Integrazioni → ServiceNow → Nuova integrazione.**

| Campo | Valore |
|---|---|
| Istanza | `https://azienda.service-now.com`, oppure solo il nome `azienda` (Regia aggiunge `.service-now.com`), oppure un indirizzo con un prefisso. Serve https |
| Autenticazione | **Utente di servizio (Basic)**: utente e password. **OAuth client credentials**: id e segreto del client |
| Classe CMDB dei servizi | la tabella dei CI che rappresentano le applicazioni (§5). Predefinita `cmdb_ci_service` |
| Filtro dei servizi | facoltativo, una *encoded query* sulla classe, per esempio `operational_status=1` o `support_group.name=Applicazioni web` (al massimo 3000 servizi) |
| Campo del codice | facoltativo: il campo del CI con il codice dell'applicazione (per esempio `u_codice_applicazione` o `correlation_id`), usato per proporre l'associazione |
| Priorità degli incidenti | quali leggere, da 1 a 5. Predefinite **1 e 2** |
| Metriche DORA | **solo dopo conferma** (predefinita) oppure **contano da soli** (§6) |
| Prima lettura | quanti giorni indietro alla prima sincronizzazione e con «Rileggi tutto» (predefinito 90) |
| Intervallo | ogni quanti minuti leggere (da 5 a 1440, predefinito 30) |
| Rete interna, Usa il proxy, CA interna | vedi §8 |

Poi:
1. **Prova la connessione.**
   - Regia si autentica e legge un record da ciascuna tabella: `incident` con le priorità scelte, `change_request` e la classe CMDB.
   - Il messaggio dice con chi ha parlato («Connessione riuscita con https://azienda.service-now.com come «regia.lettura» (utente di servizio, Basic)»). Poi elenca le tabelle con l'esito di ciascuna.
   - Spiega anche i problemi: credenziali rifiutate (401), tabella senza ACL di lettura (403), classe inesistente (404), certificato TLS, rete, proxy.
   - Un'istanza di sviluppo in ibernazione risponde con una pagina HTML: il messaggio lo dice.
2. **Servizi → Scopri i servizi** (§5).
3. **Sincronizzazioni → Sincronizza ora**, oppure si aspetta il job periodico.

## 4. Quando legge

Il job controlla ogni minuto le integrazioni dovute e le esegue una alla volta. Un lease nel database garantisce una sola sincronizzazione per integrazione anche con più processi. Per ogni servizio associato:
- **Incidenti** delle priorità scelte il cui *Servizio* (`business_service`) **o** *CI* (`cmdb_ci`) è il servizio: `priorityIN1,2^business_service=<id>^ORcmdb_ci=<id>^sys_updated_on>=<cursore>`.
- **Change request** dello stesso servizio, con lo stesso criterio.
- La lettura è **incrementale**:
  - ogni servizio ha un cursore su `sys_updated_on`, salvato nella stessa transazione dei record;
  - un servizio associato più tardi parte dalla sua prima lettura, non dal cursore degli altri;
  - rileggere non duplica nulla, perché i record si aggiornano per `sys_id`;
  - al massimo 2000 record per tabella e servizio per giro: il resto arriva al giro dopo.
- **Change già note a Regia:** i numeri di change arrivati con i rilasci (`change_number` e alias) si leggono anche se la change è di un servizio non associato. Sono al massimo 300 per giro, le più recenti non ancora chiuse, e servono al gate.

Un `429` con `Retry-After` si rispetta fino a un minuto. Oltre, la sincronizzazione si ferma con lo stato «limite di velocità» e riprende dopo. Un incidente la cui priorità scende sotto la soglia non si rilegge più e resta com'era: lo si esclude a mano (§6).

## 5. Servizi CMDB e associazione

**Quale classe.** Con CSDM le applicazioni sono di solito *application service* (`cmdb_ci_service_auto` e sottoclassi) o *business service*. Tutti e due estendono `cmdb_ci_service`, che è anche la tabella del campo *Servizio* di incidenti e change. Per questo il predefinito `cmdb_ci_service` li comprende tutti e la scoperta mostra la classe di ogni servizio: vedi subito quale usa la tua azienda e, se serve, restringi la classe o aggiungi un filtro.
- `cmdb_ci_business_app` (*business application*) è l'applicazione «logica» dell'APM: gli incidenti la citano di rado.
- `cmdb_ci_appl` sono istanze tecniche installate.

**Scopri i servizi.** Regia legge la classe (con il filtro) e propone per ogni servizio l'applicazione del catalogo con il nome o il codice più vicino, con il motivo: per esempio «codice del CI «ORD» (il nome coincide con il codice «ORD»)».
- Le proposte si confermano una per una o in blocco.
- Si può scegliere un'altra applicazione o ignorare un servizio.
- **Solo i servizi associati si sincronizzano.**
- Se l'associazione si annulla, gli incidenti e le change già letti escono dall'applicazione e dalle metriche. Restano nello storico dell'integrazione.

## 6. Incidenti: metriche DORA e proposta di incidente di Regia

**Metriche DORA.** Un incidente ServiceNow di un'applicazione associata entra nel **tempo di ripristino** (da `opened_at` a `resolved_at`, oppure `closed_at` se manca) e nel **tasso di fallimento** (un deploy in produzione seguito da un incidente entro la finestra configurata). Le regole sono due:
- **solo dopo conferma** (predefinita): conta quando una persona lo conferma («Conta nelle DORA») o lo trasforma in incidente di Regia;
- **contano da soli**: contano tutti, tranne quelli segnati «Non rilevante».

Gli incidenti annullati (stato 8) non contano mai. Nel dettaglio delle metriche un incidente ServiceNow si riconosce dal numero, che porta al record.

**Proposta di incidente di Regia.** In **Rischi e incidenti → Incidenti** chi può segnalare un incidente vede le **proposte da ServiceNow**: gli incidenti letti non ancora collegati né esclusi. Per ognuna:
- **Crea incidente di Regia** apre il modulo del nuovo incidente già compilato con titolo, istante di rilevazione, applicazioni impattate, e una descrizione con numero, priorità e link. La persona lo corregge e lo conferma.
- La **classificazione DORA/NIS2** si fa poi nella scheda dell'incidente, come per ogni incidente. **Regia non crea mai da sola un incidente**, tanto meno uno maggiore.
- Da quel momento l'incidente ServiceNow conta **una volta sola**, tramite quello di Regia.
- **Conta nelle DORA** lo conta senza creare un incidente. **Non rilevante** lo esclude.

Nella scheda dell'applicazione, la sezione ServiceNow mostra gli ultimi incidenti e le change.

## 7. Change e gate di rilascio

Le change lette si collegano ai **change di Regia** per numero o alias, e quindi a rilasci e richieste. Il collegamento si fa alla lettura, quindi resta giusto anche quando il change di Regia nasce dopo.

Nella regola **Change approvato** del gate (Configurazione → Gate di rilascio) c'è l'opzione **Approvazione di ServiceNow**:

| Valore | Effetto |
|---|---|
| non conta (predefinito) | come prima: conta solo la fase della richiesta di Regia collegata |
| basta | se la change in ServiceNow è `approved` (e non annullata) la regola passa anche senza la fase; se è `rejected` blocca; altrimenti decide la fase |
| serve | la change deve essere `approved` in ServiceNow, e in più nella fase indicata, se ce n'è una |

Il gate cerca la change con il numero ricevuto (`change`), con gli alias del change di Regia, oppure tra i change della richiesta indicata. Il dato è quello dell'ultima sincronizzazione. Il webhook (§10) lo tiene fresco quando una change viene approvata.

## 8. Rete, proxy e certificati

- **Istanze SaaS** (`*.service-now.com`): indirizzi pubblici in https, nessuna configurazione in più.
- **Proxy in uscita.** In molte aziende l'unica strada verso un servizio SaaS è un proxy.
  - L'operatore lo imposta in modo esplicito con `REGIA_CONNECTOR_PROXY=http://proxy.azienda.it:3128`; se servono credenziali vanno nell'URL, `http://utente:password@proxy:3128`.
  - L'integrazione attiva **Usa il proxy**.
  - Regia **non usa mai** `HTTP_PROXY`, `HTTPS_PROXY` e `NO_PROXY`.
  - Con il proxy la destinazione si controlla prima della richiesta: se il nome si risolve a un indirizzo interno la chiamata non parte. Se non si risolve localmente la risolve il proxy, che filtra la destinazione finale.
  - Le credenziali del proxy vanno solo al proxy e non compaiono nei messaggi. Un `407` dice che mancano o sono sbagliate.
- **Istanza nella rete interna**, cioè un'istanza ospitata in casa o un reverse proxy interno: l'operatore imposta `REGIA_CONNECTOR_ALLOW_PRIVATE=true` e l'integrazione attiva **Rete interna**. Gli indirizzi link-local (i metadati del cloud) restano bloccati.
- **CA aziendale** (proxy con ispezione TLS): incollala in **CA interna** (PEM).

## 9. Esempio: ServiceNow chiede il gate di Regia prima del CAB

Questo esempio è **solo documentato**: lo scrive e lo mantiene chi amministra ServiceNow. Regia non installa nulla su ServiceNow.

**In Regia:**
1. Un membro tecnico con il ruolo **Pipeline**, per esempio `servicenow@azienda.it`.
2. Un **token API** con il solo permesso «Chiedere il gate di rilascio» (`devsecops.gate`).

**In ServiceNow:**
1. Il token va in una credenziale o in un alias di connessione (*Connection & Credential Aliases*), non in chiaro nello script.
2. Una business rule *async* sulla tabella `change_request`, che scatta quando la change entra in *Authorize* (`state` cambia in `-3`). In alternativa, un flow di Flow Designer con un passo che esegue lo stesso script.

```javascript
(function executeRule(current, previous) {
  // Il codice dell'applicazione: qui dal campo del servizio; adatta al tuo CMDB.
  var app = current.business_service.u_codice_applicazione + '';
  var r = new sn_ws.RESTMessageV2();
  r.setEndpoint('https://regia.azienda.it/api/v1/devsecops/gate');
  r.setHttpMethod('post');
  r.setRequestHeader('Content-Type', 'application/json');
  r.setRequestHeader('Authorization', 'Bearer ' + getRegiaToken()); // dalla credenziale, non in chiaro
  r.setRequestBody(JSON.stringify({
    format: 'regia.devsecops/v1',
    application: app,
    environment: 'prod',
    change: current.getValue('number')
  }));
  var resp = r.execute();
  var code = resp.getStatusCode(); // 200 passa o avvisa, 409 blocca
  var out = JSON.parse(resp.getBody() || '{}');
  var lines = ['Gate di Regia: ' + out.result + ' — ' + out.summary];
  (out.reasons || []).forEach(function (x) {
    if (x.status !== 'pass') lines.push('- ' + x.label + ': ' + x.message);
  });
  lines.push(out.url || '');
  // La nota la scrive ServiceNow sulla propria change: Regia resta in sola lettura.
  current.work_notes = lines.join('\n');
  current.update();
})(current, previous);
```

Il CAB trova nella change l'esito e il link alla decisione registrata in Regia: requisiti, vulnerabilità, controlli della pipeline, finestra. Due accortezze:
- **Prima del CAB** la change non è ancora approvata: un gate che **richiede** l'approvazione di ServiceNow (§7) qui risponderebbe sempre «blocca». Per questa chiamata usa un ambiente o una regola senza quel requisito, oppure leggi solo gli altri motivi.
- **Prima del deploy**, la pipeline chiede di nuovo il gate (vedi [devsecops-api.md](devsecops-api.md), §4 bis): lì l'approvazione di ServiceNow può **bastare** o **servire**.

## 10. Webhook (facoltativo)

Il job periodico è il meccanismo principale. Se ServiceNow raggiunge Regia, un webhook rende i dati più freschi, per esempio una change appena approvata:

1. Nell'integrazione: **Genera il segreto del webhook**. Si mostra una volta. In alternativa lo scegli tu, da 16 caratteri, e lo scrivi nel campo.
2. In ServiceNow, una business rule *async* (dopo inserimento e aggiornamento) su `incident` e su `change_request`:

```javascript
(function executeRule(current, previous) {
  var secret = getRegiaWebhookSecret(); // da una proprietà o credenziale protetta
  var body = JSON.stringify({ table: current.getTableName(), sys_id: current.getUniqueValue(), number: current.getValue('number') });
  var mac = new GlideCertificateEncryption().generateMac(GlideStringUtil.base64Encode(secret), 'HmacSHA256', body);
  var r = new sn_ws.RESTMessageV2();
  r.setEndpoint('https://regia.azienda.it/api/v1/devsecops/servicenow/webhook/<id-integrazione>');
  r.setHttpMethod('post');
  r.setRequestHeader('Content-Type', 'application/json');
  r.setRequestHeader('X-Regia-Signature', mac); // HMAC-SHA256 in base64
  r.setRequestHeader('X-Regia-Delivery', current.getUniqueValue() + ':' + current.getValue('sys_mod_count'));
  r.setRequestBody(body);
  r.executeAsync();
})(current, previous);
```

- **Firma.** Regia ricalcola HMAC-SHA256 del corpo con il segreto e lo confronta in tempo costante. Accetta la firma in base64, che è ciò che restituisce `generateMac`, oppure in esadecimale, anche con il prefisso `sha256=`. Verifica la firma di `generateMac` sulla tua release.
- **Errori.** Senza firma o con una firma errata risponde `401`. Un'integrazione senza segreto risponde `404`.
- **Consegne.** Ogni consegna vale una volta sola: si riconosce da `X-Regia-Delivery`, altrimenti da `Idempotency-Key`, altrimenti dall'impronta del corpo.
- **Contenuto.** Regia non si fida del corpo: avvia una sincronizzazione mirata della tabella indicata (`incident` o `change_request`) e rilegge da ServiceNow con le sue credenziali. Le altre tabelle si ignorano.

## 11. Provarlo

- **Senza ServiceNow:** `regia dev fake-servicenow`. È un finto ServiceNow con dati inventati: tre servizi, incidenti P1-P3 e change approvate e no.
  - Avvia Regia con `--dev`, che accetta `http://127.0.0.1`.
  - Istanza `http://127.0.0.1:8190`, utente `regia.lettura`, password `finta-password-sviluppo`, oppure il client OAuth `regia-finto`.
  - Campo del codice: `u_codice`.
- **Con un ServiceNow vero senza toccare quello aziendale:** una *Personal Developer Instance* gratuita, da developer.servicenow.com.
  - Ha i dati dimostrativi di ITSM e va in ibernazione dopo un periodo di inattività: risvegliala prima della prova.
  - Crea l'utente di servizio come al §2, associa un paio di servizi e confronta incidenti e change con quelli dell'istanza.

## 12. Permessi, audit e limiti

- **Permessi:**
  - integrazioni, scoperta, sincronizzazioni: `tenant.admin`, anche con un token API; le modifiche della configurazione solo con una sessione;
  - incidenti e change letti: chi legge le applicazioni (`element.read`);
  - proposte, «Crea incidente di Regia», «Conta nelle DORA», «Non rilevante»: chi può segnalare un incidente (`element.write` o `incident.manage`).
- **Audit.** Ogni creazione, modifica, cancellazione, prova, scoperta, associazione, sincronizzazione e decisione è nel registro di audit (`devsecops.servicenow.*`), senza segreti.
- **Limiti:** al massimo 5 integrazioni ServiceNow per organizzazione e 3000 servizi per integrazione.
