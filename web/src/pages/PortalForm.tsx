// Il modulo di una voce del catalogo del portale: i passi con i loro campi, le stesse parti per il wizard del
// richiedente (Portal.tsx) e per l'anteprima dal vivo dell'editor (PortalCatalogConfig.tsx).
import { Select } from "../select";
import type { ReactNode } from "react";
import { CustomFieldsForm, formFacts, isEmpty, visibleIn } from "../fields";
import { compiled, evalCondition, type Facts } from "../condition";
import type { EntryView, NamedKey, StepField, StepView } from "../portalCatalogApi";
import type { PortalDraft } from "../reportApi";
import { DRIVERS, date } from "../ui";
import { useT } from "../i18n/react";

export type Form = {
  title: string;
  description: string;
  requesting_unit_id: string;
  driver: string;
  is_mandatory: boolean;
  regulatory_deadline: string;
  desired_date: string;
};

export const EMPTY: Form = { title: "", description: "", requesting_unit_id: "", driver: "", is_mandatory: false, regulatory_deadline: "", desired_date: "" };

export function formOf(d?: PortalDraft): { f: Form; custom: Record<string, unknown> } {
  if (!d) return { f: EMPTY, custom: {} };
  const s = (k: string) => (typeof d.data[k] === "string" ? (d.data[k] as string) : "");
  return {
    f: {
      ...EMPTY,
      title: d.title,
      description: s("description"),
      requesting_unit_id: s("requesting_unit_id"),
      driver: s("driver"),
      is_mandatory: d.data.is_mandatory === true,
      regulatory_deadline: s("regulatory_deadline"),
      desired_date: s("desired_date"),
    },
    custom: (d.data.fields as Record<string, unknown>) ?? {},
  };
}

export function Field({ label, hint, required, children }: { label: string; hint?: string; required?: boolean; children: ReactNode }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="label">
        {label}
        {required && <span className="text-flag"> *</span>}
      </span>
      {children}
      {hint && <span className="text-[12.5px] text-muted">{hint}</span>}
    </label>
  );
}

/** I fatti per le condizioni di visibilità: i valori dei campi più quelli della domanda. */
export function entryFacts(entry: EntryView, f: Form, custom: Record<string, unknown>): Facts {
  return formFacts(entry.fields, custom, {
    tipo: [entry.type],
    ...(f.driver ? { driver: [f.driver] } : {}),
    obbligatoria: [f.is_mandatory],
  });
}

const stdVisible = (s: StepField, facts: Facts) => !s.visible_when || evalCondition(compiled(s.visible_when), facts);

/** Titolo di un passo: quello scelto, altrimenti il nome di prodotto del modello o «Passo N». */
export function useStepTitle() {
  const t = useT();
  return (s: { title?: string; preset?: string }, index: number) =>
    s.title || (s.preset === "context" ? t("lavoro.portal.stepContext") : s.preset === "details" ? t("lavoro.portal.stepDetails") : t("lavoro.catalogo.stepN", { n: index + 1 }));
}

/** Mancano campi obbligatori visibili in questo passo? */
export function stepIncomplete(entry: EntryView, step: StepView, f: Form, custom: Record<string, unknown>, facts: Facts): boolean {
  return step.fields.some((sf) => {
    if (!sf.required || !stdVisible(sf, facts)) return false;
    if (sf.std) {
      const v = (f as unknown as Record<string, unknown>)[sf.key];
      return typeof v === "boolean" ? !v : isEmpty(v);
    }
    const fv = entry.fields.find((x) => x.key === sf.key);
    return !!fv && visibleIn(fv, facts) && isEmpty(custom[sf.key]);
  });
}

