// Errori dell'AI, ondata 3 (assistente nella scheda, conteggio dei token).
import type { Source } from "../types";

export default {
  "errors.ai.budget_insufficient": "Il budget mensile dell'AI non basta per questa richiesta: prova con meno testo o aspetta il mese prossimo. Tutto il resto funziona come sempre.",
  "errors.ai.card_action_invalid": "Azione dell'assistente sconosciuta.",
  "errors.ai.draft_invalid": "Tipo di bozza sconosciuto: commento, risposta o descrizione.",
  "errors.ai.draft_empty": "La bozza è vuota.",
  "errors.ai.hint_invalid": "Indicazioni troppo lunghe: al massimo 500 caratteri.",
  "errors.ai.links_invalid": "Indica da uno a dieci collegamenti.",
  "errors.ai.title_invalid": "Scrivi un titolo di almeno 3 caratteri.",
  "errors.ai.type_invalid": "Tipo di elemento sconosciuto.",
  "errors.ai.proposal_invalid": "Non si può inserire: l'elemento non esiste, non è visibile o non lo puoi modificare.",
} satisfies Source;
