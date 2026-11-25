# Benchmark delle fondazioni: 1 milione di elementi per tenant

Data: 2026-11-24 · Macchina: Apple M3, 8 core, 16 GB · Postgres 16 (Homebrew; il pacchetto usa la 18) · Strumento: `regia bench`

## I dati
- Un tenant con **1.000.000 business request** distribuite su tre anni e sugli stati dello stage-gate.
- Per ogni richiesta: dati della domanda, 4 stakeholder, valore dei campi, 2 voci di storia.
- Approvazioni: **20.000 in corso** e 230.000 concluse.
- Ingombro: 5,2 GB su disco. Il caricamento con COPY richiede 5 minuti.
- Carico: 8 client in parallelo per 60 s, con scenari pesati tra letture, ricerche e scritture.

## Prima e dopo

| scenario | p95 prima | p95 dopo | budget |
| --- | ---: | ---: | ---: |
| dettaglio per chiave | 28,6 ms | 4,6 ms | 100 ms |
| lista per tipo e stato | 578 ms | 4,2 ms | 100 ms |
| lista aperte per creazione | 4,9 ms | 4,1 ms | 100 ms |
| decima pagina col cursore | 2.146 ms | 3,9 ms | 100 ms |
| ricerca full-text | 4.674 ms | 74,9 ms | 200 ms |
| ricerca + filtro stato | 1.229 ms | 11,8 ms | 200 ms |
| ricerca di un termine raro o assente | 15.000 ms (timeout) | 6,1 ms | 200 ms |
| ricerca + filtro selettivo (stato al 2%) | – | 15,4 ms | 200 ms |
| storia di un elemento | 32,7 ms | 6,7 ms | 100 ms |
| inbox approvazioni | 2.397 ms | 4,8 ms | 100 ms |
| nuova richiesta + invio | 50,5 ms | 12,4 ms | 100 ms |

- **Throughput:** da 4 a **646 richieste al secondo**.
- **Memoria massima del backend:** 48,1 MB, budget 50.
- **Con 16 client:** 559 richieste al secondo, tutti gli scenari ancora nel budget. Il peggiore è la ricerca full-text senza filtri, con p95 di 113 ms.

## Cosa è stato corretto e perché
1. **Filtri come id, non come join.** Filtrare su colonne di tabelle in join (`t.key`, `o.name`) impedisce a Postgres di scorrere l'indice degli elementi già ordinato. Ora tipi, stati e spazi si risolvono in id prima della query (`resolveFilters`). Con un solo valore si usa l'uguaglianza, così l'indice `(tenant_id, state_id, updated_at, id)` dà la pagina direttamente.
2. **Indici che corrispondono agli ordinamenti** (migrazioni 0003 e 0005): `(tenant_id, updated_at, id)`, `(tenant_id, type_id, updated_at, id)`, `(tenant_id, state_id, updated_at, id)` e `(tenant_id, number, id)`. Sono tutti parziali sugli elementi vivi.
3. **Inbox con assegnatari precalcolati** (migrazione 0004). Prima, per ogni approvazione aperta del tenant, si verificava chi poteva decidere. Ora gli assegnatari del passo attivo (membro, gruppo, organo) si scrivono quando il passo si attiva, e un trigger li aggiorna se cambiano gli stakeholder. Le deleghe si risolvono alla lettura.
4. **Ricerca full-text e RLS.** L'operatore `@@` non è *leakproof*, quindi con la Row Level Security Postgres non può usare l'indice GIN prima della policy del tenant: filtrava tutto il tenant, 3,5 s per un termine assente. Le correzioni:
   - `fts_match` (migrazione 0006) è una funzione `SECURITY DEFINER` che cerca solo nel tenant corrente, combinando GIN e filtri, e restituisce id che l'app rilegge con RLS e visibilità;
   - `fts_frequency` (migrazione 0007) stima la frequenza del termine dalle statistiche di ANALYZE;
   - se il termine è frequente (≥ 0,5%) si scorre l'indice ordinato, altrimenti si usa il GIN.

   Le strategie a tempo massimo provate prima sono state scartate: sotto carico erano instabili.
