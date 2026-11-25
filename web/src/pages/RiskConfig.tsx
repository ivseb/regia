// Configurazione di rischi e incidenti (piano, passo 20): matrici con livelli e fasce,
// soglia di attenzione, preavvisi e colonne del registro delle informazioni, politica di
// accettazione, schemi di classificazione degli incidenti con criteri, regola dell'esito e
// tappe di notifica. Tutto per tenant; export e import in JSON.
import { Select } from "../select";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ErrorNote, SectionTitle, Loading } from "../ui";
import { riskApi, type Band, type Criterion, type Matrix, type RiskConfig as Config, type Scheme, type Settings, type Stage, type Tone } from "../riskApi";
import { Heatmap } from "./riskUi";
import { useT } from "../i18n/react";
import { tr } from "../i18n/core";
import { ConditionBuilder } from "./processEditor/ConditionBuilder";
import { useTypeFacts } from "../casaApi";
import type { FactInfo } from "../api";
import { phrase } from "../casa/gruppi";
import { StepsAdvanced, StepsEditor } from "./riskConfig/Politica";
import { DueAdvanced, DueRows, Timeline } from "./riskConfig/Scadenze";
import { cleanSteps, type Step } from "./riskConfig/model";

const TONES: Record<Tone, string> = {
  get ok() { return tr("configurazione.risk.tone.ok"); },
  get neutral() { return tr("configurazione.risk.tone.neutral"); },
  get accent() { return tr("configurazione.risk.tone.accent"); },
  get danger() { return tr("configurazione.risk.tone.danger"); },
};

export default function RiskConfig() {
  const t = useT();
  const cfg = useQuery({ queryKey: ["risk", "config"], queryFn: riskApi.config });
  const c = cfg.data;
  if (cfg.error) return <ErrorNote error={cfg.error} />;
  if (!c) return <Loading className="text-muted" />;
  return (
    <>
      <p className="m-0 max-w-3xl text-muted">
        {t("configurazione.risk.matriciSogliePoliticaDi")}
        {!c.can_edit && t("configurazione.risk.perModificareMatriciE")}
      </p>
      {c.matrices.map((m) => (
        <MatrixEditor key={m.key} m={m} c={c} />
      ))}
      {c.can_edit && <NewMatrix />}
      <SettingsEditor c={c} />
      <PolicyEditor c={c} />
      {c.schemes.map((s) => (
        <SchemeEditor key={s.key} s={s} canEdit={c.can_edit_schemes} />
      ))}
      {c.can_edit && <ImportExport />}
    </>
  );
}

function useSaved() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ["risk"] });
}

// ---------------------------------------------------------------------
// Matrici

