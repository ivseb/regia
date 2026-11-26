import { Select } from "../../select";
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, type Problem, type QTemplate, type ReviewOutcome, type TemplateVersion } from "../../api";
import type { QDefinition } from "../../condition";
import { reviewsApi } from "../../reviewsApi";
import { ErrorNote, SectionTitle, date, Loading } from "../../ui";
import { ScoringPanel, RulesPanel } from "./Scoring";
import { Preview } from "./Preview";
import { Field, Structure, type Change } from "./Structure";
import { skeleton, analyze, clean, describeDiff, normalize, type Finding } from "./model";
import { useT } from "../../i18n/react";
import { tr } from "../../i18n/core";
import { fmtTime } from "../../i18n/format";

// Editor visuale dei modelli di questionario (piano, passo 7). Come l'editor dei processi:
// lavora su una copia, valida dal vivo, salva da solo nella bozza e pubblica con le differenze
// in chiaro. Il JSON resta come vista avanzata.

type Tab = "struttura" | "punteggio" | "regole" | "anteprima" | "versioni" | "json";
const TAB_NAMES: Record<Tab, string> = {
  get struttura() { return tr("configurazione.qEditor.tab.struttura"); },
  get punteggio() { return tr("configurazione.qEditor.tab.punteggio"); },
  get regole() { return tr("configurazione.qEditor.tab.regole"); },
  get anteprima() { return tr("configurazione.qEditor.tab.anteprima"); },
  get versioni() { return tr("configurazione.qEditor.tab.versioni"); },
  get json() { return tr("configurazione.qEditor.tab.json"); },
};

function useMedia(q: string) {
  const [on, setOn] = useState(() => typeof matchMedia !== "undefined" && matchMedia(q).matches);
  useEffect(() => {
    const m = matchMedia(q);
    const f = () => setOn(m.matches);
    m.addEventListener("change", f);
    return () => m.removeEventListener("change", f);
  }, [q]);
  return on;
}

export function QuestionnaireEditor({ templateKey, canEdit, outcomes }: { templateKey: string; canEdit: boolean; outcomes: ReviewOutcome[] }) {
  const tpl = useQuery({ queryKey: ["review-template", templateKey], queryFn: () => reviewsApi.template(templateKey), retry: false });
  const [epoch, setEpoch] = useState(0);
  const isNew = tpl.error instanceof ApiError && tpl.error.status === 404;
  if (tpl.isPending) return <Loading className="m-0 text-muted" />;
  if (tpl.error && !isNew) return <ErrorNote error={tpl.error} />;
  const t = tpl.data;
  const initial = normalize(t?.draft?.definition ?? t?.current?.definition ?? skeleton());
  return <Editor key={`${templateKey}:${epoch}`} templateKey={templateKey} canEdit={canEdit} outcomes={outcomes} tpl={t} isNew={isNew} initial={initial} onReset={() => setEpoch((e) => e + 1)} />;
}

