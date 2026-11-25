// Modello a blocchi delle pagine del Knowledge: il formato salvato resta Markdown.
//
// Il testo si legge in una sequenza piatta di blocchi (paragrafo, titolo, voce di elenco,
// citazione, avviso, codice, tabella, immagine, allegato, riferimento a un elemento, separatore,
// «grezzo» per tutto ciò che l'editor non sa modificare) e si riscrive in Markdown.
// Stabilità: ogni blocco legge e ricorda il proprio testo originale (`src`) e la distanza dal
// blocco precedente (`pre`). Finché un blocco non viene toccato si riscrive tale e quale, quindi
// un giro lettura → scrittura senza modifiche non cambia nemmeno uno spazio; un blocco toccato si
// riscrive in forma canonica. Modulo senza dipendenze (né DOM né React): lo prova `pnpm test`.

export type Kind = "paragraph" | "heading" | "bullet" | "number" | "task" | "quote" | "callout" | "code" | "table" | "image" | "file" | "ref" | "divider" | "raw";

export type Block = {
  id: string;
  kind: Kind;
  /** Markdown in linea (paragrafo, titolo, voce, citazione, avviso); testo puro per codice e grezzo; chiave per ref; testo alternativo per immagine; nome per allegato. */
  text: string;
  /** Titolo: 1-6. Elenchi: livello di annidamento. */
  level: number;
  checked?: boolean;
  /** Avviso: NOTA, SUGGERIMENTO, IMPORTANTE, ATTENZIONE, PERICOLO. */
  callout?: string;
  lang?: string;
  fence?: string;
  url?: string;
  /** Elenco numerato: numero della voce. */
  num?: number;
  /** Tabella: righe di celle (la prima è l'intestazione) e allineamenti. */
  rows?: string[][];
  align?: string[];
  /** Testo e distanza originali, finché il blocco non è modificato. */
  src?: string;
  pre?: string;
  /** Contatore per forzare il ridisegno del testo quando cambia da programma. */
  rev?: number;
};

export type Doc = { blocks: Block[]; tail: string; crlf: boolean };

export const CALLOUTS = ["NOTA", "SUGGERIMENTO", "IMPORTANTE", "ATTENZIONE", "PERICOLO"] as const;
const CALLOUT_WORDS: Record<string, string> = {
  nota: "NOTA", note: "NOTA", info: "NOTA",
  suggerimento: "SUGGERIMENTO", tip: "SUGGERIMENTO",
  importante: "IMPORTANTE", important: "IMPORTANTE",
  attenzione: "ATTENZIONE", warning: "ATTENZIONE",
  pericolo: "PERICOLO", caution: "PERICOLO",
};

