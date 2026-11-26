import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ClipboardEvent, type DragEvent, type KeyboardEvent, type RefObject } from "react";
import { createPortal } from "react-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { RefInfo } from "../knowledgeApi";
import {
  CALLOUT_LABEL, CHOICES, convertBlock, duplicateBlock, emptyDoc, filterChoices, fromChoice, insertBlocks, isList, isRich, moveBlock, newBlock,
  parseBlocks, reflow, removeBlock, renumber, shortcut, toMarkdown, touched, type Block, type Choice, type Doc,
} from "../kbBlocks";
import { charsBefore, cleanHref, fillInline, readInline, setCaretChars } from "../kbInline";
import { useNarrow } from "./timeline";
import { useT } from "../i18n/react";
import type { Key } from "../i18n/core";

// Editor a blocchi delle pagine (ondata 3). Il formato salvato resta Markdown (src/kbBlocks.ts):
// qui ci sono solo la modifica e l'interfaccia. Modello d'uso: blocchi con «/» per inserire,
// maniglia per spostare, barra sulla selezione, Invio e Backspace; scritto da zero, senza
// librerie (vedi docs/sviluppo/knowledge-blocchi.md).

export type Uploaded = { image: boolean; name: string; url: string };
export type Member = { id: string; name: string };
type Props = {
  value: string;
  onChange: (md: string) => void;
  members: Member[];
  refs?: RefInfo[];
  upload: (f: File) => Promise<Uploaded | null>;
  onSave: () => void;
};
type Focus = { id: string; at: "start" | "end" | number };
type Slash = { id: string; q: string; idx: number };
type Mention = { id: string; q: string; node: Text; end: number; idx: number };

const CALLOUT_CLS: Record<string, { border: string; text: string }> = {
  NOTA: { border: "border-accent", text: "text-accent" },
  SUGGERIMENTO: { border: "border-ok", text: "text-ok" },
  IMPORTANTE: { border: "border-fg", text: "text-fg" },
  ATTENZIONE: { border: "border-flag", text: "text-flag" },
  PERICOLO: { border: "border-danger", text: "text-danger" },
};
const HEADING_CLS: Record<number, string> = {
  1: "text-[24px] leading-[1.15] font-bold tracking-[-0.02em]",
  2: "text-[19px] leading-tight font-bold",
  3: "text-[15.5px] font-bold",
};
const TEXT_KINDS = CHOICES.filter((c) => ["paragraph", "heading", "bullet", "number", "task", "quote", "callout", "code"].includes(c.kind));

const sameChoice = (b: Block, c: Choice) => b.kind === c.kind && (c.kind !== "heading" || b.level === c.level) && (c.kind !== "callout" || b.callout === c.callout);
/** Dopo un blocco che non si può continuare a scrivere (codice, tabella, citazione…) c'è sempre un paragrafo vuoto. */
const withTail = (blocks: Block[]) => {
  const last = blocks[blocks.length - 1];
  return last && (last.kind === "paragraph" || last.kind === "heading" || isList(last.kind)) ? blocks : [...blocks, newBlock("paragraph")];
};

function sel(): Selection | null {
  return window.getSelection();
}
function caretIn(el: HTMLElement): { node: Node; offset: number; collapsed: boolean } | null {
  const s = sel();
  if (!s || s.rangeCount === 0 || !el.contains(s.anchorNode)) return null;
  return { node: s.anchorNode!, offset: s.anchorOffset, collapsed: s.isCollapsed };
}
function atEdge(el: HTMLElement, edge: "start" | "end"): boolean {
  const c = caretIn(el);
  if (!c || !c.collapsed) return false;
  const n = charsBefore(el, c.node, c.offset);
  return edge === "start" ? n === 0 : n >= (el.textContent?.length ?? 0);
}
/** Il cursore è sulla prima (o ultima) riga visiva del testo. */
function onEdgeLine(el: HTMLElement, edge: "first" | "last"): boolean {
  const s = sel();
  if (!s || s.rangeCount === 0 || !s.isCollapsed) return false;
  const r = s.getRangeAt(0).cloneRange();
  const rects = r.getClientRects();
  const caret = rects.length ? rects[0] : (r.startContainer instanceof Element ? r.startContainer : r.startContainer.parentElement)?.getBoundingClientRect();
  if (!caret) return true;
  const box = el.getBoundingClientRect();
  const lh = parseFloat(getComputedStyle(el).lineHeight) || 20;
  return edge === "first" ? caret.top - box.top < lh : box.bottom - caret.bottom < lh;
}

