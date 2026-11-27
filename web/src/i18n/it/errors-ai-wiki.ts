// Errori della wiki personale (AI, ondata 4).
import type { Source } from "../types";

export default {
  "errors.wiki.not_found": "Pagina non trovata: non esiste, è stata tolta o è di un'altra persona.",
  "errors.wiki.conflict": "La pagina è cambiata nel frattempo (forse l'ha aggiornata l'assistente): ricaricala e riprova.",
  "errors.wiki.not_owner": "La pagina è di un'altra persona: puoi solo leggerla.",
  "errors.wiki.title_invalid": "Scrivi un titolo (al massimo 120 caratteri).",
  "errors.wiki.title_taken": "Hai già una pagina con questo titolo.",
  "errors.wiki.body_invalid": "Testo troppo lungo: al massimo 50000 caratteri.",
  "errors.wiki.too_many": "La wiki ha già 200 pagine: togline qualcuna.",
  "errors.wiki.version_invalid": "Versione inesistente o non più conservata.",
  "errors.wiki.undo_not_latest": "Si annulla solo l'ultima modifica della pagina: per le precedenti ripristina una versione dalla storia.",
  "errors.wiki.add_invalid": "Indica un elemento oppure un testo da aggiungere.",
  "errors.wiki.text_invalid": "Testo troppo lungo: al massimo 8000 caratteri.",
  "errors.wiki.space_required": "Scegli lo spazio Knowledge.",
  "errors.wiki.no_member": "La wiki è di chi lavora nell'organizzazione.",
  "errors.ai.wiki_running": "L'aggiornamento della wiki è già in corso.",
  "errors.ai.wiki_no_tools": "Il modello della wiki non sa usare gli strumenti: chi amministra l'organizzazione deve sceglierne un altro.",
} satisfies Source;
