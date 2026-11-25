import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { kb, type Thread, type ThreadDraft, type ThreadList } from "../knowledgeApi";
import { anchorFromSelection, buildIndex, locate, type TextIndex } from "../kbAnchor";
import { Markdown } from "../markdown";
import { ago, ErrorNote, SectionTitle } from "../ui";
import { MentionBox } from "./Collaboration";
import { useNarrow } from "./timeline";
import { useT } from "../i18n/react";
import { fmtDateTime } from "../i18n/format";

// Commenti in linea (replica/knowledge.md): si seleziona una frase della pagina e si
// commenta. Il thread resta ancorato alla frase; le frasi commentate sono evidenziate nel
// testo (CSS Custom Highlight API, senza toccare il DOM) e il pannello a lato elenca le
// discussioni con risposte, menzioni e stato. Sul telefono il pannello sta sotto il testo.

type HighlightApi = { set(name: string, h: unknown): void; delete(name: string): void };
function highlights(): HighlightApi | null {
  const css = (typeof CSS !== "undefined" ? CSS : null) as unknown as { highlights?: HighlightApi } | null;
  return css?.highlights && typeof (window as unknown as { Highlight?: unknown }).Highlight === "function" ? css.highlights : null;
}
function paint(name: string, ranges: Range[]) {
  const h = highlights();
  if (!h) return;
  if (ranges.length === 0) h.delete(name);
  else h.set(name, new (window as unknown as { Highlight: new (...r: Range[]) => unknown }).Highlight(...ranges));
}

type Pending = { anchor: ThreadDraft; x: number; y: number };

