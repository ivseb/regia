// Politica centrale della pipeline (DevSecOps, fase 3; replica/devsecops.md §3.3): quali controlli
// sono obbligatori, e in quale modalità, per ogni classe di applicazione. Si modifica con una
// matrice classi × controlli, con l'anteprima di quante applicazioni e servizi cambiano stato.
// - Matrice: l'editor; Versioni: lo storico con le differenze leggibili;
// - Adozione: i template che Regia propone ai repository (sezione «adoption» del profilo).
import { Select } from "../select";
import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";
import {
  MODES,
  MODE_NAME,
  STATUS_NAME,
  polApi,
  type AdoptionSpec,
  type Change,
  type Control,
  type Counts,
  type Impact,
  type Mode,
  type PolicyClass,
  type PolicyDef,
  type PolicyState,
  type ProfileItem,
  type Status,
} from "../policyApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, Loading } from "../ui";
import { useNarrow } from "./timeline";
import { getLang, tr } from "../i18n/core";
import { Trans } from "../i18n/Trans";
import { loc } from "../i18n/format";
import { crumbs } from "../crumbs";

export type PolicyTab = "matrice" | "versioni" | "adozione";
const TABS: { key: PolicyTab; label: string }[] = [
  { key: "matrice", get label() { return tr("governance.risks.matrice"); } },
  { key: "versioni", get label() { return tr("governance.compliance.versioni"); } },
  { key: "adozione", get label() { return tr("governance.pipelinepolicy.adozione"); } },
];
const crit = (): [string, string][] => [["low", tr("governance.pipelinepolicy.bassa")], ["medium", tr("governance.pipelinepolicy.media")], ["high", tr("governance.pipelinepolicy.alta")], ["critical", tr("governance.pipelinepolicy.critica")]];

function slug(s: string): string {
  return s.toLowerCase().normalize("NFD").replace(/[̀-ͯ]/g, "").replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "").slice(0, 30);
}

export function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v);
  useEffect(() => {
    const t = setTimeout(() => setD(v), ms);
    return () => clearTimeout(t);
  }, [v, ms]);
  return d;
}

function when(iso: string): string {
  return new Date(iso).toLocaleString(loc(), { day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" });
}

// ---------------------------------------------------------------------
// Pezzi comuni

export function StatusBadge({ status }: { status: Status | "" }) {
  if (!status) return null;
  const tone: Record<Status, string> = {
    aderente: "border-ok text-ok",
    parziale: "border-rule text-fg",
    fuori_politica: "border-danger text-danger",
    non_in_pipeline: "border-hair text-muted",
  };
  return <span className={`inline-block border px-1.5 text-[11.5px] font-semibold whitespace-nowrap ${tone[status]}`}>{STATUS_NAME[status]}</span>;
}

/** I quattro stati con i numeri: la distribuzione di applicazioni o servizi. */
export function StatusCounts({ c, label }: { c: Counts; label: string }) {
  const order: Status[] = ["aderente", "parziale", "fuori_politica", "non_in_pipeline"];
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <span className="label text-muted">{label}</span>
      <div className="flex flex-wrap gap-x-5 gap-y-1">
        {order.map((s) => (
          <span key={s} className="flex items-baseline gap-1.5">
            <span className={`font-mono text-[22px] leading-none font-semibold tabular ${c[s] ? "" : "text-muted"}`}>{c[s]}</span>
            <span className="text-[12px] text-muted">{STATUS_NAME[s].toLowerCase()}</span>
          </span>
        ))}
      </div>
    </div>
  );
}

function modeClass(m: Mode): string {
  return m === "block" ? "border-fg bg-fg text-bg" : m === "warn" ? "border-rule text-fg" : "border-hair text-muted";
}

/** Un controllo nella matrice: un tocco passa a spento → avviso → blocca. */
function ModeCell({ value, onChange, label }: { value: Mode; onChange: (m: Mode) => void; label: string }) {
  const next = MODES[(MODES.indexOf(value) + 1) % MODES.length] as Mode;
  return (
    <button
      type="button"
      className={`min-w-[78px] border px-2 py-1 text-[12.5px] font-semibold ${modeClass(value)}`}
      onClick={() => onChange(next)}
      aria-label={tr("governance.pipelinepolicy.cambia_in", { label, p: MODE_NAME[value], p2: MODE_NAME[next] })}
      title={tr("governance.pipelinepolicy.passa_a", { p: MODE_NAME[next].toLowerCase() })}
    >
      {MODE_NAME[value]}
    </button>
  );
}

function ModeSegment({ value, onChange, label }: { value: Mode; onChange: (m: Mode) => void; label: string }) {
  return (
    <div role="radiogroup" aria-label={label} className="flex">
      {MODES.map((m) => (
        <button
          key={m}
          type="button"
          role="radio"
          aria-checked={value === m}
          onClick={() => onChange(m)}
          className={`-ml-px flex-1 border px-2 py-1.5 text-[12.5px] font-semibold first:ml-0 ${value === m ? modeClass(m) + " relative z-10" : "border-hair text-muted"}`}
        >
          {MODE_NAME[m]}
        </button>
      ))}
    </div>
  );
}

function Chips({ options, value, onChange, label, addable }: { options: [string, string][]; value: string[]; onChange: (v: string[]) => void; label: string; addable?: boolean }) {
  const [text, setText] = useState("");
  const all: [string, string][] = [...options, ...value.filter((v) => !options.some(([k]) => k === v)).map((v): [string, string] => [v, v])];
  const toggle = (k: string) => onChange(value.includes(k) ? value.filter((x) => x !== k) : [...value, k]);
  return (
    <div className="flex flex-col gap-1.5">
      <span className="label">{label}</span>
      <div className="flex flex-wrap gap-1.5">
        {all.map(([k, n]) => (
          <button
            key={k}
            type="button"
            aria-pressed={value.includes(k)}
            onClick={() => toggle(k)}
            className={`border px-2 py-1 text-[12.5px] ${value.includes(k) ? "border-accent bg-sel font-semibold text-accent" : "border-hair text-muted hover:text-fg"}`}
          >
            {n}
          </button>
        ))}
        {all.length === 0 && !addable && <span className="text-[12.5px] text-muted">{tr("governance.pipelinepolicy.nessun_valore")}</span>}
      </div>
      {addable && (
        <input
          className="field max-w-[260px] py-1.5 text-[12.5px]"
          placeholder={tr("governance.pipelinepolicy.altro_valore_poi_invio")}
          value={text}
          aria-label={tr("governance.pipelinepolicy.aggiungi_un_valore_a", { label })}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key !== "Enter") return;
            e.preventDefault();
            const v = text.trim().toLowerCase();
            if (v && !value.includes(v)) onChange([...value, v]);
            setText("");
          }}
        />
      )}
    </div>
  );
}

