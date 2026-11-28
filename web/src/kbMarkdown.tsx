import { Fragment, useMemo, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import type { Mention } from "./api";
import type { RefInfo } from "./knowledgeApi";
import { tr } from "./i18n/core";

// Markdown esteso delle pagine (piano, passo 19), reso come elementi React: nessun HTML grezzo,
// tutto il testo passa dall'escape di React. Oltre al Markdown dei commenti (markdown.tsx):
// titoli con ancore per l'indice, elenchi annidati e di attività, tabelle con allineamento,
// codice con linguaggio, citazioni e avvisi ("> [!NOTA]", "> [!ATTENZIONE]"…), righe di
// separazione, immagini degli allegati, chiavi degli elementi con lo stato (DEM-12 · Approvata)
// e menzioni. Le immagini si mostrano solo se sono allegati di Regia (/api/v1/attachments/…).

const SAFE_URL = /^(https?:\/\/|mailto:)/i;
const ATTACHMENT_URL = /^\/api\/v1\/attachments\/[0-9a-f-]{36}\/content(\?download=1)?$/i;
const KEY = /^[A-Z][A-Z0-9_]{1,19}-[1-9][0-9]{0,17}$/;

export type Heading = { level: number; text: string; id: string };

/** Avvisi: parola chiave (italiano o inglese) → etichetta e colore del bordo. */
const CALLOUTS: Record<string, { label: "note" | "tip" | "important" | "warning" | "caution"; cls: string; text: string }> = {
  nota: { label: "note", cls: "border-accent", text: "text-accent" },
  note: { label: "note", cls: "border-accent", text: "text-accent" },
  info: { label: "note", cls: "border-accent", text: "text-accent" },
  suggerimento: { label: "tip", cls: "border-ok", text: "text-ok" },
  tip: { label: "tip", cls: "border-ok", text: "text-ok" },
  importante: { label: "important", cls: "border-fg", text: "text-fg" },
  important: { label: "important", cls: "border-fg", text: "text-fg" },
  attenzione: { label: "warning", cls: "border-flag", text: "text-flag" },
  warning: { label: "warning", cls: "border-flag", text: "text-flag" },
  pericolo: { label: "caution", cls: "border-danger", text: "text-danger" },
  caution: { label: "caution", cls: "border-danger", text: "text-danger" },
};
export const CALLOUT_KINDS = ["NOTA", "SUGGERIMENTO", "IMPORTANTE", "ATTENZIONE", "PERICOLO"];

function escapeRe(s: string) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** Identificativo di un titolo per le ancore dell'indice (stesso calcolo per indice e testo). */
export function slug(text: string): string {
  const s = text
    .toLowerCase()
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/[^\p{L}\p{N}]+/gu, "-")
    .replace(/^-+|-+$/g, "");
  return s || "sezione";
}

const HEADING = /^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$/;
const FENCE = /^\s*(```|~~~)\s*([\w+#.-]*)\s*$/;
const LIST = /^(\s*)([-*+]|\d{1,4}[.)])\s+(.*)$/;
const HR = /^\s{0,3}([-*_])(\s*\1){2,}\s*$/;
const TABLE_SEP = /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/;
const CALLOUT = /^\[!([A-Za-zÀ-ÿ]+)\]\s*(.*)$/;

/** Titoli del testo (fuori dai blocchi di codice), con ancore uniche. */
export function headingsOf(md: string): Heading[] {
  const out: Heading[] = [];
  const seen = new Map<string, number>();
  let inCode = false;
  for (const line of md.split("\n")) {
    if (FENCE.test(line)) {
      inCode = !inCode;
      continue;
    }
    if (inCode) continue;
    const m = HEADING.exec(line);
    if (m) {
      const text = m[2].replace(/[*_`~]/g, "");
      const base = slug(text);
      const n = seen.get(base) ?? 0;
      seen.set(base, n + 1);
      out.push({ level: m[1].length, text, id: n ? `${base}-${n + 1}` : base });
    }
  }
  return out;
}

type Ctx = { mentions: Mention[]; refs: Map<string, RefInfo>; mentionRe: RegExp | null; codeRe: RegExp | null; compact?: boolean };