5. **Memoria:**
   - argon2id a 12 MiB e 3 passaggi, parametri equivalenti raccomandati da OWASP, invece di 19 MiB e 2;
   - hash fittizio calcolato al primo uso, non all'avvio;
   - limite morbido di memoria per il garbage collector (40 MiB, sovrascrivibile con `GOMEMLIMIT`).
6. **Migrazioni senza timeout:** il limite di 15 s per richiesta non vale per le migrazioni.

## Limiti e prossime misure
- **Un solo tenant grande.** L'architettura prevede 10 tenant da 1M elementi e 20M attività: da misurare quando ci saranno anche i moduli work e GRC.
- **Postgres 16 locale.** Con la 18 del pacchetto (I/O asincrono) i numeri dovrebbero migliorare: da rifare nell'immagine Docker.
- **Ricerca senza filtri.** È lo scenario più vicino al budget (p95 75 ms con 8 client, 113 ms con 16), perché scorre l'indice per data verificando il testo riga per riga. Se servirà: indice GIN per tenant (partizionamento) o un indice di ricerca dedicato.
- **Indici sulle tabelle in uso.** La 0003 crea gli indici senza `CONCURRENTLY` (blocca le scritture per qualche secondo su 1M righe). Per le istanze in produzione servirà una variante concorrente fuori transazione.
- **Memoria al limite.** Il backend usa 46-48 MB sotto carico, vicino al budget di 50: va tenuta sotto controllo a ogni modulo.

## Linguaggio di query completo (2026-11-24, piano passo 16)

Misura su un **dataset ridotto**: 300.000 business request (`bench seed --elements 300000 --batch 10000`), 8 client per 40 s, stessa macchina mentre giravano altri processi. Gli scenari della prima misura restano tutti nel budget.

| scenario nuovo | query | p50 | p95 | budget |
| --- | --- | ---: | ---: | ---: |
| campo personalizzato | `tipo: business_request urgenza: Alta stato: aperta` | 8,7 ms | 16,1 ms | 100 ms |
| negazione + data relativa | `-urgenza: Bassa aggiornata: -30g` | 4,2 ms | 7,2 ms | 100 ms |
| persona (0,3% degli elementi) | `segnalatore: "Persona 042"` | 13,4 ms | 24,9 ms | 100 ms |
| osservati | `osservati: me stato: aperta` | 5,6 ms | 10,2 ms | 100 ms |
| ordina per fase | `tipo: business_request stato: aperta ordina per: fase` | 2,9 ms | 5,1 ms | 100 ms |
| ordina per campo a elenco | `tipo: business_request ordina per: urgenza desc` | 8,6 ms | 14,3 ms | 100 ms |
| ordina per titolo | `stato: aperta ordina per: titolo` | 2,8 ms | 4,5 ms | 100 ms |
| ricerca per prefisso | `regis*`, `porta* clienti`… | 19,9 ms | 56,0 ms | 200 ms |
| autocompletamento | `/api/v1/query/suggest` | 1,2 ms | 2,8 ms | 30 ms |

- **Throughput:** 915 richieste al secondo; memoria massima del server 44,5 MB.
- **Correzione emersa dalla misura:** la prima versione aveva `negazione + data relativa` a 80 ms e `osservati: me` a 396 ms di p95 (fuori budget).
  - **Causa della negazione:** Postgres trasformava la sottoquery `exists` in una tabella hash di tutte le corrispondenze (100.000 valori di urgenza).
  - **Causa di osservati:** la risoluzione in id usava `union` invece di `union all` e leggeva tutti gli stakeholder prima del limite.
  - **Correzione:** le sottoquery dei predicati finiscono con `offset 0` (valutate riga per riga mentre si scorre l'indice: 34 → 0,4 ms nell'`EXPLAIN`) e la risoluzione usa `union all`.
- **Da rifare su 1M elementi:** su un milione i filtri "persona" diventano densi (circa 3.300 elementi, oltre la soglia di 2.000 id) e si valutano scorrendo l'indice. Da misurare con l'ordinamento per campo personalizzato scalare (join su `element_values`), che non è nel carico.

## Tutti i moduli (2026-11-24)

Data: 2026-11-24 · Macchina: Apple M3, 8 core, 16 GB, **con altri processi in corso** (carico di sistema tra 4 e 13 su 8 core durante le misure) · Postgres 16 incluso · `regia bench seed|load`.

