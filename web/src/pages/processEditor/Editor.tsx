import { EntitySelect, Select } from "../../select";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
  ApiError,
  processEditorApi,
  type ActionSchema,
  type Definition,
  type Problem,
  type ProcessInfo,
  type ProcessMeta,
  type RuleAction,
} from "../../api";
import { ErrorNote, SectionTitle } from "../../ui";
import { ConditionBuilder } from "./ConditionBuilder";
import { RuleSentence, ruleSentence, useRuleRefs } from "./RuleSentence";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { AddStep } from "./AddStep";
import { Diagram, type Selection } from "./Diagram";
import {
  ANY,
  addStep,
  layout,
  type StepInput,
  CATEGORIES,
  categoryName,
  clean,
  describeCondition,
  describeDiff,
  humanFact,
  freePhaseName,
  keyFrom,
  movePhase,
  normalize,
  problemTarget,
  removePhase,
  renamePhase,
  unreachable,
} from "./model";
import { useT } from "../../i18n/react";
import { tr } from "../../i18n/core";
import { fmtTime } from "../../i18n/format";

// Editor visuale della bozza di un processo (sdlc.md §10, "Editor visuale dei processi").
// Lavora su una copia della definizione: ogni modifica si valida dal vivo con
// POST /processes/{ref}/check e si salva da sola nella bozza dopo una breve pausa.

type Change = (fn: (d: Definition) => void, coalesce?: string) => void;

const PHASE_COLORS = ["#6B7280", "#6B4FC8", "#0B6E8E", "#3550C8", "#9A5200", "#0B7A5E", "#3A7A1C", "#1F2937", "#B91C1C"];
const MIN_ZOOM = 0.35;
const MAX_ZOOM = 2;
const outcomeName = (k: string) => (k === "approve" || k === "reject" || k === "defer" ? tr(`configurazione.processEditor.outcome.${k}`) : k);
const eq = (a: string, b: string) => a.toLowerCase() === b.toLowerCase();
/** Chiave del dato → nome leggibile, per scrivere le condizioni in parole. */
const factNames = (facts: { key: string; name: string }[]) => Object.fromEntries(facts.map((f) => [f.key, f.name]));

function useNarrow() {
  const q = "(max-width: 767px)";
  const [narrow, setNarrow] = useState(() => typeof matchMedia !== "undefined" && matchMedia(q).matches);
  useEffect(() => {
    const m = matchMedia(q);
    const f = () => setNarrow(m.matches);
    m.addEventListener("change", f);
    return () => m.removeEventListener("change", f);
  }, []);
  return narrow;
}

type Grouped = {
  phase: Map<number, Problem[]>;
  transition: Map<number, Problem[]>;
  gate: Map<number, Problem[]>;
  rule: Map<number, Problem[]>;
  byPhaseName: Map<string, Problem[]>;
  general: Problem[];
};

function group(problems: Problem[], def: Definition): Grouped {
  const g: Grouped = { phase: new Map(), transition: new Map(), gate: new Map(), rule: new Map(), byPhaseName: new Map(), general: [] };
  function push<K>(m: Map<K, Problem[]>, k: K, p: Problem) {
    m.set(k, [...(m.get(k) ?? []), p]);
  }
  for (const p of problems) {
    const t = problemTarget(p.path);
    if (t.kind === "phase" || t.kind === "transition" || t.kind === "gate" || t.kind === "rule") push(g[t.kind], t.index, p);
    else if (t.kind === "phases") {
      // Avvisi sulle fasi senza indice (es. irraggiungibile): la fase è nel messaggio.
      const name = /«([^»]+)»|"([^"]+)"/.exec(p.message);
      const ph = name && def.phases.find((x) => eq(x.name, name[1] ?? name[2]));
      if (ph) push(g.byPhaseName, ph.name, p);
      else g.general.push(p);
    } else g.general.push(p);
  }
  for (const [i, ps] of g.phase) {
    const n = def.phases[i]?.name;
    if (n) g.byPhaseName.set(n, [...(g.byPhaseName.get(n) ?? []), ...ps]);
  }
  return g;
}

