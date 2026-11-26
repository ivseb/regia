import { Select } from "../select";
import { useCallback, useEffect, useMemo, useRef, useState, type ClipboardEvent, type DragEvent, type KeyboardEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, collab } from "../api";
import { kb, type Lock, type PageDetail } from "../knowledgeApi";
import { CALLOUT_KINDS, citedKeys, headingsOf, PageMarkdown } from "../kbMarkdown";
import { BlockEditor } from "./KnowledgeBlocks";
import { ErrorNote } from "../ui";
import { useNarrow } from "./timeline";
import { useT, useLang } from "../i18n/react";
import { fmtDateTime, fmtTime } from "../i18n/format";

// Editor delle pagine. Due modi di scrivere sullo stesso testo Markdown:
//  - «Blocchi» (predefinito, KnowledgeBlocks.tsx): paragrafi, titoli, elenchi, avvisi, tabelle…
//    con «/» per inserire, maniglia per spostare e barra sulla selezione;
//  - «Markdown»: la casella di testo con la barra dai pulsanti leggibili e l'anteprima
//    (affiancata su schermi larghi, a schede sul telefono).
// In entrambi: menzioni con @, chiavi degli elementi, immagini e allegati per trascinamento,
// incolla o scelta del file, bozza locale automatica, blocco di modifica rinnovato ogni minuto,
// salvataggio con nota di versione. Nessuna dipendenza esterna (vedi docs/sviluppo/knowledge-blocchi.md).

type Draft = { title: string; body: string; base: number; at: string };
const CALLOUT_LABEL = { NOTA: "note", SUGGERIMENTO: "tip", IMPORTANTE: "important", ATTENZIONE: "warning", PERICOLO: "caution" } as const;
const MODE_KEY = "regia.kb.editor.modo";
type Mode = "blocchi" | "markdown";
function readMode(): Mode {
  try {
    return localStorage.getItem(MODE_KEY) === "markdown" ? "markdown" : "blocchi";
  } catch {
    return "blocchi";
  }
}
const draftKey = (id: string) => `regia.kb.bozza.${id}`;

function readDraft(id: string): Draft | null {
  try {
    const raw = localStorage.getItem(draftKey(id));
    return raw ? (JSON.parse(raw) as Draft) : null;
  } catch {
    return null;
  }
}
function writeDraft(id: string, d: Draft | null) {
  try {
    if (d) localStorage.setItem(draftKey(id), JSON.stringify(d));
    else localStorage.removeItem(draftKey(id));
  } catch {
    /* spazio del browser pieno o non disponibile: la bozza resta solo in memoria */
  }
}

