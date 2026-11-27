// API dell'AI (replica/ai.md, ondata 1). Rispecchia internal/api/ai.go.
import { call } from "./api";
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
};

export type AIModel = { id: string; name?: string; tier?: TierKey };
export type AIKind = { key: string; needs_key: boolean; needs_url: boolean; external: boolean; tools: boolean; proposed: AIModel[] };
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

export type AIStatus = { enabled: boolean; ready: boolean; tools: boolean; model?: string; masked: boolean; budget: AIBudget; retention_days: number };

export type ProviderInput = {
  kind?: string;
  name: string;
  base_url?: string;
  api_key?: string;
  remove_key?: boolean;
  no_tools_models?: string[];
  price_input_usd?: number | null;
  price_output_usd?: number | null;
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

export const aiApi = {
  status: () => call<AIStatus>("GET", "/ai/status"),
  settings: () => call<AISettingsView>("GET", "/ai/settings"),
  saveSettings: (s: Omit<AISettings, "saved" | "regulated">) => call<AISettingsView>("PUT", "/ai/settings", s),
  createProvider: (p: ProviderInput) => call<AIProvider>("POST", "/ai/providers", p),
  updateProvider: (id: string, p: ProviderInput) => call<AIProvider>("PATCH", `/ai/providers/${q(id)}`, p),
  deleteProvider: (id: string) => call<void>("DELETE", `/ai/providers/${q(id)}`),
  models: (id: string) => call<{ items: AIModel[] }>("GET", `/ai/providers/${q(id)}/models`),
  usage: (month?: string) => call<AIUsage>("GET", `/ai/usage${month ? `?month=${q(month)}` : ""}`),
  conversations: () => call<{ items: Conversation[] }>("GET", "/ai/conversations"),
  conversation: (id: string) => call<{ conversation: Conversation; messages: ChatMessage[] }>("GET", `/ai/conversations/${q(id)}`),
  deleteConversation: (id: string) => call<void>("DELETE", `/ai/conversations/${q(id)}`),
  ask: (question: string, conversation_id?: string) => call<AskResult>("POST", "/ai/ask", { question, conversation_id }),
  confirm: (id: string) => call<Proposal>("POST", `/ai/proposals/${q(id)}/confirm`),
  reject: (id: string) => call<Proposal>("POST", `/ai/proposals/${q(id)}/reject`),
};

/** Dollari (il listino dei fornitori), con quattro decimali sotto il centesimo (stime piccole). */
export function usd(n: number): string {
  const digits = n !== 0 && Math.abs(n) < 0.01 ? 4 : 2;
  return fmtCurrency(n, "USD", { minimumFractionDigits: digits, maximumFractionDigits: digits });
}
