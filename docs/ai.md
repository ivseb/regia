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

**Aggiungi fornitore** e scegli il tipo.

| Tipo | Quando | Cosa serve |
|---|---|---|
| **Anthropic (Claude)** | i modelli Claude via API | una chiave dell'API, creata nella console di Anthropic |
| **Servizio compatibile OpenAI** | OpenAI, Mistral, vLLM, LM Studio, Ollama, gli endpoint compatibili di Gemini | l'indirizzo del servizio; la chiave solo se il servizio la chiede |

- **La chiave** si cifra con la chiave applicativa della cartella dati (`<data>/keys/secrets.key`, da tenere nel backup) e **non torna mai**: nemmeno chi l'ha inserita la rivede. Lasciando vuoto il campo in una modifica resta quella salvata. Nei log e nel registro di audit c'è solo il fatto che una chiave c'è.
- **L'indirizzo** di un servizio compatibile è quello con il prefisso dell'API (`https://api.openai.com/v1`). Per Ollama e LM Studio basta `http://192.168.1.20:11434` o `http://localhost:1234`: `/v1` si aggiunge da solo.
- **Modelli sulla tua rete.** Un indirizzo interno (IP privato, `localhost`, nomi `.lan`, `.local`, `.home.arpa`) richiede che chi gestisce l'installazione lo consenta con `REGIA_CONNECTOR_ALLOW_PRIVATE=true`, come per i connettori. Questi fornitori sono segnati «sulla tua rete»: i dati non escono, quindi non si mascherano.
- **Verifica e mostra i modelli** controlla chiave e indirizzo senza consumare token: con Anthropic elenca i modelli dell'account, con Ollama e LM Studio quelli installati.
- **Modelli che non sanno usare gli strumenti.** Il requisito minimo è la chiamata di strumenti. Se un modello locale non la sa fare, scrivilo qui: con quel modello l'assistente non legge i dati di Regia, fa solo riassunti e bozze sul testo che riceve, e il pannello lo dice.
- **Prezzi** (dollari per milione di token, facoltativi): servono a stimare il costo. Vuoti, Regia usa il listino noto dei modelli Claude e zero per gli altri.

## 2. Livelli e funzioni

Le funzioni non scelgono un modello ma un **livello**:

| Livello | Per cosa | Con Anthropic Regia propone |
|---|---|---|
| **Approfondito** | analisi, domande sul grafo, bozze, conversazione con l'assistente | `claude-opus-5-5` |
| **Rapido** | classificare, trovare duplicati, riassunti brevi, aggiornare la wiki | `claude-haiku-5-5` |

Un solo modello per tutti e due va bene: se un livello è vuoto si usa l'altro. Con un servizio compatibile scegli tra i modelli installati (per esempio il più grande per l'approfondito).

In **Funzioni** puoi spostare una funzione sull'altro livello o su **un modello preciso**, anche di un altro fornitore: per esempio la wiki su un Ollama locale e l'analisi su Claude. Sono attive:

| Funzione | Livello predefinito | Dove |
|---|---|---|
| Assistente | approfondito | pannello «Chiedi all'assistente» |
| Domande sul grafo | approfondito | pannello, modo «Domanda sul grafo» |
| Riassunti | rapido | scheda: «Riassumi» |
| Cosa manca per decidere | approfondito | scheda: «Cosa manca per decidere» |
| Bozze | approfondito | scheda: «Scrivi una bozza» |
| Classificazione e duplicati | rapido | nuova richiesta: «Cerca duplicati e proponi i campi» |
| Wiki personale | rapido | profilo → «La mia wiki»: aggiornamento in background |
| GRC assistito | approfondito | schede di evidenze, controlli e rischi; requisito normativo; notifica DORA dell'incidente (sezione 10) |
 Domande sul grafo, funzioni della scheda, collegamenti del GRC e notifica DORA non chiamano strumenti (Regia passa al modello ciò che la persona può leggere): vanno bene anche con un modello che non li sa usare. La **wiki personale** invece li usa (legge con cerca, query e leggi e scrive le pagine con uno strumento suo): con un modello segnato «senza strumenti» l'aggiornamento non parte, e la wiki resta da scrivere a mano. Le **bozze di policy e runbook** usano gli strumenti di lettura se il modello li sa usare (per cercare gli elementi da citare); senza, si scrivono con i dati che Regia passa.

Con Anthropic Regia usa la **cache del prompt**: istruzioni, strumenti e catalogo restano uguali fra le domande e il fornitore li rilegge dalla cache a una frazione del prezzo. Il consumo lo tiene già in conto nel costo stimato.

## 3. Budget

Un budget mensile in dollari (al listino del fornitore) e, se vuoi, un tetto di token al mese. Il consumo si calcola dal registro delle chiamate, nel fuso dell'organizzazione.

- **All'80%** compare un avviso nella configurazione, nel consumo e nel pannello dell'assistente, chi amministra l'organizzazione riceve una **notifica** (e l'email, se le email sono attive e non le ha silenziate per il tipo «AI»), e resta una voce nel registro di audit (`ai.budget.warning`). Una volta sola al mese.
- **Al 100%** le chiamate non partono più fino al mese dopo (restano nel registro come rifiutate). Tutto ciò che non usa l'AI funziona come sempre.
- **Prima di ogni chiamata** Regia conta i token del testo che manda (con Anthropic con l'API ufficiale di conteggio, gratuita; con gli altri fornitori con una stima prudente): una richiesta che da sola porterebbe oltre il budget non parte, e la persona lo vede («il budget non basta per questa richiesta»).

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

Anche nel dialogo **Nuovo elemento** della board e del backlog (task, story, bug, epic) c'è **Cerca duplicati e proponi i campi**: elementi simili dello stesso tipo e i valori dei campi a elenco del tipo, da accettare uno per uno. Nel form **nuova applicazione** dell'architettura, in quelli del **nuovo rischio**, del **nuovo incidente** e del **nuovo requisito** c'è **Cerca duplicati**: elementi simili dello stesso tipo che potrebbero essere lo stesso (per esempio un incidente già aperto per lo stesso guasto). Ogni chiamata è nel registro con la sua funzione; le bozze inserite e gli asset citati vi compaiono come «proposta confermata».

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

**Dall'evidenza ai controlli.** Nella scheda di un'evidenza (scheda «Dettagli»), **Proponi i controlli che soddisfa**: l'assistente legge l'evidenza (titolo, nota, link, la pagina Knowledge se è un'evidenza di tipo pagina; degli allegati conta solo quanti sono, il contenuto dei file non lo legge) e i controlli dell'organizzazione con i requisiti che coprono, e propone quelli che l'evidenza dimostra davvero, ognuno con il **motivo** e il **grado di copertura** («piena» se basta da sola, «parziale» se ne dimostra una parte). Ogni proposta si **collega** o si **scarta**: niente si collega da sé. Collega chi amministra la conformità o il responsabile del controllo; il grado e il motivo restano sul collegamento e si vedono nell'evidenza («copertura parziale», «proposta dall'assistente»). La copertura della normativa non cambia per il grado: un'evidenza collegata vale come prima, il grado è un'informazione per chi verifica.

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
- Ogni modifica della configurazione è nel registro di audit (`ai.settings.update`, `ai.provider.*`, `ai.proposal.apply`; `wiki.promote` per le pagine della wiki promosse a Knowledge).