/** Coordinate del cursore dentro una textarea (copia speculare del testo con gli stessi stili). */
function caretXY(el: HTMLTextAreaElement, pos: number): { x: number; y: number; h: number } {
  const cs = getComputedStyle(el);
  const div = document.createElement("div");
  for (const p of ["boxSizing", "width", "fontFamily", "fontSize", "fontWeight", "lineHeight", "letterSpacing", "paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "borderTopWidth", "borderLeftWidth", "tabSize"] as const) {
    div.style[p] = cs[p];
  }
  div.style.position = "absolute";
  div.style.visibility = "hidden";
  div.style.whiteSpace = "pre-wrap";
  div.style.overflowWrap = "break-word";
  div.textContent = el.value.slice(0, pos);
  const mark = document.createElement("span");
  mark.textContent = "\u200b";
  div.appendChild(mark);
  document.body.appendChild(div);
  const out = { x: mark.offsetLeft, y: mark.offsetTop - el.scrollTop, h: parseFloat(cs.lineHeight) || 20 };
  div.remove();
  return out;
}

/** Rilascio del blocco anche quando si chiude la scheda (fetch keepalive). */
function releaseLock(key: string) {
  void fetch(`/api/v1/kb/pages/${encodeURIComponent(key)}/lock`, { method: "DELETE", credentials: "same-origin", keepalive: true }).catch(() => undefined);
}

export function PageEditor({ detail, onDone }: { detail: PageDetail; onDone: () => void }) {
  const tr = useT();
  const lang = useLang();
  const qc = useQueryClient();
  const p = detail.page;
  const narrow = useNarrow();
  const [title, setTitle] = useState(p.title);
  const [body, setBody] = useState(p.body);
  const [note, setNote] = useState("");
  const [base] = useState(p.version);
  const [tab, setTab] = useState<"scrivi" | "anteprima">("scrivi");
  const [side, setSide] = useState(true);
  const [mode, setModeState] = useState<Mode>(readMode);
  // Il testo cambia da fuori dell'editor a blocchi (bozza ripresa, cambio di modo): lo si rimonta.
  const [gen, setGen] = useState(0);
  const setMode = (m: Mode) => {
    setModeState(m);
    setGen((g) => g + 1);
    try {
      localStorage.setItem(MODE_KEY, m);
    } catch {
      /* preferenza non salvata: vale solo per questa pagina */
    }
  };
  const [leaving, setLeaving] = useState(false);
  const [draft, setDraft] = useState<Draft | null>(() => {
    const d = readDraft(p.id);
    return d && (d.body !== p.body || d.title !== p.title) ? d : null;
  });
  const area = useRef<HTMLTextAreaElement>(null);
  const dirty = title !== p.title || body !== p.body;

  // Blocco di modifica: preso all'apertura, rinnovato ogni minuto, lasciato all'uscita.
  const [lock, setLock] = useState<{ lock: Lock | null; taken: boolean } | null>(null);
  const takeLock = useCallback(
    async (force = false) => {
      try {
        setLock(await kb.lock(p.key, force));
      } catch {
        /* senza blocco si scrive lo stesso: il salvataggio controlla */
      }
    },
    [p.key],
  );
  useEffect(() => {
    void takeLock();
    const t = window.setInterval(() => setLock((l) => (l?.taken ? (void takeLock(), l) : l)), 60_000);
    const onUnload = () => releaseLock(p.key);
    window.addEventListener("pagehide", onUnload);
    return () => {
      window.clearInterval(t);
      window.removeEventListener("pagehide", onUnload);
      releaseLock(p.key);
    };
  }, [p.key, takeLock]);

  // Bozza locale automatica (solo in questo browser).
  useEffect(() => {
    if (!dirty || draft) return;
    const t = window.setTimeout(() => writeDraft(p.id, { title, body, base, at: new Date().toISOString() }), 400);
    return () => window.clearTimeout(t);
  }, [title, body, dirty, draft, p.id, base]);

  const save = useMutation({
    mutationFn: () => kb.save(p.key, { title, body, note, base_version: base }),
    onSuccess: (r) => {
      writeDraft(p.id, null);
      qc.setQueryData(["kb", "page", p.key], r);
      qc.invalidateQueries({ queryKey: ["kb"] });
      releaseLock(p.key);
      onDone();
    },
  });
  const locked = save.error instanceof ApiError && save.error.code === "page.locked";

  // ---- Strumenti di scrittura ----
  const edit = (fn: (text: string, start: number, end: number) => { text: string; start: number; end: number }) => {
    const el = area.current;
    if (!el) return;
    const r = fn(body, el.selectionStart, el.selectionEnd);
    setBody(r.text);
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(r.start, r.end);
    });
  };
  const wrap = (before: string, after: string, placeholder: string) =>
    edit((t, s, e) => {
      const sel = t.slice(s, e) || placeholder;
      return { text: t.slice(0, s) + before + sel + after + t.slice(e), start: s + before.length, end: s + before.length + sel.length };
    });
  const prefixLines = (prefix: string | ((i: number) => string)) =>
    edit((t, s, e) => {
      const ls = t.lastIndexOf("\n", s - 1) + 1;
      const le = t.indexOf("\n", e);
      const end = le < 0 ? t.length : le;
      const lines = t.slice(ls, end).split("\n").map((l, i) => (typeof prefix === "string" ? prefix : prefix(i)) + l);
      const out = lines.join("\n");
      return { text: t.slice(0, ls) + out + t.slice(end), start: ls, end: ls + out.length };
    });
  const insert = (snippet: string, selectFrom?: number, selectLen?: number) =>
    edit((t, s, e) => {
      const lead = s > 0 && t[s - 1] !== "\n" && snippet.startsWith("\n") === false && /^[#>|`-]/.test(snippet) ? "\n\n" : "";
      const text = t.slice(0, s) + lead + snippet + t.slice(e);
      const at = s + lead.length + (selectFrom ?? snippet.length);
      return { text, start: at, end: at + (selectLen ?? 0) };
    });

  // ---- Allegati: trascina, incolla o scegli ----
  const [uploads, setUploads] = useState<{ name: string; error?: string }[]>([]);
  /** Carica un file come allegato della pagina; restituisce l'indirizzo da citare (o nulla se fallisce). */
  const uploadOne = async (f: File) => {
    setUploads((u) => [...u, { name: f.name }]);
    try {
      const a = await collab.upload(p.key, f);
      setUploads((u) => u.filter((x) => x.name !== f.name));
      qc.invalidateQueries({ queryKey: ["attachments", p.key] });
      const image = a.mime_type.startsWith("image/");
      return { image, name: a.name, url: image ? collab.contentUrl(a.id) : collab.contentUrl(a.id, true) };
    } catch (e) {
      setUploads((u) => u.map((x) => (x.name === f.name ? { ...x, error: e instanceof ApiError ? e.message : tr("knowledge.editor.uploadFailed") } : x)));
      return null;
    }
  };
  const upload = async (files: File[]) => {
    for (const f of files) {
      const r = await uploadOne(f);
      if (r) insert((r.image ? `![${r.name}](${r.url})` : `[${r.name}](${r.url})`) + "\n");
    }
  };
  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = [...e.clipboardData.files];
    if (files.length) {
      e.preventDefault();
      void upload(files);
    }
  };
  const onDrop = (e: DragEvent<HTMLTextAreaElement>) => {
    const files = [...e.dataTransfer.files];
    if (files.length) {
      e.preventDefault();
      void upload(files);
    }
  };
  const fileInput = useRef<HTMLInputElement>(null);

  // ---- Menzioni con @ ----
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000 });
  const [mention, setMention] = useState<{ q: string; at: number; top?: number; left?: number } | null>(null);
  const [mi, setMi] = useState(0);
  const candidates = useMemo(() => {
    if (!mention) return [];
    const q = mention.q.toLowerCase();
    const words = (n: string) => n.toLowerCase().split(/\s+/);
    return (members.data?.items ?? [])
      .filter((m) => m.member_type !== "requester" && (q === "" || m.display_name.toLowerCase().startsWith(q) || words(m.display_name).some((w) => w.startsWith(q))))
      .slice(0, 6);
  }, [mention, members.data]);
  const detectMention = (text: string, caret: number) => {
    const before = text.slice(Math.max(0, caret - 40), caret);
    const m = /(?:^|[^\p{L}\p{N}_@.])@([\p{L}][\p{L}\p{N}'’-]*(?: [\p{L}][\p{L}\p{N}'’-]*)?)?$/u.exec(before);
    if (m) {
      const el = area.current;
      const at = caret - (m[1]?.length ?? 0) - 1;
      const xy = el ? caretXY(el, at) : null;
      setMention({
        q: m[1] ?? "",
        at,
        top: el && xy ? el.offsetTop + Math.min(Math.max(xy.y + xy.h + 4, 0), el.clientHeight) : undefined,
        left: el && xy ? el.offsetLeft + Math.min(xy.x, Math.max(0, el.clientWidth - 260)) : undefined,
      });
      setMi(0);
    } else setMention(null);
  };
  const pickMention = (name: string) => {
    const el = area.current;
    if (!el || !mention) return;
    const caret = el.selectionStart;
    const text = body.slice(0, mention.at) + "@" + name + " " + body.slice(caret);
    setBody(text);
    setMention(null);
    const pos = mention.at + name.length + 2;
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(pos, pos);
    });
  };

  // ---- Elementi: chiave da cercare ----
  const [elSearch, setElSearch] = useState<string | null>(null);
  const found = useQuery({
    queryKey: ["kb", "editor-search", elSearch],
    queryFn: () => api.search(elSearch!, undefined, 8),
    enabled: !!elSearch && elSearch.trim().length >= 2,
  });

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (mention && candidates.length) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        setMi((i) => (i + (e.key === "ArrowDown" ? 1 : candidates.length - 1)) % candidates.length);
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        pickMention(candidates[mi].display_name);
        return;
      }
      if (e.key === "Escape") {
        setMention(null);
        return;
      }
    }
    const mod = e.metaKey || e.ctrlKey;
    if (mod && e.key.toLowerCase() === "s") {
      e.preventDefault();
      if (!save.isPending) save.mutate();
    } else if (mod && e.key.toLowerCase() === "b") {
      e.preventDefault();
      wrap("**", "**", tr("knowledge.editor.placeholderText"));
    } else if (mod && e.key.toLowerCase() === "i") {
      e.preventDefault();
      wrap("*", "*", tr("knowledge.editor.placeholderText"));
    } else if (e.key === "Tab" && !mod) {
      // Rientro negli elenchi (per gli elenchi annidati).
      e.preventDefault();
      if (e.shiftKey) edit((t, s, en) => {
        const ls = t.lastIndexOf("\n", s - 1) + 1;
        const n = t.slice(ls).startsWith("  ") ? 2 : 0;
        return { text: t.slice(0, ls) + t.slice(ls + n), start: Math.max(ls, s - n), end: Math.max(ls, en - n) };
      });
      else prefixLines("  ");
    }
  };

  // Pulsanti con parole, non sigle: il testo visibile è anche il nome accessibile.
  const tools: { label: string; run: () => void }[] = [
    { label: tr("knowledge.blocks.tool.title"), run: () => prefixLines("## ") },
    { label: tr("knowledge.blocks.tool.subtitle"), run: () => prefixLines("### ") },
    { label: tr("knowledge.blocks.tool.bold"), run: () => wrap("**", "**", tr("knowledge.editor.placeholderText")) },
    { label: tr("knowledge.blocks.tool.italic"), run: () => wrap("*", "*", tr("knowledge.editor.placeholderText")) },
    { label: tr("knowledge.blocks.tool.code"), run: () => wrap("`", "`", tr("knowledge.editor.placeholderCode")) },
    { label: tr("knowledge.blocks.type.bullet"), run: () => prefixLines("- ") },
    { label: tr("knowledge.blocks.type.number"), run: () => prefixLines((i) => `${i + 1}. `) },
    { label: tr("knowledge.blocks.type.task"), run: () => prefixLines("- [ ] ") },
    { label: tr("knowledge.blocks.type.quote"), run: () => prefixLines("> ") },
    { label: tr("knowledge.blocks.type.table"), run: () => { const c = tr("knowledge.editor.placeholderColumn"); insert(`| ${c} | ${c} |\n|---|---|\n| | |\n`, 2, c.length); } },
    { label: tr("knowledge.blocks.tool.codeBlock"), run: () => insert("```\n\n```\n", 4) },
    { label: tr("knowledge.blocks.tool.link"), run: () => wrap("[", "](https://)", tr("knowledge.editor.placeholderText")) },
    { label: tr("knowledge.blocks.tool.mention"), run: () => { insert("@"); setMention({ q: "", at: (area.current?.selectionStart ?? body.length) }); } },
    { label: tr("knowledge.blocks.type.ref"), run: () => setElSearch("") },
    { label: tr("knowledge.blocks.type.image"), run: () => fileInput.current?.click() },
    { label: tr("knowledge.blocks.type.divider"), run: () => insert("\n---\n") },
  ];

  const keys = useMemo(() => citedKeys(body), [body]);
  const refs = useQuery({ queryKey: ["kb", "refs", keys.join(",")], queryFn: () => kb.refs(keys), enabled: keys.length > 0, staleTime: 60_000 });
  const toc = useMemo(() => headingsOf(body).filter((h) => h.level <= 3), [body]);
  const blocks = mode === "blocchi";
  const split = !blocks && !narrow && side;
  const showWrite = split || tab === "scrivi";
  const showPreview = split || tab === "anteprima";
  const memberList = useMemo(() => (members.data?.items ?? []).filter((m) => m.member_type !== "requester").map((m) => ({ id: m.id, name: m.display_name })), [members.data]);

  return (
    <div className="flex min-w-0 flex-1 flex-col">
      <header className="sticky top-[49px] z-30 flex flex-wrap items-center gap-2 border-b border-rule bg-bg px-4 py-2 md:top-0 md:px-7">
        <span className="font-mono text-[11.5px] text-muted">
          {p.space_key} / {p.key} · v{base} → v{base + 1}
        </span>
        <span className="ml-auto flex flex-wrap items-center gap-2">
          <div role="group" aria-label={tr("knowledge.blocks.mode.label")} className="flex border border-hair text-[12.5px]">
            {(["blocchi", "markdown"] as const).map((m) => (
              <button key={m} type="button" aria-pressed={mode === m} className={`px-3 py-1 max-md:min-h-11 ${mode === m ? "bg-fg text-bg" : "text-muted"}`} onClick={() => mode !== m && setMode(m)}>
                {m === "blocchi" ? tr("knowledge.blocks.mode.blocks") : tr("knowledge.blocks.mode.markdown")}
              </button>
            ))}
          </div>
          {!narrow && !blocks && (
            <label className="flex items-center gap-1.5 text-[12.5px] text-muted">
              <input type="checkbox" checked={side} onChange={(e) => setSide(e.target.checked)} /> {tr("knowledge.editor.sideBySide")}
            </label>
          )}
          <button className="btn" onClick={() => (dirty ? setLeaving(true) : onDone())}>{tr("knowledge.editor.cancel")}</button>
          <button className="btn btn-primary" disabled={save.isPending || !title.trim()} onClick={() => save.mutate()}>
            {save.isPending ? tr("knowledge.editor.saving") : tr("knowledge.editor.saveVersion")}
          </button>
        </span>
      </header>
      <div className="flex min-w-0 flex-col gap-3 px-4 py-4 md:px-7">
        {lock && !lock.taken && lock.lock && (
          <p role="alert" className="m-0 flex flex-wrap items-center gap-2 border-l-2 border-flag pl-3 text-[13px]">
            <span>
              <span className="font-semibold">{tr("knowledge.editor.lockedBy", { name: lock.lock.name })}</span>{" "}
              {tr("knowledge.editor.lockUntil", { time: fmtTime(lock.lock.expires_at, { hour: "2-digit", minute: "2-digit" }) })}
            </span>
            <button className="btn py-1" onClick={() => void takeLock(true)}>{tr("knowledge.editor.takeOver")}</button>
          </p>
        )}
        {draft && (
          <p role="status" className="m-0 flex flex-wrap items-center gap-2 border-l-2 border-accent pl-3 text-[13px]">
            <span>
              {tr("knowledge.editor.localDraft", { when: fmtDateTime(draft.at, { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" }) })}
              {draft.base !== p.version ? `, ${tr("knowledge.editor.draftOutdated", { base: draft.base, version: p.version })}` : ""}.
            </span>
            <button
              className="btn py-1"
              onClick={() => {
                setTitle(draft.title);
                setBody(draft.body);
                setGen((g) => g + 1);
                setDraft(null);
              }}
            >
              {tr("knowledge.editor.resumeDraft")}
            </button>
            <button
              className="text-[12px] text-muted underline"
              onClick={() => {
                writeDraft(p.id, null);
                setDraft(null);
              }}
            >
              {tr("knowledge.editor.discard")}
            </button>
          </p>
        )}
        {leaving && (
          <p role="alert" className="m-0 flex flex-wrap items-center gap-2 border-l-2 border-flag pl-3 text-[13px]">
            {tr("knowledge.editor.unsaved")}
            <button className="btn py-1" onClick={onDone}>{tr("knowledge.editor.exit")}</button>
            <button
              className="btn py-1"
              onClick={() => {
                writeDraft(p.id, null);
                onDone();
              }}
            >
              {tr("knowledge.editor.discardExit")}
            </button>
            <button className="text-[12px] text-muted underline" onClick={() => setLeaving(false)}>{tr("knowledge.editor.keepWriting")}</button>
          </p>
        )}
        <label className="flex flex-col gap-1">
          <span className="sr-only">{tr("knowledge.editor.titleLabel")}</span>
          <input
            className="w-full border-0 border-b border-hair bg-transparent py-1 text-[26px] font-bold tracking-[-0.02em] text-fg outline-none focus:border-accent"
            value={title}
            maxLength={500}
            onChange={(e) => setTitle(e.target.value)}
            aria-label={tr("knowledge.editor.pageTitle")}
          />
        </label>
        {narrow && !blocks && (
          <div role="tablist" aria-label={tr("knowledge.editor.mode")} className="flex border border-hair text-[12.5px]">
            {(["scrivi", "anteprima"] as const).map((t) => (
              <button key={t} role="tab" aria-selected={tab === t} className={`flex-1 py-1.5 ${tab === t ? "bg-fg text-bg" : "text-muted"}`} onClick={() => setTab(t)}>
                {t === "scrivi" ? tr("knowledge.editor.write") : tr("knowledge.editor.preview")}
              </button>
            ))}
          </div>
        )}
        {blocks && (
          <div className="flex min-w-0 flex-col gap-2">
            <BlockEditor key={gen} value={body} onChange={setBody} members={memberList} refs={refs.data?.items} upload={uploadOne} onSave={() => !save.isPending && title.trim() && save.mutate()} />
            {uploads.map((u) => (
              <p key={u.name} className={`m-0 text-[12.5px] ${u.error ? "text-danger" : "text-muted"}`}>
                {u.error ? `${u.name}: ${u.error}` : tr("knowledge.editor.uploading", { name: u.name })}
              </p>
            ))}
            <p className="m-0 text-[11.5px] text-muted">{tr("knowledge.blocks.help")}</p>
          </div>
        )}
        <div className={`grid min-w-0 gap-4 ${split ? "grid-cols-2" : "grid-cols-1"} ${blocks ? "hidden" : ""}`}>
          {!blocks && showWrite && (
            <div className="relative flex min-w-0 flex-col gap-2">
              <div role="toolbar" aria-label={tr("knowledge.editor.formatting")} className="flex flex-wrap gap-1 border-b border-hair pb-2">
                {tools.map((t) => (
                  <button key={t.label} type="button" className="border border-hair px-2 py-0.5 text-[12px] text-fg hover:border-rule max-md:min-h-11" onMouseDown={(e) => e.preventDefault()} onClick={t.run}>
                    {t.label}
                  </button>
                ))}
                <Select
                  aria-label={tr("knowledge.blocks.tool.callout")}
                  className="border border-hair bg-bg px-1 py-0.5 text-[12px] text-fg max-md:min-h-11"
                  value=""
                  onChange={(e) => {
                    if (e.target.value) insert(`> [!${e.target.value}] `);
                  }}
                >
                  <option value="">{tr("knowledge.blocks.tool.callout")}</option>
                  {CALLOUT_KINDS.map((k) => <option key={k} value={k}>{tr(`knowledge.md.callout.${CALLOUT_LABEL[k as keyof typeof CALLOUT_LABEL]}`)}</option>)}
                </Select>
                <input
                  ref={fileInput}
                  type="file"
                  multiple
                  className="hidden"
                  onChange={(e) => {
                    void upload([...(e.target.files ?? [])]);
                    e.target.value = "";
                  }}
                />
              </div>
              <textarea
                ref={area}
                className="field min-h-[55dvh] resize-y font-mono text-[13px] leading-relaxed"
                value={body}
                spellCheck
                lang={lang}
                aria-label={tr("knowledge.editor.bodyLabel")}
                onChange={(e) => {
                  setBody(e.target.value);
                  detectMention(e.target.value, e.target.selectionStart);
                }}
                onKeyDown={onKeyDown}
                onPaste={onPaste}
                onDrop={onDrop}
                onDragOver={(e) => e.dataTransfer.types.includes("Files") && e.preventDefault()}
                onBlur={() => window.setTimeout(() => setMention(null), 150)}
              />
              {mention && candidates.length > 0 && (
                <ul
                  role="listbox"
                  aria-label={tr("knowledge.editor.mentionList")}
                  style={mention.top !== undefined ? { top: mention.top, left: mention.left } : { bottom: 8, left: 8 }}
                  className="absolute z-20 m-0 flex w-64 list-none flex-col border border-rule bg-bg p-0 text-[13px]"
                >
                  {candidates.map((m, i) => (
                    <li key={m.id} role="option" aria-selected={i === mi}>
                      <button type="button" className={`w-full px-2 py-1 text-left ${i === mi ? "bg-sel text-accent" : ""}`} onMouseDown={(e) => e.preventDefault()} onClick={() => pickMention(m.display_name)}>
                        @{m.display_name}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
              {elSearch !== null && (
                <div className="flex flex-col gap-1 border border-rule p-2">
                  <label className="flex flex-col gap-1">
                    <span className="label">{tr("knowledge.editor.itemToCite")}</span>
                    <input autoFocus className="field py-1" value={elSearch} onChange={(e) => setElSearch(e.target.value)} placeholder={tr("knowledge.editor.keyOrTitle")} onKeyDown={(e) => e.key === "Escape" && setElSearch(null)} />
                  </label>
                  <ul className="m-0 flex list-none flex-col p-0">
                    {(found.data?.items ?? []).map((el) => (
                      <li key={el.id}>
                        <button type="button" className="flex w-full items-baseline gap-2 py-1 text-left hover:text-accent" onClick={() => {
                          setElSearch(null);
                          insert(el.key + " ");
                        }}>
                          <span className="font-mono text-[12px] text-accent">{el.key}</span>
                          <span className="min-w-0 flex-1 break-words">{el.title}</span>
                          <span className="text-[11.5px] text-muted">{el.type_name}</span>
                        </button>
                      </li>
                    ))}
                  </ul>
                  <button type="button" className="self-start text-[12px] text-muted underline" onClick={() => setElSearch(null)}>{tr("knowledge.editor.close")}</button>
                </div>
              )}
              {uploads.map((u) => (
                <p key={u.name} className={`m-0 text-[12.5px] ${u.error ? "text-danger" : "text-muted"}`}>
                  {u.error ? `${u.name}: ${u.error}` : tr("knowledge.editor.uploading", { name: u.name })}
                </p>
              ))}
              <p className="m-0 text-[11.5px] text-muted">
                {tr("knowledge.editor.help")}
              </p>
            </div>
          )}
          {!blocks && showPreview && (
            <div className="flex min-w-0 flex-col gap-2" aria-label={tr("knowledge.editor.preview")}>
              {split && <span className="label border-b border-hair pb-2 text-muted">{tr("knowledge.editor.preview")}{toc.length > 1 ? ` · ${tr("knowledge.editor.sections", { count: toc.length })}` : ""}</span>}
              <div className="min-w-0 overflow-hidden">
                {body.trim() ? <PageMarkdown text={body} refs={refs.data?.items} mentions={(members.data?.items ?? []).map((m) => ({ id: m.id, name: m.display_name }))} /> : <p className="m-0 text-muted">{tr("knowledge.editor.previewEmpty")}</p>}
              </div>
            </div>
          )}
        </div>
        <label className="flex max-w-[640px] flex-col gap-1">
          <span className="label">{tr("knowledge.editor.versionNote")}</span>
          <input className="field" value={note} maxLength={500} onChange={(e) => setNote(e.target.value)} placeholder={tr("knowledge.editor.notePlaceholder")} />
        </label>
        <ErrorNote error={save.error} />
        {locked && (
          <button className="btn self-start" onClick={() => void takeLock(true).then(() => save.reset())}>
            {tr("knowledge.editor.takeOverRetry")}
          </button>
        )}
      </div>
    </div>
  );
}