function MatrixEditor({ m, c }: { m: Matrix; c: Config }) {
  const t = useT();
  const saved = useSaved();
  const [draft, setDraft] = useState<Matrix | null>(null);
  const save = useMutation({ mutationFn: (x: Matrix) => riskApi.saveMatrix(x), onSuccess: () => (setDraft(null), saved()) });
  const x = draft ?? m;
  const edit = (fn: (d: Matrix) => void) => {
    const d = structuredClone(x);
    fn(d);
    setDraft(d);
  };
  const resize = (kind: "likelihood" | "impact", n: number) =>
    edit((d) => {
      const cur = d[kind];
      d[kind] = Array.from({ length: n }, (_, i) => cur[i] ?? { value: i + 1, name: tr("configurazione.risk.levelDefault", { n: i + 1 }) });
      const max = d.likelihood.length * d.impact.length;
      const last = d.bands[d.bands.length - 1];
      if (last) last.max = max;
    });
  const cells = x.likelihood.flatMap((l) =>
    x.impact.map((i) => {
      const score = l.value * i.value;
      const b = x.bands.find((b) => score >= b.min && score <= b.max);
      return { likelihood: l.value, impact: i.value, score, band: b?.key ?? "", tone: b?.tone ?? ("neutral" as Tone), count: 0, keys: [] };
    }),
  );
  return (
    <section className="flex flex-col gap-3" aria-label={t("configurazione.risk.matrixNamed", { name: m.name })}>
      <SectionTitle
        aside={
          c.can_edit && !draft ? (
            <button className="text-[12px] text-accent underline" onClick={() => setDraft(structuredClone(m))}>
              {t("configurazione.common.modifica")}
            </button>
          ) : undefined
        }
      >
        {t("configurazione.risk.matrixTitle", { name: m.name })}
        {m.default ? t("configurazione.risk.predefinita") : ""}
      </SectionTitle>
      <div className="grid min-w-0 grid-cols-1 gap-6 lg:grid-cols-[minmax(0,380px)_minmax(0,1fr)]">
        <Heatmap matrix={x} cells={cells} label={t("configurazione.risk.matrixBands", { name: x.name })} />
        {draft ? (
          <form
            className="flex min-w-0 flex-col gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              save.mutate(draft);
            }}
          >
            <div className="flex flex-wrap gap-3">
              <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
                <span className="label">{t("configurazione.common.nome")}</span>
                <input className="field" value={x.name} onChange={(e) => edit((d) => (d.name = e.target.value))} />
              </label>
              <label className="flex w-40 flex-col gap-1">
                <span className="label">{t("configurazione.risk.sogliaDiAttenzione")}</span>
                <input
                  className="field"
                  inputMode="numeric"
                  value={x.appetite_score ?? ""}
                  onChange={(e) => edit((d) => (d.appetite_score = e.target.value === "" ? null : Number(e.target.value)))}
                />
              </label>
              <label className="flex items-center gap-2 pb-2 text-[13px]">
                <input type="checkbox" checked={x.default} onChange={(e) => edit((d) => (d.default = e.target.checked))} />
                {t("configurazione.risk.predefinita2")}
              </label>
            </div>
            {(["likelihood", "impact"] as const).map((kind) => (
              <fieldset key={kind} className="m-0 flex flex-col gap-1 border-0 p-0">
                <legend className="label mb-1 flex items-center gap-2">
                  {kind === "likelihood" ? t("configurazione.risk.probabilita") : t("configurazione.risk.impatto")}
                  <Select aria-label={t("configurazione.risk.numeroDiLivelli")} className="field w-16 py-0" value={x[kind].length} onChange={(e) => resize(kind, Number(e.target.value))}>
                    {[2, 3, 4, 5, 6, 7].map((n) => (
                      <option key={n}>{n}</option>
                    ))}
                  </Select>
                </legend>
                {x[kind].map((lv, i) => (
                  <div key={i} className="flex flex-wrap gap-2">
                    <span className="w-6 pt-1.5 font-mono text-[12px] text-muted">{lv.value}</span>
                    <input className="field w-40" aria-label={t("configurazione.risk.levelName", { n: lv.value })} value={lv.name} onChange={(e) => edit((d) => (d[kind][i].name = e.target.value))} />
                    <input
                      className="field min-w-0 flex-[1_1_220px]"
                      aria-label={t("configurazione.risk.levelDescription", { n: lv.value })}
                      value={lv.description ?? ""}
                      onChange={(e) => edit((d) => (d[kind][i].description = e.target.value))}
                    />
                  </div>
                ))}
              </fieldset>
            ))}
            <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
              <legend className="label mb-1">{t("configurazione.risk.scoreBands", { max: x.likelihood.length * x.impact.length })}</legend>
              {x.bands.map((b, i) => (
                <BandRow key={i} b={b} onChange={(nb) => edit((d) => (d.bands[i] = nb))} onRemove={x.bands.length > 2 ? () => edit((d) => d.bands.splice(i, 1)) : undefined} />
              ))}
              {x.bands.length < 6 && (
                <button
                  type="button"
                  className="self-start text-[12px] text-accent underline"
                  onClick={() => edit((d) => d.bands.push({ key: `fascia_${d.bands.length + 1}`, name: t("configurazione.risk.newBand"), min: 0, max: 0, tone: "danger" }))}
                >
                  {t("configurazione.risk.aggiungiUnaFascia")}
                </button>
              )}
            </fieldset>
            <div className="flex gap-2">
              <button className="btn btn-primary" disabled={save.isPending}>
                {t("configurazione.risk.salvaLaMatrice")}
              </button>
              <button type="button" className="btn" onClick={() => setDraft(null)}>
                {t("configurazione.common.annulla")}
              </button>
            </div>
            <ErrorNote error={save.error} />
          </form>
        ) : (
          <p className="m-0 text-[13px] text-muted">{m.description || t("configurazione.risk.punteggioProbabilitaImpatto")}</p>
        )}
      </div>
    </section>
  );
}

