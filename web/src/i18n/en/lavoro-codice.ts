import type { Translation } from "../types";
import type it from "../it/lavoro-codice";

export default {
  "lavoro.codice.titolo": "Code",
  "lavoro.codice.commit": "{n} commits",
  "lavoro.codice.pullRequest": "PR #{n}",
  "lavoro.codice.rilasciato": "Released in {versione} on {data}, lead time {giorni} days.",
  "lavoro.codice.rilasciatoOggi": "Released in {versione} on {data}, lead time under a day.",
  "lavoro.codice.nonRilasciato": "Not yet released to production.",
  "lavoro.codice.apri": "Open commit {sha}",
} satisfies Translation<typeof it>;
