// Conformità (piano, passo 20; replica/grc.md): Governance → Conformità, /conformita.
// Panoramica per normativa e per applicazione, drill-down requisito → perimetro → controllo
// → evidenza, controlli, evidenze, cataloghi (pacchetti di partenza, import ed export OSCAL,
// CSV e JSON, versioni, corrispondenze) e regole di applicabilità. Pacchetto per l'audit
// (zip) e report stampabile.
import { EntitySelect, Select } from "../select";
import { Fragment, useEffect, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, date, Loading } from "../ui";
import {
  EFFECTIVENESS,
  EVIDENCE_KINDS,
  EVIDENCE_STATUS,
  FREQUENCIES,
  ITEM_KINDS,
  RELATIONS,
  SCOPE_NAMES,
  grcApi,
  type CoverageItem,
  type Dashboard,
  type FrameworkCoverage,
  type FrameworkInfo,
  type PairView,
  type RuleInput,
  type RuleView,
} from "../grcApi";
import { MappingWorkbench } from "./ComplianceMapping";
import { livingApi } from "../grcLivingApi";
import { CoverageBar, CoverageMark, EvidenceLink, EvidenceMark, FrameworkTag, scopeLabel, useGrcPerms } from "./grc-ui";
import "../print.css";
import { useTypeNames } from "../typesApi";
import { tr } from "../i18n/core";
import { Trans } from "../i18n/Trans";
import { loc } from "../i18n/format";
import { crumbs } from "../crumbs";
import { ConditionBuilder } from "./processEditor/ConditionBuilder";
import type { CondCount } from "../casaApi";
import type { FactInfo } from "../api";

export type ComplianceTab = "panoramica" | "normative" | "applicazioni" | "controlli" | "mappatura" | "evidenze" | "cataloghi" | "regole";
export const COMPLIANCE_TABS: ComplianceTab[] = ["panoramica", "normative", "applicazioni", "controlli", "mappatura", "evidenze", "cataloghi", "regole"];
export type ComplianceSearch = { tab?: ComplianceTab; normativa?: string; perimetro?: string; stato?: string; requisito?: string };

const TAB_LABELS: Record<ComplianceTab, string> = {
  get panoramica() { return tr("governance.compliance.panoramica"); },
  get normative() { return tr("governance.compliance.normative"); },
  get applicazioni() { return tr("governance.compliance.applicazioni"); },
  get controlli() { return tr("governance.compliance.controlli"); },
  get mappatura() { return tr("governance.controlli.tab_mappatura"); },
  get evidenze() { return tr("governance.compliance.evidenze"); },
  get cataloghi() { return tr("governance.compliance.cataloghi"); },
  get regole() { return tr("governance.compliance.applicabilita"); },
};

