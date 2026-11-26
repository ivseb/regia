// Blocco «Codice» della scheda di un elemento: i commit che ne citano la chiave (area «lavoro»).
import type { Source } from "../types";

export default {
  "lavoro.codice.titolo": "Codice",
  "lavoro.codice.commit": "{n} commit",
  "lavoro.codice.pullRequest": "PR n. {n}",
  "lavoro.codice.rilasciato": "Rilasciato in {versione} il {data}, lead time {giorni} giorni.",
  "lavoro.codice.rilasciatoOggi": "Rilasciato in {versione} il {data}, lead time inferiore a un giorno.",
  "lavoro.codice.nonRilasciato": "Non ancora rilasciato in produzione.",
  "lavoro.codice.apri": "Apri il commit {sha}",
} satisfies Source;