export function BlockEditor({ value, onChange, members, refs, upload, onSave }: Props) {
  const t = useT();
  const narrow = useNarrow();
  const label = (id: string) => t(`knowledge.blocks.type.${id}` as Key);
  const root = useRef<HTMLDivElement>(null);
  const [doc, setDoc] = useState<Doc>(() => {
    const d = parseBlocks(value);
    return d.blocks.length ? d : { ...emptyDoc(), tail: d.tail, crlf: d.crlf };
  });
  const docRef = useRef(doc);
  const focusReq = useRef<Focus | null>(null);
  const [slash, setSlash] = useState<Slash | null>(null);
  const [mention, setMention] = useState<Mention | null>(null);
  const [menuFor, setMenuFor] = useState<string | null>(null);
  const [pickRef, setPickRef] = useState<string | null>(null);
  const [drop, setDrop] = useState<{ id: string; after: boolean } | null>(null);
  const pickFile = useRef<{ id: string } | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const active = useRef<string>("");
  const slashRef = useRef(slash);
  slashRef.current = slash;
  const mentionRef = useRef(mention);
  mentionRef.current = mention;
  const propsRef = useRef({ onChange, onSave, upload, members, label, t });
  propsRef.current = { onChange, onSave, upload, members, label, t };

  const commit = useCallback((blocks: Block[], focus?: Focus) => {
    const next = { ...docRef.current, blocks: withTail(blocks) };
    docRef.current = next;
    setDoc(next);
    if (focus) focusReq.current = focus;
    propsRef.current.onChange(toMarkdown(next));
  }, []);
  const blocksNow = () => docRef.current.blocks;
  const indexOf = (id: string) => blocksNow().findIndex((b) => b.id === id);
  const patch = useCallback((id: string, over: Partial<Block>, focus?: Focus) => commit(blocksNow().map((b) => (b.id === id ? touched(b, over) : b)), focus), [commit]);

  useLayoutEffect(() => {
    const f = focusReq.current;
    if (!f) return;
    focusReq.current = null;
    const el = root.current?.querySelector<HTMLElement>(`[data-bfocus="${f.id}"]`);
    if (!el) return;
    el.focus({ preventScroll: false });
    if (el.isContentEditable) setCaretChars(el, f.at === "start" ? 0 : f.at === "end" ? 1e9 : f.at);
  });

  // Numeri visibili degli elenchi numerati.
  const numbers = useMemo(() => {
    const out: number[] = [];
    const count: number[] = [];
    for (const b of doc.blocks) {
      if (b.kind === "number") {
        count.length = b.level + 1;
        count[b.level] = (count[b.level] ?? 0) + 1;
        out.push(count[b.level]);
      } else {
        if (!isList(b.kind)) count.length = 0;
        else count.length = Math.min(count.length, b.level + 1);
        out.push(0);
      }
    }
    return out;
  }, [doc.blocks]);

  // ---- Inserimento di blocchi non testuali ----
  const putAfter = useCallback(
    (id: string, items: Block[], focusId?: string) => {
      const i = indexOf(id);
      const cur = blocksNow()[i];
      const replace = !!cur && cur.kind === "paragraph" && cur.text === "";
      const at = replace ? i : i + 1;
      const out = insertBlocks(blocksNow(), at, items, replace);
      const last = out[at + items.length - 1];
      // Dopo un blocco non testuale serve sempre un paragrafo su cui scrivere.
      const needTail = !last || !out[at + items.length];
      const final = needTail ? [...out, newBlock("paragraph")] : out;
      commit(final, { id: focusId ?? final[Math.min(at + items.length, final.length - 1)].id, at: "start" });
    },
    [commit],
  );

  const uploadInto = useCallback(
    async (id: string, files: File[]) => {
      let at = id;
      for (const f of files) {
        const r = await propsRef.current.upload(f);
        if (!r) continue;
        const b = r.image ? newBlock("image", { text: r.name, url: r.url }) : newBlock("file", { text: r.name, url: r.url });
        putAfter(at, [b], undefined);
        at = b.id;
      }
    },
    [putAfter],
  );

  const applyChoice = useCallback(
    (id: string, c: Choice) => {
      setSlash(null);
      setMenuFor(null);
      const cur = blocksNow().find((b) => b.id === id);
      if (!cur) return;
      const empty = cur.text === "" || /^\//.test(cur.text);
      if (c.kind === "image") {
        patch(id, { text: "", rev: (cur.rev ?? 0) + 1 });
        pickFile.current = { id };
        fileInput.current?.click();
        return;
      }
      if (c.kind === "ref") {
        patch(id, { text: "", rev: (cur.rev ?? 0) + 1 });
        setPickRef(id);
        return;
      }
      if (c.kind === "divider" || c.kind === "table") {
        const fresh = fromChoice(c);
        if (empty) {
          const i = indexOf(id);
          const out = insertBlocks(blocksNow(), i, [fresh], true);
          const final = out[i + 1] ? out : [...out, newBlock("paragraph")];
          commit(final, { id: c.kind === "table" ? fresh.id : final[i + 1].id, at: "start" });
        } else putAfter(id, [fresh], c.kind === "table" ? fresh.id : undefined);
        return;
      }
      const base = { text: /^\//.test(cur.text) ? "" : cur.text };
      const conv = convertBlock({ ...cur, ...base }, c.kind, { level: c.kind === "heading" ? c.level : undefined, callout: c.callout });
      const blocks = blocksNow().map((b) => (b.id === id ? { ...conv, level: c.kind === "heading" ? (c.level ?? 2) : isList(c.kind) ? cur.level : 0 } : b));
      commit(renumber(blocks), { id, at: "end" });
    },
    [commit, patch, putAfter],
  );

  // ---- Testo: scrittura, scorciatoie, «/», menzioni ----
  const onText = useCallback(
    (b: Block, el: HTMLElement) => {
      const md = readInline(el);
      const sc = b.kind === "paragraph" ? shortcut(md) : null;
      if (sc) {
        commit(renumber(blocksNow().map((x) => (x.id === b.id ? convertBlock({ ...x, text: "" }, sc.kind, sc) : x))), { id: b.id, at: "start" });
        setSlash(null);
        return;
      }
      patch(b.id, { text: md });
      // «/» all'inizio di un paragrafo apre il menu di inserimento.
      if (b.kind === "paragraph" && /^\/[^\n]{0,30}$/.test(md)) setSlash((s) => ({ id: b.id, q: md.slice(1), idx: s && s.id === b.id && s.q === md.slice(1) ? s.idx : 0 }));
      else if (slashRef.current?.id === b.id) setSlash(null);
      // «@» apre i nomi delle persone.
      const s = sel();
      const node = s?.anchorNode;
      if (s && s.isCollapsed && node && node.nodeType === 3 && el.contains(node)) {
        const before = (node.nodeValue ?? "").slice(0, s.anchorOffset);
        const m = /(?:^|[^\p{L}\p{N}_@.])@([\p{L}][\p{L}\p{N}'’-]*(?: [\p{L}][\p{L}\p{N}'’-]*)?)?$/u.exec(before);
        if (m) setMention({ id: b.id, q: m[1] ?? "", node: node as Text, end: s.anchorOffset, idx: 0 });
        else setMention(null);
      } else setMention(null);
    },
    [commit, patch],
  );

  const mentionCandidates = useMemo(() => {
    if (!mention) return [];
    const q = mention.q.toLowerCase();
    return members.filter((m) => q === "" || m.name.toLowerCase().startsWith(q) || m.name.toLowerCase().split(/\s+/).some((w) => w.startsWith(q))).slice(0, 6);
  }, [mention, members]);
  const slashItems = useMemo(() => (slash ? filterChoices(label, slash.q) : []), [slash, t]); // eslint-disable-line react-hooks/exhaustive-deps

  const pickMention = (name: string) => {
    const m = mentionRef.current;
    if (!m) return;
    const r = document.createRange();
    r.setStart(m.node, m.end - m.q.length - 1);
    r.setEnd(m.node, m.end);
    const s = sel();
    s?.removeAllRanges();
    s?.addRange(r);
    document.execCommand("insertText", false, `@${name} `);
    setMention(null);
  };

  /** Divide il testo del blocco nel punto del cursore. */
  const split = (el: HTMLElement): [string, string] => {
    const s = sel()!;
    if (!s.isCollapsed) document.execCommand("delete");
    const r = sel()!.getRangeAt(0);
    const a = document.createRange();
    a.selectNodeContents(el);
    a.setEnd(r.startContainer, r.startOffset);
    const z = document.createRange();
    z.selectNodeContents(el);
    z.setStart(r.startContainer, r.startOffset);
    return [readInline(a.cloneContents()), readInline(z.cloneContents())];
  };

  const focusNeighbour = (id: string, dir: -1 | 1, at: "start" | "end") => {
    const all = [...(root.current?.querySelectorAll<HTMLElement>("[data-bfocus]") ?? [])];
    const i = all.findIndex((e) => e.dataset.bfocus === id);
    const next = all[i + dir];
    if (!next) return false;
    next.focus();
    if (next.isContentEditable) setCaretChars(next, at === "start" ? 0 : 1e9);
    return true;
  };

  const onKey = (e: KeyboardEvent<HTMLElement>, b: Block) => {
    const el = e.currentTarget;
    const mod = e.metaKey || e.ctrlKey;
    const sl = slashRef.current;
    const mn = mentionRef.current;
    if (sl && sl.id === b.id) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const n = Math.max(slashItems.length, 1);
        setSlash({ ...sl, idx: (sl.idx + (e.key === "ArrowDown" ? 1 : n - 1)) % n });
        return;
      }
      if ((e.key === "Enter" || e.key === "Tab") && slashItems.length) {
        e.preventDefault();
        applyChoice(b.id, slashItems[Math.min(sl.idx, slashItems.length - 1)]);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setSlash(null);
        return;
      }
    }
    if (mn && mn.id === b.id && mentionCandidates.length) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        setMention({ ...mn, idx: (mn.idx + (e.key === "ArrowDown" ? 1 : mentionCandidates.length - 1)) % mentionCandidates.length });
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        pickMention(mentionCandidates[Math.min(mn.idx, mentionCandidates.length - 1)].name);
        return;
      }
      if (e.key === "Escape") {
        setMention(null);
        return;
      }
    }
    if (mod && e.key.toLowerCase() === "s") {
      e.preventDefault();
      propsRef.current.onSave();
      return;
    }
    const i = indexOf(b.id);
    if (e.altKey && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
      e.preventDefault();
      const at = el.isContentEditable ? charsBefore(el, caretIn(el)?.node ?? el, caretIn(el)?.offset ?? 0) : 0;
      commit(moveBlock(blocksNow(), i, e.key === "ArrowUp" ? -1 : 1), { id: b.id, at });
      return;
    }
    if (!el.isContentEditable) return;
    if (e.key === "Tab" && isList(b.kind)) {
      e.preventDefault();
      patch(b.id, { level: Math.max(0, Math.min(b.level + (e.shiftKey ? -1 : 1), 5)) });
      commit(renumber(blocksNow()), { id: b.id, at: charsBefore(el, caretIn(el)?.node ?? el, caretIn(el)?.offset ?? 0) });
      return;
    }
    if (e.key === "Enter" && !e.shiftKey && !mod) {
      e.preventDefault();
      if (isList(b.kind) && b.text === "") {
        // Invio su una voce vuota: si esce dall'elenco (prima di un livello).
        if (b.level > 0) commit(renumber(blocksNow().map((x) => (x.id === b.id ? touched(x, { level: b.level - 1 }) : x))), { id: b.id, at: "start" });
        else commit(renumber(blocksNow().map((x) => (x.id === b.id ? convertBlock(x, "paragraph") : x))), { id: b.id, at: "start" });
        return;
      }
      if ((b.kind === "quote" || b.kind === "callout") && (b.text === "" || b.text.endsWith("\n"))) {
        commit(blocksNow().map((x) => (x.id === b.id ? convertBlock({ ...x, text: x.text.replace(/\n+$/, "") }, "paragraph") : x)), { id: b.id, at: "start" });
        return;
      }
      const [before, after] = split(el);
      const keep = b.kind === "heading" || b.kind === "quote" || b.kind === "callout" ? "paragraph" : b.kind;
      const fresh = newBlock(keep as Block["kind"], { level: isList(b.kind) ? b.level : 0, text: after, rev: 1 });
      const blocks = blocksNow().map((x) => (x.id === b.id ? touched(x, { text: before, rev: (x.rev ?? 0) + 1 }) : x));
      blocks.splice(i + 1, 0, fresh);
      setSlash(null);
      commit(renumber(reflow(blocks, i, i + 1)), { id: fresh.id, at: "start" });
      return;
    }
    if (e.key === "Backspace" && atEdge(el, "start") && !mod) {
      if (b.kind !== "paragraph") {
        e.preventDefault();
        const next = isList(b.kind) && b.level > 0 ? (x: Block) => touched(x, { level: b.level - 1 }) : (x: Block) => convertBlock(x, "paragraph");
        commit(renumber(blocksNow().map((x) => (x.id === b.id ? next(x) : x))), { id: b.id, at: "start" });
        return;
      }
      const prev = blocksNow()[i - 1];
      if (!prev) return;
      e.preventDefault();
      if (isRich(prev.kind)) {
        const joinAt = root.current?.querySelector(`[data-bfocus="${prev.id}"]`)?.textContent?.length ?? 0;
        const blocks = blocksNow().filter((x) => x.id !== b.id).map((x) => (x.id === prev.id ? touched(x, { text: prev.text + b.text, rev: (x.rev ?? 0) + 1 }) : x));
        commit(renumber(reflow(blocks, i - 1)), { id: prev.id, at: joinAt });
      } else if (b.text === "") {
        commit(removeBlock(blocksNow(), i), { id: prev.id, at: "start" });
        if (!(prev.kind === "code" || prev.kind === "table" || prev.kind === "raw")) focusReq.current = { id: prev.id, at: "start" };
      } else if (prev.kind === "divider") commit(removeBlock(blocksNow(), i - 1), { id: b.id, at: "start" });
      return;
    }
    if (e.key === "Delete" && atEdge(el, "end") && !mod) {
      const next = blocksNow()[i + 1];
      if (next && isRich(next.kind)) {
        e.preventDefault();
        const blocks = blocksNow().filter((x) => x.id !== next.id).map((x) => (x.id === b.id ? touched(x, { text: b.text + next.text, rev: (x.rev ?? 0) + 1 }) : x));
        commit(renumber(reflow(blocks, i)), { id: b.id, at: el.textContent?.length ?? 0 });
      }
      return;
    }
    if (e.key === "ArrowUp" && !e.altKey && onEdgeLine(el, "first") && focusNeighbour(b.id, -1, "end")) e.preventDefault();
    else if (e.key === "ArrowDown" && !e.altKey && onEdgeLine(el, "last") && focusNeighbour(b.id, 1, "start")) e.preventDefault();
    else if (e.key === "ArrowLeft" && atEdge(el, "start") && focusNeighbour(b.id, -1, "end")) e.preventDefault();
    else if (e.key === "ArrowRight" && atEdge(el, "end") && focusNeighbour(b.id, 1, "start")) e.preventDefault();
  };

  /** Incolla: file → allegati; testo su più righe → blocchi; testo semplice → nel punto del cursore. */
  const onPaste = (e: ClipboardEvent<HTMLElement>, b: Block) => {
    const files = [...e.clipboardData.files];
    if (files.length) {
      e.preventDefault();
      void uploadInto(b.id, files);
      return;
    }
    const text = e.clipboardData.getData("text/plain");
    e.preventDefault();
    if (!text.includes("\n")) {
      document.execCommand("insertText", false, text);
      return;
    }
    const parsed = parseBlocks(text.replace(/\r\n/g, "\n")).blocks.map((x) => ({ ...x, src: undefined }));
    if (parsed.length) putAfter(b.id, parsed, parsed[parsed.length - 1].id);
  };

  // ---- Trascinamento dei blocchi ----
  const onRowDragOver = (e: DragEvent<HTMLElement>, id: string) => {
    if (!e.dataTransfer.types.includes("application/x-regia-block") && !e.dataTransfer.types.includes("Files")) return;
    e.preventDefault();
    const r = e.currentTarget.getBoundingClientRect();
    setDrop({ id, after: e.clientY > r.top + r.height / 2 });
  };
  const onRowDrop = (e: DragEvent<HTMLElement>, id: string) => {
    const d = drop;
    setDrop(null);
    const files = [...e.dataTransfer.files];
    if (files.length) {
      e.preventDefault();
      void uploadInto(id, files);
      return;
    }
    const from = e.dataTransfer.getData("application/x-regia-block");
    if (!from || from === id || !d) return;
    e.preventDefault();
    const blocks = blocksNow();
    const fi = blocks.findIndex((x) => x.id === from);
    const out = blocks.filter((x) => x.id !== from);
    const ti = out.findIndex((x) => x.id === id);
    out.splice(d.after ? ti + 1 : ti, 0, blocks[fi]);
    commit(renumber(reflow(out, 0, out.length - 1)), { id: from, at: "start" });
  };

  const addAfter = (id: string) => {
    const i = indexOf(id);
    const fresh = newBlock("paragraph");
    const out = blocksNow().slice();
    out.splice(i + 1, 0, fresh);
    commit(reflow(out, i, i + 1), { id: fresh.id, at: "start" });
  };
  const removeOne = (id: string) => {
    const i = indexOf(id);
    commit(renumber(removeBlock(blocksNow(), i)), { id: blocksNow()[Math.max(0, i - 1)].id, at: "end" });
  };
  const live = useRef({ onKey, onText, onPaste, onRowDragOver, onRowDrop, addAfter, removeOne });
  live.current = { onKey, onText, onPaste, onRowDragOver, onRowDrop, addAfter, removeOne };
  const stable = useMemo<RowApi>(
    () => ({
      onKey: (e, b) => live.current.onKey(e, b),
      onText: (b, el) => live.current.onText(b, el),
      onPaste: (e, b) => live.current.onPaste(e, b),
      onDragOver: (e, id) => live.current.onRowDragOver(e, id),
      onDrop: (e, id) => live.current.onRowDrop(e, id),
      addAfter: (id) => live.current.addAfter(id),
      removeOne: (id) => live.current.removeOne(id),
      move: (id, delta) => commit(moveBlock(blocksNow(), indexOf(id), delta), { id, at: "start" }),
      toParagraph: (id) => commit(renumber(blocksNow().map((x) => (x.id === id ? convertBlock(x, "paragraph") : x))), { id, at: "start" }),
      patch,
      commit,
      setMenuFor,
      setActive: (id) => (active.current = id),
    }),
    // i gestori leggono sempre lo stato corrente tramite i ref: l'oggetto resta lo stesso e le righe non si ridisegnano
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [commit, patch],
  );

  const refMap = useMemo(() => new Map((refs ?? []).map((r) => [r.key, r])), [refs]);

  return (
    <div
      ref={root}
      role="group"
      aria-label={t("knowledge.blocks.editor")}
      className="relative flex min-w-0 flex-col gap-0.5"
      onDragLeave={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && setDrop(null)}
    >
      {doc.blocks.map((b, i) => (
        <div key={b.id} data-block={b.id} className="group/blk relative min-w-0">
          <BlockRow
            b={b}
            n={numbers[i]}
            narrow={narrow}
            api={stable}
            menuOpen={menuFor === b.id}
            dropMark={drop && drop.id === b.id ? (drop.after ? "after" : "before") : null}
            refInfo={b.kind === "ref" ? refMap.get(b.text) : undefined}
          />
          {slash && slash.id === b.id && (
            <SlashMenu items={slashItems} idx={Math.min(slash.idx, Math.max(slashItems.length - 1, 0))} label={label} narrow={narrow} onPick={(c) => applyChoice(b.id, c)} onHover={(idx) => setSlash({ ...slash, idx })} />
          )}
          {mention && mention.id === b.id && mentionCandidates.length > 0 && (
            <ul role="listbox" aria-label={t("knowledge.blocks.mentionList")} className="absolute left-7 top-full z-30 m-0 flex w-64 max-w-[calc(100%-1.75rem)] list-none flex-col border border-rule bg-bg p-0 text-[13px] max-md:left-0">
              {mentionCandidates.map((m, k) => (
                <li key={m.id} role="option" aria-selected={k === mention.idx}>
                  <button type="button" className={`min-h-11 w-full px-2 py-1 text-left md:min-h-0 ${k === mention.idx ? "bg-sel text-accent" : ""}`} onMouseDown={(e) => e.preventDefault()} onClick={() => pickMention(m.name)}>
                    @{m.name}
                  </button>
                </li>
              ))}
            </ul>
          )}
          {pickRef === b.id && (
            <ElementPicker
              onClose={() => {
                setPickRef(null);
                root.current?.querySelector<HTMLElement>(`[data-bfocus="${b.id}"]`)?.focus();
              }}
              onPick={(key) => {
                setPickRef(null);
                const i = indexOf(b.id);
                const out = insertBlocks(blocksNow(), i, [newBlock("ref", { text: key })], true);
                commit(out[i + 1] ? out : [...out, newBlock("paragraph")], { id: (out[i + 1] ?? out[i]).id, at: "start" });
              }}
            />
          )}
          {menuFor === b.id && (
            <BlockMenu
              b={b}
              first={i === 0}
              last={i === doc.blocks.length - 1}
              narrow={narrow}
              label={label}
              onClose={() => {
                setMenuFor(null);
                focusReq.current = null;
                root.current?.querySelector<HTMLElement>(`[data-handle="${b.id}"]`)?.focus();
              }}
              onChoice={(c) => applyChoice(b.id, c)}
              onMove={(d) => {
                setMenuFor(null);
                commit(moveBlock(blocksNow(), indexOf(b.id), d), { id: b.id, at: "end" });
              }}
              onDuplicate={() => {
                setMenuFor(null);
                const copy = duplicateBlock(blocksNow(), indexOf(b.id));
                commit(renumber(copy), { id: copy[indexOf(b.id) + 1].id, at: "end" });
              }}
              onRemove={() => {
                setMenuFor(null);
                removeOne(b.id);
              }}
            />
          )}
        </div>
      ))}
      <input
        ref={fileInput}
        type="file"
        multiple
        className="hidden"
        aria-label={t("knowledge.blocks.type.image")}
        onChange={(e) => {
          const files = [...(e.target.files ?? [])];
          e.target.value = "";
          const target = pickFile.current?.id ?? active.current;
          pickFile.current = null;
          if (files.length && target) void uploadInto(target, files);
        }}
      />
      <SelectionBar root={root} />
    </div>
  );
}