/** Il primo passo: che cosa serve, sempre presente. */
export function FirstStep({ entry, f, set, extra }: { entry: EntryView; f: Form; set: (k: keyof Form) => (e: { target: { value: string } }) => void; extra?: ReactNode }) {
  const t = useT();
  return (
    <>
      {entry.intro && <p className="m-0 max-w-2xl whitespace-pre-line text-muted">{entry.intro}</p>}
      <Field label={entry.title_label || t("lavoro.portal.whatLabel")} hint={entry.title_label ? undefined : t("lavoro.portal.whatHint")} required>
        <input className="field text-[16px] font-semibold" maxLength={500} value={f.title} onChange={set("title")} />
      </Field>
      {extra}
      <Field label={entry.desc_label || t("lavoro.portal.needLabel")} hint={entry.desc_label ? undefined : t("lavoro.portal.needHint")}>
        <textarea className="field min-h-40 leading-relaxed" value={f.description} onChange={set("description")} />
      </Field>
    </>
  );
}

/** Un passo del modulo: i dati della domanda e i campi del tipo scelti per la voce, nell'ordine. */
export function StepBody({
  entry,
  step,
  f,
  setF,
  custom,
  setCustom,
  units,
  myUnit,
}: {
  entry: EntryView;
  step: StepView;
  f: Form;
  setF: (f: Form) => void;
  custom: Record<string, unknown>;
  setCustom: (k: string, v: unknown) => void;
  units: NamedKey[];
  myUnit: string;
}) {
  const t = useT();
  const facts = entryFacts(entry, f, custom);
  const set = (k: keyof Form) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });
  const std = step.fields.filter((x) => x.std && stdVisible(x, facts));
  const customViews = step.fields.flatMap((x) => (x.std ? [] : entry.fields.filter((v) => v.key === x.key)));
  const label = (s: StepField, def: string) => s.label || def;
  return (
    <>
      {step.intro && <p className="m-0 max-w-2xl whitespace-pre-line text-muted">{step.intro}</p>}
      {std.map((s) => {
        switch (s.key) {
          case "requesting_unit_id":
            return (
              <Field key={s.key} label={label(s, t("lavoro.portal.unitLabel"))} hint={s.help} required={s.required}>
                <Select className="field" value={f.requesting_unit_id} onChange={set("requesting_unit_id")}>
                  <option value="">{t("lavoro.portal.choose")}</option>
                  {units.map((u) => <option key={u.key} value={u.key}>{u.name}</option>)}
                </Select>
                {!!myUnit && f.requesting_unit_id === myUnit && <span className="text-[12.5px] text-muted">{t("dafare.portal.unitFromProfile")}</span>}
              </Field>
            );
          case "driver":
            return (
              <Field key={s.key} label={label(s, t("lavoro.portal.whyLabel"))} hint={s.help} required={s.required}>
                <Select className="field" value={f.driver} onChange={set("driver")}>
                  <option value="">{t("lavoro.portal.dontKnow")}</option>
                  {Object.entries(DRIVERS).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                </Select>
              </Field>
            );
          case "desired_date":
            return (
              <Field key={s.key} label={label(s, t("lavoro.portal.whenLabel"))} hint={s.help ?? t("lavoro.portal.whenHint")} required={s.required}>
                <input type="date" className="field max-w-xs" value={f.desired_date} onChange={set("desired_date")} />
              </Field>
            );
          case "is_mandatory":
            return (
              <div key={s.key} className="flex flex-col gap-3 border-t border-hair pt-4">
                <label className="flex min-h-11 items-center gap-2">
                  <input type="checkbox" className="size-5 accent-[var(--accent)]" checked={f.is_mandatory} onChange={(e) => setF({ ...f, is_mandatory: e.target.checked })} />
                  <span className="font-semibold">{label(s, t("lavoro.portal.mandatory"))}</span>
                </label>
                {s.help && <span className="text-[12.5px] text-muted">{s.help}</span>}
              </div>
            );
          case "regulatory_deadline":
            return f.is_mandatory ? (
              <Field key={s.key} label={label(s, t("lavoro.portal.deadlineLabel"))} hint={s.help} required={s.required}>
                <input type="date" className="field max-w-xs" value={f.regulatory_deadline} onChange={set("regulatory_deadline")} />
              </Field>
            ) : null;
        }
        return null;
      })}
      {customViews.length > 0 && (
        <div className="grid grid-cols-1 gap-x-8 gap-y-5 md:grid-cols-2">
          <CustomFieldsForm fields={customViews} values={custom} onChange={setCustom} facts={facts} />
        </div>
      )}
    </>
  );
}

