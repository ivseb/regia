// Ancore dei commenti in linea sul testo reso. Il testo della pagina è la concatenazione dei
// nodi di testo con gli spazi ridotti a uno e uno spazio tra un blocco e l'altro: la stessa
// forma del testo di ancoraggio del server (internal/modules/knowledge/anchor.go). Una frase è
// ritrovata con il suo contesto (prima e dopo) e il numero d'ordine tra le occorrenze uguali.

const BLOCK = "p,li,td,th,h1,h2,h3,h4,h5,h6,blockquote,pre,div,tr,dd,dt,summary";
export const CONTEXT = 40;

type NodeInfo = { node: Text; first: number; end: number; norm: number[] };

export type TextIndex = {
  text: string;
  nodes: NodeInfo[];
  /** posizione (nel testo normalizzato) di un punto del DOM */
  pointToIndex(node: Node, offset: number): number | null;
  /** intervallo del DOM per [start, end) del testo normalizzato */
  range(start: number, end: number): Range | null;
};

/** Costruisce l'indice del testo di un contenitore (nodi di testo visibili). */
export function buildIndex(root: HTMLElement): TextIndex {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: (n) => (n.parentElement?.closest("script,style,[data-no-anchor]") ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT),
  });
  const parts: string[] = [];
  const producers: Array<[Text, number] | undefined> = [];
  const nodes: NodeInfo[] = [];
  let len = 0;
  let lastBlock: Element | null = null;
  let endsSpace = true;
  for (let n = walker.nextNode() as Text | null; n; n = walker.nextNode() as Text | null) {
    const data = n.data;
    const block = n.parentElement?.closest(BLOCK) ?? null;
    if (data.trim() !== "") {
      if (block !== lastBlock && !endsSpace) {
        parts.push(" ");
        producers.push(undefined);
        len += 1;
        endsSpace = true;
      }
      lastBlock = block;
    }
    const norm: number[] = new Array(data.length);
    const first = len;
    for (let i = 0; i < data.length; i++) {
      norm[i] = len;
      if (/\s/.test(data[i])) {
        if (!endsSpace) {
          parts.push(" ");
          producers.push([n, i]);
          len += 1;
          endsSpace = true;
        }
      } else {
        parts.push(data[i]);
        producers.push([n, i]);
        len += 1;
        endsSpace = false;
      }
    }
    nodes.push({ node: n, first, end: len, norm });
  }
  const text = parts.join("");
  const byNode = new Map(nodes.map((i) => [i.node, i]));
  return {
    text,
    nodes,
    pointToIndex(node, offset) {
      if (node.nodeType === Node.TEXT_NODE) {
        const info = byNode.get(node as Text);
        if (!info) return null;
        return offset >= info.norm.length ? info.end : info.norm[offset];
      }
      // Punto tra elementi: il primo nodo di testo da lì in avanti.
      const child = node.childNodes[offset] ?? null;
      for (const info of nodes) {
        if (child) {
          if (child === info.node || child.contains(info.node) || child.compareDocumentPosition(info.node) & Node.DOCUMENT_POSITION_FOLLOWING) return info.first;
        } else {
          const rel = node.compareDocumentPosition(info.node);
          if (!(rel & Node.DOCUMENT_POSITION_CONTAINED_BY) && rel & Node.DOCUMENT_POSITION_FOLLOWING) return info.first;
        }
      }
      return text.length;
    },
    range(start, end) {
      let s = start;
      let e = Math.min(end, text.length) - 1;
      while (s <= e && !producers[s]) s++;
      while (e >= s && !producers[e]) e--;
      if (e < s) return null;
      const a = producers[s]!;
      const b = producers[e]!;
      const r = document.createRange();
      try {
        r.setStart(a[0], a[1]);
        r.setEnd(b[0], b[1] + 1);
      } catch {
        return null;
      }
      return r;
    },
  };
}

function commonSuffix(a: string, b: string): number {
  let n = 0;
  while (n < a.length && n < b.length && a[a.length - 1 - n] === b[b.length - 1 - n]) n++;
  return n;
}
function commonPrefix(a: string, b: string): number {
  let n = 0;
  while (n < a.length && n < b.length && a[n] === b[n]) n++;
  return n;
}

export type Anchor = { quote: string; prefix: string; suffix: string; pos: number | null; nth: number };

/** Ritrova la frase nel testo: vince l'occorrenza con più contesto in comune. */
export function locate(text: string, a: Anchor): { start: number; end: number; nth: number } | null {
  const quote = a.quote;
  if (!quote) return null;
  const starts: number[] = [];
  for (let from = 0; ; ) {
    const i = text.indexOf(quote, from);
    if (i < 0 || starts.length >= 5000) break;
    starts.push(i);
    from = i + 1;
  }
  if (starts.length === 0) return null;
  let best = 0;
  let bestScore = -1;
  let bestDist = 0;
  starts.forEach((s, n) => {
    const score =
      commonSuffix(text.slice(Math.max(0, s - CONTEXT * 2), s), a.prefix) + commonPrefix(text.slice(s + quote.length, s + quote.length + CONTEXT * 2), a.suffix);
    const dist = a.pos != null ? Math.abs(s - a.pos) : 0;
    const better = score > bestScore || (score === bestScore && a.pos != null && dist < bestDist) || (score === bestScore && a.pos == null && n === a.nth);
    if (bestScore < 0 || better) {
      best = n;
      bestScore = score;
      bestDist = dist;
    }
  });
  return { start: starts[best], end: starts[best] + quote.length, nth: best };
}

/** La selezione dentro il contenitore diventa una frase con il suo contesto. */
export function anchorFromSelection(idx: TextIndex, range: Range): { quote: string; prefix: string; suffix: string; nth: number } | null {
  const s = idx.pointToIndex(range.startContainer, range.startOffset);
  const e = idx.pointToIndex(range.endContainer, range.endOffset);
  if (s == null || e == null) return null;
  let start = Math.min(s, e);
  let end = Math.max(s, e);
  while (start < end && idx.text[start] === " ") start++;
  while (end > start && idx.text[end - 1] === " ") end--;
  if (end - start < 2) return null;
  const quote = idx.text.slice(start, end);
  const prefix = idx.text.slice(Math.max(0, start - CONTEXT), start);
  const suffix = idx.text.slice(end, end + CONTEXT);
  // Quale occorrenza è: quante identiche la precedono.
  let nth = 0;
  for (let from = 0; ; ) {
    const i = idx.text.indexOf(quote, from);
    if (i < 0 || i >= start) break;
    nth++;
    from = i + 1;
  }
  return { quote: quote.length > 1000 ? quote.slice(0, 1000) : quote, prefix, suffix, nth };
}
