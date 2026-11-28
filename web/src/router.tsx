import { createRootRoute, createRoute, createRouter, redirect } from "@tanstack/react-router";
import { Root } from "./shell";
import { Requests } from "./pages/Requests";
import { NewRequest } from "./pages/NewRequest";
import { Inbox } from "./pages/Inbox";
import { TodoBanner } from "./pages/TodoBanner";
import { ProcessDetail, Processes } from "./pages/Processes";
import { Architecture } from "./pages/Architecture";
import { PlanPage } from "./pages/Plan";
import { Roadmap } from "./pages/Roadmap";
import { Notifications } from "./pages/Notifications";
import { Profile } from "./pages/Profile";
import { lazyRouteComponent } from "@tanstack/react-router";
import { ElementPage, TAB_KEYS, type TabKey } from "./pages/ElementPage";
import { lazy, Suspense } from "react";
import { validateListSearch, type ListSearch } from "./views/ElementList";
import { Lists } from "./pages/Lists";
import { ListsIndex } from "./pages/ListsIndex";
import { NotFound } from "./pages/NotFound";
import { Loading } from "./ui";
import { api } from "./api";
import { isAuditorProfile, moduleOn } from "./modules";

// Graduatoria e governance si caricano alla prima visita (bundle iniziale più leggero).
// «Da fare» si carica alla prima visita.
const DaFare = lazyRouteComponent(() => import("./pages/DaFare"), "DaFare");
const Ranking = lazyRouteComponent(() => import("./pages/Ranking"), "Ranking");
const Governance = lazyRouteComponent(() => import("./pages/Governance"), "Governance");
const MeetingPage = lazyRouteComponent(() => import("./pages/Governance"), "MeetingPage");

const rootRoute = createRootRoute({ component: Root });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: async () => {
    // Per il revisore la pagina iniziale è lo spazio dell'auditor (ondata 3, C6).
    const me = await api.me().catch(() => null);
    // Con la domanda spenta (per esempio un'organizzazione di una persona sola) si parte da «Da fare».
    if (isAuditorProfile(me) && moduleOn(me, "grc")) throw redirect({ to: "/audit-spazio" });
    throw redirect({ to: me && !moduleOn(me, "demand") ? "/da-fare" : "/richieste" });
  },
});

// q: query; sel: anteprima; vista: lista o tabella; v: vista salvata; g: raggruppamento
// (piano, passo 16; views/ElementList.tsx).
export type RequestsSearch = ListSearch;

export const requestsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/richieste",
  validateSearch: validateListSearch,
  component: Requests,
});

const newRequestRoute = createRoute({ getParentRoute: () => rootRoute, path: "/richieste/nuova", component: NewRequest });
const inboxRoute = createRoute({ getParentRoute: () => rootRoute, path: "/approvazioni", component: () => <><TodoBanner /><Inbox /></> });
// «Da fare» (ondata 2): la pagina unica di ciò che aspetta la persona; le vecchie code rimandano qui.
const todoRoute = createRoute({ getParentRoute: () => rootRoute, path: "/da-fare", component: DaFare });

const roadmapRoute = createRoute({ getParentRoute: () => rootRoute, path: "/roadmap", component: Roadmap });
const planRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/richieste/$key/piano",
  component: function PlanRoute() {
    const { key } = planRoute.useParams();
    return <PlanPage key={key} requestKey={key} />;
  },
});

type ArchTab = "asset" | "applicazioni" | "team";
const architectureRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/architettura",
  validateSearch: (s: Record<string, unknown>): { tab?: ArchTab } => ({
    tab: s.tab === "applicazioni" || s.tab === "team" || s.tab === "asset" ? s.tab : undefined,
  }),
  component: function ArchitecturePage() {
    const { tab } = architectureRoute.useSearch();
    return <Architecture tab={tab ?? "asset"} />;
  },
});

const processesRoute = createRoute({ getParentRoute: () => rootRoute, path: "/processi", component: Processes });
const processRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/processi/$key",
  // Dalle valutazioni: ?regola=<chiave> apre una regola, ?nuova=open_review:<tipo> ne prepara una nuova.
  validateSearch: (s: Record<string, unknown>): { regola?: string; nuova?: string } => ({
    regola: typeof s.regola === "string" && s.regola ? s.regola : undefined,
    nuova: typeof s.nuova === "string" && s.nuova ? s.nuova : undefined,
  }),
  component: function ProcessPage() {
    const { key } = processRoute.useParams();
    return <ProcessDetail key={key} processKey={key} />;
  },
});

// Notifiche in-app (piano, passo 3).
const notificationsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/notifiche", component: Notifications });

// Profilo e token API (piano, passo 15).
const profileRoute = createRoute({ getParentRoute: () => rootRoute, path: "/profilo", component: Profile });
// Wiki personale (AI, ondata 4): le pagine della persona e quelle condivise con lei.
const WikiHomePage = lazy(() => import("./pages/Wiki").then((m) => ({ default: m.WikiHome })));
const WikiPagePage = lazy(() => import("./pages/Wiki").then((m) => ({ default: m.WikiPageView })));
const wikiLoading = <Loading className="px-4 py-6 text-muted md:px-7" />;
const wikiRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/profilo/wiki",
  component: () => (
    <Suspense fallback={wikiLoading}>
      <WikiHomePage />
    </Suspense>
  ),
});
const wikiPageRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/profilo/wiki/$id",
  component: function WikiPageRoute() {
    const { id } = wikiPageRoute.useParams();
    return (
      <Suspense fallback={wikiLoading}>
        <WikiPagePage key={id} id={id} />
      </Suspense>
    );
  },
});

