// API dell'AI (replica/ai.md, ondata 1). Rispecchia internal/api/ai.go.
import { ApiError, call } from "./api";
import { fmtCurrency } from "./i18n/format";

const q = encodeURIComponent;

export type TierKey = "rapido" | "approfondito";
export type TierChoice = { provider_id: string; model: string };
export type Route = { tier?: TierKey; provider_id?: string; model?: string };

export type AISettings = {
  enabled: boolean;
  mask_personal_data: boolean;
  monthly_budget_usd: number | null;
  monthly_token_budget: number | null;
  retention_days: number;
  fast: TierChoice;
  deep: TierChoice;
  routes: Record<string, Route>;
  /** Server MCP acceso (interruttore suo, ondata 2). */
  mcp_enabled: boolean;
  saved: boolean;
  regulated: boolean;
};

export type AIProvider = {
  id: string;
  kind: string;
  name: string;
  base_url: string;
  has_key: boolean;
  no_tools_models: string[];
  price_input_usd: number | null;
  price_output_usd: number | null;
  local: boolean;
  /** Regione del cloud (Bedrock, Vertex) e progetto Google Cloud (Vertex): non sono segreti. */
  region: string;
  project_id: string;
};

/** Modello del fornitore; tools: il servizio dichiara se sa usare gli strumenti (Ollama). */
export type AIModel = { id: string; name?: string; tier?: TierKey; tools?: boolean; thinking?: boolean; size?: number };
export type AIKind = {
  key: string;
  needs_key: boolean;
  needs_url: boolean;
  /** L'indirizzo si può cambiare (OpenAI); default_url è quello proposto. */
  optional_url: boolean;
  default_url: string;
  /** «aws» (Bedrock) o «gcp» (Vertex): regione e credenziali del cloud al posto della chiave. */
  cloud: "" | "aws" | "gcp";
  external: boolean;
  tools: boolean;
  proposed: AIModel[];
};
/** Esito della prova di connessione (ondata 6). */
export type ProviderTest = { ok: boolean; models: AIModel[]; model?: string; no_tools: string[]; latency_ms: number };
export type AIFeature = { key: string; tier: TierKey; needs_tools: boolean; active: boolean };

export type AIBudget = {
  month: string;
  spent_usd: number;
  tokens: number;
  limit_usd: number | null;
  limit_tokens: number | null;
  percent: number;
  state: "none" | "ok" | "warn" | "blocked";
};

export type AISettingsView = {
  settings: AISettings;
  providers: AIProvider[];
  kinds: AIKind[];
  features: AIFeature[];
  budget: AIBudget;
  dev: boolean;
};

export type AIStatus = {
  enabled: boolean;
  ready: boolean;
  tools: boolean;
  model?: string;
  masked: boolean;
  budget: AIBudget;
  retention_days: number;
  /** Funzioni della scheda pronte (ondata 3): riassunti, mancanze, bozze, classificazione. */
  features?: Record<string, boolean>;
};

export type ProviderInput = {
  kind?: string;
  name: string;
  base_url?: string;
  api_key?: string;
  remove_key?: boolean;
  no_tools_models?: string[];
  price_input_usd?: number | null;
  price_output_usd?: number | null;
  region?: string;
  project_id?: string;
  aws_access_key_id?: string;
  aws_secret_access_key?: string;
  aws_session_token?: string;
  service_account_json?: string;
};

export type UsageRow = { key: string; label: string; calls: number; input_tokens: number; output_tokens: number; cost_usd: number; errors: number };
export type CallView = {
  id: string;
  at: string;
  member: string;
  feature: string;
  provider: string;
  model: string;
  tier: string;
  input_tokens: number;
  output_tokens: number;
  cost_usd: number;
  outcome: string;
  error_code: string;
  duration_ms: number;
  prompt_sha256: string;
};
export type AIUsage = { month: string; budget: AIBudget; total: UsageRow; by_feature: UsageRow[]; by_member: UsageRow[]; by_model: UsageRow[]; recent: CallView[] };

export type Ref = { id: string; key: string; title: string; type: string };
export type Proposal = { id: string; tool: string; summary: string; status: "pending" | "applied" | "rejected" | "failed"; error?: string; refs: Ref[]; created_at: string };
export type ChatMessage = { id: string; role: "user" | "assistant"; text: string; refs: Ref[]; proposals: Proposal[]; tools: string[]; created_at: string };
export type Conversation = { id: string; title: string; created_at: string; updated_at: string };
export type AskResult = { conversation: Conversation; messages: ChatMessage[]; status: AIStatus };

/** Errore della query con la posizione (unità UTF-16), come engine.QueryError. */
export type QueryError = { message: string; pos: number; end: number };
/** Domanda sul grafo tradotta in query (internal/ai/graph.go). */
export type GraphResult = {
  question: string;
  query: string;
  explanation: string;
  valid: boolean;
  corrected: boolean;
  first_query?: string;
  first_error?: QueryError;
  error?: QueryError;
  status: AIStatus;
};

/** Assistente nella scheda (internal/ai/card.go, ondata 3). */
export type DraftKind = "descrizione" | "commento" | "risposta";
export type CardGap = {
  kind: "campo" | "descrizione" | "relazione" | "domanda" | "incoerenza";
  text: string;
  field?: string;
  field_name?: string;
  comment_id?: string;
  comment_by?: string;
  ref?: Ref;
  required_by?: string;
  source: "regia" | "modello";
};
export type CardResult = { action: string; draft?: DraftKind; text?: string; gaps?: CardGap[]; refs: Ref[]; unverified?: number; status: AIStatus };
export type TriageField = { key: string; name: string; value: unknown; display: string; reason: string };
export type TriageResult = {
  duplicates: (Ref & { state: string; reason: string })[];
  fields: TriageField[];
  links: (Ref & { reason: string })[];
  candidates: number;
  unverified?: number;
  status: AIStatus;
};

