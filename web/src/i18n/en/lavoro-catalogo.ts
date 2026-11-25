import type { Translation } from "../types";
import type it from "../it/lavoro-catalogo";

export default {
  "lavoro.catalogo.search": "Search the catalogue",
  "lavoro.catalogo.searchPlaceholder": "Search, for example “access”",
  "lavoro.catalogo.noMatch": "No entry matches “{text}”.",
  "lavoro.catalogo.other": "Other",
  "lavoro.catalogo.startNamed": "Start: {name}",
  "lavoro.catalogo.hasDraft": "You have a draft, “{title}”, saved {when}.",
  "lavoro.catalogo.resume": "Resume the draft",
  "lavoro.catalogo.resumeNamed": "Resume the draft: {name}",
  "lavoro.catalogo.restart": "Start over",
  "lavoro.catalogo.confirmRestart": "Start over? The draft you have now will be deleted.",
  "lavoro.catalogo.stepN": "Step {n}",
  "lavoro.catalogo.missing": "Some required answers are missing: go back to the steps marked with an asterisk.",
  "lavoro.catalogo.similarHint": "Someone may have already asked for the same thing. If this is it, add yourself: no need to duplicate it.",
  "lavoro.catalogo.itIsThis": "This is it",
  "lavoro.catalogo.notThis": "No, it is different",
  "lavoro.catalogo.supportText": "I have the same need: I am adding myself to this request.",
} satisfies Translation<typeof it>;