// Valutazione e priorità del portafoglio, governance (piano, passi 8 e 9).
const rankingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/graduatoria",
  validateSearch: (s: Record<string, unknown>): { vista?: "scorecard"; unita?: string; periodo?: string } => ({
    vista: s.vista === "scorecard" ? "scorecard" : undefined,
    unita: typeof s.unita === "string" && s.unita ? s.unita : undefined,
    periodo: typeof s.periodo === "string" && s.periodo ? s.periodo : undefined,
  }),
  component: function RankingPage() {
    const { vista, unita, periodo } = rankingRoute.useSearch();
    return <Ranking view={vista ?? "graduatoria"} unit={unita} period={periodo} />;
  },
});
const governanceRoute = createRoute({ getParentRoute: () => rootRoute, path: "/governance", component: Governance });
const meetingRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/governance/sedute/$id",
  component: function MeetingRoute() {
    const { id } = meetingRoute.useParams();
    return <MeetingPage key={id} id={id} />;
  },
});
// Cabina di portafoglio (ondata 3): graduatoria, budget, capacità, roadmap e scenari insieme.
const CabinaPage = lazyRouteComponent(() => import("./pages/Cabina"), "CabinaPage");
const cabinaRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/cabina",
  validateSearch: (s: Record<string, unknown>): { unita?: string; periodo?: string } => ({
    unita: typeof s.unita === "string" && s.unita ? s.unita : undefined,
    periodo: typeof s.periodo === "string" && s.periodo ? s.periodo : undefined,
  }),
  component: function CabinaRoute() {
    const { unita, periodo } = cabinaRoute.useSearch();
    return <CabinaPage unit={unita} period={periodo} />;
  },
});
const portfolioRoutes = [rankingRoute, cabinaRoute, governanceRoute, meetingRoute];

// Budget e costi (piano, passi 11 e 12): pagina riservata a finance.read, caricata alla prima visita.
const Finance = lazyRouteComponent(() => import("./pages/Finance"), "Finance");
type FinTabKey = "budget" | "costi" | "import" | "tariffe" | "ev" | "struttura";
const FIN_TABS: FinTabKey[] = ["budget", "costi", "import", "tariffe", "ev", "struttura"];
export const financeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/finanza",
  validateSearch: (s: Record<string, unknown>): { tab?: FinTabKey; richiesta?: string } => ({
    tab: FIN_TABS.includes(s.tab as FinTabKey) ? (s.tab as FinTabKey) : undefined,
    richiesta: typeof s.richiesta === "string" && s.richiesta ? s.richiesta : undefined,
  }),
  component: function FinanceRoute() {
    const { tab, richiesta } = financeRoute.useSearch();
    return <Finance tab={tab ?? "budget"} richiesta={richiesta} />;
  },
});
// Budget gerarchico (replica/budget.md): albero dei budget e organizzazione, caricati alla prima visita.
const BudgetTreePage = lazyRouteComponent(() => import("./pages/budget/BudgetTree"), "BudgetTreePage");
const OrgConfig = lazyRouteComponent(() => import("./pages/budget/OrgConfig"), "OrgConfig");
const budgetRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/budget",
  validateSearch: (s: Record<string, unknown>): { periodo?: string } => ({ periodo: typeof s.periodo === "string" && s.periodo ? s.periodo : undefined }),
  component: function BudgetRoute() {
    const { periodo } = budgetRoute.useSearch();
    return <BudgetTreePage period={periodo} />;
  },
});
const orgConfigRoute = createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/organizzazione", component: OrgConfig });
// Valutazioni con questionari (piano, passo 4): si caricano alla prima visita.
const ReviewInbox = lazyRouteComponent(() => import("./pages/Reviews"), "ReviewInbox");
const ReviewPage = lazyRouteComponent(() => import("./pages/Reviews"), "ReviewPage");
const ReviewConfigPage = lazyRouteComponent(() => import("./pages/ReviewConfig"), "ReviewConfigPage");
const reviewInboxRoute = createRoute({ getParentRoute: () => rootRoute, path: "/valutazioni", component: () => <><TodoBanner /><ReviewInbox /></> });
const reviewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/valutazioni/$key",
  component: function ReviewRoute() {
    const { key } = reviewRoute.useParams();
    return <ReviewPage key={key} reviewKey={key} />;
  },
});
type ReviewConfigTab = "team" | "tipi" | "modelli";
const reviewConfigRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/configurazione/valutazioni",
  validateSearch: (s: Record<string, unknown>): { tab?: ReviewConfigTab; modello?: string } => ({
    tab: s.tab === "team" || s.tab === "tipi" || s.tab === "modelli" ? s.tab : undefined,
    modello: typeof s.modello === "string" ? s.modello : undefined,
  }),
  component: function ReviewConfigRoute() {
    const { tab, modello } = reviewConfigRoute.useSearch();
    return <ReviewConfigPage tab={tab ?? "team"} template={modello} />;
  },
});
const reviewRoutes = [reviewInboxRoute, reviewRoute, reviewConfigRoute];

// Requisiti (piano, passi 5 e 6): inbox «Da verificare» e configurazione, alla prima visita.
const VerifyInbox = lazyRouteComponent(() => import("./pages/Requirements"), "VerifyInbox");
const RequirementsConfigPage = lazyRouteComponent(() => import("./pages/RequirementsConfig"), "RequirementsConfigPage");
const verifyInboxRoute = createRoute({ getParentRoute: () => rootRoute, path: "/requisiti", component: () => <><TodoBanner /><VerifyInbox /></> });
type RequirementsConfigTab = "cataloghi" | "deroghe";
const requirementsConfigRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/configurazione/requisiti",
  validateSearch: (s: Record<string, unknown>): { tab?: RequirementsConfigTab; catalogo?: string } => ({
    tab: s.tab === "cataloghi" || s.tab === "deroghe" ? s.tab : undefined,
    catalogo: typeof s.catalogo === "string" && s.catalogo ? s.catalogo : undefined,
  }),
  component: function RequirementsConfigRoute() {
    const { tab, catalogo } = requirementsConfigRoute.useSearch();
    return <RequirementsConfigPage tab={tab ?? "cataloghi"} catalog={catalogo} />;
  },
});
const requirementRoutes = [verifyInboxRoute, requirementsConfigRoute];