/** «Controlla e invia»: il riepilogo di ciò che si è scritto. */
export function ReviewStep({ entry, f, custom, units }: { entry: EntryView; f: Form; custom: Record<string, unknown>; units: NamedKey[] }) {
  const t = useT();
  const facts = entryFacts(entry, f, custom);
  const stdFields = entry.steps.flatMap((s) => s.fields.filter((x) => x.std && stdVisible(x, facts)));
  const has = (k: string) => stdFields.some((x) => x.key === k);
  const unitName = units.find((u) => u.key === f.requesting_unit_id)?.name;
  const rows: [string, string][] = [
    [entry.title_label || t("lavoro.portal.sumRequest"), f.title || "—"],
    [entry.desc_label || t("lavoro.portal.sumNeed"), f.description || "—"],
    ...(has("requesting_unit_id") ? [[t("lavoro.portal.sumUnit"), unitName ?? "—"] as [string, string]] : []),
    ...(has("driver") ? [[t("lavoro.portal.sumReason"), f.driver ? DRIVERS[f.driver] : "—"] as [string, string]] : []),
    ...(has("desired_date") ? [[t("lavoro.portal.sumBy"), f.desired_date ? date(f.desired_date) : "—"] as [string, string]] : []),
    ...(has("is_mandatory") && f.is_mandatory
      ? [[t("lavoro.portal.sumMandatory"), f.regulatory_deadline ? t("lavoro.portal.yesDeadline", { date: date(f.regulatory_deadline) }) : t("lavoro.portal.yes")] as [string, string]]
      : []),
    ...entry.fields
      .filter((x) => visibleIn(x, facts) && !isEmpty(custom[x.key]))
      .map(
        (x) =>
          [
            x.name,
            (Array.isArray(custom[x.key]) ? (custom[x.key] as unknown[]) : [custom[x.key]]).map((v) => x.options?.find((o) => o.id === v)?.name ?? String(v)).join(", "),
          ] as [string, string],
      ),
  ];
  return (
    <dl className="m-0 flex flex-col">
      {rows.map(([k, v], i) => (
        <div key={`${k}-${i}`} className="grid grid-cols-1 gap-0.5 border-b border-hair py-2 sm:grid-cols-[160px_minmax(0,1fr)] sm:gap-4">
          <dt className="text-muted">{k}</dt>
          <dd className="m-0 break-words whitespace-pre-line">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Icone generiche delle carte del catalogo. */
export const ICONS = ["doc", "key", "shield", "briefcase", "tool", "people", "chart", "mail"] as const;
const PATHS: Record<string, string> = {
  doc: "M7 3h7l5 5v13H7zM14 3v5h5M10 13h6M10 17h6",
  key: "M15 9a4 4 0 1 1-3.5 6L5 15v-3h3v-2h2.5A4 4 0 0 1 15 9z",
  shield: "M12 3l7 3v6c0 4.5-3 7.5-7 9-4-1.500-7-4.500-7-9V6zM9 12l2 2 4-4",
  briefcase: "M4 8h16v11H4zM9 8V5h6v3M4 13h16",
  tool: "M14 6a4 4 0 0 0 5 5l-9 9a2 2 0 0 1-3-3l9-9a4 4 0 0 0-2-2zM16 4l4 4",
  people: "M9 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM3 20c0-3.500 2.500-6 6-6s6 2.500 6 6M17 9a2.500 2.500 0 1 0 0-5M17 14c2.500 0 4 2 4 5",
  chart: "M4 20V4M4 20h16M8 16v-4M12 16V8M16 16v-6",
  mail: "M3 6h18v12H3zM3 7l9 7 9-7",
};
export function CatalogIcon({ name, className = "size-6" }: { name: string; className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="none" stroke="currentColor" strokeWidth="1.700" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d={PATHS[name] ?? PATHS.doc} />
    </svg>
  );
}
