# Import da Jira, YouTrack e CSV

Configurazione → Import porta in Regia un progetto che oggi sta in Jira, in YouTrack o in un foglio di calcolo. Serve il ruolo di amministratore. I parser sono scritti da zero sui formati di export pubblici e documentati; l'API REST è facoltativa, in sola lettura e usa il token dell'utente.

## Come esportare

### Jira
1. Dalla ricerca di issue (filtro o JQL del progetto) scegli **Esporta → CSV (tutti i campi)**. Va bene sia il CSV con la virgola sia quello con il punto e virgola.
2. Le colonne che si ripetono (Labels, Sprint, Comment, Attachment, collegamenti) restano ripetute: Regia le legge tutte.
3. Per includere commenti, ore e allegati servono le colonne relative: l'export «tutti i campi» le contiene.
4. Con l'API: indirizzo del sito, progetto, email dell'account (Jira Cloud) e token. Con Jira Data Center il token personale basta.

### YouTrack
1. Da un progetto: **Issues → Esporta → CSV** (oppure l'azione di esportazione del menu della lista).
2. Scegli le colonne da portare: chiave, titolo, stato, tipo, priorità, assegnatario, date, etichette, sprint, stima, descrizione e i collegamenti (*subtask of*, *depends on*, *relates to*).
3. Con l'API: indirizzo dell'istanza, progetto e token permanente.

### CSV generico
Qualunque foglio di calcolo con intestazione: nel passo di mappatura si assegna a ogni colonna un ruolo (chiave, titolo, tipo, stato, genitore, assegnatario, date, etichette, sprint, commento, ore, stima, collegamenti, campo personalizzato).

## La procedura

1. **Caricamento.** Scegli la sorgente e il file (fino a 512 MB, 200.000 righe) oppure collega l'API. Il token dell'API resta in memoria per l'esecuzione e non si salva: dopo un riavvio del server va inserito di nuovo.
2. **Analisi.** Regia legge il file una sola volta e mostra cosa ha trovato: righe, tipi, stati, persone, campi personalizzati, sprint, collegamenti, commenti, ore e allegati.
3. **Mappatura.** Per ogni sezione si sceglie la destinazione:
   - tipi d'origine → epic, story, task, bug (o non importare);
   - stati → fasi del processo di ogni tipo;
   - persone → membro esistente, segnaposto «importato» (il nome si conserva) o nessuno;
   - campi personalizzati → campo esistente, campo nuovo (con tipo di dato) o ignorato;
   - priorità ed etichette → un campo a scelta, con le opzioni create se mancano;
   - sprint → date e stato (dal CSV non arrivano);
   - tipi di collegamento → tipi di relazione di Regia;
   - spazio di destinazione, formato delle date, cosa portare (commenti, ore, allegati, collegamenti, sprint).
   La mappatura si può salvare come **profilo** (`import_profiles`, target `work_items`) e riusare con il prossimo export dello stesso strumento: Regia propone già una mappatura dai nomi delle colonne e dei valori.
4. **Anteprima.** Prova completa senza salvare nulla: conteggi di ciò che verrebbe creato e aggiornato e gli **errori riga per riga** (data illeggibile, genitore mancante, stato non mappato, campo non valido). Si può correggere la mappatura e ripetere la prova.
5. **Esecuzione.** In background, a lotti, con avanzamento in tempo reale; il server resta utilizzabile. Un solo import per volta per tenant. Se il server si riavvia, l'esecuzione risulta interrotta e si riprende: è idempotente.
6. **Report.** Elementi creati, aggiornati, saltati (con il motivo), commenti, ore, relazioni, allegati, campi e opzioni creati.
7. **Annullamento.** Elimina ciò che l'import ha creato, se dopo nessuno l'ha toccato (vedi sotto).

## Cosa si porta

| Cosa | Come |
| --- | --- |
| Chiave originale | Nel campo «origine» e nella tabella `import_origins`; con «mantieni i numeri» in uno spazio nuovo PROJ-123 resta PROJ-123 |
| Gerarchia | Epic, story, sottotask, genitore o Epic Link |
| Collegamenti | Blocca, dipende da, correlato e simili, verso tipi di relazione di Regia |
| Commenti | Con autore e data originali |
| Allegati | Scaricati dall'API se possibile; dal CSV, come elenco di link nella descrizione |
| Sprint | Creati con date e stato indicati in mappatura; ogni elemento nel suo sprint |
| Ore | In `work_logs`, con persona, giorno e durata; la stima iniziale come stima |
| Date originali | Creazione, aggiornamento, risoluzione, scadenza |
| Descrizioni | Il testo wiki di Jira (titoli, grassetto, codice, elenchi, link, menzioni) diventa Markdown; ciò che non si riconosce resta com'è |

### Cosa NON si porta
- **La storia degli stati.** Gli elementi nascono direttamente nello stato finale mappato, senza passaggi di fase né regole del processo. Anche l'interfaccia lo dice.
- Cronologia delle modifiche, voti, osservatori, permessi del progetto, workflow, schemi e automazioni dell'origine.
- Gli allegati del CSV come file (non c'è modo di scaricarli senza l'API).

## Idempotenza e rieseguire un import

Ogni elemento ha la chiave d'origine. Una seconda esecuzione sullo stesso progetto **aggiorna** gli elementi già importati invece di duplicarli; commenti e ore dell'import non si ripetono. Così si può importare una prima volta per provare e rifare l'import definitivo con un export aggiornato.

## Annullamento

Elimina gli elementi creati dall'import, tranne quelli modificati dopo (versione diversa da quella scritta), aggiornati da un import successivo o con commenti, ore o allegati aggiunti in Regia. Commenti e ore dell'import si eliminano anche dagli elementi che restano. Campi, opzioni, tipi di relazione, il membro «importato», lo spazio e il team si eliminano solo se nessuno li usa più. Gli aggiornamenti fatti a elementi che esistevano già non si annullano. Il report dell'annullamento dice cosa è stato eliminato e cosa è rimasto.

## Ore dal timesheet (CSV)

Le ore si importano da **Configurazione → Import → Costi**, scegliendo **Ore**. Il flusso è quello dei costi: profilo di mappatura salvato, prova senza scrivere, conferma e annullamento. Serve il permesso di scrittura della finanza.

Campi della mappatura:

- **Attività o elemento (chiave)**: obbligatoria. Le ore si collegano all'elemento con quella chiave, attività di piano o altro. Deve esistere ed essere visibile a chi importa.
- **Persona (email)**: obbligatoria. Deve essere un membro dell'organizzazione.
- **Data**: obbligatoria, nel formato scelto nel profilo.
- **Ore** (anche decimali, con virgola o punto) **oppure Minuti**: una sola delle due colonne. Le ore diventano minuti; devono essere maggiori di zero e non più di 24 per riga.
- **Tipo di attività**, **Testo**, **Capitalizzabile (sì/no)**: facoltativi.
- **Riferimento**: facoltativo, per esempio il numero della riga nel timesheet d'origine.

La prova elenca gli errori riga per riga: chiave inesistente o non visibile, persona non membro, data non valida, ore nulle o illeggibili, periodo chiuso o settimana già inviata o approvata. Le righe buone si importano ignorando quelle con errori.

Le ore importate sono come quelle registrate a mano: stessa persona, stesso ruolo del team, costo calcolato se c'è la tariffa. Lo stesso file non duplica: ogni riga ha un riferimento (la colonna mappata o l'impronta dei suoi dati) e le righe già presenti si saltano. Due registrazioni identiche nello stesso file contano come una: per distinguerle si mappa il riferimento. L'annullamento elimina le ore di quell'esecuzione e i costi che ne derivano.

## Prestazioni

Gli elementi sono scritti in blocco, in lotti, senza regole né passaggi di fase. Il test di volume (`TestImportVolume`) con 5.000 righe, ciascuna con un commento, una registrazione di ore e collegamenti, completa l'esecuzione in circa 8 secondi; con `REGIA_IMPORT_VOLUME=50000` le 50.000 righe richiedono circa 71 secondi di esecuzione (700 elementi al secondo), più 11 secondi di anteprima, e il server resta reattivo (una richiesta durante l'import risponde in 65 ms).

## Sicurezza

- L'indirizzo dell'API passa dagli stessi controlli SSRF dei webhook (niente indirizzi interni, salvo in sviluppo o con l'opzione apposita).
- Si usa solo la lettura; il token non si salva e non compare nei log né nel report.
- Ogni import è registrato nel registro di audit.
