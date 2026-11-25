// Configurazione → Requisiti (piano, passi 5 e 6): cataloghi versionati con le baseline e
// l'anteprima delle loro condizioni sulle applicazioni; livelli di rischio residuo e
// politica di approvazione delle deroghe a soglie. Il catalogo si modifica con l'editor
// visuale (configEditors/CatalogEditor), con i problemi controllati dal server mentre si scrive;
// poi bozza e pubblicazione.
import { Select } from "../select";
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  requirementsApi,
  type Baseline,
  type CatalogDefinition,
  type PolicyRule,
  type PreviewItem,
  type RequirementConfig,
  type RequirementSettings,
  type RiskLevel,
} from "../requirementsApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, date } from "../ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { tr } from "../i18n/core";
import { ConditionBuilder } from "./processEditor/ConditionBuilder";
import { conditionError } from "./processEditor/model";
import { useDebounced, type CondCount } from "../casaApi";
import { CatalogEditor } from "./configEditors/CatalogEditor";

export type RequirementsConfigTab = "cataloghi" | "deroghe";

export function RequirementsConfigPage({ tab, catalog }: { tab: RequirementsConfigTab; catalog?: string }) {
  const t = useT();
  const cfg = useQuery({ queryKey: ["requirement-config"], queryFn: requirementsApi.config });
  const tabs: [RequirementsConfigTab, string][] = [
    ["cataloghi", t("configurazione.requirements.tabCatalogs")],
    ["deroghe", t("configurazione.requirements.tabWaivers")],
  ];
  return (
    <>
      <PageHeader
        path={t("configurazione.path.requirements")}
        title={t("configurazione.requirements.requisiti")}
        actions={
          <a className="btn no-underline" href={requirementsApi.exportUrl()} download="requisiti.json">
            {t("configurazione.common.esportaJson")}
          </a>
        }
      />
      <div role="tablist" aria-label={t("configurazione.common.sezioni")} className="flex gap-6 overflow-x-auto border-b border-hair px-4 md:px-7">
        {tabs.map(([k, l]) => (
          <Link
            key={k}
            to="/configurazione/requisiti"
            search={{ tab: k }}
            role="tab"
            aria-selected={tab === k}
            className={`shrink-0 pb-2.5 no-underline ${tab === k ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
          >
            {l}
          </Link>
        ))}
      </div>
      <div className="px-4 py-5 md:px-7">
        <ErrorNote error={cfg.error} />
        {cfg.data && tab === "cataloghi" && <Catalogs cfg={cfg.data} selected={catalog} />}
        {cfg.data && tab === "deroghe" && <Exceptions cfg={cfg.data} />}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------
// Cataloghi
// ---------------------------------------------------------------------

function Catalogs({ cfg, selected }: { cfg: RequirementConfig; selected?: string }) {
  const t = useT();
  const [newKey, setNewKey] = useState("");
  const key = selected ?? cfg.catalogs[0]?.key;
  return (
    <div className="flex flex-col gap-5">
      <p className="m-0 max-w-3xl text-[13px] text-muted">
        <Trans k="configurazione.requirements.catalogIntro" slots={{ example: <span className="font-mono">{t("configurazione.requirements.criticitaAltaCritica")}</span> }} />
      </p>
      <div className="flex flex-wrap items-end gap-3 border-b border-hair pb-4">
        {cfg.catalogs.map((c) => (
          <Link
            key={c.key}
            to="/configurazione/requisiti"
            search={{ tab: "cataloghi", catalogo: c.key }}
            className={`flex flex-col border px-3 py-2 no-underline ${c.key === key ? "border-accent bg-sel text-fg" : "border-hair text-fg hover:border-rule"}`}
          >
            <span className="font-semibold">{c.name}</span>
            <span className="text-[12px] text-muted tabular">
              {t("configurazione.requirements.catalogMeta", { version: c.version ? `v${c.version}` : t("configurazione.requirements.maiPubblicato"), items: c.items, baselines: c.baselines.length, draft: c.has_draft ? t("configurazione.requirements.bozza") : "" })}
            </span>
          </Link>
        ))}
        {cfg.can_edit && (
          <form
            className="flex items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              window.location.assign(`/configurazione/requisiti?tab=cataloghi&catalogo=${encodeURIComponent(newKey)}`);
            }}
          >
            <label className="flex flex-col gap-1">
              <span className="label">{t("configurazione.requirements.nuovoCatalogoChiave")}</span>
              <input className="field font-mono" pattern="[a-z][a-z0-9_]{0,62}" placeholder="cloud_pubblico" value={newKey} onChange={(e) => setNewKey(e.target.value)} />
            </label>
            <button className="btn" disabled={!newKey}>
              {t("configurazione.common.crea")}
            </button>
          </form>
        )}
      </div>
      {key && <CatalogDetail key={key} catalogKey={key} cfg={cfg} />}
    </div>
  );
}

const emptyCatalog = (): CatalogDefinition => ({
  name: tr("configurazione.requirements.newCatalog"),
  description: "",
  items: [{ key: "REQ-01", title: tr("configurazione.requirements.requirementTitle"), category: "", verification: { method: "manual", team: "sicurezza", criterion: "" } }],
  baselines: [{ key: "base", name: tr("configurazione.requirements.baseBaseline"), scope: "application", when: "", items: ["REQ-01"] }],
});

function CatalogDetail({ catalogKey, cfg }: { catalogKey: string; cfg: RequirementConfig }) {
  const t = useT();
  const qc = useQueryClient();
  const known = cfg.catalogs.some((c) => c.key === catalogKey);
  const cat = useQuery({ queryKey: ["requirement-config", "catalog", catalogKey], queryFn: () => requirementsApi.catalog(catalogKey), enabled: known, retry: false });
  const c = cat.data;
  const base = c?.draft?.definition ?? c?.current?.definition ?? emptyCatalog();
  // Chi può modificare vede le voci e le baseline nell'editor; gli altri, la versione pubblicata.
  const shown = cfg.can_edit ? null : (c?.current?.definition ?? (known ? null : emptyCatalog()));
  const initial = base;
  return (
    <div className="grid grid-cols-1 gap-6">
      <div className="flex min-w-0 flex-col gap-5">
        <ErrorNote error={cat.error} />
        {c?.current && (
          <p className="m-0 text-[12.5px] text-muted">
            <Trans
              k="configurazione.requirements.currentVersion"
              slots={{
                version: <span className="font-mono tabular">v{c.current.version}</span>,
                date: date(c.current.published_at),
                by: c.current.published_by ? t("configurazione.requirements.publishedBy", { name: c.current.published_by }) : "",
                count: c.requirements,
              }}
            />
          </p>
        )}
        {shown && <Baselines def={shown} />}
        {shown && <Items def={shown} cfg={cfg} />}
        {c && c.versions.length > 0 && (
          <section className="flex flex-col gap-2">
            <SectionTitle>{t("configurazione.common.versioni")}</SectionTitle>
            <ul className="m-0 list-none p-0 text-[13px]">
              {c.versions.map((v) => (
                <li key={v.id} className="flex justify-between gap-3 border-b border-hair py-1.5">
                  <span>
                    <span className="font-mono tabular">v{v.version}</span> {v.notes && <span className="text-muted">· {v.notes}</span>}
                  </span>
                  <span className="text-muted">{v.status === "published" ? t("configurazione.processes.status.current") : t("configurazione.processes.status.previous")} · {date(v.published_at)}</span>
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
      {cfg.can_edit && (cat.data || !known) && (
        <CatalogEditor
          key={c?.draft?.id ?? c?.current?.id ?? "nuovo"}
          catalogKey={catalogKey}
          cfg={cfg}
          c={c}
          initial={initial}
          onChanged={() => qc.invalidateQueries({ queryKey: ["requirement-config"] })}
        />
      )}
    </div>
  );
}

function Baselines({ def }: { def: CatalogDefinition }) {
  const t = useT();
  const [open, setOpen] = useState<string | null>(null);
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{t("configurazione.requirements.baseline")}</SectionTitle>
      <ul className="m-0 list-none p-0">
        {def.baselines.map((b) => (
          <li key={b.key} className="flex flex-col gap-1 border-b border-hair py-2">
            <span className="flex flex-wrap items-baseline justify-between gap-x-3">
              <span>
                <span className="font-semibold">{b.name}</span> <span className="text-[12.5px] text-muted">· {b.scope === "project" ? t("configurazione.requirements.project") : t("configurazione.requirements.perApplicazione")}</span>
              </span>
              <button className="text-[12px] text-accent underline" onClick={() => setOpen(open === b.key ? null : b.key)}>
                {open === b.key ? t("configurazione.requirements.chiudiLAnteprima") : t("configurazione.requirements.anteprimaDellaCondizione")}
              </button>
            </span>
            <span className="text-[12.5px]">
              <Trans k="configurazione.requirements.whenLabel" slots={{ when: <span className="font-mono">{b.when || t("configurazione.processes.sempre")}</span> }} />
            </span>
            <span className="text-[12.5px] text-muted">{t("configurazione.requirements.itemsList", { list: b.items.join(", ") })}</span>
            {open === b.key && <ConditionPreview baseline={b} />}
          </li>
        ))}
      </ul>
    </section>
  );
}

function ConditionPreview({ baseline }: { baseline: Baseline }) {
  const t = useT();
  const [cond, setCond] = useState(baseline.when ?? "");
  const applied = useDebounced(cond);
  const type = baseline.scope === "project" ? "business_request" : "application";
  const noun = baseline.scope === "project" ? t("configurazione.requirements.richiesteAperte") : t("configurazione.requirements.applicazioniAttive");
  // Stessa anteprima del server per il conteggio del costruttore e per l'elenco sotto.
  const prev = useQuery({
    queryKey: ["requirement-config", "preview", applied, baseline.scope],
    queryFn: () => requirementsApi.preview(applied, baseline.scope),
    retry: false,
    enabled: conditionError(applied) === null,
  });
  const items: PreviewItem[] = prev.data?.items ?? [];
  const countItems = async (c: string): Promise<CondCount> => {
    const r = await requirementsApi.preview(c, baseline.scope);
    const hit = r.items.filter((i) => i.matched);
    return { ok: true, matched: hit.length, total: r.items.length, items: hit.slice(0, 5).map((i) => ({ key: i.code ?? "", title: i.title })) };
  };
  return (
    <div className="flex flex-col gap-2 border-l-2 border-accent py-1 pl-3">
      <ConditionBuilder label={t("configurazione.common.condizione")} value={cond} onChange={setCond} type={type} count={countItems} noun={noun} empty={t("casa.requirements.allApply")} />
      <ErrorNote error={prev.error} />
      {prev.data && (
        <ul className="m-0 max-h-64 list-none overflow-y-auto p-0 text-[12.5px]">
          {items.map((i) => (
            <li key={i.id} className="flex justify-between gap-3 border-b border-hair py-1">
              <span className={`min-w-0 truncate ${i.matched ? "" : "text-muted"}`}>
                <span className="mr-1.5 inline-block w-3">{i.matched ? "✓" : ""}</span>
                {i.code ? <span className="font-mono">{i.code} </span> : null}
                {i.title}
              </span>
              <span className="shrink-0 font-mono text-[11.5px] text-muted">
                {Object.entries(i.facts)
                  .map(([k, v]) => `${k}: ${v || "—"}`)
                  .join(" · ")}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function Items({ def, cfg }: { def: CatalogDefinition; cfg: RequirementConfig }) {
  const t = useT();
  const team = (k?: string) => cfg.teams.find((t) => t.key === k)?.name ?? k;
  const role = (k?: string) => cfg.roles.find((r) => r.key === k)?.name ?? k;
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{def.items.length}</span>}>{t("configurazione.requirements.voci")}</SectionTitle>
      <ul className="m-0 list-none p-0">
        {def.items.map((it) => (
          <li key={it.key} className="flex flex-col gap-0.5 border-b border-hair py-2 text-[13px]">
            <span>
              <span className="font-mono text-[12px] text-muted">{it.key}</span> <span className="font-semibold">{it.title}</span>
            </span>
            <span className="text-[12.5px] text-muted">
              {it.category || "—"} · {it.verification.method === "evidence" ? t("configurazione.requirements.fromEvidence", { check: it.verification.check ?? "" }) : t("configurazione.requirements.manualVerification")} ·{" "}
              {it.verification.team ? t("configurazione.requirements.verifiedByTeam", { name: team(it.verification.team) ?? "" }) : t("configurazione.requirements.verifiedByRole", { name: role(it.verification.role) ?? "" })}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

// ---------------------------------------------------------------------
// Deroghe: livelli di rischio e politica di approvazione
// ---------------------------------------------------------------------

function Exceptions({ cfg }: { cfg: RequirementConfig }) {
  return (
    <div className="grid grid-cols-1 gap-8 xl:grid-cols-2">
      <RiskLevels cfg={cfg} />
      <PolicyEditor cfg={cfg} />
    </div>
  );
}

function RiskLevels({ cfg }: { cfg: RequirementConfig }) {
  const t = useT();
  const qc = useQueryClient();
  const [s, setS] = useState<RequirementSettings>(cfg.settings);
  const save = useMutation({ mutationFn: () => requirementsApi.saveSettings(s), onSuccess: () => qc.invalidateQueries({ queryKey: ["requirement-config"] }) });
  const setLevel = (i: number, p: Partial<RiskLevel>) => setS({ ...s, risk_levels: s.risk_levels.map((l, j) => (j === i ? { ...l, ...p } : l)) });
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle>{t("configurazione.requirements.rischioResiduoEDurata")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{t("configurazione.requirements.iLivelliInOrdine")}</p>
      <table className="w-full border-collapse text-[13px]">
        <thead>
          <tr className="label text-left">
            <th className="border-b-2 border-rule py-1.5 pr-2">{t("configurazione.common.chiave")}</th>
            <th className="border-b-2 border-rule py-1.5 pr-2">{t("configurazione.common.nome")}</th>
            <th className="w-20 border-b-2 border-rule py-1.5 pr-2">{t("configurazione.common.punteggio")}</th>
            <th className="w-8 border-b-2 border-rule" />
          </tr>
        </thead>
        <tbody>
          {s.risk_levels.map((l, i) => (
            <tr key={i} className="border-b border-hair">
              <td className="py-1 pr-2">
                <input className="field w-full font-mono text-[12.5px]" aria-label={t("configurazione.common.chiave")} disabled={!cfg.can_edit} value={l.key} onChange={(e) => setLevel(i, { key: e.target.value })} />
              </td>
              <td className="py-1 pr-2">
                <input className="field w-full" aria-label={t("configurazione.common.nome")} disabled={!cfg.can_edit} value={l.name} onChange={(e) => setLevel(i, { name: e.target.value })} />
              </td>
              <td className="py-1 pr-2">
                <input className="field w-full tabular" aria-label={t("configurazione.common.punteggio")} type="number" min={0} max={100} disabled={!cfg.can_edit} value={l.score} onChange={(e) => setLevel(i, { score: Number(e.target.value) })} />
              </td>
              <td>
                {cfg.can_edit && (
                  <button className="text-[12px] text-muted underline" aria-label={t("configurazione.common.removeNamed", { name: l.name })} onClick={() => setS({ ...s, risk_levels: s.risk_levels.filter((_, j) => j !== i) })}>
                    ×
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {cfg.can_edit && (
        <button
          className="self-start text-[12px] text-accent underline"
          onClick={() => setS({ ...s, risk_levels: [...s.risk_levels, { key: "", name: "", score: (s.risk_levels.at(-1)?.score ?? 0) + 1 }] })}
        >
          {t("configurazione.requirements.aggiungiUnLivello")}
        </button>
      )}
      <div className="grid grid-cols-2 gap-3">
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.requirements.durataMassimaGiorni")}</span>
          <input className="field tabular" type="number" min={1} max={1095} disabled={!cfg.can_edit} value={s.max_exception_days} onChange={(e) => setS({ ...s, max_exception_days: Number(e.target.value) })} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.requirements.preavvisoDellaScadenzaGiorni")}</span>
          <input className="field tabular" type="number" min={0} max={120} disabled={!cfg.can_edit} value={s.expiry_notice_days} onChange={(e) => setS({ ...s, expiry_notice_days: Number(e.target.value) })} />
        </label>
      </div>
      {cfg.can_edit && (
        <button className="btn btn-primary self-start" disabled={save.isPending} onClick={() => save.mutate()}>
          {t("configurazione.common.salva")}
        </button>
      )}
      {save.isSuccess && <p className="m-0 text-[12.5px] text-ok">{t("configurazione.common.salvato")}</p>}
      <ErrorNote error={save.error} />
    </section>
  );
}

function PolicyEditor({ cfg }: { cfg: RequirementConfig }) {
  const t = useT();
  const qc = useQueryClient();
  const [rules, setRules] = useState<PolicyRule[]>(cfg.policy.rules);
  const save = useMutation({ mutationFn: () => requirementsApi.savePolicy(rules), onSuccess: () => qc.invalidateQueries({ queryKey: ["requirement-config"] }) });
  const levels = cfg.settings.risk_levels;
  const roleName = (k?: string) => cfg.roles.find((r) => r.key === k)?.name ?? k ?? "";
  const setRule = (i: number, r: PolicyRule) => setRules(rules.map((x, j) => (j === i ? r : x)));
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle>{t("configurazione.requirements.politicaDiApprovazione")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">
        {t("configurazione.requirements.leRegoleSiLeggono")}
      </p>
      <ol className="m-0 flex list-none flex-col gap-3 p-0">
        {rules.map((r, i) => (
          <li key={i} className="flex flex-col gap-2 border-l-2 border-hair pl-3">
            <span className="flex flex-wrap items-center gap-2 text-[13px]">
              <span className="font-mono text-[12px] text-muted">{i + 1}.</span>
              <Select
                className="field w-auto py-1 text-[12.5px]"
                aria-label={t("configurazione.requirements.sogliaDiRischio")}
                disabled={!cfg.can_edit}
                value={r.when.risk_lte != null ? `lte:${r.when.risk_lte}` : r.when.risk_gt != null ? `gt:${r.when.risk_gt}` : ""}
                onChange={(e) => {
                  const [op, n] = e.target.value.split(":");
                  setRule(i, { ...r, when: op === "lte" ? { risk_lte: Number(n) } : op === "gt" ? { risk_gt: Number(n) } : {} });
                }}
              >
                <option value="">{t("configurazione.requirements.qualsiasiRischio")}</option>
                {levels.map((l) => (
                  <option key={`lte${l.key}`} value={`lte:${l.score}`}>
                    {t("configurazione.requirements.riskUpTo", { level: l.name.toLowerCase() })}
                  </option>
                ))}
                {levels.slice(0, -1).map((l) => (
                  <option key={`gt${l.key}`} value={`gt:${l.score}`}>
                    {t("configurazione.requirements.riskAbove", { level: l.name.toLowerCase() })}
                  </option>
                ))}
              </Select>
              {cfg.can_edit && (
                <button className="ml-auto text-[12px] text-muted underline" onClick={() => setRules(rules.filter((_, j) => j !== i))}>
                  {t("configurazione.common.togliLaRegola")}
                </button>
              )}
            </span>
            {r.steps.map((st, si) => (
              <span key={si} className="flex flex-wrap items-center gap-2 text-[13px]">
                <span className="text-muted">{t("configurazione.requirements.stepColon", { n: si + 1 })}</span>
                {cfg.can_edit ? (
                  <Select
                    className="field w-auto py-1 text-[12.5px]"
                    aria-label={t("configurazione.requirements.approverOfStep", { n: si + 1 })}
                    value={st.approvers[0]?.role ?? ""}
                    onChange={(e) => setRule(i, { ...r, steps: r.steps.map((x, k) => (k === si ? { approvers: [{ role: e.target.value }] } : x)) })}
                  >
                    {cfg.roles.map((ro) => (
                      <option key={ro.key} value={ro.key}>
                        {ro.name}
                      </option>
                    ))}
                  </Select>
                ) : (
                  <span>{st.approvers.map((a) => roleName(a.role) || a.body || a.member).join(", ")}</span>
                )}
                {st.approvers.length > 1 && <span className="text-[12px] text-muted">{t("configurazione.requirements.andOthersParallel", { count: st.approvers.length - 1 })}</span>}
                {cfg.can_edit && r.steps.length > 1 && (
                  <button className="text-[12px] text-muted underline" onClick={() => setRule(i, { ...r, steps: r.steps.filter((_, k) => k !== si) })}>
                    {t("configurazione.common.togli2")}
                  </button>
                )}
              </span>
            ))}
            {cfg.can_edit && (
              <button
                className="self-start text-[12px] text-accent underline"
                onClick={() => setRule(i, { ...r, steps: [...r.steps, { approvers: [{ role: "sponsor" }] }] })}
              >
                {t("configurazione.common.aggiungiUnPasso")}
              </button>
            )}
          </li>
        ))}
      </ol>
      {cfg.can_edit && (
        <span className="flex flex-wrap gap-2">
          <button className="btn" onClick={() => setRules([...rules, { when: {}, steps: [{ approvers: [{ role: "requirement_verifier" }] }] }])}>
            {t("configurazione.common.aggiungiUnaRegola")}
          </button>
          <button className="btn btn-primary" disabled={save.isPending} onClick={() => save.mutate()}>
            {t("configurazione.common.salvaLaPolitica")}
          </button>
        </span>
      )}
      {save.isSuccess && <p className="m-0 text-[12.5px] text-ok">{t("configurazione.common.salvata")}</p>}
      <ErrorNote error={save.error} />
    </section>
  );
}