export function InlineLayer({ pageKey, body, children }: { pageKey: string; body: string; children: ReactNode }) {
  const qc = useQueryClient();
  const tr = useT();
  const narrow = useNarrow();
  const box = useRef<HTMLDivElement>(null);
  const index = useRef<TextIndex | null>(null);
  const q = useQuery({ queryKey: ["kb", "threads", pageKey], queryFn: () => kb.threads(pageKey), refetchInterval: 45_000 });
  const list = q.data;
  const threads = useMemo(() => list?.items ?? [], [list]);
  const [ranges, setRanges] = useState<Map<string, Range>>(new Map());
  const [active, setActive] = useState<string | null>(null);
  const [pending, setPending] = useState<Pending | null>(null);
  const [draft, setDraft] = useState<ThreadDraft | null>(null);
  const [showResolved, setShowResolved] = useState(false);

  // Posizione delle frasi nel testo reso: si ricalcola quando cambiano discussioni, testo e DOM
  // (le chiavi degli elementi aggiungono lo stato accanto al testo).
  const recompute = useCallback(() => {
    const el = box.current;
    if (!el) return;
    const idx = buildIndex(el);
    index.current = idx;
    const found = new Map<string, Range>();
    for (const t of threads) {
      if (t.status !== "open") continue;
      const hit = locate(idx.text, { quote: t.quote, prefix: t.prefix, suffix: t.suffix, pos: null, nth: t.nth });
      const r = hit ? idx.range(hit.start, hit.end) : null;
      if (r) found.set(t.id, r);
    }
    setRanges(found);
  }, [threads]);
  useLayoutEffect(() => {
    recompute();
  }, [recompute, body]);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    let timer = 0;
    const mo = new MutationObserver(() => {
      window.clearTimeout(timer);
      timer = window.setTimeout(recompute, 150);
    });
    mo.observe(el, { childList: true, subtree: true, characterData: true });
    return () => {
      window.clearTimeout(timer);
      mo.disconnect();
    };
  }, [recompute]);
  useEffect(() => {
    paint("kb-thread", [...ranges.entries()].filter(([id]) => id !== active).map(([, r]) => r));
    const a = active ? ranges.get(active) : undefined;
    paint("kb-thread-active", a ? [a] : []);
  }, [ranges, active]);
  useEffect(
    () => () => {
      paint("kb-thread", []);
      paint("kb-thread-active", []);
    },
    [],
  );

  // Selezione di una frase: compare il pulsante «Commenta».
  const canComment = !!list?.can_comment;
  useEffect(() => {
    if (!canComment) return;
    let timer = 0;
    const on = () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        const el = box.current;
        const sel = window.getSelection();
        if (!el || !sel || sel.isCollapsed || sel.rangeCount === 0) return setPending(null);
        const r = sel.getRangeAt(0);
        if (!el.contains(r.commonAncestorContainer)) return setPending(null);
        const idx = index.current ?? buildIndex(el);
        const anchor = anchorFromSelection(idx, r);
        if (!anchor) return setPending(null);
        const rect = r.getBoundingClientRect();
        setPending({ anchor, x: rect.left + rect.width / 2, y: rect.bottom });
      }, 120);
    };
    document.addEventListener("selectionchange", on);
    return () => {
      window.clearTimeout(timer);
      document.removeEventListener("selectionchange", on);
    };
  }, [canComment]);

  // Un clic su una frase evidenziata apre la sua discussione.
  function onBoxClick() {
    const sel = window.getSelection();
    if (!sel || !sel.isCollapsed || !sel.anchorNode) return;
    for (const [id, r] of ranges) {
      try {
        if (r.isPointInRange(sel.anchorNode, sel.anchorOffset)) {
          setActive(id);
          document.getElementById(`thread-${id}`)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
          return;
        }
      } catch {
        /* nodo non più nel documento */
      }
    }
  }
  function goTo(id: string) {
    setActive(id);
    const r = ranges.get(id);
    r?.startContainer.parentElement?.scrollIntoView({ block: "center", behavior: "smooth" });
  }

  const open = threads.filter((t) => t.status === "open");
  const resolved = threads.filter((t) => t.status === "resolved");
  const showPanel = threads.length > 0 || !!draft;

  function startDraft() {
    if (!pending) return;
    setDraft(pending.anchor);
    setActive(null);
    window.getSelection()?.removeAllRanges();
    setPending(null);
    window.setTimeout(() => document.getElementById("kb-nuovo-thread")?.scrollIntoView({ block: "nearest", behavior: "smooth" }), 50);
  }

  const panel = showPanel ? (
    <ThreadsPanel
      pageKey={pageKey}
      list={list}
      open={open}
      resolved={resolved}
      showResolved={showResolved}
      onShowResolved={setShowResolved}
      active={active}
      located={ranges}
      onPick={goTo}
      draft={draft}
      onDraftDone={() => setDraft(null)}
      onChange={(l) => {
        qc.setQueryData(["kb", "threads", pageKey], l);
        qc.invalidateQueries({ queryKey: ["kb", "home"] });
      }}
    />
  ) : null;

  return (
    <div className={showPanel ? "grid min-w-0 grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_280px]" : "min-w-0"}>
      <div className="flex min-w-0 flex-col gap-2">
        {canComment && threads.length === 0 && !draft && (
          <p className="m-0 text-[12px] text-muted">{tr("knowledge.threads.hint")}</p>
        )}
        <div ref={box} onClick={onBoxClick}>
          {children}
        </div>
      </div>
      {panel}
      {pending && canComment && !draft && (
        <button
          type="button"
          className={`btn btn-primary fixed z-40 shadow-none ${narrow ? "inset-x-4 bottom-4 justify-center" : ""}`}
          style={narrow ? undefined : { left: Math.max(8, Math.min(pending.x - 60, window.innerWidth - 150)), top: Math.min(pending.y + 8, window.innerHeight - 52) }}
          onMouseDown={(e) => e.preventDefault()}
          onClick={startDraft}
        >
          {tr("knowledge.threads.commentSelection")}
        </button>
      )}
    </div>
  );
}

