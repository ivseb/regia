// Messaggi di validazione del catalogo del portale (voci, moduli e instradamento).
import type { Source } from "../types";

export default {
  "errors.validation.portal.entry_text_limits": "Nella voce il nome va da 1 a 80 caratteri, la descrizione fino a 400 e la categoria fino a 60.",
  "errors.validation.portal.entry_key_taken": "Esiste già una voce con la chiave «{key}».",
  "errors.validation.portal.group_unknown": "Il gruppo scelto non esiste.",
  "errors.validation.portal.nothing_to_publish": "Non ci sono modifiche da pubblicare.",
  "errors.validation.portal.form_text_limits": "Un testo del modulo è troppo lungo (massimo {max} caratteri).",
  "errors.validation.portal.form_too_many_steps": "Il modulo può avere al massimo {max} passi.",
  "errors.validation.portal.form_field_unknown": "Il campo «{field}» non si può usare in questa voce.",
  "errors.validation.portal.form_field_repeated": "Il campo «{field}» compare due volte nel modulo.",
  "errors.validation.portal.condition_invalid": "La condizione di «{field}» non è valida.",
  "errors.validation.portal.route_too_many": "Le regole di instradamento sono al massimo {max}.",
  "errors.validation.portal.route_priority_invalid": "Nella regola {rule} la priorità non è valida.",
  "errors.validation.portal.route_target_required": "Nella regola {rule} indica il ruolo e un team oppure un gruppo.",
  "errors.validation.portal.route_role_unknown": "Nella regola {rule} il ruolo scelto non esiste.",
  "errors.validation.portal.route_target_unknown": "Nella regola {rule} il team o il gruppo scelto non esiste.",
  "errors.validation.portal.field_required": "Il campo «{field}» è obbligatorio.",
  "errors.validation.portal.support_text": "Scrivi una riga per dire che hai la stessa esigenza.",
} satisfies Source;