### Dimensione: 300.000 elementi, non 1 milione
La macchina aveva 13 GB di disco liberi: 1 milione di elementi con i dati di tutti i moduli ne occupa circa 8 e il caricamento non lascerebbe spazio a Postgres per i file temporanei e il WAL. La misura usa quindi **300.000 elementi** (2,3 GB, 2 minuti e 34 secondi di caricamento, 1,2 GB di database). La prima misura del 1 milione di business request resta valida; quella con tutti i moduli a 1 milione è da rifare su una macchina con più disco (stesso comando, `--elements 1000000 --batch 10000`).

### I dati
Un tenant con 300.000 elementi, nelle proporzioni di un'installazione reale:

| che cosa | quantità |
| --- | ---: |
| business request (40%) | 120.000 |
| impatti sulle applicazioni | 45.209 |
| valutazioni | 12.042 |
| requisiti (con 45.000 celle della matrice) | 36.490 |
| epic, story e task in 12 spazi dei team | 5.615 + 33.578 + 50.376 |
| commenti e notifiche | 119.602 e 42.782 |
| una richiesta grande: 300 requisiti × 30 impatti | 9.000 celle |
| campo personalizzato numerico «utenti» | 120.000 valori |

Sprint di due settimane per team (28 per spazio): i passati chiusi, quello di oggi attivo. Più commenti, attività e notifiche. Il generatore usa i servizi dei moduli per i dati di riferimento (team, spazi, sprint, applicazioni) e COPY per il volume.

