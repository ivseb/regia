// Configurazione → Gate di rilascio (piano, passo 21, fase 4; replica/devsecops.md §3.5):
// regole per ambiente con anteprima «cosa succederebbe ora», chi approva gli override e la
// mappatura dei controlli della pipeline sui controlli GRC (evidenze automatiche).
import { EntitySelect, Select } from "../select";
import { useEffect, useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { dsoApi } from "../devsecopsApi";
import {
  RULE_HELP,
  RULE_NAMES,
  checkLabel,
  gateApi,
  type ChangeServiceNow,
  type EvidenceMapItem,
  type GateApprover,
  type GateConfig,
  type GatePolicy,
  type GateRule,
  type RuleKind,
} from "../gateApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, Loading } from "../ui";
import { ConditionBuilder } from "./processEditor/ConditionBuilder";
import { ReasonList, ResultBadge } from "./GatePanel";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { tr } from "../i18n/core";
import { loc } from "../i18n/format";
import { BozzaBar, useBozza } from "../casa/Bozza";
import { diffFields, diffList, type Change } from "../casa/differenze";

const MODES: { key: GatePolicy["mode"]; name: string; help: string }[] = [
  { key: "warn", get name() { return tr("configurazione.gate.mode.warn"); }, get help() { return tr("configurazione.gate.mode.warnHelp"); } },
  { key: "block", get name() { return tr("configurazione.gate.mode.block"); }, get help() { return tr("configurazione.gate.mode.blockHelp"); } },
  { key: "off", get name() { return tr("configurazione.gate.mode.off"); }, get help() { return tr("configurazione.gate.mode.offHelp"); } },
];

/** Giorni della settimana abbreviati nella lingua corrente (lunedì = 1). */
const dayNames = () => Array.from({ length: 7 }, (_, i) => new Intl.DateTimeFormat(loc(), { weekday: "short" }).format(new Date(2024, 0, i + 1)));
const KINDS: RuleKind[] = ["requirements", "vulnerabilities", "change_approved", "checks", "window", "condition"];

const asNum = (v: unknown, d: number) => (typeof v === "number" ? v : d);
const asStr = (v: unknown, d = "") => (typeof v === "string" ? v : d);
function asList<T>(v: unknown): T[] {
  return Array.isArray(v) ? (v as T[]) : [];
}

function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v);
  useEffect(() => {
    const t = setTimeout(() => setD(v), ms);
    return () => clearTimeout(t);
  }, [v, ms]);
  return d;
}