// DevSecOps (piano, passo 21): portafoglio e configurazione, caricati alla prima visita.
const DevsecopsPage = lazyRouteComponent(() => import("./pages/Devsecops"), "DevsecopsPage");
const DevsecopsConfigPage = lazyRouteComponent(() => import("./pages/Devsecops"), "DevsecopsConfigPage");
const GateConfigPage = lazyRouteComponent(() => import("./pages/GateConfig"), "GateConfigPage");
// Metriche di consegna (fase 5a): le quattro chiavi DORA, con periodo, ambito e soggetto nella query.
const DoraPage = lazyRouteComponent(() => import("./pages/Dora"), "DoraPage");
const DORA_PERIODS = ["week", "month", "quarter"];
const DORA_SCOPES = ["tenant", "team", "application", "asset", "service"];
const doraRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/devsecops/metriche",
  validateSearch: (s: Record<string, unknown>): { periodo?: "week" | "month" | "quarter"; ambito?: "tenant" | "team" | "application" | "asset" | "service"; soggetto?: string; confronto?: "team" | "application" | "asset" | "service" } => ({
    periodo: DORA_PERIODS.includes(s.periodo as string) ? (s.periodo as "week" | "month" | "quarter") : undefined,
    ambito: DORA_SCOPES.includes(s.ambito as string) ? (s.ambito as "tenant") : undefined,
    soggetto: typeof s.soggetto === "string" && s.soggetto ? s.soggetto : undefined,
    confronto: DORA_SCOPES.includes(s.confronto as string) && s.confronto !== "tenant" ? (s.confronto as "team") : undefined,
  }),
  component: function DoraRoute() {
    const search = doraRoute.useSearch();
    const navigate = doraRoute.useNavigate();
    return <DoraPage search={search} onSearch={(next) => navigate({ search: next, replace: true })} />;
  },
});
// Rilasci: gli elementi di un rilascio con le note, e la certificazione del rilascio per numero di
// change (indirizzo stabile, da mettere nella change; ?versione=N mostra un'istantanea generata).
const ReleasePage = lazyRouteComponent(() => import("./pages/Rilascio"), "ReleasePage");
const CertificationPage = lazyRouteComponent(() => import("./pages/Certificazione"), "CertificationPage");
const releaseRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/rilasci/$id",
  component: function ReleaseRoute() {
    const { id } = releaseRoute.useParams();
    return <ReleasePage id={id} />;
  },
});
const certificationRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/change/$numero/certificazione",
  validateSearch: (s: Record<string, unknown>): { versione?: number } =>
    Number.isInteger(Number(s.versione)) && Number(s.versione) > 0 ? { versione: Number(s.versione) } : {},
  component: function CertificationRoute() {
    const { numero } = certificationRoute.useParams();
    const { versione } = certificationRoute.useSearch();
    const navigate = certificationRoute.useNavigate();
    return <CertificationPage number={numero} version={versione} onVersion={(v) => navigate({ search: v ? { versione: v } : {} })} />;
  },
});
const devsecopsRoutes = [
  releaseRoute,
  certificationRoute,
  createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/gate", component: GateConfigPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/devsecops", component: DevsecopsPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/devsecops", component: DevsecopsConfigPage }),
  doraRoute,
];

// DevSecOps, fase 2 (vulnerabilità governate): cruscotto e configurazione di SLA ed escalation.
const VulnerabilitiesPage = lazyRouteComponent(() => import("./pages/Vulnerabilities"), "VulnerabilitiesPage");
const VulnerabilityConfigPage = lazyRouteComponent(() => import("./pages/VulnerabilityConfig"), "VulnerabilityConfigPage");
const vulnerabilityRoutes = [
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/devsecops/vulnerabilita",
    // q: filtro iniziale (la palette ⌘K apre la pagina sulla vulnerabilità cercata).
    validateSearch: (s: Record<string, unknown>): { q?: string } => (typeof s.q === "string" && s.q ? { q: s.q } : {}),
    component: VulnerabilitiesPage,
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/vulnerabilita", component: VulnerabilityConfigPage }),
];

// DevSecOps fase 3: politica centrale della pipeline (Configurazione) e aderenza (Governo).
const PipelinePolicyPage = lazyRouteComponent(() => import("./pages/PipelinePolicy"), "PipelinePolicyPage");
const AdherencePage = lazyRouteComponent(() => import("./pages/PipelineAdherence"), "AdherencePage");
type PolicyTabKey = "matrice" | "versioni" | "adozione";
const POLICY_TAB_KEYS: PolicyTabKey[] = ["matrice", "versioni", "adozione"];
export const pipelinePolicyRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/configurazione/devsecops/politica",
  validateSearch: (s: Record<string, unknown>): { tab?: PolicyTabKey; da?: number } => ({
    tab: POLICY_TAB_KEYS.includes(s.tab as PolicyTabKey) ? (s.tab as PolicyTabKey) : undefined,
    da: Number.isInteger(Number(s.da)) && Number(s.da) > 0 ? Number(s.da) : undefined,
  }),
  component: function PipelinePolicyRoute() {
    const { tab, da } = pipelinePolicyRoute.useSearch();
    return <PipelinePolicyPage tab={tab ?? "matrice"} from={da} />;
  },
});
const pipelineAdherenceRoute = createRoute({ getParentRoute: () => rootRoute, path: "/devsecops/aderenza", component: AdherencePage });
const pipelineRoutes = [pipelinePolicyRoute, pipelineAdherenceRoute];

