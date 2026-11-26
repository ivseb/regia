// Messaggi di validazione delle organizzazioni create dall'interfaccia.
import type { Source } from "../types";

export default {
  "errors.platform.admin_required": "Solo il superamministratore dell'istanza può gestire le organizzazioni.",
  "errors.validation.organizzazioni.name_invalid": "Scrivi il nome dell'organizzazione (massimo 120 caratteri).",
  "errors.validation.organizzazioni.slug_invalid": "Identificativo non valido: minuscole, cifre e trattini, da 2 a 63 caratteri.",
  "errors.validation.organizzazioni.slug_taken": "Esiste già un'organizzazione con questo identificativo: scegline un altro.",
  "errors.validation.organizzazioni.language_invalid": "Lingua non supportata.",
  "errors.validation.organizzazioni.currency_invalid": "Valuta non valida: codice di tre lettere, per esempio EUR.",
  "errors.validation.organizzazioni.admin_email_invalid": "L'indirizzo email dell'altro amministratore non è valido.",
  "errors.platform.cannot_suspend_current": "Non puoi sospendere l'organizzazione in cui stai lavorando: entra prima in un'altra.",
  "errors.platform.tenant_not_found": "Questa organizzazione non esiste più.",
  "errors.tenant.suspended": "L'organizzazione è sospesa: rivolgiti all'amministratore dell'istanza.",
} satisfies Source;
