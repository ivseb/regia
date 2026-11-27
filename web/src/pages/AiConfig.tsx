// Configurazione → Intelligenza artificiale (replica/ai.md §3, §4, §7): fornitori con la chiave
// cifrata (non torna mai), livelli «rapido» e «approfondito», funzioni spostate su un altro livello
// o modello, budget mensile, mascheramento dei dati personali, conservazione delle conversazioni.
// Solo per chi amministra l'organizzazione. L'AI parte spenta.
import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";
import { aiApi, REASONINGS, usd, type Reasoning, type AIKind, type AIModel, type AIProvider, type AISettings, type AISettingsView, type ProviderInput, type Route, type TierChoice, type TierKey } from "../aiApi";
import { PageHeader } from "../shell";
import { Select } from "../select";
import { ErrorNote, Loading, SectionTitle } from "../ui";
import { useT } from "../i18n/react";
import { lookup } from "../i18n/core";
import { fmtNumber } from "../i18n/format";

/** Regioni d'esempio (codici dei cloud, non testi): una europea per ciascuno. */
const REGION_EXAMPLE = { aws: "eu-central-1", gcp: "europe-west8" } as const;

const kindName = (k: string) => lookup(`ai.kind.${k}`) ?? k;
const featureName = (k: string) => lookup(`ai.feature.${k}`) ?? k;
const tierName = (k: string) => lookup(`ai.tier.${k}`) ?? k;

/** Modelli del fornitore: quelli proposti da Regia subito, quelli del servizio dopo la prova. */
function useModels(view: AISettingsView | undefined, providerId: string, loaded: Record<string, AIModel[]>): AIModel[] {
  const p = view?.providers.find((x) => x.id === providerId);
  if (!p) return [];
  return loaded[p.id] ?? view?.kinds.find((k) => k.key === p.kind)?.proposed ?? [];
}

/** Modello proposto per un livello: quello segnato dal servizio o da Regia; per il rapido, se
 *  manca, quello dell'approfondito (un solo modello per tutti e due i livelli va bene). */
function proposedFor(models: AIModel[], tier: TierKey | undefined): string {
  if (!tier) return "";
  const pick = models.find((m) => m.tier === tier)?.id;
  if (pick) return pick;
  return tier === "rapido" ? (models.find((m) => m.tier === "approfondito")?.id ?? "") : "";
}

/** Il modello non sa usare gli strumenti: lo dice il servizio o l'ha segnato chi amministra. */
function noTools(p: AIProvider | undefined, models: AIModel[], id: string): boolean {
  if (!p || !id) return false;
  return p.no_tools_models.includes(id) || models.some((m) => m.id === id && m.tools === false);
}

/** Scelta del ragionamento: vuoto = automatico (o, per una funzione, come il livello). */
function ReasoningSelect(props: { id: string; label: string; value: Reasoning | undefined; emptyLabel: string; srOnly?: boolean; onChange: (r: Reasoning) => void }) {
  const t = useT();
  return (
    <label className="flex min-w-0 max-w-xs flex-col gap-1">
      <span className={props.srOnly ? "sr-only" : "label"}>{props.label}</span>
      <Select className="field" data-testid={`ragionamento-${props.id}`} value={props.value ?? ""} onChange={(e) => props.onChange(e.target.value as Reasoning)}>
        {REASONINGS.map((r) => (
          <option key={r} value={r}>{r === "" ? props.emptyLabel : t(`ai.cfg.reasoning.${r}`)}</option>
        ))}
      </Select>
    </label>
  );
}