function BandRow({ b, onChange, onRemove }: { b: Band; onChange: (b: Band) => void; onRemove?: () => void }) {
  const t = useT();
  return (
    <div className="flex flex-wrap items-center gap-2">
      <input className="field w-28 font-mono" aria-label={t("configurazione.risk.chiaveDellaFascia")} value={b.key} onChange={(e) => onChange({ ...b, key: e.target.value })} />
      <input className="field w-32" aria-label={t("configurazione.risk.nomeDellaFascia")} value={b.name} onChange={(e) => onChange({ ...b, name: e.target.value })} />
      <input className="field w-16" aria-label={t("configurazione.processEditor.from")} inputMode="numeric" value={b.min} onChange={(e) => onChange({ ...b, min: Number(e.target.value) })} />
      <span className="text-muted">–</span>
      <input className="field w-16" aria-label={t("configurazione.processEditor.to")} inputMode="numeric" value={b.max} onChange={(e) => onChange({ ...b, max: Number(e.target.value) })} />
      <Select className="field w-44" aria-label={t("configurazione.risk.tono")} value={b.tone} onChange={(e) => onChange({ ...b, tone: e.target.value as Tone })}>
        {Object.entries(TONES).map(([k, v]) => (
          <option key={k} value={k}>
            {v}
          </option>
        ))}
      </Select>
      {onRemove && (
        <button type="button" className="text-[12px] text-muted underline hover:text-danger" onClick={onRemove}>
          {t("configurazione.common.togli2")}
        </button>
      )}
    </div>
  );
}

function NewMatrix() {
  const t = useT();
  const saved = useSaved();
  const [key, setKey] = useState("");
  const create = useMutation({
    mutationFn: () =>
      riskApi.saveMatrix({
        key,
        name: t("configurazione.risk.matrix3x3"),
        default: false,
        appetite_score: 4,
        likelihood: [1, 2, 3].map((v) => ({ value: v, name: [t("configurazione.risk.level.lowF"), t("configurazione.risk.level.mediumF"), t("configurazione.risk.level.highF")][v - 1] })),
        impact: [1, 2, 3].map((v) => ({ value: v, name: [t("configurazione.risk.level.lowM"), t("configurazione.risk.level.mediumM"), t("configurazione.risk.level.highM")][v - 1] })),
        bands: [
          { key: "basso", name: t("configurazione.risk.level.lowM"), min: 1, max: 2, tone: "ok" },
          { key: "medio", name: t("configurazione.risk.level.mediumM"), min: 3, max: 4, tone: "neutral" },
          { key: "alto", name: t("configurazione.risk.level.highM"), min: 5, max: 9, tone: "danger" },
        ],
      }),
    onSuccess: () => (setKey(""), saved()),
  });
  return (
    <form
      className="flex flex-wrap items-end gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <label className="flex w-56 flex-col gap-1">
        <span className="label">{t("configurazione.risk.nuovaMatriceChiave")}</span>
        <input className="field font-mono" required pattern="[a-z][a-z0-9_]*" value={key} onChange={(e) => setKey(e.target.value)} placeholder={t("configurazione.risk.esFornitori")} />
      </label>
      <button className="btn" disabled={create.isPending}>
        {t("configurazione.risk.creaUnaMatrice")}
      </button>
      <ErrorNote error={create.error} />
    </form>
  );
}

// ---------------------------------------------------------------------
// Impostazioni e colonne del registro

