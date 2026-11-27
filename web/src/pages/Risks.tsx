// Governance → Rischi e incidenti (piano, passo 20): cruscotto con heatmap, registro dei rischi,
// fornitori ICT con il registro delle informazioni, incidenti con le notifiche normative,
// configurazione. Ogni sezione è una scheda nella query (?tab=…).
import { EntitySelect, Select } from "../select";
import { Suspense, lazy, useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { PageHeader, useMe } from "../shell";
import { ErrorNote, SectionTitle, date, Loading } from "../ui";
import { riskApi, type Cell, type Contract, type Dashboard, type IncidentItem, type Notification, type Provider, type RiskFilter, type RiskItem } from "../riskApi";
import { Countdown, Heatmap, ScoreBadge, dateTime, fromLocalInput, toLocalInput, hasPerm, type HeatArrow } from "./riskUi";
import { useTypeNames } from "../typesApi";
import { tr } from "../i18n/core";
import { Trans } from "../i18n/Trans";
import { crumbs } from "../crumbs";
import { EmptyState } from "../emptyState";
import { GrcTabs } from "./portafoglio-ui";
import { CreateAssistant } from "./CreateAssistant";

const RiskConfig = lazy(() => import("./RiskConfig"));

export type RiskTab = "cruscotto" | "rischi" | "fornitori" | "incidenti" | "configurazione";
export const RISK_TABS: RiskTab[] = ["cruscotto", "rischi", "fornitori", "incidenti", "configurazione"];

export function RisksPage({ tab }: { tab: RiskTab }) {
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const tabs: { id: RiskTab; label: string }[] = [
    { id: "cruscotto", label: tr("governance.risks.cruscotto") },
    { id: "rischi", label: tr("governance.risks.rischi") },
    { id: "fornitori", label: tr("governance.risks.fornitori_ict") },
    { id: "incidenti", label: tr("governance.risks.incidenti") },
    { id: "configurazione", label: tr("governance.risks.configurazione") },
  ];
  return (
    <>
      <PageHeader path={crumbs("governance", "risksIncidents")} title={tr("governance.risks.rischi_e_incidenti")} />
      <GrcTabs />
      <div role="tablist" aria-label={tr("governance.risks.sezione")} className="flex gap-5 overflow-x-auto border-b border-hair px-4 md:px-7">
        {tabs.map((t) => (
          <Link
            key={t.id}
            role="tab"
            aria-selected={tab === t.id}
            to="/rischi"
            search={{ tab: t.id === "cruscotto" ? undefined : t.id }}
            className={`shrink-0 pb-2.5 no-underline ${tab === t.id ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
          >
            {t.label}
          </Link>
        ))}
      </div>
      <div className="flex min-w-0 flex-col gap-8 px-4 py-6 md:px-7">
        {tab === "cruscotto" && <DashboardTab />}
        {tab === "rischi" && <RisksTab canCreate={hasPerm(perms, "risk.admin")} />}
        {tab === "fornitori" && <ProvidersTab canEdit={hasPerm(perms, "risk.admin")} />}
        {tab === "incidenti" && <IncidentsTab canReport={hasPerm(perms, "element.write") || hasPerm(perms, "incident.manage")} />}
        {tab === "configurazione" && (
          <Suspense fallback={<Loading className="text-muted" />}>
            <RiskConfig />
          </Suspense>
        )}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------
// Cruscotto

function DashboardTab() {
  const dash = useQuery({ queryKey: ["risk", "dashboard"], queryFn: riskApi.dashboard });
  const [mode, setMode] = useState<"residual" | "inherent">("residual");
  const [cell, setCell] = useState<Cell | null>(null);
  const reg = useQuery({ queryKey: ["risk", "register", { fase: "aperti" }], queryFn: () => riskApi.risks({ fase: "aperti" }) });
  const d = dash.data;
  if (dash.error) return <ErrorNote error={dash.error} />;
  if (!d) return <Loading className="text-muted" />;
  // Rischi della cella scelta (secondo la vista inerente o residua) e frecce inerente → residuo.
  const inCell = cell
    ? (reg.data?.items ?? []).filter((r) => {
        const s = mode === "residual" ? (r.residual ?? r.inherent) : r.inherent;
        return s?.likelihood === cell.likelihood && s?.impact === cell.impact;
      })
    : [];
  const arrows: HeatArrow[] = inCell
    .filter((r) => r.inherent && r.residual && (r.inherent.likelihood !== r.residual.likelihood || r.inherent.impact !== r.residual.impact))
    .map((r) => ({ key: r.key, from: { l: r.inherent!.likelihood, i: r.inherent!.impact }, to: { l: r.residual!.likelihood, i: r.residual!.impact } }));
  return (
    <>
      <div className="grid min-w-0 grid-cols-1 gap-8 lg:grid-cols-[minmax(0,460px)_minmax(0,1fr)]">
        <section className="flex min-w-0 flex-col gap-3" aria-label={tr("governance.risks.heatmap")}>
          <SectionTitle
            aside={
              <span role="group" aria-label={tr("governance.risks.rischio")} className="flex gap-3 text-[12.5px]">
                {(["residual", "inherent"] as const).map((m) => (
                  <button
                    key={m}
                    aria-pressed={mode === m}
                    className={mode === m ? "font-semibold text-fg underline" : "text-muted hover:text-fg"}
                    onClick={() => {
                      setMode(m);
                      setCell(null);
                    }}
                  >
                    {m === "residual" ? tr("governance.risks.residuo") : tr("governance.risks.inerente")}
                  </button>
                ))}
              </span>
            }
          >{tr("governance.risks.heatmap_rischi_aperti", { count: d.open_risks })}</SectionTitle>
          {d.heatmap ? (
            <Heatmap
              matrix={d.heatmap.matrix}
              cells={mode === "residual" ? d.heatmap.residual : d.heatmap.inherent}
              selected={cell ? { l: cell.likelihood, i: cell.impact } : null}
              onSelect={setCell}
              label={mode === "residual" ? tr("governance.requirements.rischio_residuo") : tr("governance.riskdetail.rischio_inerente")}
              arrows={arrows}
            />
          ) : (
            <p className="m-0 text-muted">{tr("governance.risks.nessuna_matrice_configurata")}</p>
          )}
          {cell && (
            <div className="flex flex-col gap-1" aria-label={tr("governance.risks.rischi_della_cella", { likelihood: cell.likelihood, impact: cell.impact })}>
              <p className="m-0 text-[12.5px] text-muted">
                <span className="label mr-2">
                  {cell.likelihood} × {cell.impact}
                </span>
                {tr("governance.risks.n_rischi", { count: cell.count })}{" "}{tr("governance.risks.nella_vista")}{" "}{mode === "residual" ? tr("governance.risks.residua") : tr("governance.risks.inerente")}
                {arrows.length > 0 ? tr("governance.risks.le_frecce_vanno_dall_inerente") : "."}
              </p>
              {inCell.length > 0 ? (
                <ul className="m-0 flex list-none flex-col p-0">
                  {inCell.map((r) => (
                    <li key={r.key} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-1.5 text-[13px]">
                      <Link to="/e/$key" params={{ key: r.key }} className="w-16 shrink-0 font-mono text-[12px] text-accent no-underline hover:underline">
                        {r.key}
                      </Link>
                      <span className="min-w-[11rem] flex-1">{r.title}</span>
                      <span className="inline-flex items-center gap-1.5 text-[12px]" aria-label={tr("governance.risks.da_inerente_a_residuo")}>
                        <ScoreBadge score={r.inherent} compact />
                        <span aria-hidden="true">→</span>
                        <ScoreBadge score={r.residual} compact />
                      </span>
                      <span className="text-[12px] text-muted">{r.phase}</span>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="m-0 text-[13px]">
                  {cell.keys.map((k, i) => (
                    <span key={k}>
                      {i > 0 && ", "}
                      <Link to="/e/$key" params={{ key: k }} className="font-mono text-accent no-underline hover:underline">
                        {k}
                      </Link>
                    </span>
                  ))}
                </p>
              )}
            </div>
          )}
        </section>
        <section className="flex min-w-0 flex-col gap-3" aria-label={tr("governance.risks.rischi_oltre_la_soglia")}>
          <SectionTitle aside={<Link to="/rischi" search={{ tab: "rischi" }} className="text-[12px] text-accent">{tr("governance.risks.registro")}</Link>}>{tr("governance.risks.oltre_la_soglia_di_attenzione")}</SectionTitle>
          <RiskRows items={d.over_threshold} empty={tr("governance.risks.nessun_rischio_aperto_oltre_la")} />
          {d.overdue.length > 0 && (
            <>
              <SectionTitle>{tr("governance.risks.revisioni_e_trattamenti_scaduti")}</SectionTitle>
              <RiskRows items={d.overdue} empty="" />
            </>
          )}
        </section>
      </div>
      <div className="grid min-w-0 grid-cols-1 gap-8 lg:grid-cols-2">
        <section className="flex min-w-0 flex-col gap-3" aria-label={tr("governance.risks.incidenti_aperti")}>
          <SectionTitle aside={<Link to="/rischi" search={{ tab: "incidenti" }} className="text-[12px] text-accent">{tr("governance.risks.tutti")}</Link>}>{tr("governance.risks.incidenti_aperti_notifiche_in_scadenza")}</SectionTitle>
          <OpenNotifications items={d.open_notifications} />
          <IncidentRows items={d.open_incidents} empty={tr("governance.risks.nessun_incidente_aperto")} />
        </section>
        <section className="flex min-w-0 flex-col gap-3" aria-label={tr("governance.risks.fornitori_critici")}>
          <SectionTitle aside={<Link to="/rischi" search={{ tab: "fornitori" }} className="text-[12px] text-accent">{tr("governance.risks.fornitori", { count: d.providers })}</Link>}>{tr("governance.risks.fornitori_critici_e_contratti_in")}</SectionTitle>
          <ProviderRows items={d.critical_providers} empty={tr("governance.risks.nessun_fornitore_critico")} />
          <ExpiringContracts items={d.expiring_contracts} settings={d.settings} />
        </section>
      </div>
    </>
  );
}

function RiskRows({ items, empty }: { items: RiskItem[]; empty: string }) {
  if (items.length === 0) return empty ? <p className="m-0 text-[12.5px] text-muted">{empty}</p> : null;
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {items.map((r) => (
        <li key={r.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-1.5 text-[13px]">
          <Link to="/e/$key" params={{ key: r.key }} className="w-16 shrink-0 font-mono text-[12px] text-accent no-underline hover:underline">
            {r.key}
          </Link>
          <span className="min-w-[11rem] flex-1">{r.title}</span>
          <ScoreBadge score={r.residual ?? r.inherent} />
          <span className="text-[12px] text-muted">{r.phase}</span>
          {r.overdue && <span className="text-[12px] font-semibold text-danger">{tr("governance.risks.scaduto")}</span>}
        </li>
      ))}
    </ul>
  );
}

function OpenNotifications({ items }: { items: Notification[] }) {
  if (items.length === 0) return null;
  return (
    <ul className="m-0 flex list-none flex-col border-l-2 border-flag p-0 pl-3" aria-label={tr("governance.risks.notifiche_normative_da_inviare")}>
      {items.slice(0, 8).map((n) => (
        <li key={n.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 py-1 text-[13px]">
          <Link to="/e/$key" params={{ key: n.incident_key }} className="font-mono text-[12px] text-accent no-underline hover:underline">
            {n.incident_key}
          </Link>
          <span className="min-w-[11rem] flex-1">
            <span className="font-semibold text-flag">{n.scheme_name.split(" · ")[0]}</span> · {n.stage_name}
          </span>
          <Countdown due={n.due_at} />
        </li>
      ))}
    </ul>
  );
}

function IncidentRows({ items, empty }: { items: IncidentItem[]; empty: string }) {
  if (items.length === 0) return <p className="m-0 text-[12.5px] text-muted">{empty}</p>;
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {items.map((x) => (
        <li key={x.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-1.5 text-[13px]">
          <Link to="/e/$key" params={{ key: x.key }} className="w-16 shrink-0 font-mono text-[12px] text-accent no-underline hover:underline">
            {x.key}
          </Link>
          <span className="min-w-[11rem] flex-1">
            {x.title}
            {x.impacted.length > 0 && <span className="text-muted"> · {x.impacted.join(", ")}</span>}
          </span>
          {x.major && (
            <span className="border border-flag px-1 text-[10.5px] font-semibold text-flag" title={x.outcomes.join(", ")}>
              {x.outcomes.map((o) => o.split(" · ")[0]).join(" · ")}
            </span>
          )}
          <span className="text-[12px] text-muted">{x.phase}</span>
          {x.resolved_at && x.phase_category !== "done" && x.phase_category !== "cancelled" && (
            <span className="border border-rule px-1 text-[10.5px] font-semibold" title={tr("governance.risks.risolto_il_ma_ancora_in", { date: date(x.resolved_at), phase: x.phase })}>{tr("governance.risks.risolto_da_chiudere")}</span>
          )}
          <span className="text-[12px] text-muted tabular">{dateTime(x.detected_at)}</span>
        </li>
      ))}
    </ul>
  );
}

function ProviderRows({ items, empty }: { items: Provider[]; empty: string }) {
  if (items.length === 0) return <p className="m-0 text-[12.5px] text-muted">{empty}</p>;
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {items.map((p) => (
        <li key={p.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-1.5 text-[13px]">
          <Link to="/e/$key" params={{ key: p.key }} className="w-16 shrink-0 font-mono text-[12px] text-accent no-underline hover:underline">
            {p.key}
          </Link>
          <span className="min-w-[11rem] flex-1">
            <span className="font-medium">{p.name}</span>
            {p.critical && <span className="text-muted"> · {p.critical_reason}</span>}
          </span>
          {p.next_contract_end && <span className="text-[12px] text-muted tabular">{tr("governance.risks.fine", { date: date(p.next_contract_end) })}</span>}
        </li>
      ))}
    </ul>
  );
}

function ExpiringContracts({ items, settings }: { items: Contract[]; settings: Dashboard["settings"] }) {
  return (
    <div className="flex flex-col gap-1">
      <h4 className="label m-0 mt-2 text-muted">{tr("governance.risks.contratti_in_scadenza_preavviso_del", { contract_notice_days: settings.contract_notice_days })}</h4>
      {items.length === 0 ? (
        <p className="m-0 text-[12.5px] text-muted">{tr("governance.risks.nessun_contratto_in_scadenza")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0">
          {items.map((c) => (
            <li key={c.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-1.5 text-[13px]">
              <span className="font-mono text-[12px]">{c.reference}</span>
              <Link to="/e/$key" params={{ key: c.provider_key }} className="min-w-0 flex-1 text-fg no-underline hover:underline">
                {c.provider}
              </Link>
              <span className={`text-[12.5px] font-semibold tabular ${c.expired ? "text-danger" : "text-flag"}`}>
                {c.expired ? tr("governance.risks.scaduto_il", { date: date(c.end_date) }) : tr("governance.risks.scade_il_g", { date: date(c.end_date), days_to_end: c.days_to_end ?? "" })}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Registro dei rischi

function RisksTab({ canCreate }: { canCreate: boolean }) {
  const tn = useTypeNames();
  const [f, setF] = useState<RiskFilter>({ fase: "aperti" });
  const reg = useQuery({ queryKey: ["risk", "register", f], queryFn: () => riskApi.risks(f) });
  const [creating, setCreating] = useState(false);
  const set = (k: keyof RiskFilter, v: string | boolean) => setF((x) => ({ ...x, [k]: v || undefined }));
  const r = reg.data;
  return (
    <>
      <p className="m-0 max-w-3xl text-muted">{tr("governance.risks.il_registro_dei_rischi_ict")}</p>
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
          <span className="label">{tr("governance.risks.cerca")}</span>
          <input className="field" value={f.q ?? ""} onChange={(e) => set("q", e.target.value)} placeholder={tr("governance.risks.titolo_chiave_applicazione")} />
        </label>
        <label className="flex w-40 flex-col gap-1">
          <span className="label">{tr("governance.risks.fase")}</span>
          <Select className="field" value={f.fase ?? ""} onChange={(e) => set("fase", e.target.value)}>
            <option value="">{tr("governance.risks.tutte")}</option>
            <option value="aperti">{tr("governance.risks.aperti")}</option>
            {r?.phases.map((p) => (
              <option key={p}>{p}</option>
            ))}
          </Select>
        </label>
        <label className="flex w-36 flex-col gap-1">
          <span className="label">{tr("governance.risks.livello")}</span>
          <Select className="field" value={f.livello ?? ""} onChange={(e) => set("livello", e.target.value)}>
            <option value="">{tr("governance.risks.tutti")}</option>
            {r?.matrix?.bands.map((b) => (
              <option key={b.key} value={b.key}>
                {b.name}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex w-36 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.trattamento")}</span>
          <Select className="field" value={f.trattamento ?? ""} onChange={(e) => set("trattamento", e.target.value)}>
            <option value="">{tr("governance.risks.tutti")}</option>
            {["mitigare", "accettare", "trasferire", "evitare"].map((t) => (
              <option key={t}>{t}</option>
            ))}
          </Select>
        </label>
        <label className="flex w-40 flex-col gap-1">
          <span className="label">{tr("governance.requirements.categoria")}</span>
          <Select className="field" value={f.categoria ?? ""} onChange={(e) => set("categoria", e.target.value)}>
            <option value="">{tr("governance.risks.tutte")}</option>
            {r?.categories.map((c) => (
              <option key={c}>{c}</option>
            ))}
          </Select>
        </label>
        <label className="flex items-center gap-2 pb-2 text-[13px]">
          <input type="checkbox" checked={!!f.oltre_soglia} onChange={(e) => set("oltre_soglia", e.target.checked)} />{tr("governance.risks.oltre_la_soglia")}</label>
        <span className="ml-auto flex gap-2">
          <a className="btn no-underline" href={riskApi.risksCSV(f)} download>{tr("governance.requirements.esporta_csv")}</a>
          {canCreate && (
            <button className="btn btn-primary" onClick={() => setCreating((v) => !v)}>
              {tn.create("risk", tr("governance.risks.rischio"))}
            </button>
          )}
        </span>
      </div>
      {creating && <NewRisk onDone={() => setCreating(false)} />}
      <ErrorNote error={reg.error} />
      {r && r.items.length === 0 && (f.q || f.livello || f.trattamento || f.categoria || f.oltre_soglia || (f.fase && f.fase !== "aperti") ? (
        <p className="text-muted">{tr("governance.risks.nessun_rischio_con_questi_filtri")}</p>
      ) : (
        <EmptyState
          title={tr("lavoro.vuoti.rischi.titolo")}
          text={tr("lavoro.vuoti.rischi.testo")}
          action={canCreate ? <button className="btn btn-primary" onClick={() => setCreating(true)}>{tr("lavoro.vuoti.rischi.azione")}</button> : undefined}
        />
      ))}
      {r && r.items.length > 0 && (
        <div className="min-w-0 overflow-x-auto">
          <table className="w-full min-w-[720px] border-collapse text-[13px]">
            <thead>
              <tr className="border-b-2 border-rule text-left">
                <th className="label py-1.5 pr-3">{tr("governance.compliance.chiave")}</th>
                <th className="label px-3 py-1.5">{tr("governance.risks.rischio")}</th>
                <th className="label px-3 py-1.5">{tr("governance.riskdetail.inerente")}</th>
                <th className="label px-3 py-1.5">{tr("governance.riskdetail.residuo")}</th>
                <th className="label px-3 py-1.5">{tr("governance.riskdetail.trattamento")}</th>
                <th className="label px-3 py-1.5">{tr("governance.risks.fase")}</th>
                <th className="label py-1.5 pl-3">{tr("governance.riskdetail.titolare")}</th>
              </tr>
            </thead>
            <tbody>
              {r.items.map((it) => (
                <tr key={it.id} className="border-b border-hair align-baseline">
                  <td className="py-1.5 pr-3 font-mono text-[12px]">
                    <Link to="/e/$key" params={{ key: it.key }} className="text-accent no-underline hover:underline">
                      {it.key}
                    </Link>
                  </td>
                  <td className="px-3 py-1.5">
                    <span className="font-medium">{it.title}</span>
                    <span className="block text-[12px] text-muted">
                      {[it.category, it.targets.join(", ")].filter(Boolean).join(" · ")}
                      {it.controls > 0 && tr("governance.risks.controlli", { controls: it.controls })}
                    </span>
                  </td>
                  <td className="px-3 py-1.5">
                    <ScoreBadge score={it.inherent} matrix={r.matrix ?? undefined} compact />
                  </td>
                  <td className="px-3 py-1.5">
                    <ScoreBadge score={it.residual} matrix={r.matrix ?? undefined} />
                  </td>
                  <td className="px-3 py-1.5">
                    {it.treatment || "—"}
                    {it.treatment_due && it.treatment !== "accettare" && (
                      <span className={`block text-[12px] tabular ${it.overdue ? "font-semibold text-danger" : "text-muted"}`}>{tr("governance.riskdetail.entro_2", { date: date(it.treatment_due) })}</span>
                    )}
                    {it.acceptance !== "nessuna" && <span className="block text-[12px] text-muted">{tr("governance.risks.accettazione", { acceptance: it.acceptance })}</span>}
                  </td>
                  <td className="px-3 py-1.5 text-muted">{it.phase}</td>
                  <td className="py-1.5 pl-3">{it.owner ?? <span className="text-muted">—</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

function NewRisk({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const cfg = useQuery({ queryKey: ["risk", "config"], queryFn: riskApi.config });
  const [title, setTitle] = useState("");
  const [desc, setDesc] = useState("");
  const [category, setCategory] = useState("");
  const [matrix, setMatrix] = useState("");
  const [l, setL] = useState(0);
  const [i, setI] = useState(0);
  const [owner, setOwner] = useState("");
  const [app, setApp] = useState("");
  const m = cfg.data?.matrices.find((x) => x.key === matrix) ?? cfg.data?.matrices[0];
  const create = useMutation({
    mutationFn: () =>
      riskApi.createRisk({
        title,
        description: desc,
        category,
        matrix: m?.key,
        ...(l && i ? { inherent_likelihood: l, inherent_impact: i } : {}),
        owner_id: owner || undefined,
        targets: app ? [app] : undefined,
      }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["risk"] });
      onDone();
      navigate({ to: "/e/$key", params: { key: r.key } });
    },
  });
  return (
    <form
      className="flex flex-col gap-3 border-l-2 border-accent pl-4"
      onSubmit={(e: FormEvent) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <div className="flex flex-wrap gap-3">
        <label className="flex min-w-0 flex-[2_1_320px] flex-col gap-1">
          <span className="label">{tr("governance.risks.rischio")}</span>
          <input className="field" required value={title} onChange={(e) => setTitle(e.target.value)} placeholder={tr("governance.risks.es_indisponibilita_del_fornitore_di")} />
        </label>
        <label className="flex min-w-0 flex-[1_1_180px] flex-col gap-1">
          <span className="label">{tr("governance.requirements.categoria")}</span>
          <input className="field" value={category} onChange={(e) => setCategory(e.target.value)} placeholder={tr("governance.risks.es_terze_parti")} />
        </label>
      </div>
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.risks.descrizione")}</span>
        <textarea className="field min-h-16" value={desc} onChange={(e) => setDesc(e.target.value)} placeholder={tr("governance.risks.causa_evento_conseguenza")} />
      </label>
      <div className="flex flex-wrap gap-3">
        {(cfg.data?.matrices.length ?? 0) > 1 && (
          <label className="flex w-44 flex-col gap-1">
            <span className="label">{tr("governance.risks.matrice")}</span>
            <Select className="field" value={m?.key ?? ""} onChange={(e) => setMatrix(e.target.value)}>
              {cfg.data?.matrices.map((x) => (
                <option key={x.key} value={x.key}>
                  {x.name}
                </option>
              ))}
            </Select>
          </label>
        )}
        <label className="flex w-44 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.probabilita_inerente")}</span>
          <Select className="field" value={l} onChange={(e) => setL(Number(e.target.value))}>
            <option value={0}>{tr("governance.riskdetail.da_valutare")}</option>
            {m?.likelihood.map((x) => (
              <option key={x.value} value={x.value}>
                {x.value} · {x.name}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex w-44 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.impatto_inerente")}</span>
          <Select className="field" value={i} onChange={(e) => setI(Number(e.target.value))}>
            <option value={0}>{tr("governance.riskdetail.da_valutare")}</option>
            {m?.impact.map((x) => (
              <option key={x.value} value={x.value}>
                {x.value} · {x.name}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex w-48 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.titolare")}</span>
          <EntitySelect kind="member" noneLabel="—" className="field" value={owner} onChange={(v) => setOwner(v)} />
        </label>
        <label className="flex min-w-0 flex-[1_1_200px] flex-col gap-1">
          <span className="label">{tr("governance.risks.applicazione_esposta")}</span>
          <EntitySelect kind="application" noneLabel="—" className="field" value={app} onChange={(v) => setApp(v)} />
        </label>
      </div>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={create.isPending}>{tr("governance.risks.crea_il_rischio")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </div>
      <ErrorNote error={create.error} />
      {/* Con l'AI accesa: rischi simili già nel registro (seguito dell'ondata 3, fatto nell'ondata 5). */}
      <CreateAssistant type="risk" title={title} description={desc} />
    </form>
  );
}

// ---------------------------------------------------------------------
// Fornitori ICT e registro delle informazioni

function ProvidersTab({ canEdit }: { canEdit: boolean }) {
  const [search, setSearch] = useState("");
  const [critical, setCritical] = useState(false);
  const provs = useQuery({ queryKey: ["risk", "providers", search, critical], queryFn: () => riskApi.providers(search, critical) });
  const contracts = useQuery({ queryKey: ["risk", "contracts", "expiring"], queryFn: () => riskApi.contracts(true) });
  const [creating, setCreating] = useState(false);
  return (
    <>
      <p className="m-0 max-w-3xl text-muted">
        <Trans k="governance.risks.i_fornitori_di_servizi_ict_con" slots={{ critical: <span className="font-semibold">{tr("governance.risks.critico")}</span> }} />
      </p>
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
          <span className="label">{tr("governance.risks.cerca")}</span>
          <input className="field" value={search} onChange={(e) => setSearch(e.target.value)} placeholder={tr("governance.risks.nome_ragione_sociale_o_lei")} />
        </label>
        <label className="flex items-center gap-2 pb-2 text-[13px]">
          <input type="checkbox" checked={critical} onChange={(e) => setCritical(e.target.checked)} />{tr("governance.risks.solo_critici")}</label>
        {canEdit && (
          <button className="btn btn-primary ml-auto" onClick={() => setCreating((v) => !v)}>{tr("governance.risks.nuovo_fornitore")}</button>
        )}
      </div>
      {creating && <NewProvider onDone={() => setCreating(false)} />}
      <ErrorNote error={provs.error} />
      {provs.data && (
        <div className="min-w-0 overflow-x-auto">
          <table className="w-full min-w-[680px] border-collapse text-[13px]">
            <thead>
              <tr className="border-b-2 border-rule text-left">
                <th className="label py-1.5 pr-3">{tr("governance.compliance.chiave")}</th>
                <th className="label px-3 py-1.5">{tr("governance.riskdetail.fornitore")}</th>
                <th className="label px-3 py-1.5">{tr("governance.riskdetail.paese")}</th>
                <th className="label px-3 py-1.5">{tr("governance.compliance.applicazioni")}</th>
                <th className="label px-3 py-1.5">{tr("governance.riskdetail.contratti")}</th>
                <th className="label py-1.5 pl-3">{tr("governance.riskdetail.criticita")}</th>
              </tr>
            </thead>
            <tbody>
              {provs.data.items.length === 0 && (
                <tr>
                  <td colSpan={6} className="py-3 text-muted">{tr("governance.risks.nessun_fornitore")}</td>
                </tr>
              )}
              {provs.data.items.map((p) => (
                <tr key={p.id} className="border-b border-hair align-baseline">
                  <td className="py-1.5 pr-3 font-mono text-[12px]">
                    <Link to="/e/$key" params={{ key: p.key }} className="text-accent no-underline hover:underline">
                      {p.key}
                    </Link>
                  </td>
                  <td className="px-3 py-1.5">
                    <span className="font-medium">{p.name}</span>
                    <span className="block text-[12px] text-muted">
                      {[p.legal_name !== p.name ? p.legal_name : "", p.lei ? `LEI ${p.lei}` : "", p.is_intragroup ? "intragruppo" : ""].filter(Boolean).join(" · ")}
                    </span>
                  </td>
                  <td className="px-3 py-1.5 font-mono text-[12px]">{p.country ?? "—"}</td>
                  <td className="px-3 py-1.5">{p.applications.join(", ") || <span className="text-muted">—</span>}</td>
                  <td className="px-3 py-1.5 tabular">
                    {p.contracts}
                    {p.next_contract_end && <span className="block text-[12px] text-muted">{tr("governance.risks.fine", { date: date(p.next_contract_end) })}</span>}
                  </td>
                  <td className="py-1.5 pl-3">
                    {p.critical ? (
                      <span title={p.critical_reason} className="border border-flag px-1 text-[10.5px] font-semibold text-flag">{tr("governance.risks.critico_dora")}</span>
                    ) : (
                      <span className="text-muted">{tr("governance.risks.non_critico")}</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <section className="flex flex-col gap-2">
        <SectionTitle>{tr("governance.risks.contratti_in_scadenza")}</SectionTitle>
        {contracts.data && contracts.data.items.length === 0 && <p className="m-0 text-[12.5px] text-muted">{tr("governance.risks.nessun_contratto_in_scadenza")}</p>}
        {contracts.data?.items.map((c) => (
          <p key={c.id} className="m-0 flex flex-wrap gap-x-3 border-b border-hair py-1.5 text-[13px]">
            <span className="font-mono text-[12px]">{c.reference}</span>
            <Link to="/e/$key" params={{ key: c.provider_key }} className="flex-1 text-fg no-underline hover:underline">
              {c.provider}
            </Link>
            <span className={`font-semibold tabular ${c.expired ? "text-danger" : "text-flag"}`}>
              {c.expired ? tr("governance.risks.scaduto") : tr("governance.risks.scade")}{" "}{tr("governance.risks.il", { date: date(c.end_date) })}</span>
          </p>
        ))}
      </section>
      <RegisterExport />
    </>
  );
}

function RegisterExport() {
  const cfg = useQuery({ queryKey: ["risk", "config"], queryFn: riskApi.config });
  const [picked, setPicked] = useState<string[] | null>(null);
  const [grain, setGrain] = useState("contract");
  const [open, setOpen] = useState(false);
  const configured = cfg.data?.settings.register_columns.map((c) => c.key) ?? [];
  const cols = picked ?? configured;
  const groups = useMemo(() => {
    const m = new Map<string, { key: string; label: string }[]>();
    for (const c of cfg.data?.column_catalog ?? []) m.set(c.group, [...(m.get(c.group) ?? []), c]);
    return [...m.entries()];
  }, [cfg.data]);
  const toggle = (k: string) => setPicked((cols.includes(k) ? cols.filter((x) => x !== k) : [...cols, k]));
  const same = cols.length === configured.length && cols.every((c, i) => c === configured[i]);
  return (
    <section className="flex flex-col gap-2" aria-label={tr("governance.risks.registro_delle_informazioni")}>
      <SectionTitle
        aside={
          <button className="text-[12px] text-accent underline" onClick={() => setOpen((v) => !v)}>
            {open ? tr("governance.risks.chiudi_le_colonne") : tr("governance.risks.scegli_le_colonne")}
          </button>
        }
      >{tr("governance.risks.registro_delle_informazioni")}</SectionTitle>
      <p className="m-0 max-w-3xl text-[13px] text-muted">{tr("governance.risks.export_in_csv_ispirato_alla")}</p>
      {open && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {groups.map(([g, items]) => (
            <fieldset key={g} className="m-0 flex flex-col gap-1 border-0 p-0">
              <legend className="label mb-1">{g}</legend>
              {items.map((c) => (
                <label key={c.key} className="flex items-baseline gap-2 text-[13px]">
                  <input type="checkbox" checked={cols.includes(c.key)} onChange={() => toggle(c.key)} />
                  {c.label}
                </label>
              ))}
            </fieldset>
          ))}
        </div>
      )}
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex w-48 flex-col gap-1">
          <span className="label">{tr("governance.risks.una_riga_per")}</span>
          <Select className="field" value={grain} onChange={(e) => setGrain(e.target.value)}>
            <option value="contract">{tr("governance.risks.contratto")}</option>
            <option value="provider">{tr("governance.risks.fornitore")}</option>
          </Select>
        </label>
        <a className="btn btn-primary no-underline" href={riskApi.registerCSV(same ? [] : cols, grain)} download>{tr("governance.risks.esporta_il_registro_colonne", { length: cols.length })}</a>
      </div>
    </section>
  );
}

function NewProvider({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const apps = useQuery({ queryKey: ["applications"], queryFn: () => api.applications() });
  const [f, setF] = useState({ name: "", legal_name: "", lei: "", country: "IT", provider_type: "", substitutability: "", app: "" });
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF((x) => ({ ...x, [k]: e.target.value }));
  const create = useMutation({
    mutationFn: () =>
      riskApi.createProvider({
        name: f.name,
        legal_name: f.legal_name || undefined,
        lei: f.lei || undefined,
        country: f.country || undefined,
        provider_type: f.provider_type || undefined,
        substitutability: f.substitutability || undefined,
        applications: f.app ? [f.app] : undefined,
      }),
    onSuccess: (p) => {
      qc.invalidateQueries({ queryKey: ["risk"] });
      onDone();
      navigate({ to: "/e/$key", params: { key: p.key } });
    },
  });
  return (
    <form
      className="flex flex-col gap-3 border-l-2 border-accent pl-4"
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <div className="flex flex-wrap gap-3">
        <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
          <span className="label">{tr("governance.compliance.nome")}</span>
          <input className="field" required value={f.name} onChange={set("name")} />
        </label>
        <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.ragione_sociale")}</span>
          <input className="field" value={f.legal_name} onChange={set("legal_name")} />
        </label>
        <label className="flex w-56 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.codice_lei")}</span>
          <input className="field font-mono" value={f.lei} onChange={set("lei")} maxLength={20} />
        </label>
        <label className="flex w-20 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.paese")}</span>
          <input className="field font-mono uppercase" value={f.country} onChange={set("country")} maxLength={2} />
        </label>
      </div>
      <div className="flex flex-wrap gap-3">
        <label className="flex min-w-0 flex-[1_1_200px] flex-col gap-1">
          <span className="label">{tr("governance.compliance.tipo")}</span>
          <input className="field" value={f.provider_type} onChange={set("provider_type")} placeholder={tr("governance.risks.es_servizi_cloud")} />
        </label>
        <label className="flex w-40 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.sostituibilita")}</span>
          <Select className="field" value={f.substitutability} onChange={set("substitutability")}>
            <option value="">—</option>
            {["facile", "media", "difficile", "impossibile"].map((s) => (
              <option key={s}>{s}</option>
            ))}
          </Select>
        </label>
        <label className="flex min-w-0 flex-[1_1_200px] flex-col gap-1">
          <span className="label">{tr("governance.risks.applicazione_servita")}</span>
          <Select className="field" value={f.app} onChange={set("app")}>
            <option value="">—</option>
            {apps.data?.items.map((x) => (
              <option key={x.id} value={x.id}>
                {x.name}
                {x.supports_critical_function ? tr("governance.risks.funzione_critica") : ""}
              </option>
            ))}
          </Select>
        </label>
      </div>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={create.isPending}>{tr("governance.risks.crea_il_fornitore")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </div>
      <ErrorNote error={create.error} />
    </form>
  );
}

// ---------------------------------------------------------------------
// Incidenti

function IncidentsTab({ canReport }: { canReport: boolean }) {
  const [open, setOpen] = useState(true);
  const [major, setMajor] = useState(false);
  const [search, setSearch] = useState("");
  const list = useQuery({ queryKey: ["risk", "incidents", open, major, search], queryFn: () => riskApi.incidents({ aperti: open, gravi: major, q: search }) });
  const notices = useQuery({ queryKey: ["risk", "dashboard"], queryFn: riskApi.dashboard });
  const [creating, setCreating] = useState(false);
  return (
    <>
      <p className="m-0 max-w-3xl text-muted">
        <Trans k="governance.risks.ogni_incidente_si_classifica_con_gli" slots={{ orange: <span className="font-semibold text-flag">{tr("governance.risks.arancione")}</span> }} />
      </p>
      {notices.data && <OpenNotifications items={notices.data.open_notifications} />}
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
          <span className="label">{tr("governance.risks.cerca")}</span>
          <input className="field" value={search} onChange={(e) => setSearch(e.target.value)} />
        </label>
        <label className="flex items-center gap-2 pb-2 text-[13px]">
          <input type="checkbox" checked={open} onChange={(e) => setOpen(e.target.checked)} />{tr("governance.risks.solo_aperti")}</label>
        <label className="flex items-center gap-2 pb-2 text-[13px]">
          <input type="checkbox" checked={major} onChange={(e) => setMajor(e.target.checked)} />{tr("governance.risks.solo_gravi_o_significativi")}</label>
        {canReport && (
          <button className="btn btn-primary ml-auto" onClick={() => setCreating((v) => !v)}>{tr("governance.risks.segnala_un_incidente")}</button>
        )}
      </div>
      {creating && <NewIncident onDone={() => setCreating(false)} />}
      <ErrorNote error={list.error} />
      {list.data && <IncidentRows items={list.data.items} empty={tr("governance.risks.nessun_incidente")} />}
    </>
  );
}

function NewIncident({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const apps = useQuery({ queryKey: ["applications"], queryFn: () => api.applications() });
  const [title, setTitle] = useState("");
  const [desc, setDesc] = useState("");
  const [detected, setDetected] = useState(() => toLocalInput(new Date().toISOString()));
  const [picked, setPicked] = useState<string[]>([]);
  const create = useMutation({
    mutationFn: () => riskApi.createIncident({ title, description: desc, detected_at: fromLocalInput(detected), impacted: picked }),
    onSuccess: (x) => {
      qc.invalidateQueries({ queryKey: ["risk"] });
      onDone();
      navigate({ to: "/e/$key", params: { key: x.key } });
    },
  });
  return (
    <form
      className="flex flex-col gap-3 border-l-2 border-accent pl-4"
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <div className="flex flex-wrap gap-3">
        <label className="flex min-w-0 flex-[2_1_320px] flex-col gap-1">
          <span className="label">{tr("governance.risks.cosa_succede")}</span>
          <input className="field" required value={title} onChange={(e) => setTitle(e.target.value)} placeholder={tr("governance.risks.es_pagamenti_online_non_disponibili")} />
        </label>
        <label className="flex w-56 flex-col gap-1">
          <span className="label">{tr("governance.riskdetail.rilevato_il")}</span>
          <input className="field" type="datetime-local" required value={detected} onChange={(e) => setDetected(e.target.value)} />
        </label>
      </div>
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.risks.descrizione")}</span>
        <textarea className="field min-h-16" value={desc} onChange={(e) => setDesc(e.target.value)} />
      </label>
      <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
        <legend className="label mb-1">{tr("governance.risks.applicazioni_impattate")}</legend>
        <div className="flex flex-wrap gap-x-4 gap-y-1">
          {apps.data?.items.map((a) => (
            <label key={a.id} className="flex items-baseline gap-1.5 text-[13px]">
              <input
                type="checkbox"
                checked={picked.includes(a.id)}
                onChange={() => setPicked((p) => (p.includes(a.id) ? p.filter((x) => x !== a.id) : [...p, a.id]))}
              />
              {a.name}
              {a.supports_critical_function && <span className="text-[10.5px] font-semibold text-flag">DORA</span>}
            </label>
          ))}
        </div>
      </fieldset>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={create.isPending}>{tr("governance.risks.registra_l_incidente")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </div>
      <ErrorNote error={create.error} />
      {/* Con l'AI accesa: incidenti simili già aperti (seguito dell'ondata 3, fatto nell'ondata 5). */}
      <CreateAssistant type="incident" title={title} description={desc} />
    </form>
  );
}