export default function ProcessEditor({ proc }: { proc: ProcessInfo }) {
  const t = useT();
  const qc = useQueryClient();
  const narrow = useNarrow();
  const meta = useQuery({ queryKey: ["processMeta", proc.key], queryFn: () => processEditorApi.meta(proc.key), staleTime: 60_000 });
  const draftV = proc.versions.find((v) => v.status === "draft");
  const curV = proc.versions.find((v) => v.status === "published");
  const nextNumber = draftV?.number ?? Math.max(0, ...proc.versions.map((v) => v.number)) + 1;

  const [def, setDef] = useState<Definition>(() => normalize(proc.draft ?? proc.current));
  const [notes, setNotes] = useState(draftV?.notes ?? "");
  const [history, setHistory] = useState<Definition[]>([]);
  const [future, setFuture] = useState<Definition[]>([]);
  const [sel, setSel] = useState<Selection>(null);
  const [adding, setAdding] = useState(false);
  const [openGate, setOpenGate] = useState<number | null>(null);
  const [openRule, setOpenRule] = useState<number | null>(null);
  const [dirty, setDirty] = useState(false);
  const [autosave, setAutosave] = useState(true);
  const [saved, setSaved] = useState<Date | null>(null);
  const [published, setPublished] = useState<number | null>(null);
  const renames = useRef(new Map<string, string>()); // vecchio nome → nuovo, per le differenze
  const last = useRef<{ key: string; at: number }>({ key: "", at: 0 });
  const defRef = useRef(def);
  defRef.current = def;
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;
  const notesRef = useRef(notes);
  notesRef.current = notes;

  const change: Change = (fn, coalesce) => {
    const n = structuredClone(def);
    fn(n);
    const now = Date.now();
    // Le modifiche di fila allo stesso campo (es. mentre si scrive) sono un solo passo di "annulla".
    if (!coalesce || last.current.key !== coalesce || now - last.current.at > 1500) {
      setHistory((h) => [...h.slice(-49), def]);
    }
    last.current = { key: coalesce ?? "", at: now };
    setFuture([]);
    setDef(n);
    setDirty(true);
    setPublished(null);
  };
  const undo = () => {
    const prev = history.at(-1);
    if (!prev) return;
    setFuture((f) => [def, ...f]);
    setHistory((h) => h.slice(0, -1));
    setDef(prev);
    setDirty(true);
    last.current = { key: "", at: 0 };
  };
  const redo = () => {
    const [n, ...rest] = future;
    if (!n) return;
    setHistory((h) => [...h, def]);
    setFuture(rest);
    setDef(n);
    setDirty(true);
  };

  // Validazione dal vivo: gli stessi controlli della pubblicazione, senza salvare.
  const [check, setCheck] = useState<{ for: Definition; problems: Problem[] } | null>(null);
  const [checkError, setCheckError] = useState<unknown>(null);
  useEffect(() => {
    const snapshot = def;
    const t = setTimeout(() => {
      processEditorApi
        .check(proc.key, clean(snapshot))
        .then((r) => {
          if (defRef.current === snapshot) setCheck({ for: snapshot, problems: r.problems });
          setCheckError(null);
        })
        .catch(setCheckError);
    }, 300);
    return () => clearTimeout(t);
  }, [def, proc.key]);
  const problems = check?.problems ?? proc.draft_problems ?? [];
  const checking = check?.for !== def;
  const grouped = useMemo(() => group(problems, check?.for ?? def), [problems, check, def]);
  const errors = problems.filter((p) => p.level === "error").length;
  const lost = useMemo(() => unreachable(def), [def]);

  // Salvataggio della bozza: da solo dopo una pausa, o con il pulsante.
  const save = useMutation({
    mutationFn: async () => {
      const snapshot = defRef.current;
      const p = await api.saveDraft(proc.key, clean(snapshot), notesRef.current);
      return { p, snapshot };
    },
    onSuccess: ({ p, snapshot }) => {
      qc.setQueryData(["process", proc.key], p);
      qc.invalidateQueries({ queryKey: ["processes"] });
      if (defRef.current === snapshot) setDirty(false);
      setSaved(new Date());
    },
  });
  // Un salvataggio alla volta: finito quello in corso, si riparte se nel frattempo è cambiato qualcosa.
  useEffect(() => {
    if (!dirty || !autosave || save.isPending) return;
    const t = setTimeout(() => save.mutate(), 2000);
    return () => clearTimeout(t);
  }, [def, notes, dirty, autosave, save.isPending]);
  // Uscendo dall'editor con modifiche non salvate: si salvano comunque nella bozza.
  useEffect(
    () => () => {
      if (dirtyRef.current) {
        api.saveDraft(proc.key, clean(defRef.current), notesRef.current).then((p) => {
          qc.setQueryData(["process", proc.key], p);
          qc.invalidateQueries({ queryKey: ["processes"] });
        });
      }
    },
    [proc.key, qc],
  );
  useEffect(() => {
    if (!dirty) return;
    const f = (e: BeforeUnloadEvent) => e.preventDefault();
    addEventListener("beforeunload", f);
    return () => removeEventListener("beforeunload", f);
  }, [dirty]);

  const publish = useMutation({
    mutationFn: async () => {
      if (dirtyRef.current || !draftV) await save.mutateAsync();
      return api.publish(proc.key, notesRef.current);
    },
    onSuccess: (r) => {
      qc.setQueryData(["process", proc.key], r.process);
      qc.invalidateQueries({ queryKey: ["processes"] });
      qc.invalidateQueries({ queryKey: ["type"] });
      qc.invalidateQueries({ queryKey: ["processMeta", proc.key] });
      setPublished(r.process.versions.find((v) => v.status === "published")?.number ?? null);
      setDef(normalize(r.process.current));
      setHistory([]);
      setFuture([]);
      setDirty(false);
      setNotes("");
      renames.current.clear();
    },
  });
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const discard = useMutation({
    mutationFn: () => api.discardDraft(proc.key),
    onSuccess: async () => {
      setDirty(false);
      dirtyRef.current = false;
      const p = await qc.fetchQuery({ queryKey: ["process", proc.key], queryFn: () => api.process(proc.key) });
      setDef(normalize(p.current));
      setHistory([]);
      setFuture([]);
      setNotes("");
      setSel(null);
      setConfirmDiscard(false);
      renames.current.clear();
      qc.invalidateQueries({ queryKey: ["processes"] });
    },
  });

  // Operazioni sul diagramma.
  const addPhase = () => {
    const name = freePhaseName(def);
    change((d) => {
      const after = sel?.kind === "phase" ? d.phases.findIndex((p) => p.name === sel.name && !p.off_path) : -1;
      const main = d.phases.filter((p) => !p.off_path);
      // Senza una fase selezionata la nuova fase va prima di quella finale («Fatto»), non dopo.
      const last = main[main.length - 1];
      const atEnd = last && last.category === "done" && main.length > 1 ? main.length - 1 : main.length;
      const at = after >= 0 ? main.indexOf(d.phases[after]) + 1 : atEnd;
      d.phases.push({ name, category: "in_progress" });
      movePhase(d, name, at);
      if (!d.initial) d.initial = name;
    });
    setSel({ kind: "phase", name });
  };
  const onAddStep = (input: StepInput) => {
    let created = "";
    change((d) => {
      created = addStep(d, input);
    });
    setAdding(false);
    setSel({ kind: "phase", name: created });
  };
  const addTransition = (fromName?: string, toName?: string) => {
    const main = def.phases.filter((p) => !p.off_path);
    const from = fromName ?? (sel?.kind === "phase" ? sel.name : def.phases[0]?.name);
    if (!from) return;
    let to = toName;
    if (!to) {
      const i = main.findIndex((p) => p.name === from);
      // Dopo l'ultima fase si propone una fase finale (mai un passo indietro verso l'iniziale).
      const forward = i >= 0 ? main[i + 1] : undefined;
      to = forward?.name ?? def.phases.find((p) => p.name !== from && p.category === "done")?.name ?? def.phases.find((p) => p.name !== from)?.name ?? from;
    }
    const existing = def.transitions.findIndex((t) => !t.system && eq(t.to, to!) && t.from.some((f) => eq(f, from)));
    if (toName && existing >= 0) {
      setSel({ kind: "transition", index: existing });
      return;
    }
    change((d) => d.transitions.push({ from: [from], to: to!, label: t("configurazione.processEditor.passTo", { to }) }));
    setSel({ kind: "transition", index: def.transitions.length });
  };
  const newGate = (forTransition?: number) => {
    const key = keyFrom(t("configurazione.processEditor.nuovoGate"), (def.gates ?? []).map((g) => g.key));
    change((d) => {
      d.gates = [...(d.gates ?? []), { key, name: t("configurazione.processEditor.nuovoGate2"), condition: "", message: "" }];
      if (forTransition !== undefined) d.transitions[forTransition].gates = [...(d.transitions[forTransition].gates ?? []), key];
    });
    setOpenGate(def.gates?.length ?? 0);
    // Un gate nato da un passaggio si compila nel pannello del passaggio, che resta accanto al diagramma.
    if (forTransition === undefined) requestAnimationFrame(() => document.getElementById("editor-gates")?.scrollIntoView({ behavior: "smooth", block: "start" }));
  };
  const goTo = (p: Problem) => {
    const t = problemTarget(p.path);
    if (t.kind === "phase") setSel({ kind: "phase", name: def.phases[t.index]?.name ?? "" });
    else if (t.kind === "transition") setSel({ kind: "transition", index: t.index });
    else if (t.kind === "gate") setOpenGate(t.index);
    else if (t.kind === "rule") setOpenRule(t.index);
    else if (t.kind === "initial") def.phases[0] && setSel({ kind: "phase", name: def.initial || def.phases[0].name });
    else {
      const name = /«([^»]+)»|"([^"]+)"/.exec(p.message);
      if (name) setSel({ kind: "phase", name: name[1] ?? name[2] });
    }
    const id = t.kind === "gate" ? "editor-gates" : t.kind === "rule" ? "editor-rules" : "editor-diagram";
    requestAnimationFrame(() => document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" }));
  };

  const m = meta.data;
  const roleName = (k: string) => m?.roles.find((r) => r.key === k)?.name ?? k;
  const diff = useMemo(() => describeDiff(proc.current, clean(def), { role: roleName, renames: renames.current }), [proc.current, def, m]);
  const phaseErrors = useMemo(
    () => new Set([...grouped.byPhaseName].filter(([, ps]) => ps.some((p) => p.level === "error")).map(([n]) => n)),
    [grouped],
  );
  const transitionErrors = useMemo(
    () => new Set([...grouped.transition].filter(([, ps]) => ps.some((p) => p.level === "error")).map(([i]) => i)),
    [grouped],
  );
  const extraValues = useMemo(() => ({ stato: def.phases.map((p) => p.name) }), [def.phases]);

  // Su cellulare il pannello sta sotto l'elenco: scegliendo un elemento ci si porta lì.
  const asideRef = useRef<HTMLElement>(null);
  // Il diagramma entra nella colonna: si riduce (mai oltre il 70 %: sotto è illeggibile, e il riquadro scorre) o si
  // torna alle dimensioni reali, con scorrimento dentro il riquadro.
  const diagBox = useRef<HTMLDivElement>(null);
  // Zoom: null = adatta alla larghezza del riquadro; altrimenti il fattore scelto (1 = dimensioni reali).
  const [zoom, setZoom] = useState<number | null>(null);
  const [boxW, setBoxW] = useState(0);
  useEffect(() => {
    const el = diagBox.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setBoxW(el.clientWidth));
    ro.observe(el);
    setBoxW(el.clientWidth);
    return () => ro.disconnect();
  }, [narrow, def.phases.length === 0]);
  const lay = useMemo(() => layout(def), [def]);
  const fullW = def.phases.length ? lay.width : 0;
  const fitScale = boxW > 0 && fullW > boxW ? Math.max(MIN_ZOOM, boxW / fullW) : 1;
  const diagScale = zoom ?? fitScale;
  const zoomTo = (z: number) => setZoom(Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, Math.round(z * 100) / 100)));
  /** Porta la fase al centro del riquadro; con `onlyIfHidden` solo se non si vede per intero. */
  const centerOn = (name: string, onlyIfHidden = false) => {
    const el = diagBox.current;
    const n = lay.nodes.find((x) => x.name === name);
    if (!el || !n) return;
    const left = n.x * diagScale;
    const right = (n.x + n.w) * diagScale;
    if (onlyIfHidden && left >= el.scrollLeft && right <= el.scrollLeft + el.clientWidth) return;
    el.scrollTo({ left: Math.max(0, (left + right) / 2 - el.clientWidth / 2), behavior: "smooth" });
  };
  const selName = sel?.kind === "phase" ? sel.name : "";
  useEffect(() => {
    if (!selName || narrow) return;
    const id = requestAnimationFrame(() => centerOn(selName, true));
    return () => cancelAnimationFrame(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selName, diagScale, narrow, boxW]);
  useEffect(() => {
    if (narrow && sel) asideRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, [narrow, sel]);

  const selPhase = sel?.kind === "phase" ? def.phases.findIndex((p) => p.name === sel.name) : -1;
  const selTr = sel?.kind === "transition" && sel.index < def.transitions.length ? sel.index : -1;

  const panel =
    !m ? (
      meta.error ? <ErrorNote error={meta.error} /> : <p className="m-0 text-muted">{t("configurazione.processEditor.caricamentoDeiDatiDi")}</p>
    ) : selPhase >= 0 ? (
      <PhasePanel
        key={`p-${def.phases[selPhase].name}`}
        def={def}
        index={selPhase}
        problems={grouped.byPhaseName.get(def.phases[selPhase].name) ?? []}
        change={change}
        onRename={(o, n) => {
          for (const [k, v] of renames.current) if (v === o) renames.current.set(k, n);
          if (![...renames.current.values()].includes(n)) renames.current.set(o, n);
          setSel({ kind: "phase", name: n });
        }}
        onSelect={setSel}
        onAddTransition={(from) => addTransition(from)}
      />
    ) : selTr >= 0 ? (
      <TransitionPanel
        key={`t-${selTr}`}
        def={def}
        index={selTr}
        meta={m}
        problems={grouped.transition.get(selTr) ?? []}
        change={change}
        onSelect={setSel}
        onNewGate={() => newGate(selTr)}
        extraValues={extraValues}
      />
    ) : (
      <div className="flex flex-col gap-2 text-[13px] text-muted">
        <p className="m-0">{t("configurazione.processEditor.selezionaUnaFaseO")}</p>
        <p className="m-0">
          {narrow
            ? t("configurazione.processEditor.toccaUnaFaseO")
            : t("configurazione.processEditor.trascinaUnBloccoPer")}
        </p>
      </div>
    );

  return (
    <div
      className="flex flex-col gap-6"
      onKeyDown={(e) => {
        const tag = (e.target as HTMLElement).tagName;
        if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
        if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "z") {
          e.preventDefault();
          if (e.shiftKey) redo();
          else undo();
        }
      }}
    >
      {/* Barra degli strumenti e stato del salvataggio. */}
      <div className="flex flex-wrap items-center gap-2">
        <button className="btn btn-primary" aria-expanded={adding} disabled={!m} onClick={() => setAdding((v) => !v)}>
          {t("configurazione.passo.aggiungiUnPasso")}
        </button>
        <button className="btn" onClick={addPhase}>
          {t("configurazione.processEditor.fase")}
        </button>
        <button className="btn" onClick={() => addTransition()} disabled={def.phases.length === 0}>
          {t("configurazione.processEditor.nuovoPassaggio")}
        </button>
        <button className="btn" onClick={undo} disabled={!history.length} title={t("configurazione.common.annullaZ")}>
          {t("configurazione.common.undo")}
        </button>
        <button className="btn" onClick={redo} disabled={!future.length} title={t("configurazione.common.ripetiZ")}>
          {t("configurazione.common.ripeti")}
        </button>
        <span className="ml-auto flex flex-wrap items-center gap-3 text-[12.5px]">
          <span aria-live="polite" className={errors ? "text-danger" : "text-muted"}>
            {checking ? t("configurazione.processEditor.checkingShort") : errors ? t("configurazione.processEditor.errorCount", { count: errors }) : t("configurazione.processEditor.nessunErrore")}
          </span>
          <span aria-live="polite" className="text-muted">
            {save.isPending
              ? t("configurazione.processEditor.savingShort")
              : dirty
                ? t("configurazione.common.modificheNonSalvate")
                : saved
                  ? t("configurazione.processEditor.draftSavedAt", { version: nextNumber, time: fmtTime(saved, { hour: "2-digit", minute: "2-digit" }) })
                  : draftV
                    ? t("configurazione.processEditor.draftVersion", { version: draftV.number })
                    : curV
                      ? t("configurazione.processEditor.sameAsVersion", { version: curV.number })
                      : ""}
          </span>
          <label className="inline-flex items-center gap-1.5">
            <input type="checkbox" checked={autosave} onChange={(e) => setAutosave(e.target.checked)} />
            {t("configurazione.common.salvataggioAutomatico")}
          </label>
          <button className="btn" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
            {t("configurazione.common.salvaBozza")}
          </button>
        </span>
      </div>
      {!draftV && !dirty && curV && (
        <p className="m-0 -mt-3 text-[12.5px] text-muted">
          {t("configurazione.processEditor.viewingCurrent", { current: curV.number, next: nextNumber })}
        </p>
      )}
      <ErrorNote error={save.error} />
      <ErrorNote error={checkError} />

      {adding && m && (
        <AddStep def={def} meta={m} initialAfter={sel?.kind === "phase" ? sel.name : undefined} extraValues={extraValues} onAdd={onAddStep} onClose={() => setAdding(false)} />
      )}

      {/* Diagramma (o elenco su cellulare) e pannello dell'elemento selezionato. */}
      <div id="editor-diagram" className="grid scroll-mt-4 border-y border-hair md:grid-cols-[minmax(0,1fr)_380px]">
        <div className="min-w-0 py-3 md:pr-4">
          {def.phases.length === 0 ? (
            <div className="flex flex-col items-start gap-2 py-8">
              <p className="m-0 text-muted">{t("configurazione.processEditor.ilProcessoNonHa")}</p>
              <button className="btn btn-primary" onClick={addPhase}>
                {t("configurazione.processEditor.aggiungiLaPrimaFase")}
              </button>
            </div>
          ) : narrow ? (
            <PhaseList def={def} sel={sel} onSelect={setSel} lost={lost} errors={phaseErrors} transitionErrors={transitionErrors} change={change} />
          ) : (
            <>
              <p className="m-0 mb-2 text-[12px] text-muted">
                {t("configurazione.processEditor.sopraLaFilaI")}
              </p>
              <div className="mb-2 flex flex-wrap items-center gap-2 text-[12.5px]" role="toolbar" aria-label={t("configurazione.passo.zoomDiagramma")}>
                <button type="button" className="btn px-2.5 py-0.5" aria-label={t("configurazione.passo.zoomMeno")} disabled={diagScale <= MIN_ZOOM + 0.001} onClick={() => zoomTo(diagScale - 0.15)}>
                  −
                </button>
                <span className="min-w-12 text-center font-mono text-[12px] text-muted" aria-live="polite" data-testid="zoom-percent">
                  {Math.round(diagScale * 100)} %
                </span>
                <button type="button" className="btn px-2.5 py-0.5" aria-label={t("configurazione.passo.zoomPiu")} disabled={diagScale >= MAX_ZOOM - 0.001} onClick={() => zoomTo(diagScale + 0.15)}>
                  +
                </button>
                <button type="button" className="btn px-2.5 py-0.5" aria-label={t("configurazione.processEditor.adattaAllaLarghezza")} aria-pressed={zoom === null} onClick={() => setZoom(null)}>
                  {t("configurazione.passo.adatta")}
                </button>
                <button type="button" className="btn px-2.5 py-0.5" aria-pressed={zoom === 1} onClick={() => zoomTo(1)}>
                  100 %<span className="sr-only"> {t("configurazione.processEditor.dimensioniReali")}</span>
                </button>
                <button type="button" className="btn px-2.5 py-0.5" disabled={!selName} onClick={() => centerOn(selName)}>
                  {t("configurazione.passo.centraSullaFase")}
                </button>
                {fullW * diagScale > boxW + 1 && <span className="text-muted">{t("configurazione.passo.scorri")}</span>}
              </div>
              <div ref={diagBox} className="overflow-x-auto">
                <Diagram
                  scale={diagScale}
                  def={def}
                  selection={sel}
                  onSelect={setSel}
                  phaseErrors={phaseErrors}
                  transitionErrors={transitionErrors}
                  unreachable={lost}
                  onMove={(name, index, off) => change((d) => movePhase(d, name, index, off))}
                  onConnect={(from, to) => addTransition(from, to)}
                />
              </div>
            </>
          )}
        </div>
        <aside
          ref={asideRef}
          aria-label={t("configurazione.processEditor.proprietaDellElementoSeleziona")}
          className="scroll-mt-16 border-t border-hair py-4 md:sticky md:top-0 md:max-h-screen md:self-start md:overflow-y-auto md:[scrollbar-width:thin] md:border-t-0 md:border-l md:pl-4"
        >
          {panel}
          {narrow && sel && (
            <button
              className="mt-4 text-[12.5px] text-accent hover:underline"
              onClick={() => {
                setSel(null);
                document.getElementById("editor-diagram")?.scrollIntoView({ behavior: "smooth", block: "start" });
              }}
            >
              {t("configurazione.processEditor.tornaAllElencoDelle")}
            </button>
          )}
        </aside>
      </div>

      {m && (
        <>
          <Gates def={def} meta={m} open={openGate} setOpen={setOpenGate} problems={grouped.gate} change={change} onNew={() => newGate()} extraValues={extraValues} />
          <Rules
            def={def}
            meta={m}
            open={openRule}
            setOpen={setOpenRule}
            problems={grouped.rule}
            change={change}
            extraValues={extraValues}
          />
        </>
      )}

      <section className="flex flex-col gap-2">
        <SectionTitle aside={<span className="text-[12px] text-muted">{checking ? t("configurazione.processEditor.verificaInCorso") : t("configurazione.processEditor.aggiornatiAOgniModifica")}</span>}>
          {t("configurazione.processEditor.problemi")}
        </SectionTitle>
        {problems.length === 0 ? (
          <p className="m-0 text-ok">{t("configurazione.common.nessunProblemaLaBozza")}</p>
        ) : (
          <ul className="m-0 flex list-none flex-col p-0">
            {problems.map((p, i) => (
              <li key={i} className="border-b border-hair py-1.5 text-[12.5px]">
                <button className="text-left hover:underline" onClick={() => goTo(p)}>
                  <span className={`font-semibold ${p.level === "error" ? "text-danger" : "text-fg"}`}>{p.level === "error" ? t("configurazione.common.errore") : t("configurazione.common.avviso")}</span>{" "}
                  <span className="text-muted">{where(p, def)}</span> {p.message}
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="flex flex-col gap-2">
        <SectionTitle aside={curV && <span className="text-[12px] text-muted">{t("configurazione.processEditor.comparedWith", { version: curV.number })}</span>}>{t("configurazione.common.cosaCambia")}</SectionTitle>
        {diff.length === 0 ? (
          <p className="m-0 text-muted">{t("configurazione.processEditor.nessunaDifferenzaDallaVersione")}</p>
        ) : (
          <ul className="m-0 flex list-disc flex-col gap-0.5 pl-5 text-[13px]">
            {diff.map((d, i) => (
              <li key={i}>{d}</li>
            ))}
          </ul>
        )}
      </section>

      <section className="flex flex-col gap-3">
        <SectionTitle>{t("configurazione.common.pubblicazione")}</SectionTitle>
        <p className="m-0 max-w-3xl text-[12.5px] text-muted">
          {t("configurazione.processEditor.pubblicandoLaBozzaDiventa")}
        </p>
        <label className="flex max-w-xl flex-col gap-1">
          <span className="label">{t("configurazione.common.noteDellaVersione")}</span>
          <input
            className="field"
            value={notes}
            onChange={(e) => {
              setNotes(e.target.value);
              setDirty(true);
            }}
            placeholder={t("configurazione.common.cosaCambiaEPerche")}
          />
        </label>
        <div className="flex flex-wrap items-center gap-2">
          <button
            className="btn btn-primary"
            disabled={publish.isPending || save.isPending || checking || errors > 0 || (!draftV && !dirty)}
            onClick={() => publish.mutate()}
          >
            {t("configurazione.processEditor.publishVersion", { version: nextNumber })}
          </button>
          {draftV &&
            (confirmDiscard ? (
              <>
                <span className="text-[12.5px]">{t("configurazione.processEditor.leModificheNonPubblicate")}</span>
                <button className="btn btn-danger" disabled={discard.isPending} onClick={() => discard.mutate()}>
                  {t("configurazione.processEditor.confermaScartaLaBozza")}
                </button>
                <button className="btn" onClick={() => setConfirmDiscard(false)}>
                  No
                </button>
              </>
            ) : (
              <button className="btn btn-danger" onClick={() => setConfirmDiscard(true)}>
                {t("configurazione.common.scartaBozza")}
              </button>
            ))}
          {errors > 0 && <span className="text-[12.5px] text-danger">{t("configurazione.processEditor.correggiGliErroriPer")}</span>}
        </div>
        <ErrorNote error={publish.error} />
        {publish.error instanceof ApiError && publish.error.problems && (
          <p className="m-0 text-[12.5px] text-danger">{t("configurazione.processEditor.publishFoundProblems", { count: publish.error.problems.length })}</p>
        )}
        <ErrorNote error={discard.error} />
        {published && (
          <p className="m-0 text-ok" role="status">
            {t("configurazione.processEditor.published", { version: published })}
          </p>
        )}
      </section>
    </div>
  );
}

/** Dove sta il problema, in parole. */
function where(p: Problem, def: Definition): string {
  const t = problemTarget(p.path);
  switch (t.kind) {
    case "phase":
      return tr("configurazione.processEditor.whereStage", { name: def.phases[t.index]?.name ?? t.index + 1 });
    case "transition": {
      const tx = def.transitions[t.index];
      return tx
        ? tr("configurazione.processEditor.whereTransition", { label: tx.label || (tx.system ? tr("configurazione.common.diSistema") : tr("configurazione.processEditor.senzaEtichetta")), to: tx.to })
        : tr("configurazione.processEditor.whereTransitionOnly");
    }
    case "gate":
      return tr("configurazione.processEditor.whereGate", { name: def.gates?.[t.index]?.name ?? t.index + 1 });
    case "rule":
      return tr("configurazione.processEditor.whereRule", { name: def.rules?.[t.index]?.name ?? t.index + 1 });
    case "initial":
      return tr("configurazione.processEditor.faseIniziale");
    default:
      return "";
  }
}

function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: ReactNode }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="label">{label}</span>
      {children}
      {hint && <span className="text-[11.5px] text-muted">{hint}</span>}
    </label>
  );
}

/**
 * Sezione comprimibile del pannello laterale. Il pannello sta fisso accanto al diagramma e, con le
 * sezioni secondarie chiuse, di norma entra nello schermo senza scorrere.
 */
function Collapse({ title, summary, defaultOpen, children }: { title: string; summary?: ReactNode; defaultOpen?: boolean; children: ReactNode }) {
  return (
    <details open={defaultOpen} className="group border-t border-hair pt-2 first-of-type:border-t-0 first-of-type:pt-0">
      <summary className="flex cursor-pointer select-none items-baseline gap-2 py-0.5 marker:content-none">
        <span aria-hidden className="inline-block w-3 text-[11px] text-muted transition-transform group-open:rotate-90">▸</span>
        <span className="label">{title}</span>
        {summary && <span className="min-w-0 flex-1 truncate text-right text-[12px] text-muted group-open:hidden">{summary}</span>}
      </summary>
      <div className="mt-2 flex flex-col gap-3 pl-5">{children}</div>
    </details>
  );
}

function ProblemNotes({ problems }: { problems: Problem[] }) {
  const t = useT();
  if (!problems.length) return null;
  return (
    <ul className="m-0 flex list-none flex-col gap-1 border-l-2 border-danger p-0 pl-2.5" aria-label={t("configurazione.processEditor.problemi")}>
      {problems.map((p, i) => (
        <li key={i} className={`text-[12.5px] ${p.level === "error" ? "text-danger" : ""}`}>
          <span className="font-semibold">{p.level === "error" ? t("configurazione.common.errore") : t("configurazione.common.avviso")}:</span> {p.message}
        </li>
      ))}
    </ul>
  );
}

function TwoStepDelete({ label, detail, onConfirm }: { label: string; detail?: string; onConfirm: () => void }) {
  const t = useT();
  const [ask, setAsk] = useState(false);
  if (!ask) {
    return (
      <button type="button" className="self-start text-[12.5px] text-danger hover:underline" onClick={() => setAsk(true)}>
        {label}
      </button>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-2 text-[12.5px]">
      {detail && <span>{detail}</span>}
      <button type="button" className="btn btn-danger px-2.5 py-1" onClick={onConfirm}>
        {t("configurazione.processEditor.conferma")}
      </button>
      <button type="button" className="btn px-2.5 py-1" onClick={() => setAsk(false)}>
        No
      </button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Fase

function PhasePanel({
  def,
  index,
  problems,
  change,
  onRename,
  onSelect,
  onAddTransition,
}: {
  def: Definition;
  index: number;
  problems: Problem[];
  change: Change;
  onRename: (from: string, to: string) => void;
  onSelect: (s: Selection) => void;
  onAddTransition: (from: string) => void;
}) {
  const t = useT();
  const p = def.phases[index];
  const [name, setName] = useState(p.name);
  const [nameError, setNameError] = useState("");
  const main = def.phases.filter((x) => !x.off_path);
  const pos = main.indexOf(p);
  const outgoing = def.transitions.map((t, i) => [t, i] as const).filter(([t]) => t.from.some((f) => eq(f, p.name)));
  const incoming = def.transitions.map((t, i) => [t, i] as const).filter(([t]) => eq(t.to, p.name));
  const touching = new Set([...outgoing, ...incoming].map(([, i]) => i)).size;
  const commitName = () => {
    const n = name.trim();
    if (n === p.name) return setNameError("");
    if (!n) return setNameError(t("configurazione.processEditor.ilNomeEObbligatorio"));
    if (/[,:"]/.test(n)) return setNameError(t("configurazione.processEditor.ilNomeNonPuo"));
    if (def.phases.some((x) => x !== p && eq(x.name, n))) return setNameError(t("configurazione.processEditor.phaseExists", { name: n }));
    setNameError("");
    const old = p.name;
    change((d) => renamePhase(d, old, n));
    onRename(old, n);
  };
  const set = (fn: (ph: Definition["phases"][number]) => void, key: string) =>
    change((d) => {
      const ph = d.phases.find((x) => x.name === p.name);
      if (ph) fn(ph);
    }, `p${p.name}.${key}`);
  const isInitial = eq(def.initial, p.name);
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-baseline justify-between">
        <h3 className="label m-0">{t("configurazione.processEditor.fase2")}</h3>
        {!p.off_path && <span className="font-mono text-[12px] text-muted">{t("configurazione.processEditor.positionOf", { pos: String(pos + 1).padStart(2, "0"), total: main.length })}</span>}
      </div>
      <Collapse title={t("configurazione.processEditor.nomeECategoria")} defaultOpen summary={`${p.name} · ${categoryName(p.category)}`}>
      <Field label={t("configurazione.common.nome")} hint={nameError ? <span className="text-danger">{nameError}</span> : t("configurazione.processEditor.rinominandoSiAggiornanoPassagg")}>
        <input
          className="field"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onBlur={commitName}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              commitName();
            }
            if (e.key === "Escape") setName(p.name);
          }}
        />
      </Field>
      <Field label={t("configurazione.processEditor.categoria")} hint={t("configurazione.processEditor.diceSeLElemento")}>
        <Select className="field" value={p.category} onChange={(e) => set((ph) => (ph.category = e.target.value), "category")}>
          {CATEGORIES.map((c) => (
            <option key={c.key} value={c.key}>
              {c.name}
            </option>
          ))}
        </Select>
      </Field>
      </Collapse>
      <Collapse title={t("configurazione.common.aspetto")} summary={t("configurazione.processEditor.coloreEDescrizione")}>
      <div className="flex flex-col gap-1">
        <span className="label">{t("configurazione.common.colore")}</span>
        <div className="flex flex-wrap items-center gap-1.5">
          {PHASE_COLORS.map((c) => (
            <button
              key={c}
              type="button"
              aria-label={t("configurazione.processEditor.colorNamed", { name: c })}
              aria-pressed={p.color?.toUpperCase() === c}
              className={`size-6 border ${p.color?.toUpperCase() === c ? "border-2 border-accent" : "border-hair"}`}
              style={{ background: c }}
              onClick={() => set((ph) => (ph.color = c), "color")}
            />
          ))}
          <input
            type="color"
            aria-label={t("configurazione.processEditor.altroColore")}
            className="h-6 w-8 cursor-pointer border border-hair bg-transparent p-0"
            value={p.color ?? "#6B7280"}
            onChange={(e) => set((ph) => (ph.color = e.target.value.toUpperCase()), "color")}
          />
          <button type="button" className="text-[12px] text-accent hover:underline" onClick={() => set((ph) => delete ph.color, "color")}>
            {t("configurazione.common.nessuno")}
          </button>
        </div>
      </div>
      <Field label={t("configurazione.common.descrizione")}>
        <textarea
          className="field min-h-[56px]"
          value={p.description ?? ""}
          onChange={(e) => set((ph) => (ph.description = e.target.value), "description")}
          placeholder={t("configurazione.processEditor.cosaSuccedeInQuesta")}
        />
      </Field>
      </Collapse>
      <Collapse title={t("configurazione.etichette.titolo")} summary={p.labels?.en ? p.labels.en : undefined}>
      <Field label={t("configurazione.etichette.faseInglese")} hint={t("configurazione.etichette.faseGuida")}>
        <input
          className="field"
          maxLength={80}
          aria-label={t("configurazione.etichette.faseInglese")}
          value={p.labels?.en ?? ""}
          onChange={(e) =>
            set((ph) => {
              const v = e.target.value;
              if (v.trim()) ph.labels = { ...ph.labels, en: v };
              else if (ph.labels) {
                const { en: _en, ...rest } = ph.labels;
                void _en;
                if (Object.keys(rest).length) ph.labels = rest;
                else delete ph.labels;
              }
            }, "labels")
          }
        />
      </Field>
      </Collapse>
      <Collapse title={t("configurazione.processEditor.posizioneNelPercorso")} summary={isInitial ? t("configurazione.processEditor.faseIniziale2") : p.off_path ? t("configurazione.common.fuoriPercorso") : t("configurazione.processEditor.nelPercorso")}>
      <div className="flex flex-col gap-2 text-[13px]">
        <label className="inline-flex items-center gap-2">
          <input type="checkbox" checked={isInitial} disabled={isInitial} onChange={() => change((d) => (d.initial = p.name))} />
          {t("configurazione.processEditor.faseInizialeLeRichieste")}
        </label>
        <label className="inline-flex items-center gap-2">
          <input type="checkbox" checked={!!p.off_path} onChange={(e) => change((d) => movePhase(d, p.name, e.target.checked ? 999 : main.length, e.target.checked))} />
          {t("configurazione.processEditor.fuoriDalPercorsoPrincipale")}
        </label>
      </div>
      {!p.off_path && (
        <div className="flex gap-2">
          <button className="btn px-2.5 py-1" disabled={pos <= 0} onClick={() => change((d) => movePhase(d, p.name, pos - 1))}>
            {t("configurazione.processEditor.prima")}
          </button>
          <button className="btn px-2.5 py-1" disabled={pos >= main.length - 1} onClick={() => change((d) => movePhase(d, p.name, pos + 1))}>
            {t("configurazione.processEditor.dopo")}
          </button>
        </div>
      )}
      </Collapse>
      <Collapse title={t("configurazione.common.passaggi")} defaultOpen summary={`${outgoing.length + incoming.length}`}>
      <div className="flex flex-col gap-1">
        {outgoing.length + incoming.length === 0 &&<span className="text-[12.5px] text-muted">{t("configurazione.processEditor.nessunPassaggioDaO")}</span>}
        {outgoing.map(([t, i]) => (
          <button key={`o${i}`} className="text-left text-[12.5px] hover:underline" onClick={() => onSelect({ kind: "transition", index: i })}>
            → {t.to} <span className="text-muted">· {t.label || (t.system ? tr("configurazione.common.diSistema") : tr("configurazione.processEditor.senzaEtichetta"))}</span>
          </button>
        ))}
        {incoming.map(([t, i]) => (
          <button key={`i${i}`} className="text-left text-[12.5px] hover:underline" onClick={() => onSelect({ kind: "transition", index: i })}>
            ← {t.from.map((f) => (f === ANY ? tr("configurazione.processEditor.qualunqueFase") : f)).join(", ")}{" "}
            <span className="text-muted">· {t.label || (t.system ? tr("configurazione.common.diSistema") : tr("configurazione.processEditor.senzaEtichetta"))}</span>
          </button>
        ))}
        <button className="self-start text-[12.5px] font-semibold text-accent hover:underline" onClick={() => onAddTransition(p.name)}>
          {t("configurazione.processEditor.nuovoPassaggioDaQuesta")}
        </button>
      </div>
      </Collapse>
      <ProblemNotes problems={problems} />
      <TwoStepDelete
        label={t("configurazione.processEditor.eliminaLaFase")}
        detail={touching ? t("configurazione.processEditor.deleteStageDetail", { count: touching }) : undefined}
        onConfirm={() => {
          change((d) => removePhase(d, p.name));
          onSelect(null);
        }}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Passaggio

function TransitionPanel({
  def,
  index,
  meta,
  problems,
  change,
  onSelect,
  onNewGate,
  extraValues,
}: {
  def: Definition;
  index: number;
  meta: ProcessMeta;
  problems: Problem[];
  change: Change;
  onSelect: (s: Selection) => void;
  onNewGate: () => void;
  extraValues: Record<string, string[]>;
}) {
  const t = def.transitions[index];
  const set = (fn: (x: Definition["transitions"][number]) => void, key: string) => change((d) => fn(d.transitions[index]), `t${index}.${key}`);
  const toggle = (list: string[] | undefined, v: string, on: boolean) => (on ? [...(list ?? []), v] : (list ?? []).filter((x) => x !== v));
  const policy = meta.policies.find((p) => eq(p.stage, t.to) && (!p.type || p.type === meta.type.key));
  const missing = policy
    ? Object.entries(policy.outcomes).filter(
        ([, to]) => to && !def.transitions.some((x) => x.system && eq(x.to, to) && x.from.some((f) => eq(f, t.to))),
      )
    : [];
  return (
    <div className="flex flex-col gap-3">
      <h3 className="label m-0">{tr("configurazione.processEditor.passaggio")}</h3>
      <Collapse title={tr("configurazione.processEditor.etichettaEPercorso")} defaultOpen summary={`${t.label || tr("configurazione.processEditor.senzaEtichetta")} → ${t.to}`}>
      <Field label={tr("configurazione.processEditor.etichettaDelPulsante")} hint={t.system ? tr("configurazione.processEditor.facoltativaNeiPassaggiDi") : tr("configurazione.processEditor.ilTestoDelPulsante")}>
        <input className="field" value={t.label ?? ""} onChange={(e) => set((x) => (x.label = e.target.value), "label")} />
      </Field>
      <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
        <legend className="label mb-1">{tr("configurazione.processEditor.from")}</legend>
        <label className="inline-flex items-center gap-2 text-[13px]">
          <input type="checkbox" checked={t.from.includes(ANY)} onChange={(e) => set((x) => (x.from = toggle(x.from, ANY, e.target.checked)), "from")} />
          <span className="italic">{tr("configurazione.processEditor.qualunqueFaseNonConclusa")}</span>
        </label>
        <div className="grid grid-cols-2 gap-x-3 gap-y-0.5">
          {def.phases.map((p) => (
            <label key={p.name} className="inline-flex min-w-0 items-center gap-2 text-[13px]">
              <input
                type="checkbox"
                checked={t.from.some((f) => eq(f, p.name))}
                disabled={eq(p.name, t.to)}
                onChange={(e) => set((x) => (x.from = e.target.checked ? [...x.from, p.name] : x.from.filter((f) => !eq(f, p.name))), "from")}
              />
              <span className="truncate">{p.name}</span>
            </label>
          ))}
        </div>
      </fieldset>
      <Field label={tr("configurazione.processEditor.to")}>
        <Select className="field" value={t.to} onChange={(e) => set((x) => (x.to = e.target.value), "to")}>
          {!def.phases.some((p) => p.name === t.to) && <option value={t.to}>{tr("configurazione.processEditor.missingName", { name: t.to })}</option>}
          {def.phases.map((p) => (
            <option key={p.name} value={p.name}>
              {p.name}
            </option>
          ))}
        </Select>
      </Field>
      </Collapse>
      <Collapse title={tr("configurazione.processEditor.chiPuoEseguirlo")} summary={t.system ? tr("configurazione.processEditor.soloIlMotore") : t.roles?.length ? tr("configurazione.processEditor.roleCount", { count: t.roles.length }) : tr("configurazione.processes.chiPuoModificare")}>
      <label className="inline-flex items-center gap-2 text-[13px]">
        <input type="checkbox" checked={!!t.system}onChange={(e) => set((x) => (x.system = e.target.checked || undefined), "system")} />
        {tr("configurazione.processEditor.diSistemaLoEsegue")}
      </label>
      {!t.system && (
        <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
          <legend className="sr-only">{tr("configurazione.processEditor.ruoliChePossonoEseguirlo")}</legend>
          <div className="grid grid-cols-2 gap-x-3 gap-y-0.5">
            {meta.roles.map((r) => (
              <label key={r.key} className="inline-flex min-w-0 items-center gap-2 text-[13px]">
                <input type="checkbox" checked={!!t.roles?.includes(r.key)} onChange={(e) => set((x) => (x.roles = toggle(x.roles, r.key, e.target.checked)), "roles")} />
                <span className="truncate">{r.name}</span>
              </label>
            ))}
          </div>
          <span className="text-[11.5px] text-muted">
            {t.roles?.length ? tr("configurazione.processEditor.soloChiHaUno") : tr("configurazione.processEditor.nessunRuoloSceltoChiunque")}
          </span>
        </fieldset>
      )}
      </Collapse>
      <Collapse title={tr("configurazione.processEditor.gateDaSuperare")} defaultOpen summary={t.gates?.length ? `${t.gates.length}` : tr("configurazione.common.nessuno")}>
      <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
        <legend className="sr-only">{tr("configurazione.processEditor.gateDaSuperare")}</legend>
        {(def.gates ?? []).length === 0 && <span className="text-[12.5px] text-muted">{tr("configurazione.processEditor.nessunGateDefinito")}</span>}
        {(def.gates ?? []).map((g) => (
          <label key={g.key} className="flex items-start gap-2 text-[13px]">
            <input type="checkbox" className="mt-0.5" checked={!!t.gates?.includes(g.key)} onChange={(e) => set((x) => (x.gates = toggle(x.gates, g.key, e.target.checked)), "gates")} />
            <span className="min-w-0">
              {g.name}
              <span className="block truncate text-[11.5px] text-muted" title={g.condition}>{g.condition ? describeCondition(g.condition, factNames(meta.facts)) : g.check ? tr("configurazione.processEditor.checkNamed", { name: humanFact(g.check) }) : ""}</span>
            </span>
          </label>
        ))}
        {(t.gates ?? [])
          .filter((k) => !def.gates?.some((g) => g.key === k))
          .map((k) => (
            <label key={k} className="flex items-center gap-2 text-[13px] text-danger">
              <input type="checkbox" checked onChange={() => set((x) => (x.gates = toggle(x.gates, k, false)), "gates")} />
              {tr("configurazione.processEditor.undefinedName", { name: k })}
            </label>
          ))}
        <button type="button" className="self-start text-[12.5px] font-semibold text-accent hover:underline" onClick={onNewGate}>
          {tr("configurazione.processEditor.nuovoGatePerQuesto")}
        </button>
      </fieldset>
      {(def.gates ?? [])
        .map((g, gi) => ({ g, gi }))
        .filter(({ g }) => t.gates?.includes(g.key))
        .map(({ g, gi }) => {
          const setG = (fn: (x: NonNullable<Definition["gates"]>[number]) => void, key: string) => change((d) => fn(d.gates![gi]), `g${gi}.${key}`);
          return (
            <fieldset key={g.key} className="m-0 flex flex-col gap-2 border border-hair p-3">
              <legend className="label px-1">{tr("configurazione.processEditor.gateTitle", { name: g.name || tr("configurazione.processEditor.senzaNome") })}</legend>
              <Field label={tr("configurazione.common.nome")}>
                <input className="field" value={g.name} onChange={(e) => setG((x) => (x.name = e.target.value), "name")} />
              </Field>
              {g.check ? (
                <Field label={tr("configurazione.processEditor.controllo")}>
                  <Select className="field" value={g.check} onChange={(e) => setG((x) => (x.check = e.target.value), "check")}>
                    {!meta.checks.includes(g.check) && <option value={g.check}>{tr("configurazione.processEditor.unavailableName", { name: g.check })}</option>}
                    {meta.checks.map((c) => (
                      <option key={c} value={c}>
                        {humanFact(c)}
                      </option>
                    ))}
                  </Select>
                </Field>
              ) : (
                <ConditionBuilder
                  label={tr("configurazione.processEditor.deveValere")}
                  value={g.condition ?? ""}
                  onChange={(v) => setG((x) => (x.condition = v), "condition")}
                  facts={meta.facts}
                  type={meta.type.key}
                  sample={meta.sample}
                  empty={tr("configurazione.processEditor.vaAggiuntaAlmenoUna")}
                  extraValues={extraValues}
                />
              )}
              <Field label={tr("configurazione.processEditor.messaggioQuandoNonE")}>
                <input
                  className="field"
                  value={g.message ?? ""}
                  placeholder={tr("configurazione.processEditor.esServeUnaStima")}
                  onChange={(e) => setG((x) => (x.message = e.target.value), "message")}
                />
              </Field>
            </fieldset>
          );
        })}
      </Collapse>
      <Collapse
        title={tr("configurazione.processEditor.condizioneEApprovazione")}
        summary={[t.when ? tr("configurazione.processEditor.conCondizione") : "", t.approval ? tr("configurazione.processEditor.apreUnApprovazione") : "", t.comment_required ? tr("configurazione.processEditor.motivazione") : ""].filter(Boolean).join(" · ") || tr("configurazione.processEditor.nessuna")}
      >
      <ConditionBuilder
        label={tr("configurazione.processEditor.soloSe")}
        value={t.when ?? ""}
        onChange={(v) => set((x) => (x.when = v || undefined), "when")}
        facts={meta.facts}
        type={meta.type.key}
        sample={meta.sample}
        empty={tr("configurazione.processEditor.ilPassaggioCE")}
        extraValues={extraValues}
      />
      <div className="flex flex-col gap-1.5">
        <label className="inline-flex items-center gap-2 text-[13px]">
          <input
            type="checkbox"
            checked={!!t.approval}
            onChange={(e) => set((x) => (x.approval = e.target.checked ? x.label || "approvazione" : undefined), "approval")}
          />
          {tr("configurazione.processEditor.entersOpensApproval", { to: t.to })}
        </label>
        {t.approval && (
          <div className="flex flex-col gap-1.5 border-l-2 border-hair pl-2.5">
            <Field label={tr("configurazione.processEditor.oggettoDellApprovazione")}>
              <input className="field py-1.5" value={t.approval} onChange={(e) => set((x) => (x.approval = e.target.value), "approval.subject")} />
            </Field>
            {policy ? (
              <p className="m-0 text-[12.5px]">
                {tr("configurazione.processEditor.policyApplies", { name: policy.name,
                  outcomes:
                    Object.keys(policy.outcomes).length > 0
                      ? `: ${Object.entries(policy.outcomes)
                          .map(([k, v]) => `${outcomeName(k)} → ${v}`)
                          .join(", ")}`
                      : "" })}
              </p>
            ) : (
              <p className="m-0 text-[12.5px] text-danger">{tr("configurazione.processEditor.noApprovalPolicy", { to: t.to })}</p>
            )}
            {missing.length > 0 && (
              <button
                type="button"
                className="self-start text-left text-[12.5px] font-semibold text-accent hover:underline"
                onClick={() =>
                  change((d) => {
                    for (const [, to] of missing) d.transitions.push({ from: [t.to], to, system: true });
                  })
                }
              >
                {tr("configurazione.processEditor.addSystemTransitions", { outcomes: missing.map(([k, v]) => `${outcomeName(k)} → ${v}`).join(", ") })}
              </button>
            )}
          </div>
        )}
      </div>
      <label className="inline-flex items-center gap-2 text-[13px]">
        <input type="checkbox" checked={!!t.comment_required} onChange={(e) => set((x) => (x.comment_required = e.target.checked || undefined), "comment")} />
        {tr("configurazione.processEditor.motivazioneObbligatoria")}
      </label>
      </Collapse>
      <ProblemNotes problems={problems} />
      <TwoStepDelete
        label={tr("configurazione.processEditor.eliminaIlPassaggio")}
        onConfirm={() => {
          change((d) => d.transitions.splice(index, 1));
          onSelect(null);
        }}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Gate

function Gates({
  def,
  meta,
  open,
  setOpen,
  problems,
  change,
  onNew,
  extraValues,
}: {
  def: Definition;
  meta: ProcessMeta;
  open: number | null;
  setOpen: (i: number | null) => void;
  problems: Map<number, Problem[]>;
  change: Change;
  onNew: () => void;
  extraValues: Record<string, string[]>;
}) {
  const t = useT();
  const gates = def.gates ?? [];
  return (
    <section id="editor-gates" className="flex scroll-mt-4 flex-col gap-2">
      <SectionTitle
        aside={
          <button className="text-[12.5px] font-semibold text-accent hover:underline" onClick={onNew}>
            {t("configurazione.processEditor.nuovoGate3")}
          </button>
        }
      >
        {t("configurazione.common.gate")}
      </SectionTitle>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">
        {t("configurazione.processEditor.unGateEUna")}
      </p>
      {gates.length === 0 && <p className="m-0 text-muted">{t("configurazione.processEditor.nessunGate")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {gates.map((g, i) => {
          const users = def.transitions.filter((t) => t.gates?.includes(g.key));
          const ps = problems.get(i) ?? [];
          const isOpen = open === i;
          const set = (fn: (x: NonNullable<Definition["gates"]>[number]) => void, key: string) => change((d) => fn(d.gates![i]), `g${i}.${key}`);
          return (
            <li key={i} className={`border-b border-hair ${isOpen ? "bg-sel/40" : ""}`}>
              <button
                className="flex w-full flex-wrap items-baseline gap-x-3 gap-y-0.5 py-2 text-left"
                aria-expanded={isOpen}
                onClick={() => setOpen(isOpen ? null : i)}
              >
                <span className="font-medium">{g.name || t("configurazione.processEditor.senzaNome")}</span>
                <span className="text-[12px] text-muted" title={g.condition}>{g.condition ? describeCondition(g.condition, factNames(meta.facts)) : g.check ? t("configurazione.processEditor.checkNamed", { name: humanFact(g.check) }) : t("configurazione.processEditor.daCompletare")}</span>
                {ps.some((p) => p.level === "error") && <span className="text-[12px] text-danger">{t("configurazione.processEditor.daCorreggere")}</span>}
                <span className="ml-auto text-[12px] text-muted">{users.length ? t("configurazione.processEditor.usedByCount", { count: users.length }) : t("configurazione.processEditor.nonUsato")}</span>
              </button>
              {isOpen && (
                <div className="flex max-w-2xl flex-col gap-3 pb-4">
                  <div className="grid gap-3 sm:grid-cols-[1fr_200px]">
                    <Field label={t("configurazione.common.nome")}>
                      <input className="field" value={g.name} onChange={(e) => set((x) => (x.name = e.target.value), "name")} />
                    </Field>
                    <Field label={t("configurazione.common.chiave")} hint={t("configurazione.processEditor.usataNellaDefinizione")}>
                      <input
                        className="field font-mono text-[12.5px]"
                        value={g.key}
                        onChange={(e) => {
                          const k = e.target.value.toLowerCase().replaceAll(/[^a-z0-9_]/g, "_");
                          change((d) => {
                            d.gates![i].key = k;
                            for (const t of d.transitions) if (t.gates) t.gates = t.gates.map((x) => (x === g.key ? k : x));
                          }, `g${i}.key`);
                        }}
                      />
                    </Field>
                  </div>
                  <div className="flex flex-wrap gap-4 text-[13px]" role="radiogroup" aria-label={t("configurazione.processEditor.tipoDiGate")}>
                    <label className="inline-flex items-center gap-1.5">
                      <input
                        type="radio"
                        checked={!g.check}
                        onChange={() =>
                          set((x) => {
                            delete x.check;
                            x.condition ??= "";
                          }, "kind")
                        }
                      />
                      {t("configurazione.processEditor.condizioneSuiDati")}
                    </label>
                    <label className="inline-flex items-center gap-1.5">
                      <input
                        type="radio"
                        checked={!!g.check}
                        disabled={meta.checks.length === 0}
                        onChange={() =>
                          set((x) => {
                            delete x.condition;
                            x.check = meta.checks[0];
                          }, "kind")
                        }
                      />
                      {t("configurazione.processEditor.controlloRegistratoDaUn")}
                    </label>
                  </div>
                  {g.check ? (
                    <Field label={t("configurazione.processEditor.controllo")}>
                      <Select className="field" value={g.check} onChange={(e) => set((x) => (x.check = e.target.value), "check")}>
                        {!meta.checks.includes(g.check) && <option value={g.check}>{t("configurazione.processEditor.unavailableName", { name: g.check })}</option>}
                        {meta.checks.map((c) => (
                          <option key={c} value={c}>
                            {humanFact(c)}
                          </option>
                        ))}
                      </Select>
                    </Field>
                  ) : (
                    <ConditionBuilder
                      value={g.condition ?? ""}
                      onChange={(v) => set((x) => (x.condition = v), "condition")}
                      facts={meta.facts}
                      type={meta.type.key}
                      sample={meta.sample}
                      empty={t("configurazione.processEditor.vaAggiuntaAlmenoUna")}
                      extraValues={extraValues}
                    />
                  )}
                  <Field label={t("configurazione.processEditor.messaggioQuandoNonE")}>
                    <input
                      className="field"
                      value={g.message ?? ""}
                      placeholder={t("configurazione.processEditor.esServeUnaStima")}
                      onChange={(e) => set((x) => (x.message = e.target.value), "message")}
                    />
                  </Field>
                  {users.length > 0 && (
                    <p className="m-0 text-[12.5px] text-muted">{t("configurazione.processEditor.usedByNames", { names: users.map((x) => tr("configurazione.processEditor.quoted", { text: x.label || x.to })).join(", ") })}</p>
                  )}
                  <ProblemNotes problems={ps} />
                  <TwoStepDelete
                    label={t("configurazione.processEditor.eliminaIlGate")}
                    detail={users.length ? t("configurazione.processEditor.alsoRemovesFromTransitions", { count: users.length }) : undefined}
                    onConfirm={() => {
                      change((d) => {
                        d.gates!.splice(i, 1);
                        for (const t of d.transitions) if (t.gates) t.gates = t.gates.filter((x) => x !== g.key);
                      });
                      setOpen(null);
                    }}
                  />
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Regole: ognuna si legge e si compila come una frase «Quando… solo se… allora…» (RuleSentence).

function Rules({
  def,
  meta,
  open,
  setOpen,
  problems,
  change,
  extraValues,
}: {
  def: Definition;
  meta: ProcessMeta;
  open: number | null;
  setOpen: (i: number | null) => void;
  problems: Map<number, Problem[]>;
  change: Change;
  extraValues: Record<string, string[]>;
}) {
  const t = useT();
  const refs = useRuleRefs();
  const rules = def.rules ?? [];
  const names = { roles: meta.roles, schemas: meta.actions, facts: factNames(meta.facts) };
  const add = () => {
    const key = keyFrom(t("configurazione.processEditor.nuovaRegola"), rules.map((r) => r.key));
    change((d) => {
      d.rules = [...(d.rules ?? []), { key, name: t("configurazione.processEditor.nuovaRegola2"), on: "create", actions: [] }];
    });
    setOpen(rules.length);
  };

  // Arrivo da un altro punto: «Aggiungi una regola» del tipo di valutazione (?nuova=open_review:<tipo>)
  // oppure il link a una regola (?regola=<chiave>).
  const search = useSearch({ strict: false }) as { nuova?: string; regola?: string };
  const navigate = useNavigate();
  const done = useRef("");
  useEffect(() => {
    if (search.nuova && done.current !== `n:${search.nuova}` && refs.loaded) {
      done.current = `n:${search.nuova}`;
      const [kind, ref] = search.nuova.split(":");
      const type = refs.reviewTypes.find((x) => x.key === ref);
      const phase = def.phases.find((p) => /valutaz|assess|review/i.test(p.name))?.name;
      const key = keyFrom(type?.key ?? kind ?? "regola", rules.map((r) => r.key));
      change((d) => {
        d.rules = [
          ...(d.rules ?? []),
          { key, name: t("configurazione.regole.apriValutazioneNome", { name: type?.name ?? ref ?? "" }), on: phase ? `enter:${phase}` : "create", actions: [{ kind: kind || "open_review", ref }] },
        ];
      });
      setOpen(rules.length);
      void navigate({ search: ((s: Record<string, unknown>) => ({ ...s, nuova: undefined })) as never, replace: true });
      setTimeout(() => document.getElementById("editor-rules")?.scrollIntoView({ block: "start" }), 50);
    } else if (search.regola && done.current !== `r:${search.regola}`) {
      const i = rules.findIndex((r) => r.key === search.regola);
      if (i < 0) return;
      done.current = `r:${search.regola}`;
      setOpen(i);
      setTimeout(() => document.getElementById("editor-rules")?.scrollIntoView({ block: "start" }), 50);
    }
  }, [search.nuova, search.regola, refs.loaded]); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <section id="editor-rules" className="flex scroll-mt-4 flex-col gap-2">
      <SectionTitle
        aside={
          <button className="min-h-11 text-[12.5px] font-semibold text-accent hover:underline md:min-h-0" onClick={add}>
            {t("configurazione.processEditor.nuovaRegola3")}
          </button>
        }
      >
        {t("configurazione.common.regole")}
      </SectionTitle>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">{t("configurazione.regole.intro")}</p>
      {rules.length === 0 && <p className="m-0 text-muted">{t("configurazione.processEditor.nessunaRegola")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {rules.map((r, i) => {
          const ps = problems.get(i) ?? [];
          const isOpen = open === i;
          const set = (fn: (x: NonNullable<Definition["rules"]>[number]) => void, key: string) => change((d) => fn(d.rules![i]), `r${i}.${key}`);
          return (
            <li key={i} className={`border-b border-hair ${isOpen ? "bg-sel/40" : ""}`} data-testid="regola">
              <button className="flex min-h-11 w-full flex-col gap-0.5 py-2 text-left" aria-expanded={isOpen} onClick={() => setOpen(isOpen ? null : i)}>
                <span className="flex flex-wrap items-baseline gap-x-3">
                  <span className="font-medium">{r.name || t("configurazione.processEditor.senzaNome")}</span>
                  {ps.some((p) => p.level === "error") && <span className="text-[12px] text-danger">{t("configurazione.regole.daCorreggere")}</span>}
                </span>
                <span className="text-[13px] text-muted" data-testid="regola-frase-elenco">
                  {ruleSentence(t, r, refs, names)}
                </span>
              </button>
              {isOpen && (
                <div className="flex max-w-2xl flex-col gap-4 pb-4">
                  <RuleSentence
                    rule={r}
                    def={def}
                    meta={meta}
                    refs={refs}
                    extraValues={extraValues}
                    setRule={set}
                    renderOther={(a, j) => (
                      <ActionEditor
                        action={a}
                        schema={meta.actions.find((s) => s.kind === a.kind)}
                        schemas={meta.actions}
                        meta={meta}
                        phases={def.phases.map((p) => p.name)}
                        onChange={(na) => change((d) => (d.rules![i].actions[j] = na), `r${i}.a${j}`)}
                        onRemove={() => change((d) => d.rules![i].actions.splice(j, 1))}
                      />
                    )}
                  />
                  <details className="text-[13px]">
                    <summary className="min-h-11 cursor-pointer py-2 text-muted">{t("configurazione.regole.avanzate")}</summary>
                    <div className="grid gap-3 sm:grid-cols-[1fr_200px]">
                      <Field label={t("configurazione.regole.nomeRegola")}>
                        <input className="field" value={r.name} onChange={(e) => set((x) => (x.name = e.target.value), "name")} />
                      </Field>
                      <Field label={t("configurazione.common.chiave")}>
                        <input
                          className="field font-mono text-[12.5px]"
                          value={r.key}
                          onChange={(e) => set((x) => (x.key = e.target.value.toLowerCase().replaceAll(/[^a-z0-9_]/g, "_")), "key")}
                        />
                      </Field>
                    </div>
                  </details>
                  <ProblemNotes problems={ps} />
                  <TwoStepDelete
                    label={t("configurazione.processEditor.eliminaLaRegola")}
                    onConfirm={() => {
                      change((d) => d.rules!.splice(i, 1));
                      setOpen(null);
                    }}
                  />
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

/** Un'azione di una regola: modulo dallo schema registrato, oppure chiave/valore per i tipi sconosciuti. */
function ActionEditor({
  action,
  schema,
  schemas,
  meta,
  phases,
  onChange,
  onRemove,
}: {
  action: RuleAction;
  schema: ActionSchema | undefined;
  schemas: ActionSchema[];
  meta: ProcessMeta;
  phases: string[];
  onChange: (a: RuleAction) => void;
  onRemove: () => void;
}) {
  const t = useT();
  const a = action as Record<string, unknown>;
  const setKey = (k: string, v: unknown) => {
    const n = { ...a };
    if (v === "" || v === undefined || v === false) delete n[k];
    else n[k] = v;
    onChange(n as RuleAction);
  };
  const oneOf = schema?.one_of ?? [];
  const [pick, setPick] = useState<string | undefined>(undefined);
  // Alternativa scelta: quella valorizzata, altrimenti quella indicata con il pulsante di scelta.
  const chosen = oneOf.find((k) => a[k] !== undefined && a[k] !== "") ?? pick ?? oneOf[0];
  const input = (f: ActionSchema["fields"][number]) => {
    const v = a[f.key];
    const label = { "aria-label": f.name };
    switch (f.kind) {
      case "role":
        return (
          <Select {...label} className="field py-1.5" value={String(v ?? "")} onChange={(e) => setKey(f.key, e.target.value)}>
            <option value="">{t("configurazione.common.scegli")}</option>
            {meta.roles.map((r) => (
              <option key={r.key} value={r.key}>
                {r.name}
              </option>
            ))}
          </Select>
        );
      case "group":
        return (
          <Select {...label} className="field py-1.5" value={String(v ?? "")} onChange={(e) => setKey(f.key, e.target.value)}>
            <option value="">{t("configurazione.common.scegli")}</option>
            {v !== undefined && v !== "" && !meta.groups.some((g) => g.name === v) && <option value={String(v)}>{t("configurazione.processEditor.missingName", { name: String(v) })}</option>}
            {meta.groups.map((g) => (
              <option key={g.key} value={g.name}>
                {g.name}
              </option>
            ))}
          </Select>
        );
      case "member":
        return (
          <EntitySelect kind="member" noneLabel={t("configurazione.common.scegli")} {...label} className="field py-1.5" value={String(v ?? "")} onChange={(v) => setKey(f.key, v)} />
        );
      case "phase":
        return (
          <Select {...label} className="field py-1.5" value={String(v ?? "")} onChange={(e) => setKey(f.key, e.target.value)}>
            <option value="">{t("configurazione.common.scegli")}</option>
            {phases.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </Select>
        );
      case "bool":
        return null;
      default:
        return <input {...label} className="field py-1.5" value={String(v ?? "")} onChange={(e) => setKey(f.key, e.target.value)} />;
    }
  };
  return (
    <div className="flex flex-col gap-2 border-l-2 border-hair pl-3">
      <div className="flex items-center gap-2">
        <Select
          aria-label={t("configurazione.processEditor.tipoDiAzione")}
          className="field w-auto py-1.5"
          value={action.kind}
          onChange={(e) => onChange({ kind: e.target.value })}
        >
          {!schema && <option value={action.kind}>{t("configurazione.processEditor.undescribedKind", { name: action.kind })}</option>}
          {schemas.map((s) => (
            <option key={s.kind} value={s.kind}>
              {s.name}
            </option>
          ))}
        </Select>
        <button type="button" className="ml-auto text-[12.5px] text-danger hover:underline" onClick={onRemove}>
          {t("configurazione.common.togli")}
        </button>
      </div>
      {schema?.description && <span className="text-[12px] text-muted">{schema.description}</span>}
      {schema ? (
        <>
          {schema.fields
            .filter((f) => !oneOf.includes(f.key))
            .map((f) =>
              f.kind === "bool" ? (
                <label key={f.key} className="inline-flex items-center gap-2 text-[13px]" title={f.help}>
                  <input type="checkbox" checked={!!a[f.key]} onChange={(e) => setKey(f.key, e.target.checked)} />
                  {f.name}
                </label>
              ) : (
                <Field key={f.key} label={f.name + (f.required ? "" : t("configurazione.processEditor.facoltativo"))} hint={f.help}>
                  {input(f)}
                </Field>
              ),
            )}
          {oneOf.length > 0 && (
            <div className="flex flex-col gap-1.5">
              <div className="flex flex-wrap gap-4 text-[13px]" role="radiogroup" aria-label={t("configurazione.processEditor.assegnaA")}>
                {oneOf.map((k) => (
                  <label key={k} className="inline-flex items-center gap-1.5">
                    <input
                      type="radio"
                      checked={chosen === k}
                      onChange={() => {
                        // Il valore si sceglie subito dopo, nel campo che compare.
                        const n = { ...a };
                        for (const o of oneOf) delete n[o];
                        setPick(k);
                        onChange(n as RuleAction);
                      }}
                    />
                    {schema.fields.find((f) => f.key === k)?.name ?? k}
                  </label>
                ))}
              </div>
              {chosen && schema.fields.find((f) => f.key === chosen) && input(schema.fields.find((f) => f.key === chosen)!)}
            </div>
          )}
        </>
      ) : (
        <GenericAction action={a} onChange={(n) => onChange(n as RuleAction)} />
      )}
    </div>
  );
}

/** Modulo generico chiave/valore per i tipi di azione che l'editor non conosce. */
function GenericAction({ action, onChange }: { action: Record<string, unknown>; onChange: (a: Record<string, unknown>) => void }) {
  const t = useT();
  const entries = Object.entries(action).filter(([k]) => k !== "kind");
  const [newKey, setNewKey] = useState("");
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-[12px] text-muted">{t("configurazione.processEditor.tipoDiAzioneSenza")}</span>
      {entries.map(([k, v]) => (
        <div key={k} className="flex items-center gap-1.5">
          <span className="w-32 shrink-0 truncate font-mono text-[12px]">{k}</span>
          <input
            className="field py-1"
            aria-label={t("configurazione.processEditor.valueOf", { key: k })}
            value={typeof v === "string" ? v : JSON.stringify(v)}
            onChange={(e) => {
              let val: unknown = e.target.value;
              if (val === "true" || val === "false") val = val === "true";
              onChange({ ...action, [k]: val });
            }}
          />
          <button
            type="button"
            className="btn px-2 py-1"
            aria-label={t("configurazione.common.removeNamed", { name: k })}
            onClick={() => {
              const n = { ...action };
              delete n[k];
              onChange(n);
            }}
          >
            ×
          </button>
        </div>
      ))}
      <div className="flex items-center gap-1.5">
        <input className="field w-40 py-1 font-mono text-[12px]" placeholder={t("configurazione.processEditor.nuovaChiave")} value={newKey} onChange={(e) => setNewKey(e.target.value.trim())} />
        <button
          type="button"
          className="btn px-2.5 py-1"
          disabled={!newKey || newKey === "kind" || newKey in action}
          onClick={() => {
            onChange({ ...action, [newKey]: "" });
            setNewKey("");
          }}
        >
          {t("configurazione.common.aggiungi")}
        </button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Cellulare: elenco modificabile al posto del diagramma

function PhaseList({
  def,
  sel,
  onSelect,
  lost,
  errors,
  transitionErrors,
  change,
}: {
  def: Definition;
  sel: Selection;
  onSelect: (s: Selection) => void;
  lost: Set<string>;
  errors: Set<string>;
  transitionErrors: Set<number>;
  change: Change;
}) {
  const t = useT();
  const main = def.phases.filter((p) => !p.off_path);
  const off = def.phases.filter((p) => p.off_path);
  const anyT = def.transitions.map((t, i) => [t, i] as const).filter(([t]) => t.from.includes(ANY));
  const item = (p: Definition["phases"][number], i: number, list: typeof main) => {
    const out = def.transitions.map((t, j) => [t, j] as const).filter(([t]) => t.from.some((f) => eq(f, p.name)));
    const selected = sel?.kind === "phase" && sel.name === p.name;
    return (
      <li key={p.name} className="border-b border-hair py-2">
        <div className="flex items-center gap-2">
          {p.color && <span className="size-2.5 shrink-0" style={{ background: p.color }} aria-hidden="true" />}
          <button className={`min-w-0 flex-1 text-left ${selected ? "text-accent" : ""}`} aria-pressed={selected} onClick={() => onSelect({ kind: "phase", name: p.name })}>
            <span className="font-semibold">{p.name}</span>
            <span className={`ml-2 text-[12px] ${lost.has(p.name) || errors.has(p.name) ? "text-danger" : "text-muted"}`}>
              {[categoryName(p.category), eq(def.initial, p.name) && "inizio", lost.has(p.name) && "irraggiungibile", errors.has(p.name) && "errori"]
                .filter(Boolean)
                .join(" · ")}
            </span>
          </button>
          {!p.off_path && (
            <>
              <button className="btn px-2 py-0.5" aria-label={t("configurazione.processEditor.moveEarlier", { name: p.name })} disabled={i === 0} onClick={() => change((d) => movePhase(d, p.name, i - 1))}>
                ↑
              </button>
              <button
                className="btn px-2 py-0.5"
                aria-label={t("configurazione.processEditor.moveLater", { name: p.name })}
                disabled={i === list.length - 1}
                onClick={() => change((d) => movePhase(d, p.name, i + 1))}
              >
                ↓
              </button>
            </>
          )}
        </div>
        {out.length > 0 && (
          <ul className="m-0 mt-1 flex list-none flex-col gap-0.5 p-0 pl-4">
            {out.map(([t, j]) => (
              <TransitionItem key={j} t={t} index={j} sel={sel} onSelect={onSelect} error={transitionErrors.has(j)} />
            ))}
          </ul>
        )}
      </li>
    );
  };
  return (
    <div className="flex flex-col gap-3">
      <ol className="m-0 list-none p-0">{main.map((p, i) => item(p, i, main))}</ol>
      {anyT.length > 0 && (
        <div>
          <h4 className="label m-0 mb-1">{t("configurazione.processEditor.daQualunqueFaseNon")}</h4>
          <ul className="m-0 flex list-none flex-col gap-0.5 p-0 pl-4">
            {anyT.map(([t, j]) => (
              <TransitionItem key={j} t={t} index={j} sel={sel} onSelect={onSelect} error={transitionErrors.has(j)} />
            ))}
          </ul>
        </div>
      )}
      {off.length > 0 && (
        <div>
          <h4 className="label m-0 mb-1">{t("configurazione.processEditor.fuoriPercorso")}</h4>
          <ul className="m-0 list-none p-0">{off.map((p, i) => item(p, i, off))}</ul>
        </div>
      )}
    </div>
  );
}

function TransitionItem({
  t,
  index,
  sel,
  onSelect,
  error,
}: {
  t: Definition["transitions"][number];
  index: number;
  sel: Selection;
  onSelect: (s: Selection) => void;
  error: boolean;
}) {
  const selected = sel?.kind === "transition" && sel.index === index;
  return (
    <li>
      <button
        className={`text-left text-[12.5px] ${selected ? "font-semibold text-accent" : error ? "text-danger" : ""}`}
        aria-pressed={selected}
        onClick={() => onSelect({ kind: "transition", index })}
      >
        → {t.to} <span className={selected ? "" : "text-muted"}>· {t.label || tr("configurazione.processEditor.senzaEtichetta")}</span>
        {t.system && <span className="text-muted"> {tr("configurazione.processEditor.diSistema")}</span>}
        {!!t.gates?.length && <span className="text-muted">{tr("configurazione.processEditor.gateCount", { count: t.gates.length })}</span>}
      </button>
    </li>
  );
}
