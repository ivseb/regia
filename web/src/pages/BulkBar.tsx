// Comandi in blocco (piano, passo 18; replica/work.md «Tempo e team»): selezione multipla con
// Maiusc e Ctrl/Cmd, una barra di comandi scritti («assegna @anna fase In corso sprint 12») con
// completamento e i pulsanti guidati per chi preferisce scegliere. Ogni comando passa da
// un'anteprima: cosa cambia per ogni elemento e, per quelli bloccati (gate, ruoli), il
// motivo. L'ultimo comando si annulla. Componente riusabile: basta una lista di id.
import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMe } from "../shell";
import { ErrorNote } from "../ui";
import { Dialog } from "./work-ui";
import { applySuggestion, parseCommand, suggest } from "../bulkParse";
import { tempoApi, type BulkCommand, type BulkContext, type BulkResult } from "../tempoApi";
import { useT } from "../i18n/react";

type T = ReturnType<typeof useT>;

// ---------------------------------------------------------------------
// Selezione

/** Selezione di elementi per id: clic singolo, Maiusc per un intervallo, Ctrl/Cmd per aggiungere. */
export function useSelection(order: string[]) {
  const [sel, setSel] = useState<Set<string>>(new Set());
  const anchor = useRef<string | null>(null);
  const orderRef = useRef(order);
  orderRef.current = order;
  const toggle = useCallback((id: string, e?: { shiftKey?: boolean }) => {
    setSel((cur) => {
      const next = new Set(cur);
      if (e?.shiftKey && anchor.current && anchor.current !== id) {
        const o = orderRef.current;
        const a = o.indexOf(anchor.current);
        const b = o.indexOf(id);
        if (a >= 0 && b >= 0) {
          const [lo, hi] = a < b ? [a, b] : [b, a];
          for (let i = lo; i <= hi; i++) next.add(o[i]);
          return next;
        }
      }
      if (next.has(id)) next.delete(id);
      else next.add(id);
      anchor.current = id;
      return next;
    });
  }, []);
  const set = useCallback((ids: string[]) => setSel(new Set(ids)), []);
  const clear = useCallback(() => {
    setSel(new Set());
    anchor.current = null;
  }, []);
  // Gli elementi che spariscono dalla lista (filtro, spostamenti) escono dalla selezione.
  useEffect(() => {
    setSel((cur) => {
      if (cur.size === 0) return cur;
      const have = new Set(order);
      const next = new Set([...cur].filter((x) => have.has(x)));
      return next.size === cur.size ? cur : next;
    });
  }, [order]);
  return { sel, toggle, set, clear, size: sel.size };
}

/** Casella di selezione con l'intervallo da tastiera e Maiusc. */
export function SelectBox({ id, checked, label, onToggle, className = "" }: { id: string; checked: boolean; label: string; onToggle: (id: string, e: { shiftKey: boolean }) => void; className?: string }) {
  return (
    <input
      type="checkbox"
      aria-label={label}
      checked={checked}
      onChange={() => undefined}
      onClick={(e: MouseEvent) => {
        e.stopPropagation();
        onToggle(id, { shiftKey: e.shiftKey });
      }}
      className={`size-4 shrink-0 cursor-pointer accent-[var(--accent)] ${className}`}
    />
  );
}

// ---------------------------------------------------------------------
// Barra

type Done = { text: string; blocked: number } | null;

/**
 * `requests`: la selezione è di richieste (lista richieste, ondata 2): fasi e campi sono quelli del tipo richiesta,
 * non ci sono sprint né eliminazione, e c'è il pulsante «Prendi in carico» (assegna a me, con anteprima).
 */