function Editor({
  templateKey,
  canEdit,
  outcomes,
  tpl,
  isNew,
  initial,
  onReset,
}: {
  templateKey: string;
  canEdit: boolean;
  outcomes: ReviewOutcome[];
  tpl?: QTemplate;
  isNew: boolean;
  initial: QDefinition;
  onReset: () => void;
}) {
  const t = useT();
  const qc = useQueryClient();
  const wide = useMedia("(min-width: 1280px)");
  const [def, setDef] = useState<QDefinition>(initial);
  const [notes, setNotes] = useState(tpl?.draft?.notes ?? "");
  const [history, setHistory] = useState<QDefinition[]>([]);
  const [future, setFuture] = useState<QDefinition[]>([]);
  const [tab, setTab] = useState<Tab>(canEdit ? "struttura" : "anteprima");
  const [open, setOpen] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const [autosave, setAutosave] = useState(true);
  const [saved, setSaved] = useState<Date | null>(null);
  const [scope, setScope] = useState<"request" | "impact">("request");
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
    if (!coalesce || last.current.key !== coalesce || now - last.current.at > 1500) setHistory((h) => [...h.slice(-49), def]);
    last.current = { key: coalesce ?? "", at: now };
    setFuture([]);
    setDef(n);
    setDirty(true);
  };
  const replace = (d: QDefinition) =>
    change((x) => {
      for (const k of Object.keys(x)) delete (x as Record<string, unknown>)[k];
      Object.assign(x, structuredClone(d));
    });
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

  // Dati per il costruttore delle condizioni.
  const factsQ = useQuery({ queryKey: ["review-template-facts"], queryFn: reviewsApi.templateFacts, enabled: canEdit, staleTime: 120_000 });
  const baseFacts = useMemo(() => factsQ.data?.[scope] ?? [], [factsQ.data, scope]);
  const outcomeName = (k: string) => outcomes.find((o) => o.key === k)?.label ?? k;

  // Validazione dal vivo: i controlli locali subito, quelli del server (gli stessi della pubblicazione) dopo una pausa.
  const knownKeys = useMemo(() => [...(factsQ.data?.request ?? []), ...(factsQ.data?.impact ?? [])].map((f) => f.key), [factsQ.data]);
  const local = useMemo(() => analyze(def, knownKeys), [def, knownKeys]);
  const [server, setServer] = useState<Problem[]>(tpl?.draft_problems ?? []);
  const [serverError, setServerError] = useState<unknown>(null);
  useEffect(() => {
    if (!canEdit) return;
    const snapshot = def;
    const t = setTimeout(() => {
      reviewsApi.validateTemplate(clean(snapshot)).then(
        (r) => (defRef.current === snapshot && setServer(r.problems), setServerError(null)),
        setServerError,
      );
    }, 400);
    return () => clearTimeout(t);
  }, [def, canEdit]);
  const findings = useMemo<Finding[]>(() => {
    const dup = (p: Problem) => local.some((l) => l.path === p.path && l.level === p.level);
    return [...local, ...server.filter((p) => !dup(p)).map((p) => ({ level: p.level, path: p.path, message: p.message, code: "server" }))];
  }, [local, server]);
  const problems = (prefix: string) => findings.filter((f) => f.path === prefix || f.path.startsWith(`${prefix}.`) || f.path.startsWith(`${prefix}[`));
  const errors = findings.filter((f) => f.level === "error").length;
  const warnings = findings.length - errors;

  const save = useMutation({
    mutationFn: async () => {
      const snapshot = defRef.current;
      const r = await reviewsApi.saveTemplateDraft(templateKey, clean(snapshot), notesRef.current);
      return { r, snapshot };
    },
    onSuccess: ({ r, snapshot }) => {
      qc.setQueryData(["review-template", templateKey], r);
      qc.invalidateQueries({ queryKey: ["review-config"] });
      if (defRef.current === snapshot) setDirty(false);
      setSaved(new Date());
    },
  });
  useEffect(() => {
    if (!dirty || !autosave || save.isPending || !canEdit) return;
    const t = setTimeout(() => save.mutate(), 2000);
    return () => clearTimeout(t);
  }, [def, notes, dirty, autosave, save.isPending, canEdit]);
  // Uscendo con modifiche non salvate: si salvano comunque nella bozza.
  useEffect(
    () => () => {
      if (dirtyRef.current)
        reviewsApi.saveTemplateDraft(templateKey, clean(defRef.current), notesRef.current).then((r) => {
          qc.setQueryData(["review-template", templateKey], r);
          qc.invalidateQueries({ queryKey: ["review-config"] });
        });
    },
    [templateKey, qc],
  );
  const publish = useMutation({
    mutationFn: async () => {
      if (dirtyRef.current || isNew || !tpl?.draft) await save.mutateAsync();
      return reviewsApi.publishTemplate(templateKey, notesRef.current);
    },
    onSuccess: (r) => {
      qc.setQueryData(["review-template", templateKey], r);
      qc.invalidateQueries({ queryKey: ["review-config"] });
      setDirty(false);
    },
  });
  const discard = useMutation({
    mutationFn: () => reviewsApi.discardTemplateDraft(templateKey),
    onSuccess: async () => {
      setDirty(false);
      dirtyRef.current = false;
      await qc.invalidateQueries({ queryKey: ["review-template", templateKey] });
      qc.invalidateQueries({ queryKey: ["review-config"] });
      onReset();
    },
  });

  const go = (path: string) => {
    const m = /^sections\[(\d+)\](?:\.questions\[(\d+)\])?/.exec(path);
    if (m) {
      const s = def.sections[Number(m[1])];
      const q = m[2] !== undefined ? s?.questions[Number(m[2])] : undefined;
      setTab("struttura");
      if (q) setOpen(q.key);
      setTimeout(() => document.getElementById(q ? `dq-${q.key}` : `ds-${s?.key}`)?.scrollIntoView({ behavior: "smooth", block: "center" }), 60);
    } else if (path.startsWith("derivations")) setTab("regole");
    else if (path.startsWith("scoring")) setTab("punteggio");
  };
  const goQuestion = (key: string) => {
    setTab("struttura");
    setOpen(key);
    setTimeout(() => document.getElementById(`dq-${key}`)?.scrollIntoView({ behavior: "smooth", block: "center" }), 60);
  };

  const tabs: Tab[] = canEdit ? ["struttura", "punteggio", "regole", ...(wide ? [] : (["anteprima"] as Tab[])), "versioni", "json"] : ["anteprima", "versioni"];
  const current = !wide && tab === "anteprima" ? "anteprima" : tab === "anteprima" && wide ? "struttura" : tab;
  const showPreview = canEdit ? wide : false;

  const panel = (
    <>
      {current === "struttura" && (
        <div className="flex flex-col gap-6">
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <Field label={t("configurazione.qEditor.nomeDelModello")}>
              <input className="field" value={def.name} onChange={(e) => change((d) => (d.name = e.target.value), "name")} />
            </Field>
            <Field label={t("configurazione.common.descrizione")}>
              <input className="field" value={def.description ?? ""} onChange={(e) => change((d) => (d.description = e.target.value), "desc")} />
            </Field>
          </div>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12.5px] text-muted">
            <label className="inline-flex items-center gap-2">
              {t("configurazione.qEditor.leCondizioniLeggonoI")}
              <Select className="field w-auto py-1" value={scope} onChange={(e) => setScope(e.target.value as "request" | "impact")} aria-label={t("configurazione.qEditor.datiDisponibiliAlleCondizioni")}>
                <option value="request">{t("configurazione.qEditor.unaValutazioneSullaRichiesta")}</option>
                <option value="impact">{t("configurazione.qEditor.unaValutazioneSuUn")}</option>
              </Select>
            </label>
            {factsQ.error && <span className="text-danger">{t("configurazione.qEditor.datiNonDisponibiliLe")}</span>}
          </div>
          <Structure def={def} change={change} baseFacts={baseFacts} problems={problems} open={open} setOpen={setOpen} />
        </div>
      )}
      {current === "punteggio" && <ScoringPanel def={def} change={change} outcomes={outcomes} problems={findings} goQuestion={goQuestion} />}
      {current === "regole" && <RulesPanel def={def} change={change} baseFacts={baseFacts} problems={problems} />}
      {current === "anteprima" && <Preview def={def} outcomes={outcomes} facts={baseFacts} />}
      {current === "versioni" && (
        <Versions
          templateKey={templateKey}
          tpl={tpl}
          def={def}
          canEdit={canEdit}
          notes={notes}
          setNotes={(n) => (setNotes(n), setDirty(true))}
          errors={errors}
          dirty={dirty}
          outcomeName={outcomeName}
          publish={publish}
          discard={discard}
          onRestore={(d) => (replace(normalize(d)), setTab("struttura"))}
        />
      )}
      {current === "json" && <JsonView def={def} onApply={(d) => (replace(normalize(d)), setTab("struttura"))} canEdit={canEdit} />}
    </>
  );

  return (
    <div
      className="flex flex-col gap-5"
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
      <datalist id="quest-facts">
        {baseFacts.map((f) => (
          <option key={f.key} value={f.key} />
        ))}
      </datalist>
      <SectionTitle
        aside={
          tpl?.current ? (
            <span className="font-mono text-[12px] text-muted">
              {t("configurazione.qEditor.publishedOn", { version: tpl.current.version, date: date(tpl.current.published_at) })}
            </span>
          ) : undefined
        }
      >
        {def.name || templateKey} <span className="font-mono text-[12px] font-normal normal-case tracking-normal text-muted">{templateKey}</span>
      </SectionTitle>
      {isNew && <p className="m-0 -mt-2 text-muted">{t("configurazione.qEditor.newTemplate", { key: templateKey })}</p>}

      {canEdit && (
        <div className="flex flex-wrap items-center gap-2">
          <button className="btn" onClick={undo} disabled={!history.length} title={t("configurazione.common.annullaZ")}>
            {t("configurazione.common.undo")}
          </button>
          <button className="btn" onClick={redo} disabled={!future.length} title={t("configurazione.common.ripetiZ")}>
            {t("configurazione.common.ripeti")}
          </button>
          <span className="ml-auto flex flex-wrap items-center gap-3 text-[12.5px]">
            <button type="button" aria-live="polite" className={`underline ${errors ? "text-danger" : warnings ? "text-flag" : "text-muted"}`} onClick={() => findings[0] && go(findings[0].path)}>
              {errors ? t("configurazione.qEditor.errorCount", { count: errors }) : warnings ? t("configurazione.qEditor.warningCount", { count: warnings }) : t("configurazione.qEditor.nessunProblema")}
            </button>
            <span aria-live="polite" className="text-muted">
              {save.isPending
                ? t("configurazione.qEditor.savingShort")
                : dirty
                  ? t("configurazione.common.modificheNonSalvate")
                  : saved
                    ? t("configurazione.qEditor.draftSavedAt", { time: fmtTime(saved, { hour: "2-digit", minute: "2-digit" }) })
                    : tpl?.draft
                      ? t("configurazione.processEditor.draftVersion", { version: tpl.draft.version })
                      : tpl?.current
                        ? t("configurazione.processEditor.sameAsVersion", { version: tpl.current.version })
                        : ""}
            </span>
            <label className="inline-flex items-center gap-1.5">
              <input type="checkbox" checked={autosave} onChange={(e) => setAutosave(e.target.checked)} />
              {t("configurazione.common.salvataggioAutomatico")}
            </label>
            <button className="btn" disabled={(!dirty && !isNew) || save.isPending} onClick={() => save.mutate()}>
              {t("configurazione.common.salvaBozza")}
            </button>
            <button className="btn btn-primary" onClick={() => setTab("versioni")}>
              {t("configurazione.qEditor.pubblica")}
            </button>
          </span>
        </div>
      )}
      <ErrorNote error={save.error} />
      <ErrorNote error={serverError} />

      <div role="tablist" aria-label={t("configurazione.qEditor.sezioniDellEditor")} className="flex gap-x-5 gap-y-1 overflow-x-auto border-b border-hair">
        {tabs.map((t) => (
          <button
            key={t}
            role="tab"
            aria-selected={current === t}
            className={`-mb-px shrink-0 border-b-2 px-0.5 py-2 ${current === t ? "border-accent font-semibold text-accent" : "border-transparent text-fg hover:text-accent"}`}
            onClick={() => setTab(t)}
          >
            {TAB_NAMES[t]}
          </button>
        ))}
      </div>

      {canEdit && findings.length > 0 && (current === "struttura" || current === "punteggio" || current === "regole") && <FindingList findings={findings} go={go} />}

      <div className={showPreview ? "grid grid-cols-[minmax(0,1fr)_minmax(0,480px)] gap-8" : ""}>
        <div className="min-w-0">{panel}</div>
        {showPreview && (
          <aside aria-label={t("configurazione.qEditor.anteprimaDalVivo")} className="min-w-0 border-l border-hair pl-6 xl:sticky xl:top-2 xl:max-h-[calc(100vh-16px)] xl:self-start xl:overflow-y-auto">
            <SectionTitle>{t("configurazione.qEditor.anteprimaDalVivo")}</SectionTitle>
            <div className="pt-3">
              <Preview def={def} outcomes={outcomes} facts={baseFacts} />
            </div>
          </aside>
        )}
      </div>
    </div>
  );
}

