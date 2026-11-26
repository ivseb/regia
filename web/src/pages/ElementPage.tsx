// Pagina di dettaglio completa di un elemento (/e/CHIAVE, alias /richieste/CHIAVE), valida
// per tutti i tipi. Desktop: intestazione con titolo modificabile, fase e passaggi; colonna
// principale con descrizione e schede; colonna laterale stretta con fase, campi, persone,
// date, collegamenti e osservatori. Telefono: una colonna, i campi in alto e le schede in una
// barra scorrevole. Riusa le sezioni del dettaglio (ElementSections, Collaboration, ecc.).
import { Suspense, lazy, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api, collab, type ElementDetail } from "../api";
import { fieldsApi, type LinkedElement } from "../fieldsApi";
import { CustomFields, useCanWrite, useSaveField } from "../fields";
import { Markdown } from "../markdown";
import { ElementTitle, ErrorNote, SectionTitle, StageGate, date, euro, Loading } from "../ui";
import { CatalogId } from "../catalogId";
import { ImpactFacts, TechnicalAnalysis } from "./Analysis";
import { Attachments, CommentComposer, CommentItem, FollowButton, useScrollToHash } from "./Collaboration";
import { ApplicationFacts, Approvals, DemandFacts, People, Transitions, awaitsMyDecision, describe, fieldNamesOf, useRefreshElement } from "./ElementSections";
import { useMe } from "../shell";
import { Evaluation } from "./Evaluation";
import { IncidentBand } from "./IncidentBand";
import { RequestFinance } from "./RequestFinance";
import { useNarrow } from "./timeline";
import { WorkSidebar } from "./WorkSections";
// Sintesi di scheda (proposta 2): richiesta «decisione prima» e applicazione 360.
import { Application360, RequestSummaryTab } from "./Scheda";
import { PercorsoPanel } from "./Percorso";
// Knowledge (piano, passo 19): documenti collegati; le pagine si aprono nella vista dei documenti.
import { LinkedDocuments } from "./KnowledgeSections";
import { Navigate } from "@tanstack/react-router";
import { ago } from "../ui";
import { useT } from "../i18n/react";
import { fmtDateTime } from "../i18n/format";
import { useTypeNames } from "../typesApi";
import { FaseChip } from "../faseChip";
import { TypeMark } from "./work-ui";

type T = ReturnType<typeof useT>;

const ReviewsSection = lazy(() => import("./ReviewsSection"));
const CodiceSection = lazy(() => import("./CodiceSection"));
// Status report della richiesta (piano, passo 13): scheda «Stato», caricata alla prima apertura.
const StatusReportsTab = lazy(() => import("./StatusReports").then((m) => ({ default: m.StatusReportsTab })));

// Requisiti, verifica e deroghe (piano, passi 5 e 6): si caricano alla prima apertura.
const RequirementsSection = lazy(() => import("./Requirements"));
const RequirementDetail = lazy(() => import("./Requirements").then((m) => ({ default: m.RequirementDetail })));
const ExceptionDetail = lazy(() => import("./Requirements").then((m) => ({ default: m.ExceptionDetail })));
// Architettura enterprise (piano, passo 20): capability, dipendenze, TIME e tecnologie.
const EaSection = lazy(() => import("./ea/EaSection").then((m) => ({ default: m.EaSection })));
// Rischi, incidenti e fornitori ICT (piano, passo 20).
const GrcDetail = lazy(() => import("./RiskDetail"));
const GRC_TYPES = ["risk", "incident", "ict_provider"];

// DevSecOps (piano, passo 21): scheda «Produzione» dell'asset e «Servizi e rilasci» dell'applicazione.
const ProductionPanel = lazy(() => import("./Devsecops").then((m) => ({ default: m.ProductionPanel })));
// Gate di rilascio (fase 4): scheda dell'applicazione e della richiesta.
const GatePanel = lazy(() => import("./GatePanel").then((m) => ({ default: m.GatePanel })));
// Metriche di consegna DORA (fase 5a): riquadro sopra il pannello dell'applicazione e dell'asset.
const DoraCard = lazy(() => import("./Dora").then((m) => ({ default: m.DoraCard })));

// Conformità (piano, passo 20): sezioni del controllo, dell'evidenza, dell'applicazione e del requisito.
const ControlSection = lazy(() => import("./ComplianceSections").then((m) => ({ default: m.ControlSection })));
const EvidenceSection = lazy(() => import("./ComplianceSections").then((m) => ({ default: m.EvidenceSection })));
const ApplicationCompliance = lazy(() => import("./ComplianceSections").then((m) => ({ default: m.ApplicationCompliance })));
const RequirementLinks = lazy(() => import("./ComplianceSections").then((m) => ({ default: m.RequirementLinks })));

export type TabKey =
  | "sintesi" | "panoramica" | "attivita" | "dettagli" | "analisi" | "valutazioni" | "requisiti" | "piano" | "wp" | "milestone"
  | "stato" | "approvazioni" | "allegati" | "produzione" | "gate";
export const TAB_KEYS: TabKey[] = [
  "sintesi", "panoramica", "attivita", "dettagli", "analisi", "valutazioni", "requisiti", "piano", "wp", "milestone",
  "stato", "approvazioni", "allegati", "produzione", "gate",
];