export function BulkBar({ ids, space, requests, onClear, onDone }: { ids: string[]; space?: string; requests?: boolean; onClear: () => void; onDone?: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const me = useMe();
  const ctxQ = useQuery({ queryKey: ["work", "bulkctx", space ?? "", !!requests], queryFn: () => tempoApi.bulkContext(space, requests), staleTime: 60_000, enabled: ids.length > 0 });
  const last = useQuery({ queryKey: ["work", "bulklast"], queryFn: tempoApi.lastBulk, staleTime: 15_000 });
  const [text, setText] = useState("");
  const [pick, setPick] = useState<null | "phase" | "assign" | "sprint" | "labels" | "field">(null);
  const [preview, setPreview] = useState<{ commands: BulkCommand[]; text: string } | null>(null);
  const [done, setDone] = useState<Done>(null);
  const [active, setActive] = useState(-1);
  const [focused, setFocused] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const ctx = ctxQ.data;
  const meId = me.data?.active_tenant?.member_id;

  const parsed = useMemo(() => (ctx && text.trim() ? parseCommand(text, ctx, meId) : { commands: [], errors: [] }), [ctx, text, meId]);
  const sugg = useMemo(() => (ctx ? suggest(text, ctx) : []), [ctx, text]);
  useEffect(() => setActive(-1), [text]);
  useEffect(() => {
    if (!done) return;
    const timer = setTimeout(() => setDone(null), 12_000);
    return () => clearTimeout(timer);
  }, [done]);

  const undo = useMutation({
    mutationFn: tempoApi.undoBulk,
    onSuccess: (r) => {
      setDone({
        text: r.skipped.length
          ? t("lavoro.bulkbar.undoneSkipped", { summary: r.summary, count: r.skipped.length })
          : t("lavoro.bulkbar.undone", { summary: r.summary }),
        blocked: 0,
      });
      qc.invalidateQueries({ queryKey: ["work"] });
      onDone?.();
    },
  });

  const run = (commands: BulkCommand[], raw: string) => setPreview({ commands, text: raw });
  const submit = () => {
    if (active >= 0 && sugg[active]) {
      setText(applySuggestion(text, sugg[active]));
      input.current?.focus();
      return;
    }
    if (!text.trim() || parsed.errors.length || parsed.commands.length === 0) return;
    run(parsed.commands, text.trim());
  };

  const showBar = ids.length > 0;
  const lastCmd = last.data?.last;
  if (!showBar && !lastCmd && !done) return null;
  return (
    <>
      <div
        role="region"
        aria-label={t("lavoro.bulkbar.region")}
        className="fixed inset-x-0 bottom-0 z-40 border-t-2 border-rule bg-bg px-3 py-2.5 shadow-[0_-1px_0_var(--hair)] md:left-auto md:right-4 md:bottom-4 md:w-[min(920px,calc(100vw-280px))] md:border-2"
      >
        {done && (
          <p role="status" className="m-0 mb-2 flex flex-wrap items-center gap-x-3 text-[13px]">
            <span className="min-w-0 flex-1">
              {done.text}
              {done.blocked > 0 && <span className="text-flag"> · {t("lavoro.bulkbar.blockedUntouched", { count: done.blocked })}</span>}
            </span>
            <button className="text-[12px] text-muted underline" onClick={() => setDone(null)}>
              {t("lavoro.bulkbar.close")}
            </button>
          </p>
        )}
        {showBar ? (
          <div className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <strong className="font-mono text-[13px] tabular">{t("lavoro.bulkbar.selected", { count: ids.length })}</strong>
              <span className="hidden text-[12px] text-muted md:inline">{t("lavoro.bulkbar.selectHint")}</span>
              <span className="ml-auto flex items-center gap-3">
                {lastCmd && (
                  <button className="text-[12.5px] text-accent underline disabled:opacity-50" disabled={undo.isPending} title={lastCmd.summary} onClick={() => undo.mutate()}>
                    {t("lavoro.bulkbar.undoLast")}
                  </button>
                )}
                <button className="text-[12.5px] text-muted underline hover:text-fg" onClick={onClear}>
                  {t("lavoro.bulkbar.deselect")}
                </button>
              </span>
            </div>
            <div className="relative">
              <input
                ref={input}
                className={`field font-mono text-[13.5px] ${parsed.errors.length && text.trim() ? "border-danger" : ""}`}
                // i18n-ok: esempio della sintassi dei comandi, che è italiana (bulkParse.ts)
                placeholder="assegna @anna fase In corso sprint 12 etichetta +urgente"
                aria-label={t("lavoro.bulkbar.command")}
                aria-autocomplete="list"
                aria-expanded={focused && sugg.length > 0}
                aria-controls="bulk-sugg"
                role="combobox"
                autoComplete="off"
                spellCheck={false}
                value={text}
                onFocus={() => setFocused(true)}
                onBlur={() => setFocused(false)}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "ArrowDown" && sugg.length) {
                    e.preventDefault();
                    setActive((a) => (a + 1) % sugg.length);
                  } else if (e.key === "ArrowUp" && sugg.length) {
                    e.preventDefault();
                    setActive((a) => (a <= 0 ? sugg.length - 1 : a - 1));
                  } else if (e.key === "Tab" && sugg.length && !e.shiftKey) {
                    e.preventDefault();
                    setText(applySuggestion(text, sugg[Math.max(active, 0)]));
                  } else if (e.key === "Enter") {
                    e.preventDefault();
                    submit();
                  } else if (e.key === "Escape") {
                    input.current?.blur();
                  }
                }}
              />
              {focused && sugg.length > 0 && (
                <ul id="bulk-sugg" role="listbox" className="absolute right-0 bottom-full left-0 z-50 m-0 mb-1 flex max-h-[220px] list-none flex-col overflow-y-auto border-2 border-rule bg-bg p-0">
                  {sugg.map((s, i) => (
                    <li key={s.label + s.insert + i} role="option" aria-selected={i === active}>
                      <button
                        type="button"
                        className={`flex min-h-[40px] w-full items-baseline gap-3 px-3 py-1.5 text-left ${i === active ? "bg-sel" : "hover:bg-sel/60"}`}
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={() => {
                          setText(applySuggestion(text, s));
                          input.current?.focus();
                        }}
                      >
                        <span className="font-mono text-[13px]">{s.label}</span>
                        {s.hint && <span className="text-[11.5px] text-muted">{s.hint}</span>}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
            {text.trim() && parsed.errors.length > 0 && (
              <p role="alert" className="m-0 text-[12.5px] text-danger">
                {parsed.errors[0]}
              </p>
            )}
            {text.trim() && parsed.errors.length === 0 && parsed.commands.length > 0 && (
              <p className="m-0 flex flex-wrap items-center gap-2 text-[12.5px] text-muted">
                {parsed.commands.map((c, i) => (
                  <span key={i} className="border border-hair px-1.5 py-0.5 text-fg">
                    {describe(t, c)}
                  </span>
                ))}
                <button className="btn btn-primary ml-auto px-3 py-1" onClick={submit}>
                  {t("lavoro.bulkbar.preview")}
                </button>
              </p>
            )}
            <div className="flex flex-wrap gap-1.5" role="group" aria-label={t("lavoro.bulkbar.guided")}>
              {requests && me.data && (
                <button
                  className="btn btn-primary px-2.5 py-1 text-[12.5px]"
                  onClick={() => {
                    const c: BulkCommand = { kind: "assign", value: meId, label: me.data!.display_name };
                    run([c], describe(t, c));
                  }}
                >
                  {t("dafare.req.takeMine")}
                </button>
              )}
              {(
                [
                  ["phase", t("lavoro.bulkbar.pickPhase")],
                  ["assign", t("lavoro.bulkbar.pickAssign")],
                  ...(requests ? [] : ([["sprint", t("lavoro.bulkbar.pickSprint")]] as const)),
                  ["labels", t("lavoro.bulkbar.pickLabels")],
                  ["field", t("lavoro.bulkbar.pickField")],
                ] as const
              ).map(([k, l]) => (
                <button key={k} className="btn px-2.5 py-1 text-[12.5px]" disabled={!ctx} onClick={() => setPick(k)}>
                  {l}
                </button>
              ))}
              {!requests && (
                <button className="btn btn-danger px-2.5 py-1 text-[12.5px]" onClick={() => run([{ kind: "delete" }], describe(t, { kind: "delete" }))}>
                  {t("lavoro.bulkbar.delete")}
                </button>
              )}
            </div>
          </div>
        ) : (
          lastCmd && (
            <p className="m-0 flex flex-wrap items-center gap-3 text-[13px]">
              <span className="min-w-0 flex-1 break-words text-muted">{t("lavoro.bulkbar.lastCommand", { summary: lastCmd.summary })}</span>
              <button className="text-accent underline disabled:opacity-50" disabled={undo.isPending} onClick={() => undo.mutate()}>
                {t("lavoro.bulkbar.undo")}
              </button>
            </p>
          )
        )}
        {undo.error && <ErrorNote error={undo.error} />}
      </div>
      {pick && ctx && (
        <PickDialog
          kind={pick}
          ctx={ctx}
          onClose={() => setPick(null)}
          onPick={(c) => {
            setPick(null);
            run(c, c.map((x) => describe(t, x)).join(", "));
          }}
        />
      )}
      {preview && (
        <PreviewDialog
          ids={ids}
          commands={preview.commands}
          text={preview.text}
          onClose={() => setPreview(null)}
          onApplied={(r) => {
            setPreview(null);
            setText("");
            setDone({ text: t("lavoro.bulkbar.done", { summary: r.summary }), blocked: r.blocked });
            qc.invalidateQueries({ queryKey: ["work"] });
            qc.invalidateQueries({ queryKey: ["elements"] });
            onDone?.();
            if (r.blocked === 0) onClear();
          }}
        />
      )}
    </>
  );
}

function describe(t: T, c: BulkCommand): string {
  switch (c.kind) {
    case "phase":
      return t("lavoro.bulkbar.dPhase", { name: c.label ?? c.value ?? "" });
    case "assign":
      return c.value ? t("lavoro.bulkbar.dAssign", { name: c.label ?? c.value }) : t("lavoro.bulkbar.dUnassign");
    case "sprint":
      return c.value ? t("lavoro.bulkbar.dSprint", { name: c.label ?? c.value }) : t("lavoro.bulkbar.dBacklog");
    case "labels":
      return t("lavoro.bulkbar.dLabels", { labels: [...(c.add ?? []).map((a) => "+" + a), ...(c.remove ?? []).map((r) => "−" + r)].join(" ") });
    case "field":
      return c.raw == null || c.raw === "" ? t("lavoro.bulkbar.dClear", { name: c.label ?? c.field ?? "" }) : `${c.label ?? c.field}: ${String(c.raw)}`;
    case "delete":
      return t("lavoro.bulkbar.dDelete");
  }
}

// ---------------------------------------------------------------------
// Scelte guidate

function PickDialog({ kind, ctx, onClose, onPick }: { kind: "phase" | "assign" | "sprint" | "labels" | "field"; ctx: BulkContext; onClose: () => void; onPick: (c: BulkCommand[]) => void }) {
  const t = useT();
  const [q, setQ] = useState("");
  const [field, setField] = useState<BulkContext["fields"][number] | null>(null);
  const [add, setAdd] = useState("");
  const [rem, setRem] = useState("");
  const [value, setValue] = useState("");
  const match = (s: string) => !q.trim() || s.toLowerCase().includes(q.trim().toLowerCase());
  const row = (key: string, label: string, hint: string | undefined, run: () => void) => (
    <li key={key}>
      <button className="flex min-h-[44px] w-full items-baseline gap-3 border-b border-hair px-2 py-1.5 text-left hover:bg-sel" onClick={run}>
        <span>{label}</span>
        {hint && <span className="font-mono text-[11.5px] text-muted">{hint}</span>}
      </button>
    </li>
  );
  const list = (children: React.ReactNode) => <ul className="m-0 flex max-h-[50dvh] list-none flex-col overflow-y-auto p-0">{children}</ul>;
  const search = (
    <input className="field" autoFocus placeholder={t("lavoro.bulkbar.search")} aria-label={t("lavoro.bulkbar.searchLabel")} value={q} onChange={(e) => setQ(e.target.value)} />
  );
  if (kind === "phase")
    return (
      <Dialog title={t("lavoro.bulkbar.pickPhase")} onClose={onClose}>
        {list(ctx.phases.map((p) => row(p, p, undefined, () => onPick([{ kind: "phase", value: p, label: p }]))))}
        <p className="m-0 text-[12px] text-muted">{t("lavoro.bulkbar.phaseHelp")}</p>
      </Dialog>
    );
  if (kind === "assign")
    return (
      <Dialog title={t("lavoro.bulkbar.assignTo")} onClose={onClose}>
        {search}
        {list(
          <>
            {match(t("lavoro.bulkbar.noneLower")) && row("none", t("lavoro.bulkbar.none"), t("lavoro.bulkbar.noneHint"), () => onPick([{ kind: "assign", value: "", label: t("lavoro.bulkbar.noneLower") }]))}
            {ctx.members.filter((m) => match(m.name)).map((m) => row(m.id, m.name, undefined, () => onPick([{ kind: "assign", value: m.id, label: m.name }])))}
          </>,
        )}
      </Dialog>
    );
  if (kind === "sprint")
    return (
      <Dialog title={t("lavoro.bulkbar.moveToSprint")} onClose={onClose}>
        {list(
          <>
            {row("backlog", t("lavoro.bulkbar.backlog"), t("lavoro.bulkbar.backlogHint"), () => onPick([{ kind: "sprint", value: "", label: t("lavoro.bulkbar.backlogLower") }]))}
            {ctx.sprints.map((s) => row(s.id, s.title, `${s.key} · ${s.state.toLowerCase()}`, () => onPick([{ kind: "sprint", value: s.key, label: s.title }])))}
          </>,
        )}
        {ctx.sprints.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("lavoro.bulkbar.noSprint")}</p>}
      </Dialog>
    );
  if (kind === "labels")
    return (
      <Dialog title={t("lavoro.bulkbar.pickLabels")} onClose={onClose}>
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            const a = add.split(/[,\s]+/).filter(Boolean);
            const r = rem.split(/[,\s]+/).filter(Boolean);
            if (a.length + r.length) onPick([{ kind: "labels", add: a, remove: r }]);
          }}
        >
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{t("lavoro.bulkbar.add")}</span>
            <input className="field" autoFocus placeholder={t("lavoro.bulkbar.addPlaceholder")} value={add} onChange={(e) => setAdd(e.target.value)} list="bulk-labels" />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{t("lavoro.bulkbar.remove")}</span>
            <input className="field" value={rem} onChange={(e) => setRem(e.target.value)} list="bulk-labels" />
          </label>
          <datalist id="bulk-labels">
            {ctx.labels.map((l) => (
              <option key={l} value={l} />
            ))}
          </datalist>
          <p className="m-0 text-[12px] text-muted">{t("lavoro.bulkbar.labelsHelp")}</p>
          <button className="btn btn-primary self-start" disabled={!add.trim() && !rem.trim()}>
            {t("lavoro.bulkbar.preview")}
          </button>
        </form>
      </Dialog>
    );
  // Campo
  if (!field)
    return (
      <Dialog title={t("lavoro.bulkbar.setField")} onClose={onClose}>
        {search}
        {list(ctx.fields.filter((f) => match(f.name)).map((f) => row(f.key, f.name, f.data_type, () => setField(f))))}
      </Dialog>
    );
  const opts = field.options ?? [];
  return (
    <Dialog title={field.name} onClose={onClose}>
      {opts.length > 0 ? (
        list(
          <>
            {opts.map((o) => row(o.id, o.name, undefined, () => onPick([{ kind: "field", field: field.key, raw: o.name, label: field.name }])))}
            {row("empty", t("lavoro.bulkbar.clearField"), undefined, () => onPick([{ kind: "field", field: field.key, raw: null, label: field.name }]))}
          </>,
        )
      ) : field.data_type === "member" ? (
        list(ctx.members.map((m) => row(m.id, m.name, undefined, () => onPick([{ kind: "field", field: field.key, raw: m.id, label: field.name }]))))
      ) : (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            onPick([{ kind: "field", field: field.key, raw: value.trim() === "" ? null : field.data_type === "boolean" ? ["sì", "si", "yes", "y", "true", "1"].includes(value.trim().toLowerCase()) : value.trim(), label: field.name }]);
          }}
        >
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{t("lavoro.bulkbar.value", { hint: field.data_type === "date" ? t("lavoro.bulkbar.hintDate") : field.data_type === "duration" ? t("lavoro.bulkbar.hintDuration") : field.data_type === "boolean" ? t("lavoro.bulkbar.hintBoolean") : t("lavoro.bulkbar.hintEmpty") })}</span>
            <input className="field" autoFocus value={value} onChange={(e) => setValue(e.target.value)} />
          </label>
          <button className="btn btn-primary self-start">{t("lavoro.bulkbar.preview")}</button>
        </form>
      )}
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// Anteprima ed esecuzione

