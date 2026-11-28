// Sezione «Impatto nel tempo» della BIA (elemento di tipo `bia`): griglia orizzonti × categorie,
// proposta calcolata mentre si compila (stessa regola del backend), valori scelti, critica sì/no,
// versioni approvate e applicazioni della capability con il dichiarato contro il richiesto.
import { useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Select } from "../../select";
import { ErrorNote, Loading, SectionTitle, date } from "../../ui";
import { useT } from "../../i18n/react";
import { continuityApi, fmtMinutes, gridComplete, proposeBia, type BiaInput, type BiaView, type Impacts } from "../../continuityApi";
import { DurationInput, Minutes, OkMark } from "./ui";

export function BiaSection({ refKey }: { refKey: string }) {
  const q = useQuery({ queryKey: ["continuity", "bia", refKey], queryFn: () => continuityApi.bia(refKey) });
  if (q.isPending) return <Loading />;
  if (q.error) return <ErrorNote error={q.error} />;
  // La chiave rimonta l'editor quando il server restituisce una versione nuova (salvataggio, approvazione).
  return <BiaEditor key={stamp(q.data)} view={q.data} refKey={refKey} />;
}

const stamp = (v: BiaView) => JSON.stringify([v.state, v.impacts, v.critical, v.critical_reason, v.mtpd_minutes, v.rto_minutes, v.rpo_minutes, v.notes, v.versions.length]);

type Draft = {
  impacts: Impacts;
  critical: boolean | null;
  critical_reason: string;
  mtpd_minutes: number | null;
  rto_minutes: number | null;
  rpo_minutes: number | null;
  notes: string;
};

const fromView = (v: BiaView): Draft => ({
  impacts: v.impacts ?? {},
  critical: v.critical,
  critical_reason: v.critical_reason ?? "",
  mtpd_minutes: v.mtpd_minutes,
  rto_minutes: v.rto_minutes,
  rpo_minutes: v.rpo_minutes,
  notes: v.notes ?? "",
});

/** Solo i campi cambiati, come chiede PUT /bia/{rif}. */
function changes(v: BiaView, d: Draft): BiaInput {
  const o = fromView(v);
  const out: BiaInput = {};
  if (JSON.stringify(o.impacts) !== JSON.stringify(d.impacts)) out.impacts = d.impacts;
  if (o.critical !== d.critical) out.critical = d.critical;
  if (o.critical_reason !== d.critical_reason) out.critical_reason = d.critical_reason;
  if (o.mtpd_minutes !== d.mtpd_minutes) out.mtpd_minutes = d.mtpd_minutes;
  if (o.rto_minutes !== d.rto_minutes) out.rto_minutes = d.rto_minutes;
  if (o.rpo_minutes !== d.rpo_minutes) out.rpo_minutes = d.rpo_minutes;
  if (o.notes !== d.notes) out.notes = d.notes;
  return out;
}

