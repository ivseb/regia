# Versioni con data di entrata in vigore

«Tutto ciò che si configura evolve nel tempo»: il ciclo è sempre **modifica → anteprima → pubblica con data → transizione**. In questa ondata lo applicano i processi; il componente e l'aiuto backend sono generici.

## Modello (migrazione 0069)
- `process_versions` ha `effective_from` (da quando vale per le richieste nuove), `effective_to` (fine facoltativa, modificabile anche dopo), `deprecated_at` (da quando è stata sostituita) e `transition` (regola di transizione, json).
- Nuovo stato `scheduled`: pubblicata con una data futura. La versione in vigore è quella con stato `published`: resta una sola, quindi tutto il codice esistente (avvio, mappa, valutazioni…) continua a leggere `status = 'published'`.
- Stato derivato (`engine.Lifecycle`, campo `lifecycle`): `draft`, `scheduled`, `in_force`, `deprecated`, `concluded` (fine passata), `withdrawn` (sostituita prima di entrare in vigore). La data di fine è informativa: non blocca l'avvio di richieste se non c'è una versione successiva.
- `process_guides`: guide del processo (`phase` nulla) o di una fase; tipo `document`, `link` o `knowledge` (chiave della pagina). RLS del tenant.

## Regola di transizione
`{"default": "resta", "phases": {"<fase>": "auto" | "resta" | "manuale"}}` sulle fasi delle versioni di partenza.
- Predefinita (`DefaultTransitionRule`): le fasi iniziali (categoria «da fare», sul percorso principale) passano da sole, le altre restano. Chi pubblica la cambia per fase nell'anteprima.
- `auto` migra con la macchina di migrazione esistente (`moveGroups`: storia di ogni richiesta + audit `process.transition`); una fase che la versione nuova non ha diventa `manuale` (`missing`).
- Senza `transition` nella pubblicazione non si sposta niente (comportamento storico, usato anche da «Pubblica vN» dell'editor e dall'import).

## Backend
- `POST /processes/{ref}/publish` accetta `effective_from`, `effective_to` (`AAAA-MM-GG` ora italiana, o RFC 3339) e `transition`. Data vuota o passata = subito: la precedente diventa deprecata e la regola si applica subito. Data futura = `scheduled`; una nuova pubblicazione sostituisce la programmata.
- `POST /processes/{ref}/publish/preview` (process.admin): quante richieste migrano, restano o vanno a mano, per versione di partenza e fase; propone la regola predefinita se non si passa `transition`.
- `PATCH /processes/{ref}/versions/{n}/vigore`: data di entrata in vigore (solo programmata) e di fine.
- Entrata in vigore: `engine.ActivateDue` (idempotente, `for update skip locked`) è chiamata dal lavoratore `RunVersioni` (ogni minuto, in `cmd/regia/serve.go`; solo i tenant con una versione scaduta) e, in modo pigro, all'inizio di `StartProcess`. Audit `process.activate` e `process.transition`.
- Guide: `GET /processes/{ref}/guides` (tutti), `POST/PATCH/DELETE` (process.admin). Gli indirizzi devono iniziare con `https://`, `http://` o `/`.
- `GET /elements/{ref}/percorso` aggiunge `guides` (della fase attuale e di tutto il processo) e `version_notice` (richiesta su una versione deprecata: versione nuova, data, `can_migrate` per chi ha process.admin).

## Frontend
- `web/src/versioni.tsx`: `VersionFlow` (passi, date, regola per fase, anteprima, conferma), `VersionLifecycle` (stato con date), `VigoreDates` (cambio date). Non conosce i processi: riceve `loadPreview` e `publish`. L'adattatore dei processi è `ProcessPublishFlow` in `pages/Processes.tsx`.
- Guide: `pages/ProcessGuides.tsx` (pagina del processo), `pages/PercorsoVersioni.tsx` (pannello del percorso: «Guida di questa fase» e avviso sobrio).
- Testi: area `configurazione-versioni.ts` (chiavi `configurazione.versioni.*`), anche quelli del percorso.

## Agganciare gli altri moduli
Questionari, scorecard, politiche di approvazione, matrici SLA, politiche della pipeline, regole, cataloghi di requisiti, budget:
1. Migrazione: `effective_from`, `effective_to`, `deprecated_at`, `transition` sulla tabella delle versioni, stato `scheduled`, un solo `scheduled` per oggetto (indice parziale) e il trigger di immutabilità che ammette `scheduled → published | superseded`.
2. Backend: `publish` con `PublishInput`-like (date + `TransitionRule`); `Lifecycle` per lo stato; una funzione di anteprima che conta per gruppo e una di applicazione che sposta gli elementi (o ricalcola ciò che dipende dalla versione); un `ActivateDue` del modulo registrato in `serve.go` accanto a `RunVersioni`, e lo stesso richiamo pigro nel punto in cui si sceglie la versione in vigore.
3. Frontend: `VersionFlow` con le due funzioni, `VersionLifecycle` nell'elenco delle versioni, `VigoreDates` per le date. Se l'oggetto non ha elementi da spostare l'anteprima mostra «Niente da spostare».
4. Percorso/avvisi: un avviso come `VersionNotice` dove si lavora sull'elemento ancora sulla versione deprecata.

## Non incluso
Guide per i moduli diversi dai processi, notifica per posta dell'avviso di deprecazione, migrazione di una sola richiesta dall'avviso (il link porta alla migrazione del processo).
