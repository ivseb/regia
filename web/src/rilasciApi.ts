// Rilasci (DevSecOps): gli elementi contenuti in un rilascio, «in produzione dal…» di un
// elemento e dei suoi discendenti, le impostazioni dei rilasci, la certificazione del rilascio
// per numero di change e le note per il business (AI, funzione Bozze). Server:
// internal/api/rilasci.go, internal/modules/devsecops/{release_items,certification,close_keywords}.go.
import { call } from "./api";
import type { Release, VulnSummary } from "./devsecopsApi";

export type NoteGroup = "new" | "fix" | "other";

export type ReleaseItem = {
  id: string;
  key: string;
  title: string;
  type: string;
  type_name: string;
  state: string;
  state_category: string;
  group: NoteGroup;
  commits: number;
  /** L'elemento ha commit successivi su questo servizio: il lavoro prosegue in un rilascio seguente. */
  continues: boolean;
};

export type ReleaseDetail = {
  release: Release;
  /** Intervallo dei commit: (from, to]. from nullo = primo rilascio del servizio nell'ambiente. */
  window: { from: string | null; to: string; previous_version: string | null };
  items: ReleaseItem[];
  /** Elementi del rilascio che chi guarda non può leggere. */
  hidden: number;
  commits: number;
};

export type ElementProduction = {
  in_production: boolean;
  at: string | null;
  version: string;
  service: string;
  release_id: string | null;
  first_at: string;
  last_at: string;
  /** Servizi con commit non ancora in produzione. */
  pending: string[];
};

export type ProductionItem = {
  key: string;
  title: string;
  type: string;
  type_name: string;
  state: string;
  state_category: string;
  production: ElementProduction | null;
};

export type Production = {
  self: ElementProduction | null;
  items: ProductionItem[];
  total: number;
  in_production: number;
  last_at: string | null;
};

export type CertSection = "change" | "releases" | "items" | "tests" | "sonar" | "security" | "gate";
export const CERT_SECTIONS: CertSection[] = ["change", "releases", "items", "tests", "sonar", "security", "gate"];

export type ReleaseSettings = {
  close_on_keywords: boolean;
  cert_sections: CertSection[];
  /** Eccezioni per spazio (senza riga lo spazio eredita). */
  spaces: { key: string; name: string; close_on_keywords: boolean }[];
  updated_at: string | null;
  can_configure: boolean;
};

export type ReleaseSettingsInput = {
  close_on_keywords: boolean;
  cert_sections?: CertSection[];
  /** Tutte le eccezioni: sostituiscono le precedenti; close_on_keywords nullo = eredita. */
  spaces?: { key: string; close_on_keywords: boolean | null }[];
};

export type TestRun = {
  /** pipeline oggi; poi alm e test management nativo. */
  origin: string;
  service: string;
  version: string;
  name: string;
  outcome: string;
  summary: string;
  url: string;
  executed: number | null;
  passed: number | null;
  failed: number | null;
  skipped: number | null;
  /** Test pianificati (test set di ALM e test management); assenti per la pipeline. */
  planned?: number | null;
  blocked?: number | null;
  not_run?: number | null;
  /** Per i test set: progetto · ciclo · release. */
  detail?: string;
};

export type TestTotals = {
  executed: number;
  passed: number;
  failed: number;
  skipped: number;
  /** Assenti nelle istantanee più vecchie. */
  planned?: number;
  blocked?: number;
  not_run?: number;
  counted: boolean;
};

export type DefectSeverity = "critical" | "high" | "medium" | "low" | "unknown";

/** Difetti aperti dei test del change (assenti nelle istantanee più vecchie). */
export type CertDefects = {
  open: number;
  by_severity: Partial<Record<DefectSeverity, number>>;
  /** Al massimo 50, i più gravi prima. */
  items: { key: string; summary: string; severity: string; raw_severity: string; status: string; project: string; url: string }[];
};

export type Certification = {
  change: {
    id: string;
    number: string;
    aliases: string[];
    title: string;
    request: { key: string; title: string; state: string } | null;
    /** La change letta dal connettore ServiceNow, se c'è (assente nelle istantanee più vecchie). */
    servicenow?: ServiceNowChange | null;
  };
  sections: CertSection[];
  releases: Release[];
  items: ReleaseItem[];
  hidden: number;
  tests: TestRun[];
  test_totals: TestTotals;
  defects?: CertDefects | null;
  sonar: { service: string; version: string; name: string; outcome: string; summary: string; url: string }[];
  security: { service: string; version: string; environment: string; cves: VulnSummary | null }[];
  gate: { service: string; environment: string; version: string; outcome: "pass" | "warn" | "fail"; overridden: boolean; at: string }[];
  approvals: { service: string; version: string; environment: string; requested_by: string | null; approved_by: string | null; approved_at: string | null }[];
  /** Quando i dati sono stati letti (per un'istantanea: quando è stata generata). */
  at: string;
  /** L'istantanea mostrata; nullo = dati dal vivo. */
  version: CertVersion | null;
  generated: CertVersion[];
  can_generate: boolean;
  /** Indirizzo stabile della pagina, da mettere nella change. */
  path: string;
  /** C'è un controllo GRC mappato sulla sorgente «certification»: la generazione registra un'evidenza. */
  evidence_mapped: boolean;
};

/** Change di ServiceNow: state è il valore grezzo (-5 nuova … -2 pianificata, 3 chiusa, 4 annullata). */
export type ServiceNowChange = {
  state: string;
  /** not requested | requested | approved | rejected (vuoto se manca). */
  approval: string;
  type: string;
  planned_start?: string | null;
  planned_end?: string | null;
  actual_start?: string | null;
  actual_end?: string | null;
  url: string;
  updated_at?: string | null;
};

export type CertVersion = { version: number; at: string; by: string | null; sha256: string; evidence_key: string | null };

const q = encodeURIComponent;

export const rilasciApi = {
  release: (id: string) => call<ReleaseDetail>("GET", `/devsecops/releases/${q(id)}`),
  production: (ref: string) => call<Production>("GET", `/elements/${q(ref)}/production`),
  settings: () => call<ReleaseSettings>("GET", "/devsecops/release-settings"),
  saveSettings: (s: ReleaseSettingsInput) => call<ReleaseSettings>("PUT", "/devsecops/release-settings", s),
  certification: (number: string, version?: number) =>
    call<Certification>("GET", `/devsecops/changes/${q(number)}/certification${version ? `?version=${version}` : ""}`),
  generateCertification: (number: string) => call<Certification>("POST", `/devsecops/changes/${q(number)}/certification`),
  businessNotes: (release: string, hint?: string) => call<{ text: string }>("POST", "/ai/release-notes", { release, hint: hint ?? "" }),
};
