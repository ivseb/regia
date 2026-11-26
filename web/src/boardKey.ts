// Chiave della board di un team (pilota home lab, difetto 7): la stessa proposta del server
// (internal/modules/work/spaces.go, ProposeKey), mostrata mentre si scrive il nome del team.
// Il server la rende unica (WEB2…) se è già usata; quella scelta a mano si controlla e basta.

const STOP = new Set(["e", "di", "del", "della", "dei", "degli", "delle", "la", "il", "lo", "le", "gli", "and", "of", "the", "per"]);

/** Forma valida di una chiave: maiuscole, cifre e trattino basso, inizia con una lettera, al massimo 20 caratteri. */
export const BOARD_KEY_RE = /^[A-Z][A-Z0-9_]{0,19}$/;

/** Chiave proposta dal nome: con una parola le prime tre lettere, con più parole le iniziali (al massimo 4). */
export function proposeKey(name: string): string {
  const words = name
    .split(/[^\p{L}\p{N}]+/u)
    .map((w) => w.normalize("NFD").replace(/[̀-ͯ]/g, "").replace(/[^A-Za-z0-9]/g, "").toUpperCase())
    .filter((w) => w && !STOP.has(w.toLowerCase()));
  let key = words.length === 0 ? "TEAM" : words.length === 1 ? words[0].slice(0, 3) : words.map((w) => w[0]).join("").slice(0, 4);
  if (/^[0-9]/.test(key)) key = "T" + key;
  return key;
}