function ThreadsPanel(props: {
  pageKey: string;
  list: ThreadList | undefined;
  open: Thread[];
  resolved: Thread[];
  showResolved: boolean;
  onShowResolved: (v: boolean) => void;
  active: string | null;
  located: Map<string, Range>;
  onPick: (id: string) => void;
  draft: ThreadDraft | null;
  onDraftDone: () => void;
  onChange: (l: ThreadList) => void;
}) {
  const { list, open, resolved } = props;
  const tr = useT();
  return (
    <aside aria-label={tr("knowledge.threads.title")} className="flex min-w-0 flex-col gap-3 xl:sticky xl:top-4 xl:max-h-[calc(100dvh-2rem)] xl:self-start xl:overflow-y-auto">
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{open.length ? tr("knowledge.threads.openCount", { count: open.length }) : ""}</span>}>
        {tr("knowledge.threads.title")}
      </SectionTitle>
      {props.draft && <NewThread pageKey={props.pageKey} draft={props.draft} onDone={props.onDraftDone} onChange={props.onChange} />}
      {open.length === 0 && !props.draft && <p className="m-0 text-[12.5px] text-muted">{tr("knowledge.threads.none")}</p>}
      <ol className="m-0 flex list-none flex-col gap-3 p-0">
        {open.map((t) => (
          <ThreadCard key={t.id} t={t} list={list} active={props.active === t.id} found={props.located.has(t.id)} onPick={props.onPick} onChange={props.onChange} />
        ))}
      </ol>
      {resolved.length > 0 && (
        <details open={props.showResolved} onToggle={(e) => props.onShowResolved((e.currentTarget as HTMLDetailsElement).open)}>
          <summary className="label cursor-pointer text-muted">{tr("knowledge.threads.resolvedCount", { count: resolved.length })}</summary>
          <ol className="m-0 mt-2 flex list-none flex-col gap-3 p-0">
            {resolved.map((t) => (
              <ThreadCard key={t.id} t={t} list={list} active={false} found={false} onPick={props.onPick} onChange={props.onChange} />
            ))}
          </ol>
        </details>
      )}
    </aside>
  );
}

function Quote({ text, onClick, missing }: { text: string; onClick?: () => void; missing?: boolean }) {
  const tr = useT();
  const short = text.length > 140 ? text.slice(0, 139) + "…" : text;
  const inner = (
    <>
      <span aria-hidden="true">{tr("knowledge.threads.quoteOpen")}</span>
      {short}
      <span aria-hidden="true">{tr("knowledge.threads.quoteClose")}</span>
    </>
  );
  return (
    <div className={`border-l-2 pl-2 text-[12.5px] ${missing ? "border-flag text-muted line-through decoration-hair" : "border-hair text-muted"}`}>
      {onClick ? (
        <button type="button" className="text-left text-inherit hover:text-accent" onClick={onClick}>
          {inner}
        </button>
      ) : (
        inner
      )}
    </div>
  );
}

function NewThread({ pageKey, draft, onDone, onChange }: { pageKey: string; draft: ThreadDraft; onDone: () => void; onChange: (l: ThreadList) => void }) {
  const tr = useT();
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000 });
  const [text, setText] = useState("");
  const send = useMutation({
    mutationFn: () => kb.createThread(pageKey, { ...draft, body: text }),
    onSuccess: (l) => {
      onChange(l);
      onDone();
    },
  });
  const submit = () => text.trim() && !send.isPending && send.mutate();
  return (
    <form
      id="kb-nuovo-thread"
      className="flex flex-col gap-2 border-l-2 border-accent pl-3"
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <Quote text={draft.quote} />
      <label className="sr-only" htmlFor="kb-nuovo-commento">{tr("knowledge.threads.commentLabel")}</label>
      <MentionBox id="kb-nuovo-commento" value={text} onChange={setText} onSubmit={submit} members={members.data?.items ?? []} autoFocus placeholder={tr("knowledge.threads.commentPlaceholder")} />
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={send.isPending || !text.trim()}>{send.isPending ? tr("knowledge.threads.sending") : tr("knowledge.threads.comment")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("knowledge.threads.cancel")}</button>
      </div>
      <ErrorNote error={send.error} />
    </form>
  );
}

