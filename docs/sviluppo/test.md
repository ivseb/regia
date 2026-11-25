# Test e benchmark

## Test
`make test` esegue unit test e test di integrazione, più `pnpm test` del frontend. I test di integrazione avviano un Postgres incluso in una cartella temporanea e verificano isolamento tra tenant, RLS su tutte le tabelle, riservatezza dei dati finanziari, catena di audit, login, flusso HTTP, backup e ripristino.
- **Senza binari di Postgres** i test di integrazione vengono saltati.
- **In CI** `REGIA_ITEST_REQUIRE_PG=18` fa fallire la suite se Postgres manca o è più vecchio della 18.

`make ci` esegue in locale gli stessi passi della CI, tranne l'immagine Docker:
- frontend;
- vet, tipi e contrasti;
- test con `-race`;
- build;
- licenze Go e npm.

### CI (`.github/workflows/ci.yml`)
Tre job, nessun segreto richiesto:
- **Frontend:** dipendenze con la cache di pnpm, contrasti dei temi, tipi, `pnpm test`, build e licenze npm. Il frontend compilato passa al job Go come artefatto.
- **Backend:**
  - Postgres 18 e pgvector dal repository PGDG;
  - `gofmt`, `go vet`;
  - `go test -race` con `REGIA_ITEST_REQUIRE_PG=18`;
  - build;
  - licenze Go con `go-licenses` (versione fissata, binario in cache).
- **Immagine Docker:** build con la cache di GitHub Actions, dimensione nel riepilogo, poi una prova completa: avvio, `/healthz`, `doctor`, `backup` e arresto pulito.

Il workflow si controlla con `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`.

### Test end-to-end (Playwright)
`pnpm e2e` (da `web/`) esegue i test dell'interfaccia in `web/e2e/` su un server vero: `global-setup.ts` avvia `bin/regia` su una cartella temporanea (seed di `scripts/dev-seed.sh`, dati demo, host `e2e.localhost`, porta 8140) e a fine corsa ferma il server e cancella i dati. Servono `bin/regia` con il frontend incluso e Chrome installato. **`make build` da solo non ricompila il frontend**: dopo aver toccato `web/src` usa `make e2e` (o `make web build`), altrimenti i test girano contro un'interfaccia vecchia e falliscono senza motivo.
```bash
make e2e                                 # ricompila frontend e binario, poi esegue tutti i test
# oppure a mano: make web build && cd web && pnpm e2e
cd web && pnpm e2e                       # tutti i test (circa un minuto), con il binario già compilato
pnpm e2e e2e/board.spec.ts                # un solo file
pnpm e2e --headed --workers=1             # per guardarli
```
- **Variabili:** `E2E_PORT` (8140-8149), `E2E_URL` (usa un server già avviato con seed e dati demo), `E2E_BIN`, `E2E_DATA` (cartella dei dati, da non cancellare), `E2E_CHANNEL=chromium` per il browser scaricato da Playwright (`pnpm exec playwright install chromium`).
- **Cosa coprono:** login con cambio di organizzazione e MFA, portale del richiedente, stage-gate con gate bloccato, approvazione dall'inbox, analisi tecnica, Gantt, valutazione, requisiti con evidenza e deroga, board, campi personalizzati con query, editor dei processi, ⌘K, temi, graduatoria, cruscotto, 5 pagine a 375 px senza scorrimento orizzontale e un controllo di accessibilità (axe, WCAG A e AA) su tutte le pagine principali.
- **Dati:** ogni test crea i propri elementi con nomi unici e gira in parallelo agli altri sullo stesso server; un test non deve dipendere dai conteggi globali (il cruscotto, ad esempio, si restringe a un'unità creata dal test).
- **CI:** il workflow `e2e.yml` li esegue a ogni push su main e su ogni pull request, in parallelo a `ci.yml`; si lancia anche a mano (Actions, «Run workflow»).

## Benchmark (architecture.md §7)
Strumento di sviluppo incluso nel binario; mai su database con dati veri.
```bash
export REGIA_ADMIN_PASSWORD='una-password-di-prova'
make build
./bin/regia bench seed --data ./data-bench --tenant bench --elements 1000000 --batch 10000
./bin/regia serve --data ./data-bench --http 127.0.0.1:8091 &
./bin/regia bench load --url http://127.0.0.1:8091 --elements 1000000 --duration 60s --concurrency 8 --pid $(pgrep -f "serve --data ./data-bench")
```
- **Spazio e tempi:** 1.000.000 elementi occupano circa 8 GB e si caricano in 8-10 minuti su un M3; 300.000 elementi, 2,3 GB e 2,5 minuti (la misura del 2026-11-24, vedi [benchmark.md](../benchmark.md)). `--batch 10000` su una macchina carica: ogni COPY deve stare nei 15 s del limite per istruzione.
- **Il generatore** (`internal/bench`) carica con COPY il 40% di richieste (con dati della domanda, 4 stakeholder, valori dei campi, storia, approvazioni) e, per il resto, i dati di tutti i moduli: impatti, valutazioni, requisiti con le celle della matrice (più una richiesta con 300 requisiti × 30 impatti), epic, story e task negli sprint di 12 team (sprint passati chiusi, quello di oggi attivo), commenti, attività, notifiche e un campo personalizzato numerico. `--only-requests` carica solo le richieste, come la prima misura.
- **Gli scenari:** liste, ricerche e query della prima misura; board (con e senza filtro), backlog, cruscotto, inbox di approvazioni, valutazioni e requisiti, notifiche, pagina di dettaglio completa (6 chiamate in parallelo più la storia), roadmap, campo numerico, ⌘K per parola e per chiave, matrice 300×30. `--only board,backlog` ne sceglie alcuni.
- **L'esito:** ogni scenario gira due volte da solo prima della misura (la prima chiamata, a freddo, è nella colonna «primo»); poi p50, p95, p99 e massimo sotto carico, confrontati con il budget (p95 100 ms, ricerca 200 ms), più la memoria massima del server (budget 50 MB). Il comando esce con errore se un budget salta.
- **Memoria:** con `REGIA_PPROF=127.0.0.1:8189` il server espone i profili di Go (solo su un indirizzo di loopback): `go tool pprof -top http://127.0.0.1:8189/debug/pprof/heap`, `.../allocs` per chi alloca di più, `.../profile?seconds=15` per la CPU. `GOMEMLIMIT` e `GOGC` sovrascrivono il limite morbido del server (30 MiB).
- **Il cruscotto** parte con una visita a ciascuna unità (il costo a freddo, 0,3-1,6 s, è nella colonna «primo»): ogni variante della vista è una voce di cache diversa.
- **Attenzione alla macchina:** la misura vale solo a macchina quieta. Con un carico di sistema alto (altri processi, altre istanze di Postgres) i p95 raddoppiano o triplicano.

Risultati e correzioni del primo benchmark: [benchmark.md](../benchmark.md). Dopo la misura si può cancellare `data-bench`, che si rigenera.