// Pagina di dettaglio completa di un elemento (sdlc.md §10): /e/CHIAVE per tutti i tipi,
// /richieste/CHIAVE come alias per le richieste. La scheda aperta sta nella query.
type ElementSearch = { scheda?: TabKey };
const validateElementSearch = (s: Record<string, unknown>): ElementSearch => ({
  scheda: TAB_KEYS.includes(s.scheda as TabKey) ? (s.scheda as TabKey) : undefined,
});
export const elementRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/e/$key",
  validateSearch: validateElementSearch,
  component: function ElementRoute() {
    const { key } = elementRoute.useParams();
    const { scheda } = elementRoute.useSearch();
    const navigate = elementRoute.useNavigate();
    return <ElementPage key={key} refKey={key} tab={scheda} onTab={(t) => navigate({ search: { scheda: t }, replace: true })} />;
  },
});
const requestPageRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/richieste/$key",
  validateSearch: validateElementSearch,
  component: function RequestPageRoute() {
    const { key } = requestPageRoute.useParams();
    const { scheda } = requestPageRoute.useSearch();
    const navigate = requestPageRoute.useNavigate();
    return <ElementPage key={key} refKey={key} tab={scheda} onTab={(t) => navigate({ search: { scheda: t }, replace: true })} />;
  },
});
// Configurazione → indice per obiettivo (casa unica delle impostazioni), caricato alla prima visita.
const ConfigHome = lazyRouteComponent(() => import("./pages/ConfigHome"), "ConfigHome");
// Procedura guidata «Coinvolgi un team» (ondata semplicità).
const CoinvolgiPage = lazyRouteComponent(() => import("./pages/Coinvolgi"), "CoinvolgiPage");
const coinvolgiRoute = createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/coinvolgi", component: CoinvolgiPage });
const configHomeRoute = createRoute({ getParentRoute: () => rootRoute, path: "/configurazione", component: ConfigHome });
// Configurazione → Campi personalizzati (fields.admin o tenant.admin), caricata alla prima visita.
const FieldsConfig = lazyRouteComponent(() => import("./pages/FieldsConfig"), "FieldsConfig");
const fieldsConfigRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/configurazione/campi",
  validateSearch: (s: Record<string, unknown>): { campo?: string; tipo?: string } => ({
    campo: typeof s.campo === "string" && s.campo ? s.campo : undefined,
    tipo: typeof s.tipo === "string" && s.tipo ? s.tipo : undefined,
  }),
  component: FieldsConfig,
});
export { fieldsConfigRoute };
// Configurazione → Tipi di elemento (tenant.admin), caricata alla prima visita.
const TypesConfig = lazyRouteComponent(() => import("./pages/TypesConfig"), "TypesConfig");
const typesConfigRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/configurazione/tipi",
  validateSearch: (s: Record<string, unknown>): { tipo?: string } => ({ tipo: typeof s.tipo === "string" && s.tipo ? s.tipo : undefined }),
  component: TypesConfig,
});
export { typesConfigRoute };
const elementRoutes = [elementRoute, requestPageRoute, fieldsConfigRoute, typesConfigRoute];

// Work management (piano, passi 10 e 18): board dei team, backlog e sprint, il mio lavoro.
// Le pagine si caricano alla prima visita; lo spazio scelto e i filtri stanno nella query.
const BoardPage = lazy(() => import("./pages/Board").then((m) => ({ default: m.Board })));
const BacklogPage = lazy(() => import("./pages/Backlog").then((m) => ({ default: m.Backlog })));
const MyWorkPage = lazy(() => import("./pages/MyWork").then((m) => ({ default: m.MyWork })));
const workLoading = <Loading className="px-4 py-6 text-muted md:px-7" />;
const SWIMLANES = ["none", "assignee", "epic", "priority"] as const;
type DeliverySearch = { spazio?: string; sprint?: string; q?: string; corsie?: (typeof SWIMLANES)[number] };
const str = (v: unknown) => (typeof v === "string" && v ? v : undefined);
const boardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/delivery/board",
  validateSearch: (s: Record<string, unknown>): DeliverySearch => ({
    spazio: str(s.spazio),
    sprint: str(s.sprint),
    q: str(s.q),
    corsie: SWIMLANES.includes(s.corsie as (typeof SWIMLANES)[number]) ? (s.corsie as (typeof SWIMLANES)[number]) : undefined,
  }),
  component: function BoardRoute() {
    const search = boardRoute.useSearch();
    const navigate = boardRoute.useNavigate();
    return (
      <Suspense fallback={workLoading}>
        <BoardPage search={search} onSearch={(s) => navigate({ search: s })} />
      </Suspense>
    );
  },
});
const backlogRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/delivery/backlog",
  validateSearch: (s: Record<string, unknown>): { spazio?: string; q?: string } => ({ spazio: str(s.spazio), q: str(s.q) }),
  component: function BacklogRoute() {
    const search = backlogRoute.useSearch();
    const navigate = backlogRoute.useNavigate();
    return (
      <Suspense fallback={workLoading}>
        <BacklogPage search={search} onSearch={(s) => navigate({ search: s })} />
      </Suspense>
    );
  },
});
const myWorkRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/delivery/mio-lavoro",
  component: function MyWorkRoute() {
    return (
      <Suspense fallback={workLoading}>
        <MyWorkPage />
      </Suspense>
    );
  },
});
const workRoutes = [boardRoute, backlogRoute, myWorkRoute];

