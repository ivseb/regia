# Modello «Persona sola / home lab»

Il pilota «Home lab» (una persona sola, un laboratorio personale) ha richiesto una trentina di chiamate per
arrivare a una configurazione semplice. Il modello `persona_sola` di «Inizia da qui → Modelli» la porta in un
passo, in **una transazione**, ed è **idempotente**: riapplicarlo non duplica team, tipi o versioni dei processi.

Codice: `internal/api/avvio_persona.go` (il passo) e `internal/api/avvio_modelli.go` (catalogo, anteprima,
applicazione). Interfaccia: `web/src/pages/Avvio.tsx`. Testi: `web/src/i18n/{it,en}/configurazione-persona.ts`.
Nessuna migrazione.

## Cosa fa

| Area | Effetto |
| --- | --- |
| Moduli | Accesi Work, Progetti (progetto da zero con Gantt e work package agili), DevSecOps, Knowledge e Architettura (serve al catalogo delle applicazioni del connettore); spenti Demand e portafoglio, GRC. |
| Tipi | `task` diventa «Attività» (etichetta inglese «Task» in `labels`); `bug` resta «Bug»; `idea` è un tipo nuovo del modulo work, creato con `engine.CreateType` da `task` (copia fasi, campi e spazi); se esiste archiviato si ripristina. `story`, `epic` e `sprint` si archiviano **solo se non hanno elementi** (update diretto, con evento di audit `type.update`). |
| Flusso | Uguale per i tre tipi: Da fare → In corso → Fatto, più Annullato (fuori percorso). Niente «In revisione», nessun gate sull'assegnatario; passaggi avanti e indietro come nel pacchetto `task_standard`. È una **nuova versione pubblicata** del processo di ciascun tipo (`SaveDraft` + `Publish`): le versioni precedenti non si toccano. Le opzioni di fase che il flusso non usa più e che nessun elemento usa si archiviano, così non compaiono come colonne. |
| Campi | Solo la priorità (`priorita`): `punti` e `stima` si tolgono dai tre tipi (`type_fields`). |
| Team e board | Un team (nome predefinito «Home lab», modificabile) con chi applica il modello come referente e membro, il suo spazio di delivery con la **chiave scelta dall'utente** (proposta `HL`) e una board **kanban senza sprint** con i tipi `task`, `bug` e `idea`. Le colonne si calcolano con `work.ModelSettings("kanban")` sui tre tipi e quelle senza fasi («In revisione») si tolgono. |

Il modello non scrive righe in `avvio_applicazioni` per i processi (solo quella del modello): i processi di
partenza modificati restano riconoscibili come tali nella scheda «Cosa è applicato» e si possono ripristinare.

## Parametri

Facoltativi, retrocompatibili con gli altri modelli (che li ignorano):

- `board_key`: chiave della board e dello spazio. Maiuscole, cifre e `_`, inizia con una lettera, al massimo 20
  caratteri (come `EnsureTeamSpace`). Vuota = `HL`.
- `team_name`: nome del team. Vuoto = «Home lab».

Anteprima: `GET /api/v1/avvio/modelli/persona_sola?board_key=HL&team_name=Home%20lab`. Applicazione:
`POST /api/v1/avvio/modelli/persona_sola` con corpo JSON `{"board_key": "HL", "team_name": "Home lab"}` (il corpo
può mancare).

Errori di campo (HTTP 422, `field: "board_key"`): chiave non valida (`validation.invalid_value`) o già usata da un
altro spazio (`validation.duplicate`). L'anteprima esegue il modello in un punto di salvataggio e lo annulla, quindi
mostra anche questi errori prima di applicare.

## Idempotenza

- Se esiste già un team con quel nome **e** ha già uno spazio, team e board risultano «già presenti» e la chiave non
  si controlla.
- Il processo di un tipo si ripubblica solo se la versione corrente non ha già le fasi Da fare, In corso, Fatto,
  Annullato e nessun gate.
- Il nome «Attività» si imposta solo se il tipo si chiama ancora «Task» (o è già «Attività» senza etichetta inglese):
  un nome personalizzato non si sovrascrive.

## Anteprima

L'elenco dice cosa nasce e cosa c'è già, per tipo di voce: moduli (accesi e spenti), tipi (anche quelli che si
archiviano), campi, processi (una nuova versione ciascuno), team e board.

## Cosa resta fuori

- Il dialogo «Nuovo elemento» della board ha ancora le schede Story/Attività/Bug/Epic e il campo «Punti» fissi: dopo il
  modello la creazione di una Story fallisce (tipo archiviato). Idea non è nel dialogo. Lo sistema il lavoro su
  `web/src/pages/Board.tsx` e sul work management.
- `engine.UpdateType` rifiuta ancora di archiviare i tipi di sistema, per questo l'archiviazione è un update diretto.
- Prova: `internal/itest/modello_persona_test.go` e `web/e2e/modello-persona.spec.ts`.
