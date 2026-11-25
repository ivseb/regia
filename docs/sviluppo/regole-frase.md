# Regole a frase («Quando… allora…»)

Ondata 3, proposta 6 (seconda metà). Le regole dei processi (`engine.Rule`: `on`, `when`, `actions`) si leggono e si compilano come una frase: **Quando** [evento] **solo se** [condizione] **allora** [azioni].

**Formato salvato.** Invariato: `{key, name, on, when, actions[]}` con `on` = `create`, `enter:<fase>` o `exit:<fase>`. Nessuna migrazione (`0062_regole_frase.sql` non serve).

**Editor** (`web/src/pages/processEditor/RuleSentence.tsx`, usato dalla scheda Regole di `Editor.tsx`)
- Evento: un solo menu («si crea», «entra nella fase «X»», «esce dalla fase «X»»). Il motore non conosce altri eventi, quindi non se ne offrono altri.
- Condizione: il costruttore unico (`ConditionBuilder`), con la frase «Solo se…» e il conteggio dal vivo; sotto, «Oggi questa regola scatterebbe su n elementi» (`/conditions/count` sul tipo di elemento del processo).
- Azioni, da un menu di nomi italiani: **Apri una valutazione** (`open_review`, tipo da menu; per i tipi con ambito impatto anche la condizione sui dati di ciascun impatto), **Applica le baseline di un catalogo** (`apply_baseline`), **Assegna un ruolo** (`assign_role`, a un gruppo o a un membro). Webhook e «aggiungi requisito» non sono azioni del motore: non si mostrano. Le altre azioni già presenti in una regola (di sistema: `requirement_stage`, `sync_*`…) restano modificabili a campi, nella stessa riga.
- Nome e chiave stanno in «Nome e chiave» (chiuso). L'elenco mostra la frase intera di ogni regola (`ruleSentence`).
- Arrivi da fuori: `/processi/<chiave>?regola=<chiave regola>` apre quella regola; `?nuova=open_review:<tipo>` ne aggiunge una già impostata (evento: la fase «Valutazione» se esiste, altrimenti la creazione).

**Tipo di valutazione** (`ReviewConfig.tsx`, «Quando si apre»): le regole pubblicate che aprono il tipo, come frasi, con il link alla regola nel processo e «Aggiungi una regola» (sceglie il processo e apre l'editor con la regola impostata).

**Validazione nel backend** (`internal/engine/process_validate.go`): ogni problema di una regola porta un `code` (`Problem.Code`), restituito anche come `code` dell'errore 422 alla pubblicazione (`RuleCodeError`). Codici `validation.rule.*`: `key_invalid`, `key_duplicate`, `event_invalid`, `phase_unknown`, `condition_invalid`, `no_actions`, `kind_required`, `kind_invalid`, `action_unknown`, `role_required`, `target_required`, `role_unknown`, `group_unknown`, `member_unknown`, `param_required` (parametri obbligatori dello schema dell'azione, es. `ref` di `open_review`), `param_invalid`. Testi in `errors-regole.ts` (it/en).

**Testi**: `configurazione-regole.ts` (chiavi `configurazione.regole.*`) e `errors-regole.ts`.
