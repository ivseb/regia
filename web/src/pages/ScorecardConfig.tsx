import { Select } from "../select";
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { portfolioApi, type ScorecardDefinition } from "../api";
import { ScorecardEditor } from "./configEditors/ScorecardEditor";
import { ErrorNote, ROLE_NAMES, SectionTitle, date } from "../ui";
import { useT } from "../i18n/react";

// Configurazione della scorecard (versionata come i processi) e della graduatoria.
// La definizione si modifica con l'editor visuale (configEditors/ScorecardEditor).
// Ogni pubblicazione crea una nuova versione; i punteggi già dati restano sulla loro.

export function ScorecardConfig({ scorecardKey }: { scorecardKey: string }) {
  const t = useT();
  const qc = useQueryClient();
  const sc = useQuery({ queryKey: ["scorecard", scorecardKey], queryFn: () => portfolioApi.scorecard(scorecardKey) });
  const refresh = (data: unknown) => {
    qc.setQueryData(["scorecard", scorecardKey], data);
    qc.invalidateQueries({ queryKey: ["ranking"] });
    qc.invalidateQueries({ queryKey: ["evaluation"] });
  };
  if (sc.error) return <div className="px-4 py-5 md:px-7"><ErrorNote error={sc.error} /></div>;
  if (!sc.data) return null;
  const cur = sc.data.current;

  return (
    <div className="flex flex-col gap-6 px-4 py-5 md:px-7">
      {cur?.definition && (
        <section className="flex flex-col gap-2">
          <SectionTitle aside={<span className="font-mono text-[12px] text-muted">{t("configurazione.scorecard.published", { version: cur.version, date: date(cur.published_at) })}</span>}>
            {cur.definition.name}
          </SectionTitle>
          {cur.definition.description && <p className="m-0 max-w-3xl text-muted">{cur.definition.description}</p>}
          <p className="m-0 text-[12.5px]">
            {t("configurazione.scorecard.scorers", { roles: cur.definition.scorer_roles.map((r) => ROLE_NAMES[r] ?? r).join(", ") })}
          </p>
          <CriteriaTable def={cur.definition} />
        </section>
      )}

      <section className="flex flex-col gap-2">
        <SectionTitle>{t("configurazione.common.versioni")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {sc.data.versions.map((v) => (
            <li key={v.id} className="grid grid-cols-[48px_110px_minmax(0,1fr)_auto] items-baseline gap-2 border-b border-hair py-1.5 text-[12.5px]">
              <span className="font-mono font-semibold">v{v.version}</span>
              <span className={v.status === "published" ? "font-semibold text-accent" : "text-muted"}>
                {t(`configurazione.qEditor.status.${v.status}`)}
              </span>
              <span className="truncate text-muted">{v.notes || "—"}</span>
              <span className="font-mono text-muted tabular">{t("configurazione.scorecard.scoreCount", { count: v.results })}</span>
            </li>
          ))}
        </ul>
      </section>

      {sc.data.can_edit && <ScorecardEditor sc={sc.data} scorecardKey={scorecardKey} onSaved={refresh} />}
      <SettingsForm />
    </div>
  );
}

function CriteriaTable({ def }: { def: ScorecardDefinition }) {
  const t = useT();
  const tot = def.criteria.reduce((s, c) => s + c.weight, 0);
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[560px] border-collapse text-[12.5px] tabular">
        <thead>
          <tr className="label h-8 text-left">
            <th className="border-b-2 border-rule pr-2">{t("configurazione.scorecard.criterio")}</th>
            <th className="border-b-2 border-rule px-2 text-right">{t("configurazione.common.peso")}</th>
            <th className="border-b-2 border-rule px-2">{t("configurazione.scorecard.scala")}</th>
            <th className="border-b-2 border-rule pl-2">{t("configurazione.scorecard.valoreProposto")}</th>
          </tr>
        </thead>
        <tbody>
          {def.criteria.map((c) => (
            <tr key={c.key} className="border-b border-hair align-top">
              <td className="py-1.5 pr-2">
                <span className="font-semibold">{c.name}</span>
                {c.direction === "lower_better" && <span className="ml-1.5 text-muted">{t("configurazione.scorecard.piuBassoEMeglio")}</span>}
                {c.description && <span className="block text-muted">{c.description}</span>}
              </td>
              <td className="px-2 py-1.5 text-right font-mono whitespace-nowrap">
                {Math.round((100 * c.weight) / tot)}%<span className="block text-muted">{c.weight}</span>
              </td>
              <td className="px-2 py-1.5">
                {c.scale.map((l) => (
                  <span key={l.value} className="block whitespace-nowrap">
                    <span className="font-mono">{l.value}</span> {l.label}
                  </span>
                ))}
              </td>
              <td className="py-1.5 pl-2">
                {(c.suggest ?? []).map((s, i) => (
                  <span key={i} className="block">
                    <span className="font-mono">{s.when}</span> → <span className="font-mono font-semibold">{s.value}</span>
                  </span>
                ))}
                {c.suggest_fact && <span className="block font-mono">{c.suggest_fact}</span>}
                {!c.suggest?.length && !c.suggest_fact && <span className="text-muted">—</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SettingsForm() {
  const t = useT();
  const qc = useQueryClient();
  const rk = useQuery({ queryKey: ["ranking"], queryFn: portfolioApi.ranking });
  const st = rk.data?.settings;
  const [mandatory, setMandatory] = useState(true);
  const [basis, setBasis] = useState<"low" | "mid" | "high">("high");
  const [phases, setPhases] = useState("");
  const [roles, setRoles] = useState("");
  useEffect(() => {
    if (!st) return;
    setMandatory(st.mandatory_first);
    setBasis(st.estimate_basis);
    setPhases(st.excluded_phases.join(", "));
    setRoles(st.rank_roles.join(", "));
  }, [st]);
  const save = useMutation({
    mutationFn: () =>
      portfolioApi.updateSettings({
        mandatory_first: mandatory,
        estimate_basis: basis,
        excluded_phases: phases.split(",").map((s) => s.trim()).filter(Boolean),
        rank_roles: roles.split(",").map((s) => s.trim()).filter(Boolean),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ranking"] }),
  });
  if (!rk.data || !st) return null;
  const can = rk.data.can_admin;
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle aside={st.updated_by && <span className="text-[12px] text-muted">{t("configurazione.scorecard.updatedBy", { name: st.updated_by })}</span>}>
        {t("configurazione.scorecard.impostazioniDellaGraduatoria")}
      </SectionTitle>
      <form
        className="grid max-w-3xl grid-cols-1 gap-3 md:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <label className="flex items-center gap-2">
          <input type="checkbox" disabled={!can} checked={mandatory} onChange={(e) => setMandatory(e.target.checked)} />
          {t("configurazione.scorecard.obblighiNormativiInTesta")}
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.scorecard.stimaCheSiSomma")}</span>
          <Select className="field" disabled={!can} value={basis} onChange={(e) => setBasis(e.target.value as typeof basis)}>
            <option value="high">{t("configurazione.scorecard.massimaPrudente")}</option>
            <option value="mid">{t("configurazione.scorecard.media")}</option>
            <option value="low">{t("configurazione.scorecard.minima")}</option>
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.scorecard.fasiEscluse")}</span>
          <input className="field" disabled={!can} value={phases} onChange={(e) => setPhases(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.scorecard.ruoliCheForzanoL")}</span>
          <input className="field font-mono" disabled={!can} value={roles} onChange={(e) => setRoles(e.target.value)} />
        </label>
        {can && (
          <div className="md:col-span-2">
            <button className="btn btn-primary" disabled={save.isPending}>
              {t("configurazione.common.salvaLeImpostazioni")}
            </button>
          </div>
        )}
        <div className="md:col-span-2">
          <ErrorNote error={save.error} />
        </div>
      </form>
    </section>
  );
}