const HEADING = /^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$/;
const FENCE = /^\s*(```|~~~)\s*([\w+#.-]*)\s*$/;
const LIST = /^(\s*)([-*+]|\d{1,4}[.)])\s+(.*)$/;
const HR = /^\s{0,3}([-*_])(\s*\1){2,}\s*$/;
const TABLE_SEP = /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/;
const CALLOUT = /^\[!([A-Za-zÀ-ÿ]+)\]\s*(.*)$/;
const KEY_ONLY = /^\s*([A-Z][A-Z0-9_]{1,19}-[1-9][0-9]{0,17})\s*$/;
const IMAGE_ONLY = /^\s*!\[([^\]\n]*)\]\(([^)\s]+)\)\s*$/;
const FILE_ONLY = /^\s*\[([^\]\n]+)\]\((\/api\/v1\/attachments\/[0-9a-f-]{36}\/content(?:\?download=1)?)\)\s*$/i;

let counter = 0;
export const newId = () => `b${Date.now().toString(36)}${(counter++).toString(36)}`;

export function newBlock(kind: Kind, over: Partial<Block> = {}): Block {
  const base: Block = { id: newId(), kind, text: "", level: kind === "heading" ? 2 : 0 };
  if (kind === "callout") base.callout = "NOTA";
  if (kind === "task") base.checked = false;
  if (kind === "table") {
    base.rows = [["", ""], ["", ""]];
    base.align = ["left", "left"];
  }
  return { ...base, ...over };
}

export const isList = (k: Kind) => k === "bullet" || k === "number" || k === "task";
/** Blocchi il cui testo è Markdown in linea, modificabile con la barra sulla selezione. */
export const isRich = (k: Kind) => k === "paragraph" || k === "heading" || isList(k) || k === "quote" || k === "callout";

export function splitRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|") && !s.endsWith("\\|")) s = s.slice(0, -1);
  const cells: string[] = [];
  let cur = "";
  for (let i = 0; i < s.length; i++) {
    if (s[i] === "\\" && s[i + 1] === "|") {
      cur += "|";
      i++;
    } else if (s[i] === "|") {
      cells.push(cur.trim());
      cur = "";
    } else cur += s[i];
  }
  cells.push(cur.trim());
  return cells;
}

// ---------------------------------------------------------------- lettura

export function parseBlocks(md: string): Doc {
  const crlf = md.includes("\r\n");
  const lines = md.replace(/\r\n/g, "\n").split("\n");
  const blocks: Block[] = [];
  let prevEnd = 0;
  const push = (b: Partial<Block> & { kind: Kind }, from: number, to: number) => {
    const blanks = lines.slice(prevEnd, from).map((l) => l + "\n").join("");
    blocks.push(newBlock(b.kind, { ...b, pre: (blocks.length ? "\n" : "") + blanks, src: lines.slice(from, to).join("\n") }));
    prevEnd = to;
  };
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    const start = i;
    const fence = FENCE.exec(line);
    if (fence) {
      const code: string[] = [];
      i++;
      while (i < lines.length && !FENCE.test(lines[i])) code.push(lines[i++]);
      i = Math.min(i + 1, lines.length);
      push({ kind: "code", text: code.join("\n"), lang: fence[2], fence: fence[1] }, start, i);
      continue;
    }
    const h = HEADING.exec(line);
    if (h) {
      i++;
      push({ kind: "heading", level: h[1].length, text: h[2] }, start, i);
      continue;
    }
    if (HR.test(line) && !LIST.test(line)) {
      i++;
      push({ kind: "divider" }, start, i);
      continue;
    }
    if (line.includes("|") && i + 1 < lines.length && TABLE_SEP.test(lines[i + 1]) && lines[i + 1].includes("-")) {
      const head = splitRow(line);
      const align = splitRow(lines[i + 1]).map((c) => (c.startsWith(":") && c.endsWith(":") ? "center" : c.endsWith(":") ? "right" : "left"));
      i += 2;
      const rows: string[][] = [head];
      while (i < lines.length && lines[i].includes("|") && lines[i].trim() !== "") rows.push(splitRow(lines[i++]));
      const cols = head.length;
      push({ kind: "table", rows: rows.map((r) => Array.from({ length: cols }, (_, j) => r[j] ?? "")), align: Array.from({ length: cols }, (_, j) => align[j] ?? "left") }, start, i);
      continue;
    }
    const lm = LIST.exec(line);
    if (lm) {
      // Ogni voce è un blocco; le righe rientrate che seguono restano nella voce.
      while (i < lines.length) {
        const m = LIST.exec(lines[i]);
        if (!m) break;
        const from = i;
        const text = [m[3]];
        i++;
        while (i < lines.length && lines[i].trim() !== "" && /^\s{2,}/.test(lines[i]) && !LIST.test(lines[i])) text.push(lines[i++].trim());
        const task = /^\[( |x|X)\]\s+/.exec(text[0]);
        const indent = m[1].replace(/\t/g, "    ").length;
        const level = Math.min(Math.ceil(indent / 2), 6);
        if (task) push({ kind: "task", level, checked: task[1] !== " ", text: [text[0].slice(task[0].length), ...text.slice(1)].join("\n") }, from, i);
        else if (/\d/.test(m[2])) push({ kind: "number", level, num: parseInt(m[2], 10), text: text.join("\n") }, from, i);
        else push({ kind: "bullet", level, text: text.join("\n") }, from, i);
      }
      continue;
    }
    if (/^\s*>/.test(line)) {
      const quote: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) quote.push(lines[i++].replace(/^\s*>\s?/, ""));
      const c = CALLOUT.exec(quote[0] ?? "");
      const kind = c ? CALLOUT_WORDS[c[1].toLowerCase()] : undefined;
      const inner = kind ? [c![2], ...quote.slice(1)].filter((l, j) => j > 0 || l.trim() !== "") : quote;
      const nested = inner.some((l) => LIST.test(l) || HEADING.test(l) || FENCE.test(l) || /^\s*>/.test(l) || (HR.test(l) && !LIST.test(l)) || TABLE_SEP.test(l));
      if (nested) push({ kind: "raw", text: lines.slice(start, i).join("\n") }, start, i);
      else if (kind) push({ kind: "callout", callout: kind, text: inner.join("\n") }, start, i);
      else push({ kind: "quote", text: inner.join("\n") }, start, i);
      continue;
    }
    if (line.trim() === "") {
      i++;
      continue;
    }
    const para: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !FENCE.test(lines[i]) &&
      !HEADING.test(lines[i]) &&
      !LIST.test(lines[i]) &&
      !/^\s*>/.test(lines[i]) &&
      !(HR.test(lines[i]) && para.length === 0) &&
      !(lines[i].includes("|") && i + 1 < lines.length && TABLE_SEP.test(lines[i + 1]))
    ) {
      para.push(lines[i++]);
    }
    if (para.length === 0) para.push(lines[i++]);
    const one = para.length === 1 ? para[0] : "";
    let m: RegExpExecArray | null;
    if (one && (m = IMAGE_ONLY.exec(one))) push({ kind: "image", text: m[1], url: m[2] }, start, i);
    else if (one && (m = FILE_ONLY.exec(one))) push({ kind: "file", text: m[1], url: m[2] }, start, i);
    else if (one && (m = KEY_ONLY.exec(one))) push({ kind: "ref", text: m[1] }, start, i);
    else push({ kind: "paragraph", text: para.join("\n") }, start, i);
  }
  const tail = prevEnd < lines.length ? (blocks.length ? "\n" : "") + lines.slice(prevEnd).join("\n") : "";
  return { blocks, tail, crlf };
}

export function emptyDoc(): Doc {
  return { blocks: [newBlock("paragraph")], tail: "", crlf: false };
}

// ---------------------------------------------------------------- scrittura

const cell = (s: string) => s.replace(/\|/g, "\\|").replace(/\n/g, " ");

/** Testo canonico di un blocco, senza la distanza dal precedente. */
export function render(b: Block): string {
  const lines = b.text.split("\n");
  const indent = "  ".repeat(b.level);
  const cont = (pad: string) => [lines[0], ...lines.slice(1).map((l) => pad + l)].join("\n");
  switch (b.kind) {
    case "paragraph":
    case "raw":
      return b.text;
    case "heading":
      return "#".repeat(Math.min(Math.max(b.level, 1), 6)) + " " + b.text;
    case "bullet":
      return indent + "- " + cont(indent + "  ");
    case "number": {
      const mark = `${b.num ?? 1}. `;
      return indent + mark + cont(indent + " ".repeat(mark.length));
    }
    case "task":
      return indent + (b.checked ? "- [x] " : "- [ ] ") + cont(indent + "  ");
    case "quote":
      return lines.map((l) => (l === "" ? ">" : "> " + l)).join("\n");
    case "callout":
      return [`> [!${b.callout ?? "NOTA"}] ${lines[0]}`.trimEnd(), ...lines.slice(1).map((l) => (l === "" ? ">" : "> " + l))].join("\n");
    case "code": {
      const f = b.fence ?? "```";
      return `${f}${b.lang ?? ""}\n${b.text}\n${f}`;
    }
    case "table": {
      const rows = b.rows ?? [[""]];
      const al = (a: string | undefined) => (a === "center" ? ":---:" : a === "right" ? "---:" : "---");
      const row = (r: string[]) => "| " + r.map(cell).join(" | ") + " |";
      return [row(rows[0]), "|" + rows[0].map((_, j) => al(b.align?.[j])).join("|") + "|", ...rows.slice(1).map(row)].join("\n");
    }
    case "image":
      return `![${b.text}](${b.url ?? ""})`;
    case "file":
      return `[${b.text}](${b.url ?? ""})`;
    case "ref":
      return b.text;
    case "divider":
      return "---";
  }
}