// Progetti (replica/progetti.md, passi 1 e 2): Panoramica del PM, Piano, Work package e Milestone del
// progetto; «Dettagli» delle voci di piano; il progetto della richiesta nella scheda «Piano».
const ProjectOverview = lazy(() => import("./progetti/ProjectOverview").then((m) => ({ default: m.ProjectOverview })));
const ProjectPlanTab = lazy(() => import("./progetti/ProjectPlan").then((m) => ({ default: m.ProjectPlanTab })));
const WorkPackagesTab = lazy(() => import("./progetti/ProjectTables").then((m) => ({ default: m.WorkPackagesTab })));
const MilestonesTab = lazy(() => import("./progetti/ProjectTables").then((m) => ({ default: m.MilestonesTab })));
const PlanItemPanel = lazy(() => import("./progetti/PlanItemPanel").then((m) => ({ default: m.PlanItemPanel })));
const RequestProjectBanner = lazy(() => import("./progetti/RequestProject").then((m) => ({ default: m.RequestProjectBanner })));
const PLAN_ITEM_TYPES = ["work_package", "plan_activity", "milestone"];

type Tab = { key: TabKey; label: string; count?: number };

/** Schede presenti per tipo di elemento. */
function tabsFor(t: T, d: ElementDetail): Tab[] {
  const ty = d.element.type;
  const tabs: Tab[] = [];
  // La sintesi apre la richiesta («decisione prima») e l'applicazione (vista 360).
  if (ty === "business_request" || ty === "application") tabs.push({ key: "sintesi", label: t("scheda.tab") });
  // Il progetto si apre sulla Panoramica del PM; Piano, Work package e Milestone vengono dopo l'attività.
  if (ty === "project") tabs.push({ key: "panoramica", label: t("progetti.tab.overview") });
  if (PLAN_ITEM_TYPES.includes(ty)) tabs.push({ key: "dettagli", label: t("lavoro.elementpage.tabDetails") });
  tabs.push({ key: "attivita", label: t("lavoro.elementpage.tabActivity") });
  if (ty === "business_request" || d.impact || d.application || ty === "review" || ty === "requirement" || ty === "exception")
    tabs.push({ key: "dettagli", label: ty === "requirement" ? t("lavoro.elementpage.tabVerification") : t("lavoro.elementpage.tabDetails") });
  if (GRC_TYPES.includes(ty) && !tabs.some((x) => x.key === "dettagli")) tabs.push({ key: "dettagli", label: t("lavoro.elementpage.tabDetails") });
  // Conformità (piano, passo 20): controlli ed evidenze hanno la loro scheda di dettaglio.
  if (ty === "control" || ty === "evidence") tabs.push({ key: "dettagli", label: ty === "control" ? t("lavoro.elementpage.tabCompliance") : t("lavoro.elementpage.tabEvidence") });
  if (ty === "business_request") tabs.push({ key: "analisi", label: t("lavoro.elementpage.tabAnalysis") });
  if (ty === "business_request" || ty === "impact") tabs.push({ key: "valutazioni", label: t("lavoro.elementpage.tabReviews") });
  if (ty === "business_request" || ty === "impact") tabs.push({ key: "requisiti", label: t("lavoro.elementpage.tabRequirements") });
  if (ty === "business_request" || ty === "project") tabs.push({ key: "piano", label: t("lavoro.elementpage.tabPlan") });
  if (ty === "project") tabs.push({ key: "wp", label: t("progetti.tab.workPackages") }, { key: "milestone", label: t("progetti.tab.milestones") });
  if (ty === "business_request") tabs.push({ key: "stato", label: t("lavoro.elementpage.tabStatus") });
  if (ty === "logical_asset") tabs.push({ key: "produzione", label: t("lavoro.elementpage.tabProduction") });
  if (ty === "application") tabs.push({ key: "produzione", label: t("lavoro.elementpage.tabServices") });
  if (ty === "application" || ty === "business_request") tabs.push({ key: "gate", label: t("lavoro.elementpage.tabGate") });
  if (ty === "business_request" || d.approvals.length > 0) tabs.push({ key: "approvazioni", label: t("lavoro.elementpage.tabApprovals"), count: d.approvals.length });
  tabs.push({ key: "allegati", label: t("lavoro.elementpage.tabAttachments") });
  return tabs;
}

/**
 * Scheda con cui si apre il dettaglio quando il link non ne indica una: la decisione in attesa per chi
 * deve decidere; poi «Dettagli» per gli incidenti e «Conformità» per i controlli; poi la scheda utile per il ruolo
 * che ha sull'elemento; altrimenti la sintesi (richiesta e applicazione) o l'attività (con i commenti).
 * Un link a #commenti (notifiche, menzioni) apre l'attività, dove stanno i commenti.
 */
function defaultTab(d: ElementDetail, tabs: Tab[], memberId?: string): TabKey {
  const has = (k: TabKey) => tabs.some((t) => t.key === k);
  if (window.location.hash === "#commenti") return "attivita";
  if (awaitsMyDecision(d) && has("approvazioni")) return "approvazioni";
  if ((d.element.type === "incident" || d.element.type === "control") && has("dettagli")) return "dettagli";
  const mine = new Set(d.stakeholders.filter((s) => memberId && s.member_id === memberId).map((s) => s.role));
  if (mine.size > 0 && [...mine].every((r) => r === "requester" || r === "business_owner") && has("stato")) return "stato";
  if (mine.has("compliance") && has("gate")) return "gate";
  if (["reviewer", "review_lead", "review_owner", "review_compiler", "compliance"].some((r) => mine.has(r)) && has("requisiti")) return "requisiti";
  if (has("panoramica")) return "panoramica";
  if (PLAN_ITEM_TYPES.includes(d.element.type) && has("dettagli")) return "dettagli";
  return has("sintesi") ? "sintesi" : "attivita";
}