function TierPicker(props: {
  id: string;
  label: string;
  help: string;
  value: TierChoice;
  view: AISettingsView;
  loaded: Record<string, AIModel[]>;
  tier?: TierKey;
  /** Mostra la scelta del ragionamento (sui livelli; per le funzioni la mostra RouteRow). */
  reasoning?: boolean;
  onChange: (v: TierChoice) => void;
}) {
  const t = useT();
  const { id, value, view, onChange } = props;
  const models = useModels(view, value.provider_id, props.loaded);
  const prov = view.providers.find((x) => x.id === value.provider_id);
  return (
    <fieldset className="m-0 flex flex-col gap-2 border border-hair p-3" data-testid={`livello-${id}`}>
      <legend className="px-1 font-semibold">{props.label}</legend>
      <span className="text-[12.5px] text-muted">{props.help}</span>
      <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
        <label className="flex min-w-0 flex-col gap-1">
          <span className="label">{t("ai.cfg.provider")}</span>
          <Select
            className="field"
            value={value.provider_id}
            onChange={(e) => {
              const pid = e.target.value;
              const p = view.providers.find((x) => x.id === pid);
              const known = props.loaded[pid] ?? view.kinds.find((k) => k.key === p?.kind)?.proposed ?? [];
              onChange({ ...value, provider_id: pid, model: pid ? proposedFor(known, props.tier) : "" });
            }}
          >
            <option value="">{t("ai.cfg.none")}</option>
            {view.providers.map((p) => (
              <option key={p.id} value={p.id}>{p.name}</option>
            ))}
          </Select>
        </label>
        <label className="flex min-w-0 flex-col gap-1">
          <span className="label">{t("ai.cfg.model")}</span>
          <input
            className="field"
            list={`modelli-${id}`}
            value={value.model}
            disabled={!value.provider_id}
            maxLength={200}
            onChange={(e) => onChange({ ...value, model: e.target.value })}
          />
          <datalist id={`modelli-${id}`}>
            {models.map((m) => (
              <option key={m.id} value={m.id}>
                {(m.name ?? m.id) + (noTools(prov, models, m.id) ? ` · ${t("ai.cfg6.noToolsTag")}` : "")}
              </option>
            ))}
          </datalist>
        </label>
      </div>
      {noTools(prov, models, value.model) && (
        <span className="text-[12.5px]" data-testid="modello-senza-strumenti">{t("ai.cfg6.modelNoTools")}</span>
      )}
      {props.reasoning && (
        <ReasoningSelect id={id} label={t("ai.cfg.reasoning")} value={value.reasoning} emptyLabel={t("ai.cfg.reasoning.auto")}
          onChange={(reasoning) => onChange({ ...value, reasoning })} />
      )}
    </fieldset>
  );
}

type Draft = Omit<AISettings, "saved" | "regulated">;

/** Indirizzo del server MCP di questa installazione (internal/api/ai.go, MCPPath). */
export function mcpURL(): string {
  return `${window.location.origin}/api/v1/mcp`;
}

function toDraft(s: AISettings): Draft {
  const { saved: _s, regulated: _r, ...d } = s;
  return { ...d, routes: { ...d.routes } };
}