const empty = (b: Block) => b.kind === "paragraph" && b.text === "" && b.src === undefined;
const family = (k: Kind) => (k === "number" ? "n" : k === "bullet" || k === "task" ? "b" : "");
const autoPre = (prev: Block | undefined, b: Block) => (!prev ? "" : family(prev.kind) !== "" && family(prev.kind) === family(b.kind) ? "\n" : "\n\n");

export function toMarkdown(doc: Doc): string {
  let out = "";
  let prev: Block | undefined;
  let prevSkipped = false;
  for (const b of doc.blocks) {
    if (empty(b)) {
      prevSkipped = true;
      continue;
    }
    const pre = b.pre !== undefined && !prevSkipped ? b.pre : autoPre(prev, b);
    out += pre + (b.src ?? render(b));
    prev = b;
    prevSkipped = false;
  }
  out += doc.tail;
  return doc.crlf ? out.replace(/\n/g, "\r\n") : out;
}

// ---------------------------------------------------------------- operazioni (sempre su copie)

/** Un blocco modificato perde il testo originale. */
export function touched(b: Block, over: Partial<Block>): Block {
  return { ...b, ...over, src: undefined };
}

/** Dopo uno spostamento, un inserimento o una rimozione: le distanze dei blocchi vicini tornano automatiche. */
export function reflow(blocks: Block[], from: number, to: number = from): Block[] {
  const out = blocks.slice();
  for (let i = Math.max(0, from - 1); i <= Math.min(out.length - 1, to + 1); i++) out[i] = { ...out[i], pre: undefined };
  return renumber(out);
}