// Tempo e team (piano, passo 18): timesheet settimanale, report del tempo, team e capacità.
const TimesheetPage = lazy(() => import("./pages/Timesheet").then((m) => ({ default: m.Timesheet })));
const TimeReportPage = lazy(() => import("./pages/TimeReport").then((m) => ({ default: m.TimeReport })));
const TeamsPageC = lazy(() => import("./pages/TeamsPage").then((m) => ({ default: m.TeamsPage })));
const tempoRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tempo",
  validateSearch: (s: Record<string, unknown>): { settimana?: string; persona?: string } => ({ settimana: str(s.settimana), persona: str(s.persona) }),
  component: function TempoRoute() {
    const search = tempoRoute.useSearch();
    const navigate = tempoRoute.useNavigate();
    return (
      <Suspense fallback={workLoading}>
        <TimesheetPage search={search} onSearch={(s) => navigate({ search: s })} />
      </Suspense>
    );
  },
});
const tempoReportRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tempo/report",
  component: function TempoReportRoute() {
    return (
      <Suspense fallback={workLoading}>
        <TimeReportPage />
      </Suspense>
    );
  },
});
const tempoTeamRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/tempo/team",
  component: function TempoTeamRoute() {
    return (
      <Suspense fallback={workLoading}>
        <TeamsPageC />
      </Suspense>
    );
  },
});
const tempoRoutes = [tempoRoute, tempoReportRoute, tempoTeamRoute];
// Avanzamento (piano, passi 13 e 14): cruscotto di portafoglio, portale del richiedente e
// configurazione; si caricano alla prima visita.
const Dashboard = lazyRouteComponent(() => import("./pages/Dashboard"), "Dashboard");
const PortalHome = lazyRouteComponent(() => import("./pages/Portal"), "PortalHome");
const PortalNew = lazyRouteComponent(() => import("./pages/Portal"), "PortalNew");
const PortalRequestPage = lazyRouteComponent(() => import("./pages/Portal"), "PortalRequestPage");
const ProgressConfig = lazyRouteComponent(() => import("./pages/ProgressConfig"), "ProgressConfig");
type DashSearch = { unit?: string; driver?: string; from?: string; to?: string };
export const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/cruscotto",
  validateSearch: (s: Record<string, unknown>): DashSearch => ({ unit: str(s.unit), driver: str(s.driver), from: str(s.from), to: str(s.to) }),
  component: function DashboardRoute() {
    const filter = dashboardRoute.useSearch();
    return <Dashboard filter={filter} />;
  },
});
const portalRoute = createRoute({ getParentRoute: () => rootRoute, path: "/portale", component: PortalHome });
export const portalNewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/portale/nuova",
  validateSearch: (s: Record<string, unknown>): { tipo?: string; bozza?: string } => ({ tipo: str(s.tipo), bozza: str(s.bozza) }),
  component: function PortalNewRoute() {
    const { tipo, bozza } = portalNewRoute.useSearch();
    return <PortalNew type={tipo} draftId={bozza} />;
  },
});
export const portalRequestRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/portale/richieste/$key",
  validateSearch: (s: Record<string, unknown>): { inviata?: boolean } => ({ inviata: s.inviata === true || s.inviata === "true" ? true : undefined }),
  component: function PortalRequestRoute() {
    const { key } = portalRequestRoute.useParams();
    const { inviata } = portalRequestRoute.useSearch();
    return <PortalRequestPage key={key} refKey={key} justSent={inviata} />;
  },
});
const progressConfigRoute = createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/avanzamento", component: ProgressConfig });
const PortalCatalogConfig = lazyRouteComponent(() => import("./pages/PortalCatalogConfig"), "PortalCatalogConfig");
const portalCatalogConfigRoute = createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/portale", component: PortalCatalogConfig });
const progressRoutes = [dashboardRoute, portalRoute, portalNewRoute, portalRequestRoute, progressConfigRoute, portalCatalogConfigRoute];
// Persone e accessi, Registro di audit, Webhook (piano, passo 15) e pagine pubbliche dei
// link monouso (invito, password). Si caricano alla prima visita.
const AdminPage = lazyRouteComponent(() => import("./pages/Admin"), "AdminPage");
const AuditLog = lazyRouteComponent(() => import("./pages/AuditLog"), "AuditLog");
const WebhooksPage = lazyRouteComponent(() => import("./pages/Webhooks"), "Webhooks");
const InvitePage = lazyRouteComponent(() => import("./pages/PublicLinks"), "InvitePage");
const PasswordPage = lazyRouteComponent(() => import("./pages/PublicLinks"), "PasswordPage");
type AdminTabKey = "membri" | "ruoli" | "gruppi" | "oggetti" | "sicurezza";
const ADMIN_TAB_KEYS: AdminTabKey[] = ["membri", "ruoli", "gruppi", "oggetti", "sicurezza"];
export const adminRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/configurazione/persone",
  validateSearch: (s: Record<string, unknown>): { tab?: AdminTabKey; sel?: string } => ({
    tab: ADMIN_TAB_KEYS.includes(s.tab as AdminTabKey) ? (s.tab as AdminTabKey) : undefined,
    sel: typeof s.sel === "string" && s.sel ? s.sel : undefined,
  }),
  component: function AdminRoute() {
    const { tab, sel } = adminRoute.useSearch();
    return <AdminPage tab={tab ?? "membri"} selected={sel} />;
  },
});
const auditRoute = createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/audit", component: AuditLog });
export const webhooksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/configurazione/webhook",
  validateSearch: (s: Record<string, unknown>): { sel?: string } => ({ sel: typeof s.sel === "string" && s.sel ? s.sel : undefined }),
  component: function WebhooksRoute() {
    const { sel } = webhooksRoute.useSearch();
    return <WebhooksPage selected={sel} />;
  },
});
const inviteRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/invito/$token",
  component: function InviteRoute() {
    const { token } = inviteRoute.useParams();
    return <InvitePage token={token} />;
  },
});
const passwordRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/password/$token",
  component: function PasswordRoute() {
    const { token } = passwordRoute.useParams();
    return <PasswordPage token={token} />;
  },
});
const ModulesConfig = lazyRouteComponent(() => import("./pages/ModulesConfig"), "ModulesConfig");
const modulesRoute = createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/moduli", component: ModulesConfig });
// Primo avvio guidato (proposta 7B): «Inizia da qui», modelli per tipo di azienda e cosa è applicato.
const AvvioPage = lazyRouteComponent(() => import("./pages/Avvio"), "AvvioPage");
const AVVIO_TABS = ["passi", "modelli", "applicato"] as const;
const avvioRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/configurazione/avvio",
  validateSearch: (s: Record<string, unknown>): { tab?: (typeof AVVIO_TABS)[number] } => ({
    tab: AVVIO_TABS.includes(s.tab as (typeof AVVIO_TABS)[number]) ? (s.tab as (typeof AVVIO_TABS)[number]) : undefined,
  }),
  component: function AvvioRoute() {
    const { tab } = avvioRoute.useSearch();
    return <AvvioPage tab={tab ?? "passi"} />;
  },
});
// Ambiente di prova e promuovi (ondata 3): tenant di prova collegato, confronto e promozione.
const ProvaPage = lazyRouteComponent(() => import("./pages/Prova"), "ProvaPage");
const provaRoute = createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/prova", component: ProvaPage });
// Percorso guidato dell'amministratore e mappa d'insieme dei processi (ondata «semplicità»).
const PercorsoGuidato = lazyRouteComponent(() => import("./pages/PercorsoGuidato"), "PercorsoGuidato");
const MappaProcessi = lazyRouteComponent(() => import("./pages/MappaProcessi"), "MappaProcessi");
const guidaAdminRoutes = [
  createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/percorso", component: PercorsoGuidato }),
  createRoute({ getParentRoute: () => rootRoute, path: "/mappa-processi", component: MappaProcessi }),
];
// Organizzazioni (tenant) dall'interfaccia, solo per il superamministratore dell'istanza.
const OrganizationsPage = lazyRouteComponent(() => import("./pages/Organizations"), "OrganizationsPage");
const organizationsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/piattaforma/organizzazioni", component: OrganizationsPage });
const accessRoutes = [organizationsRoute, adminRoute, auditRoute, modulesRoute, avvioRoute, provaRoute, webhooksRoute, inviteRoute, passwordRoute];
// Governance → Rischi e incidenti (piano, passo 20): cruscotto, registri e configurazione.
const RisksPage = lazyRouteComponent(() => import("./pages/Risks"), "RisksPage");
type RiskTabKey = "cruscotto" | "rischi" | "fornitori" | "incidenti" | "configurazione";
const RISK_TAB_KEYS: RiskTabKey[] = ["rischi", "fornitori", "incidenti", "configurazione"];
export const risksRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/rischi",
  validateSearch: (s: Record<string, unknown>): { tab?: RiskTabKey } => ({
    tab: RISK_TAB_KEYS.includes(s.tab as RiskTabKey) ? (s.tab as RiskTabKey) : undefined,
  }),
  component: function RisksRoute() {
    const { tab } = risksRoute.useSearch();
    return <RisksPage tab={tab ?? "cruscotto"} />;
  },
});