function SettingsEditor({ c }: { c: Config }) {
  const t = useT();
  const saved = useSaved();
  const [s, setS] = useState<Settings>(c.settings);
  const save = useMutation({ mutationFn: () => riskApi.saveSettings(s), onSuccess: saved });
  const keys = s.register_columns.map((x) => x.key);
  const move = (i: number, d: number) =>
    setS((x) => {
      const cols = [...x.register_columns];
      const j = i + d;
      if (j < 0 || j >= cols.length) return x;
      [cols[i], cols[j]] = [cols[j], cols[i]];
      return { ...x, register_columns: cols };
    });
  return (
    <section className="flex flex-col gap-3" aria-label={t("configurazione.risk.preavvisiERegistroDelle")}>
      <SectionTitle>{t("configurazione.risk.preavvisiERegistroDelle")}</SectionTitle>
      <div className="flex flex-wrap gap-3">
        {(
          [
            ["contract_notice_days", t("configurazione.risk.preavvisoDeiContrattiGiorni")],
            ["review_notice_days", t("configurazione.risk.preavvisoDiRevisioniE")],
            ["notification_warning_hours", t("configurazione.risk.preavvisoDelleNotificheNormati")],
          ] as const
        ).map(([k, label]) => (
          <label key={k} className="flex w-60 flex-col gap-1">
            <span className="label">{label}</span>
            <input className="field" inputMode="numeric" disabled={!c.can_edit} value={s[k]} onChange={(e) => setS({ ...s, [k]: Number(e.target.value) })} />
          </label>
        ))}
      </div>
      <div className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-1">
          <h4 className="label m-0">{t("configurazione.risk.colonneDellExportNell")}</h4>
          <ol className="m-0 flex list-none flex-col p-0">
            {s.register_columns.map((col, i) => (
              <li key={col.key} className="flex items-center gap-2 border-b border-hair py-1 text-[13px]">
                <span className="w-6 font-mono text-[12px] text-muted tabular">{i + 1}</span>
                <input
                  className="field min-w-0 flex-1 py-0.5"
                  aria-label={t("configurazione.risk.headerOf", { key: col.key })}
                  disabled={!c.can_edit}
                  value={col.label}
                  onChange={(e) => setS({ ...s, register_columns: s.register_columns.map((x, j) => (j === i ? { ...x, label: e.target.value } : x)) })}
                />
                {c.can_edit && (
                  <span className="flex shrink-0 gap-2 text-[12px]">
                    <button type="button" aria-label={t("configurazione.risk.up")} className="text-muted hover:text-fg" onClick={() => move(i, -1)}>
                      ↑
                    </button>
                    <button type="button" aria-label={t("configurazione.risk.giu")} className="text-muted hover:text-fg" onClick={() => move(i, 1)}>
                      ↓
                    </button>
                    <button
                      type="button"
                      className="text-muted underline hover:text-danger"
                      onClick={() => setS({ ...s, register_columns: s.register_columns.filter((_, j) => j !== i) })}
                    >
                      {t("configurazione.common.togli2")}
                    </button>
                  </span>
                )}
              </li>
            ))}
          </ol>
        </div>
        {c.can_edit && (
          <div className="flex min-w-0 flex-col gap-1">
            <h4 className="label m-0">{t("configurazione.risk.colonneDisponibili")}</h4>
            <ul className="m-0 flex list-none flex-col p-0">
              {c.column_catalog
                .filter((x) => !keys.includes(x.key))
                .map((x) => (
                  <li key={x.key} className="flex items-baseline gap-2 border-b border-hair py-1 text-[13px]">
                    <span className="min-w-0 flex-1">
                      {x.label} <span className="text-[12px] text-muted">· {x.group}</span>
                    </span>
                    <button type="button" className="text-[12px] text-accent underline" onClick={() => setS({ ...s, register_columns: [...s.register_columns, { key: x.key, label: x.label }] })}>
                      {t("configurazione.risk.aggiungi")}
                    </button>
                  </li>
                ))}
            </ul>
          </div>
        )}
      </div>
      {c.can_edit && (
        <button className="btn btn-primary self-start" disabled={save.isPending} onClick={() => save.mutate()}>
          {t("configurazione.common.salvaLeImpostazioni")}
        </button>
      )}
      <ErrorNote error={save.error} />
    </section>
  );
}

