# Continuità operativa

Regia mette insieme quello che la continuità operativa chiede: la BIA delle funzioni di business, i tempi di ripristino richiesti e dichiarati, i piani di continuità e i test di resilienza. Lo fa sul grafo che c'è già:

capability → applicazioni → dipendenze → fornitori ICT.

Il valore sta nelle **incoerenze**, che uno strumento isolato non vede. Un esempio: l'home banking promette di ripartire in un'ora, ma chiama un servizio che riparte in un giorno.

Serve a chi gestisce la continuità (BCM, risk manager), al CISO, agli owner delle applicazioni e all'auditor. I riferimenti normativi (DORA, capo II art. 11-12 e capo IV art. 24-25; ISO 22301) stanno nei cataloghi della conformità e restano **da verificare**, come gli altri contenuti di partenza.

## Prima volta
1. **Configurazione → Moduli:** il modulo «Continuità operativa» è acceso come gli altri.
2. **Architettura → Continuità:** se il modulo non è ancora preparato, chi amministra lo prepara con «Prepara la continuità». Il pulsante crea:
   - i tipi «Analisi d'impatto (BIA)» e «Test di resilienza» con i loro processi;
   - il ruolo «Responsabile della continuità»;
   - la configurazione di partenza.
3. **Configurazione → Continuità:** controlla e adatta la configurazione all'organizzazione:
   - **categorie d'impatto:** economico, normativo, reputazionale, clienti, ognuna con i suoi livelli e la soglia «intollerabile»;
   - **orizzonti temporali:** 1 ora, 4 ore, 1 giorno, 3 giorni, 1 settimana;
   - **tipi di test:** tabletop, failover, ripristino da backup, scenario;
   - **soglie:** età massima di un test (365 giorni), riesame della BIA (12 mesi), MTPD entro cui una funzione si propone critica (3 giorni).

   Ogni salvataggio è una versione nuova e lo storico resta.

## La BIA di una funzione
- **Avvio:** Architettura → Continuità → BIA → «Avvia la BIA» sulla capability. Si fa una BIA per capability.
- **Impatto nel tempo:** per ogni categoria scegli il livello d'impatto a ogni orizzonte. Mentre compili, Regia propone:
  - **MTPD:** il primo orizzonte in cui una categoria diventa intollerabile;
  - **RTO:** l'orizzonte prima dell'MTPD;
  - **funzione critica o importante:** sì, se l'MTPD sta entro la soglia.

  Tu confermi o correggi, e indichi l'RPO e la motivazione.
- **Approvazione:** «Invia in revisione», poi chi ha il permesso «Approvare le BIA» approva. I valori approvati diventano quelli **in vigore**.
- **Versione successiva:** «Aggiorna la BIA» apre una nuova bozza. Finché la bozza non è approvata, restano in vigore i valori precedenti, e tutte le versioni approvate restano consultabili.
- **Ereditarietà:** la BIA di una capability vale anche per le capability che contiene e per tutte le applicazioni che le supportano.

## RTO e RPO
- **Dichiarati:** si scrivono sull'applicazione (scheda o import CSV, colonne `rto_minuti` e `rpo_minuti`). Per un fornitore ICT vanno sul contratto, come impegno di ripristino.
- **Richiesti:** vengono dalla BIA approvata più stringente fra le capability che l'applicazione serve. Il blocco «Continuità» dell'applicazione mostra:
  - il dichiarato e il richiesto, con la BIA da cui viene il richiesto;
  - le dipendenze più lente;
  - il piano e l'ultimo test.

## Cosa manca e cosa non torna
Architettura → Continuità elenca le incoerenze, prima le gravi:
- RTO o RPO dichiarato più lungo del richiesto, oppure RTO non dichiarato;
- un'applicazione che dipende da un'altra applicazione, o da un fornitore, con RTO più lungo del suo, o senza RTO;
- una funzione critica secondo la BIA che sull'applicazione non è segnata come tale;
- una funzione critica senza piano di continuità approvato, senza un test recente o con l'ultimo test non superato;
- una capability con applicazioni critiche e nessuna BIA approvata, o una BIA da riesaminare.

Ogni riga ha due possibilità:
- l'azione diretta, per esempio correggere l'RTO, pianificare il test o avviare la BIA;
- «Crea il lavoro», che apre un'attività nel team dell'applicazione con il testo già scritto e il collegamento al problema.

Sulla **mappa**, l'interruttore «Continuità» mostra gli RTO sui nodi e colora di rosso le dipendenze incoerenti.

## Piani di continuità
- **Cos'è:** un piano è una pagina della Knowledge resa documento controllato, con revisione e approvazione già previste.
- **Collegamento:** si collega alla capability o all'applicazione.
- **Copertura:** conta solo se ha una versione approvata. Un piano sulla capability copre anche le sue applicazioni.

## Test di resilienza
- **Piano dei test:** Architettura → Continuità → Piano dei test. Per l'anno scelto mostra le funzioni critiche, i test pianificati ed eseguiti e quelli che mancano. «Pianifica» crea il test con tipo e data.
- **Esito:** nel test registri data di esecuzione, RTO e RPO misurati (confrontati con i richiesti), esito e rilievi. Per un esito con rilievi o non superato serve almeno un rilievo.
- **Evidenza:** con «Registra l'esito» nasce un'**evidenza automatica della conformità** (raccolta `continuity.test`), che si collega ai controlli iscritti a quella raccolta. Iscrivi un controllo, mappato sugli articoli DORA del catalogo, da Configurazione → Continuità.
- **Rilievi:** ogni rilievo diventa un'azione di rimedio con «Crea il lavoro».

## Regole e automazioni
Le condizioni di processi, regole e automazioni possono usare questi fatti:
- **sull'applicazione:**
  - `rto_richiesto_minuti`, `rpo_richiesto_minuti`;
  - `rto_incoerente`;
  - `continuita_problemi`;
  - `ultimo_test_giorni`;
  - `piano_continuita`;
  - `funzione_critica_bia`;
- **sulla capability:** `bia_critica`, `mtpd_minuti`, `rto_richiesto_minuti`, `bia_giorni`.

Per esempio, `ultimo_test_giorni: >330 funzione_critica_bia: sì` può avvisare l'owner un mese prima che il test scada.

## Permessi
| Permesso | Cosa consente |
|---|---|
| Gestire BIA e test di continuità (`continuity.write`) | compilare le BIA in bozza, pianificare e registrare i test, collegare i piani |
| Approvare le BIA (`continuity.approve`) | approvare una BIA |
| Configurare la continuità (`continuity.admin`) | scale, orizzonti, tipi di test, preparazione del modulo |

Il ruolo «Responsabile della continuità» li ha tutti e tre.