// Vista di crisi dell'incidente (replica/usabilita.md, proposta 3): una pagina sola, telefono prima.
const IncidentCrisisPage = lazy(() => import("./pages/IncidentCrisis").then((m) => ({ default: m.IncidentCrisisPage })));
const crisisRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/crisi/$key",
  component: function CrisisRoute() {
    const { key } = crisisRoute.useParams();
    return (
      <Suspense fallback={workLoading}>
        <IncidentCrisisPage key={key} refKey={key} />
      </Suspense>
    );
  },
}); // i18n-ok: codice TypeScript letto come testo

// Knowledge (piano, passo 19): Documenti, spazi con l'albero delle pagine, pagina con storia e
// editor. Si caricano alla prima visita; vista, versioni e confronto stanno nella query.
const KnowledgeHome = lazyRouteComponent(() => import("./pages/Knowledge"), "KnowledgeHome");
const KnowledgeSpace = lazy(() => import("./pages/Knowledge").then((m) => ({ default: m.SpacePage })));
const KnowledgeSettings = lazy(() => import("./pages/Knowledge").then((m) => ({ default: m.SpaceSettings })));
const KnowledgePage = lazy(() => import("./pages/Knowledge").then((m) => ({ default: m.KnowledgePage })));
const kbHomeRoute = createRoute({ getParentRoute: () => rootRoute, path: "/documenti", component: KnowledgeHome });
const kbSpaceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/documenti/spazi/$space",
  validateSearch: (s: Record<string, unknown>): { q?: string } => ({ q: str(s.q) }),
  component: function KnowledgeSpaceRoute() {
    const { space } = kbSpaceRoute.useParams();
    const { q } = kbSpaceRoute.useSearch();
    const navigate = kbSpaceRoute.useNavigate();
    return (
      <Suspense fallback={workLoading}>
        <KnowledgeSpace key={space} spaceKey={space} q={q} onSearch={(t) => navigate({ search: { q: t || undefined }, replace: true })} />
      </Suspense>
    );
  },
});
const kbSettingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/documenti/spazi/$space/impostazioni",
  component: function KnowledgeSettingsRoute() {
    const { space } = kbSettingsRoute.useParams();
    return (
      <Suspense fallback={workLoading}>
        <KnowledgeSettings key={space} spaceKey={space} />
      </Suspense>
    );
  },
});
type KbPageSearch = { vista?: "storia" | "modifica"; v?: number; da?: number; a?: number; approvata?: boolean };
const num = (v: unknown) => {
  const n = typeof v === "number" ? v : typeof v === "string" ? Number(v) : NaN;
  return Number.isInteger(n) && n > 0 ? n : undefined;
};
const kbPageRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/documenti/$key",
  validateSearch: (s: Record<string, unknown>): KbPageSearch => ({
    vista: s.vista === "storia" || s.vista === "modifica" ? s.vista : undefined,
    v: num(s.v),
    da: num(s.da),
    a: num(s.a),
    approvata: s.approvata === true || s.approvata === "true" ? true : s.approvata === false || s.approvata === "false" ? false : undefined,
  }),
  component: function KnowledgePageRoute() {
    const { key } = kbPageRoute.useParams();
    const search = kbPageRoute.useSearch();
    const navigate = kbPageRoute.useNavigate();
    return (
      <Suspense fallback={workLoading}>
        <KnowledgePage key={key} pageKey={key} search={search} onSearch={(s) => navigate({ search: s })} />
      </Suspense>
    );
  },
});
const knowledgeRoutes = [kbHomeRoute, kbSpaceRoute, kbSettingsRoute, kbPageRoute];
// Architettura enterprise (piano, passo 20): mappa del grafo, capability, dipendenze,
// razionalizzazione e diagrammi. Si caricano alla prima visita.
const EaMapPage = lazy(() => import("./pages/ea/MapPage").then((m) => ({ default: m.MapPage })));
const EaCapabilityPage = lazy(() => import("./pages/ea/CapabilityPage").then((m) => ({ default: m.CapabilityPage })));
const EaDependenciesPage = lazy(() => import("./pages/ea/DependenciesPage").then((m) => ({ default: m.DependenciesPage })));
const EaRationalizationPage = lazy(() => import("./pages/ea/RationalizationPage").then((m) => ({ default: m.RationalizationPage })));
const EaDiagramsPage = lazy(() => import("./pages/ea/DiagramsPage").then((m) => ({ default: m.DiagramsPage })));
const EaDiagramPage = lazy(() => import("./pages/ea/DiagramsPage").then((m) => ({ default: m.DiagramPage })));
const eaLoading = <Loading className="px-4 py-6 text-muted md:px-7" />;
const eaMapRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/architettura/mappa",
  validateSearch: (s: Record<string, unknown>): { nodo?: string } => ({ nodo: typeof s.nodo === "string" && s.nodo ? s.nodo : undefined }),
  component: function EaMapRoute() {
    const { nodo } = eaMapRoute.useSearch();
    return (
      <Suspense fallback={eaLoading}>
        <EaMapPage focus={nodo} />
      </Suspense>
    );
  },
});
const eaPage = <P extends string>(path: P, Page: React.LazyExoticComponent<() => React.JSX.Element>) => // i18n-ok: codice TypeScript letto come testo
  createRoute({
    getParentRoute: () => rootRoute,
    path,
    component: function EaRoute() {
      return (
        <Suspense fallback={eaLoading}>
          <Page />
        </Suspense>
      );
    },
  }); // i18n-ok: codice TypeScript letto come testo