export function ElementPage({ refKey, tab, onTab }: { refKey: string; tab?: TabKey; onTab: (t: TabKey) => void }) {
  const t = useT();
  const detail = useQuery({ queryKey: ["element", refKey], queryFn: () => api.element(refKey) });
  const d = detail.data;
  const narrow = useNarrow();
  const me = useMe();
  useEffect(() => {
    if (d) document.title = `${d.element.key} ${d.element.title} · Regia`;
    return () => {
      document.title = "Regia";
    };
  }, [d]);
  if (detail.isPending) return <Loading className="px-4 py-6 text-muted md:px-7" />;
  if (!d) return <div className="px-4 py-6 md:px-7"><ErrorNote error={detail.error} /></div>;
  if (d.element.type === "page" && d.element.key) return <Navigate to="/documenti/$key" params={{ key: d.element.key }} replace />;
  const tabs = tabsFor(t, d);
  const active = tabs.find((x) => x.key === tab)?.key ?? defaultTab(d, tabs, me.data?.active_tenant?.member_id);
  return (
    <article className="flex min-w-0 flex-1 flex-col" aria-label={`${d.element.key} ${d.element.title}`}>
      <Header detail={d} active={active} onTab={onTab} />
      {d.element.type === "incident" && <IncidentBand refKey={d.element.key || d.element.id} detail={d} onOpen={() => onTab("dettagli")} />}
      <div className="grid min-w-0 flex-1 grid-cols-1 border-t border-rule lg:grid-cols-[minmax(0,1fr)_340px]">
        <aside
          aria-label={t("lavoro.elementpage.sidebarLabel")}
          className="order-2 flex min-w-0 flex-col gap-5 border-t border-hair px-4 py-5 md:px-7 lg:order-none lg:col-start-2 lg:row-start-1 lg:border-t-0 lg:border-l lg:px-5"
        >
          <Sidebar detail={d} narrow={narrow} />
        </aside>
        <div className="order-1 flex min-w-0 flex-col gap-5 px-4 py-5 md:px-7 lg:order-none lg:col-start-1 lg:row-start-1">
          {(d.element.type === "business_request" || d.element.type === "impact") && <PercorsoPanel refKey={d.element.key || d.element.id} onTab={onTab} compact={active === "approvazioni"} />}
          {/* Con la sintesi in primo piano la descrizione sta sotto: prima ciò che serve per decidere. */}
          {active !== "sintesi" && <Description detail={d} />}
          <Tabs tabs={tabs} active={active} onTab={onTab} />
          <div role="tabpanel" id={`scheda-${active}`} aria-labelledby={`tab-${active}`} className="flex min-w-0 flex-col gap-6">
            <TabBody detail={d} tab={active} onTab={onTab} />
          </div>
          {active === "sintesi" && <Description detail={d} />}
        </div>
      </div>
    </article>
  );
}

// ---------------------------------------------------------------------
// Intestazione

function Header({ detail, active, onTab }: { detail: ElementDetail; active: TabKey; onTab: (t: TabKey) => void }) {
  const t = useT();
  const tn = useTypeNames();
  const el = detail.element;
  const key = el.key || el.id;
  const refresh = useRefreshElement(key);
  // La richiesta di approvazione in cui la persona deve decidere (se c'è): la riga in testata la porta lì.
  const waiting = detail.approvals.find((r) => r.status === "pending" && r.steps.some((s) => s.can_decide));
  return (
    <header className="flex flex-col gap-3 px-4 pt-5 pb-4 md:px-7 md:pt-6">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <span className="inline-flex items-center gap-1.5 font-mono text-[11.5px] text-muted">
          <TypeMark type={el.type} />
          {el.type === "business_request" ? (
            <Link to="/richieste" className="text-muted no-underline hover:text-fg">DEMAND</Link>
          ) : (
            tn.name(el.type, el.type_name).toUpperCase()
          )}{" "}
          / <span className="font-medium text-accent">{detail.application?.code ? <CatalogId code={detail.application.code} itemKey={el.key} /> : el.key || t("lavoro.elementpage.draft")}</span>
          {el.type === "business_request" && <span> · {tn.name(el.type, el.type_name)}</span>}
        </span>
        <span className="ml-auto flex items-center gap-2">
          <FollowButton refKey={key} />
          <MoreMenu detail={detail} />
        </span>
      </div>
      <Title detail={detail} />
      {detail.demand?.is_mandatory && (
        <span className="self-start border border-flag px-1.5 py-px text-[12px] font-semibold text-flag">
          {detail.demand.regulatory_deadline ? t("lavoro.elementpage.mandatoryDeadline", { date: date(detail.demand.regulatory_deadline) }) : t("lavoro.elementpage.mandatory")}
        </span>
      )}
      {waiting && (
        <div role="status" className="flex max-w-3xl flex-wrap items-center gap-x-4 gap-y-2 border-l-[3px] border-accent bg-sel/40 px-3 py-2">
          <span className="flex min-w-0 flex-col">
            <span className="font-semibold">{t("lavoro.elementpage.awaitingDecision")}</span>
            <span className="text-[12.5px] text-muted">
              {t("lavoro.elementpage.stage", { stage: waiting.stage })}
              {waiting.amount_cents != null && ` · ${euro(waiting.amount_cents)}`}
            </span>
          </span>
          {active !== "approvazioni" && (
            <button type="button" className="btn btn-primary touch-44 ml-auto" onClick={() => onTab("approvazioni")}>
              {t("lavoro.elementpage.goToDecision")}
            </button>
          )}
        </div>
      )}
      {(detail.phases ?? []).length > 0 && (
        <div className="flex max-w-3xl flex-col gap-1.5">
          <StageGate states={detail.phases} current={el.state} size="lg" names />
          <p className="m-0 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px] text-muted">
            <span>
              {t("lavoro.elementpage.currentStage")} <FaseChip states={detail.phases} current={el.state} bare />
            </span>
            {detail.process && (
              <span>
                {t("lavoro.elementpage.process")} {detail.process.name} <span className="font-mono tabular">v{detail.process.version}</span>
              </span>
            )}
          </p>
        </div>
      )}
      <Transitions detail={detail} onDone={refresh} />
    </header>
  );
}