export function GateConfigPage() {
  const t = useT();
  const cfg = useQuery({ queryKey: ["gate-config"], queryFn: gateApi.config });
  const c = cfg.data;
  return (
    <>
      <PageHeader path={t("configurazione.path.devsecops")} title={t("configurazione.gate.gateDiRilascio")} />
      <div className="flex flex-col gap-9 px-4 pb-12 md:px-7">
        <p className="m-0 max-w-3xl text-muted">
          {t("configurazione.gate.primaDelDeployLa")}
        </p>
        <ErrorNote error={cfg.error} />
        {cfg.isPending && <Loading className="m-0 text-muted" />}
        {c && (
          <>
            <Environments cfg={c} />
            <Approvers cfg={c} />
            <EvidenceMap cfg={c} />
          </>
        )}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------
// Regole per ambiente

function Environments({ cfg }: { cfg: GateConfig }) {
  const t = useT();
  const [sel, setSel] = useState<string | null>(null);
  const [name, setName] = useState("");
  const names = cfg.policies.map((p) => p.environment);
  const [added, setAdded] = useState<string[]>([]);
  const all = [...names, ...added.filter((a) => !names.includes(a))];
  const current = sel && all.includes(sel) ? sel : all[0] ?? null;
  const free = cfg.environments.map((e) => e.name).filter((n) => !all.includes(n));
  return (
    <section className="flex min-w-0 flex-col gap-4" aria-label={t("configurazione.gate.regolePerAmbiente")}>
      <SectionTitle>{t("configurazione.gate.regolePerAmbiente")}</SectionTitle>
      <div className="flex flex-wrap items-center gap-2" role="tablist" aria-label={t("configurazione.gate.ambienti")}>
        {all.map((n) => {
          const p = cfg.policies.find((x) => x.environment === n);
          return (
            <button
              key={n}
              role="tab"
              aria-selected={n === current}
              className={`border px-2.5 py-1 text-[13px] ${n === current ? "border-accent bg-sel font-semibold" : "border-hair"}`}
              onClick={() => setSel(n)}
            >
              {n === "*" ? t("configurazione.gate.tuttiGliAltri") : n}
              <span className="ml-1.5 text-[11px] text-muted">{p ? MODES.find((m) => m.key === p.mode)?.name.toLowerCase() : t("configurazione.gate.newLower")}</span>
            </button>
          );
        })}
        {cfg.can_edit && (
          <form
            className="flex items-center gap-1.5"
            onSubmit={(e) => {
              e.preventDefault();
              const n = name.trim().toLowerCase();
              if (!n) return;
              if (!all.includes(n)) setAdded([...added, n]);
              setSel(n);
              setName("");
            }}
          >
            <input
              className="field w-40 py-1 text-[13px]"
              list="gate-envs"
              aria-label={t("configurazione.gate.nuovoAmbiente")}
              placeholder={t("configurazione.gate.aggiungiAmbiente")}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            <datalist id="gate-envs">
              {!all.includes("*") && <option value="*">{t("configurazione.gate.tuttiGliAltriAmbienti")}</option>}
              {free.map((n) => (
                <option key={n} value={n} />
              ))}
            </datalist>
            <button className="btn py-1" disabled={name.trim() === ""}>
              {t("configurazione.common.aggiungi")}
            </button>
          </form>
        )}
      </div>
      {current === null && <p className="m-0 text-muted"><Trans k="configurazione.gate.noEnvironment" slots={{ example: <span className="font-mono">{t("configurazione.gate.prod")}</span> }} /></p>}
      {current !== null && (
        <PolicyEditor
          key={current}
          cfg={cfg}
          env={current}
          saved={cfg.policies.find((p) => p.environment === current)}
          onGone={() => {
            setAdded(added.filter((a) => a !== current));
            setSel(null);
          }}
        />
      )}
    </section>
  );
}

function PolicyEditor({ cfg, env, saved, onGone }: { cfg: GateConfig; env: string; saved?: GatePolicy; onGone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const isProd = cfg.environments.find((e) => e.name === env)?.production ?? /^prod/.test(env);
  const initial = useMemo<GatePolicy>(
    () =>
      saved ?? {
        environment: env,
        mode: "warn",
        rules: cfg.default_rules.map((r) => ({ ...r, enabled: isProd ? r.enabled : r.kind === "vulnerabilities" })),
        override_enabled: true,
        override_hours: 24,
      },
    [saved, env, cfg.default_rules, isProd],
  );
  // Bozza dell'ambiente: «Cosa cambia» e pubblicazione con il componente comune.
  const bozza = useBozza<GatePolicy>(initial, `gate.${env}`);
  const draft = bozza.draft;
  const setDraft = bozza.setDraft;
  const dirty = bozza.dirty;
  const save = useMutation({
    mutationFn: () => gateApi.savePolicy(env, { mode: draft.mode, rules: draft.rules, override_enabled: draft.override_enabled, override_hours: draft.override_hours }),
    onSuccess: (p) => {
      setDraft(p);
      void qc.invalidateQueries({ queryKey: ["gate-config"] });
      void qc.invalidateQueries({ queryKey: ["gate-panel"] });
    },
  });
  const del = useMutation({
    mutationFn: () => gateApi.deletePolicy(env),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["gate-config"] });
      onGone();
    },
  });
  const setRule = (i: number, r: GateRule) => setDraft({ ...draft, rules: draft.rules.map((x, j) => (j === i ? r : x)) });
  const unused = KINDS.filter((k) => k === "condition" || !draft.rules.some((r) => r.kind === k));
  const edit = cfg.can_edit;
  return (
    <div className="grid min-w-0 grid-cols-1 gap-8 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
      <div className="flex min-w-0 flex-col gap-5">
        <fieldset className="m-0 flex min-w-0 flex-col gap-2 border-0 p-0" disabled={!edit}>
          <legend className="label mb-1 p-0">{t("configurazione.gate.whatGateDoes", { env: env === "*" ? t("configurazione.gate.tuttiGliAltri2") : env })}</legend>
          {MODES.map((m) => (
            <label key={m.key} className="flex items-start gap-2">
              <input type="radio" className="mt-1" name={`mode-${env}`} checked={draft.mode === m.key} onChange={() => setDraft({ ...draft, mode: m.key })} />
              <span>
                <span className="font-semibold">{m.name}</span>
                <span className="block text-[12.5px] text-muted">{m.help}</span>
              </span>
            </label>
          ))}
        </fieldset>

        {draft.mode !== "off" && (
          <div className="flex min-w-0 flex-col">
            <h3 className="label m-0 border-b-2 border-rule pb-1.5">{t("configurazione.common.regole")}</h3>
            {draft.rules.map((r, i) => (
              <RuleEditor
                key={r.id || i}
                rule={r}
                cfg={cfg}
                disabled={!edit}
                onChange={(x) => setRule(i, x)}
                onRemove={() => setDraft({ ...draft, rules: draft.rules.filter((_, j) => j !== i) })}
              />
            ))}
            {edit && unused.length > 0 && (
              <div className="flex flex-wrap items-center gap-2 pt-3">
                <span className="text-[12.5px] text-muted">{t("configurazione.gate.aggiungiUnaRegola")}</span>
                {unused.map((k) => (
                  <button
                    key={k}
                    className="btn py-1 text-[12.5px]"
                    onClick={() => {
                      const base = cfg.default_rules.find((r) => r.kind === k);
                      const rule: GateRule = base ? { ...base, id: "", enabled: true } : { id: "", kind: k, enabled: true, level: "block", params: k === "condition" ? { when: "", on: "application" } : {} };
                      setDraft({ ...draft, rules: [...draft.rules, rule] });
                    }}
                  >
                    {RULE_NAMES[k]}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}

        <fieldset className="m-0 flex min-w-0 flex-col gap-2 border-0 p-0" disabled={!edit}>
          <legend className="label mb-1 p-0">{t("configurazione.gate.override")}</legend>
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-1" checked={draft.override_enabled} onChange={(e) => setDraft({ ...draft, override_enabled: e.target.checked })} />
            <span>
              {t("configurazione.gate.chiHaIlPermesso")}
              <span className="block text-[12.5px] text-muted">{t("configurazione.gate.loApprovaChiIndichi")}</span>
            </span>
          </label>
          {draft.override_enabled && (
            <label className="flex items-center gap-2 pl-6">
              <span className="text-[13px]">{t("configurazione.gate.duraAlMassimo")}</span>
              <input
                className="field w-20 py-1 font-mono"
                inputMode="numeric"
                aria-label={t("configurazione.gate.oreDiValiditaDell")}
                value={draft.override_hours}
                onChange={(e) => setDraft({ ...draft, override_hours: Number(e.target.value.replace(/\D/g, "")) || 0 })}
              />
              <span className="text-[13px]">{t("configurazione.gate.ore")}</span>
            </label>
          )}
        </fieldset>

        {edit && (
          <>
            <BozzaBar
              changes={gateChanges(initial, draft, !saved)}
              pending={save.isPending}
              error={save.error}
              published={save.isSuccess && !dirty}
              publishLabel={saved ? t("casa.bozza.publish") : t("configurazione.gate.creaLeRegole")}
              impact={initial.mode !== draft.mode ? t("casa.gate.impactMode", { mode: MODES.find((m) => m.key === draft.mode)?.name ?? "", help: MODES.find((m) => m.key === draft.mode)?.help ?? "" }) : t("casa.gate.impact")}
              onPublish={() => save.mutate()}
              onDiscard={() => {
                bozza.discard();
                save.reset();
              }}
            />
            {saved && (
              <div>
                <button
                  className="btn btn-danger"
                  disabled={del.isPending}
                  onClick={() => {
                    if (window.confirm(t("configurazione.gate.confirmRemoveRules", { env }))) del.mutate();
                  }}
                >
                  {t("configurazione.gate.togliLeRegole")}
                </button>
              </div>
            )}
          </>
        )}
        <ErrorNote error={save.error ?? del.error} />
        {!edit && <p className="m-0 text-[12.5px] text-muted">{t("configurazione.gate.leRegoleLeModifica")}</p>}
      </div>
      <Preview env={env} draft={draft} />
    </div>
  );
}

/** «Cosa cambia» nelle regole di un ambiente: modalità, override e una riga per regola. */
function gateChanges(before: GatePolicy, after: GatePolicy, isNew: boolean): Change[] {
  const mode = (m: GatePolicy["mode"]) => MODES.find((x) => x.key === m)?.name ?? m;
  const rule = (r: GateRule) => `${r.enabled ? mode(r.level === "block" ? "block" : "warn").toLowerCase() : tr("casa.gate.ruleOff")}${Object.keys(r.params ?? {}).length ? ` · ${JSON.stringify(r.params)}` : ""}`;
  const out: Change[] = [];
  if (isNew) out.push({ label: tr("casa.gate.newEnvironment"), before: tr("casa.bozza.absent"), after: mode(after.mode) });
  out.push(
    ...diffFields([
      { label: tr("configurazione.gate.whatGateDoes", { env: after.environment === "*" ? tr("configurazione.gate.tuttiGliAltri2") : after.environment }), before: before.mode, after: after.mode, format: (v) => mode(v as GatePolicy["mode"]) },
      { label: tr("configurazione.gate.override"), before: before.override_enabled, after: after.override_enabled },
      { label: tr("configurazione.gate.duraAlMassimo"), before: before.override_hours, after: after.override_hours },
    ]),
    ...diffList(tr("configurazione.common.regole"), before.rules, after.rules, (r, i) => r.id || `${r.kind}#${i}`, (r) => RULE_NAMES[r.kind], rule),
  );
  return out;
}

// ---------------------------------------------------------------------
// Una regola

function RuleEditor({ rule, cfg, disabled, onChange, onRemove }: { rule: GateRule; cfg: GateConfig; disabled: boolean; onChange: (r: GateRule) => void; onRemove: () => void }) {
  const t = useT();
  const p = rule.params ?? {};
  const set = (patch: Record<string, unknown>) => onChange({ ...rule, params: { ...p, ...patch } });
  return (
    <div className="flex flex-col gap-2 border-b border-hair py-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <label className="flex items-center gap-2">
          <input type="checkbox" disabled={disabled} checked={rule.enabled} onChange={(e) => onChange({ ...rule, enabled: e.target.checked })} />
          <span className="font-semibold">{RULE_NAMES[rule.kind]}</span>
        </label>
        <Select
          className="field w-auto py-1 text-[12.5px]"
          aria-label={t("configurazione.gate.ruleNotMet", { rule: RULE_NAMES[rule.kind] })}
          disabled={disabled || !rule.enabled}
          value={rule.level}
          onChange={(e) => onChange({ ...rule, level: e.target.value as GateRule["level"] })}
        >
          <option value="block">{t("configurazione.gate.blocca")}</option>
          <option value="warn">{t("configurazione.gate.avvisaSoltanto")}</option>
        </Select>
        {!disabled && (
          <button className="ml-auto text-[12px] text-muted underline" onClick={onRemove}>
            {t("configurazione.common.togli")}
          </button>
        )}
      </div>
      <p className="m-0 pl-6 text-[12.5px] text-muted">{RULE_HELP[rule.kind]}</p>
      {rule.enabled && (
        <div className="flex min-w-0 flex-col gap-2.5 pl-6">
          {rule.kind === "vulnerabilities" && (
            <>
              <label className="flex flex-wrap items-center gap-2 text-[13px]">
                {t("configurazione.gate.nonAmmettere")}
                <Select className="field w-auto py-1" disabled={disabled} value={asStr(p.severity, "high")} onChange={(e) => set({ severity: e.target.value })}>
                  <option value="critical">{t("configurazione.gate.cveCritiche")}</option>
                  <option value="high">{t("configurazione.gate.cveCriticheEAlte")}</option>
                </Select>
                {t("configurazione.common.oltre")}
                <input
                  className="field w-16 py-1 font-mono"
                  inputMode="numeric"
                  disabled={disabled}
                  aria-label={t("configurazione.gate.quanteSeNeTollerano")}
                  value={asNum(p.max, 0)}
                  onChange={(e) => set({ max: Number(e.target.value.replace(/\D/g, "")) || 0 })}
                />
              </label>
              <label className="flex items-center gap-2 text-[13px]">
                <input type="checkbox" disabled={disabled} checked={p.only_fixable === true} onChange={(e) => set({ only_fixable: e.target.checked })} />
                {t("configurazione.gate.contaSoloQuelleCon")}
              </label>
              <label className="flex items-center gap-2 text-[13px]">
                <input type="checkbox" disabled={disabled} checked={p.require_report === true} onChange={(e) => set({ require_report: e.target.checked })} />
                {t("configurazione.gate.senzaUnaScansioneDella")}
              </label>
            </>
          )}
          {rule.kind === "change_approved" && (
            <>
              <fieldset className="m-0 flex min-w-0 flex-wrap gap-x-4 gap-y-1.5 border-0 p-0" disabled={disabled}>
                <legend className="mb-1 p-0 text-[12.5px] text-muted">{t("configurazione.gate.ilChangeEApprovato")}</legend>
                {PhaseChoices(cfg, asList<string>(p.phases)).map((ph) => (
                  <label key={ph} className="flex items-center gap-1.5 text-[13px]">
                    <input
                      type="checkbox"
                      checked={asList<string>(p.phases).includes(ph)}
                      onChange={(e) => set({ phases: e.target.checked ? [...asList<string>(p.phases), ph] : asList<string>(p.phases).filter((x) => x !== ph) })}
                    />
                    {ph}
                  </label>
                ))}
              </fieldset>
              <PhaseAdder disabled={disabled} phases={asList<string>(p.phases)} onChange={(phases) => set({ phases })} />
              <label className="flex items-center gap-2 text-[13px]">
                <input type="checkbox" disabled={disabled} checked={p.require_change === true} onChange={(e) => set({ require_change: e.target.checked })} />
                {t("configurazione.gate.senzaUnaRichiestaCollegata")}
              </label>
              <ServiceNowApproval value={asStr(p.servicenow, "") as ChangeServiceNow} phases={asList<string>(p.phases).length} disabled={disabled} onChange={(v) => set({ servicenow: v || undefined })} />
            </>
          )}
          {rule.kind === "checks" && <ChecksField names={asList<string>(p.names)} known={cfg.evidence.check_names} disabled={disabled} onChange={(names) => set({ names })} />}
          {rule.kind === "window" && <WindowFields p={p} disabled={disabled} set={set} />}
          {rule.kind === "condition" && (
            <div className="flex min-w-0 flex-col gap-2">
              <label className="flex flex-wrap items-center gap-2 text-[13px]">
                {t("configurazione.gate.valeSuiDati")}
                <Select className="field w-auto py-1" disabled={disabled} value={asStr(p.on, "application")} onChange={(e) => set({ on: e.target.value })}>
                  <option value="application">{t("configurazione.gate.dellApplicazione")}</option>
                  <option value="request">{t("configurazione.gate.dellaRichiesta")}</option>
                </Select>
              </label>
              <ConditionBuilder
                label={t("configurazione.gate.deveEssereVeraQuesta")}
                value={asStr(p.when)}
                onChange={(v) => set({ when: v })}
                facts={cfg.facts[asStr(p.on, "application") === "request" ? "business_request" : "application"] ?? []}
                sample={cfg.samples[asStr(p.on, "application") === "request" ? "business_request" : "application"]}
                type={asStr(p.on, "application") === "request" ? "business_request" : "application"}
                empty={t("configurazione.gate.scegliAlmenoUnDato")}
              />
              <label className="flex flex-col gap-1">
                <span className="label">{t("configurazione.gate.comeLaChiamiFacoltativo")}</span>
                <input className="field" disabled={disabled} value={rule.label ?? ""} maxLength={200} onChange={(e) => onChange({ ...rule, label: e.target.value })} placeholder={t("configurazione.gate.esNessunaCveCritica")} />
              </label>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// Come conta l'approvazione della change request letta da ServiceNow (sola lettura).
function ServiceNowApproval({ value, phases, disabled, onChange }: { value: ChangeServiceNow; phases: number; disabled: boolean; onChange: (v: ChangeServiceNow) => void }) {
  const t = useT();
  const help = value === "accept" ? t("altro.servicenow.gate.helpAccept") : value === "require" ? t("altro.servicenow.gate.helpRequire") : t("altro.servicenow.gate.helpNone");
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <label className="flex flex-wrap items-center gap-2 text-[13px]">
        {t("altro.servicenow.gate.label")}
        <Select className="field w-auto max-w-full py-1" disabled={disabled} value={value} onChange={(e) => onChange(e.target.value as ChangeServiceNow)}>
          <option value="">{t("altro.servicenow.gate.none")}</option>
          <option value="accept">{t("altro.servicenow.gate.accept")}</option>
          <option value="require">{t("altro.servicenow.gate.require")}</option>
        </Select>
      </label>
      <p className={`m-0 text-[12.5px] ${value === "" && phases === 0 ? "text-flag" : "text-muted"}`}>{help}</p>
    </div>
  );
}

function PhaseAdder({ phases, disabled, onChange }: { phases: string[]; disabled: boolean; onChange: (p: string[]) => void }) {
  const t = useT();
  const [v, setV] = useState("");
  return (
    <span className="flex items-center gap-2">
      <input className="field w-44 py-1" aria-label={t("configurazione.gate.unAltraFase")} placeholder={t("configurazione.gate.unAltraFase2")} disabled={disabled} value={v} onChange={(e) => setV(e.target.value)} />
      <button
        type="button"
        className="btn py-1"
        disabled={disabled || v.trim() === ""}
        onClick={() => {
          if (!phases.includes(v.trim())) onChange([...phases, v.trim()]);
          setV("");
        }}
      >
        {t("configurazione.common.aggiungi")}
      </button>
    </span>
  );
}

function PhaseChoices(cfg: GateConfig, chosen: string[]): string[] {
  const names = cfg.request_phases.map((x) => x.name);
  return [...names, ...chosen.filter((c) => !names.includes(c))];
}

function ChecksField({ names, known, disabled, onChange }: { names: string[]; known: string[]; disabled: boolean; onChange: (n: string[]) => void }) {
  const t = useT();
  const [extra, setExtra] = useState("");
  const options = [...known, ...names.filter((n) => !known.includes(n))];
  return (
    <fieldset className="m-0 flex min-w-0 flex-col gap-2 border-0 p-0" disabled={disabled}>
      <legend className="mb-1 p-0 text-[12.5px] text-muted">{t("configurazione.gate.devonoEssereSuperatiNell")}</legend>
      <div className="flex flex-wrap gap-x-4 gap-y-1.5">
        {options.length === 0 && <span className="text-[12.5px] text-muted">{t("configurazione.gate.nessunControlloRicevutoFinora")}</span>}
        {options.map((n) => (
          <label key={n} className="flex items-center gap-1.5 text-[13px]">
            <input type="checkbox" checked={names.includes(n)} onChange={(e) => onChange(e.target.checked ? [...names, n] : names.filter((x) => x !== n))} />
            <span>{checkLabel(n)}{checkLabel(n) !== n && <span className="font-mono text-[11.5px] text-muted"> {n}</span>}</span>
          </label>
        ))}
      </div>
      <span className="flex items-center gap-2">
        <input className="field w-44 py-1 font-mono" aria-label={t("configurazione.gate.altroControllo")} placeholder={t("configurazione.gate.altroControllo2")} value={extra} onChange={(e) => setExtra(e.target.value)} />
        <button
          type="button"
          className="btn py-1"
          disabled={extra.trim() === ""}
          onClick={() => {
            const n = extra.trim().toLowerCase();
            if (!names.includes(n)) onChange([...names, n]);
            setExtra("");
          }}
        >
          {t("configurazione.common.aggiungi")}
        </button>
      </span>
    </fieldset>
  );
}

function WindowFields({ p, disabled, set }: { p: Record<string, unknown>; disabled: boolean; set: (patch: Record<string, unknown>) => void }) {
  const t = useT();
  const days = asList<number>(p.days);
  const black = asList<{ from: string; to: string; label?: string }>(p.blackouts);
  const setBlack = (i: number, patch: Partial<{ from: string; to: string; label: string }>) => set({ blackouts: black.map((b, j) => (j === i ? { ...b, ...patch } : b)) });
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <fieldset className="m-0 flex min-w-0 flex-wrap items-center gap-1.5 border-0 p-0" disabled={disabled}>
        <legend className="mb-1 p-0 text-[12.5px] text-muted">{t("configurazione.gate.giorniInCuiSi")}</legend>
        {dayNames().map((d, i) => {
          const on = days.includes(i + 1);
          return (
            <button
              key={d}
              type="button"
              aria-pressed={on}
              className={`border px-2 py-1 text-[12.5px] ${on ? "border-accent bg-sel font-semibold" : "border-hair"}`}
              onClick={() => set({ days: on ? days.filter((x) => x !== i + 1) : [...days, i + 1].sort() })}
            >
              {d}
            </button>
          );
        })}
      </fieldset>
      <div className="flex flex-wrap items-center gap-2 text-[13px]">
        {t("configurazione.gate.dalle")}
        <input type="time" className="field w-28 py-1 font-mono" aria-label={t("configurazione.gate.dalle2")} disabled={disabled} value={asStr(p.from, "08:00")} onChange={(e) => set({ from: e.target.value })} />
        {t("configurazione.gate.alle")}
        <input type="time" className="field w-28 py-1 font-mono" aria-label={t("configurazione.gate.alle2")} disabled={disabled} value={asStr(p.to, "18:00")} onChange={(e) => set({ to: e.target.value })} />
        <Select className="field w-auto py-1" aria-label={t("configurazione.gate.fusoOrario")} disabled={disabled} value={asStr(p.timezone, "Europe/Rome")} onChange={(e) => set({ timezone: e.target.value })}>
          {["Europe/Rome", "Europe/London", "Europe/Berlin", "UTC", "America/New_York"].map((z) => (
            <option key={z}>{z}</option>
          ))}
        </Select>
      </div>
      <div className="flex flex-col gap-2">
        <span className="text-[12.5px] text-muted">{t("configurazione.gate.blocchiDelCalendarioChiusure")}</span>
        {black.map((b, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2 text-[13px]">
            {t("configurazione.gate.dal")}
            <input type="date" className="field w-auto py-1 font-mono" aria-label={t("configurazione.common.dal")} disabled={disabled} value={b.from} onChange={(e) => setBlack(i, { from: e.target.value })} />
            {t("configurazione.gate.al")}
            <input type="date" className="field w-auto py-1 font-mono" aria-label={t("configurazione.gate.toCap")} disabled={disabled} value={b.to} onChange={(e) => setBlack(i, { to: e.target.value })} />
            <input className="field min-w-0 flex-1 basis-40 py-1" aria-label={t("configurazione.gate.motivoDelBlocco")} placeholder={t("configurazione.gate.motivo")} disabled={disabled} value={b.label ?? ""} onChange={(e) => setBlack(i, { label: e.target.value })} />
            {!disabled && (
              <button type="button" className="text-[12px] text-muted underline" onClick={() => set({ blackouts: black.filter((_, j) => j !== i) })}>
                {t("configurazione.common.togli2")}
              </button>
            )}
          </div>
        ))}
        {!disabled && (
          <button type="button" className="btn self-start py-1 text-[12.5px]" onClick={() => set({ blackouts: [...black, { from: "", to: "", label: "" }] })}>
            {t("configurazione.gate.aggiungiUnBlocco")}
          </button>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------
// Anteprima: cosa succederebbe ora

function Preview({ env, draft }: { env: string; draft: GatePolicy }) {
  const t = useT();
  const apps = useQuery({ queryKey: ["dso-config-apps"], queryFn: dsoApi.config, staleTime: 60_000 });
  const [app, setApp] = useState("");
  const [request, setRequest] = useState("");
  const [version, setVersion] = useState("");
  const body = useDebounced(JSON.stringify({ app, request: request.trim(), version: version.trim(), draft }), 450);
  const run = useQuery({
    queryKey: ["gate-preview", env, body],
    enabled: app !== "",
    queryFn: () => {
      const b = JSON.parse(body) as { app: string; request: string; version: string; draft: GatePolicy };
      return gateApi.preview({ application: b.app, environment: env === "*" ? "altro" : env, request: b.request || undefined, version: b.version || undefined, policy: b.draft });
    },
    retry: false,
  });
  const o = run.data;
  return (
    <aside className="flex min-w-0 flex-col gap-3 border-t-2 border-rule pt-3 lg:border-t-0 lg:sticky lg:top-4 lg:max-h-[calc(100vh-2rem)] lg:self-start lg:overflow-y-auto lg:border-l lg:border-hair lg:pl-6 lg:pt-0" aria-label={t("configurazione.common.anteprima")}>
      <h3 className="label m-0">{t("configurazione.gate.cosaSuccederebbeOra")}</h3>
      <label className="flex flex-col gap-1">
        <span className="label">{t("configurazione.gate.applicazione")}</span>
        <Select className="field" value={app} onChange={(e) => setApp(e.target.value)}>
          <option value="">{t("configurazione.gate.scegliUnApplicazione")}</option>
          {(apps.data?.applications ?? []).map((a) => (
            <option key={a.id} value={a.key}>
              {a.code ? `${a.code} · ` : ""}
              {a.name}
            </option>
          ))}
        </Select>
      </label>
      <div className="flex flex-wrap gap-3">
        <label className="flex min-w-0 flex-[1_1_120px] flex-col gap-1">
          <span className="label">{t("configurazione.gate.richiestaFacoltativa")}</span>
          <input className="field font-mono" value={request} onChange={(e) => setRequest(e.target.value.toUpperCase())} placeholder="DEM-12" />
        </label>
        <label className="flex min-w-0 flex-[1_1_120px] flex-col gap-1">
          <span className="label">{t("configurazione.gate.versioneFacoltativa")}</span>
          <input className="field font-mono" value={version} onChange={(e) => setVersion(e.target.value)} placeholder="1.4.0" />
        </label>
      </div>
      {app === "" && <p className="m-0 text-[12.5px] text-muted">{t("configurazione.gate.scegliUnApplicazioneL")}</p>}
      {app !== "" && run.isFetching && !o && <p className="m-0 text-muted">{t("configurazione.gate.calcolo")}</p>}
      <ErrorNote error={run.error} />
      {o && (
        <div className={`flex flex-col gap-3 ${run.isFetching ? "opacity-60" : ""}`} aria-live="polite">
          <div className="flex flex-wrap items-center gap-2">
            <ResultBadge result={o.result} overridden={o.overridden} />
            {o.raw_result !== o.result && !o.overridden && <span className="text-[12px] text-muted">{o.raw_result === "fail" ? t("configurazione.gate.wouldBeBlocked") : t("configurazione.gate.wouldBe", { result: o.raw_result })}</span>}
          </div>
          <p className="m-0 text-[13px]">{o.summary}</p>
          <ReasonList reasons={o.reasons} />
        </div>
      )}
    </aside>
  );
}

// ---------------------------------------------------------------------
// Chi approva gli override

function Approvers({ cfg }: { cfg: GateConfig }) {
  const t = useT();
  const qc = useQueryClient();
  const [rows, setRows] = useState<GateApprover[]>(cfg.approvers);
  const dirty = JSON.stringify(rows.map(({ member, role }) => ({ member, role }))) !== JSON.stringify(cfg.approvers.map(({ member, role }) => ({ member, role })));
  const save = useMutation({
    mutationFn: () => gateApi.saveApprovers(rows.filter((r) => r.member || r.role).map(({ member, role }) => ({ member, role }))),
    onSuccess: (r) => {
      setRows(r.approvers);
      void qc.invalidateQueries({ queryKey: ["gate-config"] });
    },
  });
  const edit = cfg.can_edit;
  return (
    <section className="flex min-w-0 flex-col gap-3" aria-label={t("configurazione.gate.chiApprovaGliOverride")}>
      <SectionTitle>{t("configurazione.gate.chiApprovaGliOverride")}</SectionTitle>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">
        {t("configurazione.gate.unOverridePassaDa")}
      </p>
      <ol className="m-0 flex list-none flex-col gap-2 p-0">
        {rows.map((r, i) => (
          <li key={i} className="flex flex-wrap items-center gap-2">
            <span className="w-14 text-[12.5px] text-muted">{t("configurazione.gate.step", { n: i + 1 })}</span>
            <Select
              className="field w-auto py-1"
              aria-label={t("configurazione.gate.approverTypeOfStep", { n: i + 1 })}
              disabled={!edit}
              value={r.role !== undefined ? "role" : "member"}
              onChange={(e) => setRows(rows.map((x, j) => (j === i ? (e.target.value === "role" ? { role: cfg.roles[0]?.key ?? "" } : { member: "" }) : x)))}
            >
              <option value="member">{t("configurazione.gate.unaPersona")}</option>
              <option value="role">{t("configurazione.gate.unRuoloSullApplicazione")}</option>
            </Select>
            {r.role !== undefined ? (
              <Select className="field w-auto min-w-0 py-1" aria-label={t("configurazione.common.ruolo")} disabled={!edit} value={r.role} onChange={(e) => setRows(rows.map((x, j) => (j === i ? { role: e.target.value } : x)))}>
                {cfg.roles.map((ro) => (
                  <option key={ro.key} value={ro.key}>
                    {ro.name}
                  </option>
                ))}
              </Select>
            ) : (
              <EntitySelect kind="member" className="field w-auto min-w-0 py-1" aria-label={t("configurazione.common.persona")} disabled={!edit} noneLabel={t("configurazione.common.scegli")} filter={(o) => o.detail !== "richiedente"} value={r.member ?? ""} onChange={(v) => setRows(rows.map((x, j) => (j === i ? { member: v } : x)))} />
            )}
            {edit && (
              <button className="text-[12px] text-muted underline" onClick={() => setRows(rows.filter((_, j) => j !== i))}>
                {t("configurazione.common.togli2")}
              </button>
            )}
          </li>
        ))}
        {rows.length === 0 && <li className="text-muted">{t("configurazione.gate.nessunApprovatoreGliOverride")}</li>}
      </ol>
      {edit && (
        <span className="flex flex-wrap items-center gap-2">
          <button className="btn" onClick={() => setRows([...rows, { member: "" }])}>
            {t("configurazione.common.aggiungiUnPasso")}
          </button>
          <button className="btn btn-primary" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
            {t("configurazione.gate.salvaGliApprovatori")}
          </button>
          {save.isSuccess && !dirty && <span className="text-[12.5px] text-ok">{t("configurazione.common.salvato")}</span>}
        </span>
      )}
      <ErrorNote error={save.error} />
    </section>
  );
}

// ---------------------------------------------------------------------
// Evidenze automatiche dei controlli GRC

function EvidenceMap({ cfg }: { cfg: GateConfig }) {
  const t = useT();
  const qc = useQueryClient();
  const ev = cfg.evidence;
  const [rows, setRows] = useState<EvidenceMapItem[]>(ev.items);
  const dirty = JSON.stringify(rows) !== JSON.stringify(ev.items);
  const save = useMutation({
    mutationFn: () => gateApi.saveEvidenceMap(rows.filter((r) => r.control)),
    onSuccess: (r) => {
      setRows(r.items);
      void qc.invalidateQueries({ queryKey: ["gate-config"] });
    },
  });
  const edit = cfg.can_edit;
  const sources = [{ key: "gate", name: t("configurazione.gate.decisioneDelGateSuperata") }, { key: "certification", name: t("rilasci.certificazione.sorgente") }, ...ev.check_names.map((n) => ({ key: `check:${n}`, name: t("configurazione.gate.checkPassedInBlock", { name: checkLabel(n) }) }))];
  for (const r of rows) if (!sources.some((s) => s.key === r.source)) sources.push({ key: r.source, name: r.source.startsWith("check:") ? t("configurazione.gate.checkPassedInBlock", { name: r.source.slice(6) }) : r.source });
  return (
    <section className="flex min-w-0 flex-col gap-3" aria-label={t("configurazione.gate.evidenzePerLaConformita")}>
      <SectionTitle>{t("configurazione.gate.evidenzePerLaConformita")}</SectionTitle>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">
        <Trans k="configurazione.gate.evidenceHelp" slots={{ block: <span className="font-mono">{t("configurazione.gate.block")}</span> }} />
      </p>
      {!ev.enabled && <p className="m-0 text-muted">{t("configurazione.gate.ilModuloDellaConformita")}</p>}
      {ev.enabled && ev.controls.length === 0 && (
        <p className="m-0 border-l-2 border-rule pl-3">
          <Trans k="configurazione.gate.noControlsYet" slots={{ link: <Link to="/conformita">{t("configurazione.gate.crealiInConformita")}</Link> }} />
        </p>
      )}
      {ev.enabled && ev.controls.length > 0 && (
        <>
          <ul className="m-0 flex list-none flex-col gap-3 p-0">
            {rows.map((r, i) => {
              const set = (patch: Partial<EvidenceMapItem>) => setRows(rows.map((x, j) => (j === i ? { ...x, ...patch } : x)));
              return (
                <li key={i} className="flex flex-wrap items-end gap-x-3 gap-y-2 border-b border-hair pb-3">
                  <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
                    <span className="label">{t("configurazione.gate.quandoSuccede")}</span>
                    <Select className="field" disabled={!edit} value={r.source} onChange={(e) => set({ source: e.target.value })}>
                      {sources.map((s) => (
                        <option key={s.key} value={s.key}>
                          {s.name}
                        </option>
                      ))}
                    </Select>
                  </label>
                  <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
                    <span className="label">{t("configurazione.gate.evidenzaPerIlControllo")}</span>
                    <Select className="field" disabled={!edit} value={r.control} onChange={(e) => set({ control: e.target.value })}>
                      <option value="">{t("configurazione.common.scegli")}</option>
                      {ev.controls.map((c) => (
                        <option key={c.key} value={c.key}>
                          {c.key} · {c.title}
                        </option>
                      ))}
                    </Select>
                  </label>
                  <label className="flex w-28 flex-col gap-1">
                    <span className="label">{t("configurazione.gate.validitaGiorni")}</span>
                    <input
                      className="field font-mono"
                      inputMode="numeric"
                      disabled={!edit}
                      placeholder={t("configurazione.gate.standard")}
                      value={r.valid_days ?? ""}
                      onChange={(e) => set({ valid_days: e.target.value === "" ? null : Number(e.target.value.replace(/\D/g, "")) || null })}
                    />
                  </label>
                  <label className="flex items-center gap-1.5 pb-2 text-[13px]">
                    <input type="checkbox" disabled={!edit} checked={r.production_only} onChange={(e) => set({ production_only: e.target.checked })} />
                    {t("configurazione.gate.soloInProduzione")}
                  </label>
                  {edit && (
                    <button className="pb-2 text-[12px] text-muted underline" onClick={() => setRows(rows.filter((_, j) => j !== i))}>
                      {t("configurazione.common.togli2")}
                    </button>
                  )}
                </li>
              );
            })}
            {rows.length === 0 && <li className="text-muted">{t("configurazione.gate.nessunaMappaturaIRilasci")}</li>}
          </ul>
          {edit && (
            <span className="flex flex-wrap items-center gap-2">
              <button className="btn" onClick={() => setRows([...rows, { source: ev.check_names[0] ? `check:${ev.check_names[0]}` : "gate", control: "", valid_days: null, production_only: true }])}>
                {t("configurazione.gate.aggiungiUnaMappatura")}
              </button>
              <button className="btn btn-primary" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
                {t("configurazione.gate.salvaLaMappatura")}
              </button>
              {save.isSuccess && !dirty && <span className="text-[12.5px] text-ok">{t("configurazione.common.salvata")}</span>}
            </span>
          )}
          <ErrorNote error={save.error} />
        </>
      )}
    </section>
  );
}