function PreviewDialog({ ids, commands, text, onClose, onApplied }: { ids: string[]; commands: BulkCommand[]; text: string; onClose: () => void; onApplied: (r: BulkResult) => void }) {
  const t = useT();
  const [cmds, setCmds] = useState(commands);
  const [allOrNothing, setAllOrNothing] = useState(false);
  const [comment, setComment] = useState("");
  const [showAll, setShowAll] = useState(false);
  const pv = useQuery({
    queryKey: ["work", "bulkpreview", ids, cmds],
    queryFn: () => tempoApi.bulk(ids, cmds, { dry_run: true, text }),
    staleTime: 0,
    gcTime: 0,
    retry: false,
  });
  const apply = useMutation({ mutationFn: () => tempoApi.bulk(ids, cmds, { all_or_nothing: allOrNothing, text }), onSuccess: onApplied });
  const r = pv.data;
  const destructive = cmds.some((c) => c.kind === "delete");
  const needsComment = !!r?.items.some((i) => i.status === "blocked" && /motivazione/i.test(i.reason ?? ""));
  const withComment = () => setCmds((cs) => cs.map((c) => (c.kind === "phase" ? { ...c, comment } : c)));
  const blocked = r?.items.filter((i) => i.status === "blocked") ?? [];
  const okItems = r?.items.filter((i) => i.status === "ok") ?? [];
  const same = r?.items.filter((i) => i.status === "unchanged") ?? [];
  return (
    <Dialog title={t("lavoro.bulkbar.previewTitle")} onClose={onClose} wide>
      <p className="m-0 font-mono text-[12.5px] text-muted">{cmds.map((x) => describe(t, x)).join(" · ")}</p>
      {pv.isPending && <p className="m-0 text-muted">{t("lavoro.bulkbar.checking")}</p>}
      <ErrorNote error={pv.error ?? apply.error} />
      {r && (
        <>
          <p className="m-0 flex flex-wrap gap-x-4 gap-y-1 text-[13px]">
            <strong className="text-ok">{t("lavoro.bulkbar.toChange", { count: r.applied })}</strong>
            {r.blocked > 0 && <strong className="text-flag">{t("lavoro.bulkbar.blocked", { count: r.blocked })}</strong>}
            {r.unchanged > 0 && <span className="text-muted">{t("lavoro.bulkbar.unchanged", { count: r.unchanged })}</span>}
          </p>
          {blocked.length > 0 && (
            <section aria-label={t("lavoro.bulkbar.blockedLabel")} className="flex flex-col gap-0.5 border-2 border-flag p-2">
              <h3 className="m-0 text-[12.5px] font-bold text-flag">{t("lavoro.bulkbar.stayAsIs")}</h3>
              <ul className="m-0 flex max-h-[24dvh] list-none flex-col overflow-y-auto p-0">
                {blocked.map((i) => (
                  <li key={i.id} className="flex flex-col gap-0.5 border-b border-hair py-1.5 last:border-b-0">
                    <span className="flex gap-2">
                      <span className="font-mono text-[12px] text-accent">{i.key}</span>
                      <span className="min-w-0 break-words">{i.title}</span>
                    </span>
                    <span className="text-[12.5px] text-muted">{i.reason}</span>
                  </li>
                ))}
              </ul>
            </section>
          )}
          {needsComment && (
            <div className="flex flex-col gap-1">
              <label className="flex flex-col gap-1">
                <span className="label text-muted">{t("lavoro.bulkbar.reasonNeeded")}</span>
                <input className="field" value={comment} onChange={(e) => setComment(e.target.value)} />
              </label>
              <button className="btn self-start" disabled={!comment.trim()} onClick={withComment}>
                {t("lavoro.bulkbar.recalc")}
              </button>
            </div>
          )}
          {okItems.length > 0 && (
            <section aria-label={t("lavoro.bulkbar.changesLabel")} className="flex flex-col gap-0.5">
              <h3 className="label m-0 text-muted">{t("lavoro.bulkbar.whatChanges")}</h3>
              <ul className="m-0 flex max-h-[26dvh] list-none flex-col overflow-y-auto p-0">
                {(showAll ? okItems : okItems.slice(0, 8)).map((i) => (
                  <li key={i.id} className="flex flex-col gap-0.5 border-b border-hair py-1.5">
                    <span className="flex gap-2">
                      <span className="font-mono text-[12px] text-accent">{i.key}</span>
                      <span className="min-w-0 break-words">{i.title}</span>
                    </span>
                    <span className="pl-1 text-[12px] text-muted">{i.changes?.join(" · ")}</span>
                  </li>
                ))}
              </ul>
              {okItems.length > 8 && (
                <button className="self-start text-[12.5px] text-accent underline" onClick={() => setShowAll(!showAll)}>
                  {showAll ? t("lavoro.bulkbar.less") : t("lavoro.bulkbar.showAll", { count: okItems.length })}
                </button>
              )}
            </section>
          )}
          {r.blocked > 0 && r.applied > 0 && (
            <label className="flex items-center gap-2 text-[13px]">
              <input type="checkbox" checked={allOrNothing} onChange={(e) => setAllOrNothing(e.target.checked)} />
              {t("lavoro.bulkbar.allOrNothing")}
            </label>
          )}
          {same.length > 0 && r.applied === 0 && r.blocked === 0 && <p className="m-0 text-muted">{t("lavoro.bulkbar.nothingToChange")}</p>}
          <div className="flex flex-wrap items-center gap-3">
            <button className={`btn ${destructive ? "btn-danger" : "btn-primary"}`} disabled={apply.isPending || r.applied === 0} onClick={() => apply.mutate()}>
              {apply.isPending ? t("lavoro.bulkbar.applying") : destructive ? t("lavoro.bulkbar.deleteN", { count: r.applied }) : t("lavoro.bulkbar.applyTo", { count: r.applied })}
            </button>
            <button className="btn" onClick={onClose}>
              {t("lavoro.bulkbar.cancel")}
            </button>
            <span className="text-[12px] text-muted">{t("lavoro.bulkbar.canUndo")}</span>
          </div>
        </>
      )}
    </Dialog>
  );
}
