import { EntitySelect, Select } from "../../select";
import { useId, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import type { ActionSchema, Definition, ProcessMeta, Rule, RuleAction } from "../../api";
import { casaApi, useDebounced } from "../../casaApi";
import { requirementsApi } from "../../requirementsApi";
import { reviewsApi } from "../../reviewsApi";
import { useT } from "../../i18n/react";
import { ConditionBuilder } from "./ConditionBuilder";
import { describeCondition, splitOn } from "./model";

// Regole a frase (replica/usabilita.md, proposta 6): «Quando [evento] solo se [condizione] allora
// [azioni]». Il formato salvato è quello di sempre (Rule: on, when, actions): qui cambia solo il
// modo di leggerlo e di compilarlo. Le azioni offerte sono quelle che il motore esegue davvero.

/** Azioni offerte a frase, nell'ordine del menu. Le altre azioni di una regola restano modificabili a campi. */
export const SENTENCE_ACTIONS = ["open_review", "apply_baseline", "assign_role"] as const;
type SentenceAction = (typeof SENTENCE_ACTIONS)[number];
const isSentence = (kind: string): kind is SentenceAction => (SENTENCE_ACTIONS as readonly string[]).includes(kind);

export type RuleRefs = {
  reviewTypes: { key: string; name: string; scope: string }[];
  catalogs: { key: string; name: string }[];
  /** I nomi sono arrivati (o non si possono leggere): si può scrivere il nome di una regola nuova. */
  loaded: boolean;
};

/** Nomi dei tipi di valutazione e dei cataloghi di requisiti a cui le azioni rimandano per chiave. */
export function useRuleRefs(): RuleRefs {
  const rev = useQuery({ queryKey: ["review-config"], queryFn: reviewsApi.config, retry: false, staleTime: 60_000 });
  const cat = useQuery({ queryKey: ["requirement-config"], queryFn: requirementsApi.config, retry: false, staleTime: 60_000 });
  return {
    reviewTypes: (rev.data?.types ?? []).map((x) => ({ key: x.key, name: x.name, scope: x.scope })),
    catalogs: (cat.data?.catalogs ?? []).map((x) => ({ key: x.key, name: x.name })),
    loaded: !rev.isPending,
  };
}

type T = ReturnType<typeof useT>;

/** L'evento in parole: «si crea», «entra nella fase «X»». */
export function eventPhrase(t: T, on: string): string {
  const [ev, phase] = splitOn(on);
  if (ev === "enter") return t("configurazione.regole.evento.enter", { phase });
  if (ev === "exit") return t("configurazione.regole.evento.exit", { phase });
  return t("configurazione.regole.evento.create");
}

type Names = { roles?: { key: string; name: string }[]; schemas?: ActionSchema[]; facts?: Record<string, string> };

/** Un'azione in parole: «apri la valutazione «Sicurezza»». */
export function actionPhrase(t: T, a: RuleAction, refs: RuleRefs, names: Names = {}): string {
  const when = a.when ? describeCondition(a.when) : "";
  switch (a.kind) {
    case "open_review": {
      const name = refs.reviewTypes.find((x) => x.key === a.ref)?.name ?? (a.ref || t("configurazione.regole.chi.nessuno"));
      return when ? t("configurazione.regole.azione.open_reviewImpatti", { name, condition: when }) : t("configurazione.regole.azione.open_review", { name });
    }
    case "apply_baseline": {
      const name = refs.catalogs.find((x) => x.key === a.ref)?.name ?? (a.ref || t("configurazione.regole.chi.nessuno"));
      return when ? t("configurazione.regole.azione.apply_baselineImpatti", { name, condition: when }) : t("configurazione.regole.azione.apply_baseline", { name });
    }
    case "assign_role": {
      const role = names.roles?.find((r) => r.key === a.role)?.name ?? (a.role || t("configurazione.regole.chi.nessuno"));
      const who = a.group ? t("configurazione.regole.chi.gruppo", { name: a.group }) : a.member ? t("configurazione.regole.chi.membro") : t("configurazione.regole.chi.nessuno");
      return t("configurazione.regole.azione.assign_role", { role, who });
    }
  }
  return t("configurazione.regole.azione.altra", { name: (names.schemas?.find((s) => s.kind === a.kind)?.name ?? a.kind).toLowerCase() });
}

/** La regola intera in una frase. */
export function ruleSentence(t: T, r: Pick<Rule, "on" | "when" | "actions">, refs: RuleRefs, names: Names = {}): string {
  const event = eventPhrase(t, r.on);
  const actions = r.actions.length ? r.actions.map((a) => actionPhrase(t, a, refs, names)).join(", ") : t("configurazione.regole.nessunaAzione");
  const when = r.when?.trim() ? describeCondition(r.when, names.facts) : "";
  return when ? t("configurazione.regole.fraseSe", { event, condition: when, actions }) : t("configurazione.regole.frase", { event, actions });
}

function Part({ word, children }: { word?: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      {word && <span className="label">{word}</span>}
      {children}
    </div>
  );
}

/** L'editor di una regola a frase: «Quando… solo se… allora…». */
export function RuleSentence({
  rule,
  def,
  meta,
  refs,
  extraValues,
  setRule,
  renderOther,
}: {
  rule: Rule;
  def: Definition;
  meta: ProcessMeta;
  refs: RuleRefs;
  extraValues: Record<string, string[]>;
  /** Modifica la regola; `key` distingue le modifiche per l'annulla/ripeti. */
  setRule: (fn: (r: Rule) => void, key: string) => void;
  /** Azioni che non si leggono a frase: si modificano a campi. */
  renderOther: (a: RuleAction, j: number) => ReactNode;
}) {
  const t = useT();
  const eventId = useId();
  const kinds = SENTENCE_ACTIONS.filter((k) => meta.actions.some((s) => s.kind === k));
  const [, phase] = splitOn(rule.on);
  const known = rule.on === "create" || def.phases.some((p) => rule.on === `enter:${p.name}` || rule.on === `exit:${p.name}`);
  return (
    <div className="flex flex-col gap-4" data-testid="regola-frase">
      <Part word={t("configurazione.regole.quando")}>
        <Select id={eventId} aria-label={t("configurazione.regole.evento.label")} className="field" value={rule.on} onChange={(e) => setRule((x) => (x.on = e.target.value), "on")}>
          <option value="create">{t("configurazione.regole.evento.create")}</option>
          {!known && <option value={rule.on}>{eventPhrase(t, rule.on) || phase}</option>}
          <optgroup label={t("configurazione.regole.evento.gruppoEnter")}>
            {def.phases.map((p) => (
              <option key={p.name} value={`enter:${p.name}`}>
                {t("configurazione.regole.evento.enter", { phase: p.name })}
              </option>
            ))}
          </optgroup>
          <optgroup label={t("configurazione.regole.evento.gruppoExit")}>
            {def.phases.map((p) => (
              <option key={p.name} value={`exit:${p.name}`}>
                {t("configurazione.regole.evento.exit", { phase: p.name })}
              </option>
            ))}
          </optgroup>
        </Select>
      </Part>

      <ConditionBuilder
        label={t("configurazione.regole.soloSe")}
        value={rule.when ?? ""}
        onChange={(v) => setRule((x) => (x.when = v || undefined), "when")}
        facts={meta.facts}
        type={meta.type.key}
        sample={meta.sample}
        empty={t("configurazione.processEditor.laRegolaValeSempre")}
        extraValues={extraValues}
      />
      <Preview when={rule.when ?? ""} type={meta.type.key} />

      <Part word={t("configurazione.regole.allora")}>
        <ul className="m-0 flex list-none flex-col gap-3 p-0">
          {rule.actions.map((a, j) => (
            <li key={j} className="flex flex-col gap-2 border-l-2 border-hair pl-3" data-testid="regola-azione">
              {isSentence(a.kind) && (kinds as readonly string[]).includes(a.kind) ? (
                <SentenceActionRow
                  action={a}
                  meta={meta}
                  refs={refs}
                  kinds={kinds}
                  onChange={(na) => setRule((x) => (x.actions[j] = na), `a${j}`)}
                  onRemove={() => setRule((x) => x.actions.splice(j, 1), `a${j}-rm`)}
                />
              ) : (
                renderOther(a, j)
              )}
            </li>
          ))}
        </ul>
        <Select
          aria-label={t("configurazione.regole.aggiungiAzione")}
          className="field w-auto self-start"
          value=""
          onChange={(e) => e.target.value && setRule((x) => x.actions.push({ kind: e.target.value }), "a-add")}
        >
          <option value="">{t("configurazione.regole.aggiungiAzione")}</option>
          {kinds.map((k) => (
            <option key={k} value={k}>
              {t(`configurazione.regole.tipo.${k}`)}
            </option>
          ))}
        </Select>
      </Part>
    </div>
  );
}

/** «Oggi questa regola scatterebbe su n elementi», dal conteggio dal vivo della condizione. */
function Preview({ when, type }: { when: string; type: string }) {
  const t = useT();
  const debounced = useDebounced(when.trim());
  const q = useQuery({ queryKey: ["regola-conto", type, debounced], queryFn: () => casaApi.count(debounced, type), enabled: !!debounced, retry: false });
  if (!debounced) return null;
  if (when.trim() !== debounced || q.isFetching) return <p className="m-0 text-[12.5px] text-muted">{t("configurazione.regole.anteprimaConto")}</p>;
  if (q.isError || (q.data && !q.data.ok)) return null;
  if (!q.data) return null;
  return (
    <p className="m-0 text-[12.5px]" data-testid="regola-anteprima">
      {t(q.data.truncated ? "configurazione.regole.anteprimaAlmeno" : "configurazione.regole.anteprima", { count: q.data.matched })}
    </p>
  );
}

function SentenceActionRow({
  action,
  meta,
  refs,
  kinds,
  onChange,
  onRemove,
}: {
  action: RuleAction;
  meta: ProcessMeta;
  refs: RuleRefs;
  kinds: readonly SentenceAction[];
  onChange: (a: RuleAction) => void;
  onRemove: () => void;
}) {
  const t = useT();
  const kind = action.kind as SentenceAction;
  const withKey = (k: string, v: unknown) => {
    const n: Record<string, unknown> = { ...action };
    if (v === "" || v === undefined || v === false) delete n[k];
    else n[k] = v;
    onChange(n as RuleAction);
  };
  const reviewType = refs.reviewTypes.find((x) => x.key === action.ref);
  const impactScope = reviewType?.scope === "impact";
  const unknownRef = (list: { key: string }[]) => !!action.ref && !list.some((x) => x.key === action.ref);
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-start gap-2">
        <Select
          aria-label={t("configurazione.regole.tipo.label")}
          className="field min-w-0 flex-1"
          value={kind}
          onChange={(e) => onChange({ kind: e.target.value })}
        >
          {kinds.map((k) => (
            <option key={k} value={k}>
              {t(`configurazione.regole.tipo.${k}`)}
            </option>
          ))}
        </Select>
        <button type="button" className="btn min-h-11 shrink-0 px-3" aria-label={t("configurazione.regole.togliAzione")} title={t("configurazione.regole.togliAzione")} onClick={onRemove}>
          ×
        </button>
      </div>

      {kind === "open_review" && (
        <>
          <Select aria-label={t("configurazione.regole.tipoValutazione")} className="field" value={action.ref ?? ""} onChange={(e) => withKey("ref", e.target.value)}>
            <option value="">{t("configurazione.regole.scegli")}</option>
            {unknownRef(refs.reviewTypes) && <option value={action.ref}>{action.ref}</option>}
            {refs.reviewTypes.map((x) => (
              <option key={x.key} value={x.key}>
                {x.name}
              </option>
            ))}
          </Select>
          {impactScope && <ImpactCondition value={action.when ?? ""} onChange={(v) => withKey("when", v)} />}
        </>
      )}

      {kind === "apply_baseline" && (
        <>
          <Select aria-label={t("configurazione.regole.catalogo")} className="field" value={action.ref ?? ""} onChange={(e) => withKey("ref", e.target.value)}>
            <option value="">{t("configurazione.regole.scegli")}</option>
            {unknownRef(refs.catalogs) && <option value={action.ref}>{action.ref}</option>}
            {refs.catalogs.map((x) => (
              <option key={x.key} value={x.key}>
                {x.name}
              </option>
            ))}
          </Select>
          <ImpactCondition value={action.when ?? ""} onChange={(v) => withKey("when", v)} />
        </>
      )}

      {kind === "assign_role" && (
        <>
          <Select aria-label={t("configurazione.regole.ruolo")} className="field" value={action.role ?? ""} onChange={(e) => withKey("role", e.target.value)}>
            <option value="">{t("configurazione.regole.scegli")}</option>
            {meta.roles.map((r) => (
              <option key={r.key} value={r.key}>
                {r.name}
              </option>
            ))}
          </Select>
          <Select
            aria-label={t("configurazione.regole.assegnaA")}
            className="field"
            value={action.member ? "member" : "group"}
            onChange={(e) => {
              const n: RuleAction = { ...action };
              delete n.group;
              delete n.member;
              onChange(e.target.value === "member" ? { ...n, member: "" } : n);
            }}
          >
            <option value="group">{t("configurazione.regole.aUnGruppo")}</option>
            <option value="member">{t("configurazione.regole.aUnMembro")}</option>
          </Select>
          {action.member !== undefined ? (
            <EntitySelect kind="member" noneLabel={t("configurazione.regole.scegli")} aria-label={t("configurazione.regole.membro")} className="field" value={action.member} onChange={(v) => withKey("member", v)} />
          ) : (
            <Select aria-label={t("configurazione.regole.gruppo")} className="field" value={action.group ?? ""} onChange={(e) => withKey("group", e.target.value)}>
              <option value="">{t("configurazione.regole.scegli")}</option>
              {!!action.group && !meta.groups.some((g) => g.name === action.group) && <option value={action.group}>{action.group}</option>}
              {meta.groups.map((g) => (
                <option key={g.key} value={g.name}>
                  {g.name}
                </option>
              ))}
            </Select>
          )}
          <label className="inline-flex min-h-11 items-center gap-2 text-[13px]">
            <input type="checkbox" className="size-5" checked={!!action.replace} onChange={(e) => withKey("replace", e.target.checked)} />
            {t("configurazione.regole.sostituisce")}
          </label>
        </>
      )}
    </div>
  );
}

/** Condizione propria dell'azione, sui dati di ciascun impatto. */
function ImpactCondition({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const t = useT();
  return <ConditionBuilder label={t("configurazione.regole.soloImpatti")} value={value} onChange={onChange} type="impact" />;
}