// ------------------------------------------------------------------ riga di un blocco

type RowApi = {
  onKey: (e: KeyboardEvent<HTMLElement>, b: Block) => void;
  onText: (b: Block, el: HTMLElement) => void;
  onPaste: (e: ClipboardEvent<HTMLElement>, b: Block) => void;
  patch: (id: string, over: Partial<Block>, focus?: Focus) => void;
  commit: (blocks: Block[], focus?: Focus) => void;
  setMenuFor: (id: string | null) => void;
  setActive: (id: string) => void;
  onDragOver: (e: DragEvent<HTMLElement>, id: string) => void;
  onDrop: (e: DragEvent<HTMLElement>, id: string) => void;
  addAfter: (id: string) => void;
  removeOne: (id: string) => void;
  move: (id: string, delta: number) => void;
  toParagraph: (id: string) => void;
};

type RowProps = {
  b: Block;
  n: number;
  narrow: boolean;
  api: RowApi;
  menuOpen: boolean;
  dropMark: "before" | "after" | null;
  refInfo?: RefInfo;
};

const BlockRow = memo(function BlockRow({ b, n, narrow, api, menuOpen, dropMark, refInfo }: RowProps) {
  const t = useT();
  const row = useRef<HTMLDivElement>(null);
  const tag = (id: string) => t(`knowledge.blocks.type.${id}` as Key);
  const name =
    b.kind === "heading" ? tag(`h${Math.min(b.level, 3)}`) : b.kind === "callout" ? tag(CALLOUT_LABEL[b.callout ?? "NOTA"]) : b.kind === "raw" ? t("knowledge.blocks.raw.name") : tag(b.kind === "file" ? "image" : b.kind);
  const handleKey = (e: KeyboardEvent<HTMLButtonElement>) => {
    if (e.altKey && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
      e.preventDefault();
      api.move(b.id, e.key === "ArrowUp" ? -1 : 1);
    }
  };
  return (
    <div
      ref={row}
      className={`flex min-w-0 items-start gap-1 border-t-2 border-b-2 ${dropMark === "before" ? "border-t-accent" : "border-t-transparent"} ${dropMark === "after" ? "border-b-accent" : "border-b-transparent"}`}
      onDragOver={(e) => api.onDragOver(e, b.id)}
      onDrop={(e) => api.onDrop(e, b.id)}
      onFocusCapture={() => api.setActive(b.id)}
    >
      {!narrow && (
        <button
          type="button"
          data-handle={b.id}
          draggable
          aria-label={t("knowledge.blocks.handle", { block: name })}
          aria-haspopup="menu"
          aria-expanded={menuOpen}
          className="mt-0.5 flex h-7 w-6 shrink-0 cursor-grab items-center justify-center text-[14px] leading-none text-muted opacity-30 hover:opacity-100 focus:opacity-100 group-focus-within/blk:opacity-100 group-hover/blk:opacity-100"
          onClick={() => api.setMenuFor(menuOpen ? null : b.id)}
          onKeyDown={handleKey}
          onDragStart={(e) => {
            e.dataTransfer.setData("application/x-regia-block", b.id);
            e.dataTransfer.effectAllowed = "move";
            if (row.current) e.dataTransfer.setDragImage(row.current, 0, 0);
          }}
        >
          <span aria-hidden="true">⋮⋮</span>
        </button>
      )}
      <div className={`min-w-0 flex-1 ${narrow ? "group-focus-within/blk:pr-12" : ""}`}>
        <BlockBody b={b} n={n} name={name} api={api} refInfo={refInfo} />
      </div>
      {narrow && (
        <button
          type="button"
          data-handle={b.id}
          aria-label={t("knowledge.blocks.menuButton", { block: name })}
          aria-haspopup="menu"
          aria-expanded={menuOpen}
          className="absolute right-0 top-0 z-10 hidden h-11 w-11 items-center justify-center border border-hair bg-bg text-[16px] leading-none group-focus-within/blk:flex"
          onClick={() => api.setMenuFor(menuOpen ? null : b.id)}
        >
          <span aria-hidden="true">⋯</span>
        </button>
      )}
    </div>
  );
});

