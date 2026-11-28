# Regole a frase («Quando… allora…»)

Ondata 3, proposta 6 (seconda metà). Le regole dei processi (`engine.Rule`: `on`, `when`, `actions`) si leggono e si compilano come una frase: **Quando** [evento] **solo se** [condizione] **allora** [azioni].

**Formato salvato.** Invariato: `{key, name, on, when, actions[]}`. `on` vale `create`, `enter:<fase>` o `exit:<fase>` e, con le automazioni ([automazioni.md](automazioni.md)), anche `change:<campo>`, `at:<data>:<±giorni>`, `every:day` ed `every:week:<1-7>`.

**Editor** (`web/src/pages/processEditor/RuleSentence.tsx`, usato dalla scheda Regole di `Editor.tsx`)
- Evento: un solo menu con «si crea», «entra nella fase «X»», «esce dalla fase «X»», «cambia il campo «X»», «N giorni prima/dopo «data»», «ogni giorno» e «ogni settimana, il …» ([automazioni.md](automazioni.md)). Con «cambia il campo» la condizione può usare anche `prima.<campo>`.
- Condizione: il costruttore unico (`ConditionBuilder`), con la frase «Solo se…» e il conteggio dal vivo.
- Anteprima sotto la condizione:
  - per gli eventi sugli elementi, «Oggi questa regola scatterebbe su n elementi» (`/conditions/count` sul tipo di elemento del processo);
  - per gli eventi a tempo, «Nei prossimi 30 giorni scatterebbe su n elementi»;
  - per quelli periodici, il conteggio del prossimo giro (`/rules/preview`).
- Le regole a tempo già pubblicate hanno «Ultime esecuzioni» (`/processes/{ref}/rules/{rule}/runs`).
- Azioni, da un menu di nomi italiani:
  - **Apri una valutazione** (`open_review`): il tipo si sceglie da un menu; per i tipi con ambito impatto c'è anche la condizione sui dati di ciascun impatto.
  - **Applica le baseline di un catalogo** (`apply_baseline`).
  - **Assegna un ruolo** (`assign_role`), a un gruppo o a un membro.
  - Le azioni delle automazioni: **Imposta un campo** (`set_field`), **Manda una notifica** (`notify`), **Aggiungi un commento** (`add_comment`), **Crea un elemento collegato** (`create_linked`), **Invia un webhook** (`webhook`). I campi di testo sono modelli con i segnaposto `{{…}}`, inseribili da un menu.
  - «Aggiungi requisito» non è un'azione a sé: lo coprono `apply_baseline` e `create_linked`.
  - Le altre azioni già presenti in una regola (di sistema: `requirement_stage`, `sync_*`…) restano modificabili a campi, nella stessa riga.
- Nome e chiave stanno in «Nome e chiave» (chiuso). L'elenco mostra la frase intera di ogni regola (`ruleSentence`).
- Arrivi da fuori: `/processi/<chiave>?regola=<chiave regola>` apre quella regola; `?nuova=open_review:<tipo>` ne aggiunge una già impostata (evento: la fase «Valutazione» se esiste, altrimenti la creazione).

**Tipo di valutazione** (`ReviewConfig.tsx`, «Quando si apre»): le regole pubblicate che aprono il tipo, come frasi, con il link alla regola nel processo e «Aggiungi una regola» (sceglie il processo e apre l'editor con la regola impostata).

**Validazione nel backend** (`internal/engine/process_validate.go`): ogni problema di una regola porta un `code` (`Problem.Code`), restituito anche come `code` dell'errore 422 alla pubblicazione (`RuleCodeError`). Codici `validation.rule.*`: `key_invalid`, `key_duplicate`, `event_invalid`, `phase_unknown`, `condition_invalid`, `no_actions`, `kind_required`, `kind_invalid`, `action_unknown`, `role_required`, `target_required`, `role_unknown`, `group_unknown`, `member_unknown`, `field_unknown` (campo di `change:` che non è del tipo), `date_unknown` (data di `at:` che non è del tipo), `param_required` (parametri obbligatori dello schema dell'azione, es. `ref` di `open_review`), `param_invalid`. Testi in `errors-regole.ts` (it/en).

**Testi**: `configurazione-regole.ts` (chiavi `configurazione.regole.*`) e `errors-regole.ts`.
