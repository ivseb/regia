import { Select } from "../select";
import { Suspense, lazy, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, processEditorApi, type Definition, type ProcessInfo, type Problem, type VersionInfo } from "../api";
import { PageHeader } from "../shell";
import { ErrorNote, ROLE_NAMES, SectionTitle, StageGate, ago, date, Loading } from "../ui";
import { EmptyState } from "../emptyState";
import { ConditionBuilder } from "./processEditor/ConditionBuilder";
import { describeCondition, humanFact, keyFrom, priorityTies } from "./processEditor/model";
import { Miniatura } from "./processEditor/Miniatura";
import { phrase } from "../casa/gruppi";
import { useTypeFacts } from "../casaApi";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { tr } from "../i18n/core";
import { BozzaBar, useBozza } from "../casa/Bozza";
import { diffFields } from "../casa/differenze";
import { VersionFlow, VersionLifecycle, VigoreDates, type FlowOptions, type FlowPreview } from "../versioni";
import { ProcessGuides } from "./ProcessGuides";

// Configurazione dei processi (piano, passi 1 e 7): elenco con una spiegazione e la
// creazione di un processo; nel dettaglio l'editor visuale della bozza, la lettura
// della definizione in tabelle e, per i casi avanzati, il JSON.

// L'editor visuale si carica alla prima apertura (bundle iniziale più leggero).
const ProcessEditor = lazy(() => import("./processEditor/Editor"));

const CATEGORY: Record<string, string> = {
  get todo() { return tr("configurazione.processEditor.category.todo"); },
  get in_progress() { return tr("configurazione.processEditor.category.in_progress"); },
  get done() { return tr("configurazione.processEditor.category.done"); },
  get cancelled() { return tr("configurazione.processEditor.category.cancelled"); },
};
const STATUS: Record<VersionInfo["status"], string> = {
  get draft() { return tr("configurazione.processes.status.draft"); },
  get scheduled() { return tr("configurazione.versioni.lifecycle.scheduledShort"); },
  get published() { return tr("configurazione.processes.status.current"); },
  get superseded() { return tr("configurazione.processes.status.previous"); },
};

export function Processes() {
  const t = useT();
  const qc = useQueryClient();
  const nav = useNavigate();
  const list = useQuery({ queryKey: ["processes"], queryFn: api.processes });
  const file = useRef<HTMLInputElement>(null);
  const [creating, setCreating] = useState(false);
  const narrow = useNarrowList();
  const [view, setView] = useState<"elenco" | "mappa">("elenco");
  // Sul telefono la lista è una mappa di schede (la tabella a 6 colonne non ci sta); sul resto si sceglie.
  const mappa = narrow || view === "mappa";
  const imp = useMutation({
    mutationFn: async (f: File) => api.importProcess(JSON.parse(await f.text()), false),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["processes"] });
      nav({ to: "/processi/$key", params: { key: r.process.key } });
    },
  });
  return (
    <>
      <PageHeader
        path={t("configurazione.path.processes")}
        title={t("configurazione.processes.processi")}
        actions={
          <>
            <button className="btn btn-primary" onClick={() => setCreating(!creating)} aria-expanded={creating}>
              {t("configurazione.processes.nuovoProcesso")}
            </button>
            <input
              ref={file}
              type="file"
              accept="application/json,.json"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) imp.mutate(f);
                e.target.value = "";
              }}
            />
            <button className="btn" onClick={() => file.current?.click()} disabled={imp.isPending}>
              {t("configurazione.processes.importaDaJson")}
            </button>
          </>
        }
      />
      {creating && <NewProcess items={list.data?.items ?? []} onClose={() => setCreating(false)} />}
      <Intro />
      <div className="mx-4 md:mx-7 flex flex-col gap-2">
        <ErrorNote error={imp.error ?? list.error} />
        {imp.error instanceof SyntaxError && <p className="text-danger">{t("configurazione.common.ilFileNonE")}</p>}
      </div>
      {!narrow && (
        <div role="group" aria-label={t("configurazione.mappa.vista")} className="mx-4 mb-3 flex gap-2 md:mx-7">
          {([["elenco", t("configurazione.mappa.elenco")], ["mappa", t("configurazione.mappa.mappa")]] as const).map(([v, label]) => (
            <button key={v} type="button" className={`btn ${view === v ? "btn-primary" : ""}`} aria-pressed={view === v} onClick={() => setView(v)}>
              {label}
            </button>
          ))}
        </div>
      )}
      {mappa && list.data && <ProcessMap items={list.data.items} />}
      <div className={`overflow-x-auto px-4 pb-8 md:px-7 ${mappa ? "hidden" : ""}`}>
        <table className="w-full min-w-[720px] border-collapse tabular">
          <thead>
            <tr className="label h-9 text-left">
              <th scope="col" className="border-b-2 border-rule pr-3">{t("configurazione.processes.processo")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("configurazione.common.tipo")}</th>
              <th scope="col" className="border-b-2 border-rule px-3 text-right">{t("configurazione.processes.priorita")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("configurazione.processes.siApplicaQuando")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("configurazione.processes.versione")}</th>
              <th scope="col" className="border-b-2 border-rule pl-3 text-right">{t("configurazione.common.elementi")}</th>
            </tr>
          </thead>
          <tbody>
            {list.data?.items.map((p) => {
              const cur = p.versions.find((v) => v.status === "published");
              const draft = p.versions.find((v) => v.status === "draft");
              const total = p.versions.reduce((n, v) => n + v.elements, 0);
              // Richieste rimaste su una versione precedente: la migrazione è manuale.
              const stale = cur ? p.versions.filter((v) => v.status === "superseded" && v.elements > 0) : [];
              const staleCount = stale.reduce((n, v) => n + v.elements, 0);
              return (
                <tr key={p.id} className={`border-b border-hair hover:bg-sel/50 ${p.archived ? "text-muted" : ""}`}>
                  <td className="py-2.5 pr-3">
                    <Link to="/processi/$key" params={{ key: p.key }} className="font-medium text-fg no-underline hover:underline">
                      {p.name}
                    </Link>
                    <span className="ml-2 font-mono text-[11.5px] text-muted">{p.key}</span>
                    {p.archived && <span className="ml-2 text-[12px]">{t("configurazione.common.archiviato")}</span>}
                    {stale.length > 0 && (
                      <Link
                        to="/processi/$key"
                        params={{ key: p.key }}
                        hash="migrazione"
                        className="mt-0.5 block text-[12px] font-semibold text-flag no-underline hover:underline"
                      >
                        {t("configurazione.processes.staleOn", { count: staleCount, versions: stale.map((v) => `v${v.number}`).join(", ") })}
                        {cur ? ` ${t("configurazione.processes.currentParen", { version: cur.number })}` : ""} · {t("configurazione.processes.migrate")}
                      </Link>
                    )}
                  </td>
                  <td className="px-3 py-2.5">{p.type_name}</td>
                  <td className="px-3 py-2.5 text-right font-mono text-[12px]">{p.priority}</td>
                  <td className="px-3 py-2.5 text-[12.5px]" title={p.applies_when || undefined}><Frase p={p} /></td>
                  <td className="px-3 py-2.5 whitespace-nowrap">
                    {cur ? <span className="font-mono text-[12px]">v{cur.number}</span> : <span className="text-flag">{t("configurazione.processes.nonPubblicato")}</span>}
                    {draft && <span className="ml-2 border border-accent px-1 text-[11.5px] text-accent">{t("configurazione.processEditor.draftVersion", { version: draft.number })}</span>}
                  </td>
                  <td className="py-2.5 pl-3 text-right font-mono text-[12px]">{total}</td>
                </tr>
              );
            })}
            {list.data && list.data.items.length === 0 && (
              <tr>
                <td colSpan={6} className="py-8">
                  <EmptyState
                    title={t("configurazione.processes.nessunProcessoConfigurato")}
                    text={t("configurazione.processes.unProcessoDiceCome")}
                    action={
                      <button className="btn btn-primary" onClick={() => setCreating(true)}>
                        {t("configurazione.processes.creaIlPrimoProcesso")}
                      </button>
                    }
                  />
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </>
  );
}

