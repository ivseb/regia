// Sezione «Esito» del test di resilienza (elemento `resilience_test`): tipo e date, RTO/RPO misurati
// contro richiesti e dichiarati, esito, rilievi (ognuno con il lavoro da creare nel team), note
// ed evidenza nata entrando in «Eseguito».
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Select } from "../../select";
import { ErrorNote, Loading, SectionTitle } from "../../ui";
import { useT } from "../../i18n/react";
import { ProblemActions } from "../ProblemActions";
import { OUTCOMES, continuityApi, withinLimit, type Outcome, type TestInput, type TestView } from "../../continuityApi";
import { DurationInput, Minutes, OkMark } from "./ui";

export function TestSection({ refKey }: { refKey: string }) {
  const q = useQuery({ queryKey: ["continuity", "test", refKey], queryFn: () => continuityApi.test(refKey) });
  const types = useQuery({ queryKey: ["continuity", "settings"], queryFn: continuityApi.settings, staleTime: 60_000 });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorNote error={q.error} />;
  return <TestEditor key={stamp(q.data)} view={q.data} refKey={refKey} types={types.data?.config.test_types ?? []} />;
}

const stamp = (v: TestView) =>
  JSON.stringify([v.state, v.type, v.planned_on, v.executed_on, v.rto_measured_minutes, v.rpo_measured_minutes, v.outcome, v.notes, v.findings.map((f) => f.text)]);

type Draft = {
  type: string;
  planned_on: string;
  executed_on: string;
  rto: number | null;
  rpo: number | null;
  outcome: Outcome | "";
  findings: string[];
  notes: string;
};

const fromView = (v: TestView): Draft => ({
  type: v.type,
  planned_on: v.planned_on ?? "",
  executed_on: v.executed_on ?? "",
  rto: v.rto_measured_minutes,
  rpo: v.rpo_measured_minutes,
  outcome: v.outcome ?? "",
  findings: v.findings.map((f) => f.text),
  notes: v.notes ?? "",
});

function changes(v: TestView, d: Draft): TestInput {
  const o = fromView(v);
  const out: TestInput = {};
  if (o.type !== d.type) out.type = d.type;
  if (o.planned_on !== d.planned_on) out.planned_on = d.planned_on || null;
  if (o.executed_on !== d.executed_on) out.executed_on = d.executed_on || null;
  if (o.rto !== d.rto) out.rto_measured_minutes = d.rto;
  if (o.rpo !== d.rpo) out.rpo_measured_minutes = d.rpo;
  if (o.outcome !== d.outcome) out.outcome = d.outcome || null;
  const clean = d.findings.map((f) => f.trim()).filter(Boolean);
  if (JSON.stringify(o.findings) !== JSON.stringify(clean)) out.findings = clean;
  if (o.notes !== d.notes) out.notes = d.notes;
  return out;
}

