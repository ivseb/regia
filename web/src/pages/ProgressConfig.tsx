// Configurazione → Avanzamento (piano, passi 13 e 14): periodicità e promemoria degli
// status report, indicatori normativi del cruscotto, portale del richiedente (benvenuto,
// tipi esposti, campi mostrati, fasi spiegate in parole semplici).
import { Select } from "../select";
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { FREQUENCIES, reportApi, type Frequency, type Indicator, type PortalSettings, type PortalType, type ReportSettings } from "../reportApi";
import { PageHeader, useMe } from "../shell";
import { ErrorNote, SectionTitle, mainPath, Loading } from "../ui";
import { useT } from "../i18n/react";
import { tr } from "../i18n/core";

export function ProgressConfig() {
  const t = useT();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const has = (p: string) => perms.includes("*") || perms.includes(p);
  const canPortal = has("tenant.admin") || has("portfolio.admin");
  return (
    <>
      <PageHeader path={t("configurazione.path.progress")} title={t("configurazione.progress.statusReportEPortale")} />
      <div className="flex max-w-4xl flex-col gap-10 px-4 pb-12 md:px-7">
        <ReportSettingsForm />
        {canPortal && <PortalSettingsForm />}
      </div>
    </>
  );
}

function ReportSettingsForm() {
  const t = useT();
  const qc = useQueryClient();
  const st = useQuery({ queryKey: ["report-settings"], queryFn: reportApi.settings });
  const type = useQuery({ queryKey: ["type", "business_request"], queryFn: () => api.type("business_request"), staleTime: 300_000 });
  const [s, setS] = useState<ReportSettings | null>(null);
  useEffect(() => setS(st.data ?? null), [st.data]);
  const save = useMutation({
    mutationFn: () => reportApi.saveSettings({ frequency: s!.frequency, reminder_days: s!.reminder_days, phases: s!.phases, regulatory: s!.regulatory }),
    onSuccess: (x) => {
      qc.setQueryData(["report-settings"], x);
      qc.invalidateQueries({ queryKey: ["dashboard"] });
    },
  });
  if (st.error) return <ErrorNote error={st.error} />;
  if (!s) return <Loading className="m-0 text-muted" />;
  const ro = !s.can_edit;
  const phases = type.data ? mainPath(type.data.states).map((p) => p.name) : [];
  const setInd = (i: number, patch: Partial<Indicator>) => setS({ ...s, regulatory: s.regulatory.map((r, j) => (j === i ? { ...r, ...patch } : r)) });
  return (
    <section className="flex flex-col gap-4" aria-label={t("configurazione.progress.statusReport")}>
      <SectionTitle>{t("configurazione.progress.statusReport")}</SectionTitle>
      <div className="flex flex-wrap gap-6">
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.progress.periodicitaDiDefault")}</span>
          <Select className="field" disabled={ro} value={s.frequency} onChange={(e) => setS({ ...s, frequency: e.target.value as Frequency })}>
            {(Object.keys(FREQUENCIES) as Frequency[]).map((f) => <option key={f} value={f}>{FREQUENCIES[f]}</option>)}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.progress.promemoriaAlPm")}</span>
          <span className="flex items-center gap-2">
            <input type="number" min={0} max={30} className="field w-20" disabled={ro} value={s.reminder_days}
              onChange={(e) => setS({ ...s, reminder_days: Number(e.target.value) })} />
            <span className="text-muted">{t("configurazione.progress.giorniPrimaDellaScadenza")}</span>
          </span>
        </label>
      </div>
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("configurazione.progress.fasiInCuiIl")}</legend>
        <div className="flex flex-wrap gap-x-5 gap-y-2">
          {phases.map((p) => (
            <label key={p} className="flex items-center gap-1.5">
              <input type="checkbox" className="size-4 accent-[var(--accent)]" disabled={ro} checked={s.phases.includes(p)}
                onChange={(e) => setS({ ...s, phases: e.target.checked ? [...s.phases, p] : s.phases.filter((x) => x !== p) })} />
              {p}
            </label>
          ))}
        </div>
        <span className="text-[12px] text-muted">{t("configurazione.progress.ilPmOSe")}</span>
      </fieldset>
      <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("configurazione.progress.indicatoriNormativiDelCruscott")}</legend>
        <span className="text-[12px] text-muted">{t("configurazione.progress.unEtichettaEUna")}</span>
        {s.regulatory.map((r, i) => (
          <div key={i} className="grid grid-cols-[120px_minmax(0,1fr)_auto] items-center gap-2">
            <input className="field" aria-label={t("configurazione.progress.etichetta")} disabled={ro} maxLength={40} value={r.label} onChange={(e) => setInd(i, { label: e.target.value })} />
            <input className="field font-mono text-[12.5px]" aria-label={t("configurazione.progress.query")} disabled={ro} value={r.query} onChange={(e) => setInd(i, { query: e.target.value })} />
            {!ro && (
              <button className="text-[12px] text-muted underline hover:text-danger" onClick={() => setS({ ...s, regulatory: s.regulatory.filter((_, j) => j !== i) })}>
                {t("configurazione.common.togli")}
              </button>
            )}
          </div>
        ))}
        {!ro && s.regulatory.length < 8 && (
          <button className="btn self-start" onClick={() => setS({ ...s, regulatory: [...s.regulatory, { label: "", query: "" }] })}>
            {t("configurazione.progress.aggiungiUnIndicatore")}
          </button>
        )}
      </fieldset>
      {!ro && (
        <div className="flex items-center gap-2">
          <button className="btn btn-primary" disabled={save.isPending} onClick={() => save.mutate()}>{t("configurazione.common.salva")}</button>
          {save.isSuccess && <span className="text-[12px] text-muted">{t("configurazione.common.salvato")}</span>}
        </div>
      )}
      {ro && <p className="m-0 text-[12px] text-muted">{t("configurazione.progress.leModificaIlGoverno")}</p>}
      <ErrorNote error={save.error} />
    </section>
  );
}