/** Rinumera gli elenchi numerati: solo le voci il cui numero cambia perdono il testo originale. */
export function renumber(blocks: Block[]): Block[] {
  const count: number[] = [];
  return blocks.map((b) => {
    if (b.kind !== "number") {
      if (!isList(b.kind)) count.length = 0;
      else count.length = Math.min(count.length, b.level + 1);
      return b;
    }
    count.length = b.level + 1;
    count[b.level] = (count[b.level] ?? 0) + 1;
    const n = count[b.level];
    return b.num === n ? b : { ...b, num: n, src: undefined };
  });
}

export function moveBlock(blocks: Block[], index: number, delta: number): Block[] {
  const to = index + delta;
  if (to < 0 || to >= blocks.length || delta === 0) return blocks;
  const out = blocks.slice();
  const [b] = out.splice(index, 1);
  out.splice(to, 0, b);
  return reflow(out, Math.min(index, to), Math.max(index, to));
}

export function duplicateBlock(blocks: Block[], index: number): Block[] {
  const out = blocks.slice();
  out.splice(index + 1, 0, { ...blocks[index], id: newId(), pre: undefined });
  return reflow(out, index, index + 1);
}

export function removeBlock(blocks: Block[], index: number): Block[] {
  const out = blocks.slice();
  out.splice(index, 1);
  if (out.length === 0) out.push(newBlock("paragraph"));
  return reflow(out, Math.min(index, out.length - 1));
}

export function insertBlocks(blocks: Block[], index: number, items: Block[], replace = false): Block[] {
  const out = blocks.slice();
  out.splice(index, replace ? 1 : 0, ...items.map((b) => ({ ...b, pre: undefined })));
  return reflow(out, index, index + items.length - 1);
}

