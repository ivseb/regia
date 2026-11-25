// Errori della cabina di portafoglio: scenari e verbale della seduta.
import type { Source } from "../types";

export default {
  "errors.validation.portfolio.scenario_name_required": "Serve un nome per lo scenario.",
  "errors.validation.portfolio.scenario_limit": "Al massimo {max} scenari: eliminane uno.",
  "errors.validation.portfolio.minutes_no_decisions": "Nessun punto ha una decisione: il verbale si genera dopo le decisioni.",
  "errors.validation.portfolio.minutes_no_space": "Non c'è uno spazio di Knowledge in cui puoi scrivere.",
} satisfies Source;