function PortalSettingsForm() {
  const t = useT();
  const qc = useQueryClient();
  const st = useQuery({ queryKey: ["portal-settings"], queryFn: reportApi.portalSettings });
  const [s, setS] = useState<PortalSettings | null>(null);
  useEffect(() => setS(st.data ?? null), [st.data]);
  const save = useMutation({
    mutationFn: () => reportApi.savePortalSettings({ welcome: s!.welcome, types: s!.types, phase_texts: s!.phase_texts }),
    onSuccess: (x) => {
      qc.setQueryData(["portal-settings"], x);
      qc.invalidateQueries({ queryKey: ["portal"] });
    },
  });
  if (st.error) return <ErrorNote error={st.error} />;
  if (!s) return <Loading className="m-0 text-muted" />;
  const exposed = (k: string) => s.types.find((t) => t.type === k);
  const setType = (k: string, patch: Partial<PortalType>) => setS({ ...s, types: s.types.map((t) => (t.type === k ? { ...t, ...patch } : t)) });
  const toggle = (list: string[], v: string, on: boolean) => (on ? [...list, v] : list.filter((x) => x !== v));
  return (
    <section className="flex flex-col gap-5" aria-label={t("configurazione.progress.portaleDelRichiedente")}>
      <SectionTitle>{t("configurazione.progress.portaleDelRichiedente")}</SectionTitle>
      <label className="flex flex-col gap-1.5">
        <span className="label">{t("configurazione.progress.testoDiBenvenuto")}</span>
        <textarea className="field min-h-24" maxLength={4000} value={s.welcome} onChange={(e) => setS({ ...s, welcome: e.target.value })}
          placeholder={t("configurazione.progress.perEsempioChiSiamo")} />
        <span className="text-[12px] text-muted">{t("configurazione.progress.markdownGrassettoElenchiCon")}</span>
      </label>
      <div className="flex flex-col gap-3">
        <span className="label">{t("configurazione.progress.tipiDiRichiestaEsposti")}</span>
        {(s.available ?? []).map((a) => {
          const t = exposed(a.key);
          return (
            <div key={a.key} className="flex flex-col gap-2 border-b border-hair pb-3">
              <label className="flex items-center gap-2 font-semibold">
                <input type="checkbox" className="size-4 accent-[var(--accent)]" checked={!!t}
                  onChange={(e) =>
                    setS({
                      ...s,
                      types: e.target.checked
                        ? [...s.types, { type: a.key, label: a.name, description: "", fields: a.fields.map((f) => f.key), standard: a.key === "business_request" ? (s.standard_fields ?? []).map((f) => f.key) : [] }]
                        : s.types.filter((x) => x.type !== a.key),
                    })
                  } />
                {a.name}
              </label>
              {t && (
                <div className="flex flex-col gap-2 pl-6">
                  <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                    <input className="field" aria-label={tr("configurazione.progress.nomeNelPortale")} maxLength={80} value={t.label} onChange={(e) => setType(a.key, { label: e.target.value })} />
                    <input className="field" aria-label={tr("configurazione.progress.descrizioneNelPortale")} maxLength={400} placeholder={tr("configurazione.progress.descrizionePerIlRichiedente")} value={t.description}
                      onChange={(e) => setType(a.key, { description: e.target.value })} />
                  </div>
                  {a.key === "business_request" && (
                    <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-[13px]">
                      <span className="text-muted">{tr("configurazione.progress.datiDellaDomanda")}</span>
                      {(s.standard_fields ?? []).map((f) => (
                        <label key={f.key} className="flex items-center gap-1.5">
                          <input type="checkbox" className="size-4 accent-[var(--accent)]" checked={t.standard.includes(f.key)}
                            onChange={(e) => setType(a.key, { standard: toggle(t.standard, f.key, e.target.checked) })} />
                          {f.name}
                        </label>
                      ))}
                    </div>
                  )}
                  <div className="flex flex-wrap gap-x-4 gap-y-1.5 text-[13px]">
                    <span className="text-muted">{tr("configurazione.progress.campi")}</span>
                    {a.fields.length === 0 && <span className="text-muted">{tr("configurazione.common.nessuno")}</span>}
                    {a.fields.map((f) => (
                      <label key={f.key} className="flex items-center gap-1.5">
                        <input type="checkbox" className="size-4 accent-[var(--accent)]" checked={t.fields.includes(f.key)}
                          onChange={(e) => setType(a.key, { fields: toggle(t.fields, f.key, e.target.checked) })} />
                        {f.name}
                      </label>
                    ))}
                  </div>
                </div>
              )}
            </div>
          );
        })}
      </div>
      <div className="flex flex-col gap-2">
        <span className="label">{t("configurazione.progress.leFasiInParole")}</span>
        <span className="text-[12px] text-muted">{t("configurazione.progress.vuotoIlTestoProposto")}</span>
        {(s.phases ?? []).map((p) => (
          <label key={p.name} className="grid grid-cols-1 gap-1 sm:grid-cols-[150px_minmax(0,1fr)] sm:items-center sm:gap-3">
            <span className="font-semibold">{p.name}</span>
            <input className="field" maxLength={500} placeholder={p.default || p.description} value={s.phase_texts[p.name] ?? ""}
              onChange={(e) => setS({ ...s, phase_texts: { ...s.phase_texts, [p.name]: e.target.value } })} />
          </label>
        ))}
      </div>
      <div className="flex items-center gap-2">
        <button className="btn btn-primary" disabled={save.isPending} onClick={() => save.mutate()}>{t("configurazione.progress.salvaIlPortale")}</button>
        {save.isSuccess && <span className="text-[12px] text-muted">{t("configurazione.common.salvato")}</span>}
      </div>
      <ErrorNote error={save.error} />
    </section>
  );
}