/** Cambia il tipo di un blocco tenendo il testo. I tipi con struttura propria tornano paragrafo con il loro Markdown. */
export function convertBlock(b: Block, kind: Kind, over: Partial<Block> = {}): Block {
  const plain = isRich(b.kind) || b.kind === "code" || b.kind === "raw";
  const raw = plain ? b.text : b.kind === "divider" ? "" : render(b);
  const text = kind === "heading" ? raw.replace(/\n/g, " ") : raw;
  const base: Block = { id: b.id, kind, text, level: kind === "heading" ? (over.level ?? 2) : isList(kind) && isList(b.kind) ? b.level : 0, rev: (b.rev ?? 0) + 1 };
  if (kind === "task") base.checked = false;
  if (kind === "callout") base.callout = over.callout ?? "NOTA";
  return { ...base, ...over, id: b.id };
}

/** Voci del menu «/» e del menu del blocco: ogni voce ha un identificatore (per l'etichetta nel catalogo) e il tipo che produce. */
export type Choice = { id: string; kind: Kind; level?: number; callout?: string; words: string };
export const CHOICES: Choice[] = [
  { id: "paragraph", kind: "paragraph", words: "testo paragrafo text" },
  { id: "h1", kind: "heading", level: 1, words: "titolo heading h1 1" },
  { id: "h2", kind: "heading", level: 2, words: "titolo heading h2 2 sottotitolo" },
  { id: "h3", kind: "heading", level: 3, words: "titolo heading h3 3" },
  { id: "bullet", kind: "bullet", words: "elenco puntato lista bullet" },
  { id: "number", kind: "number", words: "elenco numerato lista numbered" },
  { id: "task", kind: "task", words: "elenco di controllo attivita checklist todo" },
  { id: "quote", kind: "quote", words: "citazione quote" },
  { id: "note", kind: "callout", callout: "NOTA", words: "avviso nota callout info" },
  { id: "tip", kind: "callout", callout: "SUGGERIMENTO", words: "avviso suggerimento tip" },
  { id: "important", kind: "callout", callout: "IMPORTANTE", words: "avviso importante" },
  { id: "warning", kind: "callout", callout: "ATTENZIONE", words: "avviso attenzione warning" },
  { id: "caution", kind: "callout", callout: "PERICOLO", words: "avviso pericolo caution" },
  { id: "code", kind: "code", words: "codice code" },
  { id: "table", kind: "table", words: "tabella table" },
  { id: "image", kind: "image", words: "immagine allegato file image upload" },
  { id: "ref", kind: "ref", words: "elemento riferimento chiave item" },
  { id: "divider", kind: "divider", words: "separatore linea divider" },
];

const norm = (s: string) => s.toLowerCase().normalize("NFD").replace(/[̀-ͯ]/g, "");
export function filterChoices(label: (id: string) => string, q: string): Choice[] {
  const t = norm(q.trim());
  if (!t) return CHOICES;
  return CHOICES.filter((c) => norm(label(c.id) + " " + c.words).includes(t));
}

/** Un blocco appena creato dalla scelta. */
export function fromChoice(c: Choice, prev?: Block): Block {
  if (c.kind === "table") return newBlock("table");
  return newBlock(c.kind, { level: c.kind === "heading" ? (c.level ?? 2) : isList(c.kind) && prev && isList(prev.kind) ? prev.level : 0, callout: c.callout });
}

/** Sigle di avviso: nome nel catalogo (`knowledge.md.callout.*`). */
export const CALLOUT_LABEL: Record<string, "note" | "tip" | "important" | "warning" | "caution"> = {
  NOTA: "note", SUGGERIMENTO: "tip", IMPORTANTE: "important", ATTENZIONE: "warning", PERICOLO: "caution",
};

/** Scorciatoie di scrittura: «## », «- », «1. », «> », «[] » all'inizio di un paragrafo vuoto. */
export function shortcut(text: string): Partial<Block> & { kind: Kind } | null {
  let m: RegExpExecArray | null;
  if ((m = /^(#{1,3}) $/.exec(text))) return { kind: "heading", level: m[1].length };
  if (/^[-*] $/.test(text)) return { kind: "bullet" };
  if (/^1[.)] $/.test(text)) return { kind: "number", num: 1 };
  if (/^(\[\]|- \[ \]) $/.test(text)) return { kind: "task" };
  if (/^> $/.test(text)) return { kind: "quote" };
  return null;
}