function ProviderForm({ kinds, provider, onDone, onCancel }: { kinds: AIKind[]; provider?: AIProvider; onDone: () => void; onCancel?: () => void }) {
  const t = useT();
  const [kind, setKind] = useState(provider?.kind ?? kinds[0]?.key ?? "");
  const [name, setName] = useState(provider?.name ?? "");
  const [baseURL, setBaseURL] = useState(provider?.base_url ?? "");
  const [key, setKey] = useState("");
  const [removeKey, setRemoveKey] = useState(false);
  const [noTools, setNoTools] = useState((provider?.no_tools_models ?? []).join(", "));
  const [priceIn, setPriceIn] = useState(provider?.price_input_usd?.toString() ?? "");
  const [priceOut, setPriceOut] = useState(provider?.price_output_usd?.toString() ?? "");
  const [region, setRegion] = useState(provider?.region ?? "");
  const [projectId, setProjectId] = useState(provider?.project_id ?? "");
  const [awsMode, setAwsMode] = useState<"key" | "iam">("key");
  const [awsKeyId, setAwsKeyId] = useState("");
  const [awsSecret, setAwsSecret] = useState("");
  const [awsToken, setAwsToken] = useState("");
  const [saJSON, setSaJSON] = useState("");
  const k = kinds.find((x) => x.key === kind);
  const cloud = k?.cloud ?? "";
  const num = (s: string) => (s.trim() === "" ? null : Number(s.replace(",", ".")));
  const save = useMutation({
    mutationFn: () => {
      const body: ProviderInput = {
        name,
        base_url: baseURL,
        api_key: cloud === "aws" && awsMode === "iam" ? "" : key,
        remove_key: removeKey,
        no_tools_models: noTools.split(/[,\n]/).map((s) => s.trim()).filter(Boolean),
        price_input_usd: num(priceIn),
        price_output_usd: num(priceOut),
        region,
        project_id: projectId,
      };
      if (cloud === "aws" && awsMode === "iam") Object.assign(body, { aws_access_key_id: awsKeyId, aws_secret_access_key: awsSecret, aws_session_token: awsToken });
      if (cloud === "gcp") body.service_account_json = saJSON;
      return provider ? aiApi.updateProvider(provider.id, body) : aiApi.createProvider({ ...body, kind });
    },
    onSuccess: () => {
      setKey("");
      setAwsSecret("");
      setAwsToken("");
      setSaJSON("");
      onDone();
    },
  });
  const err = save.error instanceof ApiError ? save.error : null;
  const bad = (f: string) => err?.field === f;
  const savedHint = provider?.has_key ? t("ai.cfg6.credsSaved") : "";
  const secretInput = (id: string, label: string, value: string, set: (v: string) => void, field: string, help?: string) => (
    <label className="flex flex-col gap-1">
      <span className="label">{label}</span>
      <input
        className="field font-mono"
        type="password"
        autoComplete="off"
        spellCheck={false}
        data-testid={id}
        value={value}
        aria-invalid={bad(field)}
        placeholder={savedHint}
        onChange={(e) => set(e.target.value)}
      />
      {help && <span className="text-[12.5px] text-muted">{help}</span>}
    </label>
  );
  return (
    <form
      className="flex flex-col gap-3 border-2 border-rule p-3"
      aria-label={provider ? t("ai.cfg.editProvider", { name: provider.name }) : t("ai.cfg.addProvider")}
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      {!provider && (
        <label className="flex flex-col gap-1">
          <span className="label">{t("ai.cfg.kind")}</span>
          <Select className="field" value={kind} onChange={(e) => setKind(e.target.value)}>
            {kinds.map((x) => (
              <option key={x.key} value={x.key}>{kindName(x.key)}</option>
            ))}
          </Select>
          <span className="text-[12.5px] text-muted">{lookup(`ai.kind.${kind}.help`) ?? ""}</span>
        </label>
      )}
      <label className="flex flex-col gap-1">
        <span className="label">{t("ai.cfg.name")}</span>
        <input className="field" required maxLength={80} value={name} aria-invalid={bad("name")} onChange={(e) => setName(e.target.value)} />
      </label>
      {(k?.needs_url || k?.optional_url) && (
        <label className="flex flex-col gap-1">
          <span className="label">{k?.needs_url ? t("ai.cfg.baseUrl") : t("ai.cfg6.baseUrlOptional")}</span>
          <input
            className="field"
            required={k?.needs_url}
            type="url"
            value={baseURL}
            aria-invalid={bad("base_url")}
            placeholder={k?.default_url || t("ai.cfg.baseUrlPlaceholder")}
            onChange={(e) => setBaseURL(e.target.value)}
          />
          <span className="text-[12.5px] text-muted">{lookup(`ai.kind.${kind}.url`) ?? t("ai.cfg.baseUrlHelp")}</span>
        </label>
      )}
      {cloud !== "" && (
        <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
          <label className="flex flex-col gap-1">
            <span className="label">{t("ai.cfg6.region")}</span>
            <input
              className="field font-mono"
              required
              maxLength={40}
              value={region}
              aria-invalid={bad("region")}
              placeholder={REGION_EXAMPLE[cloud]}
              onChange={(e) => setRegion(e.target.value.trim())}
            />
            <span className="text-[12.5px] text-muted">{cloud === "aws" ? t("ai.cfg6.regionAws") : t("ai.cfg6.regionGcp")}</span>
          </label>
          {cloud === "gcp" && (
            <label className="flex flex-col gap-1">
              <span className="label">{t("ai.cfg6.project")}</span>
              <input className="field font-mono" maxLength={30} value={projectId} aria-invalid={bad("project_id")} onChange={(e) => setProjectId(e.target.value.trim())} />
              <span className="text-[12.5px] text-muted">{t("ai.cfg6.projectHelp")}</span>
            </label>
          )}
        </div>
      )}
      {cloud === "aws" && (
        <fieldset className="m-0 flex flex-col gap-2 border border-hair p-3">
          <legend className="px-1 font-semibold">{t("ai.cfg6.awsCreds")}</legend>
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            {(["key", "iam"] as const).map((m) => (
              <label key={m} className="flex min-h-[44px] items-center gap-2">
                <input type="radio" name="aws-mode" checked={awsMode === m} onChange={() => setAwsMode(m)} />
                {t(m === "key" ? "ai.cfg6.awsModeKey" : "ai.cfg6.awsModeIam")}
              </label>
            ))}
          </div>
          {awsMode === "key" ? (
            secretInput("bedrock-chiave", t("ai.cfg6.awsApiKey"), key, setKey, "api_key", t("ai.cfg6.awsApiKeyHelp"))
          ) : (
            <>
              <label className="flex flex-col gap-1">
                <span className="label">{t("ai.cfg6.awsKeyId")}</span>
                <input className="field font-mono" autoComplete="off" spellCheck={false} value={awsKeyId} aria-invalid={bad("aws_access_key_id")} placeholder={savedHint} onChange={(e) => setAwsKeyId(e.target.value.trim())} />
              </label>
              {secretInput("bedrock-segreta", t("ai.cfg6.awsSecret"), awsSecret, setAwsSecret, "aws_secret_access_key")}
              {secretInput("bedrock-sessione", t("ai.cfg6.awsToken"), awsToken, setAwsToken, "aws_session_token", t("ai.cfg6.awsTokenHelp"))}
            </>
          )}
          <span className="text-[12.5px] text-muted">{t("ai.cfg.keyHelp")}</span>
        </fieldset>
      )}
      {cloud === "gcp" && (
        <label className="flex flex-col gap-1">
          <span className="label">{t("ai.cfg6.serviceAccount")}</span>
          <textarea
            className="field min-h-[7rem] font-mono text-[12.5px]"
            autoComplete="off"
            spellCheck={false}
            data-testid="vertex-chiave"
            value={saJSON}
            aria-invalid={bad("service_account_json")}
            placeholder={savedHint || t("ai.cfg6.serviceAccountPlaceholder")}
            onChange={(e) => setSaJSON(e.target.value)}
          />
          <span className="text-[12.5px] text-muted">{t("ai.cfg6.serviceAccountHelp")}</span>
        </label>
      )}
      {cloud === "" && (k?.needs_key || k?.needs_url || k?.optional_url) && (
        <label className="flex flex-col gap-1">
          <span className="label">{k?.needs_key ? t("ai.cfg.key") : t("ai.cfg.keyOptional")}</span>
          <input
            className="field font-mono"
            type="password"
            autoComplete="off"
            spellCheck={false}
            value={key}
            aria-invalid={bad("api_key")}
            placeholder={provider?.has_key ? t("ai.cfg.keySaved") : ""}
            onChange={(e) => setKey(e.target.value)}
          />
          <span className="text-[12.5px] text-muted">{t("ai.cfg.keyHelp")}</span>
        </label>
      )}
      {provider?.has_key && !k?.needs_key && (
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={removeKey} onChange={(e) => setRemoveKey(e.target.checked)} />
          {t("ai.cfg.removeKey")}
        </label>
      )}
      {!k?.tools && (
        <label className="flex flex-col gap-1">
          <span className="label">{t("ai.cfg.noTools")}</span>
          <input className="field font-mono" value={noTools} onChange={(e) => setNoTools(e.target.value)} />
          <span className="text-[12.5px] text-muted">{t("ai.cfg.noToolsHelp")}</span>
        </label>
      )}
      <div className="grid grid-cols-1 gap-2 md:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="label">{t("ai.cfg.priceIn")}</span>
          <input className="field" inputMode="decimal" value={priceIn} aria-invalid={bad("price_input_usd")} onChange={(e) => setPriceIn(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("ai.cfg.priceOut")}</span>
          <input className="field" inputMode="decimal" value={priceOut} onChange={(e) => setPriceOut(e.target.value)} />
        </label>
      </div>
      <span className="text-[12.5px] text-muted">{cloud ? t("ai.cfg6.priceHelpCloud") : t("ai.cfg.priceHelp")}</span>
      <ErrorNote error={save.error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" type="submit" disabled={save.isPending}>
          {save.isPending ? t("ai.cfg.saving") : provider ? t("ai.cfg.saveProvider") : t("ai.cfg.addProvider")}
        </button>
        {onCancel && (
          <button type="button" className="btn" onClick={onCancel}>{t("ai.cfg.cancel")}</button>
        )}
      </div>
    </form>
  );
}

/** Motivo dell'errore del fornitore (ai.ProviderError.Code) nelle parole di chi amministra. */
function ProviderReason({ error }: { error: unknown }) {
  if (!(error instanceof ApiError) || error.code !== "ai.provider_error") return null;
  const reason = lookup(`ai.reason.${String(error.params?.reason ?? "")}`);
  return reason ? <p className="m-0 text-[13px]">{reason}</p> : null;
}

function ProviderRow({ p, view, onModels, onChanged }: { p: AIProvider; view: AISettingsView; onModels: (id: string, m: AIModel[]) => void; onChanged: () => void }) {
  const t = useT();
  const [editing, setEditing] = useState(false);
  const test = useMutation({
    mutationFn: () => aiApi.testProvider(p.id),
    onSuccess: (r) => {
      onModels(p.id, r.models);
      if (r.no_tools.length > 0) onChanged();
    },
  });
  const del = useMutation({ mutationFn: () => aiApi.deleteProvider(p.id), onSuccess: onChanged });
  if (editing) {
    return <ProviderForm kinds={view.kinds} provider={p} onDone={() => { setEditing(false); onChanged(); }} onCancel={() => setEditing(false)} />;
  }
  return (
    <li className="flex flex-col gap-2 border-b border-hair py-3" data-testid="fornitore">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="font-semibold">{p.name}</span>
        <span className="text-[12.5px] text-muted">{kindName(p.kind)}</span>
        {p.base_url && <span className="font-mono text-[12px] text-muted [overflow-wrap:anywhere]">{p.base_url}</span>}
        {p.region && <span className="font-mono text-[12px] text-muted" data-testid="fornitore-regione">{p.project_id ? `${p.project_id} · ${p.region}` : p.region}</span>}
        <span className="text-[12.5px] text-muted">{p.has_key ? t("ai.cfg.hasKey") : t("ai.cfg.noKey")}</span>
        <span className="text-[12.5px] text-muted">{p.local ? t("ai.cfg.local") : t("ai.cfg.external")}</span>
      </div>
      <div className="flex flex-wrap gap-2">
        <button className="btn py-1" disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? t("ai.cfg.checking") : t("ai.cfg6.test")}
        </button>
        <button className="btn py-1" onClick={() => setEditing(true)}>{t("ai.cfg.edit")}</button>
        <button className="btn btn-danger py-1" disabled={del.isPending} onClick={() => del.mutate()}>{t("ai.cfg.remove")}</button>
      </div>
      {test.data && (
        <div className="flex flex-col gap-1 text-[12.5px]" role="status" data-testid="prova-esito">
          <span className="text-ok">
            {t("ai.cfg6.testOk", { ms: fmtNumber(test.data.latency_ms) })} {t("ai.cfg.modelsFound", { count: test.data.models.length })}
            {test.data.models.length > 0 ? `: ${test.data.models.slice(0, 12).map((m) => m.id).join(", ")}` : ""}
          </span>
          {test.data.model && <span>{t("ai.cfg6.testModel", { model: test.data.model })}</span>}
          {test.data.no_tools.length > 0 && <span>{t("ai.cfg6.testNoTools", { models: test.data.no_tools.join(", ") })}</span>}
        </div>
      )}
      <ErrorNote error={test.error ?? del.error} />
      <ProviderReason error={test.error} />
    </li>
  );
}

function RouteRow({ f, route, view, loaded, onChange }: { f: AISettingsView["features"][number]; route: Route | undefined; view: AISettingsView; loaded: Record<string, AIModel[]>; onChange: (r: Route | undefined) => void }) {
  const t = useT();
  const mode = route?.provider_id !== undefined && route.provider_id !== "" ? "model" : route?.model !== undefined ? "model" : (route?.tier ?? "");
  const choice: TierChoice = { provider_id: route?.provider_id ?? "", model: route?.model ?? "" };
  const reasoning = route?.reasoning || undefined;
  // Il ragionamento della funzione resta anche quando si cambia livello o modello.
  const keep = (r: Route | undefined): Route | undefined => (reasoning ? { ...(r ?? {}), reasoning } : r);
  return (
    <li className="flex flex-col gap-2 border-b border-hair py-3" data-testid={`funzione-${f.key}`}>
      <div className="flex flex-wrap items-baseline gap-x-3">
        <span className="font-semibold">{featureName(f.key)}</span>
        <span className="text-[12.5px] text-muted">{lookup(`ai.feature.${f.key}.d`) ?? ""}</span>
        {!f.active && <span className="font-mono text-[11.5px] text-muted">{t("ai.cfg.soon")}</span>}
      </div>
      <label className="flex max-w-md flex-col gap-1">
        <span className="sr-only">{t("ai.cfg.routeFor", { name: featureName(f.key) })}</span>
        <Select
          className="field"
          value={mode}
          onChange={(e) => {
            const v = e.target.value;
            if (v === "") onChange(keep(undefined));
            else if (v === "model") onChange(keep({ provider_id: "", model: "" }));
            else onChange(keep({ tier: v as TierKey }));
          }}
        >
          <option value="">{t("ai.cfg.routeDefault", { tier: tierName(f.tier) })}</option>
          <option value="rapido">{tierName("rapido")}</option>
          <option value="approfondito">{tierName("approfondito")}</option>
          <option value="model">{t("ai.cfg.routeModel")}</option>
        </Select>
      </label>
      {mode === "model" && (
        <TierPicker id={`f-${f.key}`} label={t("ai.cfg.routeModel")} help={t("ai.cfg.routeModelHelp")} value={choice} view={view} loaded={loaded}
          onChange={(c) => onChange(keep({ provider_id: c.provider_id, model: c.model }))} />
      )}
      <ReasoningSelect id={`f-${f.key}`} srOnly label={t("ai.cfg.reasoningFor", { name: featureName(f.key) })} value={reasoning} emptyLabel={t("ai.cfg.reasoningTier")}
        onChange={(r) => {
          const base: Route = { ...(route ?? {}) };
          delete base.reasoning;
          const next: Route = r ? { ...base, reasoning: r } : base;
          onChange(Object.keys(next).length ? next : undefined);
        }} />
    </li>
  );
}

export function AiConfig() {
  const t = useT();
  const qc = useQueryClient();
  const view = useQuery({ queryKey: ["ai-settings"], queryFn: aiApi.settings });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [loaded, setLoaded] = useState<Record<string, AIModel[]>>({});
  const [adding, setAdding] = useState(false);
  const [budgetUSD, setBudgetUSD] = useState("");
  const [budgetTokens, setBudgetTokens] = useState("");
  useEffect(() => {
    if (view.data && !draft) {
      setDraft(toDraft(view.data.settings));
      setBudgetUSD(view.data.settings.monthly_budget_usd?.toString() ?? "");
      setBudgetTokens(view.data.settings.monthly_token_budget?.toString() ?? "");
    }
  }, [view.data, draft]);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["ai-settings"] });
    qc.invalidateQueries({ queryKey: ["ai-status"] });
  };
  const save = useMutation({
    mutationFn: (d: Draft) => aiApi.saveSettings(d),
    onSuccess: (v) => {
      qc.setQueryData(["ai-settings"], v);
      qc.invalidateQueries({ queryKey: ["ai-status"] });
      setDraft(toDraft(v.settings));
    },
  });

  if (view.error instanceof ApiError && view.error.status === 403) {
    return (
      <>
        <PageHeader path={t("ai.cfg.path")} title={t("ai.cfg.title")} />
        <p className="px-4 text-muted md:px-7">{t("ai.cfg.adminOnly")}</p>
      </>
    );
  }
  const v = view.data;
  const num = (s: string) => (s.trim() === "" ? null : Number(s.replace(",", ".")));
  const set = (patch: Partial<Draft>) => setDraft((d) => (d ? { ...d, ...patch } : d));
  const onModels = (id: string, m: AIModel[]) => setLoaded((x) => ({ ...x, [id]: m }));
  const budget = v?.budget;

  return (
    <>
      <PageHeader
        path={t("ai.cfg.path")}
        title={t("ai.cfg.title")}
        actions={<Link to="/configurazione/ai/consumo" className="btn">{t("ai.cfg.usageLink")}</Link>}
      />
      <div className="flex max-w-[820px] flex-col gap-6 px-4 pb-10 md:px-7">
        <p className="m-0 text-muted">{t("ai.cfg.intro")}</p>
        {view.isPending && <Loading />}
        <ErrorNote error={view.error} />
        {v && draft && (
          <>
            <section aria-labelledby="ai-fornitori" className="flex flex-col gap-2">
              <SectionTitle><span id="ai-fornitori">{t("ai.cfg.providers")}</span></SectionTitle>
              <p className="m-0 text-[13px] text-muted">{t("ai.cfg.providersHelp")}</p>
              {v.providers.length === 0 && <p className="m-0 text-muted">{t("ai.cfg.noProviders")}</p>}
              <ul className="m-0 list-none p-0">
                {v.providers.map((p) => (
                  <ProviderRow key={p.id} p={p} view={v} onModels={onModels} onChanged={refresh} />
                ))}
              </ul>
              {adding ? (
                <ProviderForm kinds={v.kinds} onDone={() => { setAdding(false); refresh(); }} onCancel={() => setAdding(false)} />
              ) : (
                <div>
                  <button className="btn" onClick={() => setAdding(true)}>{t("ai.cfg.addProvider")}</button>
                </div>
              )}
            </section>

            <form
              className="flex flex-col gap-6"
              aria-label={t("ai.cfg.settings")}
              onSubmit={(e) => {
                e.preventDefault();
                save.mutate({ ...draft, monthly_budget_usd: num(budgetUSD), monthly_token_budget: num(budgetTokens) });
              }}
            >
              <section aria-labelledby="ai-livelli" className="flex flex-col gap-3">
                <SectionTitle><span id="ai-livelli">{t("ai.cfg.tiers")}</span></SectionTitle>
                <p className="m-0 text-[13px] text-muted">{t("ai.cfg.tiersHelp")}</p>
                <TierPicker id="approfondito" tier="approfondito" reasoning label={tierName("approfondito")} help={t("ai.cfg.deepHelp")} value={draft.deep} view={v} loaded={loaded} onChange={(deep) => set({ deep })} />
                <TierPicker id="rapido" tier="rapido" reasoning label={tierName("rapido")} help={t("ai.cfg.fastHelp")} value={draft.fast} view={v} loaded={loaded} onChange={(fast) => set({ fast })} />
                <p className="m-0 text-[12.5px] text-muted">{t("ai.cfg.reasoningHelp")}</p>
              </section>

              <section aria-labelledby="ai-funzioni" className="flex flex-col gap-2">
                <SectionTitle><span id="ai-funzioni">{t("ai.cfg.features")}</span></SectionTitle>
                <p className="m-0 text-[13px] text-muted">{t("ai.cfg.featuresHelp")}</p>
                <ul className="m-0 list-none p-0">
                  {v.features.map((f) => (
                    <RouteRow
                      key={f.key}
                      f={f}
                      route={draft.routes[f.key]}
                      view={v}
                      loaded={loaded}
                      onChange={(r) => {
                        const routes = { ...draft.routes };
                        if (r) routes[f.key] = r;
                        else delete routes[f.key];
                        set({ routes });
                      }}
                    />
                  ))}
                </ul>
              </section>

              <section aria-labelledby="ai-budget" className="flex flex-col gap-3">
                <SectionTitle><span id="ai-budget">{t("ai.cfg.budget")}</span></SectionTitle>
                <p className="m-0 text-[13px] text-muted">{t("ai.cfg.budgetHelp")}</p>
                <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                  <label className="flex flex-col gap-1">
                    <span className="label">{t("ai.cfg.budgetUsd")}</span>
                    <input className="field" inputMode="decimal" value={budgetUSD} onChange={(e) => setBudgetUSD(e.target.value)} />
                  </label>
                  <label className="flex flex-col gap-1">
                    <span className="label">{t("ai.cfg.budgetTokens")}</span>
                    <input className="field" inputMode="numeric" value={budgetTokens} onChange={(e) => setBudgetTokens(e.target.value)} />
                  </label>
                </div>
                {budget && (
                  <p className="m-0 text-[13px]" role="status" data-testid="budget-stato">
                    {t("ai.cfg.budgetNow", { month: budget.month, spent: usd(budget.spent_usd), tokens: fmtNumber(budget.tokens) })}
                    {budget.state !== "none" ? ` ${t(`ai.budget.${budget.state}`, { percent: fmtNumber(Math.floor(budget.percent)) })}` : ""}
                  </p>
                )}
              </section>

              <section aria-labelledby="ai-dati" className="flex flex-col gap-3">
                <SectionTitle><span id="ai-dati">{t("ai.cfg.privacy")}</span></SectionTitle>
                <label className="flex items-start gap-3">
                  <input type="checkbox" className="mt-1" checked={draft.mask_personal_data} onChange={(e) => set({ mask_personal_data: e.target.checked })} />
                  <span className="flex flex-col gap-1">
                    <span className="font-semibold">{t("ai.cfg.mask")}</span>
                    <span className="text-[13px] text-muted">{t("ai.cfg.maskHelp")}</span>
                    {v.settings.regulated && <span className="text-[13px]">{t("ai.cfg.regulated")}</span>}
                  </span>
                </label>
                <label className="flex max-w-xs flex-col gap-1">
                  <span className="label">{t("ai.cfg.retention")}</span>
                  <input className="field" type="number" min={1} max={3650} value={draft.retention_days} onChange={(e) => set({ retention_days: Number(e.target.value) })} />
                  <span className="text-[12.5px] text-muted">{t("ai.cfg.retentionHelp")}</span>
                </label>
              </section>

              <section aria-labelledby="ai-stato" className="flex flex-col gap-3">
                <SectionTitle><span id="ai-stato">{t("ai.cfg.state")}</span></SectionTitle>
                <label className="flex items-start gap-3">
                  <input type="checkbox" className="mt-1" checked={draft.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
                  <span className="flex flex-col gap-1">
                    <span className="font-semibold">{t("ai.cfg.enable")}</span>
                    <span className="text-[13px] text-muted">{t("ai.cfg.enableHelp")}</span>
                  </span>
                </label>
              </section>

              <section aria-labelledby="ai-mcp" className="flex flex-col gap-3">
                <SectionTitle><span id="ai-mcp">{t("ai.mcp.title")}</span></SectionTitle>
                <p className="m-0 text-[13px] text-muted">{t("ai.mcp.intro")}</p>
                <label className="flex items-start gap-3">
                  <input type="checkbox" className="mt-1" checked={draft.mcp_enabled} onChange={(e) => set({ mcp_enabled: e.target.checked })} />
                  <span className="flex flex-col gap-1">
                    <span className="font-semibold">{t("ai.mcp.enable")}</span>
                    <span className="text-[13px] text-muted">{t("ai.mcp.enableHelp")}</span>
                  </span>
                </label>
                <div className="flex flex-col gap-1">
                  <span className="label">{t("ai.mcp.url")}</span>
                  <code className="block border border-hair bg-side px-2 py-1 font-mono text-[13px] [overflow-wrap:anywhere]" data-testid="mcp-indirizzo">{mcpURL()}</code>
                  <span className="text-[12.5px] text-muted">{t("ai.mcp.urlHelp")}</span>
                </div>
              </section>

              <ErrorNote error={save.error} />
              {save.isSuccess && <p className="m-0 text-ok" role="status">{t("ai.cfg.saved")}</p>}
              <div>
                <button className="btn btn-primary" type="submit" disabled={save.isPending}>
                  {save.isPending ? t("ai.cfg.saving") : t("ai.cfg.save")}
                </button>
              </div>
            </form>
          </>
        )}
      </div>
    </>
  );
}