function Title({ detail }: { detail: ElementDetail }) {
  const t = useT();
  const canEdit = useCanWrite(detail);
  const save = useSaveField(detail);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(detail.element.title);
  const cls = "m-0 text-[26px] leading-[1.1] font-bold tracking-[-0.02em] md:text-[32px]";
  if (!editing) {
    return (
      <h1 className={cls}>
        {canEdit ? (
          <button
            type="button"
            className="-mx-1 px-1 text-left hover:bg-sel/60"
            title={t("lavoro.elementpage.editTitle")}
            onClick={() => {
              setDraft(detail.element.title);
              save.reset();
              setEditing(true);
            }}
          >
            <ElementTitle type={detail.element.type} title={detail.element.title} />
          </button>
        ) : (
          <ElementTitle type={detail.element.type} title={detail.element.title} />
        )}
      </h1>
    );
  }
  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate({ title: draft }, { onSuccess: () => setEditing(false) });
      }}
      onKeyDown={(e) => e.key === "Escape" && setEditing(false)}
    >
      <label className="sr-only" htmlFor="titolo">{t("lavoro.elementpage.title")}</label>
      <input id="titolo" className={`field ${cls} md:text-[28px]`} autoFocus required maxLength={500} value={draft} onChange={(e) => setDraft(e.target.value)} />
      <span className="flex gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>{t("lavoro.elementpage.save")}</button>
        <button type="button" className="btn" onClick={() => setEditing(false)}>{t("lavoro.elementpage.cancel")}</button>
      </span>
      <ErrorNote error={save.error} />
    </form>
  );
}

/** Menu «Altro»: link, vista nella lista, piano, questionario, richiesta di origine. */
function MoreMenu({ detail }: { detail: ElementDetail }) {
  const t = useT();
  const el = detail.element;
  const ref = useRef<HTMLDetailsElement>(null);
  const [copied, setCopied] = useState(false);
  const close = () => ref.current?.removeAttribute("open");
  const item = "block px-3 py-2 text-left text-fg no-underline hover:bg-sel/60";
  return (
    <details ref={ref} className="relative">
      <summary className="btn cursor-pointer list-none py-1 text-[12.5px] [&::-webkit-details-marker]:hidden" aria-label={t("lavoro.elementpage.moreLabel")}>
        {t("lavoro.elementpage.more")}
      </summary>
      <div className="absolute right-0 z-30 mt-1 flex w-[240px] flex-col border border-rule bg-bg py-1 text-[13px]" role="menu">
        <button
          type="button"
          role="menuitem"
          className={item}
          onClick={() => {
            void navigator.clipboard?.writeText(window.location.origin + `/e/${el.key || el.id}`).then(() => setCopied(true));
            setTimeout(() => {
              setCopied(false);
              close();
            }, 900);
          }}
        >
          {copied ? t("lavoro.elementpage.linkCopied") : t("lavoro.elementpage.copyLink")}
        </button>
        {el.type === "business_request" && (
          <>
            <Link role="menuitem" to="/richieste" search={{ sel: el.key }} className={item} onClick={close}>
              {t("lavoro.elementpage.showInList")}
            </Link>
            <Link role="menuitem" to="/richieste/$key/piano" params={{ key: el.key }} className={item} onClick={close}>
              {t("lavoro.elementpage.planGantt")}
            </Link>
          </>
        )}
        {el.type === "review" && (
          <Link role="menuitem" to="/valutazioni/$key" params={{ key: el.key }} className={item} onClick={close}>
            {t("lavoro.elementpage.openQuestionnaire")}
          </Link>
        )}
        {detail.impact && (
          <Link role="menuitem" to="/e/$key" params={{ key: detail.impact.request_key }} className={item} onClick={close}>
            {t("lavoro.elementpage.requestKey", { key: detail.impact.request_key })}
          </Link>
        )}
      </div>
    </details>
  );
}

// ---------------------------------------------------------------------
// Descrizione

