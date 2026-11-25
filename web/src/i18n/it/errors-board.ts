// Messaggi di validazione della board per team (colonne, bozza, modelli).
import type { Source } from "../types";

export default {
  "errors.validation.board.type_lacks_phase": "Un elemento di tipo {type} non ha fasi nella colonna «{column}»: assegna alla colonna una fase del suo processo o spostalo a mano.",
  "errors.validation.board.type_unknown": "Colonna «{name}»: il tipo {type} non è tra quelli della board.",
  "errors.validation.board.sort_invalid": "L'ordinamento della board può essere manuale o per scadenza.",
  "errors.validation.board.no_draft": "Non c'è una bozza da pubblicare: modifica prima le colonne.",
  "errors.validation.board.no_columns": "La board deve avere almeno una colonna.",
  "errors.validation.board.page_needs_all": "Le pagine per colonna valgono solo quando la board mostra tutto lo spazio, senza sprint.",
  "errors.validation.board.cursor": "La card da cui si riparte non è più in questa colonna: ricarica la board.",
} satisfies Source;