function useNarrowList() {
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

/** Quando vale il processo, in una frase («Solo se Classificazione dei dati è personali»), non come codice. */
function Frase({ p }: { p: ProcessInfo }) {
  const t = useT();
  const { facts } = useTypeFacts(p.applies_when ? p.type : undefined);
  if (!p.applies_when.trim()) return <span className="text-muted">{t("configurazione.processes.sempre")}</span>;
  const names = Object.fromEntries(facts.map((f) => [f.key, f.name]));
  return <>{t("casa.cond.onlyIf", { condition: phrase(p.applies_when, names) })}</>;
}

/** Stato di migrazione: quanti elementi sono ancora su una versione precedente della corrente. */
function Migrazione({ p }: { p: ProcessInfo }) {
  const t = useT();
  const cur = p.versions.find((v) => v.status === "published");
  if (!cur) return <span className="text-flag">{t("configurazione.processes.nonPubblicato")}</span>;
  const stale = p.versions.filter((v) => v.status === "superseded" && v.elements > 0);
  const count = stale.reduce((n, v) => n + v.elements, 0);
  if (stale.length === 0) return <span className="text-muted">{t("configurazione.mappa.tuttiSullaCorrente", { version: cur.number })}</span>;
  return (
    <Link to="/processi/$key" params={{ key: p.key }} hash="migrazione" className="font-semibold text-flag no-underline hover:underline">
      {t("configurazione.processes.staleOn", { count, versions: stale.map((v) => `v${v.number}`).join(", ") })} {t("configurazione.processes.currentParen", { version: cur.number })} · {t("configurazione.processes.migrate")}
    </Link>
  );
}

/** Mappa dei processi: una scheda per processo con la miniatura del diagramma, versione e stato di migrazione. */
function ProcessMap({ items }: { items: ProcessInfo[] }) {
  const t = useT();
  if (items.length === 0) return null;
  return (
    <ul className="m-0 mb-8 grid list-none gap-4 px-4 p-0 sm:grid-cols-2 md:px-7 xl:grid-cols-3" aria-label={t("configurazione.mappa.titolo")} data-testid="mappa-processi">
      {items.map((p) => (
        <ProcessCard key={p.id} p={p} all={items} />
      ))}
    </ul>
  );
}

function ProcessCard({ p, all }: { p: ProcessInfo; all: ProcessInfo[] }) {
  const t = useT();
  const q = useQuery({ queryKey: ["process", p.key], queryFn: () => api.process(p.key), staleTime: 60_000 });
  const def = q.data?.current ?? q.data?.draft;
  const cur = p.versions.find((v) => v.status === "published");
  const draft = p.versions.find((v) => v.status === "draft");
  const ties = priorityTies(all, { key: p.key, type: p.type, priority: p.priority, when: p.applies_when });
  return (
    <li className={`flex min-w-0 flex-col gap-2 border border-hair p-3 ${p.archived ? "text-muted" : ""}`}>
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
        <Link to="/processi/$key" params={{ key: p.key }} className="inline-flex min-h-11 min-w-0 items-center font-semibold text-fg no-underline hover:underline md:min-h-0">
          {p.name}
        </Link>
        {p.archived && <span className="text-[12px]">{t("configurazione.common.archiviato")}</span>}
      </div>
      <p className="m-0 text-[12.5px] text-muted">
        {p.type_name} · {t("configurazione.processes.priorita")} <span className="font-mono">{p.priority}</span>
      </p>
      <div className="border border-hair bg-sel/30 p-2" style={{ minHeight: 72 }}>
        {def ? <Miniatura def={def} /> : <span className="text-[12px] text-muted">{q.error ? "" : t("configurazione.mappa.caricamento")}</span>}
      </div>
      <p className="m-0 text-[12.5px]">
        <Frase p={p} />
      </p>
      <p className="m-0 flex flex-wrap items-center gap-x-2 text-[12.5px]">
        {cur ? <span className="font-mono text-[12px]">v{cur.number}</span> : null}
        {draft && <span className="border border-accent px-1 text-[11.5px] text-accent">{t("configurazione.processEditor.draftVersion", { version: draft.number })}</span>}
        <Migrazione p={p} />
      </p>
      {ties.length > 0 && (
        <p className="m-0 text-[12px] font-semibold text-danger">{t("configurazione.mappa.parita", { names: ties.map((x) => t("configurazione.processEditor.quoted", { text: x.name })).join(", ") })}</p>
      )}
    </li>
  );
}

/** Che cos'è un processo, con l'esempio del ciclo di vita della domanda. */
function Intro() {
  const t = useT();
  const parts: { title: string; text: ReactNode }[] = [
    {
      title: t("configurazione.processes.fasi"),
      text: (
        <>
          <Trans
            k="configurazione.processes.introStages"
            slots={{
              flow: <i>{t("configurazione.processes.bozzaInviataTriageValutazione")}</i>,
              onHold: <i>{t("configurazione.processes.sospesa")}</i>,
              rejected: <i>{t("configurazione.processes.respinta")}</i>,
            }}
          />
        </>
      ),
    },
    {
      title: t("configurazione.common.passaggi"),
      text: (
        <>
          <Trans k="configurazione.processes.introTransitions" slots={{ example: <i>{t("configurazione.processes.invia")}</i> }} />
        </>
      ),
    },
    {
      title: t("configurazione.common.gate"),
      text: (
        <>
          <Trans k="configurazione.processes.introGates" slots={{ example: <code className="font-mono text-[12px]">{t("configurazione.processes.stimaMaxPresente")}</code> }} />
        </>
      ),
    },
    {
      title: t("configurazione.common.regole"),
      text: (
        <>
          {t("configurazione.processes.leAzioniAutomaticheEsempio")}
        </>
      ),
    },
  ];
  return (
    <section className="mx-4 mb-6 flex max-w-5xl flex-col gap-3 md:mx-7" aria-label={t("configurazione.processes.comeFunzionanoIProcessi")}>
      <p className="m-0 max-w-3xl">
        {t("configurazione.processes.unProcessoDescriveCome")}
      </p>
      <dl className="m-0 grid border-y border-hair md:grid-cols-4">
        {parts.map((p, i) => (
          <div key={p.title} className={`py-3 md:px-4 ${i ? "border-t border-hair md:border-t-0 md:border-l" : "md:pl-0"}`}>
            <dt className="label mb-1">{p.title}</dt>
            <dd className="m-0 text-[13px] text-muted">{p.text}</dd>
          </div>
        ))}
      </dl>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">
        {t("configurazione.processes.sePiuProcessiValgono")}
      </p>
    </section>
  );
}

type Origin = "blank" | "copy" | "starter";

/** Creazione di un processo: da zero, copiando un processo esistente o da un pacchetto di partenza. */
function NewProcess({ items, onClose }: { items: ProcessInfo[]; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const nav = useNavigate();
  const catalog = useQuery({ queryKey: ["processCatalog"], queryFn: processEditorApi.catalog });
  const [origin, setOrigin] = useState<Origin>("blank");
  const [source, setSource] = useState("");
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const [keyEdited, setKeyEdited] = useState(false);
  const [type, setType] = useState("");
  const [description, setDescription] = useState("");
  const [priority, setPriority] = useState("");
  // Priorità proposta: un processo nuovo non pareggia con quelli del tipo. Senza condizione vale per
  // tutto e va dopo gli altri (numero più alto); la condizione si imposta dopo e può farlo passare davanti.
  const sameType = items.filter((p) => p.type === type && !p.archived);
  const suggested = sameType.length ? Math.max(...sameType.map((p) => p.priority)) + 10 : 100;
  const effectivePriority = priority.trim() === "" ? suggested : Number(priority) || suggested;
  const clash = sameType.filter((p) => p.priority === effectivePriority);
  const copyable = items.filter((p) => p.versions.some((v) => v.status === "published"));
  const starters = catalog.data?.starters ?? [];
  const types = catalog.data?.types ?? [];
  const taken = items.map((p) => p.key);
  const effectiveKey = keyEdited ? key : name ? keyFrom(name, taken) : "";
  const chosenType = types.find((t) => t.key === type);

  const pickSource = (o: Origin, src: string) => {
    setSource(src);
    const t = o === "copy" ? items.find((p) => p.key === src)?.type : o === "starter" ? starters.find((s) => s.key === src)?.type : undefined;
    if (t) setType(t);
  };
  const create = useMutation({
    mutationFn: async () => {
      const base = { key: effectiveKey, name: name.trim(), type, description: description.trim(), priority: effectivePriority };
      if (origin === "starter") {
        const st = starters.find((s) => s.key === source);
        if (!st) throw new Error(t("configurazione.processes.choosePack"));
        const r = await api.importProcess({ ...st, ...base, notes: t("configurazione.processes.fromPack", { name: st.name }) }, false);
        return r.process;
      }
      return processEditorApi.create({ ...base, copy_from: origin === "copy" ? source : undefined });
    },
    onSuccess: (p) => {
      qc.invalidateQueries({ queryKey: ["processes"] });
      qc.invalidateQueries({ queryKey: ["processCatalog"] });
      onClose();
      nav({ to: "/processi/$key", params: { key: p.key } });
    },
  });
  const ready = name.trim() && effectiveKey && type && (origin === "blank" || source);
  return (
    <section className="mx-4 mb-6 flex max-w-3xl flex-col gap-4 border-y-2 border-rule py-4 md:mx-7" aria-label={t("configurazione.processes.nuovoProcesso")}>
      <h2 className="label m-0">{t("configurazione.processes.nuovoProcesso")}</h2>
      <ErrorNote error={catalog.error} />
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className="label mb-1.5">{t("configurazione.processes.siParte")}</legend>
        {(
          [
            ["blank", t("configurazione.common.daZero"), t("configurazione.processes.conLeFasiAttuali")],
            ["copy", t("configurazione.processes.copiandoUnProcessoEsistente"), t("configurazione.processes.laSuaVersioneCorrente")],
            ["starter", t("configurazione.processes.daUnPacchettoDi"), t("configurazione.processes.unProcessoProntoIncluso")],
          ] as const
        ).map(([k, label, hint]) => (
          <label key={k} className="flex items-start gap-2">
            <input
              type="radio"
              name="origin"
              className="mt-1"
              checked={origin === k}
              onChange={() => {
                setOrigin(k);
                setSource("");
              }}
            />
            <span>
              {label}
              <span className="block text-[12.5px] text-muted">{hint}</span>
            </span>
          </label>
        ))}
      </fieldset>
      {origin === "copy" && (
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.processes.processoDaCopiare")}</span>
          <Select className="field" value={source} onChange={(e) => pickSource("copy", e.target.value)}>
            <option value="">{t("configurazione.common.scegli")}</option>
            {copyable.map((p) => (
              <option key={p.key} value={p.key}>
                {p.name} · {p.type_name}
              </option>
            ))}
          </Select>
        </label>
      )}
      {origin === "starter" && (
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.processes.pacchetto")}</span>
          <Select className="field" value={source} onChange={(e) => pickSource("starter", e.target.value)}>
            <option value="">{t("configurazione.common.scegli")}</option>
            {starters.map((s) => (
              <option key={s.key} value={s.key}>
                {s.name} · {t("configurazione.processes.stageCount", { count: s.definition.phases.length })}
              </option>
            ))}
          </Select>
          {source && <span className="text-[12.5px] text-muted">{starters.find((s) => s.key === source)?.description}</span>}
        </label>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.common.nome")}</span>
          <input className="field" value={name} onChange={(e) => setName(e.target.value)} placeholder={t("configurazione.processes.esChangeRapido")} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.common.chiave")}</span>
          <input
            className="field font-mono text-[12.5px]"
            value={effectiveKey}
            onChange={(e) => {
              setKeyEdited(true);
              setKey(e.target.value.toLowerCase().replaceAll(/[^a-z0-9_]/g, "_"));
            }}
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.processes.tipoDiElemento")}</span>
          <Select className="field" value={type} onChange={(e) => setType(e.target.value)}>
            <option value="">{t("configurazione.common.scegli")}</option>
            {types.map((t) => (
              <option key={t.key} value={t.key}>
                {t.name}
                {t.processes ? ` · ${tr("configurazione.processes.processCount", { count: t.processes })}` : ""}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.processes.priorita")}</span>
          <input className="field font-mono" inputMode="numeric" placeholder={String(suggested)} value={priority} onChange={(e) => setPriority(e.target.value)} />
        </label>
      </div>
      {clash.length > 0 && (
        <p className="m-0 text-[12.5px] font-semibold text-danger" role="alert">
          {t("configurazione.processes.priorityClash", { priority: effectivePriority, names: clash.map((p) => t("configurazione.processEditor.quoted", { text: p.name })).join(", ") })}
        </p>
      )}
      {chosenType && chosenType.processes > 0 && (
        <p className="m-0 text-[12.5px] text-muted">
          {t("configurazione.processes.typeHasProcesses", { name: chosenType.name, count: chosenType.processes })}
        </p>
      )}
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.common.descrizione")}</span>
        <input className="field" value={description} onChange={(e) => setDescription(e.target.value)} placeholder={t("configurazione.processes.aCosaServe")} />
      </label>
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" disabled={!ready || create.isPending} onClick={() => create.mutate()}>
          {t("configurazione.processes.creaEApriL")}
        </button>
        <button className="btn" onClick={onClose}>
          {t("configurazione.common.annulla")}
        </button>
      </div>
      <ErrorNote error={create.error instanceof ApiError ? create.error : null} />
      {create.error && !(create.error instanceof ApiError) && <p className="m-0 text-danger">{(create.error as Error).message}</p>}
    </section>
  );
}

type View = "editor" | "read" | "json";
const VIEWS: { key: View; label: string }[] = [
  { key: "editor", get label() { return tr("configurazione.processes.view.editor"); } },
  { key: "read", get label() { return tr("configurazione.processes.view.read"); } },
  { key: "json", get label() { return tr("configurazione.processes.view.json"); } },
];

export function ProcessDetail({ processKey }: { processKey: string }) {
  const t = useT();
  const p = useQuery({ queryKey: ["process", processKey], queryFn: () => api.process(processKey) });
  const [shown, setShown] = useState<number | null>(null);
  const [view, setView] = useState<View>("editor");
  if (p.isPending) return <Loading className="p-8 text-muted" />;
  if (p.error) return <div className="p-8"><ErrorNote error={p.error} /></div>;
  const proc = p.data!;
  const cur = proc.versions.find((v) => v.status === "published");
  const draft = proc.versions.find((v) => v.status === "draft");
  const selected = shown ?? cur?.number ?? draft?.number ?? 0;
  return (
    <>
      <PageHeader
        path={t("configurazione.path.process", { key: proc.key })}
        title={proc.name}
        actions={
          <a className="btn no-underline" href={api.exportUrl(proc.key, selected || undefined)} download>
            {t("configurazione.processes.exportVersion", { version: selected })}
          </a>
        }
      />
      <div className="flex flex-col gap-8 px-4 md:px-7 pb-10">
        {proc.description && <p className="m-0 max-w-3xl text-muted">{proc.description}</p>}
        <Settings proc={proc} />
        <Versions proc={proc} selected={selected} onSelect={setShown} />
        <ProcessPublishFlow proc={proc} />
        <ProcessGuides proc={proc} />
        <section className="flex flex-col gap-5">
          <div role="tablist" aria-label={t("configurazione.processes.vistaDellaDefinizione")} className="flex gap-5 border-b border-hair">
            {VIEWS.map((v) => (
              <button
                key={v.key}
                role="tab"
                aria-selected={view === v.key}
                className={`-mb-px border-b-2 pb-2 font-semibold ${view === v.key ? "border-accent text-fg" : "border-transparent text-muted hover:text-fg"}`}
                onClick={() => setView(v.key)}
              >
                {v.label}
              </button>
            ))}
          </div>
          {view === "editor" && (
            <Suspense fallback={<p className="m-0 text-muted">{t("configurazione.processes.caricamentoDellEditor")}</p>}>
              <ProcessEditor key={proc.key} proc={proc} />
            </Suspense>
          )}
          {view === "read" && <VersionView proc={proc} number={selected} />}
          {view === "json" && (
            <>
              <DraftEditor proc={proc} />
              <ConditionTester />
            </>
          )}
        </section>
        {cur && <Migration proc={proc} />}
      </div>
    </>
  );
}

function Settings({ proc }: { proc: ProcessInfo }) {
  const t = useT();
  const qc = useQueryClient();
  // Priorità e applicabilità sono una bozza: si vede cosa cambia e si pubblica (o si scarta).
  const { draft, patch, discard, dirty } = useBozza({ priority: String(proc.priority), when: proc.applies_when }, `processo.${proc.key}.applicabilita`);
  const { priority, when } = draft;
  const setPriority = (v: string) => patch({ priority: v });
  const setWhen = (v: string) => patch({ when: v });
  const meta = useQuery({ queryKey: ["processMeta", proc.key], queryFn: () => processEditorApi.meta(proc.key), staleTime: 60_000 });
  const save = useMutation({
    mutationFn: (extra: { archived?: boolean }) => api.updateProcess(proc.key, { priority: Number(priority), applies_when: when, ...extra }),
    onSuccess: (p) => {
      qc.setQueryData(["process", proc.key], p);
      qc.invalidateQueries({ queryKey: ["processes"] });
    },
  });
  // La priorità decide chi vince tra processi dello stesso tipo: serve sempre, soprattutto con la condizione vuota.
  const priorityOk = /^\d+$/.test(priority.trim());
  const all = useQuery({ queryKey: ["processes"], queryFn: api.processes });
  const ties = priorityOk ? priorityTies(all.data?.items ?? [], { key: proc.key, type: proc.type, priority: Number(priority), when }) : [];
  const names = useMemo(() => Object.fromEntries((meta.data?.facts ?? []).map((f) => [f.key, f.name])), [meta.data]);
  const always = t("configurazione.processes.ilProcessoValePer");
  const changes = diffFields([
    { label: t("configurazione.processes.priorita"), before: String(proc.priority), after: priority },
    { label: t("configurazione.processes.siApplicaQuando"), before: proc.applies_when, after: when, format: (v) => (String(v ?? "").trim() ? describeCondition(String(v), names) : always) },
  ]);
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle aside={<span className="text-[12px] text-muted">{t("configurazione.processes.typeNamed", { name: proc.type_name })}</span>}>{t("configurazione.processes.applicabilita")}</SectionTitle>
      <form
        className="flex flex-wrap items-start gap-3"
        onSubmit={(e) => {
          e.preventDefault();
        }}
      >
        <label className="flex w-28 flex-col gap-1">
          <span className="label">{t("configurazione.processes.priorita")}</span>
          <input className="field font-mono" inputMode="numeric" value={priority} onChange={(e) => setPriority(e.target.value)} />
        </label>
        {ties.length > 0 && (
          <ul className="m-0 flex basis-full list-none flex-col gap-1 p-0 text-[12.5px] font-semibold text-danger" role="alert" data-testid="parita-priorita">
            {ties.map((x) => (
              <li key={x.name}>
                {t(x.kind === "all" ? "configurazione.mappa.pariTutti" : x.kind === "same" ? "configurazione.mappa.pariStessi" : "configurazione.mappa.pariForse", { name: x.name, type: proc.type_name, priority })}
              </li>
            ))}
          </ul>
        )}
        {ties.length === 0 && (proc.warnings?.length ?? 0) > 0 && (
          <ul className="m-0 flex basis-full list-none flex-col gap-1 p-0 text-[12.5px] font-semibold text-danger" role="alert">
            {proc.warnings!.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        )}
        <div className="min-w-0 flex-[1_1_360px]">
          {meta.data ? (
            <ConditionBuilder
              label={t("configurazione.processes.siApplicaQuando")}
              value={when}
              onChange={setWhen}
              facts={meta.data.facts}
              type={meta.data.type.key}
              sample={meta.data.sample}
              empty={always}
            />
          ) : (
            <label className="flex flex-col gap-1">
              <span className="label">{t("configurazione.processes.siApplicaQuando")}</span>
              <input
                className="field font-mono text-[12.5px]"
                spellCheck={false}
                placeholder={t("configurazione.processes.vuotoSempreEsStima")}
                value={when}
                onChange={(e) => setWhen(e.target.value)}
              />
            </label>
          )}
        </div>
        {!priorityOk && (
          <p className="m-0 basis-full text-[12.5px] font-semibold text-danger" role="alert">
            {when.trim() ? t("configurazione.processes.priorityRequired") : t("configurazione.processes.priorityRequiredNoCondition")}
          </p>
        )}
        <div className="basis-full">
          <BozzaBar
            changes={changes}
            blocked={!priorityOk}
            pending={save.isPending}
            error={save.error}
            published={save.isSuccess && !dirty}
            onPublish={() => save.mutate({})}
            onDiscard={() => {
              discard();
              save.reset();
            }}
            impact={t("casa.processes.impact", { type: proc.type_name })}
          />
        </div>
        <button type="button" className="btn" disabled={save.isPending} onClick={() => save.mutate({ archived: !proc.archived })}>
          {proc.archived ? t("configurazione.common.riattiva") : t("configurazione.common.archivia")}
        </button>
      </form>
    </section>
  );
}

function Versions({ proc, selected, onSelect }: { proc: ProcessInfo; selected: number; onSelect: (n: number) => void }) {
  const t = useT();
  const qc = useQueryClient();
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{t("configurazione.common.versioni")}</SectionTitle>
      <div className="overflow-x-auto">
        <table className="w-full border-collapse tabular">
          <tbody>
            {proc.versions.map((v) => (
              <tr key={v.id} className={`border-b border-hair ${v.number === selected ? "bg-sel" : ""}`}>
                <td className="w-16 py-2 pr-3">
                  <button className="font-mono text-[12.5px] font-semibold text-accent hover:underline" onClick={() => onSelect(v.number)}>
                    v{v.number}
                  </button>
                </td>
                <td className="w-44 px-3 py-2">
                  <VersionLifecycle lifecycle={v.lifecycle} from={v.effective_from} to={v.effective_to} deprecatedAt={v.deprecated_at} />
                </td>
                <td className="px-3 py-2">{v.notes || <span className="text-muted">—</span>}</td>
                <td className="px-3 py-2 text-[12px] whitespace-nowrap text-muted">
                  {v.published_at ? (v.published_by ? t("configurazione.processes.publishedOnBy", { date: date(v.published_at), name: v.published_by }) : t("configurazione.processes.publishedOn", { date: date(v.published_at) })) : t("configurazione.processes.savedAgo", { when: ago(v.created_at) })}
                </td>
                <td className="px-3 py-2">
                  <VigoreDates
                    key={`${v.id}-${v.effective_from}-${v.effective_to}`}
                    lifecycle={v.lifecycle}
                    from={v.effective_from}
                    to={v.effective_to}
                    onSave={async (b) => {
                      const p = await api.setVigore(proc.key, v.number, b);
                      qc.setQueryData(["process", proc.key], p);
                      qc.invalidateQueries({ queryKey: ["processes"] });
                    }}
                  />
                </td>
                <td className="py-2 pl-3 text-right font-mono text-[12px] whitespace-nowrap">{t("configurazione.admin.itemCount", { count: v.elements })}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function VersionView({ proc, number }: { proc: ProcessInfo; number: number }) {
  const t = useT();
  const v = proc.versions.find((x) => x.number === number);
  const inline = v?.status === "published" ? proc.current : v?.status === "draft" ? proc.draft : undefined;
  const q = useQuery({
    queryKey: ["processVersion", proc.key, number],
    queryFn: () => api.processVersion(proc.key, number),
    enabled: !!v && !inline,
    staleTime: Infinity,
  });
  const def = inline ?? q.data;
  if (!v) return null;
  if (!def) return q.error ? <ErrorNote error={q.error} /> : null;
  return <DefinitionView def={def} title={t("configurazione.processes.definitionTitle", { version: v.number, status: STATUS[v.status] })} />;
}

function Cell({ children, mono }: { children: ReactNode; mono?: boolean }) {
  return <td className={`px-3 py-2 align-top ${mono ? "font-mono text-[12px]" : ""}`}>{children}</td>;
}

function Head({ cols }: { cols: string[] }) {
  return (
    <thead>
      <tr className="label h-8 text-left">
        {cols.map((c) => (
          <th key={c} scope="col" className="border-b border-rule px-3 font-semibold">
            {c}
          </th>
        ))}
      </tr>
    </thead>
  );
}

function DefinitionView({ def, title }: { def: Definition; title: string }) {
  const t = useT();
  const states = def.phases.map((p) => ({ id: p.name, name: p.name, category: p.category, off_path: !!p.off_path }));
  return (
    <section className="flex flex-col gap-5">
      <SectionTitle>{title}</SectionTitle>
      <div className="flex flex-col gap-1.5">
        <StageGate states={states} current={def.initial} size="lg" />
        <p className="m-0 text-[12.5px] text-muted">
          {t("configurazione.processes.summary", { phases: def.phases.length, transitions: def.transitions.length, gates: def.gates?.length ?? 0, rules: def.rules?.length ?? 0 })}{" "}
          <span className="font-semibold text-fg">{def.initial}</span>
        </p>
      </div>
      <div className="overflow-x-auto">
        <h4 className="label m-0 mb-1">{t("configurazione.processes.fasi")}</h4>
        <table className="w-full min-w-[560px] border-collapse">
          <Head cols={["#", t("configurazione.processes.col.stage"), t("configurazione.processEditor.categoria"), t("configurazione.common.descrizione")]} />
          <tbody>
            {def.phases.map((p, i) => (
              <tr key={p.name} className="border-b border-hair">
                <Cell mono>{String(i + 1).padStart(2, "0")}</Cell>
                <Cell>
                  <span className="inline-flex items-center gap-2">
                    {p.color && <span className="size-2.5 shrink-0" style={{ background: p.color }} aria-hidden="true" />}
                    <span className="font-medium">{p.name}</span>
                    {p.off_path && <span className="text-[11.5px] text-muted">{t("configurazione.common.fuoriPercorso")}</span>}
                  </span>
                </Cell>
                <Cell>{CATEGORY[p.category] ?? p.category}</Cell>
                <Cell>{p.description ?? ""}</Cell>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="overflow-x-auto">
        <h4 className="label m-0 mb-1">{t("configurazione.common.passaggi")}</h4>
        <table className="w-full min-w-[720px] border-collapse">
          <Head cols={[t("configurazione.processEditor.from"), t("configurazione.processEditor.to"), t("configurazione.processes.col.button"), t("configurazione.processes.col.who"), t("configurazione.processes.col.gatesConditions")]} />
          <tbody>
            {def.transitions.map((t, i) => (
              <tr key={i} className="border-b border-hair">
                <Cell>{t.from.join(", ")}</Cell>
                <Cell>
                  <span className="font-medium">{t.to}</span>
                </Cell>
                <Cell>
                  {t.system ? <span className="text-muted">{tr("configurazione.processes.automaticoEsitoApprovazione")}</span> : t.label}
                  {t.comment_required && <span className="block text-[12px] text-muted">{tr("configurazione.processes.motivazioneObbligatoria")}</span>}
                </Cell>
                <Cell>{t.system ? tr("configurazione.processes.system") : t.roles?.length ? t.roles.map((r) => ROLE_NAMES[r] ?? r).join(", ") : tr("configurazione.processes.chiPuoModificare")}</Cell>
                <Cell>
                  <span className="flex flex-col gap-0.5">
                    {t.gates?.map((g) => (
                      <span key={g}>
                        <span className="label mr-1.5">{tr("configurazione.processes.gate")}</span>
                        {def.gates?.find((x) => x.key === g)?.name ?? g}
                      </span>
                    ))}
                    {t.when && <span className="text-[12px]" title={t.when}>{tr("configurazione.processEditor.onlyIf", { condition: describeCondition(t.when) })}</span>}
                    {t.approval && <span className="text-[12px] text-flag">{tr("configurazione.processes.opensApproval", { name: t.approval })}</span>}
                  </span>
                </Cell>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!!def.gates?.length && (
        <div className="overflow-x-auto">
          <h4 className="label m-0 mb-1">{t("configurazione.common.gate")}</h4>
          <table className="w-full min-w-[560px] border-collapse">
            <Head cols={[t("configurazione.common.chiave"), t("configurazione.common.nome"), t("configurazione.common.condizione"), t("configurazione.processes.col.message")]} />
            <tbody>
              {def.gates.map((g) => (
                <tr key={g.key} className="border-b border-hair">
                  <Cell mono>{g.key}</Cell>
                  <Cell>{g.name}</Cell>
                  <Cell>{g.condition ? describeCondition(g.condition) : t("configurazione.processEditor.checkNamed", { name: humanFact(g.check ?? "") })}</Cell>
                  <Cell>{g.message}</Cell>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {!!def.rules?.length && (
        <div className="overflow-x-auto">
          <h4 className="label m-0 mb-1">{t("configurazione.common.regole")}</h4>
          <table className="w-full min-w-[560px] border-collapse">
            <Head cols={[t("configurazione.processes.col.rule"), t("configurazione.vuln.quando"), t("configurazione.processes.col.if"), t("configurazione.processes.col.effect")]} />
            <tbody>
              {def.rules.map((r) => (
                <tr key={r.key} className="border-b border-hair">
                  <Cell>{r.name}</Cell>
                  <Cell mono>{r.on.replace(/^enter:/, t("configurazione.processes.entraIn")).replace(/^exit:/, t("configurazione.processes.esceDa")).replace(/^create$/, t("configurazione.common.allaCreazione"))}</Cell>
                  <Cell>{r.when ? describeCondition(r.when) : t("configurazione.processes.sempre")}</Cell>
                  <Cell>
                    {r.actions.map((a, i) => (
                      <span key={i} className="block">
                        {a.kind === "assign_role" ? (a.group ? t("configurazione.processes.assignRoleGroup", { role: ROLE_NAMES[a.role ?? ""] ?? a.role ?? "", group: a.group }) : t("configurazione.processes.assignRoleMember", { role: ROLE_NAMES[a.role ?? ""] ?? a.role ?? "" })) : a.kind}
                      </span>
                    ))}
                  </Cell>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function Problems({ problems }: { problems: Problem[] }) {
  const t = useT();
  if (problems.length === 0) return <p className="m-0 text-ok">{t("configurazione.common.nessunProblemaLaBozza")}</p>;
  return (
    <ul className="m-0 flex list-none flex-col gap-1 p-0">
      {problems.map((p, i) => (
        <li key={i} className="text-[12.5px]">
          <span className={`font-semibold ${p.level === "error" ? "text-danger" : "text-flag"}`}>{p.level === "error" ? t("configurazione.common.errore") : t("configurazione.common.avviso")}</span>{" "}
          <span className="font-mono text-[11.5px] text-muted">{p.path}</span> {p.message}
        </li>
      ))}
    </ul>
  );
}

function DraftEditor({ proc }: { proc: ProcessInfo }) {
  const t = useT();
  const qc = useQueryClient();
  const draftV = proc.versions.find((v) => v.status === "draft");
  const base = proc.draft ?? proc.current;
  const [text, setText] = useState(() => JSON.stringify(base, null, 2));
  const [notes, setNotes] = useState(draftV?.notes ?? "");
  const [parseError, setParseError] = useState<string | null>(null);
  useEffect(() => {
    setText(JSON.stringify(proc.draft ?? proc.current, null, 2));
    setNotes(proc.versions.find((v) => v.status === "draft")?.notes ?? "");
  }, [proc]);
  const done = (p: ProcessInfo) => {
    qc.setQueryData(["process", proc.key], p);
    qc.invalidateQueries({ queryKey: ["processes"] });
    qc.invalidateQueries({ queryKey: ["type"] });
  };
  const save = useMutation({
    mutationFn: () => {
      let def: unknown;
      try {
        def = JSON.parse(text);
      } catch (e) {
        setParseError((e as Error).message);
        throw e;
      }
      setParseError(null);
      return api.saveDraft(proc.key, def, notes);
    },
    onSuccess: done,
  });
  const publish = useMutation({ mutationFn: () => api.publish(proc.key, notes), onSuccess: (r) => done(r.process) });
  const discard = useMutation({
    mutationFn: () => api.discardDraft(proc.key),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["process", proc.key] }),
  });
  const problems = (publish.error instanceof ApiError && publish.error.problems) || proc.draft_problems || [];
  const nextNumber = draftV?.number ?? Math.max(0, ...proc.versions.map((v) => v.number)) + 1;
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle aside={<span className="text-[12px] text-muted">{draftV ? t("configurazione.processEditor.draftVersion", { version: draftV.number }) : t("configurazione.processes.newDraftFromCurrent", { version: nextNumber })}</span>}>
        {t("configurazione.common.modifica")}
      </SectionTitle>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">
        <Trans
          k="configurazione.processes.jsonHelp"
          slots={{
            // i18n-ok: sintassi del linguaggio delle condizioni, uguale in ogni lingua
            example1: <code className="font-mono">stima_max: &gt;50000 driver: normativo</code>,
            example2: <code className="font-mono">{t("configurazione.processes.ruoloSponsorPresente")}</code>,
          }}
        />
      </p>
      <textarea
        aria-label={t("configurazione.processes.definizioneDelProcessoIn")}
        className="field min-h-[420px] font-mono text-[12px] leading-[1.5]"
        spellCheck={false}
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
      {parseError && <p className="m-0 text-danger">{t("configurazione.processes.invalidJson", { error: parseError })}</p>}
      <label className="flex max-w-xl flex-col gap-1">
        <span className="label">{t("configurazione.common.noteDellaVersione")}</span>
        <input className="field" value={notes} onChange={(e) => setNotes(e.target.value)} placeholder={t("configurazione.common.cosaCambiaEPerche")} />
      </label>
      <div className="flex flex-wrap gap-2">
        <button className="btn" disabled={save.isPending} onClick={() => save.mutate()}>
          {t("configurazione.common.salvaBozza")}
        </button>
        {draftV && (
          <>
            <button className="btn btn-primary" disabled={publish.isPending || save.isPending} onClick={() => publish.mutate()}>
              {t("configurazione.processEditor.publishVersion", { version: draftV.number })}
            </button>
            <button className="btn btn-danger" disabled={discard.isPending} onClick={() => discard.mutate()}>
              {t("configurazione.common.scartaBozza")}
            </button>
          </>
        )}
      </div>
      {draftV && <Problems problems={problems} />}
      <ErrorNote error={save.error instanceof SyntaxError ? null : save.error} />
      <ErrorNote error={publish.error} />
      <ErrorNote error={discard.error} />
    </section>
  );
}

function Migration({ proc }: { proc: ProcessInfo }) {
  const t = useT();
  const qc = useQueryClient();
  const [map, setMap] = useState<Record<string, string>>({});
  const preview = useQuery({
    queryKey: ["migration", proc.key, proc.versions.find((v) => v.status === "published")?.id, map],
    queryFn: () => api.previewMigration(proc.key, map),
  });
  const run = useMutation({
    mutationFn: () => api.migrate(proc.key, map),
    onSuccess: () => {
      setMap({});
      qc.invalidateQueries({ queryKey: ["process", proc.key] });
      qc.invalidateQueries({ queryKey: ["migration", proc.key] });
      qc.invalidateQueries({ queryKey: ["element"] });
    },
  });
  const plan = preview.data;
  const phases = proc.current?.phases ?? [];
  return (
    <section id="migrazione" className="flex scroll-mt-4 flex-col gap-3">
      <SectionTitle aside={plan && <span className="text-[12px] text-muted">{t("configurazione.processes.toVersion", { version: plan.to_version })}</span>}>{t("configurazione.processes.migrazioneDelleRichiesteIn")}</SectionTitle>
      <ErrorNote error={preview.error} />
      {plan && plan.total === 0 && <p className="m-0 text-muted">{t("configurazione.processes.tutteLeRichiesteSono")}</p>}
      {plan && plan.total > 0 && (
        <>
          <table className="w-full border-collapse tabular">
            <Head cols={[t("configurazione.processes.col.fromVersion"), t("configurazione.processes.col.currentStage"), t("configurazione.processes.col.requests"), t("configurazione.processes.col.targetStage")]} />
            <tbody>
              {plan.groups.map((g) => (
                <tr key={`${g.from_version}-${g.phase}`} className="border-b border-hair">
                  <Cell mono>v{g.from_version}</Cell>
                  <Cell>{g.phase || "—"}</Cell>
                  <Cell mono>{g.count}</Cell>
                  <Cell>
                    <Select
                      aria-label={t("configurazione.processes.targetStageFor", { phase: g.phase })}
                      className={`field py-1 ${g.ok ? "" : "border-flag"}`}
                      value={map[g.phase] ?? (g.ok ? g.target_phase : "")}
                      onChange={(e) => setMap({ ...map, [g.phase]: e.target.value })}
                    >
                      {!g.ok && <option value="">{t("configurazione.processes.scegliUnaFase")}</option>}
                      {phases.map((p) => (
                        <option key={p.name} value={p.name}>
                          {p.name}
                        </option>
                      ))}
                    </Select>
                  </Cell>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="flex items-center gap-3">
            <button className="btn btn-primary" disabled={plan.blocked > 0 || run.isPending} onClick={() => run.mutate()}>
              {t("configurazione.processes.migrateCount", { count: plan.total, version: plan.to_version })}
            </button>
            {plan.blocked > 0 && <span className="text-[12.5px] text-flag">{t("configurazione.processes.blocked", { count: plan.blocked })}</span>}
          </div>
        </>
      )}
      <ErrorNote error={run.error} />
      {run.data && <p className="m-0 text-ok">{t("configurazione.processes.migrated", { count: run.data.total, version: run.data.to_version })}</p>}
    </section>
  );
}

function ConditionTester() {
  const t = useT();
  const [cond, setCond] = useState("");
  const [el, setEl] = useState("");
  const check = useMutation({ mutationFn: () => api.checkCondition(cond, el.trim() || undefined) });
  const r = check.data;
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle>{t("configurazione.processes.provaUnaCondizione")}</SectionTitle>
      <form
        className="flex flex-wrap items-end gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          check.mutate();
        }}
      >
        <label className="flex min-w-0 flex-[1_1_360px] flex-col gap-1">
          <span className="label">{t("configurazione.common.condizione")}</span>
          <input className="field font-mono text-[12.5px]" spellCheck={false} value={cond} onChange={(e) => setCond(e.target.value)} placeholder={t("configurazione.processes.driverNormativoOppureStima")} />
        </label>
        <label className="flex w-36 flex-col gap-1">
          <span className="label">{t("configurazione.processes.suElemento")}</span>
          <input className="field font-mono text-[12.5px]" value={el} onChange={(e) => setEl(e.target.value)} placeholder="DEM-12" />
        </label>
        <button className="btn btn-primary" disabled={check.isPending}>{t("configurazione.common.prova")}</button>
      </form>
      <ErrorNote error={check.error} />
      {r && !r.ok && <p className="m-0 text-danger">{t("configurazione.conditionBuilder.invalid", { error: r.error ?? "" })}</p>}
      {r && r.ok && r.result === undefined && <p className="m-0 text-ok">{t("configurazione.processes.validUses", { keys: r.keys.join(", ") || t("configurazione.processes.nessunCampo") })}</p>}
      {r && r.result !== undefined && (
        <p className="m-0">
          <Trans
            k="configurazione.processes.conditionResult"
            slots={{
              element: el.toUpperCase(),
              result: <span className={`font-semibold ${r.result ? "text-ok" : "text-flag"}`}>{r.result ? t("configurazione.processes.true") : t("configurazione.processes.false")}</span>,
            }}
          />
        </p>
      )}
      {r?.facts && (
        <table className="w-full max-w-2xl border-collapse">
          <Head cols={[t("configurazione.processes.col.fact"), t("configurazione.conditionBuilder.valore")]} />
          <tbody>
            {Object.entries(r.facts)
              .sort(([a], [b]) => a.localeCompare(b))
              .map(([k, v]) => (
                <tr key={k} className="border-b border-hair">
                  <Cell mono>{k}</Cell>
                  <Cell>{v.length ? v.map(String).join(", ") : <span className="text-muted">{t("configurazione.common.assente")}</span>}</Cell>
                </tr>
              ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

/** Pubblicazione con data di entrata in vigore e regola di transizione (componente generico in versioni.tsx). */
function ProcessPublishFlow({ proc }: { proc: ProcessInfo }) {
  const qc = useQueryClient();
  const draft = proc.versions.find((v) => v.status === "draft");
  const body = (o: FlowOptions) => ({
    effective_from: o.effectiveFrom || undefined,
    effective_to: o.effectiveTo || undefined,
    transition: o.rule,
  });
  const loadPreview = async (o: FlowOptions): Promise<FlowPreview> => {
    const r = await api.previewPublish(proc.key, body(o));
    return {
      immediate: r.immediate,
      effectiveFrom: r.effective_from,
      inForce: r.in_force ? `v${r.in_force}` : "",
      withdrawn: r.withdrawn ? `v${r.withdrawn}` : "",
      rule: r.plan.rule,
      groups: r.plan.groups.map((g) => ({ from: `v${g.from_version}`, phase: g.phase, count: g.count, mode: g.mode, missing: g.missing })),
      counts: { auto: r.plan.auto, stay: r.plan.stay, manual: r.plan.manual },
      errors: (r.problems ?? []).filter((p) => p.level === "error").length,
    };
  };
  return (
    <VersionFlow
      scope={["process", proc.key]}
      hasDraft={!!draft}
      versionLabel={`v${draft?.number ?? Math.max(0, ...proc.versions.map((v) => v.number)) + 1}`}
      loadPreview={loadPreview}
      publish={async (o) => {
        const r = await api.publishWith(proc.key, body(o));
        qc.setQueryData(["process", proc.key], r.process);
        return r;
      }}
      onPublished={() => {
        qc.invalidateQueries({ queryKey: ["processes"] });
        qc.invalidateQueries({ queryKey: ["type"] });
        qc.invalidateQueries({ queryKey: ["processMeta", proc.key] });
        qc.invalidateQueries({ queryKey: ["migration", proc.key] });
      }}
    />
  );
}
