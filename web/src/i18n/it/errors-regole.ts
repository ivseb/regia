// Messaggi di validazione delle regole «Quando… allora…» dei processi.
import type { Source } from "../types";

export default {
  "errors.validation.rule.key_invalid": "La chiave della regola non è valida: usa minuscole, cifre e trattino basso.",
  "errors.validation.rule.key_duplicate": "Due regole hanno la stessa chiave.",
  "errors.validation.rule.event_invalid": "L'evento della regola non è valido: scegli «Quando si crea», «Quando entra in…» o «Quando esce da…».",
  "errors.validation.rule.phase_unknown": "La fase dell'evento non esiste nel processo.",
  "errors.validation.rule.condition_invalid": "La condizione della regola non è valida.",
  "errors.validation.rule.no_actions": "La regola non fa nulla: aggiungi almeno un'azione.",
  "errors.validation.rule.kind_required": "Scegli il tipo di azione.",
  "errors.validation.rule.kind_invalid": "Il tipo di azione non è valido.",
  "errors.validation.rule.action_unknown": "Questa azione non è disponibile.",
  "errors.validation.rule.role_required": "Scegli il ruolo da assegnare.",
  "errors.validation.rule.target_required": "Scegli un gruppo oppure un membro a cui assegnare il ruolo.",
  "errors.validation.rule.role_unknown": "Il ruolo scelto non esiste.",
  "errors.validation.rule.group_unknown": "Il gruppo scelto non esiste.",
  "errors.validation.rule.member_unknown": "Il membro scelto non esiste o non è attivo.",
  "errors.validation.rule.param_required": "Nell'azione manca un dato obbligatorio.",
  "errors.validation.rule.param_invalid": "Un dato dell'azione non è valido.",
} satisfies Source;
