# Board per team: colonne proprie e modelli

Ondata 3, `replica/usabilita/configurazione.md` §3.14. La board di un team (lo spazio di delivery) ha le colonne che il team sceglie; senza configurazione resta una colonna per fase dei processi, come prima.

## Dati

- Configurazione pubblicata: vista di sistema `board:<spazio>` (tabella `views`), JSON `BoardSettings` in `internal/modules/work/board.go`. Una colonna ha `name`, `phases`, `wip`, `color` e `type_phases` (fasi diverse per tipo: se il tipo ha una voce, vale quella al posto di `phases`). Campi di impostazione: `model`, `no_sprint` (flusso continuo: la board non mostra né filtra per sprint), `sort` (`""` manuale, `due` per scadenza).
- Bozza: tabella `board_drafts` (migrazione `0068_board_team.sql`, una riga per spazio, RLS). La bozza può avere colonne senza fasi; la pubblicazione le ammette e l'anteprima le segnala.

## Spostamento

`Move` porta la carta alla prima fase della colonna che il processo del tipo ammette dalla fase attuale (`targetPhase` in `board_team.go`, usa `engine.Available`); se nessuna è raggiungibile tenta la prima che il tipo ha, e il motore spiega perché non si può (passaggio non previsto, gate, motivazione). Un tipo senza fasi nella colonna dà `validation.board.type_lacks_phase`.

## API (`internal/api/work_board_team.go`)

- `GET /work/spaces/{key}/board/config`: pubblicata, bozza, fasi dei processi, modelli, anteprima.
- `POST .../board/preview` con `{model}` o `{settings}`: colonne con i conteggi di oggi, fasi senza colonna, colonne vuote. Non salva.
- `PUT|DELETE .../board/draft`, `POST .../board/publish`.
- `POST /work/spaces` accetta `model` per applicare un modello alla creazione dello spazio.

## Modelli

Scrum, Kanban (senza sprint, WIP 3 e 2 sulle colonne centrali) e Supporto (senza sprint, ordine per `due_at`) sono in `boardModels`. Ogni fase dei processi va nella prima colonna adatta per categoria e parole del nome (revisione, verifica, attesa, cliente); le colonne che non trovano fasi restano vuote e l'anteprima lo dice. Si cambia modello quando si vuole: anteprima, bozza, pubblica.

## Interfaccia

`web/src/pages/BoardColumnsEditor.tsx`, dentro ogni team di Tempo → Team (`/tempo/team`). Colonne con nome, colore, WIP, fasi da menu multiplo, per tipo, riordino con frecce o trascinamento. Sul telefono tutto in una colonna, bersagli da 44 px. Un team senza spazio propone di crearlo scegliendo il modello. Testi in `lavoro-board.ts` (chiavi `lavoro.boardTeam.`) e `errors-board.ts`.

## Non fatto

- La swimlane per epic non è nell'editor (resta nelle impostazioni della board); il backlog continua a mostrare gli sprint anche per i team a flusso continuo.
- L'ordine per scadenza dipende da `due_at` dell'elemento, non dalla scadenza SLA dei ticket.
