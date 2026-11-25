// Controllo dei testi scritti nel codice (replica/lingua.md, «Controllo in CI»).
//
// Conta i testi dell'interfaccia lasciati nel JSX invece che nel catalogo (web/src/i18n):
//   1. testo nel JSX:            <p>Salva le modifiche</p>
//   2. attributi visibili:       aria-label, title, placeholder, alt, con testo fisso (anche dentro {…})
//   3. testi dentro {…} nel JSX: {n > 0 ? "Aperta" : "Chiusa"} (confronti e chiavi non contano)
//   4. tabelle di etichette (Record<…, string>, proprietà label/title/name…) e frasi italiane
//      scritte come letterali in qualunque punto di .ts e .tsx (findTableTexts, findPhrases)
// Non contano: acronimi, numeri, simboli, nomi di classe, i testi dentro t(…), i file di web/src/i18n.
// Un'eccezione motivata si segna con un commento `i18n-ok: motivo` sulla stessa riga o su quella prima.
//
// La soglia sta in scripts/i18n-soglia.json: il numero può solo scendere. Il conteggio per file si
// stampa sempre. Alla fine della conversione la soglia è 0.
//
//   node scripts/check-i18n.mjs             controlla (esce con errore se il numero sale)
//   node scripts/check-i18n.mjs --update    abbassa la soglia al conteggio attuale (mai la alza)
//   node scripts/check-i18n.mjs --quiet     stampa solo il totale
import { readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const src = join(root, "web", "src");
const thresholdFile = join(root, "scripts", "i18n-soglia.json");
const VISIBLE_ATTRS = new Set(["aria-label", "title", "placeholder", "alt"]);
const BRAND = new Set(["Regia"]);

/** Un testo da tradurre ha almeno due minuscole di fila (esclude acronimi, sigle e numeri). */
const hasWords = (s) => /\p{Ll}{2,}/u.test(s) && !BRAND.has(s.trim());

export function findTexts(code) {
  const out = [];
  const lineAt = (i) => code.slice(0, i).split("\n").length;
  const add = (i, kind, text) => out.push({ line: lineAt(i), kind, text: text.trim().replace(/\s+/g, " ").slice(0, 60) });
  const isIdent = (c) => c !== undefined && /[\w$]/.test(c);
  const prevNonSpace = (i) => {
    let j = i - 1;
    while (j >= 0 && /\s/.test(code[j])) j--;
    return j;
  };

  // «<» apre un elemento JSX se è subito seguito da un nome o da «>» e viene dove ci aspetta un'espressione.
  function jsxStart(i) {
    if (!/[A-Za-z>]/.test(code[i + 1] ?? "")) return false;
    if (isIdent(code[i - 1])) return false; // Foo<Bar>, useState<string>
    const j = prevNonSpace(i);
    if (j < 0) return true;
    if ("(,=?:&|{};>[!".includes(code[j])) return code[j] !== ">" || code[j - 1] === "=" || code[j - 1] === "/" || true;
    const word = /([A-Za-z]+)$/.exec(code.slice(Math.max(0, j - 10), j + 1))?.[1];
    return word === "return" || word === "default";
  }

  // Un letterale «ammesso» come testo: non è un confronto, un indice o una chiave.
  function looksLikeText(lit, start, end) {
    if (!hasWords(lit)) return false;
    if (!/\s/.test(lit) && /[-_/.:#=]/.test(lit.replace(/…$/, ""))) return false; // classi, percorsi, chiavi
    if (!/\s/.test(lit) && !/^\p{Lu}/u.test(lit)) return false; // «approved», «span»
    const before = code.slice(Math.max(0, start - 12), start).trimEnd();
    const after = code.slice(end, end + 12).trimStart();
    if (/(===|!==|==|!=|case)$/.test(before) || /^(===|!==|==|!=)/.test(after)) return false;
    if (before.endsWith("[") && after.startsWith("]")) return false;
    if (after.startsWith(":") && /[{,]$/.test(before)) return false; // chiave di oggetto
    return true;
  }

  function skipString(i) {
    const q = code[i];
    let j = i + 1;
    while (j < code.length && code[j] !== q) j += code[j] === "\\" ? 2 : 1;
    return j + 1;
  }
  function skipRegex(i) {
    let j = i + 1;
    let cls = false;
    while (j < code.length) {
      const c = code[j];
      if (c === "\\") j += 2;
      else if (c === "[") (cls = true), j++;
      else if (c === "]") (cls = false), j++;
      else if (c === "/" && !cls) return j + 1;
      else if (c === "\n") return j;
      else j++;
    }
    return j;
  }

  // Scansiona codice da i fino alla «}» che chiude (se stopAtBrace). ctx: "code" | "child" | "attr".
  function scanCode(i, ctx, stopAtBrace) {
    let depth = 0;
    const calls = []; // true = dentro una chiamata t(…)
    while (i < code.length) {
      const c = code[i];
      const n = code[i + 1];
      if (c === "/" && n === "/") {
        while (i < code.length && code[i] !== "\n") i++;
      } else if (c === "/" && n === "*") {
        const e = code.indexOf("*/", i + 2);
        i = e < 0 ? code.length : e + 2;
      } else if (c === '"' || c === "'") {
        const e = skipString(i);
        const lit = code.slice(i + 1, e - 1);
        if ((ctx === "child" || ctx === "attr") && !calls.includes(true)) {
          if (ctx === "attr" ? hasWords(lit) && !(!/\s/.test(lit) && /[-_/]/.test(lit)) : looksLikeText(lit, i, e)) add(i, ctx, lit);
        }
        i = e;
      } else if (c === "`") {
        i = scanTemplate(i, ctx, calls);
      } else if (c === "/" && /[(,=:[!&|?{};]|^$/.test(code[prevNonSpace(i)] ?? "")) {
        i = skipRegex(i);
      } else if (c === "<" && jsxStart(i)) {
        i = parseJsx(i);
      } else if (c === "(") {
        const w = /(?:^|[^\w$.])(t|tr|lookup)$/.test(code.slice(Math.max(0, i - 8), i));
        calls.push(w);
        i++;
      } else if (c === ")") {
        calls.pop();
        i++;
      } else if (c === "{") {
        depth++;
        i++;
      } else if (c === "}") {
        if (stopAtBrace && depth === 0) return i;
        depth--;
        i++;
      } else i++;
    }
    return i;
  }

  function scanTemplate(i, ctx, calls) {
    let j = i + 1;
    let quasi = "";
    const flush = () => {
      if (quasi && (ctx === "child" || ctx === "attr") && !calls.includes(true) && hasWords(quasi) && (ctx === "attr" || /\s/.test(quasi.trim()))) add(i, ctx, quasi);
      quasi = "";
    };
    while (j < code.length && code[j] !== "`") {
      if (code[j] === "\\") {
        j += 2;
      } else if (code[j] === "$" && code[j + 1] === "{") {
        flush();
        j = scanCode(j + 2, "code", true) + 1;
      } else quasi += code[j++];
    }
    flush();
    return j + 1;
  }

  // Elemento JSX che inizia in i («<»); restituisce la posizione dopo la sua fine.
  function parseJsx(i) {
    i++;
    if (code[i] === ">") return parseChildren(i + 1);
    let name = "";
    while (i < code.length && /[\w.:$-]/.test(code[i])) name += code[i++];
    for (;;) {
      while (i < code.length && /\s/.test(code[i])) i++;
      if (i >= code.length) return i;
      if (code[i] === "/" && code[i + 1] === ">") return i + 2;
      if (code[i] === ">") return parseChildren(i + 1);
      if (code[i] === "{") {
        i = scanCode(i + 1, "code", true) + 1; // {...props}
        continue;
      }
      let attr = "";
      while (i < code.length && /[\w:$-]/.test(code[i])) attr += code[i++];
      if (!attr) {
        i++;
        continue;
      }
      while (i < code.length && /\s/.test(code[i])) i++;
      if (code[i] !== "=") continue;
      i++;
      while (i < code.length && /\s/.test(code[i])) i++;
      const visible = VISIBLE_ATTRS.has(attr);
      if (code[i] === '"' || code[i] === "'") {
        const e = skipString(i);
        const lit = code.slice(i + 1, e - 1);
        if (visible && hasWords(lit) && !(!/\s/.test(lit) && /[-_/]/.test(lit))) add(i, "attr", lit);
        i = e;
      } else if (code[i] === "{") {
        i = scanCode(i + 1, visible ? "attr" : "code", true) + 1;
      } else if (code[i] === "<") {
        i = parseJsx(i);
      }
    }
  }

  function parseChildren(i) {
    let text = "";
    let textAt = i;
    const flush = () => {
      const t = text.replace(/&[a-z]+;/g, " ");
      if (hasWords(t)) add(textAt, "text", t);
      text = "";
    };
    while (i < code.length) {
      const c = code[i];
      if (c === "<" && code[i + 1] === "/") {
        flush();
        const e = code.indexOf(">", i);
        return e < 0 ? code.length : e + 1;
      }
      if (c === "<") {
        flush();
        i = parseJsx(i);
        textAt = i;
      } else if (c === "{") {
        flush();
        i = scanCode(i + 1, "child", true) + 1;
        textAt = i;
      } else {
        if (!text.trim()) textAt = i;
        text += c;
        i++;
      }
    }
    return i;
  }

  scanCode(0, "code", false);
  // Eccezioni motivate: «i18n-ok» sulla riga del testo o su quella prima.
  const lines = code.split("\n");
  return out.filter((t) => !/i18n-ok/.test(`${lines[t.line - 2] ?? ""}\n${lines[t.line - 1] ?? ""}`));
}

// Testi italiani nelle tabelle di etichette: `const X: Record<K, string> = { chiave: "Testo" }` e proprietà
// di oggetti come `label: "Testo"`, `title: "Testo"` (il JSX non li vede). I getter `get x() { return tr(…) }`
// non contano. Le classi CSS, i colori e gli identificativi si riconoscono e si escludono.
const LABEL_PROPS = new Set(["label", "labels", "title", "name", "text", "help", "hint", "description", "placeholder", "message", "empty", "note", "caption", "heading", "short", "long", "tooltip"]);
const CSS_TOKEN = /^[!a-z0-9:\-[\]/().%#_,]+$/;
const CSS_WORDS = new Set(["italic", "underline", "uppercase", "lowercase", "block", "inline", "hidden", "flex", "grid", "transparent", "none", "solid", "dashed", "bold", "normal"]);
// Nomi uguali in italiano e in inglese (tipi di lavoro, tecnologie, caratteri tipografici): non si traducono.
const SAME_IN_BOTH = new Set(["Epic", "Story", "Task", "Bug", "Sprint", "Stack", "Atkinson Hyperlegible", "IBM Plex Sans", "Source Sans 3", "Public Sans"]);
const isClassLike = (v) => v.split(/\s+/).every((tok) => CSS_WORDS.has(tok) || (CSS_TOKEN.test(tok) && /[-:[(/#%.0-9_]/.test(tok)));

function findTableTexts(code) {
  const out = [];
  const lineAt = (i) => code.slice(0, i).split("\n").length;
  const push = (i, text) => out.push({ line: lineAt(i), kind: "table", text: text.slice(0, 60) });
  const masked = code.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, " ")).replace(/(^|[^:])\/\/.*$/gm, (m, a) => a + " ".repeat(m.length - a.length));
  // 1) Tabelle dichiarate come Record<…, string> = { … }
  const re = /Record<[^=;]*?\bstring>\s*=\s*\{/g;
  let m;
  while ((m = re.exec(masked))) {
    let depth = 1;
    let i = re.lastIndex;
    const start = i;
    while (i < masked.length && depth > 0) {
      const c = masked[i];
      if (c === "{") depth++;
      else if (c === "}") depth--;
      else if (c === '"' || c === "'" || c === "`") {
        const q = c;
        i++;
        while (i < masked.length && masked[i] !== q) i += masked[i] === "\\" ? 2 : 1;
      }
      i++;
    }
    const body = masked.slice(start, i - 1);
    for (const e of body.matchAll(/(?:^|[,{\n])\s*(?:"[^"\n]*"|'[^'\n]*'|[\w$]+|\[[^\]\n]+\])\s*:\s*(["'])((?:\\.|(?!\1).)*)\1\s*(?=[,}\n])/g)) {
      const v = e[2];
      if (hasWords(v) && !isClassLike(v) && !SAME_IN_BOTH.has(v)) push(start + e.index + e[0].length - 1, v);
    }
  }
  // 2) Proprietà di oggetti con un nome da etichetta e un testo (non una chiave) come valore
  for (const e of masked.matchAll(/(?:^|[,{(\s])([A-Za-z]+)\s*:\s*(["'])((?:\\.|(?!\2).)*)\2\s*(?=[,}\n)])/g)) {
    if (!LABEL_PROPS.has(e[1])) continue;
    const v = e[3];
    if (!hasWords(v) || isClassLike(v) || SAME_IN_BOTH.has(v)) continue;
    if (!/\s/.test(v) && !/^\p{Lu}/u.test(v)) continue;
    const line = lineAt(e.index + 1);
    if (out.some((t) => t.line === line && t.text === v.slice(0, 60))) continue;
    push(e.index + 1, v);
  }
  const lines = code.split("\n");
  return out.filter((t) => !/i18n-ok/.test(`${lines[t.line - 2] ?? ""}\n${lines[t.line - 1] ?? ""}`));
}

// Frasi italiane scritte come letterali in qualunque punto del codice (messaggi, ternari, unità): una
// stringa con uno spazio e una parola italiana tipica, o con una lettera accentata. Le parole chiave
// della sintassi delle query e dei comandi sono eccezioni segnate con `i18n-ok`.
const IT_WORDS = /\b(della|delle|degli|dello|nella|nelle|nei|sono|non|che|per|con|una|gli|nessun|nessuna|è|più|già|giorno|giorni|ore|oggi|domani|tutti|tutte|ancora)\b/i;
function findPhrases(code) {
  const out = [];
  const lines = code.split("\n");
  let inBlock = false;
  lines.forEach((l, i) => {
    if (inBlock) {
      if (l.includes("*/")) inBlock = false;
      return;
    }
    if (/^\s*\/\*/.test(l) && !l.includes("*/")) inBlock = true;
    if (/^\s*(import|export .* from|\/\/|\*|\/\*|\{\/\*)/.test(l) || /i18n-ok/.test(l) || /i18n-ok/.test(lines[i - 1] ?? "")) return;
    for (const m of l.replace(/\/\/.*$/, "").replace(/\/\*.*?\*\//g, "").matchAll(/(["'`])((?:\\.|(?!\1).)*)\1/g)) {
      const v = m[2];
      if (!/\s/.test(v) || !/\p{Ll}{2,}/u.test(v)) continue;
      if (isClassLike(v)) continue;
      if (/[àèéìòù]/i.test(v) || IT_WORDS.test(v)) out.push({ line: i + 1, kind: "frase", text: v.slice(0, 60) });
    }
  });
  return out;
}

function walk(dir, acc = []) {
  for (const f of readdirSync(dir).sort()) {
    const p = join(dir, f);
    if (statSync(p).isDirectory()) {
      if (f === "i18n" || f === "fonts") continue;
      walk(p, acc);
    } else if (/\.tsx?$/.test(f) && !/\.d\.ts$/.test(f)) acc.push(p);
  }
  return acc;
}

const args = new Set(process.argv.slice(2));
if (import.meta.url === `file://${process.argv[1]}`) {
  const files = {};
  let total = 0;
  for (const f of walk(src)) {
    const text = readFileSync(f, "utf8");
    const found = [...(f.endsWith(".tsx") ? findTexts(text) : []), ...findTableTexts(text), ...findPhrases(text)];
    if (found.length) files[relative(root, f)] = found;
    total += found.length;
  }
  let saved = { total: Infinity, files: {} };
  try {
    saved = JSON.parse(readFileSync(thresholdFile, "utf8"));
  } catch {
    /* prima esecuzione: nessuna soglia */
  }
  if (!args.has("--quiet")) {
    const rows = Object.entries(files).sort((a, b) => b[1].length - a[1].length);
    for (const [f, items] of rows) {
      const was = saved.files?.[f];
      const delta = was === undefined ? "" : items.length === was ? "" : `  (era ${was})`;
      console.log(`${String(items.length).padStart(5)}  ${f}${delta}`);
    }
    if (args.has("--verbose")) for (const [f, items] of rows) for (const t of items) console.log(`  ${f}:${t.line} [${t.kind}] ${t.text}`);
  }
  console.log(`Testi italiani fuori da t(): ${total} (soglia ${saved.total === Infinity ? "assente" : saved.total}, ${Object.keys(files).length} file)`);
  if (args.has("--update")) {
    if (total > saved.total) {
      console.error("La soglia non si alza: sposta i testi nel catalogo o segna l'eccezione con «i18n-ok: motivo».");
      process.exit(1);
    }
    const counts = Object.fromEntries(Object.entries(files).map(([f, v]) => [f, v.length]));
    writeFileSync(thresholdFile, JSON.stringify({ total, files: counts }, null, 2) + "\n");
    console.log(`Soglia salvata: ${total}`);
  } else if (total > saved.total) {
    console.error(`Troppi testi fuori dal catalogo: ${total} contro una soglia di ${saved.total}. Usa t() (web/src/i18n, docs/sviluppo/testi-e-lingue.md).`);
    process.exit(1);
  } else if (total < saved.total) {
    console.log("Il numero è sceso: abbassa la soglia con `node scripts/check-i18n.mjs --update` e committa il file.");
  }
}