/** Evento della risposta in streaming (internal/ai/assistant.go, AskEvent). */
export type AskEvent = { type: "text"; text: string } | { type: "tool"; tool: string } | { type: "step" };

/** Errore dell'evento «error» dello streaming: stesso formato degli errori dell'API. */
function streamError(p: { status?: number; code?: string; detail?: string; field?: string; params?: Record<string, string | number> }): ApiError {
  return new ApiError(p.status ?? 500, p.code ?? "unknown", p.detail ?? "", p.field, undefined, undefined, p.params);
}

/**
 * Domanda all'assistente con la risposta in streaming (Server-Sent Events): onEvent riceve i
 * pezzi del testo e gli strumenti usati, la promessa il risultato finale. Se arriva una risposta
 * JSON invece dello streaming, si usa quella.
 */
export async function askStream(question: string, conversation_id: string | undefined, onEvent: (e: AskEvent) => void, signal?: AbortSignal): Promise<AskResult> {
  const res = await fetch("/api/v1/ai/ask", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
    body: JSON.stringify({ question, conversation_id }),
    signal,
  });
  if (!res.ok || !res.body || !(res.headers.get("Content-Type") ?? "").startsWith("text/event-stream")) {
    if (!res.ok) {
      const problem = await res.json().catch(() => ({ status: res.status }));
      throw streamError({ status: res.status, ...problem });
    }
    // Risposta normale (JSON): un proxy ha tolto lo streaming, il risultato è già qui.
    return (await res.json()) as AskResult;
  }
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += value;
    let i: number;
    while ((i = buf.indexOf("\n\n")) >= 0) {
      const block = buf.slice(0, i);
      buf = buf.slice(i + 2);
      let event = "message";
      let data = "";
      for (const line of block.split("\n")) {
        if (line.startsWith("event: ")) event = line.slice(7);
        else if (line.startsWith("data: ")) data += line.slice(6);
      }
      if (!data) continue;
      const payload = JSON.parse(data);
      if (event === "done") return payload as AskResult;
      if (event === "error") throw streamError(payload);
      if (event === "text" || event === "tool" || event === "step") onEvent(payload as AskEvent);
    }
  }
  throw streamError({ status: 502, code: "ai.stream_interrupted" });
}

export const aiApi = {
  status: () => call<AIStatus>("GET", "/ai/status"),
  settings: () => call<AISettingsView>("GET", "/ai/settings"),
  saveSettings: (s: Omit<AISettings, "saved" | "regulated">) => call<AISettingsView>("PUT", "/ai/settings", s),
  createProvider: (p: ProviderInput) => call<AIProvider>("POST", "/ai/providers", p),
  updateProvider: (id: string, p: ProviderInput) => call<AIProvider>("PATCH", `/ai/providers/${q(id)}`, p),
  deleteProvider: (id: string) => call<void>("DELETE", `/ai/providers/${q(id)}`),
  models: (id: string) => call<{ items: AIModel[] }>("GET", `/ai/providers/${q(id)}/models`),
  testProvider: (id: string, model?: string) => call<ProviderTest>("POST", `/ai/providers/${q(id)}/test`, model ? { model } : {}),
  usage: (month?: string) => call<AIUsage>("GET", `/ai/usage${month ? `?month=${q(month)}` : ""}`),
  conversations: () => call<{ items: Conversation[] }>("GET", "/ai/conversations"),
  conversation: (id: string) => call<{ conversation: Conversation; messages: ChatMessage[] }>("GET", `/ai/conversations/${q(id)}`),
  deleteConversation: (id: string) => call<void>("DELETE", `/ai/conversations/${q(id)}`),
  ask: (question: string, conversation_id?: string) => call<AskResult>("POST", "/ai/ask", { question, conversation_id }),
  graph: (question: string) => call<GraphResult>("POST", "/ai/graph", { question }),
  card: (element: string, action: "riassunto" | "mancanze" | "bozza", draft?: DraftKind, hint?: string) =>
    call<CardResult>("POST", "/ai/card", { element, action, draft, hint }),
  cardInsert: (element: string, draft: DraftKind, text: string) => call<Proposal>("POST", "/ai/card/insert", { element, draft, text }),
  triage: (type: string, title: string, description: string) => call<TriageResult>("POST", "/ai/card/triage", { type, title, description }),
  cardLinks: (element: string, links: string[]) => call<{ items: { key: string; ok: boolean; error?: string }[] }>("POST", "/ai/card/links", { element, links }),
  confirm: (id: string) => call<Proposal>("POST", `/ai/proposals/${q(id)}/confirm`),
  reject: (id: string) => call<Proposal>("POST", `/ai/proposals/${q(id)}/reject`),
};

/** Dollari (il listino dei fornitori), con quattro decimali sotto il centesimo (stime piccole). */
export function usd(n: number): string {
  const digits = n !== 0 && Math.abs(n) < 0.01 ? 4 : 2;
  return fmtCurrency(n, "USD", { minimumFractionDigits: digits, maximumFractionDigits: digits });
}