function Description({ detail }: { detail: ElementDetail }) {
  const t = useT();
  const canEdit = useCanWrite(detail);
  const save = useSaveField(detail);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const body = detail.element.body_text;
  return (
    <section className="flex flex-col gap-2" aria-label={t("lavoro.elementpage.description")}>
      <SectionTitle
        aside={
          canEdit && !editing ? (
            <button
              className="text-[12px] text-accent underline"
              onClick={() => {
                setDraft(body);
                save.reset();
                setEditing(true);
              }}
            >
              {body ? t("lavoro.elementpage.edit") : t("lavoro.elementpage.add")}
            </button>
          ) : undefined
        }
      >
        {t("lavoro.elementpage.description")}
      </SectionTitle>
      {editing ? (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate({ body_text: draft }, { onSuccess: () => setEditing(false) });
          }}
        >
          <label className="sr-only" htmlFor="descrizione">{t("lavoro.elementpage.description")}</label>
          <textarea id="descrizione" className="field min-h-40 leading-relaxed" autoFocus value={draft} onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => e.key === "Escape" && setEditing(false)} />
          <span className="flex flex-wrap items-center gap-2">
            <button className="btn btn-primary" disabled={save.isPending}>{t("lavoro.elementpage.save")}</button>
            <button type="button" className="btn" onClick={() => setEditing(false)}>{t("lavoro.elementpage.cancel")}</button>
            <span className="text-[12px] text-muted">{t("lavoro.elementpage.markdownHelp")}</span>
          </span>
          <ErrorNote error={save.error} />
        </form>
      ) : body ? (
        <div className="max-w-3xl leading-relaxed">
          <Markdown text={body} />
        </div>
      ) : (
        <p className="m-0 text-muted">{t("lavoro.elementpage.noDescription")}</p>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Schede

function Tabs({ tabs, active, onTab }: { tabs: Tab[]; active: TabKey; onTab: (t: TabKey) => void }) {
  const t = useT();
  const bar = useRef<HTMLDivElement>(null);
  // Sul telefono la barra scorre: la scheda aperta (per esempio «Approvazioni») deve essere visibile.
  useEffect(() => {
    const b = bar.current;
    const on = b?.querySelector<HTMLElement>('[aria-selected="true"]');
    if (b && on) b.scrollLeft = Math.max(0, on.offsetLeft - (b.clientWidth - on.offsetWidth) / 2);
  }, [active]);
  return (
    <div ref={bar} role="tablist" aria-label={t("lavoro.elementpage.tabs")} className="relative -mx-4 flex gap-5 overflow-x-auto border-b border-hair px-4 md:mx-0 md:px-0">
      {tabs.map((tb) => {
        const on = tb.key === active;
        return (
          <button
            key={tb.key}
            id={`tab-${tb.key}`}
            role="tab"
            aria-selected={on}
            aria-controls={`scheda-${tb.key}`}
            className={`shrink-0 pb-2.5 whitespace-nowrap ${on ? "border-b-2 border-fg font-semibold" : "text-muted hover:text-fg"}`}
            onClick={() => onTab(tb.key)}
          >
            {tb.label}
            {!!tb.count && <span className="ml-1.5 font-mono text-[11px] tabular">{tb.count}</span>}
          </button>
        );
      })}
    </div>
  );
}

function TabBody({ detail, tab, onTab }: { detail: ElementDetail; tab: TabKey; onTab: (t: TabKey) => void }) {
  const t = useT();
  const el = detail.element;
  const key = el.key || el.id;
  const refresh = useRefreshElement(key);
  switch (tab) {
    case "sintesi":
      return el.type === "application" ? <Application360 detail={detail} onTab={onTab} /> : <RequestSummaryTab detail={detail} onTab={onTab} />;
    case "panoramica":
      return (
        <Suspense fallback={<Loading className="text-muted" />}>
          <ProjectOverview refKey={key} onTab={onTab} />
        </Suspense>
      );
    case "wp":
      return (
        <Suspense fallback={<Loading className="text-muted" />}>
          <WorkPackagesTab refKey={key} />
        </Suspense>
      );
    case "milestone":
      return (
        <Suspense fallback={<Loading className="text-muted" />}>
          <MilestonesTab refKey={key} />
        </Suspense>
      );
    case "attivita":
      return <ActivityFeed detail={detail} />;
    case "dettagli":
      return (
        <>
          {PLAN_ITEM_TYPES.includes(el.type) && (
            <Suspense fallback={<Loading className="text-muted" />}>
              <PlanItemPanel refKey={key} />
            </Suspense>
          )}
          {detail.demand && <DemandFacts detail={detail} onDone={refresh} />}
          {el.type === "business_request" && <Evaluation detail={detail} onDone={refresh} />}
          {el.type === "business_request" && <RequestFinance detail={detail} />}
          {detail.impact && <ImpactFacts impact={detail.impact} onDone={refresh} />}
          {detail.application && <ApplicationFacts app={detail.application} />}
          {(el.type === "application" || el.type === "logical_asset" || el.type === "capability") && (
            <Suspense fallback={<Loading className="text-muted" />}>
              <EaSection refKey={key} />
            </Suspense>
          )}
          {el.type === "review" && (
            <Link to="/valutazioni/$key" params={{ key: el.key }} className="btn btn-primary self-start no-underline">
              {t("lavoro.elementpage.openQuestionnaire")}
            </Link>
          )}
          {(el.type === "requirement" || el.type === "exception") && (
            <Suspense fallback={<Loading className="text-muted" />}>
              {el.type === "requirement" ? <RequirementDetail refKey={key} /> : <ExceptionDetail refKey={key} />}
            </Suspense>
          )}
          {GRC_TYPES.includes(el.type) && (
            <Suspense fallback={<Loading className="text-muted" />}>
              <GrcDetail refKey={key} type={el.type} />
            </Suspense>
          )}
          {/* Conformità (piano, passo 20): controllo, evidenza, normative dell'applicazione, riferimenti del requisito. */}
          <Suspense fallback={<Loading className="text-muted" />}>
            {el.type === "control" && <ControlSection refKey={key} />}
            {el.type === "evidence" && <EvidenceSection refKey={key} />}
            {detail.application && <ApplicationCompliance appKey={key} />}
            {el.type === "requirement" && <RequirementLinks refKey={key} />}
          </Suspense>
        </>
      );
    case "requisiti":
      return (
        <Suspense fallback={<Loading className="text-muted" />}>
          <RequirementsSection refKey={key} isImpact={el.type === "impact"} />
        </Suspense>
      );
    case "analisi":
      return <TechnicalAnalysis detail={detail} onDone={refresh} />;
    case "valutazioni":
      return (
        <Suspense fallback={<Loading className="text-muted" />}>
          <ReviewsSection refKey={key} isImpact={el.type === "impact"} />
        </Suspense>
      );
    case "piano":
      return el.type === "project" ? (
        <Suspense fallback={<Loading className="text-muted" />}>
          <ProjectPlanTab refKey={key} />
        </Suspense>
      ) : (
        <>
          <Suspense fallback={null}>
            <RequestProjectBanner refKey={key} />
          </Suspense>
          <PlanPreview refKey={key} />
        </>
      );
    case "stato":
      return (
        <Suspense fallback={<Loading className="text-muted" />}>
          <StatusReportsTab refKey={key} />
        </Suspense>
      );
    case "approvazioni":
      return detail.approvals.length > 0 ? (
        <Approvals detail={detail} onDone={refresh} />
      ) : (
        <p className="m-0 text-muted">{t("lavoro.elementpage.noApprovals")}</p>
      );
    case "allegati":
      return <Attachments refKey={key} />;
    case "produzione":
      return (
        <Suspense fallback={<Loading className="text-muted" />}>
          <DoraCard scope={el.type === "application" ? "application" : "asset"} subject={el.id} />
          <ProductionPanel refKey={key} />
        </Suspense>
      );
    case "gate":
      return (
        <Suspense fallback={<Loading className="text-muted" />}>
          <GatePanel refKey={key} />
        </Suspense>
      );
  }
}

type Filter = "tutto" | "commenti" | "storia";

/** Attività: commenti e storia insieme, in ordine cronologico, con il filtro. */
function ActivityFeed({ detail }: { detail: ElementDetail }) {
  const t = useT();
  const el = detail.element;
  const key = el.key || el.id;
  // Si apre sui commenti: gli eventi di sistema stanno ripiegati sotto («N eventi · Mostra»).
  const [filter, setFilter] = useState<Filter>("commenti");
  const comments = useQuery({ queryKey: ["comments", key], queryFn: () => collab.comments(key) });
  const hist = useQuery({ queryKey: ["activity", el.id], queryFn: () => api.activity(el.id) });
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000 });
  const names = useMemo(() => fieldNamesOf(detail), [detail]);
  const ref = useScrollToHash<HTMLElement>("commenti", !!comments.data && !!hist.data);
  type Entry = { at: string; id: string; node: ReactNode };
  const entries: Entry[] = [];
  if (filter !== "storia") {
    for (const c of comments.data?.items ?? []) {
      entries.push({ at: c.created_at, id: "c" + c.id, node: <CommentItem c={c} members={members.data?.items ?? []} refKey={key} /> });
    }
  }
  if (filter !== "commenti") {
    for (const a of hist.data?.items ?? []) {
      if (a.kind === "comment") continue; // il commento stesso è già nell'elenco
      entries.push({
        at: a.created_at,
        id: "a" + a.id,
        node: (
          <li className="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-3 border-b border-hair py-2 text-[12.5px]">
            <span className="min-w-0">
              <span className="font-semibold">{a.actor_name}</span> <span className="text-muted">{describe(t, a.kind, a.field_key, a.added, a.removed, names, a)}</span>
            </span>
            <time dateTime={a.created_at} title={fmtDateTime(a.created_at)} className="text-muted tabular whitespace-nowrap">
              {ago(a.created_at)}
            </time>
          </li>
        ),
      });
    }
  }
  entries.sort((a, b) => a.at.localeCompare(b.at));
  const nComments = comments.data?.items.length ?? 0;
  const nEvents = (hist.data?.items ?? []).filter((a) => a.kind !== "comment").length;
  return (
    <section ref={ref} id="commenti" className="flex flex-col gap-2" aria-label={t("lavoro.elementpage.tabActivity")}>
      <div role="radiogroup" aria-label={t("lavoro.elementpage.activityFilter")} className="flex self-start border border-hair text-[12px]">
        {(
          [
            ["tutto", t("lavoro.elementpage.filterAll")],
            ["commenti", nComments ? t("lavoro.elementpage.filterCommentsN", { count: nComments }) : t("lavoro.elementpage.filterComments")],
            ["storia", t("lavoro.elementpage.filterHistory")],
          ] as [Filter, string][]
        ).map(([k, label]) => (
          <button key={k} role="radio" aria-checked={filter === k} className={`px-3 py-1 ${filter === k ? "bg-fg text-bg" : "text-muted hover:text-fg"}`} onClick={() => setFilter(k)}>
            {label}
          </button>
        ))}
      </div>
      <ErrorNote error={comments.error ?? hist.error} />
      {(comments.isPending || hist.isPending) && <Loading className="m-0 text-muted" />}
      <ol className="m-0 flex list-none flex-col p-0">
        {entries.map((e) => (
          <ActivityRow key={e.id}>{e.node}</ActivityRow>
        ))}
      </ol>
      {comments.data && hist.data && entries.length === 0 && (
        <p className="m-0 text-[12.5px] text-muted">{filter === "commenti" ? t("lavoro.elementpage.noComments") : t("lavoro.elementpage.nothingToShow")}</p>
      )}
      {filter === "commenti" && nEvents > 0 && (
        <button type="button" className="touch-44 self-start py-1 text-left text-[12.5px] text-muted underline hover:text-fg" onClick={() => setFilter("tutto")}>
          {t("lavoro.elementpage.systemEvents", { count: nEvents })}
        </button>
      )}
      {filter !== "storia" && <CommentComposer refKey={key} />}
    </section>
  );
}