function BiaEditor({ view, refKey }: { view: BiaView; refKey: string }) {
  const t = useT();
  const qc = useQueryClient();
  const [d, setD] = useState<Draft>(() => fromView(view));
  const edit = view.can.edit;
  const cfg = view.config;
  const horizons = useMemo(() => [...cfg.horizons].sort((a, b) => a.minutes - b.minutes), [cfg.horizons]);
  const proposal = useMemo(() => (edit ? proposeBia(cfg, d.impacts) : (view.proposal ?? proposeBia(cfg, d.impacts))), [edit, cfg, d.impacts, view.proposal]);
  const diff = changes(view, d);
  const dirty = Object.keys(diff).length > 0;
  const done = (v: BiaView) => {
    qc.setQueryData(["continuity", "bia", refKey], v);
    void qc.invalidateQueries({ queryKey: ["continuity"], predicate: (x) => x.queryKey[1] !== "bia" || x.queryKey[2] !== refKey });
    void qc.invalidateQueries({ queryKey: ["element", refKey] });
  };
  const save = useMutation({ mutationFn: () => continuityApi.saveBia(refKey, diff), onSuccess: done });
  const approve = useMutation({ mutationFn: () => continuityApi.approveBia(refKey), onSuccess: done });
  const setLevel = (cat: string, hor: string, v: string) =>
    setD((x) => {
      const row = { ...(x.impacts[cat] ?? {}) };
      if (v === "") delete row[hor];
      else row[hor] = Number(v);
      return { ...x, impacts: { ...x.impacts, [cat]: row } };
    });
  const complete = gridComplete(cfg, d.impacts) && d.rto_minutes != null && d.rpo_minutes != null;
  const catName = (k: string | null) => cfg.categories.find((c) => c.key === k)?.name ?? k ?? "";
  const horName = (k: string | null) => cfg.horizons.find((h) => h.key === k)?.name ?? k ?? "";

  return (
    <section className="flex min-w-0 flex-col gap-5" aria-label={t("continuita.biaSection.title")}>
      <SectionTitle
        aside={
          <Link to="/architettura/continuita/bia" className="text-[12px] text-accent underline">
            {t("continuita.biaSection.allBia")}
          </Link>
        }
      >
        {t("continuita.biaSection.title")}
      </SectionTitle>
      <p className="m-0 text-[12.5px]">
        <span className="text-muted">{t("continuita.biaSection.capability")} </span>
        <Link to="/e/$key" params={{ key: view.capability.key }} className="font-medium">
          {view.capability.title}
        </Link>
      </p>
      {view.config_changed && (
        <p role="status" className="m-0 border-l-4 border-flag pl-3 text-[13px]">
          {t("continuita.biaSection.configChanged", { v: view.settings_version })}
        </p>
      )}
      {!edit && <p className="m-0 text-[12.5px] text-muted">{t("continuita.biaSection.readOnly")}</p>}

      {/* Griglia: sul telefono scorre dentro il suo riquadro, non la pagina. */}
      <div className="max-w-full overflow-x-auto border border-hair" role="region" aria-label={t("continuita.biaSection.grid")} tabIndex={0}>
        <table className="w-full min-w-[560px] border-collapse text-[12.5px]">
          <caption className="sr-only">{t("continuita.biaSection.gridCaption")}</caption>
          <thead>
            <tr className="border-b border-rule">
              <th scope="col" className="sticky left-0 z-[1] bg-bg px-2 py-1.5 text-left font-semibold">
                {t("continuita.biaSection.category")}
              </th>
              {horizons.map((h) => (
                <th key={h.key} scope="col" className="px-2 py-1.5 text-left font-semibold whitespace-nowrap">
                  {h.name}
                  <span className="block text-[11px] font-normal text-muted tabular">{fmtMinutes(h.minutes)}</span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {cfg.categories.map((c) => (
              <tr key={c.key} className="border-b border-hair">
                <th scope="row" className="sticky left-0 z-[1] bg-bg px-2 py-1.5 text-left align-top font-medium">
                  {c.name}
                  <span className="block text-[11px] font-normal text-muted">
                    {t("continuita.biaSection.intolerableFrom", { label: c.levels.find((l) => l.value === c.intolerable)?.label ?? String(c.intolerable) })}
                  </span>
                </th>
                {horizons.map((h) => {
                  const v = d.impacts[c.key]?.[h.key];
                  const hot = v != null && v >= c.intolerable;
                  const first = proposal.category === c.key && proposal.horizon === h.key;
                  const label = c.levels.find((l) => l.value === v)?.label;
                  return (
                    <td key={h.key} className={`px-1.5 py-1 align-top ${hot ? "bg-sel" : ""} ${first ? "outline-2 -outline-offset-2 outline-danger" : ""}`}>
                      {edit ? (
                        <Select
                          className={`field w-full min-w-[7.5rem] py-1 ${hot ? "font-semibold" : ""}`}
                          aria-label={t("continuita.biaSection.cellLabel", { category: c.name, horizon: h.name })}
                          value={v == null ? "" : String(v)}
                          onChange={(e) => setLevel(c.key, h.key, e.target.value)}
                        >
                          <option value="">—</option>
                          {c.levels.map((l) => (
                            <option key={l.value} value={String(l.value)}>
                              {`${l.value} · ${l.label}`}
                            </option>
                          ))}
                        </Select>
                      ) : (
                        <span className={hot ? "font-semibold text-danger" : ""}>{v == null ? "—" : `${v} · ${label ?? ""}`}</span>
                      )}
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* Proposta */}
        <div className="flex flex-col gap-2 border-l-4 border-hair pl-3">
          <span className="label">{t("continuita.biaSection.proposal")}</span>
          <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[13px]">
            <dt className="text-muted">{t("continuita.field.mtpd")}</dt>
            <dd className="m-0">
              <Minutes m={proposal.mtpd_minutes} />
            </dd>
            <dt className="text-muted">{t("continuita.field.rto")}</dt>
            <dd className="m-0">
              <Minutes m={proposal.rto_minutes} />
            </dd>
            <dt className="text-muted">{t("continuita.field.critical")}</dt>
            <dd className="m-0">{proposal.critical ? t("continuita.common.yes") : t("continuita.common.no")}</dd>
          </dl>
          <p className="m-0 text-[12.5px] text-muted">
            {proposal.category
              ? t("continuita.biaSection.reason", { category: catName(proposal.category), horizon: horName(proposal.horizon) })
              : t("continuita.biaSection.reasonNone")}
            {" "}
            {proposal.critical
              ? t("continuita.biaSection.reasonCritical", { limit: fmtMinutes(cfg.critical_mtpd_minutes) })
              : t("continuita.biaSection.reasonNotCritical", { limit: fmtMinutes(cfg.critical_mtpd_minutes) })}
          </p>
          {edit && (
            <button
              type="button"
              className="btn self-start"
              onClick={() => setD((x) => ({ ...x, mtpd_minutes: proposal.mtpd_minutes, rto_minutes: proposal.rto_minutes, critical: proposal.critical }))}
            >
              {t("continuita.biaSection.useProposal")}
            </button>
          )}
        </div>

        {/* Valori scelti */}
        <div className="flex flex-col gap-3">
          <span className="label">{t("continuita.biaSection.chosen")}</span>
          <div className="flex flex-wrap gap-4">
            <DurationInput label={t("continuita.field.mtpd")} value={d.mtpd_minutes} disabled={!edit} onChange={(m) => setD((x) => ({ ...x, mtpd_minutes: m }))} />
            <DurationInput label={t("continuita.field.rto")} value={d.rto_minutes} disabled={!edit} onChange={(m) => setD((x) => ({ ...x, rto_minutes: m }))} />
            <DurationInput label={t("continuita.field.rpo")} value={d.rpo_minutes} disabled={!edit} onChange={(m) => setD((x) => ({ ...x, rpo_minutes: m }))} />
          </div>
          {d.mtpd_minutes != null && d.rto_minutes != null && d.rto_minutes > d.mtpd_minutes && (
            <p className="m-0 text-[12.5px] text-flag">{t("continuita.biaSection.rtoOverMtpd")}</p>
          )}
          <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
            <legend className="label mb-1">{t("continuita.biaSection.criticalQuestion")}</legend>
            <div className="flex flex-wrap gap-4 text-[13px]">
              {([
                [true, t("continuita.common.yes")],
                [false, t("continuita.common.no")],
                [null, t("continuita.biaSection.undecided")],
              ] as const).map(([val, label]) => (
                <label key={String(val)} className="flex min-h-[32px] items-center gap-1.5">
                  <input type="radio" name={`critica-${view.id}`} checked={d.critical === val} disabled={!edit} onChange={() => setD((x) => ({ ...x, critical: val }))} />
                  {label}
                </label>
              ))}
            </div>
            {d.critical !== null && d.critical !== proposal.critical && (
              <span className="text-[12px] text-flag">{t("continuita.biaSection.differsFromProposal")}</span>
            )}
          </fieldset>
          <label className="flex flex-col gap-1">
            <span className="label">{t("continuita.biaSection.criticalReason")}</span>
            <textarea className="field min-h-16" value={d.critical_reason} disabled={!edit} onChange={(e) => setD((x) => ({ ...x, critical_reason: e.target.value }))} />
          </label>
        </div>
      </div>

      <label className="flex flex-col gap-1">
        <span className="label">{t("continuita.biaSection.notes")}</span>
        <textarea className="field min-h-20" value={d.notes} disabled={!edit} onChange={(e) => setD((x) => ({ ...x, notes: e.target.value }))} />
      </label>

      {(edit || view.can.approve) && (
        <div className="flex flex-col gap-2">
          {!complete && <p className="m-0 text-[12.5px] text-muted">{t("continuita.biaSection.incomplete")}</p>}
          <div className="flex flex-wrap gap-2">
            {edit && (
              <button type="button" className="btn btn-primary min-h-[44px]" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
                {save.isPending ? t("continuita.common.saving") : t("continuita.common.save")}
              </button>
            )}
            {edit && dirty && (
              <button type="button" className="btn min-h-[44px]" onClick={() => setD(fromView(view))}>
                {t("continuita.common.discard")}
              </button>
            )}
            {view.can.approve && (
              <button
                type="button"
                className="btn min-h-[44px]"
                disabled={dirty || approve.isPending}
                title={dirty ? t("continuita.biaSection.saveFirst") : undefined}
                onClick={() => approve.mutate()}
              >
                {t("continuita.biaSection.approve")}
              </button>
            )}
          </div>
          <ErrorNote error={save.error ?? approve.error} />
        </div>
      )}

      <section className="flex flex-col gap-2" aria-label={t("continuita.biaSection.inForce")}>
        <SectionTitle>{t("continuita.biaSection.inForce")}</SectionTitle>
        {view.approved ? (
          <p className="m-0 text-[13px]">
            {t("continuita.biaSection.approvedLine", {
              v: view.approved.version,
              date: date(view.approved.at),
              by: view.approved.by ?? "—",
              mtpd: fmtMinutes(view.approved.mtpd_minutes),
              rto: fmtMinutes(view.approved.rto_minutes),
              rpo: fmtMinutes(view.approved.rpo_minutes),
            })}
            {view.approved.critical && <span className="ml-2 border border-flag px-1 text-[10.5px] font-semibold text-flag">{t("continuita.bia.critical")}</span>}
          </p>
        ) : (
          <p className="m-0 text-[13px] text-muted">{t("continuita.biaSection.neverApproved")}</p>
        )}
        {view.versions.length > 0 && (
          <ul className="m-0 flex list-none flex-col p-0">
            {view.versions.map((v) => (
              <li key={v.version} className="flex flex-wrap items-baseline gap-x-3 border-b border-hair py-1.5 text-[12.5px]">
                <span className="font-mono font-semibold tabular">v{v.version}</span>
                <span className="text-muted">
                  {date(v.at)} · {v.by ?? "—"}
                </span>
                <span className="tabular">
                  {t("continuita.biaSection.versionValues", { mtpd: fmtMinutes(v.mtpd_minutes), rto: fmtMinutes(v.rto_minutes), rpo: fmtMinutes(v.rpo_minutes) })}
                </span>
                {v.critical && <span className="text-flag">{t("continuita.bia.critical")}</span>}
                <span className="text-[11.5px] text-muted">{t("continuita.biaSection.settingsVersion", { v: v.settings_version })}</span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="flex flex-col gap-2" aria-label={t("continuita.biaSection.applications")}>
        <SectionTitle>{t("continuita.biaSection.applications")}</SectionTitle>
        {view.applications.length === 0 && <p className="m-0 text-[13px] text-muted">{t("continuita.biaSection.noApplications")}</p>}
        <ul className="m-0 flex list-none flex-col p-0">
          {view.applications.map((a) => (
            <li key={a.id} className="flex flex-wrap items-baseline gap-x-4 gap-y-0.5 border-b border-hair py-1.5 text-[12.5px]">
              <Link to="/e/$key" params={{ key: a.key }} className="min-w-0 flex-[1_1_200px] font-medium text-fg no-underline hover:underline">
                {a.title}
              </Link>
              <span className="flex items-baseline gap-1.5">
                <span className="text-muted">{t("continuita.field.rto")}</span>
                <Minutes m={a.rto_minutes} />
                <OkMark ok={a.rto_ok} />
              </span>
              <span className="flex items-baseline gap-1.5">
                <span className="text-muted">{t("continuita.field.rpo")}</span>
                <Minutes m={a.rpo_minutes} />
                <OkMark ok={a.rpo_ok} />
              </span>
            </li>
          ))}
        </ul>
      </section>
    </section>
  );
}