// ---------------------------------------------------------------------
// Politica di accettazione

function PolicyEditor({ c }: { c: Config }) {
  const t = useT();
  const saved = useSaved();
  const [rows, setRows] = useState<{ op: string; n: string; steps: Step[] }[]>(() =>
    c.policy.rules.map((r) => ({ op: r.when.risk_gt != null ? "gt" : "lte", n: String(r.when.risk_gt ?? r.when.risk_lte ?? ""), steps: r.steps.map((x) => ({ approvers: x.approvers.map((a) => ({ ...a })) })) })),
  );
  const save = useMutation({
    mutationFn: () =>
      riskApi.savePolicy(rows.map((r) => ({ when: r.n === "" ? {} : r.op === "gt" ? { risk_gt: Number(r.n) } : { risk_lte: Number(r.n) }, steps: cleanSteps(r.steps) }))),
    onSuccess: saved,
  });
  const set = (i: number, p: Partial<(typeof rows)[number]>) => setRows(rows.map((x, j) => (j === i ? { ...x, ...p } : x)));
  return (
    <section className="flex flex-col gap-3" aria-label={t("configurazione.risk.politicaDiAccettazione")}>
      <SectionTitle>{t("configurazione.risk.accettazioneDelRischioResiduo")}</SectionTitle>
      <p className="m-0 max-w-3xl text-[13px] text-muted">
        {t("configurazione.risk.vinceLaPrimaRegola")}
      </p>
      {rows.map((r, i) => (
        <div key={i} className="flex min-w-0 flex-col gap-2 border-b border-hair pb-3" data-testid="regola-accettazione">
          <div className="flex flex-wrap items-center gap-2 text-[13px]">
            <span className="label">{t("configurazione.rischi.seIlRischio")}</span>
            <Select className="field w-28" aria-label={t("configurazione.risk.soglia")} disabled={!c.can_edit} value={r.op} onChange={(e) => set(i, { op: e.target.value })}>
              <option value="lte">{t("configurazione.risk.finoA")}</option>
              <option value="gt">{t("configurazione.risk.above")}</option>
            </Select>
            <input className="field w-20" aria-label={t("configurazione.common.punteggio")} inputMode="numeric" disabled={!c.can_edit} value={r.n} onChange={(e) => set(i, { n: e.target.value })} />
            <span className="text-[12.5px] text-muted">{r.n === "" ? t("configurazione.rischi.altrimenti") : t("configurazione.rischi.approvaIn")}</span>
            {c.can_edit && rows.length > 1 && (
              <button type="button" className="touch-44 ml-auto inline-flex items-center text-[12px] text-muted underline hover:text-danger" onClick={() => setRows(rows.filter((_, j) => j !== i))}>
                {t("configurazione.common.togli2")}
              </button>
            )}
          </div>
          <StepsEditor steps={r.steps} disabled={!c.can_edit} onChange={(steps) => set(i, { steps })} />
          <StepsAdvanced steps={r.steps} disabled={!c.can_edit} onChange={(steps) => set(i, { steps })} />
        </div>
      ))}
      {c.can_edit && (
        <div className="flex flex-wrap gap-2">
          <button type="button" className="btn" onClick={() => setRows([...rows, { op: "gt", n: "", steps: [{ approvers: [{ role: "risk_owner" }] }] }])}>
            {t("configurazione.common.aggiungiUnaRegola")}
          </button>
          <button className="btn btn-primary" disabled={save.isPending} onClick={() => save.mutate()}>
            {t("configurazione.common.salvaLaPolitica")}
          </button>
        </div>
      )}
      <ErrorNote error={save.error} />
    </section>
  );
}

// ---------------------------------------------------------------------
// Schemi di classificazione