const eaCapabilityRoute = eaPage("/architettura/capability", EaCapabilityPage);
const eaDependenciesRoute = eaPage("/architettura/dipendenze", EaDependenciesPage);
const eaRationalizationRoute = eaPage("/architettura/razionalizzazione", EaRationalizationPage);
const eaDiagramsRoute = eaPage("/architettura/diagrammi", EaDiagramsPage);
const eaDiagramRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/architettura/diagrammi/$id",
  component: function EaDiagramRoute() {
    const { id } = eaDiagramRoute.useParams();
    return (
      <Suspense fallback={eaLoading}>
        <EaDiagramPage id={id} />
      </Suspense>
    );
  },
}); // i18n-ok: codice TypeScript letto come testo
const eaRoutes = [eaMapRoute, eaCapabilityRoute, eaDependenciesRoute, eaRationalizationRoute, eaDiagramsRoute, eaDiagramRoute];
// DevSecOps: connettore GitHub e profili di pipeline (Configurazione → Integrazioni → GitHub).
const GitHubPage = lazyRouteComponent(() => import("./pages/GitHub"), "GitHubPage");
type GitHubTabKey = "integrazioni" | "repository" | "profili" | "sincronizzazioni";
const GITHUB_TAB_KEYS: GitHubTabKey[] = ["integrazioni", "repository", "profili", "sincronizzazioni"];
export const githubRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/configurazione/integrazioni/github",
  validateSearch: (s: Record<string, unknown>): { tab?: GitHubTabKey; sel?: string; integrazione?: string } => ({
    tab: GITHUB_TAB_KEYS.includes(s.tab as GitHubTabKey) ? (s.tab as GitHubTabKey) : undefined,
    sel: typeof s.sel === "string" && s.sel ? s.sel : undefined,
    integrazione: typeof s.integrazione === "string" && s.integrazione ? s.integrazione : undefined,
  }),
  component: function GitHubRoute() {
    const { tab, sel, integrazione } = githubRoute.useSearch();
    return <GitHubPage tab={tab ?? "integrazioni"} sel={sel} integration={integrazione} />;
  },
}); // i18n-ok: codice TypeScript letto come testo

// DevSecOps: connettore GitLab (Configurazione → Integrazioni → GitLab).
const GitLabPage = lazyRouteComponent(() => import("./pages/GitLab"), "GitLabPage");
type GitLabTabKey = "integrazioni" | "progetti" | "profili" | "sincronizzazioni";
const GITLAB_TAB_KEYS: GitLabTabKey[] = ["integrazioni", "progetti", "profili", "sincronizzazioni"];
export const gitlabRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/configurazione/integrazioni/gitlab",
  validateSearch: (s: Record<string, unknown>): { tab?: GitLabTabKey; sel?: string; integrazione?: string } => ({
    tab: GITLAB_TAB_KEYS.includes(s.tab as GitLabTabKey) ? (s.tab as GitLabTabKey) : undefined,
    sel: typeof s.sel === "string" && s.sel ? s.sel : undefined,
    integrazione: typeof s.integrazione === "string" && s.integrazione ? s.integrazione : undefined,
  }),
  component: function GitLabRoute() {
    const { tab, sel, integrazione } = gitlabRoute.useSearch();
    return <GitLabPage tab={tab ?? "integrazioni"} sel={sel} integration={integrazione} />;
  },
}); // i18n-ok: codice TypeScript letto come testo

// DevSecOps: connettore Gitea (Configurazione → Integrazioni → Gitea).
const GiteaPage = lazyRouteComponent(() => import("./pages/Gitea"), "GiteaPage");
type GiteaTabKey = "integrazioni" | "repository" | "profili" | "sincronizzazioni";
const GITEA_TAB_KEYS: GiteaTabKey[] = ["integrazioni", "repository", "profili", "sincronizzazioni"];
export const giteaRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/configurazione/integrazioni/gitea",
  validateSearch: (s: Record<string, unknown>): { tab?: GiteaTabKey; sel?: string; integrazione?: string } => ({
    tab: GITEA_TAB_KEYS.includes(s.tab as GiteaTabKey) ? (s.tab as GiteaTabKey) : undefined,
    sel: typeof s.sel === "string" && s.sel ? s.sel : undefined,
    integrazione: typeof s.integrazione === "string" && s.integrazione ? s.integrazione : undefined,
  }),
  component: function GiteaRoute() {
    const { tab, sel, integrazione } = giteaRoute.useSearch();
    return <GiteaPage tab={tab ?? "integrazioni"} sel={sel} integration={integrazione} />;
  },
}); // i18n-ok: codice TypeScript letto come testo