function inline(text: string, ctx: Ctx, k: string): ReactNode[] {
  const parts = [
    ctx.mentionRe ? ctx.mentionRe.source : "(\\u0000@never)",
    "(`[^`\\n]+`)",
    "(!\\[[^\\]\\n]*\\]\\([^)\\s]+\\))",
    "(\\*\\*[^*\\n]+?\\*\\*|__[^_\\n]+?__)",
    "(~~[^~\\n]+?~~)",
    "(\\*[^*\\s][^*\\n]*?\\*|(?<![\\p{L}\\p{N}])_[^_\\s][^_\\n]*?_(?![\\p{L}\\p{N}]))",
    "(\\[[^\\]\\n]+\\]\\([^)\\s]+\\))",
    "(https?:\\/\\/[^\\s<>()]+[^\\s<>().,;:!?'\"])",
    "(\\b[A-Z][A-Z0-9_]{1,19}-[1-9][0-9]{0,17}\\b)",
    ctx.codeRe ? ctx.codeRe.source : "(\\u0000#never)",
  ];
  const re = new RegExp(parts.join("|"), "gu");
  const out: ReactNode[] = [];
  let last = 0;
  let i = 0;
  for (const m of text.matchAll(re)) {
    const at = m.index ?? 0;
    if (at > last) out.push(text.slice(last, at));
    const key = `${k}-${i++}`;
    const [all, mention, code, image, bold, strike, italic, link, url, elKey, extraCode] = m;
    if (mention) {
      out.push(<span key={key} className="font-semibold text-accent">{mention}</span>);
    } else if (code) {
      out.push(<code key={key} className="bg-sel px-1 font-mono text-[0.92em]">{code.slice(1, -1)}</code>);
    } else if (image) {
      const sep = image.indexOf("](");
      const alt = image.slice(2, sep);
      const src = image.slice(sep + 2, -1);
      out.push(
        ATTACHMENT_URL.test(src) ? (
          <a key={key} href={src.replace(/\?download=1$/, "")} target="_blank" rel="noopener" className="inline-block max-w-full">
            <img src={src.replace(/\?download=1$/, "")} alt={alt} loading="lazy" decoding="async" className="block h-auto max-w-full border border-hair" />
          </a>
        ) : (
          <span key={key} className="text-muted">{tr("knowledge.md.externalImage", { name: alt || src })}</span>
        ),
      );
    } else if (bold) {
      out.push(<strong key={key}>{inline(bold.slice(2, -2), ctx, key)}</strong>);
    } else if (strike) {
      out.push(<s key={key}>{inline(strike.slice(2, -2), ctx, key)}</s>);
    } else if (italic) {
      out.push(<em key={key}>{inline(italic.slice(1, -1), ctx, key)}</em>);
    } else if (link) {
      const sep = link.indexOf("](");
      const label = link.slice(1, sep);
      const href = link.slice(sep + 2, -1);
      if (KEY.test(href)) {
        out.push(
          <Link key={key} to="/e/$key" params={{ key: href }} className="text-accent underline">
            {label}
          </Link>,
        );
      } else if (ATTACHMENT_URL.test(href)) {
        out.push(<a key={key} href={href} className="text-accent underline">{label}</a>);
      } else if (href.startsWith("#")) {
        out.push(<a key={key} href={href} className="text-accent underline">{label}</a>);
      } else if (SAFE_URL.test(href)) {
        out.push(
          <a key={key} href={href} target="_blank" rel="noopener noreferrer nofollow" className="text-accent underline">
            {label}
          </a>,
        );
      } else out.push(all);
    } else if (url) {
      out.push(
        <a key={key} href={url} target="_blank" rel="noopener noreferrer nofollow" className="break-all text-accent underline">
          {url}
        </a>,
      );
    } else if (elKey) {
      out.push(<ElementRef key={key} k={elKey} info={ctx.refs.get(elKey)} />);
    } else if (extraCode) {
      out.push(<ElementRef key={key} k={extraCode} info={ctx.refs.get(extraCode)} />);
    } else out.push(all);
    last = at + all.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

/** Chiave di un elemento: link con lo stato, barrata se chiuso. */
function ElementRef({ k, info }: { k: string; info?: RefInfo }) {
  const closed = info?.state_category === "done" || info?.state_category === "cancelled";
  return (
    <Link to="/e/$key" params={{ key: k }} title={info ? (info.type_name ? `${info.type_name}: ${info.title}` : info.title) : undefined} className="whitespace-nowrap no-underline">
      <span className={`font-mono text-[0.92em] ${closed ? "text-muted line-through" : "text-accent"}`}>{k}</span>
      {info?.state && <span className="text-[0.85em] text-muted"> · {info.state}</span>}
    </Link>
  );
}

function withBreaks(lines: string[], ctx: Ctx, k: string): ReactNode[] {
  const out: ReactNode[] = [];
  lines.forEach((l, i) => {
    if (i > 0) out.push(<br key={`${k}-br${i}`} />);
    out.push(...inline(l.replace(/\s+$/, ""), ctx, `${k}-${i}`));
  });
  return out;
}

function splitRow(line: string): string[] {
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

type ListItem = { text: string[]; indent: number; ordered: boolean; children: ListItem[] };

function renderList(items: ListItem[], ctx: Ctx, k: string): ReactNode {
  const ordered = items[0]?.ordered;
  const cls = `m-0 flex flex-col gap-0.5 pl-5 ${ordered ? "list-decimal" : "list-disc"}`;
  const lis = items.map((it, j) => {
    const task = /^\[( |x|X)\]\s+/.exec(it.text[0] ?? "");
    const first = task ? [it.text[0].slice(task[0].length), ...it.text.slice(1)] : it.text;
    return (
      <li key={j} className={task ? "list-none -ml-5 flex gap-2" : undefined}>
        {task && <input type="checkbox" checked={task[1] !== " "} readOnly disabled aria-label={task[1] !== " " ? tr("knowledge.md.done") : tr("knowledge.md.todo")} className="mt-1" />}
        <span className="min-w-0">
          {withBreaks(first, ctx, `${k}-${j}`)}
          {it.children.length > 0 && renderList(it.children, ctx, `${k}-${j}c`)}
        </span>
      </li>
    );
  });
  return ordered ? <ol key={k} className={cls}>{lis}</ol> : <ul key={k} className={cls}>{lis}</ul>;
}

function parseBlocks(lines: string[], ctx: Ctx, prefix: string, ids: Map<string, number>): ReactNode[] {
  const blocks: ReactNode[] = [];
  let i = 0;
  let b = 0;
  while (i < lines.length) {
    const line = lines[i];
    const k = `${prefix}${b++}`;
    const fence = FENCE.exec(line);
    if (fence) {
      const lang = fence[2];
      const code: string[] = [];
      i++;
      while (i < lines.length && !FENCE.test(lines[i])) code.push(lines[i++]);
      i++;
      blocks.push(
        <figure key={k} className="m-0 flex flex-col border-l-2 border-rule">
          {lang && <figcaption className="px-3 pt-1 font-mono text-[11px] text-muted">{lang}</figcaption>}
          <pre className="m-0 overflow-x-auto px-3 py-1.5 font-mono text-[12.5px] leading-relaxed">
            <code>{code.join("\n")}</code>
          </pre>
        </figure>,
      );
      continue;
    }
    const h = HEADING.exec(line);
    if (h) {
      const level = Math.min(h[1].length, 4);
      const text = h[2];
      const base = slug(text.replace(/[*_`~]/g, ""));
      const n = ids.get(base) ?? 0;
      ids.set(base, n + 1);
      const id = n ? `${base}-${n + 1}` : base;
      const cls = ctx.compact
        ? "text-[14.5px] font-bold mt-1"
        : {
        1: "text-[26px] leading-[1.15] font-bold tracking-[-0.02em] mt-2",
        2: "text-[19px] leading-tight font-bold mt-3 border-b border-hair pb-1",
        3: "text-[15.5px] font-bold mt-2",
        4: "text-[13.5px] font-bold mt-1",
      }[level];
      const Tag = `h${level + 1 > 6 ? 6 : level === 1 ? 2 : level + 1}` as "h2" | "h3" | "h4" | "h5";
      blocks.push(
        <Tag key={k} id={ctx.compact ? undefined : id} className={`m-0 scroll-mt-20 ${cls}`}>
          {inline(text, ctx, k)}
        </Tag>,
      );
      i++;
      continue;
    }
    if (HR.test(line) && !LIST.test(line)) {
      blocks.push(<hr key={k} className="m-0 border-0 border-t border-hair" />);
      i++;
      continue;
    }
    // Tabella: riga d'intestazione con | seguita dalla riga di allineamento.
    if (line.includes("|") && i + 1 < lines.length && TABLE_SEP.test(lines[i + 1]) && lines[i + 1].includes("-")) {
      const head = splitRow(line);
      const align = splitRow(lines[i + 1]).map((c) => (c.startsWith(":") && c.endsWith(":") ? "center" : c.endsWith(":") ? "right" : "left"));
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && lines[i].includes("|") && lines[i].trim() !== "") rows.push(splitRow(lines[i++]));
      const al = (j: number) => (align[j] === "right" ? "text-right" : align[j] === "center" ? "text-center" : "text-left");
      const emptyHead = head.every((c) => c === "");
      blocks.push(
        <div key={k} className="max-w-full overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            {!emptyHead && (
              <thead>
                <tr>
                  {head.map((c, j) => (
                    <th key={j} scope="col" className={`border-b-2 border-rule px-2 py-1.5 align-bottom font-semibold ${al(j)}`}>
                      {inline(c, ctx, `${k}-h${j}`)}
                    </th>
                  ))}
                </tr>
              </thead>
            )}
            <tbody>
              {rows.map((r, ri) => (
                <tr key={ri}>
                  {head.map((_, j) => (
                    <td key={j} className={`border-b border-hair px-2 py-1.5 align-top ${al(j)} ${emptyHead && j === 0 ? "text-muted" : ""}`}>
                      {inline(r[j] ?? "", ctx, `${k}-${ri}-${j}`)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>,
      );
      continue;
    }
    const lm = LIST.exec(line);
    if (lm) {
      // Elenco (anche annidato per rientro): righe di continuazione rientrate restano nella voce.
      const root: ListItem[] = [];
      const stack: ListItem[] = [];
      while (i < lines.length) {
        const m = LIST.exec(lines[i]);
        if (m) {
          const item: ListItem = { text: [m[3]], indent: m[1].replace(/\t/g, "    ").length, ordered: /\d/.test(m[2]), children: [] };
          while (stack.length && stack[stack.length - 1].indent >= item.indent) stack.pop();
          if (stack.length) stack[stack.length - 1].children.push(item);
          else if (root.length && root[0].ordered !== item.ordered) break;
          else root.push(item);
          stack.push(item);
          i++;
        } else if (lines[i].trim() !== "" && /^\s{2,}/.test(lines[i]) && stack.length) {
          stack[stack.length - 1].text.push(lines[i].trim());
          i++;
        } else break;
      }
      blocks.push(renderList(root, ctx, k));
      continue;
    }
    if (/^\s*>/.test(line)) {
      const quote: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) quote.push(lines[i++].replace(/^\s*>\s?/, ""));
      const c = CALLOUT.exec(quote[0] ?? "");
      const kind = c ? CALLOUTS[c[1].toLowerCase()] : undefined;
      if (kind) {
        const rest = [c![2], ...quote.slice(1)].filter((l, j) => j > 0 || l.trim() !== "");
        blocks.push(
          <aside key={k} role="note" className={`flex flex-col gap-1 border-l-2 py-0.5 pl-3 ${kind.cls}`}>
            <span className={`label ${kind.text}`}>{tr(`knowledge.md.callout.${kind.label}`)}</span>
            <div className="flex flex-col gap-2">{parseBlocks(rest, ctx, `${k}q`, ids)}</div>
          </aside>,
        );
      } else {
        blocks.push(
          <blockquote key={k} className="m-0 flex flex-col gap-2 border-l-2 border-hair pl-3 text-muted">
            {parseBlocks(quote, ctx, `${k}q`, ids)}
          </blockquote>,
        );
      }
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
    if (para.length === 0) {
      para.push(lines[i++]);
    }
    blocks.push(
      <p key={k} className="m-0">
        {withBreaks(para, ctx, k)}
      </p>,
    );
  }
  return blocks;
}

/** Pagina in Markdown esteso. refs: stato degli elementi citati (facoltativo). codes: codici
 * da rendere come link oltre alle chiavi (APP-MOB). compact: testo di un pannello (risposte
 * dell'AI): titoli più piccoli e spazi più stretti. */
export function PageMarkdown({ text, mentions = [], refs, codes, compact }: { text: string; mentions?: Mention[]; refs?: RefInfo[]; codes?: string[]; compact?: boolean }) {
  const content = useMemo(() => {
    const names = [...mentions].sort((a, b) => b.name.length - a.name.length).map((m) => escapeRe(m.name));
    const extra = [...new Set(codes ?? [])].filter((c) => c && !KEY.test(c)).sort((a, b) => b.length - a.length).map(escapeRe);
    const ctx: Ctx = {
      mentions,
      refs: new Map((refs ?? []).map((r) => [r.key, r])),
      mentionRe: names.length ? new RegExp(`(@(?:${names.join("|")}))(?![\\p{L}\\p{N}])`, "u") : null,
      codeRe: extra.length ? new RegExp(`((?<![\\p{L}\\p{N}_-])(?:${extra.join("|")})(?![\\p{L}\\p{N}_-]))`, "u") : null,
      compact,
    };
    return parseBlocks(text.replace(/\r\n/g, "\n").split("\n"), ctx, "b", new Map());
  }, [text, mentions, refs, codes, compact]);
  const cls = compact ? "gap-2 text-[14px]" : "gap-3 text-[14.5px]";
  return <div className={`kb-page flex min-w-0 flex-col leading-relaxed break-words ${cls}`}>{content.map((c, i) => <Fragment key={i}>{c}</Fragment>)}</div>;
}

/** Chiavi degli elementi citate nel testo (fuori dai blocchi di codice). */
export function citedKeys(md: string): string[] {
  const out = new Set<string>();
  let inCode = false;
  for (const line of md.split("\n")) {
    if (FENCE.test(line)) {
      inCode = !inCode;
      continue;
    }
    if (inCode) continue;
    for (const m of line.matchAll(/\b[A-Z][A-Z0-9_]{1,19}-[1-9][0-9]{0,17}\b/g)) {
      if (out.size < 200) out.add(m[0]);
    }
  }
  return [...out];
}
