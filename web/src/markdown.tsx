import type { ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import type { Mention } from "./api";

// Markdown leggero dei commenti, reso come elementi React: nessun HTML grezzo, quindi il
// testo è sempre sottoposto all'escape di React. Si riconoscono grassetto, corsivo,
// codice (in linea e a blocchi), link http/https/mailto, elenchi, citazioni, menzioni
// e chiavi degli elementi (DEM-12), che diventano link interni.

const SAFE_URL = /^(https?:\/\/|mailto:)/i;

function escapeRe(s: string) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function inline(text: string, mentions: Mention[], k: string): ReactNode[] {
  const names = [...mentions].sort((a, b) => b.name.length - a.name.length).map((m) => escapeRe(m.name));
  const parts = [
    names.length ? `(@(?:${names.join("|")}))(?![\\p{L}\\p{N}])` : "(\\u0000@never)",
    "(`[^`\\n]+`)",
    "(\\*\\*[^*\\n]+?\\*\\*|__[^_\\n]+?__)",
    "(\\*[^*\\s][^*\\n]*?\\*|(?<![\\p{L}\\p{N}])_[^_\\s][^_\\n]*?_(?![\\p{L}\\p{N}]))",
    "(\\[[^\\]\\n]+\\]\\([^)\\s]+\\))",
    "(https?:\\/\\/[^\\s<>()]+[^\\s<>().,;:!?'\"])",
    "(\\b[A-Z][A-Z0-9_]{1,19}-[1-9][0-9]{0,17}\\b)",
  ];
  const re = new RegExp(parts.join("|"), "giu");
  const out: ReactNode[] = [];
  let last = 0;
  let i = 0;
  for (const m of text.matchAll(re)) {
    const at = m.index ?? 0;
    if (at > last) out.push(text.slice(last, at));
    const key = `${k}-${i++}`;
    const [all, mention, code, bold, italic, link, url, elKey] = m;
    if (mention) {
      out.push(<span key={key} className="font-semibold text-accent">{mention}</span>);
    } else if (code) {
      out.push(<code key={key} className="bg-sel px-1 font-mono text-[12.5px]">{code.slice(1, -1)}</code>);
    } else if (bold) {
      out.push(<strong key={key}>{inline(bold.slice(2, -2), mentions, key)}</strong>);
    } else if (italic) {
      out.push(<em key={key}>{inline(italic.slice(1, -1), mentions, key)}</em>);
    } else if (link) {
      const sep = link.indexOf("](");
      const label = link.slice(1, sep);
      const href = link.slice(sep + 2, -1);
      out.push(
        SAFE_URL.test(href) ? (
          <a key={key} href={href} target="_blank" rel="noopener noreferrer nofollow" className="text-accent underline">
            {label}
          </a>
        ) : (
          all
        ),
      );
    } else if (url) {
      out.push(
        <a key={key} href={url} target="_blank" rel="noopener noreferrer nofollow" className="break-all text-accent underline">
          {url}
        </a>,
      );
    } else if (elKey && m[0] === elKey && /^[A-Z]/.test(elKey)) {
      out.push(
        <Link key={key} to="/e/$key" params={{ key: elKey }} className="font-mono text-[12.5px] text-accent">
          {elKey}
        </Link>,
      );
    } else {
      out.push(all);
    }
    last = at + all.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

function withBreaks(lines: string[], mentions: Mention[], k: string): ReactNode[] {
  const out: ReactNode[] = [];
  lines.forEach((l, i) => {
    if (i > 0) out.push(<br key={`${k}-br${i}`} />);
    out.push(...inline(l, mentions, `${k}-${i}`));
  });
  return out;
}

const LIST = /^\s*([-*+]|\d{1,3}[.)])\s+(.*)$/;

export function Markdown({ text, mentions = [] }: { text: string; mentions?: Mention[] }) {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const blocks: ReactNode[] = [];
  let i = 0;
  let b = 0;
  while (i < lines.length) {
    const line = lines[i];
    const k = `b${b++}`;
    if (/^```/.test(line)) {
      const code: string[] = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) code.push(lines[i++]);
      i++;
      blocks.push(
        <pre key={k} className="m-0 overflow-x-auto border-l-2 border-rule py-1 pl-3 font-mono text-[12.5px] leading-relaxed">
          <code>{code.join("\n")}</code>
        </pre>,
      );
      continue;
    }
    const lm = LIST.exec(line);
    if (lm) {
      const ordered = /\d/.test(lm[1]);
      const items: string[] = [];
      while (i < lines.length) {
        const m = LIST.exec(lines[i]);
        if (!m || /\d/.test(m[1]) !== ordered) break;
        items.push(m[2]);
        i++;
      }
      const cls = `m-0 flex flex-col gap-0.5 pl-5 ${ordered ? "list-decimal" : "list-disc"}`;
      const lis = items.map((it, j) => <li key={j}>{inline(it, mentions, `${k}-${j}`)}</li>);
      blocks.push(ordered ? <ol key={k} className={cls}>{lis}</ol> : <ul key={k} className={cls}>{lis}</ul>);
      continue;
    }
    if (/^>\s?/.test(line)) {
      const quote: string[] = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) quote.push(lines[i++].replace(/^>\s?/, ""));
      blocks.push(
        <blockquote key={k} className="m-0 border-l-2 border-hair pl-3 text-muted">
          {withBreaks(quote, mentions, k)}
        </blockquote>,
      );
      continue;
    }
    if (line.trim() === "") {
      i++;
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() !== "" && !/^```/.test(lines[i]) && !LIST.test(lines[i]) && !/^>\s?/.test(lines[i])) {
      para.push(lines[i++]);
    }
    blocks.push(
      <p key={k} className="m-0">
        {withBreaks(para, mentions, k)}
      </p>,
    );
  }
  return <div className="flex flex-col gap-2 leading-relaxed break-words">{blocks}</div>;
}