function ThreadCard({ t, list, active, found, onPick, onChange }: { t: Thread; list: ThreadList | undefined; active: boolean; found: boolean; onPick: (id: string) => void; onChange: (l: ThreadList) => void }) {
  const tr = useT();
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000, enabled: !!list?.can_comment });
  const [reply, setReply] = useState<string | null>(null);
  const resolve = useMutation({ mutationFn: () => (t.status === "open" ? kb.resolve(t.id) : kb.reopen(t.id)), onSuccess: onChange });
  const send = useMutation({
    mutationFn: () => kb.reply(t.id, reply ?? ""),
    onSuccess: (l) => {
      setReply(null);
      onChange(l);
    },
  });
  const del = useMutation({ mutationFn: (cid: string) => kb.deleteThreadComment(t.id, cid), onSuccess: onChange });
  const gone = t.status === "open" && (t.orphan || !found);
  const mem = useMemo(() => members.data?.items ?? [], [members.data]);
  return (
    <li id={`thread-${t.id}`} className={`flex flex-col gap-2 border-l-2 pl-3 ${active ? "border-accent bg-sel/40" : t.status === "resolved" ? "border-hair" : "border-flag"}`}>
      <Quote text={t.quote} missing={gone} onClick={t.status === "open" && found ? () => onPick(t.id) : undefined} />
      {gone && <p className="m-0 text-[12px] text-flag">{tr("knowledge.threads.gone")}</p>}
      <ul className="m-0 flex list-none flex-col gap-2 p-0">
        {t.comments.map((c) => (
          <li key={c.id} className="flex flex-col gap-0.5">
            <div className="flex flex-wrap items-baseline gap-x-2 text-[12px]">
              <span className="font-semibold text-[13px]">{c.author}</span>
              <span className="text-muted" title={fmtDateTime(c.created_at)}>{ago(c.created_at)}</span>
              {c.can_delete && (
                <button type="button" className="ml-auto text-muted underline hover:text-danger" disabled={del.isPending} onClick={() => confirm(tr("knowledge.threads.deleteConfirm")) && del.mutate(c.id)}>
                  {tr("knowledge.threads.delete")}
                </button>
              )}
            </div>
            <div className="text-[13.5px]"><Markdown text={c.body} mentions={c.mentions} /></div>
          </li>
        ))}
      </ul>
      {t.status === "resolved" && (
        <p className="m-0 text-[12px] text-muted">{tr("knowledge.threads.resolved")}{t.resolved_by ? ` ${tr("knowledge.threads.by", { name: t.resolved_by })}` : ""}{t.resolved_at ? ` · ${ago(t.resolved_at)}` : ""}</p>
      )}
      {reply !== null && list?.can_comment && (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (reply.trim() && !send.isPending) send.mutate();
          }}
        >
          <MentionBox value={reply} onChange={setReply} onSubmit={() => reply.trim() && send.mutate()} members={mem} autoFocus placeholder={tr("knowledge.threads.replyPlaceholder")} />
          <div className="flex gap-2">
            <button className="btn btn-primary" disabled={send.isPending || !reply.trim()}>{tr("knowledge.threads.reply")}</button>
            <button type="button" className="btn" onClick={() => setReply(null)}>{tr("knowledge.threads.cancel")}</button>
          </div>
        </form>
      )}
      <div className="flex flex-wrap gap-3 text-[12.5px]">
        {reply === null && list?.can_comment && (
          <button type="button" className="text-accent underline" onClick={() => setReply("")}>{tr("knowledge.threads.reply")}</button>
        )}
        {list?.can_resolve && (
          <button type="button" className="text-accent underline" disabled={resolve.isPending} onClick={() => resolve.mutate()}>
            {t.status === "open" ? tr("knowledge.threads.resolve") : tr("knowledge.threads.reopen")}
          </button>
        )}
      </div>
      <ErrorNote error={resolve.error ?? send.error ?? del.error} />
    </li>
  );
}