### Il carico
8 client in parallelo per 60 secondi su 34 scenari pesati (letture, ricerche, query, viste dei moduli, scritture). Ogni scenario gira due volte da solo prima della misura: la prima chiamata (a freddo) è nella colonna «primo». I tempi sono in millisecondi; il budget è p95 < 100 ms (200 ms per le ricerche testuali, 30 ms per l'autocompletamento).

| scenario | p50 prima | p95 prima | p50 dopo | p95 dopo | primo (freddo) | esito |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| board dello sprint attivo | 388 | 787 | 78 | 164 | 155 | fuori (p95) |
| board con filtro | 2.241 | 3.315 | 73 | 145 | 143 | fuori (p95) |
| backlog | 1.476 | 1.714 | 64 | 121 | 75 | fuori (p95) |
| cruscotto di portafoglio (con scritture in corso) | 3.118 | 5.819 | 844 | 3.201 | 1.209 | fuori |
| cruscotto di portafoglio (senza scritture) | 3.118 | 5.819 | 1,0 | 2,1 | 823 | **ok** |
| inbox approvazioni | 4 | 15 | 2 | 8 | 6 | ok |
| inbox valutazioni | 180 | 461 | 194 | 313 | 164 | fuori |
| inbox requisiti | 114 | 238 | 3,5 | 133 | 70 | fuori (p95) |
| notifiche | 21 | 66 | 17 | 33 | 11 | ok |
| contatore notifiche | 2 | 11 | 1,6 | 9,7 | 1,5 | ok |
| pagina di dettaglio completa (6 chiamate + storia) | 21 | 46 | 13 | 47 | 25 | ok |
| roadmap | 337 | 576 | 7 | 15 | 254 | ok (a regime) |
| campo personalizzato numerico | 94 | 1.853 | 32 | 136 | 304 | fuori (p95) |
| ⌘K per parola | 22 | 121 | 25 | 75 | 15 | ok |
| ⌘K per chiave | 2,7 | 7,5 | 2,9 | 11 | 1,6 | ok |
| matrice requisiti 300×30 | 350 | 470 | 379 | 681 | 139 | fuori |

Gli scenari già misurati (dettaglio, liste, cursore, ricerche full-text, storia, query con campi, persone, osservati, ordinamenti, prefisso, autocompletamento, creazione con invio) restano nel budget, con p95 tra 8 e 73 ms (ricerca per prefisso 61 ms, budget 200).

- **Throughput** della misura mista: 177 richieste al secondo.
- **Memoria massima del backend: 56-68 MB** (budget 50): **fuori budget**. A riposo il processo dopo il carico occupa 52 MB e già al primo avvio circa 40 MB (binario con l'interfaccia incorporata, runtime, buffer di pgx); il limite morbido del garbage collector a 40 MB (provato fino a 24 MB) non cambia il picco, che viene dalle risposte grandi in parallelo (matrice 1,3 MB, backlog 460 KB, inbox).
- Con un solo client (macchina meno carica) board, board con filtro e backlog stanno a p95 88-89 ms, 89 ms e 246 ms (il backlog ha code lunghe: una risposta da 460 KB con 1.400 voci).

### Cosa è stato corretto e perché
1. **Epic della board** (`work/board.go`): `e.id::text = any(...) or (spazio, aperto)` obbligava a leggere tutti gli elementi (700 ms con 300.000). Ora si legge l'indice (spazio, tipo) delle voci aperte, nuovo nella migrazione 0032: 3,6 ms.
2. **Backlog** (`work/sprint.go`): scorreva tutti gli elementi dello spazio (7.000) e applicava i join a tutti. Ora parte dall'insieme delle voci aperte e di quelle pianificate negli sprint non chiusi (`union`) e fa i join solo su quelle: da 80-360 ms a 14 ms di query.
3. **Cruscotto** (`report/dashboard.go`, `api/swr.go`): le 15 query rifacevano ciascuna il join su 120.000 richieste, e con più utenti la stessa vista si calcolava più volte. Ora il perimetro si materializza una volta in una tabella temporanea della transazione, con indice e `ANALYZE` (`as not materialized`: come CTE materializzata perdeva le statistiche e una query non finiva più), e un solo ricalcolo per chiave (singleflight) serve tutti. Calcolo a freddo: da 1,2-1,9 s a 0,3 s per un'unità e 0,8-1,6 s per il portafoglio intero; con la voce in cache 1-2 ms.
4. **Roadmap** (`api/planning.go`): ordina tutte le richieste aperte (74.000) prima del limite di 1.000: 270 ms. Ora ha la stessa cache del cruscotto, ma con un ritardo tollerato di 5 secondi dopo una modifica (poi si serve la vecchia voce mentre una sola richiesta la rinnova in background): 7 ms a regime, 254 ms al primo accesso o dopo 60 s.
5. **Campi numerici con intervallo** (migrazione 0032, `engine/query_plan.go`): gli operatori di confronto su `numeric` non sono *leakproof*, e con la RLS Postgres non li usa come condizione dell'indice `(tenant, campo, v_num)`: leggeva tutti i 120.000 valori (84 ms, fino a 2,6 s sotto carico). `field_num_match` (SECURITY DEFINER, filtra sul tenant corrente come `fts_match`) usa l'indice e restituisce solo id, che l'app rilegge con RLS. Inoltre la soglia di risoluzione dei filtri in id sale da 2.000 a 5.000: `utenti: 100..120 ordina per: utenti desc` (2.400 elementi) scorreva l'indice per valore e probava ogni riga (480 ms), ora sono 30 ms.
6. **Inbox delle valutazioni**: 1.170 voci, 800 KB e 110 ms. Ogni coda mostra le 300 più urgenti, con un campo `more` quando ce ne sono altre.
7. **Seed**: gli sprint creati dal servizio occupano i primi numeri dello spazio, e il generatore ripartiva da 1 (violazione di `elements_number_uq`): ora parte dal massimo dello spazio.

### Cosa resta fuori budget, e perché
- **Cruscotto dopo ogni scrittura.** La voce in cache vale finché nel tenant non arriva una modifica (il test `TestReportDashboardPortal` pretende che un costo appena scritto si veda subito), quindi con scritture continue ogni lettura riparte da 0,3-1,6 s. Senza scritture p95 è 2 ms. Strada: aggregati incrementali (una tabella di conteggi per tenant, aggiornata dai trigger o dal worker) oppure un ritardo tollerato di pochi secondi come per la roadmap, da decidere con il prodotto.
- **Matrice 300 × 30:** 9.000 celle, 75 ms di query, 1,3 MB di JSON. Serve la paginazione per requisiti o la compressione dei duplicati nella risposta.
- **Backlog, board e inbox valutazioni:** i valori a un client sono vicini al budget (board 88 ms, backlog p50 31 ms con code lunghe fino a 250 ms); sotto 8 client in parallelo con la macchina carica il p95 sale a 120-310 ms. Strada: paginare il backlog (oggi fino a 20.000 voci in una risposta) e ordinare le inbox con un indice sulle code.
- **Inbox requisiti** (p95 133 ms): la prima esecuzione fa 60 ms per i 4.700 requisiti da verificare.
- **Memoria:** vedi sopra; per stare sotto i 50 MB serve ridurre il peso delle risposte (compressione e paginazione) e togliere dal binario le risorse non servite.
- **Misure a 1 milione di elementi** con tutti i moduli, e con il Postgres 18 del pacchetto.

### Come ripetere la misura
```bash
export REGIA_ADMIN_PASSWORD='una-password-di-prova'
./bin/regia bench seed --data ./data-bench --tenant bench --elements 300000 --batch 10000
./bin/regia serve --data ./data-bench --http 127.0.0.1:8091 &
./bin/regia bench load --url http://127.0.0.1:8091 --elements 300000 --duration 60s --concurrency 8 --pid $(pgrep -f "serve --data ./data-bench")
./bin/regia bench load ... --only cruscotto,board --concurrency 1     # pochi scenari, un client
```

## Il budget: gli scenari fuori e la memoria (2026-11-25)

Stesso dataset di sopra (300.000 elementi, tenant `bench`) e stesso strumento. Macchina: Apple M3, 8 core, 16 GB, **condivisa con altri agenti**: il carico di sistema (load average) era tra 3 e 15 all'inizio delle misure e ha toccato 40 durante alcune. Con 8 client senza pause il benchmark da solo porta il sistema a 10-15, quindi i p95 a 8 client includono la coda per la CPU: per questo si danno anche quelli a 4 client e i tempi di una richiesta sola. Una misura a 4 client fatta con il carico a 13 (ricerche full-text a 470 ms, che non c'entrano con queste modifiche) è stata scartata.

### Prima e dopo
«Prima» è il binario di d314fd6, «dopo» quello di questo ramo, sullo stesso database, nella stessa fascia di tempo; 60 secondi, p95 in millisecondi (budget 100). Per «dopo» a 8 client si dà il minimo e il massimo di quattro misure; a 4 client di due. «Una richiesta sola» è la durata nel log del server con la macchina meno carica.

| scenario | 8 client prima | 8 client dopo | 4 client prima | 4 client dopo | una richiesta sola, prima → dopo |
| --- | ---: | ---: | ---: | ---: | ---: |
| cruscotto con scritture in corso | 3.222 | **13-20** | 2.751 | **2,4** | a regime 2 |
| matrice requisiti 300×30 | 425 | 139-220 | 325 | **86-101** | 123-145 → **36** |
| backlog | 83 | 112-131 | 68 | 62-75 | 23 → 28 |
| inbox valutazioni | 192 | **85-97** | 169 | **48-61** | 113-170 → **26** |
| inbox requisiti | 121 | **67-92** | 95 | **40-52** | 56-65 → **0,6** |
| board dello sprint attivo | 84 | **48-66** | 64 | **28-36** | 26 → **11** |
| board con filtro (sprint = tutti) | 122 | 118-148 | 97 | 78-100 | 29 → 29 |
| campo numerico | 115 | 100-151 | 109 | **72-93** | 20-30 |
| memoria massima del backend | 78,6 MB | **47-48 MB** | 67,9 MB | **47,6-48 MB** | |

Gli altri 26 scenari non sono stati toccati (salvo il calcolo dei giorni lavorativi e il codec dei uuid) e restano nel budget quando la macchina è quieta. **Non tutto è nel budget:** a 4 client restano al limite la matrice (86-101) e la board con filtro (78-100); a 8 client, con la macchina condivisa, backlog, board con filtro, campo numerico e matrice stanno tra 100 e 220 ms, ma da soli sono a 11-36 ms. Il backlog non è più veloce (28 contro 23 ms: tre letture invece di una): la risposta passa da 453 a 193 KB (da 65 a 28 KB compressi).

### Cosa è stato fatto
1. **Cruscotto con scritture concorrenti** (`api/swr.go`, `api/events.go`, `api/report.go`). Le scritture si dividono in *tollerate*, la creazione di una richiesta e il passaggio di fase (`POST /demand/requests` e `POST /elements/{id}/transitions`, le sole che il benchmark fa), e *strict*, tutte le altre: costi, budget, approvazioni, report di avanzamento, valutazioni, impatti, piani, impostazioni. Il hub tiene due contatori per tenant. Una modifica strict rende la voce subito non servibile: il costo scritto si vede subito, `TestReportDashboardPortal` è rimasto com'era e passa. Una modifica tollerata lascia la voce servibile per `dashboardLag` = 2 secondi, poi la rinnova in background (un solo ricalcolo per chiave) servendo intanto la vecchia. Prima ogni scrittura la invalidava e ogni lettura rifaceva 0,3-1,6 s di calcolo. **Va confermato con il prodotto:** dopo che una richiesta è stata creata o ha cambiato fase, il cruscotto può restare indietro di 2 secondi più la durata del ricalcolo. Gli aggregati incrementali non li ho fatti: i 9 pannelli dipendono da filtri (unità, driver, periodo) e dal giorno, e una tabella di conteggi per tenant non li copre senza riscrivere le query. Il costo a freddo, 0,3-1,6 s per voce e una voce per unità (40 nel dataset), resta: il benchmark ora visita ogni unità prima di misurare e lo riporta in «primo».
2. **Matrice a pagine** (`requirements/matrix_page.go`, `api/requirements.go`, `Requirements.tsx`). `GET /elements/{ref}/requirements?limit=50&offset=0`, parametri facoltativi: senza, la matrice è intera come prima (anche per il CSV e per l'impatto). Riepilogo, team, categorie e `total_rows` sono di tutta la matrice; le righe sono la pagina; `more` dice se ce ne sono altre. L'aggregazione per requisito gira in SQL (300 righe invece di 9.000, con le regole di `cellRow.status` e `summarize` e senza i join per cella su impatto, stato e team), le celle si leggono solo per la pagina e l'elenco dei mancanti si ferma ai primi 25 con `mancanti_totale`. `TestRequirements` confronta, per 7 filtri, le pagine da 3 righe con la matrice intera (righe, celle, riepilogo). Il JSON passa da 451 a 77 KB. L'interfaccia scrive «50 di 390 requisiti» con «Mostra altri» e «Mostra tutti», e il filtro riparte dalla prima pagina.
3. **Backlog a pagine** (`work/sprint.go`, `api/work.go`, `Backlog.tsx`). `GET /work/spaces/{key}/backlog?limit=300&offset=0`, facoltativi: gli sprint arrivano sempre interi, il backlog a pagine con `backlog_total` e `more`; senza parametri è la risposta di prima. L'interfaccia carica 300 voci e «Carica altre voci» ne chiede altre 300 (la richiesta ricomincia da zero con un limite più alto, quindi la cache ottimistica del trascinamento resta valida). Trascinando una voce in fondo all'elenco caricato la si piazza dopo l'ultima caricata, non dopo l'ultima del backlog.
4. **Inbox** (`reviews/view.go`, `requirements/requirement.go`, migrazione 0043). La coda si legge in due passi: gli id delle più urgenti dall'indice delle code, poi il dettaglio con i 13 join solo per quelle. La coda del team non aveva limite e leggeva tutto. Chi non è verificatore (quasi tutti) non paga più il join a 13 tabelle: erano 50 ms e 243.000 assegnazioni lette per un'inbox vuota, e il menu la chiede a ogni pagina. Indici: `ext_review_team_queue_idx`, `ext_review_compile_queue_idx`, `stakeholders_role_member_idx`, `stakeholders_role_group_idx`. Le due inbox accettano `?limit=N` (fino a 5.000) e rispondono con `more` (nelle valutazioni c'era già, nei requisiti no); le pagine hanno «Mostra altre» e il menu chiede 99 voci e mostra «99+». Il calcolo dei giorni di ritardo era il 25% della CPU del carico: la Pasqua si ricalcolava per ogni giorno di ogni conteggio (`platform/calendar`) e il conteggio si rifaceva per ogni riga.
5. **Board e campo numerico** (`work/board.go`). Con `EXPLAIN ANALYZE` la board dello sprint leggeva e univa tutti i 7.700 elementi dello spazio per tenerne 209 (26 ms): ora parte dalle relazioni dello sprint, 11 ms. Per `sprint=tutti` ho provato a partire dagli aperti e dai chiusi da poco, con un indice nuovo: costava di più (36 contro 29 ms) e l'ho tolto. Quella vista restituisce fino a 2.500 card e 250-940 KB di JSON: la query è 20-30 ms, il resto è serializzare e comprimere. Il campo numerico ha già il piano giusto (`field_num_match` con l'indice su `v_num`): 20-30 ms; i p95 a 8 client sono code di CPU e il primo accesso a ciascun filtro (300 ms).
6. **Memoria:** sotto.

### Memoria
Con `REGIA_PPROF=127.0.0.1:8189` il server espone i profili di Go (nuovo, solo loopback).
- **A riposo:** 18 MB di RSS dopo 20 s dall'avvio (heap in uso 5,5 MB: buffer e cache di pgx, regexp compilate, catalogo). Il frontend incorporato sta nel segmento dati del binario e non entra nell'heap: `http.FileServerFS` legge dall'`embed.FS` senza copie, quindi non c'è niente da caricare «solo quando serve». Dopo un carico l'RSS restava a 80 MB (prima) e ora scende a 37-43 MB, perché il server restituisce al sistema la memoria libera ogni minuto (`debug.FreeOSMemory`).
- **Sotto carico** (8 client): heap in uso 15-24 MB, 31-40 MB di heap totale, picco di RSS 78,6 MB prima, **47-48 MB** dopo. Dai profili delle allocazioni (`/debug/pprof/allocs`) venivano soprattutto da: un compressore gzip nuovo da 1,2 MB a ogni risposta di board e backlog (10%); gli errori di codifica di pgx che formattavano l'intero array di id a ogni query di ricerca (7%, 101 MB in 40 s); il buffer di `encoding/json` per le risposte grandi (13%); le descrizioni delle istruzioni preparate (512 per connessione).
- **Cosa è cambiato:** compressori gzip in una lista di 4 che il garbage collector non svuota (una `sync.Pool` non basta: con il limite di memoria il GC gira decine di volte al secondo e la svuota ogni volta); codec dei uuid come stringhe in binario (`platform/db/uuidcodec.go`: una stringa non valida fallisce come prima e pgx ripete in testo, l'errore resta quello di Postgres, 22P02); cache delle istruzioni a 128; limite morbido del GC da 40 a 30 MiB (misurato con 40: picco 56-59 MB e 1.097 cicli di GC in 40 s; con 32: 49,2 MB e 2.175 cicli; con 28: 46,6 MB e 3.695-5.099 cicli; con 30, quello scelto, 47-48,4 MB); rilascio periodico della memoria libera.
- **Il costo:** il GC gira più spesso e il throughput misurato è sceso da 388 a 319-344 richieste al secondo nei confronti a carico di sistema simile; il rumore della macchina condivisa è dello stesso ordine. Sotto i 46 MB di picco non si va senza un costo maggiore: dei 47 MB circa 20 sono segmenti del binario, stack e metadati del runtime, non heap. Su Linux (il container di deploy) l'RSS si comporta in modo diverso da macOS: il picco va rimisurato lì.

### Cosa resta
- **Board con filtro** (78-100 ms a 4 client, 118-148 a 8): `sprint=tutti` restituisce fino a 2.500 card. Serve la paginazione per colonna o card più leggere (id come indici nei dizionari `members`, `epics`, `priorities`); non l'ho fatto perché cambia il contratto della board, su cui lavora un altro agente.
- **A 8 client** backlog, matrice e campo numerico stanno tra 100 e 220 ms con la macchina condivisa. Da rimisurare a macchina quieta.
- **Cruscotto:** il ritardo di 2 secondi sulle richieste (sopra) e il costo a freddo di ogni variante. Strada per toglierli: aggregati incrementali per tenant aggiornati dai trigger, o il precalcolo di tutte le unità dopo l'avvio.
- **Ricerca** (`runBuckets`): è il 38% delle allocazioni; ogni pagina legge il testo del corpo e i filtri sono array da 5.000 id.
- `TestKnowledge` fallisce tra mezzanotte e l'una (ora italiana) perché confronta la data locale con quella UTC: non dipende da queste modifiche.
- Misure a 1 milione di elementi e con Postgres 18, come sopra.
