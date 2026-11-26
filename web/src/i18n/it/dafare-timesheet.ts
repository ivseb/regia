// «Da fare»: la decisione sul timesheet in linea, senza aprire la pagina del tempo. Le chiavi iniziano
// con «dafare.ts.»: vedi web/src/i18n/GLOSSARIO.md.
import type { Source } from "../types";

export default {
  "dafare.ts.approve": "Approva",
  "dafare.ts.reject": "Respingi",
  "dafare.ts.confirm": "Conferma il rifiuto",
  "dafare.ts.cancel": "Annulla",
  "dafare.ts.reason": "Motivo del rifiuto",
  "dafare.ts.reasonHint": "Obbligatorio: la persona lo legge e corregge la settimana.",
  "dafare.ts.reasonRequired": "Scrivi il motivo del rifiuto.",
  "dafare.ts.open": "Apri il timesheet",
  "dafare.ts.detail": "Ore per giorno",
  "dafare.ts.total": "Totale: {time}",
  "dafare.ts.approved": "Settimana di {member} approvata.",
  "dafare.ts.rejected": "Settimana di {member} respinta: la persona può correggerla.",
} satisfies Source;
