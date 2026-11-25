import type { Translation } from "../types";
import type it from "../it/configurazione-requisiti-regole";

export default {
  "configurazione.requisitiQ.regola.guidaRequisito": "Creates a real requirement: when the assessment is closed (favorable outcome or with directives) it enters the Requirements module, as an application requirement if the assessment is about an impact, as a project one if it is about the request; the assessing team verifies it.",
  "configurazione.requisitiQ.regola.guidaPrescrizione": "Proposes a directive to the team, which accepts or discards it; an accepted directive then becomes a requirement.",
  "configurazione.requisitiQ.regola.titolo": "Requirement title (optional)",
  "configurazione.requisitiQ.regola.titoloPlaceholder": "If empty it is taken from the text",
  "configurazione.requisitiQ.regola.metodo": "Verification",
  "configurazione.requisitiQ.regola.manuale": "Manual, with a signature",
  "configurazione.requisitiQ.regola.evidenza": "Automatic from evidence",
  "configurazione.requisitiQ.regola.controllo": "Check key",
} satisfies Translation<typeof it>;