export function Compliance({ search }: { search: ComplianceSearch }) {
  const tab = search.tab ?? "panoramica";
  const dash = useQuery({ queryKey: ["grc", "dashboard"], queryFn: grcApi.dashboard });
  const [printing, setPrinting] = useState(false);
  useEffect(() => {
    document.title = tr("governance.compliance.conformita_regia");
    return () => {
      document.title = "Regia";
    };
  }, []);
  return (
    <>
      <PageHeader
        path={crumbs("governance", "compliance")}
        title={tr("governance.compliance.conformita")}
        actions={
          <>
            <button className="btn" onClick={() => setPrinting(true)} disabled={!dash.data}>{tr("governance.compliance.report_stampabile")}</button>
            <a className="btn btn-primary no-underline" href={grcApi.auditPackUrl(tab === "normative" ? search.normativa : "")} download>{tr("governance.compliance.pacchetto_per_l_audit")}</a>
          </>
        }
      />
      <div role="tablist" aria-label={tr("governance.compliance.sezioni")} className="flex gap-6 overflow-x-auto border-b border-hair px-4 md:px-7">
        {COMPLIANCE_TABS.map((k) => (
          <Link
            key={k}
            to="/conformita"
            search={{ tab: k === "panoramica" ? undefined : k }}
            role="tab"
            aria-selected={tab === k}
            className={`shrink-0 pb-2.5 no-underline ${tab === k ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
          >
            {TAB_LABELS[k]}
          </Link>
        ))}
      </div>
      <div className="flex min-w-0 flex-col gap-6 px-4 py-5 pb-10 md:px-7">
        <ErrorNote error={dash.error} />
        {tab === "panoramica" && dash.data && <Overview d={dash.data} />}
        {tab === "normative" && dash.data && <Frameworks d={dash.data} selected={search.normativa} open={search.requisito} />}
        {tab === "applicazioni" && dash.data && <Scopes d={dash.data} selected={search.perimetro} />}
        {tab === "controlli" && <Controls />}
        {tab === "mappatura" && <MappingWorkbench />}
        {tab === "evidenze" && <EvidenceTab status={search.stato} />}
        {tab === "cataloghi" && <Catalogs />}
        {tab === "regole" && <RulesTab />}
      </div>
      {printing && dash.data && <PrintReport d={dash.data} onDone={() => setPrinting(false)} />}
    </>
  );
}

// ---------------------------------------------------------------------
// Panoramica

function Stat({ label, value, note, tone }: { label: string; value: ReactNode; note?: ReactNode; tone?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <span className="label text-muted">{label}</span>
      <span className={`font-mono text-[34px] leading-none font-semibold tabular ${tone ?? ""}`}>{value}</span>
      {note && <span className="text-[12px] text-muted">{note}</span>}
    </div>
  );
}

function Overview({ d }: { d: Dashboard }) {
  if (d.frameworks.length === 0) return <Empty canEdit={d.can_edit} />;
  return (
    <>
      <section aria-label={tr("governance.compliance.in_sintesi")} className="grid grid-cols-2 gap-x-6 gap-y-5 border-y-2 border-rule py-4 md:grid-cols-4">
        <Stat
          label={tr("governance.compliance.copertura_pesata_per_criticita")}
          value={d.tenant.applicable ? `${d.tenant.weighted_percent}%` : "—"}
          note={tr("governance.compliance.semplice_coppie_coperte_su_un", { percent: d.tenant.percent, covered: d.tenant.covered, applicable: d.tenant.applicable })}
        />
        <Stat label={tr("governance.compliance.controlli_in_esercizio")} value={d.controls} />
        <Stat label={tr("governance.compliance.evidenze_valide")} value={d.valid_evidence} />
        <Stat label={tr("governance.compliance.evidenze_in_scadenza")} value={d.expiring.length} tone={d.expiring.length ? "text-flag" : ""} note={tr("governance.compliance.entro_giorni", { expiry_notice_days: d.settings.expiry_notice_days })} />
      </section>
      <section className="flex flex-col gap-2">
        <SectionTitle>{tr("governance.compliance.per_normativa")}</SectionTitle>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] border-collapse text-[13px]">
            <thead>
              <tr className="border-b-2 border-rule text-left">
                <th className="label py-1.5">{tr("governance.compliance.normativa")}</th>
                <th className="label py-1.5">{tr("governance.compliance.copertura")}</th>
                <th className="label py-1.5 text-right" title={tr("governance.compliance.requisiti_con_almeno_un_perimetro")}>{tr("governance.compliance.senza_controllo")}</th>
                <th className="label py-1.5 text-right" title={tr("governance.compliance.controlli_che_in_qualche_perimetro")}>{tr("governance.compliance.controlli_senza_evidenza")}</th>
                <th className="label py-1.5 text-right">{tr("governance.compliance.in_scadenza")}</th>
                <th className="label hidden py-1.5 text-right md:table-cell">{tr("governance.compliance.perimetri")}</th>
              </tr>
            </thead>
            <tbody>
              {d.frameworks.map((f) => (
                <tr key={f.key} className="border-b border-hair">
                  <td className="py-2 pr-3">
                    <Link to="/conformita" search={{ tab: "normative", normativa: f.key }} className="flex flex-col no-underline">
                      <span className="font-semibold text-fg">{f.name}</span>
                      <span className="flex items-center gap-2 text-[12px] text-muted">
                        <FrameworkTag k={f.key} />{" "}{tr("governance.compliance.versione_requisiti", { version: f.version, requirements: f.requirements })}</span>
                    </Link>
                  </td>
                  <td className="py-2 pr-3">
                    <CoverageBar c={f} wide />
                  </td>
                  <td className={`py-2 text-right font-mono tabular ${f.without_control ? "text-danger" : ""}`}>{f.without_control}</td>
                  <td className={`py-2 text-right font-mono tabular ${f.controls_without_evidence ? "text-danger" : ""}`}>{f.controls_without_evidence}</td>
                  <td className={`py-2 text-right font-mono tabular ${f.expiring_evidence ? "text-flag" : ""}`}>{f.expiring_evidence}</td>
                  <td className="hidden py-2 text-right font-mono tabular md:table-cell">{f.scopes}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <Legend />
      </section>
      {d.scopes.length > 0 && <ScopeTable d={d} />}
      <div className="grid grid-cols-1 gap-x-10 gap-y-8 lg:grid-cols-3">
        <section className="flex flex-col gap-2">
          <SectionTitle aside={<span className="flex items-baseline gap-3"><Link to="/conformita" search={{ tab: "mappatura" }} className="touch-44 inline-flex items-center text-[12.5px] text-accent">{tr("governance.controlli.map_open")}</Link><span className="font-mono text-[11.5px] text-muted tabular">{d.without_control.length}</span></span>}>{tr("governance.compliance.requisiti_senza_controllo")}</SectionTitle>
          <ul className="m-0 flex list-none flex-col p-0">
            {d.without_control.slice(0, 12).map((r) => (
              <li key={r.id} className="border-b border-hair py-1.5">
                <Link to="/conformita" search={{ tab: "normative", normativa: r.framework, requisito: r.id }} className="flex items-baseline gap-2 no-underline">
                  <FrameworkTag k={r.framework} />
                  <span className="font-mono text-[12px] text-fg">{r.ref}</span>
                  <span className="min-w-0 truncate text-fg">{r.title}</span>
                </Link>
              </li>
            ))}
            {d.without_control.length === 0 && <li className="py-1.5 text-muted">{tr("governance.compliance.ogni_requisito_ha_almeno_un")}</li>}
            {d.without_control.length > 12 && <li className="py-1.5 text-[12px] text-muted">{tr("governance.compliance.e_altri_apri_la_normativa", { p: d.without_control.length - 12 })}</li>}
          </ul>
        </section>
        <section className="flex flex-col gap-2">
          <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{d.controls_without_evidence.length}</span>}>{tr("governance.compliance.controlli_senza_evidenza_valida")}</SectionTitle>
          <ul className="m-0 flex list-none flex-col p-0">
            {d.controls_without_evidence.map((c) => (
              <li key={c.id} className="flex flex-col border-b border-hair py-1.5">
                <span className="flex items-baseline gap-2">
                  <Link to="/e/$key" params={{ key: c.key }} search={{ scheda: "dettagli" }} className="font-mono text-[12px]">
                    {c.key}
                  </Link>
                  <span className="min-w-0 truncate">{c.title}</span>
                </span>
                <span className="text-[12px] text-muted">
                  {c.owner || tr("governance.compliance.senza_responsabile")}{" "}{tr("governance.compliance.manca_in")}{" "}{c.missing.map(scopeLabel).join(", ")}
                </span>
              </li>
            ))}
            {d.controls_without_evidence.length === 0 && <li className="py-1.5 text-muted">{tr("governance.compliance.tutti_i_controlli_hanno_evidenze")}</li>}
          </ul>
        </section>
        <section className="flex flex-col gap-2">
          <SectionTitle aside={<span className="font-mono text-[11.5px] text-flag tabular">{d.expiring.length}</span>}>{tr("governance.compliance.evidenze_in_scadenza")}</SectionTitle>
          <ul className="m-0 flex list-none flex-col p-0">
            {d.expiring.map((e) => (
              <li key={e.id} className="flex flex-col border-b border-hair py-1.5">
                <EvidenceLink e={e} />
                <span className="text-[12px] text-muted">{tr("governance.compliance.controlli_2")}{" "}{(e.controls ?? []).join(", ")}</span>
              </li>
            ))}
            {d.expiring.length === 0 && <li className="py-1.5 text-muted">{tr("governance.compliance.nessuna_nei_prossimi_giorni", { expiry_notice_days: d.settings.expiry_notice_days })}</li>}
          </ul>
        </section>
      </div>
      <p className="m-0 font-mono text-[11px] text-muted">
        {tr("governance.compliance.calcolata_il_evidenza", { date: date(d.generated_at), recomputed: d.settings.recomputed_at ? tr("governance.compliance.applicabilita_ricalcolata_il", { date: date(d.settings.recomputed_at) }) : "" })}
      </p>
    </>
  );
}

function Legend() {
  return (
    <p className="m-0 flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-muted">
      <CoverageMark status="covered" />
      <CoverageMark status="partial" />
      <CoverageMark status="no_evidence" />
      <CoverageMark status="no_control" />
      <span>{tr("governance.compliance.la_percentuale_conta_i_requisiti")}</span>
    </p>
  );
}

function Empty({ canEdit }: { canEdit: boolean }) {
  return (
    <div className="flex max-w-2xl flex-col gap-3 border-y-2 border-rule py-5">
      <h2 className="m-0 text-[20px] font-bold">{tr("governance.compliance.nessuna_normativa_abilitata")}</h2>
      <p className="m-0 text-muted">{tr("governance.compliance.le_normative_sono_cataloghi_di")}</p>
      {canEdit && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
          <Link to="/conformita" search={{ tab: "cataloghi" }} className="btn btn-primary self-start no-underline">{tr("governance.compliance.vai_ai_cataloghi")}</Link>
          <Link to="/configurazione/avvio" search={{ tab: "modelli" }} className="touch-44 inline-flex items-center text-accent underline">{tr("lavoro.vuoti.conformita.modello")}</Link>
        </div>
      )}
    </div>
  );
}

function ScopeTable({ d }: { d: Dashboard }) {
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{d.scopes.length}</span>}>{tr("governance.compliance.per_applicazione_e_perimetro")}</SectionTitle>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[560px] border-collapse text-[13px]">
          <thead>
            <tr className="border-b-2 border-rule text-left">
              <th className="label py-1.5">{tr("governance.compliance.perimetro")}</th>
              <th className="label py-1.5">{tr("governance.compliance.normative_a_cui_e_soggetto")}</th>
              <th className="label py-1.5">{tr("governance.compliance.copertura")}</th>
            </tr>
          </thead>
          <tbody>
            {d.scopes.map((s) => (
              <tr key={`${s.kind}:${s.id}`} className="border-b border-hair">
                <td className="py-2 pr-3">
                  <Link to="/conformita" search={{ tab: "applicazioni", perimetro: `${s.kind}:${s.key || s.id}` }} className="flex flex-col no-underline">
                    <span className="font-medium text-fg">{s.name}</span>
                    <span className="font-mono text-[11.5px] text-muted">
                      {SCOPE_NAMES[s.kind]} {s.key}
                    </span>
                  </Link>
                </td>
                <td className="py-2 pr-3">
                  <span className="flex flex-wrap gap-x-3 gap-y-1">
                    {s.frameworks.map((f) => (
                      <span key={f.key} className="inline-flex items-center gap-1.5">
                        <FrameworkTag k={f.key} title={f.name} />
                        <CoverageMark status={f.worst} />
                      </span>
                    ))}
                  </span>
                </td>
                <td className="py-2">
                  <CoverageBar c={s} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------
// Normative: copertura voce per voce, fino all'evidenza

function Frameworks({ d, selected, open }: { d: Dashboard; selected?: string; open?: string }) {
  const navigate = useNavigate();
  const key = selected ?? d.frameworks[0]?.key;
  if (!key) return <Empty canEdit={d.can_edit} />;
  return (
    <div className="flex flex-col gap-4">
      <label className="flex max-w-md flex-col gap-1">
        <span className="label text-muted">{tr("governance.compliance.normativa")}</span>
        <Select className="field" value={key} onChange={(e) => navigate({ to: "/conformita", search: { tab: "normative", normativa: e.target.value } })}>
          {d.frameworks.map((f) => (
            <option key={f.key} value={f.key}>
              {f.name} · {f.percent}%
            </option>
          ))}
        </Select>
      </label>
      <FrameworkTree key={key} fwKey={key} open={open} />
    </div>
  );
}

function FrameworkTree({ fwKey, open }: { fwKey: string; open?: string }) {
  const cov = useQuery({ queryKey: ["grc", "coverage", fwKey], queryFn: () => grcApi.coverage(fwKey) });
  const [expanded, setExpanded] = useState<string | null>(open ?? null);
  const [filter, setFilter] = useState<"" | "problemi">("");
  const c = cov.data;
  if (cov.error) return <ErrorNote error={cov.error} />;
  if (!c) return <p className="m-0 text-muted">{tr("governance.compliance.calcolo")}</p>;
  const depth = (it: CoverageItem) => {
    let n = 0;
    let p = it.parent;
    const byRef = new Map(c.items.map((x) => [x.ref, x]));
    while (p && n < 6) {
      n++;
      p = byRef.get(p)?.parent ?? "";
    }
    return n;
  };
  const shown = c.items.filter((it) => !filter || !it.assessable || it.status === "no_control" || it.status === "no_evidence" || it.status === "partial");
  return (
    <section className="flex flex-col gap-2">
      {c.framework && (
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2 border-y-2 border-rule py-3">
          <span className="flex flex-col">
            <span className="text-[17px] font-bold">{c.framework.name}</span>
            <span className="text-[12px] text-muted">{tr("governance.compliance.versione_requisiti_perimetri", { version: c.framework.version, requirements: c.framework.requirements, scopes: c.framework.scopes })}</span>
          </span>
          {c.enabled ? (
            <div className="min-w-[220px] flex-1">
              <CoverageBar c={c.framework} wide />
            </div>
          ) : (
            <span className="text-muted">{tr("governance.compliance.non_abilitata_nessuna_copertura_calcolata")}</span>
          )}
          <div className="flex gap-2">
            <a className="btn no-underline" href={grcApi.exportUrl(fwKey, "oscal")} download>
              OSCAL
            </a>
            <a className="btn no-underline" href={grcApi.exportUrl(fwKey, "csv")} download>
              CSV
            </a>
          </div>
        </div>
      )}
      <label className="flex items-center gap-2 self-start text-[13px]">
        <input type="checkbox" checked={filter === "problemi"} onChange={(e) => setFilter(e.target.checked ? "problemi" : "")} />{tr("governance.compliance.solo_i_requisiti_da_sistemare")}</label>
      <ul className="m-0 flex list-none flex-col p-0" aria-label={tr("governance.compliance.voci_del_catalogo")}>
        {shown.map((it) => {
          const isOpen = expanded === it.id;
          const pad = { paddingLeft: `${depth(it) * 16}px` };
          if (!it.assessable)
            return (
              <li key={it.id} className="flex flex-wrap items-baseline gap-x-3 border-b border-hair pt-3 pb-1.5" style={pad}>
                <span className="font-mono text-[12px] text-muted">{it.ref}</span>
                <span className="font-semibold">{it.title}</span>
                {it.counts.applicable > 0 && (
                  <span className="ml-auto text-[12px] text-muted tabular">{tr("governance.compliance.coperti", { covered: it.counts.covered, applicable: it.counts.applicable })}</span>
                )}
              </li>
            );
          return (
            <li key={it.id} className="border-b border-hair" style={pad}>
              <button
                type="button"
                aria-expanded={isOpen}
                onClick={() => setExpanded(isOpen ? null : it.id)}
                className="flex w-full flex-wrap items-baseline gap-x-3 gap-y-1 py-2 text-left hover:bg-sel"
              >
                <span className="w-24 shrink-0 font-mono text-[12px] text-accent">{it.ref}</span>
                <span className="min-w-0 flex-1">{it.title}</span>
                <CoverageMark status={it.status} expiring={it.counts.expiring > 0} />
                <span className="w-24 text-right text-[12px] text-muted tabular">
                  {it.counts.applicable > 0 ? tr("governance.compliance.perim", { covered: it.counts.covered, applicable: it.counts.applicable }) : ""}{" "}{tr("governance.compliance.contr", { controls: it.controls })}</span>
              </button>
              {isOpen && <RequirementPanel id={it.id} summary={it.summary} pairs={it.pairs ?? []} />}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function PairsTable({ pairs }: { pairs: PairView[] }) {
  if (pairs.length === 0) return <p className="m-0 text-muted">{tr("governance.compliance.non_si_applica_a_nessun")}</p>;
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[520px] border-collapse text-[13px]">
        <thead>
          <tr className="border-b-2 border-rule text-left">
            <th className="label py-1">{tr("governance.compliance.perimetro")}</th>
            <th className="label py-1">{tr("governance.compliance.stato")}</th>
            <th className="label py-1">{tr("governance.compliance.controlli_ed_evidenze")}</th>
          </tr>
        </thead>
        <tbody>
          {pairs.map((p) => (
            <tr key={`${p.scope.kind}:${p.scope.id}`} className="border-b border-hair align-top">
              <td className="py-1.5 pr-3">
                <span className="flex flex-col">
                  <span>{scopeLabel(p.scope)}</span>
                  <span className="text-[11.5px] text-muted">
                    {p.source === "tenant" ? tr("governance.compliance.vale_per_tutti") : p.source === "manual" ? tr("governance.compliance.decisione") : tr("governance.compliance.regola", { p: p.source.slice(5) })}
                  </span>
                </span>
              </td>
              <td className="py-1.5 pr-3">
                <CoverageMark status={p.status} expiring={p.expiring} />
                {p.note && p.status === "not_applicable" && <span className="block text-[12px] text-muted">{p.note}</span>}
              </td>
              <td className="py-1.5">
                {p.controls.length === 0 && <span className="text-muted">—</span>}
                <ul className="m-0 flex list-none flex-col gap-1 p-0">
                  {p.controls.map((c) => (
                    <li key={c.id} className="flex flex-col">
                      <span className="flex flex-wrap items-baseline gap-x-2">
                        <Link to="/e/$key" params={{ key: c.key }} search={{ scheda: "dettagli" }} className="font-mono text-[12px]">
                          {c.key}
                        </Link>
                        <span>{c.title}</span>
                        {c.coverage === "partial" && <span className="text-[12px] text-muted">{tr("governance.compliance.copertura_parziale")}</span>}
                        {c.effectiveness !== "effective" && c.effectiveness !== "not_assessed" && (
                          <span className="text-[12px] text-danger">{tr("governance.compliance.efficacia", { p: EFFECTIVENESS[c.effectiveness] })}</span>
                        )}
                      </span>
                      {c.evidence.map((e) => (
                        <span key={e.id} className="pl-3 text-[12.5px]">
                          <EvidenceLink e={e} />
                        </span>
                      ))}
                      {c.evidence.length === 0 && <span className="pl-3 text-[12.5px] text-danger">{tr("governance.compliance.nessuna_evidenza_valida")}</span>}
                    </li>
                  ))}
                </ul>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RequirementPanel({ id, summary, pairs }: { id: string; summary: string; pairs: PairView[] }) {
  const qc = useQueryClient();
  const det = useQuery({ queryKey: ["grc", "requirement", id], queryFn: () => grcApi.requirement(id) });
  const [why, setWhy] = useState("");
  const [editing, setEditing] = useState(false);
  const r = det.data;
  const tenantNA = r?.decisions.find((x) => x.scope.kind === "tenant" && !x.rule);
  const save = useMutation({
    mutationFn: (na: boolean) => {
      const others = (r?.decisions ?? [])
        .filter((x) => !x.rule && x.scope.kind !== "tenant")
        .map((x) => ({ scope: x.scope.kind, target: x.scope.key || x.scope.id, status: x.status, justification: x.justification }));
      return grcApi.setApplicability(id, na ? [...others, { scope: "tenant", status: "not_applicable", justification: why }] : others);
    },
    onSuccess: () => {
      setEditing(false);
      qc.invalidateQueries({ queryKey: ["grc"] });
    },
  });
  return (
    <div className="flex flex-col gap-3 border-l-2 border-accent bg-sel/40 px-3 py-3 mb-2">
      {summary && <p className="m-0 max-w-3xl text-[13px]">{summary}</p>}
      <PairsTable pairs={pairs} />
      <ErrorNote error={det.error ?? save.error} />
      {r && (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.compliance.controlli_collegati")}</span>
            {r.mapped_controls.length === 0 && <span className="text-muted">{tr("governance.compliance.nessuno_collegalo_dal_controllo")}</span>}
            {r.mapped_controls.map((c) => (
              <span key={c.id} className="flex flex-wrap items-baseline gap-x-2 text-[13px]">
                <Link to="/e/$key" params={{ key: c.key }} search={{ scheda: "dettagli" }} className="font-mono text-[12px]">
                  {c.key}
                </Link>
                {c.title}
                <span className="text-[12px] text-muted">
                  {c.phase}
                  {c.coverage === "partial" ? tr("governance.compliance.parziale") : ""}
                </span>
              </span>
            ))}
          </div>
          <div className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.compliance.corrispondenze_con_altre_normative")}</span>
            {r.mappings.length === 0 && <span className="text-muted">{tr("governance.compliance.nessuna")}</span>}
            {r.mappings.map((m) => {
              const other = m.from_id === id ? { ref: m.to, title: m.to_title } : { ref: m.from, title: m.from_title };
              const [fw, ref] = [other.ref.split(":")[0], other.ref.slice(other.ref.indexOf(":") + 1)];
              return (
                <span key={m.id} className="flex flex-wrap items-baseline gap-x-2 text-[13px]">
                  <FrameworkTag k={fw} />
                  <span className="font-mono text-[12px]">{ref}</span>
                  <span>{other.title}</span>
                  <span className="text-[12px] text-muted">{RELATIONS[m.relation]}</span>
                </span>
              );
            })}
          </div>
          {r.rules.length > 0 && (
            <p className="m-0 text-[12.5px] text-muted md:col-span-2">{tr("governance.compliance.si_applica_solo_dove_lo")}{" "}<span className="font-mono">{r.rules.join(", ")}</span>.
            </p>
          )}
          {r.can_edit && (
            <div className="flex flex-col gap-2 md:col-span-2">
              {tenantNA ? (
                <p className="m-0 text-[13px]">{tr("governance.compliance.non_applicabile_all_organizzazione", { justification: tenantNA.justification, decided_by: tenantNA.decided_by, date: date(tenantNA.decided_at) })}{" "}<button className="btn ml-2" onClick={() => save.mutate(false)} disabled={save.isPending}>{tr("governance.compliance.torna_applicabile")}</button>
                </p>
              ) : editing ? (
                <form
                  className="flex flex-wrap items-end gap-2"
                  onSubmit={(e) => {
                    e.preventDefault();
                    save.mutate(true);
                  }}
                >
                  <label className="flex min-w-[260px] flex-1 flex-col gap-1">
                    <span className="label text-muted">{tr("governance.compliance.perche_non_si_applica")}</span>
                    <input className="field" required value={why} onChange={(e) => setWhy(e.target.value)} />
                  </label>
                  <button className="btn btn-primary" disabled={!why.trim() || save.isPending}>{tr("governance.compliance.segna_non_applicabile")}</button>
                  <button type="button" className="btn" onClick={() => setEditing(false)}>{tr("governance.compliance.annulla")}</button>
                </form>
              ) : (
                !r.rules.length && (
                  <button className="btn self-start" onClick={() => setEditing(true)}>{tr("governance.compliance.non_applicabile")}</button>
                )
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Applicazioni e perimetri

function Scopes({ d, selected }: { d: Dashboard; selected?: string }) {
  const [kind, target] = selected ? [selected.slice(0, selected.indexOf(":")), selected.slice(selected.indexOf(":") + 1)] : ["", ""];
  return (
    <div className="flex flex-col gap-6">
      {selected && <ScopeDetail kind={kind} target={target} />}
      {d.scopes.length > 0 ? (
        <ScopeTable d={d} />
      ) : (
        <p className="m-0 text-muted">
          <Trans k="governance.compliance.nessun_perimetro_con_requisiti_propri_regole" slots={{ /* i18n-ok: esempio di condizione, i nomi dei campi sono dati */ example: <span className="font-mono">funzione_critica: sì</span> }} />
        </p>
      )}
    </div>
  );
}

function ScopeDetail({ kind, target }: { kind: string; target: string }) {
  const sc = useQuery({ queryKey: ["grc", "scope", kind, target], queryFn: () => grcApi.scope(kind, target) });
  const s = sc.data;
  if (sc.error) return <ErrorNote error={sc.error} />;
  if (!s) return <p className="m-0 text-muted">{tr("governance.compliance.calcolo")}</p>;
  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 border-y-2 border-rule py-3">
        <h2 className="m-0 text-[20px] font-bold">{s.scope.name}</h2>
        {s.scope.key && (
          <Link to="/e/$key" params={{ key: s.scope.key }} className="font-mono text-[12px]">
            {s.scope.key}
          </Link>
        )}
        <span className="text-[12.5px] text-muted">{tr("governance.compliance.normative_con_requisiti_propri_piu", { p: SCOPE_NAMES[s.scope.kind], length: s.frameworks.length, applicable: s.tenant.applicable, percent: s.tenant.percent })}</span>
      </div>
      {s.frameworks.length === 0 && <p className="m-0 text-muted">{tr("governance.compliance.nessun_requisito_proprio_di_questo")}</p>}
      {s.frameworks.map((f) => (
        <div key={f.key} className="flex flex-col gap-2">
          <SectionTitle aside={<CoverageBar c={f} />}>
            <span className="inline-flex items-center gap-2">
              <FrameworkTag k={f.key} /> {f.name}
            </span>
          </SectionTitle>
          <ul className="m-0 flex list-none flex-col p-0">
            {f.items.map((it) => (
              <li key={it.id} className="flex flex-col gap-1 border-b border-hair py-2">
                <span className="flex flex-wrap items-baseline gap-x-3">
                  <Link to="/conformita" search={{ tab: "normative", normativa: f.key, requisito: it.id }} className="w-24 font-mono text-[12px]">
                    {it.ref}
                  </Link>
                  <span className="min-w-0 flex-1">{it.title}</span>
                  <CoverageMark status={it.status} expiring={it.expiring} />
                </span>
                {it.controls.map((c) => (
                  <span key={c.id} className="pl-[108px] text-[12.5px] max-sm:pl-3">
                    <Link to="/e/$key" params={{ key: c.key }} search={{ scheda: "dettagli" }} className="font-mono text-[12px]">
                      {c.key}
                    </Link>{" "}
                    {c.title}
                    {c.evidence.map((e) => (
                      <span key={e.id} className="block pl-3">
                        <EvidenceLink e={e} />
                      </span>
                    ))}
                  </span>
                ))}
              </li>
            ))}
          </ul>
        </div>
      ))}
    </section>
  );
}

// ---------------------------------------------------------------------
// Controlli

function Controls() {
  const tn = useTypeNames();
  const { admin } = useGrcPerms();
  const list = useQuery({ queryKey: ["grc", "controls"], queryFn: grcApi.controls });
  const props = useQuery({ queryKey: ["grc", "proposals"], queryFn: livingApi.proposals });
  const proposalOf = new Map((props.data?.items ?? []).map((x) => [x.control, x]));
  const [creating, setCreating] = useState(false);
  const items = list.data?.items ?? [];
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle
        aside={
          admin && !creating ? (
            <button className="btn" onClick={() => setCreating(true)}>
              {tn.create("control", tr("governance.compliance.controllo"))}
            </button>
          ) : undefined
        }
      >{tr("governance.compliance.controlli_interni")}</SectionTitle>
      {creating && <NewControl onDone={() => setCreating(false)} />}
      <ErrorNote error={list.error} />
      <div className="overflow-x-auto">
        <table className="w-full min-w-[720px] border-collapse text-[13px]">
          <thead>
            <tr className="border-b-2 border-rule text-left">
              <th className="label py-1.5">{tr("governance.compliance.controllo")}</th>
              <th className="label py-1.5">{tr("governance.compliance.responsabile")}</th>
              <th className="label py-1.5">{tr("governance.compliance.frequenza")}</th>
              <th className="label py-1.5">{tr("governance.compliance.efficacia_2")}</th>
              <th className="label py-1.5">{tr("governance.compliance.normative")}</th>
              <th className="label py-1.5 text-right">{tr("governance.compliance.evidenze_valide")}</th>
              <th className="label py-1.5 text-right" title={tr("governance.compliance.perimetri_in_cui_serve_e")}>{tr("governance.compliance.scoperti")}</th>
              <th className="label py-1.5 text-right">{tr("governance.compliance.prossima_scadenza")}</th>
            </tr>
          </thead>
          <tbody>
            {items.map((c) => (
              <tr key={c.id} className="border-b border-hair">
                <td className="py-2 pr-3">
                  <Link to="/e/$key" params={{ key: c.key }} search={{ scheda: "dettagli" }} className="flex flex-col no-underline">
                    <span className="text-fg">
                      <span className="font-mono text-[12px] text-accent">{c.key}</span> {c.title}
                    </span>
                    <span className="text-[12px] text-muted">{c.phase}</span>
                  </Link>
                </td>
                <td className="py-2 pr-3">{c.owner || <span className="text-danger">{tr("governance.compliance.da_indicare")}</span>}</td>
                <td className="py-2 pr-3">{FREQUENCIES[c.frequency]}</td>
                <td className={`py-2 pr-3 ${c.effectiveness === "ineffective" ? "text-danger" : ""}`}>
                  {EFFECTIVENESS[c.effectiveness]}
                  {proposalOf.get(c.key)?.pending && (
                    <span className="block text-[12px] text-flag">{tr("governance.controlli.ctl_proposal", { state: EFFECTIVENESS[proposalOf.get(c.key)!.proposed] ?? "" })}</span>
                  )}
                </td>
                <td className="py-2 pr-3">
                  <span className="flex flex-wrap gap-1">
                    {c.frameworks.map((f) => (
                      <FrameworkTag key={f} k={f} />
                    ))}
                  </span>
                </td>
                <td className="py-2 text-right font-mono tabular">{c.valid_evidence}</td>
                <td className={`py-2 text-right font-mono tabular ${c.missing_scopes ? "text-danger" : ""}`}>{c.operational ? c.missing_scopes : "—"}</td>
                <td className={`py-2 text-right font-mono tabular ${c.expiring_evidence ? "text-flag" : ""}`}>{c.next_expiry ? date(c.next_expiry) : "—"}</td>
              </tr>
            ))}
            {items.length === 0 && list.data && (
              <tr>
                <td colSpan={8} className="py-3 text-muted">{tr("governance.compliance.nessun_controllo_un_controllo_copre")}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function NewControl({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const [title, setTitle] = useState("");
  const [text, setText] = useState("");
  const [owner, setOwner] = useState("");
  const [freq, setFreq] = useState("annual");
  const [refs, setRefs] = useState("");
  const create = useMutation({
    mutationFn: () =>
      grcApi.createControl({
        title,
        text,
        owner_id: owner || undefined,
        frequency: freq,
        requirements: refs
          .split(/\n/)
          .map((r) => r.trim())
          .filter(Boolean)
          .map((ref) => ({ ref })),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["grc"] });
      onDone();
    },
  });
  return (
    <form
      className="grid grid-cols-1 gap-3 border-y border-hair py-4 md:grid-cols-2"
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <label className="flex flex-col gap-1 md:col-span-2">
        <span className="label text-muted">{tr("governance.compliance.titolo")}</span>
        <input className="field" required value={title} onChange={(e) => setTitle(e.target.value)} placeholder={tr("governance.compliance.backup_e_prove_di_ripristino")} />
      </label>
      <label className="flex flex-col gap-1 md:col-span-2">
        <span className="label text-muted">{tr("governance.compliance.come_funziona")}</span>
        <textarea className="field min-h-[64px]" value={text} onChange={(e) => setText(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.compliance.responsabile")}</span>
        <EntitySelect kind="member" noneLabel="—" className="field" value={owner} onChange={(v) => setOwner(v)} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.compliance.frequenza_di_verifica")}</span>
        <Select className="field" value={freq} onChange={(e) => setFreq(e.target.value)}>
          {Object.entries(FREQUENCIES).map(([k, v]) => (
            <option key={k} value={k}>
              {v}
            </option>
          ))}
        </Select>
      </label>
      <label className="flex flex-col gap-1 md:col-span-2">
        <span className="label text-muted">{tr("governance.compliance.requisiti_coperti_uno_per_riga")}</span>
        <textarea className="field min-h-[72px] font-mono" value={refs} onChange={(e) => setRefs(e.target.value)} placeholder={/* i18n-ok: esempio di riferimenti */ "dora:Art. 12\nnis2:Art. 21(2)(c)\niso27001:A.8.13"} />
      </label>
      <ErrorNote error={create.error} />
      <div className="flex gap-2 md:col-span-2">
        <button className="btn btn-primary" disabled={!title.trim() || create.isPending}>{tr("governance.compliance.crea_il_controllo")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </div>
    </form>
  );
}

// ---------------------------------------------------------------------
// Evidenze

function EvidenceTab({ status }: { status?: string }) {
  const navigate = useNavigate();
  const list = useQuery({ queryKey: ["grc", "evidence", status ?? ""], queryFn: () => grcApi.evidenceList(status ?? "") });
  const items = list.data?.items ?? [];
  return (
    <section className="flex flex-col gap-3">
      <label className="flex max-w-xs flex-col gap-1">
        <span className="label text-muted">{tr("governance.compliance.stato")}</span>
        <Select className="field" value={status ?? ""} onChange={(e) => navigate({ to: "/conformita", search: { tab: "evidenze", stato: e.target.value || undefined } })}>
          <option value="">{tr("governance.compliance.tutte")}</option>
          {Object.entries(EVIDENCE_STATUS).map(([k, v]) => (
            <option key={k} value={k}>
              {v}
            </option>
          ))}
        </Select>
      </label>
      <ErrorNote error={list.error} />
      <div className="overflow-x-auto">
        <table className="w-full min-w-[640px] border-collapse text-[13px]">
          <thead>
            <tr className="border-b-2 border-rule text-left">
              <th className="label py-1.5">{tr("governance.compliance.evidenza")}</th>
              <th className="label py-1.5">{tr("governance.compliance.tipo")}</th>
              <th className="label py-1.5">{tr("governance.compliance.perimetro")}</th>
              <th className="label py-1.5">{tr("governance.compliance.controlli")}</th>
              <th className="label py-1.5">{tr("governance.compliance.stato")}</th>
              <th className="label py-1.5 text-right">{tr("governance.compliance.valida_fino_al")}</th>
            </tr>
          </thead>
          <tbody>
            {items.map((e) => (
              <tr key={e.id} className="border-b border-hair">
                <td className="py-2 pr-3">
                  <Link to="/e/$key" params={{ key: e.key }} search={{ scheda: "dettagli" }} className="no-underline">
                    <span className="font-mono text-[12px] text-accent">{e.key}</span> <span className="text-fg">{e.title}</span>
                  </Link>
                  {e.validated_by && <span className="block text-[12px] text-muted">{tr("governance.compliance.validata_da", { validated_by: e.validated_by })}</span>}
                </td>
                <td className="py-2 pr-3">{EVIDENCE_KINDS[e.kind] ?? e.kind}</td>
                <td className="py-2 pr-3">{scopeLabel(e.scope)}</td>
                <td className="py-2 pr-3 font-mono text-[12px]">{(e.controls ?? []).join(", ")}</td>
                <td className="py-2 pr-3">
                  <EvidenceMark status={e.status} />
                </td>
                <td className="py-2 text-right font-mono tabular">{e.valid_until ? date(e.valid_until) : "—"}</td>
              </tr>
            ))}
            {items.length === 0 && list.data && (
              <tr>
                <td colSpan={6} className="py-3 text-muted">
                  {tr("governance.compliance.nessuna_evidenza_aggiungi", { state: status ? tr("governance.compliance.in_questo_stato") : "" })}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------
// Cataloghi

function Catalogs() {
  const qc = useQueryClient();
  const lib = useQuery({ queryKey: ["grc", "library"], queryFn: grcApi.library });
  const refresh = () => qc.invalidateQueries({ queryKey: ["grc"] });
  const starter = useMutation({ mutationFn: (k: string) => grcApi.importStarter(k), onSuccess: refresh });
  const l = lib.data;
  if (lib.error) return <ErrorNote error={lib.error} />;
  if (!l) return <Loading className="m-0 text-muted" />;
  const pending = l.starters.filter((s) => !s.imported);
  return (
    <div className="flex flex-col gap-6">
      <p className="m-0 max-w-3xl text-[13px] text-muted">{tr("governance.compliance.le_normative_sono_dati_cataloghi")}</p>
      {l.can_edit && pending.length > 0 && (
        <section className="flex flex-col gap-2">
          <SectionTitle>{tr("governance.compliance.pacchetti_di_partenza")}</SectionTitle>
          <ul className="m-0 flex list-none flex-col p-0">
            {pending.map((s) => (
              <li key={s.key} className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-hair py-2">
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="font-semibold">{s.name}</span>
                  <span className="text-[12px] text-muted">{tr("governance.compliance.voci", { description: s.description, items: s.items })}</span>
                </span>
                <button className="btn" onClick={() => starter.mutate(s.key)} disabled={starter.isPending}>{tr("governance.compliance.importa_e_abilita")}</button>
              </li>
            ))}
          </ul>
          <ErrorNote error={starter.error} />
        </section>
      )}
      <section className="flex flex-col gap-2">
        <SectionTitle aside={<a href={grcApi.exportPackageUrl()} className="text-[12.5px]" download>{tr("governance.compliance.esporta_la_configurazione_json")}</a>}>{tr("governance.compliance.normative")}</SectionTitle>
        {l.frameworks.length === 0 && <p className="m-0 text-muted">{tr("governance.compliance.nessuna_normativa_importata")}</p>}
        {l.frameworks.map((f) => (
          <FrameworkRow key={f.id} f={f} canEdit={l.can_edit} />
        ))}
      </section>
      {l.can_edit && <ImportForm />}
      <MappingsSection frameworks={l.frameworks} canEdit={l.can_edit} />
    </div>
  );
}

const FW_STATUS: Record<string, string> = { get active() { return tr("governance.compliance.abilitata"); }, get paused() { return tr("governance.compliance.sospesa"); }, get retired() { return tr("governance.compliance.ritirata"); }, get ""() { return tr("governance.compliance.non_abilitata"); } };

function FrameworkRow({ f, canEdit }: { f: FrameworkInfo; canEdit: boolean }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [version, setVersion] = useState("");
  const refresh = () => qc.invalidateQueries({ queryKey: ["grc"] });
  const act = useMutation({
    mutationFn: async (a: "enable" | "pause" | "publish" | "discard" | "draft"): Promise<unknown> => {
      switch (a) {
        case "enable":
          return grcApi.setStatus(f.key, "active");
        case "pause":
          return grcApi.setStatus(f.key, "paused");
        case "publish":
          return grcApi.publish(f.key);
        case "discard":
          return grcApi.discardDraft(f.key);
        case "draft":
          return grcApi.newDraft(f.key, version);
      }
    },
    onSuccess: refresh,
  });
  const editable = canEdit && !f.system;
  return (
    <div className="flex flex-col gap-2 border-b border-hair py-3">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <span className="flex min-w-[220px] flex-1 flex-col">
          <span className="flex items-center gap-2">
            <FrameworkTag k={f.key} />
            <span className="font-semibold">{f.name}</span>
          </span>
          <span className="text-[12px] text-muted">
            {f.current ? tr("governance.compliance.versione_requisiti_2", { version: f.current.version, assessable: f.current.assessable }) : tr("governance.compliance.mai_pubblicata")}
            {f.draft ? tr("governance.compliance.bozza_voci", { version: f.draft.version, items: f.draft.items }) : ""} · {FW_STATUS[f.status]}
            {f.system ? tr("governance.compliance.catalogo_di_sistema") : ""}
          </span>
        </span>
        <span className="flex flex-wrap gap-2">
          {f.current && (
            <>
              <a className="btn no-underline" href={grcApi.exportUrl(f.key, "oscal")} download>
                OSCAL
              </a>
              <a className="btn no-underline" href={grcApi.exportUrl(f.key, "csv")} download>
                CSV
              </a>
              <a className="btn no-underline" href={grcApi.exportUrl(f.key, "json")} download>
                JSON
              </a>
            </>
          )}
          {canEdit && f.current && f.status !== "active" && (
            <button className="btn btn-primary" onClick={() => act.mutate("enable")} disabled={act.isPending}>{tr("governance.compliance.abilita")}</button>
          )}
          {canEdit && f.status === "active" && (
            <button className="btn" onClick={() => act.mutate("pause")} disabled={act.isPending}>{tr("governance.compliance.sospendi")}</button>
          )}
          {editable && (
            <button className="btn" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
              {open ? tr("governance.compliance.chiudi") : tr("governance.compliance.versioni")}
            </button>
          )}
        </span>
      </div>
      {f.current?.license_note && <p className="m-0 text-[12px] text-muted">{f.current.license_note}</p>}
      <ErrorNote error={act.error} />
      {open && editable && (
        <div className="flex flex-col gap-3 border-l-2 border-accent pl-3">
          {f.draft ? (
            <>
              <DraftEditor fwKey={f.key} />
              <span className="flex flex-wrap gap-2">
                <button className="btn btn-primary" onClick={() => act.mutate("publish")} disabled={act.isPending}>{tr("governance.compliance.pubblica_la_bozza", { version: f.draft.version })}</button>
                <button className="btn" onClick={() => act.mutate("discard")} disabled={act.isPending}>{tr("governance.compliance.scarta_la_bozza")}</button>
              </span>
            </>
          ) : (
            <form
              className="flex flex-wrap items-end gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                act.mutate("draft");
              }}
            >
              <label className="flex flex-col gap-1">
                <span className="label text-muted">{tr("governance.compliance.nuova_versione")}</span>
                <input className="field" required value={version} onChange={(e) => setVersion(e.target.value)} placeholder="2026" />
              </label>
              <button className="btn" disabled={!version.trim() || act.isPending}>{tr("governance.compliance.apri_la_bozza")}</button>
            </form>
          )}
          <p className="m-0 text-[12px] text-muted">{tr("governance.compliance.versioni_2")}{" "}{f.versions.map((v) => `${v.version} (${v.status === "published" ? tr("governance.pipelinepolicy.corrente") : v.status === "draft" ? tr("governance.riskapi.bozza") : tr("governance.gatepanel.superata")})`).join(", ")}
          </p>
        </div>
      )}
    </div>
  );
}

function DraftEditor({ fwKey }: { fwKey: string }) {
  const qc = useQueryClient();
  const fw = useQuery({ queryKey: ["grc", "framework", fwKey, "draft"], queryFn: () => grcApi.framework(fwKey, true) });
  const [it, setIt] = useState({ ref: "", parent: "", kind: "requirement", title: "", summary: "" });
  const save = useMutation({
    mutationFn: () => grcApi.draftItem(fwKey, it),
    onSuccess: () => {
      setIt({ ...it, ref: "", title: "", summary: "" });
      qc.invalidateQueries({ queryKey: ["grc"] });
    },
  });
  const remove = useMutation({ mutationFn: (ref: string) => grcApi.removeDraftItem(fwKey, ref), onSuccess: () => qc.invalidateQueries({ queryKey: ["grc"] }) });
  const items = fw.data?.items ?? [];
  return (
    <div className="flex flex-col gap-2">
      <ul className="m-0 flex max-h-72 list-none flex-col overflow-y-auto p-0">
        {items.map((x) => (
          <li key={x.id} className="flex items-baseline gap-2 border-b border-hair py-1 text-[13px]">
            <span className="w-28 shrink-0 font-mono text-[12px]">{x.ref}</span>
            <span className="min-w-0 flex-1 truncate">{x.title}</span>
            <span className="text-[11.5px] text-muted">{ITEM_KINDS[x.kind]}</span>
            <button className="text-[12px] text-muted hover:text-danger" onClick={() => remove.mutate(x.ref)} aria-label={tr("governance.compliance.togli", { ref: x.ref })}>{tr("governance.compliance.togli_2")}</button>
          </li>
        ))}
      </ul>
      <form
        className="grid grid-cols-2 gap-2 md:grid-cols-[120px_120px_140px_minmax(0,1fr)_auto]"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <input className="field font-mono" required placeholder={tr("governance.compliance.riferimento")} value={it.ref} onChange={(e) => setIt({ ...it, ref: e.target.value })} />
        <Select className="field" value={it.parent} onChange={(e) => setIt({ ...it, parent: e.target.value })} aria-label={tr("governance.compliance.sotto")}>
          <option value="">{tr("governance.compliance.radice")}</option>
          {items.map((x) => (
            <option key={x.id} value={x.ref}>
              {x.ref}
            </option>
          ))}
        </Select>
        <Select className="field" value={it.kind} onChange={(e) => setIt({ ...it, kind: e.target.value })} aria-label={tr("governance.compliance.tipo")}>
          {Object.entries(ITEM_KINDS).map(([k, v]) => (
            <option key={k} value={k}>
              {v}
            </option>
          ))}
        </Select>
        <input className="field col-span-2 md:col-span-1" required placeholder={tr("governance.compliance.titolo_breve")} value={it.title} onChange={(e) => setIt({ ...it, title: e.target.value })} />
        <button className="btn col-span-2 md:col-span-1" disabled={save.isPending}>{tr("governance.compliance.aggiungi")}</button>
        <textarea
          className="field col-span-2 min-h-[48px] md:col-span-5"
          placeholder={tr("governance.compliance.sintesi_scritta_da_voi_mai")}
          value={it.summary}
          onChange={(e) => setIt({ ...it, summary: e.target.value })}
        />
      </form>
      <ErrorNote error={save.error ?? remove.error} />
    </div>
  );
}

function ImportForm() {
  const qc = useQueryClient();
  const [format, setFormat] = useState<"oscal" | "csv" | "json">("oscal");
  const [content, setContent] = useState("");
  const [fileName, setFileName] = useState("");
  const [key, setKey] = useState("");
  const [name, setName] = useState("");
  const [version, setVersion] = useState("");
  const [publish, setPublish] = useState(true);
  const [enable, setEnable] = useState(false);
  const imp = useMutation({
    mutationFn: () =>
      grcApi.importCatalog({
        format,
        content,
        framework: key || name ? { key, name } : undefined,
        version: version ? { version } : undefined,
        publish,
        enable: publish && enable,
      }),
    onSuccess: () => {
      setContent("");
      setFileName("");
      qc.invalidateQueries({ queryKey: ["grc"] });
    },
  });
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{tr("governance.compliance.importa_un_catalogo")}</SectionTitle>
      <form
        className="grid grid-cols-1 gap-3 md:grid-cols-4"
        onSubmit={(e) => {
          e.preventDefault();
          imp.mutate();
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{tr("governance.compliance.formato")}</span>
          <Select className="field" value={format} onChange={(e) => setFormat(e.target.value as typeof format)}>
            <option value="oscal">OSCAL (catalog, JSON)</option> {/* i18n-ok: formato di file */}
            <option value="csv">CSV (riferimento;padre;tipo;titolo;sintesi)</option> {/* i18n-ok: le colonne del file sono fisse */}
            <option value="json">{tr("governance.compliance.json_di_regia")}</option>
          </Select>
        </label>
        <label className="flex flex-col gap-1 md:col-span-3">
          <span className="label text-muted">{tr("governance.compliance.file")}</span>
          <input
            type="file"
            className="field"
            accept=".json,.csv,.txt"
            onChange={async (e) => {
              const f = e.target.files?.[0];
              if (!f) return;
              setFileName(f.name);
              setContent(await f.text());
            }}
          />
          {fileName && <span className="text-[12px] text-muted">{fileName} · {content.length.toLocaleString(loc())}{" "}{tr("governance.compliance.caratteri")}</span>}
        </label>
        {format !== "json" && (
          <>
            <label className="flex flex-col gap-1">
              <span className="label text-muted">{tr("governance.compliance.chiave")}</span>
              <input className="field font-mono" value={key} onChange={(e) => setKey(e.target.value)} placeholder="misure_pa" pattern="[a-z][a-z0-9_]{0,62}" />
            </label>
            <label className="flex flex-col gap-1 md:col-span-2">
              <span className="label text-muted">{tr("governance.compliance.nome")}</span>
              <input className="field" value={name} onChange={(e) => setName(e.target.value)} />
            </label>
            <label className="flex flex-col gap-1">
              <span className="label text-muted">{tr("governance.compliance.versione")}</span>
              <input className="field" value={version} onChange={(e) => setVersion(e.target.value)} />
            </label>
          </>
        )}
        <div className="flex flex-wrap items-center gap-4 md:col-span-4">
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={publish} onChange={(e) => setPublish(e.target.checked)} />{" "}{tr("governance.compliance.pubblica_subito")}</label>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={enable} disabled={!publish} onChange={(e) => setEnable(e.target.checked)} />{" "}{tr("governance.compliance.abilita_per_l_organizzazione")}</label>
          <button className="btn btn-primary" disabled={!content || imp.isPending}>{tr("governance.compliance.importa")}</button>
        </div>
        <ErrorNote error={imp.error} />
        {imp.data && (
          <p className="m-0 text-ok md:col-span-4">
            {imp.data.name}: {imp.data.current ? tr("governance.compliance.versione_pubblicata", { version: imp.data.current.version }) : tr("governance.compliance.bozza_salvata")}.
          </p>
        )}
      </form>
    </section>
  );
}

function MappingsSection({ frameworks, canEdit }: { frameworks: FrameworkInfo[]; canEdit: boolean }) {
  const qc = useQueryClient();
  const [fw, setFw] = useState(frameworks[0]?.key ?? "");
  const maps = useQuery({ queryKey: ["grc", "mappings", fw], queryFn: () => grcApi.mappings(fw), enabled: !!fw });
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const add = useMutation({
    mutationFn: () => grcApi.addMapping(from, to, "related"),
    onSuccess: () => {
      setFrom("");
      setTo("");
      qc.invalidateQueries({ queryKey: ["grc", "mappings"] });
    },
  });
  const del = useMutation({ mutationFn: grcApi.deleteMapping, onSuccess: () => qc.invalidateQueries({ queryKey: ["grc", "mappings"] }) });
  if (frameworks.length === 0) return null;
  const items = maps.data?.items ?? [];
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle aside={<a href={grcApi.mappingsCsvUrl(fw)} className="text-[12.5px]" download>CSV</a>}>{tr("governance.compliance.corrispondenze_tra_normative")}</SectionTitle>
      <Select className="field max-w-xs" value={fw} onChange={(e) => setFw(e.target.value)} aria-label={tr("governance.compliance.normativa")}>
        {frameworks.map((f) => (
          <option key={f.key} value={f.key}>
            {f.name}
          </option>
        ))}
      </Select>
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((m) => (
          <li key={m.id} className="flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1.5 text-[13px]">
            <span className="font-mono text-[12px]">{m.from}</span>
            <span className="text-muted">{RELATIONS[m.relation]}</span>
            <span className="font-mono text-[12px]">{m.to}</span>
            <span className="min-w-0 flex-1 truncate text-muted">
              {m.from_title} → {m.to_title}
            </span>
            {canEdit && !m.system && (
              <button className="text-[12px] text-muted hover:text-danger" onClick={() => del.mutate(m.id)}>{tr("governance.compliance.togli_2")}</button>
            )}
          </li>
        ))}
        {items.length === 0 && maps.data && <li className="py-1.5 text-muted">{tr("governance.compliance.nessuna_corrispondenza")}</li>}
      </ul>
      {canEdit && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            add.mutate();
          }}
        >
          <input className="field font-mono" required /* i18n-ok: esempio di riferimento */ placeholder="nis2:Art. 21(2)(c)" value={from} onChange={(e) => setFrom(e.target.value)} aria-label={tr("governance.compliance.da")} />
          <input className="field font-mono" required /* i18n-ok: esempio di riferimento */ placeholder="dora:Art. 11" value={to} onChange={(e) => setTo(e.target.value)} aria-label={tr("governance.compliance.a")} />
          <button className="btn" disabled={add.isPending}>{tr("governance.compliance.aggiungi")}</button>
        </form>
      )}
      <ErrorNote error={add.error ?? del.error} />
    </section>
  );
}

// ---------------------------------------------------------------------
// Applicabilità: regole sui fatti

function RulesTab() {
  const qc = useQueryClient();
  const rules = useQuery({ queryKey: ["grc", "rules"], queryFn: grcApi.rules });
  const [draft, setDraft] = useState<RuleInput[] | null>(null);
  const recompute = useMutation({ mutationFn: grcApi.recompute, onSuccess: () => qc.invalidateQueries({ queryKey: ["grc"] }) });
  const save = useMutation({
    mutationFn: (items: RuleInput[]) => grcApi.saveRules(items),
    onSuccess: () => {
      setDraft(null);
      qc.invalidateQueries({ queryKey: ["grc"] });
    },
  });
  const r = rules.data;
  if (rules.error) return <ErrorNote error={rules.error} />;
  if (!r) return <Loading className="m-0 text-muted" />;
  const strip = (v: RuleView): RuleInput => ({
    key: v.key,
    name: v.name,
    description: v.description,
    framework: v.framework,
    refs: v.refs,
    controls: v.controls,
    scope: v.scope,
    condition: v.condition,
    active: v.active,
  });
  const items = draft ?? r.items.map(strip);
  const set = (i: number, patch: Partial<RuleInput>) => setDraft(items.map((x, j) => (j === i ? { ...x, ...patch } : x)));
  return (
    <section className="flex flex-col gap-4">
      <p className="m-0 max-w-3xl text-[13px] text-muted">
        <Trans
          k="governance.compliance.senza_regole_un_requisito_di_una"
          slots={{
            /* i18n-ok: esempio di condizione */ ex1: <span className="font-mono">funzione_critica: sì</span>,
            /* i18n-ok: esempio di condizione */ ex2: <span className="font-mono">classificazione_dati: personali criticita: alta, critica</span>,
          }}
        />
      </p>
      <div className="flex flex-wrap items-center gap-3">
        {r.can_edit && (
          <button className="btn" onClick={() => recompute.mutate()} disabled={recompute.isPending}>{tr("governance.compliance.ricalcola_ora")}</button>
        )}
        <span className="text-[12px] text-muted">{r.recomputed_at ? tr("governance.compliance.ultimo_ricalcolo", { date: date(r.recomputed_at) }) : tr("governance.compliance.mai_ricalcolata")}{" "}{tr("governance.compliance.ogni_ora_in_automatico")}</span>
      </div>
      <ErrorNote error={recompute.error ?? save.error} />
      {items.map((it, i) => (
        <RuleCard key={i} rule={it} view={draft ? undefined : r.items[i]} canEdit={r.can_edit} onChange={(p) => set(i, p)} onRemove={() => setDraft(items.filter((_, j) => j !== i))} />
      ))}
      {r.can_edit && (
        <div className="flex flex-wrap gap-2">
          <button
            className="btn"
            onClick={() => setDraft([...items, { key: `regola_${items.length + 1}`, name: tr("governance.compliance.nuova_regola"), framework: "", refs: [], scope: "application", condition: "", active: true }])}
          >{tr("governance.compliance.aggiungi_una_regola")}</button>
          {draft && (
            <>
              <button className="btn btn-primary" onClick={() => save.mutate(draft)} disabled={save.isPending}>{tr("governance.compliance.salva_e_ricalcola")}</button>
              <button className="btn" onClick={() => setDraft(null)}>{tr("governance.compliance.annulla")}</button>
            </>
          )}
        </div>
      )}
    </section>
  );
}

/** Dati dell'entità legale nelle condizioni di applicabilità (nome, codice, tipo). */
const LEGAL_ENTITY_FACTS = (): FactInfo[] => [
  { key: "nome", name: tr("casa.grc.factName"), kind: "text", source: "base" },
  { key: "codice", name: tr("casa.grc.factCode"), kind: "text", source: "base" },
  { key: "tipo", name: tr("casa.grc.factKind"), kind: "text", source: "base" },
];

function RuleCard({
  rule,
  view,
  canEdit,
  onChange,
  onRemove,
}: {
  rule: RuleInput;
  view?: RuleView;
  canEdit: boolean;
  onChange: (p: Partial<RuleInput>) => void;
  onRemove: () => void;
}) {
  // Il costruttore unico conta i perimetri che soddisfano la condizione (stessa anteprima del server).
  const countScopes = async (condition: string): Promise<CondCount> => {
    const res = await grcApi.preview(rule.scope, condition);
    const hit = res.items.filter((x) => x.matched);
    return { ok: true, matched: hit.length, total: res.items.length, items: hit.slice(0, 5).map((x) => ({ key: x.key ?? "", title: x.name })) };
  };
  return (
    <div className="flex flex-col gap-2 border-t-2 border-rule pt-3">
      <div className="grid grid-cols-1 gap-2 md:grid-cols-[160px_minmax(0,1fr)_140px]">
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{tr("governance.compliance.chiave")}</span>
          <input className="field font-mono" value={rule.key} disabled={!canEdit} onChange={(e) => onChange({ key: e.target.value })} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{tr("governance.compliance.nome")}</span>
          <input className="field" value={rule.name} disabled={!canEdit} onChange={(e) => onChange({ name: e.target.value })} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{tr("governance.compliance.perimetro")}</span>
          <Select className="field" value={rule.scope} disabled={!canEdit} onChange={(e) => onChange({ scope: e.target.value as RuleInput["scope"] })}>
            <option value="application">{tr("governance.compliance.applicazioni")}</option>
            <option value="process">{tr("governance.compliance.processi")}</option>
            <option value="legal_entity">{tr("governance.compliance.entita_legali")}</option>
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{tr("governance.compliance.normativa")}</span>
          <input className="field font-mono" value={rule.framework ?? ""} disabled={!canEdit} onChange={(e) => onChange({ framework: e.target.value })} /* i18n-ok: chiave della normativa */ placeholder="dora" />
        </label>
        <label className="flex flex-col gap-1 md:col-span-2">
          <span className="label text-muted">{tr("governance.compliance.riferimenti_vuoto_tutti_i_requisiti")}</span>
          <input
            className="field font-mono"
            value={(rule.refs ?? []).join(", ")}
            disabled={!canEdit}
            onChange={(e) =>
              onChange({
                refs: e.target.value
                  .split(",")
                  .map((s) => s.trim())
                  .filter(Boolean),
              })
            }
          />
        </label>
        <fieldset disabled={!canEdit} className="m-0 min-w-0 border-0 p-0 md:col-span-3">
          <ConditionBuilder
            label={tr("casa.grc.appliesWhen")}
            value={rule.condition ?? ""}
            onChange={(c) => onChange({ condition: c })}
            facts={rule.scope === "legal_entity" ? LEGAL_ENTITY_FACTS() : undefined}
            type={rule.scope === "application" ? "application" : rule.scope === "process" ? "business_process" : undefined}
            count={countScopes}
            noun={rule.scope === "application" ? tr("casa.grc.nounApplications") : rule.scope === "process" ? tr("casa.grc.nounProcesses") : tr("casa.grc.nounEntities")}
            empty={tr("casa.grc.appliesToAll")}
          />
        </fieldset>
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12.5px]">
        {view && (
          <span className="text-muted">
            <Trans k="governance.compliance.oggi_si_applica_a" params={{ count: view.matches }} slots={{ n: <span className="font-semibold text-fg tabular">{view.matches}</span> }} />
            {view.missing.length > 0 && <span className="text-danger">{" "}{tr("governance.compliance.riferimenti_mancanti")}{" "}{view.missing.join(", ")}</span>}
          </span>
        )}
        {canEdit && (
          <>
            <label className="flex items-center gap-1.5">
              <input type="checkbox" checked={rule.active !== false} onChange={(e) => onChange({ active: e.target.checked })} />{" "}{tr("governance.compliance.attiva")}</label>
            <button type="button" className="text-muted hover:text-danger" onClick={onRemove}>{tr("governance.compliance.togli_la_regola")}</button>
          </>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------
// Report stampabile: panoramica e, per ogni normativa, i requisiti con lo stato.

function PrintReport({ d, onDone }: { d: Dashboard; onDone: () => void }) {
  const [covs, setCovs] = useState<FrameworkCoverage[] | null>(null);
  const [root] = useState(() => {
    const el = document.createElement("div");
    el.id = "print-root";
    el.dataset.skin = document.documentElement.dataset.skin ?? "atlante";
    el.dataset.font = document.documentElement.dataset.font ?? "atkinson";
    el.dataset.theme = "light";
    return el;
  });
  useEffect(() => {
    let alive = true;
    Promise.all(d.frameworks.map((f) => grcApi.coverage(f.key)))
      .then((c) => alive && setCovs(c))
      .catch(() => alive && onDone());
    return () => {
      alive = false;
    };
  }, [d, onDone]);
  useEffect(() => {
    if (!covs) return;
    document.body.appendChild(root);
    const done = () => onDone();
    window.addEventListener("afterprint", done);
    const t = window.setTimeout(() => window.print(), 50);
    return () => {
      window.clearTimeout(t);
      window.removeEventListener("afterprint", done);
      root.remove();
    };
  }, [covs, root, onDone]);
  if (!covs) return null;
  return createPortal(
    <div className="flex flex-col gap-5">
      <header className="flex flex-col gap-1 border-b-2 border-rule pb-2">
        <span className="font-mono text-[11px] text-muted">{tr("governance.compliance.conformita_report")}</span>
        <h1 className="m-0 text-[24px] leading-tight font-bold">{tr("governance.compliance.stato_della_conformita")}</h1>
        <p className="m-0 text-[12px]">{tr("governance.compliance.al_copertura_complessiva_su_pesata", { date: date(d.generated_at), percent: d.tenant.percent, covered: d.tenant.covered, applicable: d.tenant.applicable, weighted_percent: d.tenant.weighted_percent, controls: d.controls, valid_evidence: d.valid_evidence })}</p>
      </header>
      <table className="w-full border-collapse text-[10pt]">
        <thead>
          <tr className="border-b-2 border-rule text-left">
            <th className="py-1">{tr("governance.compliance.normativa")}</th>
            <th className="py-1 text-right">{tr("governance.compliance.copertura")}</th>
            <th className="py-1 text-right">{tr("governance.compliance.senza_controllo")}</th>
            <th className="py-1 text-right">{tr("governance.compliance.controlli_senza_evidenza")}</th>
            <th className="py-1 text-right">{tr("governance.compliance.in_scadenza")}</th>
          </tr>
        </thead>
        <tbody>
          {d.frameworks.map((f) => (
            <tr key={f.key} className="border-b border-hair">
              <td className="py-1">
                {f.name} ({f.version})
              </td>
              <td className="py-1 text-right">
                {f.percent}% ({f.covered}/{f.applicable})
              </td>
              <td className="py-1 text-right">{f.without_control}</td>
              <td className="py-1 text-right">{f.controls_without_evidence}</td>
              <td className="py-1 text-right">{f.expiring_evidence}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {covs.map((c) => (
        <section key={c.framework?.key} className="flex flex-col gap-1">
          <h3 className="m-0 border-b-2 border-rule pb-1 text-[13pt] font-bold">{c.framework?.name}</h3>
          <table className="w-full border-collapse text-[9.5pt]">
            <tbody>
              {c.items.map((it) => (
                <Fragment key={it.id}>
                  <tr className="border-b border-hair">
                    <td className={`py-0.5 pr-2 font-mono ${it.assessable ? "" : "font-bold"}`}>{it.ref}</td>
                    <td className={`py-0.5 pr-2 ${it.assessable ? "" : "font-bold"}`}>{it.title}</td>
                    <td className="py-0.5 text-right">{it.assessable ? (it.status ? STATUS_TEXT[it.status] : "") : ""}</td>
                    <td className="py-0.5 text-right">{it.assessable && it.counts.applicable ? `${it.counts.covered}/${it.counts.applicable}` : ""}</td>
                  </tr>
                </Fragment>
              ))}
            </tbody>
          </table>
        </section>
      ))}
      <p className="m-0 text-[9pt]">{tr("governance.compliance.il_dettaglio_di_mappature_controlli")}</p>
    </div>,
    root,
  );
}

const STATUS_TEXT: Record<string, string> = {
  get covered() { return tr("governance.compliance.coperto"); },
  get partial() { return tr("governance.compliancesections.parziale"); },
  get no_evidence() { return tr("governance.compliance.senza_evidenza"); },
  get no_control() { return tr("governance.compliance.senza_controllo_2"); },
  get not_applicable() { return tr("governance.compliance.non_applicabile_2"); },
};