function condSummary(c: PolicyClass, exposureOn: boolean): string {
  const p: string[] = [];
  if (c.criticality?.length) p.push(tr("governance.pipelinepolicy.criticita", { values: c.criticality.map((k) => crit().find(([x]) => x === k)?.[1] ?? k).join(tr("governance.pipelinepolicy.o")) }));
  if (c.critical_function === "yes") p.push(tr("governance.pipelinepolicy.funzione_critica_dora"));
  if (c.critical_function === "no") p.push(tr("governance.pipelinepolicy.senza_funzione_critica"));
  if (c.data?.length) p.push(tr("governance.pipelinepolicy.dati", { values: c.data.join(tr("governance.pipelinepolicy.o")) }));
  if (exposureOn && c.exposure?.length) p.push(tr("governance.pipelinepolicy.esposizione_2", { values: c.exposure.join(tr("governance.pipelinepolicy.o")) }));
  return p.length ? p.join(", ") : tr("governance.pipelinepolicy.tutte_le_altre");
}

// ---------------------------------------------------------------------
// Pagina

export function PipelinePolicyPage({ tab, from }: { tab: PolicyTab; from?: number }) {
  const st = useQuery({ queryKey: ["pol-policy"], queryFn: polApi.policy });
  return (
    <>
      <PageHeader
        path={crumbs("configuration", "devsecops", "policy")}
        title={tr("governance.pipelinepolicy.politica_della_pipeline")}
        actions={<Link to="/devsecops/aderenza" className="btn no-underline">{tr("governance.pipelinepolicy.vedi_l_aderenza")}</Link>}
      />
      <div role="tablist" aria-label={tr("governance.compliance.sezioni")} className="flex gap-6 overflow-x-auto border-b border-hair px-4 md:px-7">
        {TABS.map((t) => (
          <Link
            key={t.key}
            to="/configurazione/devsecops/politica"
            search={{ tab: t.key }}
            role="tab"
            aria-selected={tab === t.key}
            className={`shrink-0 pb-2.5 no-underline ${tab === t.key ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
          >
            {t.label}
          </Link>
        ))}
      </div>
      <div className="flex max-w-6xl flex-col gap-7 px-4 py-6 pb-10 md:px-7">
        <ErrorNote error={st.error} />
        {st.isPending && <Loading className="m-0 text-muted" />}
        {st.data && tab === "matrice" && <MatrixTab state={st.data} fromVersion={from} />}
        {st.data && tab === "versioni" && <VersionsTab state={st.data} />}
        {st.data && tab === "adozione" && <AdoptionTab state={st.data} />}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------
// Matrice

function MatrixTab({ state, fromVersion }: { state: PolicyState; fromVersion?: number }) {
  const old = useQuery({ queryKey: ["pol-version", fromVersion], queryFn: () => polApi.version(fromVersion!), enabled: !!fromVersion });
  if (fromVersion && old.isPending) return <p className="m-0 text-muted">{tr("governance.pipelinepolicy.caricamento_della_versione", { fromVersion })}</p>;
  const initial = (fromVersion && old.data?.definition) || state.definition;
  return <MatrixEditor key={`${state.version}-${fromVersion ?? ""}`} state={state} initial={initial} restored={fromVersion && old.data ? fromVersion : undefined} />;
}

function MatrixEditor({ state, initial, restored }: { state: PolicyState; initial: PolicyDef; restored?: number }) {
  const qc = useQueryClient();
  const narrow = useNarrow();
  const [draft, setDraft] = useState<PolicyDef>(() => structuredClone(initial));
  const [sel, setSel] = useState(0); // classe aperta nelle condizioni; classes.length = quella di ripiego
  const [note, setNote] = useState(restored ? tr("governance.pipelinepolicy.ripristina_la_versione", { restored }) : "");
  const edit = (fn: (d: PolicyDef) => void) =>
    setDraft((d) => {
      const n = structuredClone(d);
      fn(n);
      return n;
    });
  const dirty = JSON.stringify(draft) !== JSON.stringify(state.definition) || !state.configured;
  const delayed = useDebounced(draft, 500);
  const impact = useQuery({
    queryKey: ["pol-impact", JSON.stringify(delayed)],
    queryFn: () => polApi.impact(delayed),
    enabled: dirty,
    placeholderData: keepPreviousData,
    staleTime: 15_000,
  });
  const save = useMutation({
    mutationFn: () => polApi.save(draft, note),
    onSuccess: (s) => {
      qc.setQueryData(["pol-policy"], s);
      for (const k of ["pol-adherence", "dso-panel", "pol-pipeline"]) qc.invalidateQueries({ queryKey: [k] });
    },
  });
  const ro = !state.can_edit;
  const exposureOn = !!draft.exposure_field;
  const classes = [...draft.classes, draft.fallback];
  const classAt = (i: number): PolicyClass => classes[i] ?? draft.fallback;
  const setMode = (ci: number, key: string, m: Mode) =>
    edit((d) => {
      (ci < d.classes.length ? d.classes[ci]! : d.fallback).modes[key] = m;
    });
  const patchClass = (ci: number, fn: (c: PolicyClass) => void) => edit((d) => fn(ci < d.classes.length ? d.classes[ci]! : d.fallback));
  const addClass = () => {
    edit((d) => {
      let n = d.classes.length + 1;
      while (d.classes.some((c) => c.key === `classe_${n}`) || d.fallback.key === `classe_${n}`) n++;
      d.classes.push({ key: `classe_${n}`, name: tr("governance.pipelinepolicy.nuova_classe", { n }), modes: { ...d.fallback.modes } });
    });
    setSel(draft.classes.length);
  };
  const removeClass = (ci: number) => {
    edit((d) => void d.classes.splice(ci, 1));
    setSel(0);
  };
  const moveClass = (ci: number, by: number) =>
    edit((d) => {
      const j = ci + by;
      if (j < 0 || j >= d.classes.length) return;
      const a = d.classes[ci]!;
      d.classes[ci] = d.classes[j]!;
      d.classes[j] = a;
    });
  const selClass = classAt(sel);
  const isFallback = sel >= draft.classes.length;

  return (
    <div className="grid grid-cols-1 gap-8 lg:grid-cols-[minmax(0,1fr)_340px]">
      <div className="flex min-w-0 flex-col gap-7">
        <p className="m-0 max-w-2xl text-muted">
          <Trans
            k="governance.pipelinepolicy.per_ogni_classe_di_applicazione_scegli"
            slots={{
              block: <strong className="text-fg">{tr("governance.pipelinepolicy.blocca")}</strong>,
              warn: <strong className="text-fg">{tr("governance.pipelinepolicy.avviso")}</strong>,
              off: <strong className="text-fg">{tr("governance.pipelinepolicy.spento")}</strong>,
            }}
          />
          {!state.configured && tr("governance.pipelinepolicy.questa_e_una_proposta_di")}
        </p>
        {restored && <p className="m-0 border-l-2 border-accent pl-3">{tr("governance.pipelinepolicy.stai_modificando_una_copia_della", { restored })}</p>}

        <section className="flex flex-col gap-3" aria-label={tr("governance.pipelinepolicy.matrice_dei_controlli")}>
          <SectionTitle>{tr("governance.pipelinepolicy.classi_e_controlli")}</SectionTitle>
          {narrow ? (
            <div className="flex flex-col gap-3">
              <label className="flex flex-col gap-1">
                <span className="label">{tr("governance.pipelinepolicy.classe")}</span>
                <Select className="field" value={sel} onChange={(e) => setSel(Number(e.target.value))}>
                  {classes.map((c, i) => (
                    <option key={c.key} value={i}>{c.name}</option>
                  ))}
                </Select>
              </label>
              <p className="m-0 text-[12.5px] text-muted">{condSummary(selClass, exposureOn)}</p>
              <ul className="m-0 flex list-none flex-col p-0">
                {draft.controls.map((c) => (
                  <li key={c.key} className="flex flex-col gap-1.5 border-b border-hair py-2.5">
                    <span className="font-medium">{c.name}</span>
                    <ModeSegment value={selClass.modes[c.key] ?? "off"} label={tr("governance.pipelinepolicy.per", { name: c.name, name2: selClass.name })} onChange={(m) => !ro && setMode(sel, c.key, m)} />
                  </li>
                ))}
              </ul>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left">
                <thead>
                  <tr>
                    <th scope="col" className="label border-b-2 border-rule py-2 pr-3 text-muted">{tr("governance.compliance.controllo")}</th>
                    {classes.map((c, i) => (
                      <th key={c.key} scope="col" className={`border-b-2 px-2 py-2 align-bottom ${sel === i ? "border-accent" : "border-rule"}`}>
                        <button
                          type="button"
                          className={`flex w-full flex-col items-start gap-0.5 bg-transparent p-0 text-left ${sel === i ? "text-accent" : "text-fg"}`}
                          onClick={() => setSel(i)}
                          aria-pressed={sel === i}
                          title={tr("governance.pipelinepolicy.modifica_le_condizioni_di_questa")}
                        >
                          <span className="text-[13px] font-semibold">{c.name}</span>
                          <span className="max-w-[170px] text-[11.5px] font-normal text-muted">{condSummary(c, exposureOn)}</span>
                        </button>
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {draft.controls.map((c) => (
                    <tr key={c.key} className="border-b border-hair">
                      <th scope="row" className="py-2 pr-3 font-medium">{c.name}</th>
                      {classes.map((k, i) => (
                        <td key={k.key} className="px-2 py-2">
                          <ModeCell value={k.modes[c.key] ?? "off"} label={tr("governance.pipelinepolicy.per", { name: c.name, name2: k.name })} onChange={(m) => !ro && setMode(i, c.key, m)} />
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {!ro && (
            <div>
              <button type="button" className="btn" onClick={addClass} disabled={draft.classes.length >= 12}>{tr("governance.pipelinepolicy.aggiungi_una_classe")}</button>
            </div>
          )}
        </section>

        <section className="flex flex-col gap-3" aria-label={tr("governance.pipelinepolicy.classe_selezionata")}>
          <SectionTitle>{tr("governance.pipelinepolicy.classe_2", { name: selClass.name })}</SectionTitle>
          <label className="flex max-w-sm flex-col gap-1">
            <span className="label">{tr("governance.compliance.nome")}</span>
            <input className="field" disabled={ro} value={selClass.name} onChange={(e) => patchClass(sel, (c) => void (c.name = e.target.value))} />
          </label>
          {isFallback ? (
            <p className="m-0 max-w-xl text-muted">{tr("governance.pipelinepolicy.vale_per_le_applicazioni_che")}</p>
          ) : (
            <div className="flex flex-col gap-4">
              <Chips label={tr("governance.riskdetail.criticita")} options={crit()} value={selClass.criticality ?? []} onChange={(v) => patchClass(sel, (c) => void (c.criticality = v))} />
              <div className="flex flex-col gap-1.5">
                <span className="label">{tr("governance.pipelinepolicy.funzione_critica_dora_2")}</span>
                <div role="radiogroup" aria-label={tr("governance.pipelinepolicy.funzione_critica_dora_2")} className="flex max-w-sm">
                  {([["", tr("governance.pipelinepolicy.qualsiasi")], ["yes", "Sì"], ["no", "No"]] as const).map(([k, n]) => (
                    <button
                      key={k}
                      type="button"
                      role="radio"
                      aria-checked={(selClass.critical_function ?? "") === k}
                      onClick={() => patchClass(sel, (c) => void (c.critical_function = k))}
                      className={`-ml-px flex-1 border px-2 py-1.5 text-[12.5px] first:ml-0 ${(selClass.critical_function ?? "") === k ? "relative z-10 border-accent bg-sel font-semibold text-accent" : "border-hair text-muted"}`}
                    >
                      {n}
                    </button>
                  ))}
                </div>
              </div>
              <Chips
                label={tr("governance.pipelinepolicy.classificazione_dei_dati")}
                addable
                options={state.observed.data.map((v): [string, string] => [v, v])}
                value={selClass.data ?? []}
                onChange={(v) => patchClass(sel, (c) => void (c.data = v))}
              />
              {exposureOn && (
                <Chips
                  label={tr("governance.pipelinepolicy.esposizione")}
                  addable
                  options={state.observed.exposure.map((v): [string, string] => [v, v])}
                  value={selClass.exposure ?? []}
                  onChange={(v) => patchClass(sel, (c) => void (c.exposure = v))}
                />
              )}
              {!ro && (
                <div className="flex flex-wrap gap-2">
                  <button type="button" className="btn" disabled={sel === 0} onClick={() => { moveClass(sel, -1); setSel(sel - 1); }}>{tr("governance.pipelinepolicy.sposta_prima")}</button>
                  <button type="button" className="btn" disabled={sel >= draft.classes.length - 1} onClick={() => { moveClass(sel, 1); setSel(sel + 1); }}>{tr("governance.pipelinepolicy.sposta_dopo")}</button>
                  <button type="button" className="btn btn-danger" onClick={() => removeClass(sel)}>{tr("governance.pipelinepolicy.elimina_la_classe")}</button>
                </div>
              )}
            </div>
          )}
        </section>

        <ControlsEditor draft={draft} edit={edit} state={state} ro={ro} />

        {!ro && (
          <form
            className="flex flex-col gap-3 border-t-2 border-rule pt-4"
            onSubmit={(e) => {
              e.preventDefault();
              save.mutate();
            }}
          >
            <label className="flex max-w-xl flex-col gap-1">
              <span className="label">{tr("governance.pipelinepolicy.nota_della_versione_facoltativa")}</span>
              <input className="field" value={note} maxLength={500} onChange={(e) => setNote(e.target.value)} placeholder={tr("governance.pipelinepolicy.per_esempio_il_sast_diventa")} />
            </label>
            <PolicyError error={save.error} />
            <div className="flex flex-wrap items-center gap-3">
              <button className="btn btn-primary" disabled={!dirty || save.isPending}>
                {save.isPending ? tr("governance.pipelinepolicy.salvo") : state.configured ? tr("governance.pipelinepolicy.salva_come_versione", { p: state.version + 1 }) : tr("governance.pipelinepolicy.salva_la_prima_versione")}
              </button>
              {dirty && state.configured && (
                <button type="button" className="btn" onClick={() => { setDraft(structuredClone(state.definition)); setSel(0); }}>{tr("governance.pipelinepolicy.annulla_le_modifiche")}</button>
              )}
              {!dirty && state.configured && <span className="text-muted">{tr("governance.pipelinepolicy.nessuna_modifica_rispetto_alla_versione", { version: state.version })}</span>}
            </div>
          </form>
        )}
        {ro && <p className="m-0 text-muted">{tr("governance.pipelinepolicy.serve_il_permesso_di_gestire")}</p>}
      </div>

      <aside className="flex min-w-0 flex-col gap-4 lg:sticky lg:top-4 lg:self-start" aria-label={tr("governance.pipelinepolicy.cosa_cambia")}>
        <ImpactPanel impact={impact.data} loading={impact.isFetching} dirty={dirty} configured={state.configured} error={impact.error} />
      </aside>
    </div>
  );
}

function PolicyError({ error }: { error: unknown }) {
  if (!error) return null;
  const probs = error instanceof ApiError ? error.problems : undefined;
  return (
    <div className="flex flex-col gap-1">
      <ErrorNote error={error} />
      {probs && probs.length > 0 && (
        <ul className="m-0 pl-5 text-[12.5px] text-danger">
          {probs.slice(0, 8).map((p, i) => (
            <li key={i}><span className="font-mono">{p.path}</span>: {p.message}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

function ControlsEditor({ draft, edit, state, ro }: { draft: PolicyDef; edit: (fn: (d: PolicyDef) => void) => void; state: PolicyState; ro: boolean }) {
  const [name, setName] = useState("");
  const known = new Set(draft.controls.flatMap((c) => [c.key, ...(c.aliases ?? [])]));
  const unknown = state.observed.checks.filter((n) => !known.has(n.toLowerCase()));
  const add = () => {
    const n = name.trim();
    if (!n) return;
    let key = slug(n) || "controllo";
    if (!/^[a-z]/.test(key)) key = "c_" + key;
    while (draft.controls.some((c) => c.key === key)) key += "_2";
    edit((d) => {
      d.controls.push({ key, name: n, aliases: [] });
      for (const c of [...d.classes, d.fallback]) c.modes[key] = "off";
    });
    setName("");
  };
  return (
    <section className="flex flex-col gap-3" aria-label={tr("governance.compliance.controlli")}>
      <SectionTitle>{tr("governance.compliance.controlli")}</SectionTitle>
      <p className="m-0 max-w-2xl text-muted">{tr("governance.pipelinepolicy.le_pipeline_riportano_i_controlli")}</p>
      <ul className="m-0 flex list-none flex-col p-0">
        {draft.controls.map((c: Control, i) => (
          <li key={c.key} className="grid grid-cols-1 gap-2 border-b border-hair py-2.5 md:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)_auto] md:items-end">
            <label className="flex flex-col gap-1">
              <span className="label">{tr("governance.compliance.controllo")}{" "}<span className="font-mono normal-case text-muted">{c.key}</span></span>
              <input className="field" disabled={ro} value={c.name} onChange={(e) => edit((d) => void (d.controls[i]!.name = e.target.value))} />
            </label>
            <label className="flex flex-col gap-1">
              <span className="label">{tr("governance.pipelinepolicy.nomi_alternativi")}</span>
              <input
                className="field font-mono"
                disabled={ro}
                defaultValue={(c.aliases ?? []).join(", ")}
                onBlur={(e) => edit((d) => void (d.controls[i]!.aliases = e.target.value.split(",").map((x) => x.trim().toLowerCase()).filter(Boolean)))}
              />
            </label>
            {!ro && (
              <button
                type="button"
                className="btn btn-danger"
                disabled={draft.controls.length <= 1}
                onClick={() => edit((d) => {
                  d.controls.splice(i, 1);
                  for (const k of [...d.classes, d.fallback]) delete k.modes[c.key];
                })}
              >{tr("governance.pipelinepolicy.togli")}</button>
            )}
          </li>
        ))}
      </ul>
      {unknown.length > 0 && (
        <p className="m-0 text-[12.5px] text-muted">{tr("governance.pipelinepolicy.controlli_che_le_pipeline_riportano")}{" "}<span className="font-mono">{unknown.slice(0, 12).join(", ")}</span>
        </p>
      )}
      {!ro && (
        <form className="flex max-w-xl gap-2" onSubmit={(e) => { e.preventDefault(); add(); }}>
          <input className="field" placeholder={tr("governance.pipelinepolicy.nuovo_controllo_per_esempio_licenze")} value={name} onChange={(e) => setName(e.target.value)} aria-label={tr("governance.pipelinepolicy.nome_del_nuovo_controllo")} />
          <button className="btn" disabled={!name.trim() || draft.controls.length >= 30}>{tr("governance.compliance.aggiungi")}</button>
        </form>
      )}
      <label className="flex max-w-sm flex-col gap-1">
        <span className="label">{tr("governance.pipelinepolicy.campo_dell_esposizione")}</span>
        <Select className="field" disabled={ro} value={draft.exposure_field ?? ""} onChange={(e) => edit((d) => void (d.exposure_field = e.target.value || undefined))}>
          <option value="">{tr("governance.pipelinepolicy.non_usare_l_esposizione")}</option>
          {state.fields.map((f) => (
            <option key={f.key} value={f.key}>{f.name}</option>
          ))}
        </Select>
        <span className="text-[12px] text-muted">{tr("governance.pipelinepolicy.un_campo_personalizzato_dell_applicazione")}</span>
      </label>
    </section>
  );
}

function ImpactPanel({ impact, loading, dirty, configured, error }: { impact?: Impact; loading: boolean; dirty: boolean; configured: boolean; error: unknown }) {
  return (
    <section className="flex flex-col gap-3 border-t-2 border-rule pt-3" aria-label={tr("governance.pipelinepolicy.anteprima_dell_impatto")} aria-busy={loading}>
      <h3 className="label m-0">{tr("governance.pipelinepolicy.cosa_cambia")}</h3>
      <ErrorNote error={error} />
      {!dirty && <p className="m-0 text-muted">{tr("governance.pipelinepolicy.modifica_la_matrice_qui_vedi")}</p>}
      {dirty && !impact && <p className="m-0 text-muted">{tr("governance.compliance.calcolo")}</p>}
      {dirty && impact && (
        <div className={`flex flex-col gap-4 ${loading ? "opacity-60" : ""}`}>
          {impact.problems.length > 0 ? (
            <ul className="m-0 pl-5 text-danger">
              {impact.problems.slice(0, 6).map((p, i) => <li key={i}><span className="font-mono">{p.path}</span>: {p.message}</li>)}
            </ul>
          ) : (
            <>
              {impact.before_configured ? (
                <p className="m-0 text-[15px] font-semibold">
                  {impact.changed_apps + impact.changed_services === 0
                    ? tr("governance.pipelinepolicy.nessuna_applicazione_o_servizio_cambia")
                    : tr("governance.pipelinepolicy.cambiano_stato", { apps: tr("governance.pipelinepolicy.n_applicazioni", { count: impact.changed_apps }), services: tr("governance.pipelinepolicy.n_servizi_cambiano", { count: impact.changed_services }) })}
                </p>
              ) : (
                <p className="m-0 text-[15px] font-semibold">{configured ? "" : tr("governance.pipelinepolicy.prima_versione_ecco_come_si")}</p>
              )}
              <StatusCounts c={impact.after.apps} label={tr("governance.pipelinepolicy.applicazioni_dopo")} />
              <StatusCounts c={impact.after.services} label={tr("governance.pipelinepolicy.servizi_dopo")} />
              {impact.transitions.length > 0 && (
                <ul className="m-0 flex list-none flex-col gap-1 p-0">
                  {impact.transitions.map((t) => (
                    <li key={t.from + t.to} className="text-[12.5px]">
                      {STATUS_NAME[t.from as Status] ?? t.from} → <strong>{STATUS_NAME[t.to as Status] ?? t.to}</strong>:{" "}
                      <Trans k="governance.pipelinepolicy.appl_serv" slots={{ apps: <span className="tabular">{t.apps}</span>, services: <span className="tabular">{t.services}</span> }} />
                    </li>
                  ))}
                </ul>
              )}
              {impact.items.length > 0 && (
                <details>
                  <summary className="cursor-pointer text-[12.5px] text-muted">{tr("governance.pipelinepolicy.elenco", { length: impact.items.length })}{impact.items.length >= 80 ? "+" : ""})</summary>
                  <ul className="m-0 mt-1 flex list-none flex-col p-0">
                    {impact.items.map((i) => (
                      <li key={i.kind + i.id} className="border-b border-hair py-1 text-[12.5px]">
                        <span className="font-medium">{i.name}</span> <span className="text-muted">{i.kind === "service" ? tr("governance.pipelinepolicy.servizio_di", { of: i.of ?? "" }) : i.key}</span>
                        <span className="block text-muted">{STATUS_NAME[i.from as Status] ?? i.from} → {STATUS_NAME[i.to as Status] ?? i.to}</span>
                      </li>
                    ))}
                  </ul>
                </details>
              )}
              <ChangeList changes={impact.changes} title={tr("governance.pipelinepolicy.modifiche_alla_politica")} />
            </>
          )}
        </div>
      )}
    </section>
  );
}

export function ChangeList({ changes, title }: { changes: Change[]; title: string }) {
  if (changes.length === 0) return null;
  return (
    <div className="flex flex-col gap-1.5">
      <span className="label text-muted">{title}</span>
      <ul className="m-0 flex list-none flex-col p-0">
        {changes.map((c, i) => (
          <li key={i} className="border-b border-hair py-1.5 text-[12.5px]">
            {c.text}
            {c.stricter !== undefined && <span className={`ml-1.5 text-[11.5px] font-semibold ${c.stricter ? "text-fg" : "text-muted"}`}>{c.stricter ? tr("governance.pipelinepolicy.piu_severa") : tr("governance.pipelinepolicy.piu_morbida")}</span>}
          </li>
        ))}
      </ul>
    </div>
  );
}

// ---------------------------------------------------------------------
// Versioni

function VersionsTab({ state }: { state: PolicyState }) {
  const vs = state.versions;
  const [to, setTo] = useState(vs[0]?.version ?? 0);
  const prev = vs.find((v) => v.version < to)?.version ?? 0;
  const [from, setFrom] = useState<number | null>(null);
  const f = from ?? prev;
  const diff = useQuery({ queryKey: ["pol-diff", f, to], queryFn: () => polApi.diff(f, to), enabled: to > 0 });
  if (!state.configured) return <p className="m-0 text-muted">{tr("governance.pipelinepolicy.nessuna_versione_salvata_la_prima")}</p>;
  return (
    <div className="grid grid-cols-1 gap-8 md:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)]">
      <section className="flex min-w-0 flex-col gap-2" aria-label={tr("governance.pipelinepolicy.storico")}>
        <SectionTitle>{tr("governance.pipelinepolicy.storico")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {vs.map((v) => (
            <li key={v.version} className={`border-b border-hair py-2 ${v.version === to ? "bg-sel" : ""}`}>
              <button type="button" className="flex w-full flex-col items-start gap-0.5 bg-transparent p-1 text-left" onClick={() => { setTo(v.version); setFrom(null); }} aria-pressed={v.version === to}>
                <span>
                  <span className="font-mono font-semibold tabular">v{v.version}</span>
                  {v.version === state.version && <span className="ml-2 border border-accent px-1 text-[11px] font-semibold text-accent">{tr("governance.pipelinepolicy.corrente")}</span>}
                  <span className="ml-2 text-[12.5px] text-muted">{when(v.created_at)}{v.created_by ? ` · ${v.created_by}` : ""}</span>
                </span>
                {v.note && <span className="text-[12.5px]">{v.note}</span>}
              </button>
            </li>
          ))}
        </ul>
      </section>
      <section className="flex min-w-0 flex-col gap-3" aria-label={tr("governance.pipelinepolicy.differenze")}>
        <SectionTitle>
          {to > 0 ? tr("governance.pipelinepolicy.differenze_nella_versione", { to }) : tr("governance.pipelinepolicy.differenze")}
        </SectionTitle>
        {to > 0 && (
          <label className="flex max-w-xs items-center gap-2">
            <span className="whitespace-nowrap text-muted">{tr("governance.pipelinepolicy.rispetto_a")}</span>
            <Select className="field" value={f} onChange={(e) => setFrom(Number(e.target.value))}>
              <option value={0}>{tr("governance.pipelinepolicy.nessuna_tutto_e_nuovo")}</option>
              {vs.filter((v) => v.version < to).map((v) => (
                <option key={v.version} value={v.version}>{tr("governance.pipelinepolicy.versione", { version: v.version })}</option>
              ))}
            </Select>
          </label>
        )}
        <ErrorNote error={diff.error} />
        {diff.data && diff.data.changes.length === 0 && <p className="m-0 text-muted">{tr("governance.pipelinepolicy.nessuna_differenza")}</p>}
        {diff.data && <ChangeList changes={diff.data.changes} title={diff.data.changes.length === 1 ? tr("governance.pipelinepolicy.1_modifica") : tr("governance.pipelinepolicy.modifiche", { length: diff.data.changes.length })} />}
        {state.can_edit && to > 0 && to !== state.version && (
          <div>
            <Link to="/configurazione/devsecops/politica" search={{ tab: "matrice", da: to }} className="btn no-underline">{tr("governance.pipelinepolicy.ripristina_nella_matrice")}</Link>
          </div>
        )}
      </section>
    </div>
  );
}

// ---------------------------------------------------------------------
// Adozione: i template che Regia propone ai repository

const EMPTY_SPEC: AdoptionSpec = { templates: [] };

function AdoptionTab({ state }: { state: PolicyState }) {
  const profiles = useQuery({ queryKey: ["pol-profiles"], queryFn: polApi.profiles });
  const [sel, setSel] = useState<string>("");
  if (profiles.error instanceof ApiError && profiles.error.status === 403) {
    return <p className="m-0 text-muted">{tr("governance.pipelinepolicy.serve_il_permesso_amministrare_l")}</p>;
  }
  const items = profiles.data?.items ?? [];
  const cur = items.find((p) => p.id === sel) ?? items.find((p) => (p.definition as { adoption?: unknown }).adoption) ?? items[0];
  return (
    <div className="flex flex-col gap-5">
      <p className="m-0 max-w-3xl text-muted">
        <Trans k="governance.pipelinepolicy.regia_non_conosce_il_contenuto_dei" slots={{ workflows: <strong className="text-fg">{tr("governance.pipelinepolicy.workflow_riusabili")}</strong> }} />
      </p>
      <ErrorNote error={profiles.error} />
      {profiles.data && items.length === 0 && (
        <p className="m-0">
          <Trans k="governance.pipelinepolicy.non_c_e_nessun_profilo_di" slots={{ link: <Link to="/configurazione/integrazioni/github" search={{ tab: "profili" }}>{tr("governance.pipelinepolicy.configurazione_github_profili_di_pipeline")}</Link> }} />
        </p>
      )}
      {cur && (
        <>
          {items.length > 1 && (
            <label className="flex max-w-sm flex-col gap-1">
              <span className="label">{tr("governance.pipelinepolicy.profilo")}</span>
              <Select className="field" value={cur.id} onChange={(e) => setSel(e.target.value)}>
                {items.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
              </Select>
            </label>
          )}
          <AdoptionEditor key={cur.id + ":" + cur.version} profile={cur} controls={state.definition.controls} />
        </>
      )}
    </div>
  );
}

function AdoptionEditor({ profile, controls }: { profile: ProfileItem; controls: Control[] }) {
  const qc = useQueryClient();
  const initial = ((profile.definition as { adoption?: AdoptionSpec }).adoption ?? EMPTY_SPEC) as AdoptionSpec;
  const [spec, setSpec] = useState<AdoptionSpec>(() => structuredClone(initial));
  const [withText, setWithText] = useState<string[]>(() => initial.templates.map((t) => Object.entries(t.with ?? {}).map(([k, v]) => `${k}=${v}`).join("\n")));
  const edit = (fn: (s: AdoptionSpec) => void) =>
    setSpec((s) => {
      const n = structuredClone(s);
      fn(n);
      return n;
    });
  const dirty = JSON.stringify(spec) !== JSON.stringify(initial);
  const save = useMutation({
    mutationFn: () => {
      const clean: AdoptionSpec = structuredClone(spec);
      clean.templates = clean.templates.map((t, i) => ({
        ...t,
        with: Object.fromEntries((withText[i] ?? "").split("\n").map((l) => l.trim()).filter((l) => l.includes("=")).map((l) => [l.slice(0, l.indexOf("=")).trim(), l.slice(l.indexOf("=") + 1).trim()])),
      }));
      return polApi.saveProfile(profile.id, profile.name, { ...profile.definition, adoption: clean }, tr("governance.pipelinepolicy.template_di_adozione"));
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["pol-profiles"] });
      qc.invalidateQueries({ queryKey: ["pol-adoption"] });
    },
  });
  const add = () => {
    edit((s) => {
      let n = s.templates.length + 1;
      while (s.templates.some((t) => t.key === `template_${n}`)) n++;
      s.templates.push({ key: `template_${n}`, name: tr("governance.pipelinepolicy.nuovo_template"), uses: "", default: s.templates.length === 0 });
    });
    setWithText((w) => [...w, ""]);
  };
  const others = (i: number) => spec.templates.filter((_, j) => j !== i);
  return (
    <form className="flex flex-col gap-6" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
      <section className="flex flex-col gap-3">
        <SectionTitle>{tr("governance.pipelinepolicy.file_generati")}</SectionTitle>
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <label className="flex flex-col gap-1">
            <span className="label">{tr("governance.pipelinepolicy.workflow")}</span>
            <input className="field font-mono" placeholder=".github/workflows/regia-pipeline.yml" value={spec.workflow_path ?? ""} onChange={(e) => edit((s) => void (s.workflow_path = e.target.value || undefined))} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label">{tr("governance.pipelinepolicy.file_dei_controlli")}</span>
            <input className="field font-mono" placeholder=".github/pipeline.yml" value={spec.config_path ?? ""} onChange={(e) => edit((s) => void (s.config_path = e.target.value || undefined))} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label">{tr("governance.pipelinepolicy.chiave_dei_controlli_nel_file")}</span>
            <input className="field font-mono" /* i18n-ok: chiave del file di configurazione */ placeholder="checks" value={spec.config_key ?? ""} onChange={(e) => edit((s) => void (s.config_key = e.target.value || undefined))} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label">{tr("governance.pipelinepolicy.branch_che_avviano_il_workflow")}</span>
            <input className="field font-mono" placeholder={tr("governance.pipelinepolicy.il_branch_predefinito")} value={(spec.branches ?? []).join(", ")} onChange={(e) => edit((s) => void (s.branches = e.target.value.split(",").map((x) => x.trim()).filter(Boolean)))} />
          </label>
        </div>
      </section>

      <section className="flex flex-col gap-3">
        <SectionTitle>{tr("governance.pipelinepolicy.parametri_chiesti_nella_procedura")}</SectionTitle>
        <p className="m-0 text-[12.5px] text-muted">{tr("governance.pipelinepolicy.si_usano_nei_valori_dei_template", { example: getLang() === "en" ? "{name}" : "{nome}", available: "{application}, {service}, {repository}, {default_branch}" })}</p>
        {(spec.params ?? []).map((p, i) => (
          <div key={i} className="grid grid-cols-1 gap-2 border-b border-hair pb-3 md:grid-cols-[1fr_1.2fr_1fr_1.4fr_auto] md:items-end">
            <label className="flex flex-col gap-1"><span className="label">{tr("governance.compliance.nome")}</span>
              <input className="field font-mono" value={p.key} onChange={(e) => edit((s) => void (s.params![i]!.key = e.target.value))} /></label>
            <label className="flex flex-col gap-1"><span className="label">{tr("governance.pipelinepolicy.etichetta")}</span>
              <input className="field" value={p.label} onChange={(e) => edit((s) => void (s.params![i]!.label = e.target.value))} /></label>
            <label className="flex flex-col gap-1"><span className="label">{tr("governance.pipelinepolicy.predefinito")}</span>
              <input className="field" value={p.default ?? ""} onChange={(e) => edit((s) => void (s.params![i]!.default = e.target.value))} /></label>
            <label className="flex flex-col gap-1"><span className="label">{tr("governance.pipelinepolicy.opzioni_virgole")}</span>
              <input className="field" value={(p.options ?? []).join(", ")} onChange={(e) => edit((s) => void (s.params![i]!.options = e.target.value.split(",").map((x) => x.trim()).filter(Boolean)))} /></label>
            <button type="button" className="btn btn-danger" onClick={() => edit((s) => void s.params!.splice(i, 1))}>{tr("governance.pipelinepolicy.togli")}</button>
          </div>
        ))}
        <div><button type="button" className="btn" onClick={() => edit((s) => void (s.params = [...(s.params ?? []), { key: "stack", label: "Stack" }]))}>{tr("governance.pipelinepolicy.aggiungi_un_parametro")}</button></div>
      </section>

      <section className="flex flex-col gap-3">
        <SectionTitle>{tr("governance.pipelineadherence.template")}</SectionTitle>
        {spec.templates.length === 0 && <p className="m-0 text-muted">{tr("governance.pipelinepolicy.nessun_template_aggiungi_il_primo")}</p>}
        {spec.templates.map((t, i) => (
          <fieldset key={i} className="m-0 flex flex-col gap-3 border border-hair p-3">
            <legend className="px-1 font-semibold">{t.name || tr("governance.pipelineadherence.template")}</legend>
            <div className="grid grid-cols-1 gap-3 md:grid-cols-[1fr_1fr]">
              <label className="flex flex-col gap-1"><span className="label">{tr("governance.compliance.nome")}</span>
                <input className="field" value={t.name} onChange={(e) => edit((s) => void (s.templates[i]!.name = e.target.value))} /></label>
              <label className="flex flex-col gap-1"><span className="label">{tr("governance.pipelinepolicy.chiave_nome_del_job")}</span>
                <input className="field font-mono" value={t.key} onChange={(e) => edit((s) => void (s.templates[i]!.key = e.target.value))} /></label>
            </div>
            <label className="flex flex-col gap-1"><span className="label">{tr("governance.pipelinepolicy.workflow_riusabile_uses")}</span>
              <input className="field font-mono" placeholder="organizzazione/repository/.github/workflows/ci.yml@v1" value={t.uses} onChange={(e) => edit((s) => void (s.templates[i]!.uses = e.target.value))} /></label>
            <label className="flex flex-col gap-1"><span className="label">{tr("governance.risks.descrizione")}</span>
              <input className="field" value={t.description ?? ""} onChange={(e) => edit((s) => void (s.templates[i]!.description = e.target.value))} /></label>
            <label className="flex flex-col gap-1"><span className="label">{tr("governance.pipelinepolicy.parametri_passati_al_template_uno")}</span>
              <textarea className="field min-h-[72px] font-mono text-[12.5px]" value={withText[i] ?? ""} onChange={(e) => setWithText((w) => w.map((x, j) => (j === i ? e.target.value : x)))} /></label>
            <Chips
              label={tr("governance.pipelinepolicy.controlli_che_esegue")}
              options={controls.map((c): [string, string] => [c.key, c.name])}
              value={t.controls ?? []}
              onChange={(v) => edit((s) => void (s.templates[i]!.controls = v))}
            />
            {others(i).length > 0 && (
              <Chips
                label={tr("governance.pipelinepolicy.dipende_da")}
                options={others(i).map((o): [string, string] => [o.key, o.name])}
                value={t.needs ?? []}
                onChange={(v) => edit((s) => void (s.templates[i]!.needs = v))}
              />
            )}
            <div className="flex flex-wrap items-center gap-5">
              <label className="flex items-center gap-2"><input type="checkbox" checked={!!t.default} onChange={(e) => edit((s) => void (s.templates[i]!.default = e.target.checked))} />{" "}{tr("governance.pipelinepolicy.preselezionato")}</label>
              <label className="flex items-center gap-2"><input type="checkbox" checked={!!t.inherit_secrets} onChange={(e) => edit((s) => void (s.templates[i]!.inherit_secrets = e.target.checked))} />{" "}{tr("governance.pipelinepolicy.passa_i_segreti_del_repository")}</label>
              <button type="button" className="btn btn-danger ml-auto" onClick={() => { edit((s) => void s.templates.splice(i, 1)); setWithText((w) => w.filter((_, j) => j !== i)); }}>{tr("governance.pipelinepolicy.togli_il_template")}</button>
            </div>
          </fieldset>
        ))}
        <div><button type="button" className="btn" onClick={add}>{tr("governance.pipelinepolicy.aggiungi_un_template")}</button></div>
      </section>

      <PolicyError error={save.error} />
      <div className="flex flex-wrap items-center gap-3 border-t-2 border-rule pt-4">
        <button className="btn btn-primary" disabled={!dirty || save.isPending}>{save.isPending ? tr("governance.pipelinepolicy.salvo") : tr("governance.pipelinepolicy.salva_nel_profilo")}</button>
        {save.isSuccess && !dirty && <span className="text-ok">{tr("governance.pipelinepolicy.salvato_nuova_versione_del_profilo")}</span>}
        <span className="text-[12.5px] text-muted">{tr("governance.pipelinepolicy.profilo_versione", { name: profile.name, version: profile.version })}</span>
      </div>
    </form>
  );
}