function FindingList({ findings, go }: { findings: Finding[]; go: (path: string) => void }) {
  const t = useT();
  const [all, setAll] = useState(false);
  const shown = all ? findings : findings.slice(0, 4);
  return (
    <ul className="m-0 flex list-none flex-col gap-0.5 border-l-2 border-hair p-0 pl-3 text-[12.5px]" aria-label={t("configurazione.qEditor.problemiDelQuestionario")}>
      {shown.map((f, i) => (
        <li key={i}>
          <button type="button" className={`text-left hover:underline ${f.level === "error" ? "text-danger" : "text-flag"}`} onClick={() => go(f.path)}>
            {f.level === "error" ? t("configurazione.common.errore") : t("configurazione.common.attenzione")}: {f.message}.
          </button>
        </li>
      ))}
      {findings.length > 4 && (
        <li>
          <button type="button" className="text-muted underline" onClick={() => setAll(!all)}>
            {all ? t("configurazione.qEditor.mostraMeno") : t("configurazione.qEditor.othersCount", { count: findings.length - 4 })}
          </button>
        </li>
      )}
    </ul>
  );
}

// ---------------------------------------------------------------------
// JSON (vista avanzata)
// ---------------------------------------------------------------------

function JsonView({ def, onApply, canEdit }: { def: QDefinition; onApply: (d: QDefinition) => void; canEdit: boolean }) {
  const t = useT();
  const [text, setText] = useState(() => JSON.stringify(clean(def), null, 2));
  const [error, setError] = useState<string | null>(null);
  const apply = () => {
    try {
      const d = JSON.parse(text) as QDefinition;
      if (!d || !Array.isArray(d.sections)) throw new Error(tr("configurazione.qEditor.needSections"));
      onApply(d);
    } catch (e) {
      setError((e as Error).message);
    }
  };
  return (
    <div className="flex flex-col gap-2">
      <p className="m-0 text-[12.5px] text-muted">
        {t("configurazione.qEditor.laDefinizioneCompletaPer")}
      </p>
      <textarea
        className="field min-h-[420px] font-mono text-[12px] leading-relaxed"
        spellCheck={false}
        readOnly={!canEdit}
        aria-label={t("configurazione.qEditor.definizioneDelModelloJson")}
        value={text}
        onChange={(e) => (setText(e.target.value), setError(null))}
      />
      {error && <p className="m-0 text-[12.5px] text-danger">JSON: {error}</p>}
      {canEdit && (
        <div>
          <button className="btn" onClick={apply}>
            {t("configurazione.qEditor.applica")}
          </button>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Versioni: elenco, differenze, pubblicazione, duplica, esporta e importa
// ---------------------------------------------------------------------

const FORMAT = "regia.questionnaire/1";

function download(name: string, data: unknown) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: "application/json" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function DiffList({ items }: { items: string[] }) {
  const t = useT();
  return items.length ? (
    <ul className="m-0 flex list-disc flex-col gap-0.5 pl-5 text-[13px]">
      {items.map((x, i) => (
        <li key={i}>{x}</li>
      ))}
    </ul>
  ) : (
    <p className="m-0 text-[13px] text-muted">{t("configurazione.qEditor.nessunaDifferenza")}</p>
  );
}

function VersionDiff({ templateKey, v, prev, outcomeName }: { templateKey: string; v: number; prev?: number; outcomeName: (k: string) => string }) {
  const a = useQuery({ queryKey: ["review-template-ver", templateKey, v], queryFn: () => reviewsApi.templateVersion(templateKey, v) });
  const b = useQuery({ queryKey: ["review-template-ver", templateKey, prev], queryFn: () => reviewsApi.templateVersion(templateKey, prev!), enabled: !!prev });
  if (a.isPending || (prev && b.isPending)) return <Loading className="m-0 text-[12.5px] text-muted" />;
  if (a.error || b.error) return <ErrorNote error={a.error ?? b.error} />;
  return <DiffList items={describeDiff(prev ? b.data?.definition : undefined, a.data!.definition!, outcomeName)} />;
}

function Versions({
  templateKey,
  tpl,
  def,
  canEdit,
  notes,
  setNotes,
  errors,
  dirty,
  outcomeName,
  publish,
  discard,
  onRestore,
}: {
  templateKey: string;
  tpl?: QTemplate;
  def: QDefinition;
  canEdit: boolean;
  notes: string;
  setNotes: (n: string) => void;
  errors: number;
  dirty: boolean;
  outcomeName: (k: string) => string;
  publish: { mutate: () => void; isPending: boolean; error: unknown };
  discard: { mutate: () => void; isPending: boolean; error: unknown };
  onRestore: (d: QDefinition) => void;
}) {
  const t = useT();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [openV, setOpenV] = useState<number | null>(null);
  const [newKey, setNewKey] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  const file = useRef<HTMLInputElement>(null);
  const diff = useMemo(() => describeDiff(tpl?.current?.definition, normalize(def), outcomeName), [tpl, def, outcomeName]);
  const next = tpl?.draft?.version ?? (tpl?.current ? tpl.current.version + 1 : 1);
  const dup = useMutation({
    mutationFn: () => reviewsApi.saveTemplateDraft(newKey, clean({ ...def, name: t("configurazione.qEditor.copySuffix", { name: def.name }) }), t("configurazione.qEditor.copyOf", { key: templateKey })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["review-config"] });
      nav({ to: "/configurazione/valutazioni", search: { tab: "modelli", modello: newKey } });
    },
  });
  const restore = useMutation({
    mutationFn: (n: number) => reviewsApi.templateVersion(templateKey, n),
    onSuccess: (v: TemplateVersion) => v.definition && onRestore(v.definition),
  });
  const keyOk = /^[a-z][a-z0-9_]{0,62}$/.test(newKey);
  const exportVersion = async (n?: number) => {
    const d = n ? (await reviewsApi.templateVersion(templateKey, n)).definition! : clean(def);
    download(`${templateKey}${n ? `-v${n}` : "-bozza"}.json`, { format: FORMAT, key: templateKey, name: d.name, version: n ?? null, definition: d });
  };
  const importFile = async (f: File | undefined) => {
    if (!f) return;
    setMsg(null);
    try {
      const j = JSON.parse(await f.text());
      const d = (j && typeof j === "object" && "definition" in j ? j.definition : j) as QDefinition;
      if (!d || !Array.isArray(d.sections)) throw new Error(tr("configurazione.qEditor.noQuestionnaire"));
      if (!confirm(t("configurazione.qEditor.confirmReplace", { name: d.name || f.name }))) return;
      onRestore(d);
    } catch (e) {
      setMsg(t("configurazione.qEditor.importFailed", { error: (e as Error).message }));
    } finally {
      if (file.current) file.current.value = "";
    }
  };
  const err = publish.error ?? discard.error ?? dup.error ?? restore.error;
  return (
    <div className="flex flex-col gap-8">
      {canEdit && (
        <section className="flex flex-col gap-3">
          <SectionTitle aside={<span className="font-mono text-[12px] text-flag">v{next}</span>}>{t("configurazione.common.pubblicazione")}</SectionTitle>
          <p className="m-0 text-[12.5px] text-muted">
            {t("configurazione.qEditor.leNuoveValutazioniUsano")}
          </p>
          <div className="flex flex-col gap-1.5">
            <span className="label">{tpl?.current ? t("configurazione.qEditor.whatChanges", { version: tpl.current.version }) : t("configurazione.qEditor.contenutoDellaPrimaVersione")}</span>
            {tpl?.current ? <DiffList items={diff} /> : <p className="m-0 text-[13px] text-muted">{t("configurazione.qEditor.saraLaPrimaVersione")}</p>}
          </div>
          <Field label={t("configurazione.common.noteDellaVersione")}>
            <input className="field" value={notes} placeholder={t("configurazione.qEditor.esAggiuntaLaSezione")} onChange={(e) => setNotes(e.target.value)} />
          </Field>
          {errors > 0 && <p className="m-0 text-[12.5px] text-danger">{t("configurazione.qEditor.thereAreErrors", { count: errors })}</p>}
          <div className="flex flex-wrap gap-2">
            <button className="btn btn-primary" disabled={errors > 0 || publish.isPending || (!dirty && !tpl?.draft)} onClick={() => publish.mutate()}>
              {t("configurazione.qEditor.publishV", { version: next })}
            </button>
            {tpl?.draft && (
              <button
                className="btn"
                disabled={discard.isPending}
                onClick={() => confirm(t("configurazione.qEditor.confirmDiscard")) && discard.mutate()}
              >
                {t("configurazione.common.scartaLaBozza")}
              </button>
            )}
          </div>
        </section>
      )}

      <section className="flex flex-col gap-2">
        <SectionTitle>{t("configurazione.common.versioni")}</SectionTitle>
        {tpl ? (
          <ul className="m-0 flex list-none flex-col p-0">
            {tpl.versions.map((v, i) => (
              <li key={v.id} className="flex flex-col gap-2 border-b border-hair py-2">
                <div className="grid grid-cols-[44px_88px_minmax(0,1fr)] items-baseline gap-x-2 gap-y-1 text-[13px] sm:grid-cols-[44px_88px_minmax(0,1fr)_auto]">
                  <span className="font-mono font-semibold">v{v.version}</span>
                  <span className={v.status === "published" ? "font-semibold text-accent" : v.status === "draft" ? "text-flag" : "text-muted"}>
                    {t(`configurazione.qEditor.status.${v.status}`)}
                  </span>
                  <span className="min-w-0 [overflow-wrap:anywhere] text-muted">{v.notes || (v.published_at ? t("configurazione.qEditor.publishedOnShort", { date: date(v.published_at) }) : "—")}</span>
                  <span className="col-span-full font-mono text-[12px] text-muted tabular sm:col-span-1">{t("configurazione.qEditor.reviewCount", { count: v.reviews })}</span>
                </div>
                <div className="flex flex-wrap gap-x-4 gap-y-1 text-[12.5px]">
                  <button type="button" className="text-accent hover:underline" aria-expanded={openV === v.version} onClick={() => setOpenV(openV === v.version ? null : v.version)}>
                    {openV === v.version ? t("configurazione.qEditor.nascondiLeDifferenze") : t("configurazione.qEditor.differenze")}
                  </button>
                  <button type="button" className="text-accent hover:underline" onClick={() => exportVersion(v.version)}>
                    {t("configurazione.common.esporta")}
                  </button>
                  {canEdit && v.status !== "draft" && (
                    <button type="button" className="text-accent hover:underline" disabled={restore.isPending} onClick={() => confirm(t("configurazione.qEditor.confirmRestore", { version: v.version })) && restore.mutate(v.version)}>
                      {t("configurazione.qEditor.riprendiNellaBozza")}
                    </button>
                  )}
                </div>
                {openV === v.version && (
                  <div className="border-l-2 border-hair pl-3">
                    <VersionDiff templateKey={templateKey} v={v.version} prev={tpl.versions[i + 1]?.version} outcomeName={outcomeName} />
                  </div>
                )}
              </li>
            ))}
          </ul>
        ) : (
          <p className="m-0 text-[13px] text-muted">{t("configurazione.qEditor.nessunaVersioneAncoraSalva")}</p>
        )}
      </section>

      {canEdit && (
        <section className="flex flex-col gap-3">
          <SectionTitle>{t("configurazione.qEditor.duplicaEsportaImporta")}</SectionTitle>
          <div className="flex flex-col gap-1.5">
            <span className="label">{t("configurazione.qEditor.duplicaComeNuovoModello")}</span>
            <div className="flex flex-wrap items-center gap-2">
              <input aria-label={t("configurazione.qEditor.chiaveDelNuovoModello")} className="field w-[220px] font-mono text-[12.5px]" placeholder="chiave_nuovo_modello" value={newKey} onChange={(e) => setNewKey(e.target.value)} />
              <button className="btn" disabled={!keyOk || dup.isPending} onClick={() => dup.mutate()}>
                {t("configurazione.common.duplica")}
              </button>
            </div>
            <span className="text-[12px] text-muted">{t("configurazione.qEditor.laCopiaNasceCome")}</span>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <button className="btn" onClick={() => exportVersion()}>
              {t("configurazione.qEditor.esportaLaBozzaJson")}
            </button>
            <button className="btn" onClick={() => file.current?.click()}>
              {t("configurazione.qEditor.importaDaFileJson")}
            </button>
            <input ref={file} type="file" accept="application/json,.json" className="hidden" aria-label={t("configurazione.qEditor.fileJsonDaImportare")} onChange={(e) => importFile(e.target.files?.[0])} />
          </div>
          {msg && <p className="m-0 text-[12.5px] text-danger">{msg}</p>}
        </section>
      )}
      <ErrorNote error={err} />
    </div>
  );
}