function TestEditor({ view, refKey, types }: { view: TestView; refKey: string; types: { key: string; name: string }[] }) {
  const t = useT();
  const qc = useQueryClient();
  const [d, setD] = useState<Draft>(() => fromView(view));
  const edit = view.can.edit;
  const diff = changes(view, d);
  const dirty = Object.keys(diff).length > 0;
  const save = useMutation({
    mutationFn: () => continuityApi.saveTest(refKey, diff),
    onSuccess: (v) => {
      qc.setQueryData(["continuity", "test", refKey], v);
      void qc.invalidateQueries({ queryKey: ["continuity", "tests"] });
      void qc.invalidateQueries({ queryKey: ["element", refKey] });
    },
  });
  // Ok/non ok sul valore che si sta scrivendo; dal server quando non è cambiato.
  const rtoOk = d.rto === view.rto_measured_minutes ? view.rto_ok : withinLimit(d.rto, view.required.rto ?? view.declared.rto);
  const rpoOk = d.rpo === view.rpo_measured_minutes ? view.rpo_ok : withinLimit(d.rpo, view.required.rpo ?? view.declared.rpo);
  const typeOptions = types.some((x) => x.key === view.type) ? types : [{ key: view.type, name: view.type_name || view.type }, ...types];
  const setFinding = (i: number, text: string) => setD((x) => ({ ...x, findings: x.findings.map((f, j) => (j === i ? text : f)) }));

  return (
    <section className="flex min-w-0 flex-col gap-5" aria-label={t("continuita.test.title")}>
      <SectionTitle
        aside={
          <Link to="/architettura/continuita/test" search={{ anno: Number((view.executed_on ?? view.planned_on ?? "").slice(0, 4)) || undefined }} className="text-[12px] text-accent underline">
            {t("continuita.test.plan")}
          </Link>
        }
      >
        {t("continuita.test.title")}
      </SectionTitle>
      <p className="m-0 text-[12.5px]">
        <span className="text-muted">{view.subject.kind === "application" ? t("continuita.kind.application") : t("continuita.kind.capability")} </span>
        <Link to="/e/$key" params={{ key: view.subject.key }} className="font-medium">
          {view.subject.title}
        </Link>
      </p>

      <div className="flex flex-wrap items-end gap-4">
        <label className="flex w-full flex-col gap-1 sm:w-56">
          <span className="label">{t("continuita.test.type")}</span>
          {edit ? (
            <Select className="field" value={d.type} onChange={(e) => setD((x) => ({ ...x, type: e.target.value }))}>
              {typeOptions.map((x) => (
                <option key={x.key} value={x.key}>
                  {x.name}
                </option>
              ))}
            </Select>
          ) : (
            <span>{view.type_name || view.type}</span>
          )}
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("continuita.test.plannedOn")}</span>
          <input className="field" type="date" value={d.planned_on} disabled={!edit} onChange={(e) => setD((x) => ({ ...x, planned_on: e.target.value }))} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("continuita.test.executedOn")}</span>
          <input className="field" type="date" value={d.executed_on} disabled={!edit} onChange={(e) => setD((x) => ({ ...x, executed_on: e.target.value }))} />
        </label>
      </div>

      {/* Misurato contro richiesto e dichiarato. */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        {([
          ["rto", t("continuita.field.rto"), d.rto, view.required.rto, view.declared.rto, rtoOk],
          ["rpo", t("continuita.field.rpo"), d.rpo, view.required.rpo, view.declared.rpo, rpoOk],
        ] as const).map(([k, label, measured, required, declared, ok]) => (
          <div key={k} className="flex flex-col gap-2 border-l-4 border-hair pl-3">
            <DurationInput
              label={t("continuita.test.measured", { label })}
              value={measured}
              disabled={!edit}
              onChange={(m) => setD((x) => (k === "rto" ? { ...x, rto: m } : { ...x, rpo: m }))}
            />
            <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-4 gap-y-0.5 text-[12.5px]">
              <dt className="text-muted">{t("continuita.test.required")}</dt>
              <dd className="m-0">
                <Minutes m={required} />
              </dd>
              <dt className="text-muted">{t("continuita.test.declared")}</dt>
              <dd className="m-0">
                <Minutes m={declared} />
              </dd>
              <dt className="text-muted">{t("continuita.test.result")}</dt>
              <dd className="m-0">
                <OkMark ok={ok} />
              </dd>
            </dl>
          </div>
        ))}
      </div>

      <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
        <legend className="label mb-1">{t("continuita.test.outcome")}</legend>
        <div className="flex flex-wrap gap-x-5 gap-y-1 text-[13px]">
          {OUTCOMES.map((o) => (
            <label key={o} className="flex min-h-[32px] items-center gap-1.5">
              <input type="radio" name={`esito-${view.id}`} checked={d.outcome === o} disabled={!edit} onChange={() => setD((x) => ({ ...x, outcome: o }))} />
              <span className={o === "failed" && d.outcome === o ? "font-semibold text-danger" : ""}>{t(`continuita.outcome.${o}`)}</span>
            </label>
          ))}
          {edit && d.outcome !== "" && (
            <button type="button" className="bg-transparent p-0 text-[12.5px] text-muted underline" onClick={() => setD((x) => ({ ...x, outcome: "" }))}>
              {t("continuita.test.clearOutcome")}
            </button>
          )}
        </div>
        {!view.outcome && <span className="text-[12px] text-muted">{t("continuita.test.outcomeHint")}</span>}
      </fieldset>

      <section className="flex flex-col gap-2" aria-label={t("continuita.test.findings")}>
        <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{d.findings.length || ""}</span>}>{t("continuita.test.findings")}</SectionTitle>
        {d.findings.length === 0 && <p className="m-0 text-[13px] text-muted">{t("continuita.test.noFindings")}</p>}
        <ol className="m-0 flex list-none flex-col gap-3 p-0">
          {d.findings.map((f, i) => {
            // Il riferimento del rilievo vale solo per il testo salvato in quella posizione.
            const saved = view.findings[i] && view.findings[i].text === f.trim() ? view.findings[i] : null;
            return (
              <li key={i} className="flex flex-col gap-1.5 border-b border-hair pb-3">
                <div className="flex items-start gap-2">
                  <span className="pt-1.5 font-mono text-[12px] text-muted tabular">{i + 1}.</span>
                  {edit ? (
                    <textarea
                      className="field min-h-12 min-w-0 flex-1"
                      aria-label={t("continuita.test.findingLabel", { n: i + 1 })}
                      value={f}
                      onChange={(e) => setFinding(i, e.target.value)}
                    />
                  ) : (
                    <p className="m-0 min-w-0 flex-1 pt-1 whitespace-pre-wrap">{f}</p>
                  )}
                  {edit && (
                    <button
                      type="button"
                      className="bg-transparent p-0 pt-1.5 text-[12px] text-muted underline hover:text-danger"
                      onClick={() => setD((x) => ({ ...x, findings: x.findings.filter((_, j) => j !== i) }))}
                    >
                      {t("continuita.test.removeFinding")}
                    </button>
                  )}
                </div>
                {saved?.work && (
                  <p className="m-0 pl-6 text-[12.5px]">
                    <span className="text-muted">{t("continuita.test.work")} </span>
                    <Link to="/e/$key" params={{ key: saved.work.key }}>
                      <span className="font-mono">{saved.work.key}</span> {saved.work.title}
                    </Link>
                    <span className="text-muted"> · {saved.work.state}</span>
                  </p>
                )}
                {saved && !dirty && (
                  <div className="pl-6">
                    <ProblemActions kind="continuity" refKey={saved.ref} compact />
                  </div>
                )}
              </li>
            );
          })}
        </ol>
        {edit && (
          <button type="button" className="btn self-start" onClick={() => setD((x) => ({ ...x, findings: [...x.findings, ""] }))}>
            {t("continuita.test.addFinding")}
          </button>
        )}
      </section>

      <label className="flex flex-col gap-1">
        <span className="label">{t("continuita.test.notes")}</span>
        <textarea className="field min-h-20" value={d.notes} disabled={!edit} onChange={(e) => setD((x) => ({ ...x, notes: e.target.value }))} />
      </label>

      {edit && (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap gap-2">
            <button type="button" className="btn btn-primary min-h-[44px]" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
              {save.isPending ? t("continuita.common.saving") : t("continuita.common.save")}
            </button>
            {dirty && (
              <button type="button" className="btn min-h-[44px]" onClick={() => setD(fromView(view))}>
                {t("continuita.common.discard")}
              </button>
            )}
          </div>
          <ErrorNote error={save.error} />
        </div>
      )}

      <section className="flex flex-col gap-1" aria-label={t("continuita.test.evidence")}>
        <SectionTitle>{t("continuita.test.evidence")}</SectionTitle>
        {view.evidence ? (
          <p className="m-0 text-[13px]">
            {t("continuita.test.evidenceCreated")}{" "}
            <Link to="/e/$key" params={{ key: view.evidence.key }} className="font-mono">
              {view.evidence.key}
            </Link>
          </p>
        ) : view.evidence_note ? (
          <p className="m-0 text-[13px] text-muted">{view.evidence_note}</p>
        ) : (
          <p className="m-0 text-[13px] text-muted">{t("continuita.test.evidenceLater")}</p>
        )}
      </section>
    </section>
  );
}
