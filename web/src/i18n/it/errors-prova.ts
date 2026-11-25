// Messaggi di validazione dell'ambiente di prova.
import type { Source } from "../types";

export default {
  "errors.validation.sandbox.not_linked": "Non c'è nessun ambiente di prova collegato a questa organizzazione.",
  "errors.validation.sandbox.exists": "Esiste già un ambiente di prova collegato: entra in quello o ricrealo.",
  "errors.validation.sandbox.in_sandbox": "Questo è già un ambiente di prova: la prova si crea dall'organizzazione principale.",
  "errors.validation.sandbox.slug_taken": "Esiste già un'organizzazione con l'identificativo della prova: contatta chi amministra la piattaforma.",
  "errors.validation.sandbox.nothing_selected": "Scegli almeno un oggetto da promuovere.",
  "errors.validation.sandbox.item_unknown": "Uno degli oggetti scelti non è più tra le differenze: ricarica la pagina e scegli di nuovo.",
  "errors.validation.sandbox.dependency": "Un oggetto richiede qualcosa che nel principale non c'è: promuovi insieme anche quello (di solito il campo o il tipo che usa).",
} satisfies Source;
