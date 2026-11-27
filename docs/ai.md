# Intelligenza artificiale

L'AI di Regia è **spenta finché non la accendi**. Quando è accesa propone e la persona conferma: legge solo ciò che la persona può vedere, ogni chiamata finisce nel registro e, se la spegni o il budget finisce, tutto il resto funziona come prima.

Dove: **Configurazione → Intelligenza artificiale** e **Configurazione → Consumo dell'AI** (serve il permesso «Amministrare l'organizzazione»). Le modifiche si fanno solo da una sessione, non con un token API.

C'è il pannello **«Chiedi all'assistente»** nel menu, con due modi:

- **Conversazione:** si fanno domande sui dati, l'assistente cerca e legge con i permessi della persona e risponde con i link agli elementi. Le risposte lunghe si vedono crescere mentre arrivano. Commenti, ore, nuovi elementi, modifiche e collegamenti arrivano come **proposte**: non cambia niente finché la persona non preme **Conferma**.
- **Domanda sul grafo:** la persona chiede quali elementi vuole vedere («Quali richieste aperte sono urgenti?», «Quali applicazioni hanno vulnerabilità scadute?», «Applicazioni collegate a rischi aperti») e l'assistente scrive la **query di Regia**, anche con le relazioni (`collegata: (tipo: risk fase: aperta)`) e con gli impatti dell'analisi (`tipo: application impattata_da: (tipo: business_request urgenza: Alta)`). La query resta visibile: la persona la cambia, la riesegue, apre il risultato nell'elenco **Domande sul grafo** e la **salva come vista**. Se la prima query non è valida, l'assistente la corregge una volta sola e lo dice; se non ci riesce nemmeno così, lo dice e la query si corregge a mano. La query si scrive anche senza AI, nello stesso elenco.

Nella **scheda di ogni elemento** (richiesta, progetto, applicazione, attività…) c'è il riquadro **Assistente**, e nel form di una **nuova richiesta** l'assistente cerca i duplicati e propone i campi (sezione 8).

Con il **server MCP** (sezione 7) Claude Desktop, Claude Code o un IDE lavorano su Regia con i permessi della persona, con il suo modello.

Nel **profilo** ogni persona ha la sua **wiki personale** (sezione 9): appunti in Markdown che l'assistente, se la persona lo vuole, tiene aggiornati e usa come contesto. Senza AI si scrive a mano.

Nella **conformità** (sezione 10) l'assistente propone i collegamenti fra evidenze e controlli, scrive bozze di policy e runbook e prepara la bozza della notifica DORA di un incidente grave.

## 1. Il fornitore

**Aggiungi fornitore** e scegli il tipo. Puoi averne più d'uno (per esempio Ollama sulla tua rete per la wiki e Claude per l'analisi).

| Tipo | Quando | Cosa serve | Dove vanno i dati |
|---|---|---|---|
| **Anthropic (Claude)** | i modelli Claude con l'API di Anthropic | una chiave dell'API | Anthropic |
| **Anthropic via AWS Bedrock** | Claude dal tuo account AWS, in una regione che scegli (per esempio europea) | regione e chiave API di Bedrock o chiavi d'accesso IAM | la regione AWS scelta |
| **Anthropic via Google Vertex AI** | Claude dal tuo progetto Google Cloud, in una regione che scegli | regione, progetto e chiave JSON di un service account | la regione Google Cloud scelta |
| **OpenAI** | i modelli GPT con l'API di OpenAI | una chiave dell'API; l'indirizzo solo se OpenAI te ne ha dato un altro | OpenAI |
| **Google Gemini** | i modelli Gemini con l'API di Google | una chiave dell'API (Google AI Studio) | Google |
| **Ollama** | modelli installati sul tuo server Ollama | l'indirizzo del server; la chiave solo se un proxy la chiede | la tua rete |
| **Servizio compatibile OpenAI** | altri servizi con il protocollo di OpenAI: Azure OpenAI, Mistral, vLLM, LM Studio | l'indirizzo con il prefisso dell'API; la chiave se il servizio la chiede | il servizio |

Regole comuni:

- **Chiavi e credenziali** (anche le chiavi d'accesso AWS e la chiave del service account) si cifrano con la chiave applicativa della cartella dati (`<data>/keys/secrets.key`, da tenere nel backup) e **non tornano mai**: nemmeno chi le ha inserite le rivede. Lasciando vuoti i campi in una modifica restano quelle salvate. Nei log e nel registro di audit c'è solo il fatto che ci sono. Regione e progetto invece si vedono: non sono segreti.
- **Prova la connessione** (accanto a ogni fornitore) controlla chiave, indirizzo e credenziali e mostra i modelli, senza consumare budget: con Anthropic, OpenAI e Gemini elenca i modelli dell'account, con Ollama e i compatibili quelli installati; con Bedrock e Vertex, che non hanno l'elenco dei modelli, conta i token di una richiesta minima sul modello di un livello (o su quello proposto), così verifica anche che il modello sia abilitato in quella regione. I modelli trovati compaiono nel campo **Modello** dei livelli.
- **Modelli che non sanno usare gli strumenti.** Il requisito minimo è la chiamata di strumenti. Ollama dichiara le capacità di ogni modello: la prova di connessione segna da sola quelli senza strumenti e lo dice. Per un servizio compatibile scrivili tu nel campo apposito. Con questi modelli l'assistente non legge i dati di Regia, fa solo riassunti e bozze sul testo che riceve, e sia la configurazione sia il pannello lo dicono.
- **Modelli sulla tua rete.** Un indirizzo interno (IP privato, `localhost`, nomi `.lan`, `.local`, `.home.arpa`) richiede che chi gestisce l'installazione lo consenta con `REGIA_CONNECTOR_ALLOW_PRIVATE=true`, come per i connettori. Questi fornitori sono segnati «sulla tua rete»: i dati non escono, quindi non si mascherano. Verso un servizio esterno serve `https://`.
- **Prezzi** (dollari per milione di token, facoltativi): servono a stimare il costo. Vuoti, Regia usa il listino noto dei modelli che propone (Claude, GPT, Gemini; per l'input letto dalla cache il prezzo ridotto di ciascuno) e zero per gli altri, per esempio i modelli locali. Su Bedrock e Vertex i prezzi sono quelli del cloud: se servono stime esatte, scrivili.

### Anthropic (Claude)

1. Nella console di Anthropic crea una chiave dell'API.
2. **Tipo** «Anthropic (Claude)», un nome, la chiave (`sk-ant-…`), **Aggiungi fornitore**, poi **Prova la connessione**.

### Anthropic via AWS Bedrock (dati nella regione UE)

Le richieste vanno all'endpoint Messages di Bedrock nella regione scelta (`https://bedrock-mantle.<regione>.api.aws/anthropic`), con il tuo account AWS.

1. In AWS scegli una regione europea in cui il modello Claude che ti serve è disponibile (per esempio `eu-central-1`, Francoforte, o `eu-south-1`, Milano: la disponibilità dei modelli per regione la dà AWS) e abilita l'accesso al modello in Bedrock.
2. Credenziali, una delle due:
   - una **chiave API di Bedrock** (console di Amazon Bedrock);
   - le **chiavi d'accesso** di un utente IAM dedicato, con il solo permesso di invocare i modelli (ID della chiave `AKIA…` e chiave segreta; il token di sessione solo per credenziali temporanee, che scadono).
3. **Tipo** «Anthropic via AWS Bedrock», un nome, la **regione** (`eu-central-1`), le credenziali, **Aggiungi fornitore**, poi **Prova la connessione**.

I modelli hanno il prefisso `anthropic.`: Regia propone `anthropic.claude-haiku-5-5` (rapido) e `anthropic.claude-opus-5-5` (approfondito). Regia non usa le credenziali né la regione del server (variabili `AWS_*`, profili, ruolo dell'istanza): solo quelle dell'organizzazione.

### Anthropic via Google Vertex AI (dati nella regione UE)

1. Nel progetto Google Cloud abilita Vertex AI e il modello Claude nel Model Garden, in una regione europea in cui è disponibile (per esempio `europe-west8`, Milano) o nella multiregione `eu`.
2. Crea un **service account** con il ruolo «Vertex AI User» e scarica la sua **chiave JSON**.
3. **Tipo** «Anthropic via Google Vertex AI», un nome, la **regione** (`europe-west8` o `eu`), il **progetto** (se lo lasci vuoto si prende dalla chiave), incolla la chiave JSON, **Aggiungi fornitore**, poi **Prova la connessione**.

Su Vertex i modelli hanno il nome di Anthropic (`claude-opus-5-5`). Si accetta solo la chiave di un service account (`"type": "service_account"`) che chiede il token a Google (`https://oauth2.googleapis.com/token`); le credenziali predefinite del server non si usano mai.

### OpenAI

1. Nella piattaforma di OpenAI crea una chiave dell'API.
2. **Tipo** «OpenAI», un nome, la chiave (`sk-…`). L'**indirizzo** lascialo vuoto (`https://api.openai.com/v1`); cambialo solo se OpenAI te ne ha dato un altro, per esempio per la residenza dei dati in Europa.
3. **Aggiungi fornitore**, poi **Prova la connessione**.

Regia usa la Responses API: strumenti, ragionamento che resta fra un passo e l'altro, sforzo di ragionamento per livello, cache del prompt. Le risposte non si conservano da OpenAI (`store: false`). Propone `gpt-6-luna` (rapido) e `gpt-6.2-astra` (approfondito).

### Google Gemini

1. In Google AI Studio crea una chiave dell'API.
2. **Tipo** «Google Gemini», un nome, la chiave, **Aggiungi fornitore**, poi **Prova la connessione**.

La chiave va nell'intestazione della richiesta, mai nell'indirizzo. Regia propone `gemini-4-hydrogen` (rapido) e `gemini-4-carbon` (approfondito).

### Ollama

1. Installa i modelli sul server Ollama (`ollama pull <modello>`), meglio se sanno usare gli strumenti.
2. **Tipo** «Ollama», un nome, l'**indirizzo** del server senza `/v1` (per esempio `http://ollama.lan:11434`), **Aggiungi fornitore**, poi **Prova la connessione**: compaiono i modelli installati, con il più grande proposto per l'approfondito e il più piccolo per il rapido, e quelli senza strumenti segnati.

Per un Ollama sulla tua rete serve `REGIA_CONNECTOR_ALLOW_PRIVATE=true` (vedi sopra). La chiave serve solo se Ollama sta dietro un proxy che la chiede (va come `Authorization: Bearer`).

### Servizio compatibile OpenAI

Per Azure OpenAI, Mistral, vLLM, LM Studio e gli altri servizi con il protocollo di OpenAI. L'**indirizzo** è quello con il prefisso dell'API (`https://<servizio>/v1`); per LM Studio basta `http://localhost:1234`: `/v1` si aggiunge da solo. Scegli i modelli fra quelli che la prova di connessione elenca e segna a mano quelli senza strumenti.

## 2. Livelli e funzioni

Le funzioni non scelgono un modello ma un **livello**:

| Livello | Per cosa | Anthropic | OpenAI | Gemini | Ollama |
|---|---|---|---|---|---|
| **Approfondito** | analisi, bozze, conversazione con l'assistente | `claude-opus-5-5` | `gpt-6.2-astra` | `gemini-4-carbon` | il modello più grande installato |
| **Rapido** | classificare, trovare duplicati, riassunti brevi, domande sul grafo, aggiornare la wiki | `claude-haiku-5-5` | `gpt-6-luna` | `gemini-4-hydrogen` | il più piccolo |

Quando scegli il fornitore di un livello Regia propone il modello (su Bedrock con il prefisso `anthropic.`); lo cambi scrivendo o scegliendo fra quelli trovati dalla prova di connessione. Un solo modello per tutti e due va bene: se un livello è vuoto si usa l'altro, e il rapido propone il modello dell'approfondito se non ne ha uno suo. Con un servizio compatibile scegli tra i modelli installati.

In **Funzioni** puoi spostare una funzione sull'altro livello o su **un modello preciso**, anche di un altro fornitore: per esempio la wiki su un Ollama locale e l'analisi su Claude. Sono attive:

| Funzione | Livello predefinito | Dove |
|---|---|---|
| Assistente | approfondito | pannello «Chiedi all'assistente» |
| Domande sul grafo | rapido | pannello, modo «Domanda sul grafo» |
| Riassunti | rapido | scheda: «Riassumi» |
| Cosa manca per decidere | approfondito | scheda: «Cosa manca per decidere» |
| Bozze | approfondito | scheda: «Scrivi una bozza» |
| Classificazione e duplicati | rapido | nuova richiesta: «Cerca duplicati e proponi i campi» |
| Wiki personale | rapido | profilo → «La mia wiki»: aggiornamento in background |
| GRC assistito | approfondito | schede di evidenze, controlli e rischi; requisito normativo; notifica DORA dell'incidente (sezione 10) |
 Domande sul grafo, funzioni della scheda, collegamenti del GRC e notifica DORA non chiamano strumenti (Regia passa al modello ciò che la persona può leggere): vanno bene anche con un modello che non li sa usare. La **wiki personale** invece li usa (legge con cerca, query e leggi e scrive le pagine con uno strumento suo): con un modello segnato «senza strumenti» l'aggiornamento non parte, e la wiki resta da scrivere a mano. Le **bozze di policy e runbook** usano gli strumenti di lettura se il modello li sa usare (per cercare gli elementi da citare); senza, si scrivono con i dati che Regia passa.

Regia usa la **cache del prompt** dove il fornitore la offre: istruzioni, strumenti e catalogo restano uguali fra le domande e il fornitore li rilegge dalla cache a una frazione del prezzo (Anthropic, anche su Bedrock e Vertex; OpenAI; Gemini con la cache implicita; Ollama tiene il contesto in memoria). I token letti dalla cache sono nel registro e il costo stimato li conta al prezzo ridotto.

### Ragionamento

I modelli che ragionano prima di rispondere (Claude, GPT, Gemini) si regolano con **Ragionamento**: automatico, basso, medio o alto. Più ragionamento vuol dire risposte più accurate, ma più lente e più costose (il ragionamento si paga come testo in uscita).

- **Per livello**, accanto al modello. In automatico il rapido ragiona poco e l'approfondito molto.
- **Per funzione**, sotto la scelta del livello o del modello: vince su quello del livello. Per esempio le bozze ad «alto» e l'assistente a «medio».
- **Richieste brevi.** Domande sul grafo, riassunti della scheda, duplicati e classificazione, riassunti delle conversazioni hanno una risposta breve e strutturata: ragionano poco anche sul livello approfondito, salvo un valore scelto per la funzione.
- Regia lo traduce nel parametro di ogni fornitore: l'*effort* di Claude, il *reasoning effort* di GPT, il *thinkingLevel* di Gemini (su Gemini 3 Pro il medio diventa alto, perché quel modello ha solo basso e alto). Con Ollama e i servizi compatibili il ragionamento lo decide il modello.
- Il ragionamento conta nei token di uscita: Regia allarga da sé il limite di ogni risposta quando il modello ragiona, così una risposta non resta vuota perché il ragionamento ha consumato tutto.

### Conversazioni lunghe

Al modello tornano interi gli ultimi 10 messaggi della conversazione. Quelli più vecchi non si perdono: Regia li riassume con la funzione **Riassunti** (livello rapido, ragionamento basso) e manda il riassunto in testa alla conversazione. Il riassunto si aggiorna a pezzi, solo con i messaggi usciti dalla finestra da allora, e la chiamata compare nel registro con la conversazione. Se il riassunto non riesce (per esempio budget esaurito) la conversazione va avanti con gli ultimi 10 messaggi.

## 3. Budget

Un budget mensile in dollari (al listino del fornitore) e, se vuoi, un tetto di token al mese. Il consumo si calcola dal registro delle chiamate, nel fuso dell'organizzazione.

- **All'80%** compare un avviso nella configurazione, nel consumo e nel pannello dell'assistente, chi amministra l'organizzazione riceve una **notifica** (e l'email, se le email sono attive e non le ha silenziate per il tipo «AI»), e resta una voce nel registro di audit (`ai.budget.warning`). Una volta sola al mese.
- **Al 100%** le chiamate non partono più fino al mese dopo (restano nel registro come rifiutate). Tutto ciò che non usa l'AI funziona come sempre.
- **Prima di ogni chiamata** Regia stima i token del testo che manda. Solo se la stima, raddoppiata per prudenza, si avvicina al limite li conta con le API ufficiali del fornitore (Anthropic, anche su Bedrock e Vertex, OpenAI e Gemini; gratuite ma un giro in più, che così non rallenta le altre chiamate). Una richiesta che da sola porterebbe oltre il budget non parte, e la persona lo vede («il budget non basta per questa richiesta»).

## 4. Dati personali e conservazione

- **Mascheramento verso i fornitori esterni.** Prima di uscire, i nomi delle persone dell'organizzazione, email, telefoni, IBAN e codici fiscali diventano segnaposto (`[persona-1]`, `[email-1]`); nella risposta tornano i valori veri. Per le organizzazioni **regolamentate** (con una normativa attiva come DORA o NIS2) parte acceso; per le altre parte spento. Lo può cambiare solo chi amministra l'organizzazione.
- **Conservazione.** Le conversazioni con l'assistente si cancellano dopo 90 giorni dall'ultimo messaggio (configurabile da 1 a 3650). Sono private della persona e si cancellano anche con il suo account. Il registro delle chiamate resta: non contiene il testo.
- **Wiki personale.** Le pagine restano (è lì che passa ciò che conta delle conversazioni), ma con lo stesso numero di giorni si cancellano la **storia** delle modifiche (resta sempre l'ultima versione di ogni pagina), le pagine **tolte** e le aggiunte a mano già lette dall'assistente. La wiki si cancella con il membro e quando il membro viene anonimizzato; la persona la scarica quando vuole in un file Markdown.

## 5. Accendere

Spunta **AI accesa** e **Salva le impostazioni** (serve almeno il modello di un livello). Il pulsante «Chiedi all'assistente» compare a chi lavora nell'organizzazione. Per spegnere, togli la spunta: il pannello sparisce e il resto non cambia.

## 6. Consumo

**Configurazione → Consumo dell'AI** mostra per mese: chiamate, token, costo stimato, chiamate non riuscite, poi i totali per funzione, per persona e per modello e le ultime 100 chiamate con esito e **impronta** (SHA-256) del testo inviato. Il testo non si registra mai.

## 7. Server MCP

Il server MCP permette a Claude Desktop, Claude Code o un IDE di cercare, leggere e proporre modifiche su Regia **con i permessi della persona** che si collega. Ha un **interruttore suo** in Configurazione → Intelligenza artificiale → **Server MCP**, spento di default e indipendente da «AI accesa»: il modello è quello della persona, quindi non serve un fornitore e il budget dell'organizzazione non si consuma.

Prima di accenderlo considera che il testo letto dagli strumenti va al modello del client della persona (per esempio Claude via Claude Desktop): **Regia non può mascherarlo**. Per un'organizzazione regolamentata decidi tu se è accettabile.

Come collegare i client, con gli esempi: [ai-mcp.md](ai-mcp.md).

## 8. Assistente nella scheda e alla creazione

Compare solo con l'AI accesa e il modello del livello della funzione: senza AI la scheda e i form sono identici.

**Nella scheda** di un elemento, il riquadro **Assistente** ha tre pulsanti:

- **Riassumi:** l'elemento, i commenti e i collegamenti in poche righe, con i link agli elementi citati.
- **Cosa manca per decidere:** prima i controlli di Regia (campi obbligatori vuoti in questa fase, descrizione vuota o troppo breve, nessun collegamento, una richiesta senza impatti), poi ciò che trova il modello: incoerenze fra campi e date, domande nei commenti a cui nessuno ha risposto, collegamenti che mancano. Ogni voce porta al campo, al commento o all'elemento. Se il modello cita un campo, un commento o un elemento che non esiste, la voce resta senza link e il riquadro lo dice.
- **Scrivi una bozza:** un commento, una **risposta al richiedente** (che lo menziona, così riceve la notifica) o una nuova descrizione, con indicazioni facoltative. La bozza si **rilegge e si cambia** in un campo di testo; si pubblica come commento o sostituisce la descrizione **solo quando la persona preme il pulsante**, con i suoi permessi (chi non può commentare o modificare riceve l'errore).

**Nella nuova richiesta**, **Cerca duplicati e proponi i campi** usa la ricerca testuale di Regia (titoli simili e parole in comune, niente vettori) e fa rileggere al modello i candidati:

- **Forse esiste già:** le richieste che sono probabilmente lo stesso bisogno, con il motivo e il link;
- **Valori proposti:** driver, urgenza e gli altri campi a elenco, ognuno con il motivo e il pulsante **Accetta**: un valore entra nel form solo se lo accetti;
- **Asset che potrebbe riguardare:** quelli spuntati si citano nella richiesta dopo il salvataggio.

L'assistente legge solo ciò che la persona può vedere: un richiedente del portale non riceve come duplicati le richieste degli altri.

Anche nel dialogo **Nuovo elemento** della board e del backlog (task, story, bug, epic) c'è **Cerca duplicati e proponi i campi**: elementi simili dello stesso tipo e i valori dei campi a elenco del tipo, da accettare uno per uno. Nel form **nuova applicazione** dell'architettura c'è **Cerca duplicati**. In quelli del **nuovo rischio**, del **nuovo incidente** e del **nuovo requisito** c'è **Cerca duplicati e proponi i campi**: elementi simili dello stesso tipo che potrebbero essere lo stesso (per esempio un incidente già aperto per lo stesso guasto) e i valori degli elenchi del form, ognuno con il motivo e il pulsante **Accetta**: per il rischio probabilità e impatto inerenti fra i livelli della matrice, per l'incidente le applicazioni impattate (si aggiungono a quelle già scelte), per il requisito categoria, ambito e criterio di verifica. Ogni chiamata è nel registro con la sua funzione; le bozze inserite e gli asset citati vi compaiono come «proposta confermata».

## 9. Wiki personale

Dal **profilo** → **Apri la mia wiki** (indirizzo `/profilo/wiki`). Ogni persona ha le sue pagine in Markdown: una per progetto o iniziativa che segue, una per comitato, «Le mie priorità», un «Registro delle decisioni», o quelle che vuole. Gli elementi si citano con la **chiave** (`DEM-12`) o con il **codice** (`APP-MOB`) e diventano link, con lo stato e il titolo visti con i permessi di chi legge: ciò che non si vede resta testo.

- **Privata.** Le pagine le vede solo la persona: nemmeno chi amministra l'organizzazione le legge (la separazione è anche nel database). Una pagina si può **condividere**: le persone dell'organizzazione con il link la leggono (non i richiedenti del portale), ma solo chi la possiede la modifica, e la storia resta sua. Le pagine condivise con te compaiono in «Condivise con te».
- **Promuovi a pagina Knowledge.** Crea una pagina nuova in uno spazio Knowledge in cui la persona può scrivere, con lo stesso titolo e testo. Da lì vive da sola (versioni, revisioni, letture del modulo Knowledge); la pagina della wiki resta com'è e ricorda la chiave.
- **Storia e registro.** Ogni modifica ha l'autore (**tu** o **assistente**), le righe aggiunte e tolte, le **differenze** con la versione prima e si **annulla**: «Annulla» sull'ultima modifica di una pagina (anche una pagina creata dall'assistente: così si toglie), «Ripristina questa versione» per le precedenti. Anche l'annullamento è una modifica, che si annulla a sua volta. Il **registro delle modifiche** nella pagina della wiki mostra le ultime cento, di tutte le pagine.
- **Aggiungi alla wiki.** Nella scheda di ogni elemento (riquadro «La mia wiki» a lato) e sotto ogni risposta dell'assistente: si sceglie la pagina (di partenza «Appunti») e una nota, e l'elemento o la risposta si aggiungono in fondo. Funziona anche senza AI; con l'AI accesa l'assistente lo legge al prossimo aggiornamento.

**Con l'AI accesa** compare il riquadro **Aggiornamento con l'assistente** (serve il modello della funzione «Wiki personale», livello rapido, capace di usare gli strumenti):

- **Lascia che l'assistente tenga aggiornata la wiki** è spento finché la persona non lo accende: è lei a decidere di spendere il budget. Acceso, Regia controlla ogni cinque minuti e aggiorna la wiki al massimo **una volta l'ora**, e solo se c'è qualcosa di nuovo; se non c'è niente di nuovo non chiama il modello. **Aggiorna adesso** parte subito, anche con l'aggiornamento automatico spento.
- **Fonti** (decisione del 2026-11-26): solo gli elementi che la persona **segue o ha creato** e che sono cambiati dall'ultimo aggiornamento, le sue **conversazioni** con l'assistente e ciò che ha **aggiunto a mano**. Il resto si aggiunge a mano. L'assistente può approfondire con cerca, query e leggi, sempre con i permessi della persona: non legge mai ciò che lei non vede.
- **Scrive senza chiedere conferma** pagina per pagina: la wiki è lo spazio della persona e non tocca dati condivisi. Al massimo otto pagine per aggiornamento; una pagina che la persona ha modificato mentre l'aggiornamento era in corso non si sovrascrive. Ogni modifica è nel registro e si annulla.
- Il testo degli elementi arriva al modello come **dato**, non come istruzione; il **mascheramento** dei dati personali, il **budget** e il **registro delle chiamate** (funzione «wiki») valgono come per le altre funzioni. Il riquadro dice com'è andato l'ultimo aggiornamento (pagine cambiate, niente di nuovo, budget esaurito, modello mancante).
- Se l'aggiornamento **non riesce** (budget esaurito, modello mancante o senza strumenti, errore del fornitore) la persona riceve una **notifica** (tipo «AI», anche per email se le ha attive) che porta alla wiki. Una volta sola: la successiva arriva solo dopo un aggiornamento riuscito, così l'aggiornamento automatico non manda un avviso ogni ora.

**Contesto per l'assistente.** Quando la persona fa una domanda nel pannello, chiede un riassunto o una bozza nella scheda o fa una domanda sul grafo, le pagine della sua wiki **pertinenti** (che citano gli stessi elementi o hanno parole in comune) entrano nella richiesta come dato, al massimo cinque pagine e circa **1500 token**. Con il server MCP lo strumento **wiki** legge la wiki di chi si collega, in sola lettura: mai quella degli altri, nemmeno le pagine condivise.

## 10. Conformità (GRC assistito)

Serve il modello della funzione **GRC assistito** (livello approfondito). Senza AI accesa le schede dei controlli, delle evidenze, dei rischi e le notifiche degli incidenti sono identiche a prima. Ogni proposta e ogni bozza è dell'assistente: decide la persona, con i suoi permessi.

**Dall'evidenza ai controlli.** Nella scheda di un'evidenza (scheda «Dettagli»), **Proponi i controlli che soddisfa**: l'assistente legge l'evidenza (titolo, nota, link, la pagina Knowledge se è un'evidenza di tipo pagina, e il testo di al massimo tre allegati: file di testo, Word, OpenDocument e PDF con testo; le immagini e i PDF scansionati non hanno testo da leggere, e l'assistente sa quanti allegati non ha letto) e i controlli dell'organizzazione con i requisiti che coprono, e propone quelli che l'evidenza dimostra davvero, ognuno con il **motivo** e il **grado di copertura** («piena» se basta da sola, «parziale» se ne dimostra una parte). Ogni proposta si **collega** o si **scarta**: niente si collega da sé. Collega chi amministra la conformità o il responsabile del controllo; il grado e il motivo restano sul collegamento e si vedono nell'evidenza («copertura parziale», «proposta dall'assistente»). La copertura della normativa non cambia per il grado: un'evidenza collegata vale come prima, il grado è un'informazione per chi verifica.

**Dal controllo alle evidenze.** Nella scheda di un controllo, **Cerca le evidenze e cosa manca**: le evidenze esistenti non ancora collegate che potrebbero dimostrarlo (con motivo e grado, da accettare una per una) e **cosa manca**: prima ciò che Regia sa da sé (perimetri senza evidenza valida, nessuna evidenza valida), poi ciò che vede l'assistente (tipo di prova, periodo, perimetro), legato al requisito quando c'entra. Serve il permesso di leggere la conformità.

**Bozze di policy e runbook.** Nella scheda di un controllo o di un rischio, e nel pannello di un requisito normativo (Conformità → normativa → requisito), **Scrivi una bozza di policy o runbook**: l'assistente scrive in Markdown una policy (scopo, ambito, riferimenti, regole, ruoli, frequenze, eccezioni, revisione) o un runbook (servizio e reperibilità, passi, verifiche, escalation, evidenze da conservare), con i riferimenti ai controlli, ai requisiti e agli elementi del grafo. Ruoli e frequenze vengono dai dati (responsabile, frequenza del controllo); dove mancano scrive «da definire». Con un modello che sa usare gli strumenti cerca e legge gli elementi da citare, sempre con i permessi della persona. Le chiavi citate diventano link solo se esistono e la persona le vede. La persona **rilegge e cambia** titolo e testo, sceglie lo spazio Knowledge e preme **Salva come bozza in Knowledge**: nasce un **documento controllato in fase «Bozza»** (con il modello «Policy» o «Runbook» se lo spazio lo ha), che documenta il controllo o il rischio. Non si approva mai da sé: lo approvano revisori e approvatori con il processo del documento controllato.

**Bozza della notifica DORA.** In un incidente classificato grave, apri una notifica dello schema DORA (**prepara** o **riprendi la bozza**) e premi **Prepara la bozza DORA**. La bozza segue il **modello ufficiale** del Regolamento di esecuzione (UE) 2025/302 (allegato I, con i numeri dei campi; obblighi per tappa dall'allegato II) e il Regolamento delegato (UE) 2025/301 (contenuto delle tre tappe e termini):

- la **notifica iniziale** ha le informazioni generali (1.1-1.15) e i campi 2.1-2.10; la **relazione intermedia** anche i campi 3.1-3.35; la **relazione finale** anche i campi 4.1-4.16. Con le relazioni successive il modello si ripresenta intero e si aggiornano anche i campi delle tappe prima;
- ogni campo ha uno stato: **dai dati** (Regia lo ricava dall'incidente: chiave, rilevazione, classificazione, criteri soddisfatti, clienti, fermo, durata, perdita di dati, applicazioni con funzione critica, fornitore all'origine, altre notifiche inviate, postmortem, costi, valuta dell'organizzazione), **da verificare** (dedotto da Regia o scritto dall'assistente: descrizione, come è stato scoperto, misure temporanee, cause…) o **mancante**, con **da dove si ricava** (per esempio il LEI dell'entità, che Regia non conserva, o il codice assegnato dall'autorità alla ricevuta della notifica iniziale);
- l'assistente scrive solo i campi descrittivi e solo con ciò che risulta dall'incidente (descrizione, timeline, applicazioni, causa, postmortem); i **dati di contatto** delle persone (1.7-1.12) li compila Regia e non vanno mai al modello.

La persona completa e corregge i campi e preme **Salva come bozza nell'incidente**: il testo in Markdown diventa il contenuto della notifica e una **versione** con i campi, nella storia dell'incidente. Da lì anche le bozze salvate a mano diventano versioni. Ogni versione si **esporta** in **Markdown** o **JSON** dal riquadro «versioni salvate». **Regia non invia la notifica**: si invia dal canale dell'autorità competente e poi si registra l'invio come sempre («Registra l'invio fatto fuori»), da cui partono le scadenze delle tappe successive. Gli schemi diversi da DORA (per esempio NIS2) non hanno il modello: la bozza si scrive a mano.

Ogni proposta, bozza e notifica preparata è nel registro delle chiamate con la funzione «grc»; le decisioni della persona (collegamento accettato o scartato, bozza salvata) vi compaiono come «proposta confermata» o «rifiutata». Budget, mascheramento dei dati personali, contenuto come dato e contesto dalla wiki della persona valgono come per le altre funzioni.

## Sicurezza in breve

- L'assistente usa gli stessi permessi della persona: passa dagli stessi servizi dell'interfaccia, con l'isolamento tra organizzazioni e la visibilità degli spazi.
- Il contenuto degli elementi (titoli, descrizioni, commenti) arriva al modello delimitato come **dato**: un testo che prova a dare ordini all'assistente non viene seguito.
- Le scritture sono sempre proposte da confermare, e alla conferma i permessi si controllano di nuovo. Con il server MCP la conferma è uno strumento separato («conferma»), che il client chiama solo dopo il sì della persona. Nella scheda la conferma è il pulsante sotto la bozza riletta.
- Il server MCP accetta solo token API: non la sessione del browser. Un token di sola lettura vede solo gli strumenti di lettura.
- La wiki personale è separata per persona anche nel database (Row Level Security sul membro); l'assistente la aggiorna con i permessi della persona e ogni sua modifica si annulla.
- Nel GRC l'assistente non collega, non approva e non invia niente: i collegamenti si accettano uno per uno con i permessi della persona (`grc.evidence.link` nel registro di audit), le policy nascono come documenti controllati in bozza (`ai.grc.draft.save`), la notifica DORA si salva come versione (`regulatory_notification.draft_version`) e si invia fuori da Regia.
- Chiavi e credenziali dei fornitori sono dell'organizzazione: Regia non usa mai quelle del server (variabili d'ambiente di Anthropic, AWS o Google, profili, ruolo dell'istanza, chiavi di Ollama sul disco), così un'organizzazione non usa le credenziali di un'altra né quelle di chi gestisce l'installazione.
- Ogni modifica della configurazione è nel registro di audit (`ai.settings.update`, `ai.provider.*` compresa la prova di connessione `ai.provider.test`, `ai.proposal.apply`; `wiki.promote` per le pagine della wiki promosse a Knowledge).
