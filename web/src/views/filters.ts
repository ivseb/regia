// Filtri visuali e query testuale sono la stessa cosa (piano, passo 16): ogni clausola
// "campo: valore, valore" della query è un chip, e ogni chip si scrive come clausola. Qui
// c'è solo la traduzione, senza interfaccia: la usano i chip, le viste e il test.
//
// Il resto della query (ordinamento, testo libero) non è un chip e resta com'è, nello stesso
// ordine: chi scrive a mano non perde nulla quando usa i chip.

export type Clause = { kind: "clause"; field: string; neg: boolean; values: string[] };
export type Part = Clause | { kind: "sort"; raw: string } | { kind: "text"; raw: string };

type Tok = { kind: "colon" | "comma" | "quoted" | "word"; text: string; from: number; to: number };

function lex(s: string): Tok[] {
  const out: Tok[] = [];
  let i = 0;
  while (i < s.length) {
    const c = s[i];
    if (/\s/.test(c)) i++;
    else if (c === ":" || c === ",") {
      out.push({ kind: c === ":" ? "colon" : "comma", text: c, from: i, to: i + 1 });
      i++;
    } else if (c === '"') {
      const j = s.indexOf('"', i + 1);
      const to = j < 0 ? s.length : j + 1;
      out.push({ kind: "quoted", text: s.slice(i, to), from: i, to });
      i = to;
    } else {
      let j = i;
      while (j < s.length && !/[\s:,"]/.test(s[j])) j++;
      out.push({ kind: "word", text: s.slice(i, j), from: i, to: j });
      i = j;
    }
  }
  return out;
}

const SORT = new Set(["ordina", "ordinamento", "sort", "order"]);
const BY = new Set(["per", "by"]);
const DIR = new Set(["asc", "desc", "crescente", "decrescente", "ascendente", "discendente", "ascending", "descending"]);
const OR = new Set(["oppure", "or"]);
const OP = /^(>=|<=|>|<|=)$/;

export function unquote(v: string): string {
  return v.length >= 2 && v.startsWith('"') && v.endsWith('"') ? v.slice(1, -1) : v;
}

/** Un valore con gli spazi o i simboli della sintassi va tra virgolette. */
export function quoteValue(v: string): string {
  if (v.startsWith('"')) return v;
  return /[\s:,"]|\.\./.test(v) && !/^(>=|<=|>|<|=)\S+$/.test(v) && !/^\S*\.\.\S*$/.test(v) ? `"${v.replace(/"/g, "")}"` : v;
}

const isName = (t?: Tok) => !!t && (t.kind === "word" || t.kind === "quoted");

/** Scompone la query in clausole (chip), ordinamento e testo libero. */
export function parseQuery(src: string): Part[] {
  const toks = lex(src);
  const parts: Part[] = [];
  let i = 0;
  // I pezzi di testo contigui restano un solo pezzo (con gli spazi originali).
  let run: Tok[] = [];
  const flush = () => {
    if (run.length) parts.push({ kind: "text", raw: src.slice(run[0].from, run[run.length - 1].to) });
    run = [];
  };
  while (i < toks.length) {
    const t = toks[i];
    const lw = t.text.toLowerCase();
    // Ordinamento: "ordina per: campo [asc|desc]".
    if (t.kind === "word" && SORT.has(lw)) {
      let j = i + 1;
      if (toks[j]?.kind === "word" && BY.has(toks[j].text.toLowerCase())) j++;
      if (toks[j]?.kind === "colon") {
        j++;
        if (isName(toks[j])) {
          let end = toks[j].to;
          j++;
          if (toks[j]?.kind === "word" && DIR.has(toks[j].text.toLowerCase())) {
            end = toks[j].to;
            j++;
          }
          flush();
          parts.push({ kind: "sort", raw: src.slice(t.from, end) });
          i = j;
          continue;
        }
      }
    }
    // Clausola: [-]campo ":" valori.
    if (isName(t) && toks[i + 1]?.kind === "colon") {
      flush();
      let field = t.kind === "quoted" ? unquote(t.text) : t.text;
      let neg = false;
      if (t.kind === "word" && field.startsWith("-") && field.length > 1) {
        neg = true;
        field = field.slice(1);
      }
      i += 2;
      const values: string[] = [];
      let expect = true;
      while (i < toks.length) {
        const v = toks[i];
        if (v.kind === "comma") {
          expect = true;
          i++;
          continue;
        }
        if (v.kind === "word" && OR.has(v.text.toLowerCase()) && !expect) {
          // "oppure" tra valori; se segue una nuova clausola è un'alternativa sul campo ripetuto.
          i++;
          expect = true;
          continue;
        }
        if (!expect || !isName(v)) break;
        if (toks[i + 1]?.kind === "colon") break; // inizia la clausola seguente
        if (v.kind === "word" && OP.test(v.text) && isName(toks[i + 1]) && toks[i + 2]?.kind !== "colon") {
          values.push(src.slice(v.from, toks[i + 1].to));
          i += 2;
        } else {
          values.push(src.slice(v.from, v.to));
          i++;
        }
        expect = false;
      }
      parts.push({ kind: "clause", field, neg, values });
      continue;
    }
    run.push(t);
    i++;
  }
  flush();
  // Un "oppure" rimasto in fondo (campo ripetuto) non è testo da cercare.
  const out: Part[] = [];
  for (const p of parts) {
    if (p.kind === "text") {
      const raw = p.raw.replace(/\s+(oppure|or)\s*$/i, "").trim();
      if (!raw || /^(oppure|or)$/i.test(raw)) continue;
      out.push({ kind: "text", raw });
    } else if (p.kind === "clause") {
      // Campo ripetuto = valori in alternativa: un solo chip.
      const same = out.find((o): o is Clause => o.kind === "clause" && o.field.toLowerCase() === p.field.toLowerCase() && o.neg === p.neg);
      if (same) same.values.push(...p.values.filter((v) => !same.values.includes(v)));
      else out.push({ ...p, values: [...p.values] });
    } else out.push(p);
  }
  return out;
}

export function clauseText(c: Clause): string {
  const f = /[\s:,"]/.test(c.field) ? `"${c.field}"` : c.field;
  return `${c.neg ? "-" : ""}${f}: ${c.values.join(", ")}`;
}

export function formatQuery(parts: Part[]): string {
  return parts
    .map((p) => (p.kind === "clause" ? (p.values.length ? clauseText(p) : "") : p.raw))
    .filter(Boolean)
    .join(" ");
}

/** Le clausole della query, nell'ordine in cui compaiono. */
export function clausesOf(q: string): Clause[] {
  return parseQuery(q).filter((p): p is Clause => p.kind === "clause");
}

/** Imposta (o aggiunge) la clausola di un campo; senza valori la toglie. */
export function setClause(q: string, field: string, values: string[], neg = false): string {
  const parts = parseQuery(q);
  const i = parts.findIndex((p) => p.kind === "clause" && p.field.toLowerCase() === field.toLowerCase());
  const next: Clause = { kind: "clause", field, neg, values: values.map(quoteValue) };
  if (i >= 0) {
    if (values.length === 0) parts.splice(i, 1);
    else parts[i] = next;
  } else if (values.length) {
    // Prima dell'ordinamento, così resta in fondo come si scrive di solito.
    const s = parts.findIndex((p) => p.kind === "sort");
    parts.splice(s < 0 ? parts.length : s, 0, next);
  }
  return formatQuery(parts);
}

/** Chip → query: sostituisce la clausola in posizione `index` tra le clausole. */
export function replaceClause(q: string, index: number, next: Clause | null): string {
  const parts = parseQuery(q);
  let n = -1;
  for (let k = 0; k < parts.length; k++) {
    if (parts[k].kind !== "clause") continue;
    if (++n !== index) continue;
    if (next && next.values.length) parts[k] = next;
    else parts.splice(k, 1);
    break;
  }
  return formatQuery(parts);
}

export function addClause(q: string, c: Clause): string {
  const parts = parseQuery(q);
  const s = parts.findIndex((p) => p.kind === "sort");
  parts.splice(s < 0 ? parts.length : s, 0, c);
  return formatQuery(parts);
}

/** Etichetta leggibile di un valore: senza virgolette. */
export const showValue = unquote;