// Conformità (piano, passo 20): Governance → Conformità, caricata alla prima visita. Scheda,
// normativa, requisito aperto, perimetro e stato delle evidenze stanno nella query.
const CompliancePage = lazy(() => import("./pages/Compliance").then((m) => ({ default: m.Compliance }))); // i18n-ok: codice TypeScript letto come testo
const COMPLIANCE_TAB_KEYS = ["panoramica", "normative", "applicazioni", "controlli", "mappatura", "evidenze", "cataloghi", "regole"] as const;
type ComplianceTabKey = (typeof COMPLIANCE_TAB_KEYS)[number];
export const complianceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/conformita",
  validateSearch: (s: Record<string, unknown>): { tab?: ComplianceTabKey; normativa?: string; perimetro?: string; stato?: string; requisito?: string } => ({
    tab: COMPLIANCE_TAB_KEYS.includes(s.tab as ComplianceTabKey) ? (s.tab as ComplianceTabKey) : undefined,
    normativa: str(s.normativa),
    perimetro: str(s.perimetro),
    stato: str(s.stato),
    requisito: str(s.requisito),
  }),
  component: function ComplianceRoute() {
    const search = complianceRoute.useSearch();
    return (
      <Suspense fallback={workLoading}>
        <CompliancePage search={search} />
      </Suspense>
    );
  },
}); // i18n-ok: codice TypeScript letto come testo
// Spazio dell'auditor (ondata 3, C6): incarico, avanzamento, campioni, richieste e rilievi.
const AuditSpace = lazyRouteComponent(() => import("./pages/AuditSpace"), "AuditSpace");
export const auditSpaceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/audit-spazio",
  validateSearch: (s: Record<string, unknown>): { incarico?: string } => ({ incarico: str(s.incarico) }),
  component: function AuditSpaceRoute() {
    const { incarico } = auditSpaceRoute.useSearch();
    return <AuditSpace incarico={incarico} />;
  },
}); // i18n-ok: codice TypeScript letto come testo
// Vista DPO (ondata 3): trattamenti, DPIA, violazioni dei dati e flussi; si carica alla prima visita.
const PrivacyPage = lazy(() => import("./pages/Privacy").then((m) => ({ default: m.PrivacyPage }))); // i18n-ok: codice TypeScript letto come testo
const PRIVACY_TABS = ["trattamenti", "dpia", "violazioni", "flussi"] as const;
export const privacyRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/privacy",
  validateSearch: (s: Record<string, unknown>): { scheda?: (typeof PRIVACY_TABS)[number] } => ({
    scheda: PRIVACY_TABS.includes(s.scheda as (typeof PRIVACY_TABS)[number]) ? (s.scheda as (typeof PRIVACY_TABS)[number]) : undefined,
  }),
  component: function PrivacyRoute() {
    const { scheda } = privacyRoute.useSearch();
    return (
      <Suspense fallback={workLoading}>
        <PrivacyPage tab={scheda ?? "panoramica"} />
      </Suspense>
    );
  },
}); // i18n-ok: codice TypeScript letto come testo
const grcRoutes = [complianceRoute, auditSpaceRoute, privacyRoute];
// Import di progetti da Jira, YouTrack e CSV (docs/import.md); si carica alla prima visita.
const ImportHub = lazyRouteComponent(() => import("./pages/ImportHub"), "ImportHub");
const IMPORT_WHAT = ["lavoro", "applicazioni", "costi", "organizzazione"] as const;
export const importRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/configurazione/import",
  validateSearch: (s: Record<string, unknown>): { run?: string; cosa?: (typeof IMPORT_WHAT)[number] } => ({
    run: typeof s.run === "string" && s.run ? s.run : undefined,
    cosa: IMPORT_WHAT.find((k) => k === s.cosa),
  }),
  component: function ImportRoute() {
    const { run, cosa } = importRoute.useSearch();
    return <ImportHub runId={run} cosa={cosa} />;
  },
}); // i18n-ok: codice TypeScript letto come testo

// Liste con filtri visuali, viste salvate e tabella generica: impatti, applicazioni, asset,
// rischi, incidenti, controlli, documenti (piano, passo 16; pages/Lists.tsx).
// Indice degli elenchi (menu snello): la voce «Elenchi» del menu porta qui.
const listsIndexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/elenco", component: ListsIndex }); // i18n-ok: codice TypeScript letto come testo
export const listRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/elenco/$lista",
  validateSearch: validateListSearch,
  component: Lists,
}); // i18n-ok: codice TypeScript letto come testo

// Progetti (replica/progetti.md, passi 1 e 2): elenco, nuovo progetto, piano a tutta pagina, impostazioni.
const ProjectsPage = lazyRouteComponent(() => import("./pages/progetti/ProjectsList"), "ProjectsPage");
const NewProjectPage = lazyRouteComponent(() => import("./pages/progetti/ProjectsList"), "NewProjectPage");
const ProjectPlanRoute = lazyRouteComponent(() => import("./pages/progetti/ProjectPlan"), "ProjectPlanRoute");
const ProjectSettingsPage = lazyRouteComponent(() => import("./pages/progetti/ProjectSettings"), "ProjectSettingsPage");
const projectRoutes = [
  createRoute({ getParentRoute: () => rootRoute, path: "/progetti", component: ProjectsPage }), // i18n-ok: codice TypeScript letto come testo
  createRoute({ getParentRoute: () => rootRoute, path: "/progetti/nuovo", component: NewProjectPage }), // i18n-ok: codice TypeScript letto come testo
  createRoute({ getParentRoute: () => rootRoute, path: "/progetti/$key/piano", component: ProjectPlanRoute }), // i18n-ok: codice TypeScript letto come testo
  createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/progetti", component: ProjectSettingsPage }), // i18n-ok: codice TypeScript letto come testo
];

// AI (replica/ai.md, ondata 1): impostazioni e consumo, per chi amministra l'organizzazione.
const AiConfigPage = lazyRouteComponent(() => import("./pages/AiConfig"), "AiConfig");
const AiUsagePage = lazyRouteComponent(() => import("./pages/AiUsage"), "AiUsage");
const aiRoutes = [
  createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/ai", component: AiConfigPage }), // i18n-ok: codice TypeScript letto come testo
  createRoute({ getParentRoute: () => rootRoute, path: "/configurazione/ai/consumo", component: AiUsagePage }), // i18n-ok: codice TypeScript letto come testo
];

export const router = createRouter({
  routeTree: rootRoute.addChildren([
    indexRoute,
    listRoute,
    listsIndexRoute,
    requestsRoute,
    newRequestRoute,
    inboxRoute,
    todoRoute,
    processesRoute,
    processRoute,
    architectureRoute,
    roadmapRoute,
    planRoute,
    notificationsRoute,
    profileRoute,
    wikiRoute,
    wikiPageRoute,
    ...portfolioRoutes,
    financeRoute,
    budgetRoute,
    orgConfigRoute,
    ...reviewRoutes,
    ...elementRoutes,
    ...workRoutes,
    ...tempoRoutes,
    ...progressRoutes,
    ...requirementRoutes,
    ...accessRoutes,
    githubRoute,
    gitlabRoute,
    giteaRoute,
    ...devsecopsRoutes,
    ...vulnerabilityRoutes,
    ...pipelineRoutes,
    ...knowledgeRoutes,
    ...eaRoutes,
    risksRoute,
    crisisRoute,
    ...grcRoutes,
    importRoute,
    configHomeRoute,
    ...guidaAdminRoutes,
    coinvolgiRoute,
    ...projectRoutes,
    ...aiRoutes,
  ]),
  defaultPreload: "intent",
  defaultNotFoundComponent: NotFound,
}); // i18n-ok: codice TypeScript letto come testo

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
