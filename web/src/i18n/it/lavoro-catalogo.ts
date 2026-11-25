// Catalogo del portale visto dal richiedente: carte con ricerca, bozza per voce, «È questa» sulle richieste simili.
// Le chiavi iniziano con «lavoro.»: vedi docs/sviluppo/testi-e-lingue.md.
import type { Source } from "../types";

export default {
  "lavoro.catalogo.search": "Cerca nel catalogo",
  "lavoro.catalogo.searchPlaceholder": "Cerca, per esempio «accesso»",
  "lavoro.catalogo.noMatch": "Nessuna voce corrisponde a «{text}».",
  "lavoro.catalogo.other": "Altro",
  "lavoro.catalogo.startNamed": "Inizia: {name}",
  "lavoro.catalogo.hasDraft": "Hai una bozza, «{title}», salvata {when}.",
  "lavoro.catalogo.resume": "Riprendi la bozza",
  "lavoro.catalogo.resumeNamed": "Riprendi la bozza: {name}",
  "lavoro.catalogo.restart": "Ricomincia",
  "lavoro.catalogo.confirmRestart": "Ricominciare da capo? La bozza che hai ora verrà eliminata.",
  "lavoro.catalogo.stepN": "Passo {n}",
  "lavoro.catalogo.missing": "Mancano alcune risposte obbligatorie: torna ai passi indicati con l'asterisco.",
  "lavoro.catalogo.similarHint": "Forse qualcuno ha già chiesto la stessa cosa. Se è questa, aggiungiti: non serve duplicarla.",
  "lavoro.catalogo.itIsThis": "È questa",
  "lavoro.catalogo.notThis": "No, è diversa",
  "lavoro.catalogo.supportText": "Ho la stessa esigenza: mi aggiungo a questa richiesta.",
} satisfies Source;