function SchemeEditor({ s, canEdit }: { s: Scheme; canEdit: boolean }) {
  const t = useT();
  const saved = useSaved();
  const [d, setD] = useState<Scheme | null>(null);
  const [incident, setIncident] = useState("");
  const incidentFacts = useTypeFacts("incident");
  const x = d ?? s;
  const names = Object.fromEntries(incidentFacts.facts.map((f) => [f.key, f.name]));
  const edit = (fn: (y: Scheme) => void) => {
    const y = structuredClone(x);
    fn(y);
    setD(y);
  };
  const save = useMutation({ mutationFn: () => riskApi.saveScheme(x), onSuccess: () => (setD(null), saved()) });
  const preview = useMutation({ mutationFn: () => riskApi.previewScheme(x, incident) });
  return (
    <section className="flex flex-col gap-3" aria-label={t("configurazione.risk.schemeNamed", { name: s.name })}>
      <SectionTitle
        aside={
          canEdit && !d ? (
            <button className="text-[12px] text-accent underline" onClick={() => setD(structuredClone(s))}>
              {t("configurazione.common.modifica")}
            </button>
          ) : (
            <span className="text-[12px] text-muted">{s.active ? t("configurazione.risk.attivo") : t("configurazione.common.nonAttivo")}</span>
          )
        }
      >
        {t("configurazione.risk.classificationTitle", { name: s.name })}
      </SectionTitle>
      {!d ? (
        <div className="flex flex-col gap-2 text-[13px]">
          {s.description && <p className="m-0 max-w-3xl">{s.description}</p>}
          {s.reference && <p className="m-0 text-[12px] text-muted">{s.reference}</p>}
          <table className="w-full border-collapse">
            <tbody>
              {s.criteria.map((c) => (
                <tr key={c.key} className="border-b border-hair align-baseline">
                  <td className="py-1 pr-3 font-medium">
                    {c.name}
                    {c.primary && <span className="text-[12px] text-muted"> {t("configurazione.risk.principale")}</span>}
                  </td>
                  <td className="py-1 text-[12.5px] text-muted" title={c.condition}>{c.condition ? phrase(c.condition, names) : t("casa.risk.criterionNever")}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="m-0">
            <span className="label mr-2">{t("configurazione.risk.outcomeIf", { name: s.outcome_name })}</span>
            <span className="text-[12.5px]" title={s.when}>{s.when ? phrase(s.when, { ...Object.fromEntries(s.criteria.map((c) => [`criterio.${c.key}`, tr("casa.risk.criterionMet", { name: c.name })])), criteri_secondari: tr("casa.risk.secondaryCount"), ...names }) : t("casa.risk.outcomeNever")}</span>
          </p>
          <Timeline stages={s.stages} />
        </div>
      ) : (
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <div className="flex flex-wrap gap-3">
            <label className="flex min-w-0 flex-[1_1_240px] flex-col gap-1">
              <span className="label">{t("configurazione.common.nome")}</span>
              <input className="field" value={x.name} onChange={(e) => edit((y) => (y.name = e.target.value))} />
            </label>
            <label className="flex w-40 flex-col gap-1">
              <span className="label">{t("configurazione.risk.esitoPositivo")}</span>
              <input className="field" value={x.outcome_name} onChange={(e) => edit((y) => (y.outcome_name = e.target.value))} />
            </label>
            <label className="flex items-center gap-2 pb-2 text-[13px]">
              <input type="checkbox" checked={x.active} onChange={(e) => edit((y) => (y.active = e.target.checked))} />
              {t("configurazione.risk.attivo")}
            </label>
          </div>
          <label className="flex flex-col gap-1">
            <span className="label">{t("configurazione.risk.riferimentoSoloIlRiferimento")}</span>
            <input className="field" value={x.reference ?? ""} onChange={(e) => edit((y) => (y.reference = e.target.value))} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label">{t("configurazione.common.descrizione")}</span>
            <textarea className="field min-h-12" value={x.description ?? ""} onChange={(e) => edit((y) => (y.description = e.target.value))} />
          </label>
          <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
            <legend className="label mb-1">{t("configurazione.risk.criteriCondizioniSuiFatti")}</legend>
            {x.criteria.map((c, i) => (
              <CriterionRow
                key={i}
                c={c}
                onChange={(nc) => edit((y) => (y.criteria[i] = nc))}
                onRemove={x.criteria.length > 1 ? () => edit((y) => y.criteria.splice(i, 1)) : undefined}
              />
            ))}
            <button type="button" className="self-start text-[12px] text-accent underline" onClick={() => edit((y) => y.criteria.push({ key: `criterio_${y.criteria.length + 1}`, name: t("configurazione.risk.newCriterion"), condition: "clienti_coinvolti: >=1000" }))}>
              {t("configurazione.risk.aggiungiUnCriterio")}
            </button>
          </fieldset>
          <ConditionBuilder
            label={t("configurazione.risk.regolaDellEsitoUsa")}
            value={x.when}
            onChange={(c) => edit((y) => (y.when = c))}
            facts={outcomeFacts(x.criteria, incidentFacts.facts)}
            empty={t("casa.risk.outcomeNever")}
          />
          <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
            <legend className="label mb-1">{t("configurazione.risk.tappeDiNotificaTermini")}</legend>
            {x.stages.map((st, i) => (
              <StageRow key={i} st={st} earlier={x.stages.slice(0, i)} onChange={(ns) => edit((y) => (y.stages[i] = ns))} onRemove={() => edit((y) => y.stages.splice(i, 1))} />
            ))}
            <button type="button" className="self-start text-[12px] text-accent underline" onClick={() => edit((y) => y.stages.push({ key: `tappa_${y.stages.length + 1}`, name: t("configurazione.risk.newMilestone"), due: [{ from: "detected", hours: 24 }] }))}>
              {t("configurazione.risk.aggiungiUnaTappa")}
            </button>
            <Timeline stages={x.stages} />
          </fieldset>
          <div className="flex flex-wrap items-end gap-2">
            <label className="flex w-48 flex-col gap-1">
              <span className="label">{t("configurazione.risk.anteprimaSuUnIncidente")}</span>
              <input className="field font-mono" placeholder="INC-1" value={incident} onChange={(e) => setIncident(e.target.value.toUpperCase())} />
            </label>
            <button type="button" className="btn" disabled={preview.isPending} onClick={() => preview.mutate()}>
              {t("configurazione.common.prova")}
            </button>
            {preview.data?.result && (
              <span className={`text-[13px] ${preview.data.result.positive ? "font-semibold text-flag" : "text-muted"}`}>
                {preview.data.result.outcome} · {preview.data.result.criteria.filter((c) => c.met).map((c) => c.name).join(", ") || t("configurazione.risk.nessunCriterio")}
              </span>
            )}
            {preview.data && !preview.data.result && <span className="text-[13px] text-ok">{t("configurazione.risk.schemaValido")}</span>}
          </div>
          <div className="flex gap-2">
            <button className="btn btn-primary" disabled={save.isPending}>
              {t("configurazione.risk.salvaLoSchema")}
            </button>
            <button type="button" className="btn" onClick={() => setD(null)}>
              {t("configurazione.common.annulla")}
            </button>
          </div>
          <ErrorNote error={save.error ?? preview.error} />
        </form>
      )}
    </section>
  );
}

/** Dati dell'esito: quelli dell'incidente più «criterio.<chiave>» per ogni criterio e il numero dei secondari. */
function outcomeFacts(criteria: Criterion[], base: FactInfo[]): FactInfo[] {
  return [
    ...criteria.map((c): FactInfo => ({ key: `criterio.${c.key}`, name: tr("casa.risk.criterionMet", { name: c.name }), kind: "bool", source: "modulo" })),
    { key: "criteri_secondari", name: tr("casa.risk.secondaryCount"), kind: "number", source: "modulo" },
    ...base,
  ];
}

function CriterionRow({ c, onChange, onRemove }: { c: Criterion; onChange: (c: Criterion) => void; onRemove?: () => void }) {
  const t = useT();
  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-hair pb-1">
      <input className="field w-36 font-mono text-[12.5px]" aria-label={t("configurazione.common.chiave")} value={c.key} onChange={(e) => onChange({ ...c, key: e.target.value })} />
      <input className="field w-48" aria-label={t("configurazione.common.nome")} value={c.name} onChange={(e) => onChange({ ...c, name: e.target.value })} />
      <div className="min-w-0 basis-full">
        <ConditionBuilder label={t("configurazione.common.condizione")} value={c.condition} onChange={(v) => onChange({ ...c, condition: v })} type="incident" noun={t("casa.risk.nounIncidents")} empty={t("casa.risk.criterionNever")} />
      </div>
      <label className="flex items-center gap-1 text-[12.5px]">
        <input type="checkbox" checked={!!c.primary} onChange={(e) => onChange({ ...c, primary: e.target.checked })} />
        {t("configurazione.risk.principale2")}
      </label>
      {onRemove && (
        <button type="button" className="text-[12px] text-muted underline hover:text-danger" onClick={onRemove}>
          {t("configurazione.common.togli2")}
        </button>
      )}
    </div>
  );
}

function StageRow({ st, earlier, onChange, onRemove }: { st: Stage; earlier: Stage[]; onChange: (s: Stage) => void; onRemove: () => void }) {
  const t = useT();
  return (
    <div className="flex flex-col gap-2 border-b border-hair pb-2" data-testid="tappa">
      <div className="flex flex-wrap items-center gap-2">
        <input className="field w-32 font-mono text-[12.5px]" aria-label={t("configurazione.common.chiave")} value={st.key} onChange={(e) => onChange({ ...st, key: e.target.value })} />
        <input className="field min-w-0 flex-[1_1_200px]" aria-label={t("configurazione.common.nome")} value={st.name} onChange={(e) => onChange({ ...st, name: e.target.value })} />
        <button type="button" className="touch-44 inline-flex items-center text-[12px] text-muted underline hover:text-danger" onClick={onRemove}>
          {t("configurazione.common.togli2")}
        </button>
      </div>
      <DueRows st={st} earlier={earlier} onChange={(due) => onChange({ ...st, due })} />
      <DueAdvanced st={st} onChange={(due) => onChange({ ...st, due })} />
    </div>
  );
}

// ---------------------------------------------------------------------
// Export e import

function ImportExport() {
  const t = useT();
  const saved = useSaved();
  const [raw, setRaw] = useState("");
  const exp = useMutation({ mutationFn: riskApi.exportConfig, onSuccess: (p) => setRaw(JSON.stringify(p, null, 2)) });
  const imp = useMutation({ mutationFn: () => riskApi.importConfig(raw), onSuccess: saved });
  return (
    <section className="flex flex-col gap-2" aria-label={t("configurazione.risk.exportEImport")}>
      <SectionTitle>{t("configurazione.risk.exportEImportJson")}</SectionTitle>
      <textarea className="field min-h-32 font-mono text-[12px]" aria-label={t("configurazione.risk.configurazioneInJson")} value={raw} onChange={(e) => setRaw(e.target.value)} placeholder={t("configurazione.risk.formatRegiaRiskV")} />
      <div className="flex gap-2">
        <button className="btn" disabled={exp.isPending} onClick={() => exp.mutate()}>
          {t("configurazione.risk.esportaLaConfigurazione")}
        </button>
        <button className="btn btn-primary" disabled={imp.isPending || !raw.trim()} onClick={() => imp.mutate()}>
          {t("configurazione.common.importa")}
        </button>
        {imp.data && (
          <span className="self-center text-[13px] text-ok">
            {t("configurazione.risk.imported", { matrices: imp.data.matrices.length, schemes: imp.data.schemes.length })}
          </span>
        )}
      </div>
      {imp.error && (
        <p role="alert" className="m-0 text-danger">
          {String((imp.error as Error).message)}
        </p>
      )}
      <ErrorNote error={exp.error} />
    </section>
  );
}