const editableCls = "min-w-0 whitespace-pre-wrap break-words outline-none empty:before:pointer-events-none empty:before:text-muted empty:before:content-[attr(data-ph)] data-[empty=true]:before:pointer-events-none data-[empty=true]:before:text-muted data-[empty=true]:before:content-[attr(data-ph)] [&_code]:bg-sel [&_code]:px-1 [&_code]:font-mono [&_code]:text-[0.92em] [&_a]:text-accent [&_a]:underline";

function Rich({ b, api, cls, label, ph }: { b: Block; api: RowApi; cls: string; label: string; ph: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (ref.current) fillInline(ref.current, b.text);
    // il testo si ridisegna solo quando cambia da programma (rev), non a ogni battuta
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [b.rev]);
  return (
    <div
      ref={ref}
      role="textbox"
      aria-multiline="true"
      aria-label={label}
      contentEditable
      suppressContentEditableWarning
      spellCheck
      data-bfocus={b.id}
      data-rich="1"
      data-ph={ph}
      data-empty={b.text === ""}
      className={`${editableCls} ${cls}`}
      onInput={(e) => api.onText(b, e.currentTarget)}
      onKeyDown={(e) => api.onKey(e, b)}
      onPaste={(e) => api.onPaste(e, b)}
    />
  );
}

