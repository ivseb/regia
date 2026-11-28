// Architettura → Continuità (docs/sviluppo/continuita.md, «Interfaccia»): riepilogo e il «cosa
// manca» con le incoerenze raggruppate per gravità, ognuna con la frase, l'azione diretta e il
// lavoro da creare nel team. Filtri per capability, team e tipo nella query string.
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { EntitySelect, Select } from "../../select";
import { Loading, SectionTitle } from "../../ui";
import { useT } from "../../i18n/react";
import { EaPage } from "../ea/tabs";
import { ISSUE_CODES, continuityApi, groupBySeverity, type Summary } from "../../continuityApi";
import { ContinuityError, ContinuityNav, IssueRow, NotReady, SeverityLabel } from "./ui";

export type ContinuitySearch = { capability?: string; team?: string; tipo?: string };

export function ContinuityPage({ search }: { search: ContinuitySearch }) {
  const t = useT();
  const nav = useNavigate();
  const ov = useQuery({
    queryKey: ["continuity", "overview", search.capability ?? "", search.team ?? "", search.tipo ?? ""],
    queryFn: () => continuityApi.overview({ capability: search.capability, team: search.team, code: search.tipo }),
  });
  const set = (patch: Partial<ContinuitySearch>) =>
    nav({ to: "/architettura/continuita", search: (s: ContinuitySearch) => ({ ...s, ...patch }) as ContinuitySearch, replace: true });
  const d = ov.data;
  const filtered = !!(search.capability || search.team || search.tipo);
  return (
    <EaPage active="continuita" title={t("continuita.title")}>
      <ContinuityNav active="panoramica" />
      <p className="m-0 max-w-3xl text-muted">{t("continuita.intro")}</p>
      {ov.isPending && <Loading />}
      <ContinuityError error={ov.error} />
      {d && !d.ready && <NotReady can={d.can} />}
      {d?.ready && (
        <>
          <SummaryBlock s={d.summary} />
          <section className="flex flex-col gap-3" aria-label={t("continuita.missing.title")}>
            <SectionTitle aside={<span className="text-[12px] text-muted tabular">{t("continuita.missing.count", { count: d.issues.length })}</span>}>
              {t("continuita.missing.title")}
            </SectionTitle>
            <div className="flex flex-wrap items-end gap-3">
              <label className="flex w-full flex-col gap-1 sm:w-56">
                <span className="label">{t("continuita.filter.capability")}</span>
                <EntitySelect
                  kind="element"
                  types={["capability"]}
                  className="field"
                  noneLabel={t("continuita.filter.all")}
                  value={search.capability ?? ""}
                  onChange={(v: string) => set({ capability: v || undefined })}
                  aria-label={t("continuita.filter.capability")}
                />
              </label>
              <label className="flex w-full flex-col gap-1 sm:w-48">
                <span className="label">{t("continuita.filter.team")}</span>
                <EntitySelect
                  kind="team"
                  className="field"
                  noneLabel={t("continuita.filter.all")}
                  value={search.team ?? ""}
                  onChange={(v: string) => set({ team: v || undefined })}
                  aria-label={t("continuita.filter.team")}
                />
              </label>
              <label className="flex w-full flex-col gap-1 sm:w-64">
                <span className="label">{t("continuita.filter.code")}</span>
                <Select className="field" value={search.tipo ?? ""} onChange={(e) => set({ tipo: e.target.value || undefined })}>
                  <option value="">{t("continuita.filter.all")}</option>
                  {ISSUE_CODES.map((c) => (
                    <option key={c} value={c}>
                      {t(`continuita.code.${c}`)}
                    </option>
                  ))}
                </Select>
              </label>
              {filtered && (
                <button type="button" className="btn" onClick={() => nav({ to: "/architettura/continuita", search: {}, replace: true })}>
                  {t("continuita.filter.clear")}
                </button>
              )}
            </div>
            {d.issues.length === 0 && <p className="m-0 text-muted">{filtered ? t("continuita.missing.noneFiltered") : t("continuita.missing.none")}</p>}
            {groupBySeverity(d.issues).map((g) => (
              <div key={g.severity} className="flex flex-col">
                <h4 className="label m-0 mt-2 flex items-baseline gap-2">
                  <SeverityLabel severity={g.severity} />
                  <span className="font-mono text-muted tabular">{g.items.length}</span>
                </h4>
                <ul className="m-0 flex list-none flex-col p-0">
                  {g.items.map((i) => (
                    <IssueRow key={i.ref} issue={i} />
                  ))}
                </ul>
              </div>
            ))}
          </section>
        </>
      )}
    </EaPage>
  );
}

function SummaryBlock({ s }: { s: Summary }) {
  const t = useT();
  const cells: { label: string; value: number; warn: boolean }[] = [
    { label: t("continuita.summary.issues"), value: s.issues, warn: s.high > 0 },
    { label: t("continuita.summary.high"), value: s.high, warn: s.high > 0 },
    { label: t("continuita.summary.criticalFunctions"), value: s.critical_functions, warn: false },
    { label: t("continuita.summary.biaApproved"), value: s.bia_approved, warn: false },
    { label: t("continuita.summary.biaMissing"), value: s.bia_missing, warn: s.bia_missing > 0 },
    { label: t("continuita.summary.testsDue"), value: s.tests_due, warn: s.tests_due > 0 },
    { label: t("continuita.summary.plansMissing"), value: s.plans_missing, warn: s.plans_missing > 0 },
  ];
  return (
    <dl className="m-0 grid grid-cols-2 gap-x-6 gap-y-3 border-y-2 border-rule py-4 sm:grid-cols-4 lg:grid-cols-7">
      {cells.map((c) => (
        <div key={c.label} className={`flex flex-col border-l-4 pl-3 ${c.warn ? "border-flag" : "border-hair"}`}>
          <dt className="label">{c.label}</dt>
          <dd className="m-0 font-mono text-[26px] leading-none font-bold tabular">{c.value}</dd>
        </div>
      ))}
    </dl>
  );
}
