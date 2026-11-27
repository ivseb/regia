// Configurazione → Intelligenza artificiale (replica/ai.md §3, §4, §7): fornitori con la chiave
// cifrata (non torna mai), livelli «rapido» e «approfondito», funzioni spostate su un altro livello
// o modello, budget mensile, mascheramento dei dati personali, conservazione delle conversazioni.
// Solo per chi amministra l'organizzazione. L'AI parte spenta.
import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";
import { aiApi, usd, type AIKind, type AIModel, type AIProvider, type AISettings, type AISettingsView, type Route, type TierChoice, type TierKey } from "../aiApi";
import { PageHeader } from "../shell";
import { Select } from "../select";
import { ErrorNote, Loading, SectionTitle } from "../ui";
import { useT } from "../i18n/react";
import { lookup } from "../i18n/core";
import { fmtNumber } from "../i18n/format";

const kindName = (k: string) => lookup(`ai.kind.${k}`) ?? k;
const featureName = (k: string) => lookup(`ai.feature.${k}`) ?? k;
const tierName = (k: string) => lookup(`ai.tier.${k}`) ?? k;

/** Modelli del fornitore: quelli proposti da Regia subito, quelli del servizio su richiesta. */
function useModels(view: AISettingsView | undefined, providerId: string, loaded: Record<string, AIModel[]>): AIModel[] {
  const p = view?.providers.find((x) => x.id === providerId);
  if (!p) return [];
  return loaded[p.id] ?? view?.kinds.find((k) => k.key === p.kind)?.proposed ?? [];
}

function TierPicker(props: {
  id: string;
  label: string;
  help: string;
  value: TierChoice;
  view: AISettingsView;
  loaded: Record<string, AIModel[]>;
  tier?: TierKey;
  onChange: (v: TierChoice) => void;
}) {
  const t = useT();
  const { id, value, view, onChange } = props;
  const models = useModels(view, value.provider_id, props.loaded);
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
              const proposed = view.kinds.find((k) => k.key === p?.kind)?.proposed ?? [];
              const pick = proposed.find((m) => m.tier === props.tier)?.id ?? "";
              onChange({ provider_id: pid, model: pid ? pick : "" });
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
              <option key={m.id} value={m.id}>{m.name ?? m.id}</option>
            ))}
          </datalist>
        </label>
      </div>
    </fieldset>
  );
}

type Draft = Omit<AISettings, "saved" | "regulated">;

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
  const k = kinds.find((x) => x.key === kind);
  const num = (s: string) => (s.trim() === "" ? null : Number(s.replace(",", ".")));
  const save = useMutation({
    mutationFn: () => {
      const body = {
        name,
        base_url: baseURL,
        api_key: key,
        remove_key: removeKey,
        no_tools_models: noTools.split(/[,\n]/).map((s) => s.trim()).filter(Boolean),
        price_input_usd: num(priceIn),
        price_output_usd: num(priceOut),
      };
      return provider ? aiApi.updateProvider(provider.id, body) : aiApi.createProvider({ ...body, kind });
    },
    onSuccess: () => {
      setKey("");
      onDone();
    },
  });
  const err = save.error instanceof ApiError ? save.error : null;
  const bad = (f: string) => err?.field === f;
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
      {k?.needs_url && (
        <label className="flex flex-col gap-1">
          <span className="label">{t("ai.cfg.baseUrl")}</span>
          <input className="field" required type="url" value={baseURL} aria-invalid={bad("base_url")} placeholder={t("ai.cfg.baseUrlPlaceholder")} onChange={(e) => setBaseURL(e.target.value)} />
          <span className="text-[12.5px] text-muted">{t("ai.cfg.baseUrlHelp")}</span>
        </label>
      )}
      {(k?.needs_key || k?.needs_url) && (
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
      <span className="text-[12.5px] text-muted">{t("ai.cfg.priceHelp")}</span>
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

function ProviderRow({ p, view, onModels, onChanged }: { p: AIProvider; view: AISettingsView; onModels: (id: string, m: AIModel[]) => void; onChanged: () => void }) {
  const t = useT();
  const [editing, setEditing] = useState(false);
  const models = useMutation({ mutationFn: () => aiApi.models(p.id), onSuccess: (r) => onModels(p.id, r.items) });
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
        <span className="text-[12.5px] text-muted">{p.has_key ? t("ai.cfg.hasKey") : t("ai.cfg.noKey")}</span>
        <span className="text-[12.5px] text-muted">{p.local ? t("ai.cfg.local") : t("ai.cfg.external")}</span>
      </div>
      <div className="flex flex-wrap gap-2">
        <button className="btn py-1" disabled={models.isPending} onClick={() => models.mutate()}>
          {models.isPending ? t("ai.cfg.checking") : t("ai.cfg.check")}
        </button>
        <button className="btn py-1" onClick={() => setEditing(true)}>{t("ai.cfg.edit")}</button>
        <button className="btn btn-danger py-1" disabled={del.isPending} onClick={() => del.mutate()}>{t("ai.cfg.remove")}</button>
      </div>
      {models.data && (
        <span className="text-[12.5px] text-ok" role="status">
          {t("ai.cfg.modelsFound", { count: models.data.items.length })}
          {models.data.items.length > 0 ? `: ${models.data.items.slice(0, 12).map((m) => m.id).join(", ")}` : ""}
        </span>
      )}
      <ErrorNote error={models.error ?? del.error} />
    </li>
  );
}

function RouteRow({ f, route, view, loaded, onChange }: { f: AISettingsView["features"][number]; route: Route | undefined; view: AISettingsView; loaded: Record<string, AIModel[]>; onChange: (r: Route | undefined) => void }) {
  const t = useT();
  const mode = route?.provider_id !== undefined && route.provider_id !== "" ? "model" : route?.model !== undefined ? "model" : (route?.tier ?? "");
  const choice: TierChoice = { provider_id: route?.provider_id ?? "", model: route?.model ?? "" };
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
            if (v === "") onChange(undefined);
            else if (v === "model") onChange({ provider_id: "", model: "" });
            else onChange({ tier: v as TierKey });
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
          onChange={(c) => onChange({ provider_id: c.provider_id, model: c.model })} />
      )}
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
                <TierPicker id="approfondito" tier="approfondito" label={tierName("approfondito")} help={t("ai.cfg.deepHelp")} value={draft.deep} view={v} loaded={loaded} onChange={(deep) => set({ deep })} />
                <TierPicker id="rapido" tier="rapido" label={tierName("rapido")} help={t("ai.cfg.fastHelp")} value={draft.fast} view={v} loaded={loaded} onChange={(fast) => set({ fast })} />
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
