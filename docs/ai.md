# Intelligenza artificiale

L'AI di Regia è **spenta finché non la accendi**. Quando è accesa propone e la persona conferma: legge solo ciò che la persona può vedere, ogni chiamata finisce nel registro e, se la spegni o il budget finisce, tutto il resto funziona come prima.

Dove: **Configurazione → Intelligenza artificiale** e **Configurazione → Consumo dell'AI** (serve il permesso «Amministrare l'organizzazione»). Le modifiche si fanno solo da una sessione, non con un token API.

C'è il pannello **«Chiedi all'assistente»** nel menu, con due modi:

- **Conversazione:** si fanno domande sui dati, l'assistente cerca e legge con i permessi della persona e risponde con i link agli elementi. Le risposte lunghe si vedono crescere mentre arrivano. Commenti, ore, nuovi elementi, modifiche e collegamenti arrivano come **proposte**: non cambia niente finché la persona non preme **Conferma**.
- **Domanda sul grafo:** la persona chiede quali elementi vuole vedere («Quali richieste aperte sono urgenti?») e l'assistente scrive la **query di Regia**. La query resta visibile: la persona la cambia, la riesegue, apre il risultato nell'elenco **Domande sul grafo** e la **salva come vista**. Se la prima query non è valida, l'assistente la corregge una volta sola e lo dice; se non ci riesce nemmeno così, lo dice e la query si corregge a mano. La query si scrive anche senza AI, nello stesso elenco.

Con il **server MCP** (sezione 7) Claude Desktop, Claude Code o un IDE lavorano su Regia con i permessi della persona, con il suo modello.

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

In **Funzioni** puoi spostare una funzione sull'altro livello o su **un modello preciso**, anche di un altro fornitore: per esempio la wiki su un Ollama locale e l'analisi su Claude. Oggi sono attive l'**Assistente** e le **Domande sul grafo**; le altre funzioni sono segnate «in arrivo» ma si possono già sistemare. Le domande sul grafo non chiamano strumenti: vanno bene anche con un modello che non li sa usare.

Con Anthropic Regia usa la **cache del prompt**: istruzioni, strumenti e catalogo restano uguali fra le domande e il fornitore li rilegge dalla cache a una frazione del prezzo. Il consumo lo tiene già in conto nel costo stimato.

## 3. Budget

Un budget mensile in dollari (al listino del fornitore) e, se vuoi, un tetto di token al mese. Il consumo si calcola dal registro delle chiamate, nel fuso dell'organizzazione.

- **All'80%** compare un avviso nella configurazione, nel consumo e nel pannello dell'assistente, chi amministra l'organizzazione riceve una **notifica** (e l'email, se le email sono attive e non le ha silenziate per il tipo «AI»), e resta una voce nel registro di audit (`ai.budget.warning`). Una volta sola al mese.
- **Al 100%** le chiamate non partono più fino al mese dopo (restano nel registro come rifiutate). Tutto ciò che non usa l'AI funziona come sempre.

## 4. Dati personali e conservazione

- **Mascheramento verso i fornitori esterni.** Prima di uscire, i nomi delle persone dell'organizzazione, email, telefoni, IBAN e codici fiscali diventano segnaposto (`[persona-1]`, `[email-1]`); nella risposta tornano i valori veri. Per le organizzazioni **regolamentate** (con una normativa attiva come DORA o NIS2) parte acceso; per le altre parte spento. Lo può cambiare solo chi amministra l'organizzazione.
- **Conservazione.** Le conversazioni con l'assistente si cancellano dopo 90 giorni dall'ultimo messaggio (configurabile da 1 a 3650). Sono private della persona e si cancellano anche con il suo account. Il registro delle chiamate resta: non contiene il testo.

## 5. Accendere

Spunta **AI accesa** e **Salva le impostazioni** (serve almeno il modello di un livello). Il pulsante «Chiedi all'assistente» compare a chi lavora nell'organizzazione. Per spegnere, togli la spunta: il pannello sparisce e il resto non cambia.

## 6. Consumo

**Configurazione → Consumo dell'AI** mostra per mese: chiamate, token, costo stimato, chiamate non riuscite, poi i totali per funzione, per persona e per modello e le ultime 100 chiamate con esito e **impronta** (SHA-256) del testo inviato. Il testo non si registra mai.

## 7. Server MCP

Il server MCP permette a Claude Desktop, Claude Code o un IDE di cercare, leggere e proporre modifiche su Regia **con i permessi della persona** che si collega. Ha un **interruttore suo** in Configurazione → Intelligenza artificiale → **Server MCP**, spento di default e indipendente da «AI accesa»: il modello è quello della persona, quindi non serve un fornitore e il budget dell'organizzazione non si consuma.

Prima di accenderlo considera che il testo letto dagli strumenti va al modello del client della persona (per esempio Claude via Claude Desktop): **Regia non può mascherarlo**. Per un'organizzazione regolamentata decidi tu se è accettabile.

Come collegare i client, con gli esempi: [ai-mcp.md](ai-mcp.md).

## Sicurezza in breve

- L'assistente usa gli stessi permessi della persona: passa dagli stessi servizi dell'interfaccia, con l'isolamento tra organizzazioni e la visibilità degli spazi.
- Il contenuto degli elementi (titoli, descrizioni, commenti) arriva al modello delimitato come **dato**: un testo che prova a dare ordini all'assistente non viene seguito.
- Le scritture sono sempre proposte da confermare, e alla conferma i permessi si controllano di nuovo. Con il server MCP la conferma è uno strumento separato («conferma»), che il client chiama solo dopo il sì della persona.
- Il server MCP accetta solo token API: non la sessione del browser. Un token di sola lettura vede solo gli strumenti di lettura.
- Ogni modifica della configurazione è nel registro di audit (`ai.settings.update`, `ai.provider.*`, `ai.proposal.apply`).