// I commenti e le voci della storia sono già <li>: il contenitore serve solo alla chiave.
function ActivityRow({ children }: { children: ReactNode }) {
  return <>{children}</>;
}

/** Anteprima del piano: le attività (impatti) sulla finestra della richiesta, con il link al Gantt. */
function PlanPreview({ refKey }: { refKey: string }) {
  const t = useT();
  const plan = useQuery({ queryKey: ["plan", refKey], queryFn: () => api.plan(refKey) });
  if (plan.error) return <ErrorNote error={plan.error} />;
  if (!plan.data) return <Loading className="m-0 text-muted" />;
  const { request: rq, tasks } = plan.data;
  const days = (s: string) => new Date(s + "T00:00:00Z").getTime() / 86_400_000;
  const dated = tasks.filter((k) => k.start && k.end);
  const from = Math.min(...dated.map((k) => days(k.start!)), rq.start ? days(rq.start) : Infinity);
  const to = Math.max(...dated.map((k) => days(k.end!)), rq.end ? days(rq.end) : -Infinity);
  const span = Math.max(to - from + 1, 1);
  return (
    <section className="flex flex-col gap-3" aria-label={t("lavoro.elementpage.tabPlan")}>
      <SectionTitle
        aside={
          <Link to="/richieste/$key/piano" params={{ key: rq.key }} className="text-[12px] text-accent underline">
            {t("lavoro.elementpage.openGantt")}
          </Link>
        }
      >
        {t("lavoro.elementpage.tabPlan")}
      </SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">
        {t("lavoro.elementpage.window")} <span className="text-fg tabular">{rq.start ? date(rq.start) : "—"} → {rq.end ? date(rq.end) : "—"}</span> · {t("lavoro.elementpage.scheduled", { scheduled: rq.scheduled, count: rq.impacts })}
      </p>
      {tasks.length === 0 ? (
        <p className="m-0 text-muted">{t("lavoro.elementpage.noImpacts")}</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0">
          {tasks.map((k) => {
            const ok = k.start && k.end && Number.isFinite(from);
            const left = ok ? ((days(k.start!) - from) / span) * 100 : 0;
            const width = ok ? Math.max(((days(k.end!) - days(k.start!) + 1) / span) * 100, 1.5) : 0;
            return (
              <li key={k.id} className="grid grid-cols-1 gap-1 border-b border-hair py-2 sm:grid-cols-[minmax(0,220px)_minmax(0,1fr)] sm:items-center sm:gap-3">
                <span className="min-w-0 text-[12.5px] break-words">
                  <Link to="/e/$key" params={{ key: k.key }} className="font-mono text-accent no-underline hover:underline">{k.key}</Link> {k.application}
                  {k.team && <span className="text-muted"> · {k.team}</span>}
                </span>
                <span className="relative block h-3 bg-hair" aria-label={ok ? t("lavoro.elementpage.fromTo", { from: date(k.start), to: date(k.end) }) : t("lavoro.elementpage.unscheduled")}>
                  {ok && <span className={`absolute top-0 h-3 ${k.state_category === "done" ? "bg-fg" : "bg-accent"}`} style={{ left: `${left}%`, width: `${width}%` }} />}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Colonna laterale

function SideSection({ title, children, open = true, aside }: { title: string; children: ReactNode; open?: boolean; aside?: ReactNode }) {
  return (
    <details open={open} className="group flex flex-col">
      <summary className="flex cursor-pointer list-none items-baseline justify-between border-b-2 border-rule pb-1.5 [&::-webkit-details-marker]:hidden">
        <span className="label m-0">
          <span aria-hidden="true" className="mr-1.5 inline-block w-2 text-muted group-open:rotate-90">›</span>
          {title}
        </span>
        {aside}
      </summary>
      <div className="flex flex-col gap-2 pt-2">{children}</div>
    </details>
  );
}

function Sidebar({ detail, narrow }: { detail: ElementDetail; narrow: boolean }) {
  const t = useT();
  const el = detail.element;
  const key = el.key || el.id;
  const shown = (detail.custom_fields ?? []).filter((f) => f.visible).length;
  return (
    <>
      <SideSection title={t("lavoro.elementpage.fields")} aside={<span className="font-mono text-[11.5px] text-muted tabular">{shown || ""}</span>}>
        <CustomFields detail={detail} />
      </SideSection>
      <SideSection title={t("lavoro.elementpage.peopleRoles")} open={!narrow}>
        {detail.stakeholders.length > 0 ? <PeopleList detail={detail} /> : <p className="m-0 text-[12.5px] text-muted">{t("lavoro.elementpage.noRoles")}</p>}
      </SideSection>
      <SideSection title={t("lavoro.elementpage.dates")} open={!narrow}>
        <dl className="m-0 grid grid-cols-[minmax(84px,38%)_minmax(0,1fr)] text-[13px]">
          {(
            [
              [t("lavoro.elementpage.created"), ago(el.created_at), el.created_at],
              [t("lavoro.elementpage.updated"), ago(el.updated_at), el.updated_at],
              [t("lavoro.elementpage.plannedStart"), el.start_at ? date(el.start_at) : "—", null],
              [t("lavoro.elementpage.plannedEnd"), el.due_at ? date(el.due_at) : "—", null],
              ...(el.resolved_at ? [[t("lavoro.elementpage.closed"), date(el.resolved_at), el.resolved_at] as [string, string, string]] : []),
            ] as [string, string, string | null][]
          ).map(([k, v, iso]) => (
            <div key={k} className="contents">
              <dt className="border-b border-hair py-1.5 text-muted">{k}</dt>
              <dd className="m-0 border-b border-hair py-1.5 tabular" title={iso ? fmtDateTime(iso) : undefined}>
                {v}
              </dd>
            </div>
          ))}
        </dl>
      </SideSection>
      <Links refKey={key} open={!narrow} />
      {/* Codice: i commit che citano la chiave dell'elemento (compare solo se ce ne sono). */}
      <Suspense fallback={null}>
        <CodiceSection refKey={key} open={!narrow} />
      </Suspense>
      {/* Work management: epic dei team e avanzamento, assegnatario, sprint, ore (WorkSections.tsx). */}
      <WorkSidebar detail={detail} narrow={narrow} />
      <LinkedDocuments refKey={key} open={!narrow} />
      <Watchers refKey={key} />
    </>
  );
}

function PeopleList({ detail }: { detail: ElementDetail }) {
  return <People detail={detail} bare />;
}

function Links({ refKey, open }: { refKey: string; open: boolean }) {
  const t = useT();
  const links = useQuery({ queryKey: ["links", refKey], queryFn: () => fieldsApi.links(refKey) });
  const l = links.data;
  const n = l ? (l.parent ? 1 : 0) + l.children.length + l.relations.length : 0;
  const byType = new Map<string, LinkedElement[]>();
  for (const c of l?.children ?? []) byType.set(c.type_name, [...(byType.get(c.type_name) ?? []), c]);
  return (
    <SideSection title={t("lavoro.elementpage.links")} open={open} aside={<span className="font-mono text-[11.5px] text-muted tabular">{n || ""}</span>}>
      <ErrorNote error={links.error} />
      {l && n === 0 && <p className="m-0 text-[12.5px] text-muted">{t("lavoro.elementpage.noLinks")}</p>}
      {l?.parent && (
        <div className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.elementpage.parent")}</span>
          <LinkRow l={l.parent} />
        </div>
      )}
      {[...byType.entries()].map(([type, list]) => (
        <div key={type} className="flex flex-col gap-1">
          <span className="label text-muted">{type} <span className="font-mono tabular">{list.length}</span></span>
          {list.map((c) => <LinkRow key={c.id} l={c} />)}
        </div>
      ))}
      {l && l.relations.length > 0 && (
        <div className="flex flex-col gap-1">
          <span className="label text-muted">{t("lavoro.elementpage.relations")}</span>
          {l.relations.map((r) => <LinkRow key={r.id + (r.relation ?? "")} l={r} />)}
        </div>
      )}
    </SideSection>
  );
}

function LinkRow({ l }: { l: LinkedElement }) {
  const closed = l.state_category === "done" || l.state_category === "cancelled";
  return (
    <Link to="/e/$key" params={{ key: l.key || l.id }} className="flex min-w-0 flex-col py-0.5 text-[13px] text-fg no-underline hover:text-accent">
      <span className="flex min-w-0 items-baseline gap-1.5">
        <span className={`shrink-0 font-mono text-[12px] ${closed ? "text-muted line-through" : "text-accent"}`}>{l.key}</span>
        <span className="min-w-0 break-words">{l.title}</span>
      </span>
      <span className="text-[11.5px] text-muted">
        {l.relation ? `${l.relation} · ` : ""}
        {l.state || l.type_name}
      </span>
    </Link>
  );
}

function Watchers({ refKey }: { refKey: string }) {
  const t = useT();
  const st = useQuery({ queryKey: ["watch", refKey], queryFn: () => collab.watch(refKey) });
  if (!st.data) return null;
  const { followers, following, auto } = st.data;
  return (
    <SideSection title={t("lavoro.elementpage.watchers")} aside={<span className="font-mono text-[11.5px] text-muted tabular">{followers || ""}</span>}>
      <p className="m-0 text-[12.5px] text-muted">
        {t("lavoro.elementpage.followers", { count: followers })}{" "}
        {following ? (auto ? t("lavoro.elementpage.followingAuto") : t("lavoro.elementpage.following")) : t("lavoro.elementpage.notFollowing")}
      </p>
    </SideSection>
  );
}
