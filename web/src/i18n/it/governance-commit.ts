// Metriche di consegna: il primo commit come inizio del lead time (area «governance»).
import type { Source } from "../types";

export default {
  "governance.dora.primo_commit": "primo commit legato a un elemento",
  "governance.dora.primo_commit_2": "Primo commit legato a un elemento",
  "governance.dora.il_primo_commit": "Il primo commit che cita un elemento del servizio dopo il rilascio precedente; se manca, il primo controllo del commit rilasciato, poi la creazione del change.",
  "governance.dora.il_primo_commit_solo": "Il primo commit che cita un elemento del servizio (chiave tipo HL-12 nel messaggio) dopo il rilascio precedente, letto dai connettori GitHub e Gitea.",
} satisfies Source;
