// Markdown in linea ⇄ DOM modificabile (editor a blocchi). Si usano solo nodi creati con
// createElement/createTextNode: nessun HTML grezzo entra mai nella pagina. Le parti di testo
// restano Markdown grezzo, così ciò che non si riconosce non va perso.

const SAFE_HREF = /^(https?:\/\/|mailto:|#|\/)/i;

/** Indirizzo accettato per un link scritto a mano: aggiunge https:// a «esempio.it». */
export function cleanHref(raw: string): string | null {
  const v = raw.trim();
  if (!v || /\s/.test(v)) return null;
  if (SAFE_HREF.test(v)) return v;
  if (/^[A-Z][A-Z0-9_]{1,19}-[1-9][0-9]*$/.test(v)) return v;
  if (/^[a-z][a-z0-9+.-]*:/i.test(v)) return null;
  return "https://" + v;
}

const TOKEN = new RegExp(
  [
    "(`[^`\\n]+`)",
    "(\\*\\*[^*\\n]+?\\*\\*|__[^_\\n]+?__)",
    "(~~[^~\\n]+?~~)",
    "(\\*[^*\\s][^*\\n]*?\\*|(?<![\\p{L}\\p{N}])_[^_\\s][^_\\n]*?_(?![\\p{L}\\p{N}]))",
    "(\\[[^\\]\\n]+\\]\\([^)\\s]+\\))",
  ].join("|"),
  "gu",
);

function build(md: string, into: Node, doc: Document) {
  const lines = md.split("\n");
  lines.forEach((line, li) => {
    if (li > 0) into.appendChild(doc.createElement("br"));
    let last = 0;
    for (const m of line.matchAll(TOKEN)) {
      const at = m.index ?? 0;
      if (at > last) into.appendChild(doc.createTextNode(line.slice(last, at)));
      const [all, code, bold, strike, italic, link] = m;
      if (code) {
        const e = doc.createElement("code");
        e.textContent = code.slice(1, -1);
        into.appendChild(e);
      } else if (bold) build(bold.slice(2, -2), into.appendChild(doc.createElement("strong")), doc);
      else if (strike) build(strike.slice(2, -2), into.appendChild(doc.createElement("s")), doc);
      else if (italic) build(italic.slice(1, -1), into.appendChild(doc.createElement("em")), doc);
      else if (link) {
        const sep = link.indexOf("](");
        const href = link.slice(sep + 2, -1);
        if (SAFE_HREF.test(href) || /^[A-Z][A-Z0-9_]*-\d+$/.test(href)) {
          const a = doc.createElement("a");
          a.setAttribute("href", href);
          a.setAttribute("rel", "noopener noreferrer");
          build(link.slice(1, sep), a, doc);
          into.appendChild(a);
        } else into.appendChild(doc.createTextNode(all));
      }
      last = at + all.length;
    }
    if (last < line.length) into.appendChild(doc.createTextNode(line.slice(last)));
  });
}

/** Riempie l'elemento con il Markdown in linea. */
export function fillInline(el: HTMLElement, md: string) {
  el.replaceChildren();
  build(md, el, el.ownerDocument);
}

const wrap = (mark: string, inner: string) => {
  const m = /^(\s*)([\s\S]*?)(\s*)$/.exec(inner)!;
  return m[2] === "" ? inner : m[1] + mark + m[2] + mark + m[3];
};

function walk(n: Node, on: Set<string>): string {
  if (n.nodeType === 3) return (n.nodeValue ?? "").replace(/ /g, " ").replace(/​/g, "");
  if (!(n instanceof HTMLElement)) return "";
  const tag = n.tagName;
  const mark = tag === "STRONG" || tag === "B" ? "**" : tag === "EM" || tag === "I" ? "*" : tag === "S" || tag === "STRIKE" || tag === "DEL" ? "~~" : "";
  if (tag === "BR") return "\n";
  if (tag === "CODE") return wrap("`", n.textContent?.replace(/​/g, "") ?? "");
  const nextOn = mark && !on.has(mark) ? new Set(on).add(mark) : on;
  const inner = [...n.childNodes].map((c) => walk(c, nextOn)).join("");
  if (mark) return on.has(mark) ? inner : wrap(mark, inner);
  if (tag === "A") {
    const href = n.getAttribute("href") ?? "";
    return SAFE_HREF.test(href) || /^[A-Z][A-Z0-9_]*-\d+$/.test(href) ? `[${inner}](${href})` : inner;
  }
  // Se il browser avvolge una riga in <div>/<p>, va a capo prima.
  if (tag === "DIV" || tag === "P") return (n.previousSibling ? "\n" : "") + inner;
  return inner;
}

/** Markdown in linea del contenuto di un elemento (o di un frammento). */
export function readInline(root: Node): string {
  return [...root.childNodes].map((c) => walk(c, new Set())).join("").replace(/\n+$/, "");
}

/** Numero di caratteri di testo dall'inizio dell'elemento al punto indicato. */
export function charsBefore(el: HTMLElement, node: Node, offset: number): number {
  const r = el.ownerDocument.createRange();
  r.selectNodeContents(el);
  r.setEnd(node, offset);
  return r.toString().length;
}

/** Mette il cursore dopo `n` caratteri di testo (oltre la fine: in fondo). */
export function setCaretChars(el: HTMLElement, n: number) {
  const doc = el.ownerDocument;
  const walker = doc.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  let left = n;
  let node = walker.nextNode();
  const r = doc.createRange();
  while (node) {
    const len = node.nodeValue?.length ?? 0;
    if (left <= len) {
      r.setStart(node, left);
      r.collapse(true);
      apply(doc, r);
      return;
    }
    left -= len;
    node = walker.nextNode();
  }
  r.selectNodeContents(el);
  r.collapse(false);
  apply(doc, r);
}

function apply(doc: Document, r: Range) {
  const s = doc.defaultView?.getSelection();
  s?.removeAllRanges();
  s?.addRange(r);
}
