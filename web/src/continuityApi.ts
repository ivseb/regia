// Client della continuità operativa (docs/sviluppo/continuita.md): BIA per capability, RTO/RPO
// richiesti contro dichiarati, incoerenze sul grafo, piani e test di resilienza. Tipi e funzioni
// pure stanno in continuityModel.ts e si riesportano da qui.
import { call } from "./api";
import type {
  ApplicationContinuity,
  BiaInput,
  BiaListItem,
  BiaView,
  Can,
  CapabilityContinuity,
  Config,
  ContinuityGraph,
  Overview,
  OverviewFilter,
  PlanRef,
  Settings,
  SettingsVersion,
  TestInput,
  TestPlan,
  TestView,
} from "./continuityModel";

export * from "./continuityModel";

const q = encodeURIComponent;
const B = "/continuity";

const qs = (f: Record<string, string | number | undefined>) => {
  const p = Object.entries(f).filter(([, v]) => v !== undefined && v !== "");
  return p.length ? `?${p.map(([k, v]) => `${k}=${q(String(v))}`).join("&")}` : "";
};

export const continuityApi = {
  settings: () => call<Settings>("GET", `${B}/settings`),
  saveSettings: (version: number, config: Config) => call<Settings>("PUT", `${B}/settings`, { version, config }),
  settingsVersions: () => call<{ items: SettingsVersion[] }>("GET", `${B}/settings/versions`),
  setup: () => call<{ ready: true }>("POST", `${B}/setup`),
  overview: (f: OverviewFilter = {}) => call<Overview>("GET", `${B}/overview${qs(f)}`),

  biaList: () => call<{ items: BiaListItem[]; can: Can }>("GET", `${B}/bia`),
  startBia: (capability: string) => call<BiaView>("POST", `${B}/bia`, { capability }),
  bia: (ref: string) => call<BiaView>("GET", `${B}/bia/${q(ref)}`),
  saveBia: (ref: string, b: BiaInput) => call<BiaView>("PUT", `${B}/bia/${q(ref)}`, b),
  approveBia: (ref: string, comment?: string) => call<BiaView>("POST", `${B}/bia/${q(ref)}/approve`, comment ? { comment } : {}),

  application: (ref: string) => call<ApplicationContinuity>("GET", `${B}/applications/${q(ref)}`),
  capability: (ref: string) => call<CapabilityContinuity>("GET", `${B}/capabilities/${q(ref)}`),
  graph: () => call<ContinuityGraph>("GET", `${B}/graph`),

  plans: () => call<{ items: PlanRef[] }>("GET", `${B}/plans`),
  addPlan: (page: string, target: string) => call<PlanRef>("POST", `${B}/plans`, { page, target }),
  removePlan: (page: string, target: string) => call<void>("DELETE", `${B}/plans?page=${q(page)}&target=${q(target)}`),

  tests: (year: number) => call<TestPlan>("GET", `${B}/tests?year=${year}`),
  createTest: (subject: string, type: string, planned_on: string, title?: string) =>
    call<TestView>("POST", `${B}/tests`, title ? { subject, type, planned_on, title } : { subject, type, planned_on }),
  test: (ref: string) => call<TestView>("GET", `${B}/tests/${q(ref)}`),
  saveTest: (ref: string, t: TestInput) => call<TestView>("PUT", `${B}/tests/${q(ref)}`, t),
};