function BlockBody({ b, n, name, api, refInfo }: { b: Block; n: number; name: string; api: RowApi; refInfo?: RefInfo }) {
  const t = useT();
  switch (b.kind) {
    case "paragraph":
      return <Rich b={b} api={api} cls="py-1 text-[14.5px] leading-relaxed" label={name} ph={t("knowledge.blocks.ph.paragraph")} />;
    case "heading":
      return <Rich b={b} api={api} cls={`py-1 ${HEADING_CLS[Math.min(b.level, 3)]}`} label={name} ph={name} />;
    case "bullet":
    case "number":
    case "task":
      return (
        <div className="flex min-w-0 items-start gap-1" style={{ paddingLeft: b.level * 24 }}>
          {b.kind === "task" ? (
            <input
              type="checkbox"
              checked={!!b.checked}
              aria-label={t("knowledge.blocks.taskDone")}
              className="mt-2.5 size-4 shrink-0 max-md:size-5"
              onChange={(e) => api.patch(b.id, { checked: e.target.checked })}
            />
          ) : (
            <span aria-hidden="true" className="w-5 shrink-0 select-none py-1 text-right text-[14.5px] leading-relaxed text-muted">{b.kind === "number" ? `${n}.` : "•"}</span>
          )}
          <Rich b={b} api={api} cls={`flex-1 py-1 text-[14.5px] leading-relaxed ${b.kind === "task" && b.checked ? "text-muted line-through" : ""}`} label={`${name}, ${t("knowledge.blocks.level", { level: b.level + 1 })}`} ph={t("knowledge.blocks.ph.item")} />
        </div>
      );
    case "quote":
      return <Rich b={b} api={api} cls="border-l-2 border-hair py-1 pl-3 text-[14.5px] leading-relaxed text-muted" label={name} ph={t("knowledge.blocks.ph.quote")} />;
    case "callout": {
      const c = CALLOUT_CLS[b.callout ?? "NOTA"] ?? CALLOUT_CLS.NOTA;
      return (
        <div className={`flex min-w-0 flex-col gap-0.5 border-l-2 py-0.5 pl-3 ${c.border}`}>
          <span aria-hidden="true" className={`label ${c.text}`}>{t(`knowledge.md.callout.${CALLOUT_LABEL[b.callout ?? "NOTA"]}` as Key)}</span>
          <Rich b={b} api={api} cls="py-0.5 text-[14.5px] leading-relaxed" label={name} ph={t("knowledge.blocks.ph.callout")} />
        </div>
      );
    }
    case "code":
      return (
        <div className="flex min-w-0 flex-col border-l-2 border-rule">
          <input
            className="w-24 border-0 bg-transparent px-3 pt-1 font-mono text-[11px] text-muted outline-none"
            value={b.lang ?? ""}
            maxLength={30}
            aria-label={t("knowledge.blocks.code.lang")}
            placeholder={t("knowledge.blocks.code.lang")}
            onChange={(e) => api.patch(b.id, { lang: e.target.value.replace(/[^\w+#.-]/g, "") })}
          />
          <textarea
            data-bfocus={b.id}
            aria-label={t("knowledge.blocks.code.body")}
            spellCheck={false}
            rows={Math.max(2, b.text.split("\n").length)}
            value={b.text}
            className="m-0 w-full min-w-0 resize-none border-0 bg-transparent px-3 py-1.5 font-mono text-[12.5px] leading-relaxed outline-none"
            onChange={(e) => api.patch(b.id, { text: e.target.value })}
            onKeyDown={(e) => {
              api.onKey(e, b);
              if (e.defaultPrevented) return;
              if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) (e.preventDefault(), api.addAfter(b.id));
              else if (e.key === "Backspace" && b.text === "" && !b.lang) (e.preventDefault(), api.toParagraph(b.id));
            }}
            onPaste={(e) => {
              if (e.clipboardData.files.length) api.onPaste(e, b);
            }}
          />
        </div>
      );
    case "table":
      return <TableBlock b={b} api={api} name={name} />;
    case "image":
      return (
        <div data-bfocus={b.id} tabIndex={0} role="group" aria-label={name} className="flex min-w-0 flex-col gap-1 py-1 outline-none focus-visible:outline-2 focus-visible:outline-accent" onKeyDown={(e) => nonText(e, b, api)}>
          {b.url && /^\/api\/v1\/attachments\//.test(b.url) ? (
            <img src={b.url.replace(/\?download=1$/, "")} alt={b.text} loading="lazy" decoding="async" className="block h-auto max-h-[420px] max-w-full self-start border border-hair" />
          ) : (
            <span className="text-[13px] text-muted">{t("knowledge.md.externalImage", { name: b.text || b.url || "" })}</span>
          )}
          <input className="field max-w-md py-1 text-[12.5px]" value={b.text} aria-label={t("knowledge.blocks.image.alt")} placeholder={t("knowledge.blocks.image.alt")} onChange={(e) => api.patch(b.id, { text: e.target.value.replace(/[\]\n]/g, "") })} />
        </div>
      );
    case "file":
      return (
        <div data-bfocus={b.id} tabIndex={0} role="group" aria-label={name} className="flex min-w-0 items-center gap-2 py-1.5 outline-none focus-visible:outline-2 focus-visible:outline-accent" onKeyDown={(e) => nonText(e, b, api)}>
          <span aria-hidden="true">📎</span>
          <a href={b.url} className="min-w-0 break-words text-accent underline">{b.text}</a>
        </div>
      );
    case "ref":
      return (
        <div data-bfocus={b.id} tabIndex={0} role="group" aria-label={`${name}: ${b.text}`} className="flex min-w-0 flex-wrap items-baseline gap-x-2 py-1.5 outline-none focus-visible:outline-2 focus-visible:outline-accent" onKeyDown={(e) => nonText(e, b, api)}>
          <span className="font-mono text-[13px] text-accent">{b.text}</span>
          {refInfo && <span className="min-w-0 break-words text-[13.5px]">{refInfo.title}</span>}
          {refInfo && <span className="text-[11.5px] text-muted">{refInfo.type_name}{refInfo.state ? ` · ${refInfo.state}` : ""}</span>}
        </div>
      );
    case "divider":
      return (
        <div data-bfocus={b.id} tabIndex={0} role="separator" aria-label={name} className="flex h-8 items-center outline-none focus-visible:outline-2 focus-visible:outline-accent" onKeyDown={(e) => nonText(e, b, api)}>
          <hr className="m-0 w-full border-0 border-t border-hair" />
        </div>
      );
    case "raw":
      return (
        <div className="flex min-w-0 flex-col gap-1 border-l-2 border-flag pl-3">
          <span className="text-[11.5px] text-muted">{t("knowledge.blocks.raw.hint")}</span>
          <textarea
            data-bfocus={b.id}
            aria-label={t("knowledge.blocks.raw.label")}
            spellCheck={false}
            rows={Math.max(2, b.text.split("\n").length)}
            value={b.text}
            className="m-0 w-full min-w-0 resize-none border-0 bg-transparent p-0 font-mono text-[12.5px] leading-relaxed outline-none"
            onChange={(e) => api.patch(b.id, { text: e.target.value })}
            onKeyDown={(e) => api.onKey(e, b)}
          />
        </div>
      );
  }
}

/** Blocchi senza testo: Invio aggiunge un paragrafo sotto, Backspace/Canc li toglie. */
function nonText(e: KeyboardEvent<HTMLElement>, b: Block, api: RowApi) {
  if (e.target !== e.currentTarget) return;
  api.onKey(e, b);
  if (e.defaultPrevented) return;
  if (e.key === "Enter") {
    e.preventDefault();
    api.addAfter(b.id);
  } else if (e.key === "Backspace" || e.key === "Delete") {
    e.preventDefault();
    api.removeOne(b.id);
  }
}

function TableBlock({ b, api, name }: { b: Block; api: RowApi; name: string }) {
  const t = useT();
  const rows = b.rows ?? [[""]];
  const cols = rows[0].length;
  const set = (r: number, c: number, v: string) => api.patch(b.id, { rows: rows.map((row, i) => (i === r ? row.map((x, j) => (j === c ? v : x)) : row)) });
  const btn = "border border-hair px-2 py-1 text-[12px] hover:border-rule max-md:min-h-11";
  return (
    <div className="flex min-w-0 flex-col gap-1 py-1" role="group" aria-label={name}>
      <div className="max-w-full overflow-x-auto">
        <table className="w-full border-collapse text-[13px]">
          <tbody>
            {rows.map((row, r) => (
              <tr key={r}>
                {row.map((v, c) => (
                  <td key={c} className={`border-b p-0 ${r === 0 ? "border-b-2 border-rule" : "border-hair"}`}>
                    <input
                      data-bfocus={r === 0 && c === 0 ? b.id : undefined}
                      className={`w-full min-w-[110px] bg-transparent px-2 py-1.5 outline-none focus:bg-sel max-md:min-h-11 ${r === 0 ? "font-semibold" : ""}`}
                      value={v}
                      aria-label={t("knowledge.blocks.table.cell", { row: r + 1, col: c + 1 })}
                      onChange={(e) => set(r, c, e.target.value)}
                      onKeyDown={(e) => {
                        if (e.altKey && (e.key === "ArrowUp" || e.key === "ArrowDown")) api.onKey(e as unknown as KeyboardEvent<HTMLElement>, b);
                      }}
                    />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="flex flex-wrap gap-1">
        <button type="button" className={btn} onClick={() => api.patch(b.id, { rows: [...rows, Array(cols).fill("")] })}>{t("knowledge.blocks.table.addRow")}</button>
        <button type="button" className={btn} onClick={() => api.patch(b.id, { rows: rows.map((r) => [...r, ""]), align: [...(b.align ?? []), "left"] })}>{t("knowledge.blocks.table.addCol")}</button>
        <button type="button" className={btn} disabled={rows.length < 2} onClick={() => api.patch(b.id, { rows: rows.slice(0, -1) })}>{t("knowledge.blocks.table.delRow")}</button>
        <button type="button" className={btn} disabled={cols < 2} onClick={() => api.patch(b.id, { rows: rows.map((r) => r.slice(0, -1)), align: (b.align ?? []).slice(0, -1) })}>{t("knowledge.blocks.table.delCol")}</button>
      </div>
    </div>
  );
}

// ------------------------------------------------------------------ menu

function SlashMenu({ items, idx, label, narrow, onPick, onHover }: { items: Choice[]; idx: number; label: (id: string) => string; narrow: boolean; onPick: (c: Choice) => void; onHover: (i: number) => void }) {
  const t = useT();
  const list = useRef<HTMLUListElement>(null);
  useEffect(() => {
    list.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" });
  }, [idx]);
  return (
    <ul
      ref={list}
      role="listbox"
      aria-label={t("knowledge.blocks.slash.label")}
      className={`absolute z-30 m-0 flex max-h-[50dvh] list-none flex-col overflow-y-auto border border-rule bg-bg p-0 text-[13.5px] ${narrow ? "inset-x-0 top-full" : "left-7 top-full w-72"}`}
    >
      {items.length === 0 && <li role="presentation" className="px-3 py-2 text-muted">{t("knowledge.blocks.slash.none")}</li>}
      {items.map((c, i) => (
        <li key={c.id} role="option" id={`slash-${c.id}`} aria-selected={i === idx}>
          <button type="button" tabIndex={-1} className={`flex min-h-11 w-full items-center px-3 py-1 text-left md:min-h-8 ${i === idx ? "bg-sel text-accent" : ""}`} onMouseDown={(e) => e.preventDefault()} onMouseMove={() => onHover(i)} onClick={() => onPick(c)}>
            {label(c.id)}
          </button>
        </li>
      ))}
    </ul>
  );
}

function BlockMenu({ b, first, last, narrow, label, onClose, onChoice, onMove, onDuplicate, onRemove }: {
  b: Block; first: boolean; last: boolean; narrow: boolean; label: (id: string) => string;
  onClose: () => void; onChoice: (c: Choice) => void; onMove: (d: number) => void; onDuplicate: () => void; onRemove: () => void;
}) {
  const t = useT();
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    box.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
    const away = (e: MouseEvent) => {
      if (box.current && !box.current.contains(e.target as Node) && !(e.target as HTMLElement).closest("[data-handle]")) onClose();
    };
    document.addEventListener("mousedown", away);
    return () => document.removeEventListener("mousedown", away);
  }, [onClose]);
  const convertible = isRich(b.kind) || b.kind === "code";
  const item = "flex min-h-11 w-full items-center px-3 py-1 text-left hover:bg-sel md:min-h-8 focus:bg-sel focus:outline-none";
  return (
    <div
      ref={box}
      role="menu"
      aria-label={t("knowledge.blocks.menu.label")}
      className={`z-40 flex max-h-[70dvh] flex-col overflow-y-auto border border-rule bg-bg text-[13.5px] ${narrow ? "fixed inset-x-2 bottom-2" : "absolute left-0 top-full w-60"}`}
      onKeyDown={(e) => {
        const all = [...(box.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])];
        const i = all.indexOf(document.activeElement as HTMLElement);
        if (e.key === "ArrowDown") (e.preventDefault(), all[(i + 1) % all.length]?.focus());
        else if (e.key === "ArrowUp") (e.preventDefault(), all[(i + all.length - 1) % all.length]?.focus());
        else if (e.key === "Escape" || e.key === "Tab") (e.preventDefault(), onClose());
      }}
    >
      {convertible && (
        <>
          <span className="label px-3 pt-2 text-muted" role="presentation">{t("knowledge.blocks.menu.changeTo")}</span>
          {TEXT_KINDS.map((c) => (
            <button key={c.id} role="menuitemradio" aria-checked={sameChoice(b, c)} type="button" className={`${item} ${sameChoice(b, c) ? "text-accent" : ""}`} onClick={() => onChoice(c)}>
              {label(c.id)}
            </button>
          ))}
          <hr className="m-0 border-0 border-t border-hair" />
        </>
      )}
      {!convertible && (
        <button role="menuitem" type="button" className={item} onClick={() => onChoice(CHOICES[0])}>{t("knowledge.blocks.menu.toText")}</button>
      )}
      <button role="menuitem" type="button" className={item} disabled={first} onClick={() => onMove(-1)}>{t("knowledge.blocks.menu.up")}</button>
      <button role="menuitem" type="button" className={item} disabled={last} onClick={() => onMove(1)}>{t("knowledge.blocks.menu.down")}</button>
      <button role="menuitem" type="button" className={item} onClick={onDuplicate}>{t("knowledge.blocks.menu.duplicate")}</button>
      <button role="menuitem" type="button" className={`${item} text-danger`} onClick={onRemove}>{t("knowledge.blocks.menu.remove")}</button>
    </div>
  );
}

function ElementPicker({ onPick, onClose }: { onPick: (key: string) => void; onClose: () => void }) {
  const t = useT();
  const [q, setQ] = useState("");
  const [debounced, setDebounced] = useState("");
  useEffect(() => {
    const h = window.setTimeout(() => setDebounced(q.trim()), 250);
    return () => window.clearTimeout(h);
  }, [q]);
  const found = useQuery({ queryKey: ["kb", "editor-search", debounced], queryFn: () => api.search(debounced, undefined, 8), enabled: debounced.length >= 2 });
  return (
    <div className="absolute left-7 top-full z-30 flex w-80 max-w-full flex-col gap-1 border border-rule bg-bg p-2 max-md:left-0">
      <label className="flex flex-col gap-1">
        <span className="label">{t("knowledge.blocks.ref.field")}</span>
        <input autoFocus className="field py-1 max-md:min-h-11" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("knowledge.blocks.ref.placeholder")} onKeyDown={(e) => e.key === "Escape" && onClose()} />
      </label>
      <ul className="m-0 flex list-none flex-col p-0">
        {(found.data?.items ?? []).map((el) => (
          <li key={el.id}>
            <button type="button" className="flex min-h-11 w-full items-baseline gap-2 py-1 text-left hover:text-accent md:min-h-0" onClick={() => onPick(el.key)}>
              <span className="font-mono text-[12px] text-accent">{el.key}</span>
              <span className="min-w-0 flex-1 break-words">{el.title}</span>
              <span className="text-[11.5px] text-muted">{el.type_name}</span>
            </button>
          </li>
        ))}
      </ul>
      <button type="button" className="self-start text-[12px] text-muted underline max-md:min-h-11" onClick={onClose}>{t("knowledge.blocks.ref.close")}</button>
    </div>
  );
}

// ------------------------------------------------------------------ barra sulla selezione

function SelectionBar({ root }: { root: RefObject<HTMLDivElement | null> }) {
  const t = useT();
  const narrow = useNarrow();
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const [link, setLink] = useState<{ range: Range; url: string; bad: boolean } | null>(null);
  const linkRef = useRef(link);
  linkRef.current = link;

  useEffect(() => {
    const on = () => {
      if (linkRef.current) return;
      const s = window.getSelection();
      if (!s || s.rangeCount === 0 || s.isCollapsed) return setPos(null);
      const r = s.getRangeAt(0);
      const host = (r.commonAncestorContainer instanceof Element ? r.commonAncestorContainer : r.commonAncestorContainer.parentElement)?.closest("[data-rich]");
      if (!host || !root.current?.contains(host)) return setPos(null);
      const box = r.getBoundingClientRect();
      const top = box.top > 56 ? box.top - 48 : box.bottom + 8;
      setPos({ top, left: Math.min(Math.max(8, box.left), Math.max(8, window.innerWidth - 332)) });
    };
    document.addEventListener("selectionchange", on);
    return () => document.removeEventListener("selectionchange", on);
  }, [root]);

  const hostOf = (): HTMLElement | null => {
    const s = window.getSelection();
    const n = s?.anchorNode;
    return (n instanceof Element ? n : n?.parentElement)?.closest<HTMLElement>("[data-rich]") ?? null;
  };
  const sync = (host: HTMLElement | null) => host?.dispatchEvent(new Event("input", { bubbles: true }));
  const bold = () => document.execCommand("bold");
  const italic = () => document.execCommand("italic");
  const code = () => {
    const s = window.getSelection();
    const host = hostOf();
    if (!s || s.rangeCount === 0 || !host) return;
    const inside = (s.anchorNode instanceof Element ? s.anchorNode : s.anchorNode?.parentElement)?.closest("code");
    const r = s.getRangeAt(0);
    if (inside && host.contains(inside)) {
      inside.replaceWith(...inside.childNodes);
    } else {
      const c = document.createElement("code");
      c.textContent = r.toString();
      r.deleteContents();
      r.insertNode(c);
      const n = document.createRange();
      n.selectNodeContents(c);
      s.removeAllRanges();
      s.addRange(n);
    }
    sync(host);
  };
  const startLink = () => {
    const s = window.getSelection();
    if (!s || s.rangeCount === 0) return;
    const a = (s.anchorNode instanceof Element ? s.anchorNode : s.anchorNode?.parentElement)?.closest("a");
    if (a) {
      document.execCommand("unlink");
      return;
    }
    setLink({ range: s.getRangeAt(0).cloneRange(), url: "", bad: false });
  };
  const applyLink = () => {
    if (!link) return;
    const href = cleanHref(link.url);
    if (!href) return setLink({ ...link, bad: true });
    const s = window.getSelection();
    s?.removeAllRanges();
    s?.addRange(link.range);
    document.execCommand("createLink", false, href);
    setLink(null);
    setPos(null);
  };
  const mention = () => {
    const s = window.getSelection();
    s?.collapseToEnd();
    document.execCommand("insertText", false, "@");
  };

  if (!pos && !link) return null;
  const btn = "flex min-h-11 flex-1 items-center justify-center border border-hair bg-bg px-2.5 text-[12.5px] hover:border-rule md:min-h-8 md:flex-none";
  const at = pos ?? { top: 8, left: 8 };
  return createPortal(
    <div
      role="toolbar"
      aria-label={t("knowledge.blocks.sel.label")}
      style={narrow ? { top: at.top, left: 8, right: 8 } : { top: at.top, left: at.left }}
      className="fixed z-50 flex flex-wrap gap-1 border border-rule bg-bg p-1 shadow-sm"
      onMouseDown={(e) => !(e.target as HTMLElement).closest("input") && e.preventDefault()}
    >
      {link ? (
        <form
          className="flex w-full flex-wrap items-center gap-1"
          onSubmit={(e) => {
            e.preventDefault();
            applyLink();
          }}
        >
          <input autoFocus className="field min-w-0 flex-1 py-1 max-md:min-h-11" aria-label={t("knowledge.blocks.sel.linkUrl")} placeholder="https://" value={link.url} onChange={(e) => setLink({ ...link, url: e.target.value, bad: false })} onKeyDown={(e) => e.key === "Escape" && setLink(null)} />
          <button type="submit" className={btn}>{t("knowledge.blocks.sel.linkApply")}</button>
          {link.bad && <span role="alert" className="w-full px-1 text-[12px] text-danger">{t("knowledge.blocks.sel.linkInvalid")}</span>}
        </form>
      ) : (
        <>
          <button type="button" className={`${btn} font-bold`} onClick={bold}>{t("knowledge.blocks.sel.bold")}</button>
          <button type="button" className={`${btn} italic`} onClick={italic}>{t("knowledge.blocks.sel.italic")}</button>
          <button type="button" className={`${btn} font-mono`} onClick={code}>{t("knowledge.blocks.sel.code")}</button>
          <button type="button" className={btn} onClick={startLink}>{t("knowledge.blocks.sel.link")}</button>
          <button type="button" className={btn} onClick={mention}>{t("knowledge.blocks.sel.mention")}</button>
        </>
      )}
    </div>,
    document.body,
  );
}
