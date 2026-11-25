// Configurazione → Portale: il catalogo delle voci, l'editor visivo del modulo di ciascuna (passi, campi, testi,
// condizioni di visibilità) con l'anteprima dal vivo, e le regole di instradamento «se… allora assegna…».
// «Modifica → anteprima → pubblica»: il modulo si salva in bozza e il richiedente vede solo quello pubblicato.
import { Select } from "../select";
import { useEffect, useMemo, useState } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "../shell";
import { ErrorNote, Loading, SectionTitle } from "../ui";
import { catalogApi, type CatalogConfig, type Entry, type EntryForm, type EntryPreview, type FormField, type FormStep, type Priority, type Route } from "../portalCatalogApi";
import { useDebounced } from "../casaApi";
import { ConditionBuilder } from "./processEditor/ConditionBuilder";
import { CatalogIcon, EMPTY, FirstStep, ICONS, ReviewStep, StepBody, useStepTitle, type Form } from "./PortalForm";
import { useT } from "../i18n/react";

function clone<T>(x: T): T {
  return JSON.parse(JSON.stringify(x));
}
/** Forma confrontabile del modulo: chiavi in ordine e senza i valori vuoti, che il server non restituisce. */
function canon(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(canon);
  if (v && typeof v === "object") {
    return Object.fromEntries(
      Object.entries(v as Record<string, unknown>)
        .filter(([, x]) => x !== "" && x !== false && x != null)
        .sort(([a], [b]) => (a < b ? -1 : 1))
        .map(([k, x]) => [k, canon(x)]),
    );
  }
  return v;
}
const norm = (f: EntryForm) => JSON.stringify(canon(f));

export function PortalCatalogConfig() {
  const t = useT();
  const cfg = useQuery({ queryKey: ["portal-catalog"], queryFn: catalogApi.config });
  const [sel, setSel] = useState<string | null>(null);
  return (
    <>
      <PageHeader path={t("configurazione.path.portale")} title={t("configurazione.portale.title")} />
      <div className="flex max-w-6xl flex-col gap-8 px-4 pb-12 md:px-7">
        <ErrorNote error={cfg.error} />
        {!cfg.data && !cfg.error && <Loading className="m-0 text-muted" />}
        {cfg.data && (sel && cfg.data.entries.some((e) => e.key === sel) ? <EntryEditor key={sel} cfg={cfg.data} entryKey={sel} onBack={() => setSel(null)} /> : <EntryList cfg={cfg.data} onOpen={setSel} />)}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------
// L'elenco delle voci

function EntryList({ cfg, onOpen }: { cfg: CatalogConfig; onOpen: (key: string) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const refresh = () => Promise.all([qc.invalidateQueries({ queryKey: ["portal-catalog"] }), qc.invalidateQueries({ queryKey: ["portal"] })]);
  const toggle = useMutation({ mutationFn: (v: { key: string; active: boolean }) => catalogApi.update(v.key, { active: v.active }), onSuccess: refresh });
  const order = useMutation({ mutationFn: (keys: string[]) => catalogApi.reorder(keys), onSuccess: refresh });
  const [name, setName] = useState("");
  const [type, setType] = useState("");
  const create = useMutation({
    mutationFn: () => catalogApi.create({ label: name, type: type || cfg.types[0]?.key }),
    onSuccess: async (e) => {
      setName("");
      await refresh();
      onOpen(e.key);
    },
  });
  const move = (i: number, d: number) => {
    const keys = cfg.entries.map((e) => e.key);
    [keys[i], keys[i + d]] = [keys[i + d], keys[i]];
    order.mutate(keys);
  };
  const typeName = (k: string) => cfg.types.find((x) => x.key === k)?.name ?? k;
  return (
    <>
      <p className="m-0 max-w-2xl text-muted">{t("configurazione.portale.intro")}</p>
      <section className="flex flex-col gap-2" aria-label={t("configurazione.portale.entries")}>
        <SectionTitle>{t("configurazione.portale.entries")}</SectionTitle>
        <ul className="m-0 flex list-none flex-col p-0">
          {cfg.entries.map((e, i) => (
            <li key={e.key} className="flex flex-wrap items-center gap-3 border-b border-hair py-3">
              <span className="text-accent"><CatalogIcon name={e.icon} /></span>
              <span className="flex min-w-0 flex-1 basis-48 flex-col">
                <span className="font-semibold">{e.label}</span>
                <span className="text-[12.5px] text-muted">
                  {typeName(e.type)}
                  {e.category && ` · ${e.category}`}
                  {e.draft && ` · ${t("configurazione.portale.unpublished")}`}
                  {!e.active && ` · ${t("configurazione.portale.inactive")}`}
                </span>
              </span>
              <label className="flex min-h-11 items-center gap-2">
                <input type="checkbox" className="size-5 accent-[var(--accent)]" checked={e.active} disabled={toggle.isPending}
                  aria-label={t("configurazione.portale.activeNamed", { name: e.label })} onChange={(x) => toggle.mutate({ key: e.key, active: x.target.checked })} />
                <span>{t("configurazione.portale.active")}</span>
              </label>
              <span className="flex gap-1">
                <button type="button" className="btn" disabled={i === 0 || order.isPending} aria-label={t("configurazione.portale.moveUpNamed", { name: e.label })} onClick={() => move(i, -1)}>↑</button>
                <button type="button" className="btn" disabled={i === cfg.entries.length - 1 || order.isPending} aria-label={t("configurazione.portale.moveDownNamed", { name: e.label })} onClick={() => move(i, 1)}>↓</button>
              </span>
              <button type="button" className="btn" aria-label={t("configurazione.portale.editNamed", { name: e.label })} onClick={() => onOpen(e.key)}>{t("configurazione.portale.edit")}</button>
            </li>
          ))}
        </ul>
        <ErrorNote error={toggle.error ?? order.error} />
      </section>
      <section className="flex flex-col gap-3" aria-label={t("configurazione.portale.addTitle")}>
        <SectionTitle>{t("configurazione.portale.addTitle")}</SectionTitle>
        <form
          className="flex flex-wrap items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (name.trim()) create.mutate();
          }}
        >
          <label className="flex min-w-56 flex-1 flex-col gap-1">
            <span className="label">{t("configurazione.portale.addName")}</span>
            <input className="field" maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <label className="flex min-w-56 flex-col gap-1">
            <span className="label">{t("configurazione.portale.addType")}</span>
            <Select className="field" value={type || cfg.types[0]?.key || ""} onChange={(e) => setType(e.target.value)}>
              {cfg.types.map((x) => <option key={x.key} value={x.key}>{x.name}</option>)}
            </Select>
          </label>
          <button className="btn btn-primary" disabled={!name.trim() || create.isPending}>{t("configurazione.portale.addButton")}</button>
        </form>
        <ErrorNote error={create.error} />
      </section>
    </>
  );
}

// ---------------------------------------------------------------------
// L'editor di una voce

type Meta = Pick<Entry, "label" | "description" | "category" | "icon" | "group_id" | "active">;
type Pos = { s: number; f: number };

function moveField(form: EntryForm, from: Pos, to: Pos): EntryForm {
  const next = clone(form);
  const [item] = next.steps[from.s].fields.splice(from.f, 1);
  next.steps[to.s].fields.splice(to.f, 0, item);
  return next;
}

function EntryEditor({ cfg, entryKey, onBack }: { cfg: CatalogConfig; entryKey: string; onBack: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const entry = cfg.entries.find((e) => e.key === entryKey)!;
  const typeInfo = cfg.types.find((x) => x.key === entry.type);
  const [meta, setMeta] = useState<Meta>({ label: entry.label, description: entry.description, category: entry.category, icon: entry.icon, group_id: entry.group_id, active: entry.active });
  const [form, setForm] = useState<EntryForm>(() => clone(entry.draft ?? entry.form));
  const [tab, setTab] = useState<"edit" | "preview">("edit");
  const formDirty = norm(form) !== norm(entry.draft ?? entry.form);
  const metaDirty = (Object.keys(meta) as (keyof Meta)[]).some((k) => meta[k] !== entry[k]);
  const apply = (e: Entry) => {
    qc.setQueryData<CatalogConfig>(["portal-catalog"], (c) => c && { ...c, entries: c.entries.map((x) => (x.key === e.key ? e : x)) });
    qc.invalidateQueries({ queryKey: ["portal"] });
  };
  const patch = () => ({ ...meta, ...(formDirty ? { form } : {}) });
  const saveDraft = useMutation({ mutationFn: () => catalogApi.update(entryKey, patch()), onSuccess: apply });
  const publish = useMutation({
    mutationFn: async () => {
      if (formDirty || metaDirty) await catalogApi.update(entryKey, patch());
      return catalogApi.publish(entryKey);
    },
    onSuccess: apply,
  });
  const discard = useMutation({
    mutationFn: () => catalogApi.discard(entryKey),
    onSuccess: (e) => {
      apply(e);
      setForm(clone(e.form));
    },
  });
  const busy = saveDraft.isPending || publish.isPending || discard.isPending;
  const status = formDirty || metaDirty ? t("configurazione.portale.dirty") : entry.draft ? t("configurazione.portale.draftSaved") : t("configurazione.portale.live");

  const debounced = useDebounced(form, 500);
  const preview = useQuery({
    queryKey: ["portal-preview", entryKey, norm(debounced)],
    queryFn: () => catalogApi.preview(entryKey, debounced),
    placeholderData: keepPreviousData,
    retry: false,
  });

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center gap-3">
        <button type="button" className="btn" onClick={onBack}>← {t("configurazione.portale.back")}</button>
        <h2 className="m-0 min-w-0 flex-1 basis-40 text-[20px] font-bold">{entry.label}</h2>
        <span role="status" className="text-[12.5px] text-muted">{status}</span>
        <button type="button" className="btn" disabled={busy || (!formDirty && !metaDirty)} onClick={() => saveDraft.mutate()}>{t("configurazione.portale.saveDraft")}</button>
        <button type="button" className="btn btn-primary" disabled={busy || (!formDirty && !entry.draft)} onClick={() => publish.mutate()}>{t("configurazione.portale.publish")}</button>
        {entry.draft && !formDirty && (
          <button type="button" className="btn" disabled={busy} onClick={() => window.confirm(t("configurazione.portale.confirmDiscard")) && discard.mutate()}>
            {t("configurazione.portale.discard")}
          </button>
        )}
      </div>
      <ErrorNote error={saveDraft.error ?? publish.error ?? discard.error} />
      <p className="m-0 text-[12.5px] text-muted">{t("configurazione.portale.immediateNote")}</p>
      <div role="tablist" aria-label={t("configurazione.portale.title")} className="flex gap-2 lg:hidden">
        {(["edit", "preview"] as const).map((k) => (
          <button key={k} type="button" role="tab" aria-selected={tab === k} className={`btn flex-1 justify-center ${tab === k ? "btn-primary" : ""}`} onClick={() => setTab(k)}>
            {k === "edit" ? t("configurazione.portale.tabEdit") : t("configurazione.portale.tabPreview")}
          </button>
        ))}
      </div>
      <div className="grid grid-cols-1 gap-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,420px)]">
        <div className={`min-w-0 flex-col gap-8 ${tab === "edit" ? "flex" : "hidden lg:flex"}`}>
          <MetaSection cfg={cfg} meta={meta} setMeta={setMeta} form={form} setForm={setForm} />
          <StepsSection cfg={cfg} entry={entry} typeFields={typeInfo?.fields ?? []} form={form} setForm={setForm} />
          <RoutingSection cfg={cfg} entry={entry} form={form} setForm={setForm} />
        </div>
        <aside className={`min-w-0 ${tab === "preview" ? "block" : "hidden lg:block"}`} aria-label={t("configurazione.portale.previewTitle")}>
          <div className="flex flex-col gap-3 lg:sticky lg:top-4">
            <SectionTitle>{t("configurazione.portale.previewTitle")}</SectionTitle>
            <ErrorNote error={preview.error} />
            {preview.data && <PreviewPane data={preview.data} />}
          </div>
        </aside>
      </div>
    </div>
  );
}

function MetaSection({ cfg, meta, setMeta, form, setForm }: { cfg: CatalogConfig; meta: Meta; setMeta: (m: Meta) => void; form: EntryForm; setForm: (f: EntryForm) => void }) {
  const t = useT();
  const set = (k: keyof Meta, v: string | boolean) => setMeta({ ...meta, [k]: v } as Meta);
  return (
    <section className="flex flex-col gap-4" aria-label={t("configurazione.portale.sectionEntry")}>
      <SectionTitle>{t("configurazione.portale.sectionEntry")}</SectionTitle>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.portale.name")}</span>
          <input className="field" maxLength={80} value={meta.label} onChange={(e) => set("label", e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.portale.category")}</span>
          <input className="field" maxLength={60} value={meta.category} onChange={(e) => set("category", e.target.value)} />
        </label>
        <label className="flex flex-col gap-1 sm:col-span-2">
          <span className="label">{t("configurazione.portale.description")}</span>
          <input className="field" maxLength={400} value={meta.description} onChange={(e) => set("description", e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.portale.icon")}</span>
          <Select className="field" value={meta.icon} onChange={(e) => set("icon", e.target.value)}>
            {ICONS.map((i) => <option key={i} value={i}>{t(`configurazione.portale.icon.${i}`)}</option>)}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.portale.visibleTo")}</span>
          <Select className="field" value={meta.group_id} onChange={(e) => set("group_id", e.target.value)}>
            <option value="">{t("configurazione.portale.everyone")}</option>
            {cfg.groups.map((g) => <option key={g.key} value={g.key}>{g.name}</option>)}
          </Select>
        </label>
      </div>
      <div className="grid grid-cols-1 gap-4">
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.portale.intro2")}</span>
          <textarea className="field min-h-20" maxLength={1000} value={form.intro ?? ""} onChange={(e) => setForm({ ...form, intro: e.target.value })} />
        </label>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <label className="flex flex-col gap-1">
            <span className="label">{t("configurazione.portale.titleLabel")}</span>
            <input className="field" maxLength={120} value={form.title_label ?? ""} onChange={(e) => setForm({ ...form, title_label: e.target.value })} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label">{t("configurazione.portale.descLabel")}</span>
            <input className="field" maxLength={120} value={form.desc_label ?? ""} onChange={(e) => setForm({ ...form, desc_label: e.target.value })} />
          </label>
        </div>
        <span className="text-[12.5px] text-muted">{t("configurazione.portale.defaultsHint")}</span>
      </div>
    </section>
  );
}

function StepsSection({ cfg, entry, typeFields, form, setForm }: { cfg: CatalogConfig; entry: Entry; typeFields: { key: string; name: string }[]; form: EntryForm; setForm: (f: EntryForm) => void }) {
  const t = useT();
  const stepTitle = useStepTitle();
  const [drag, setDrag] = useState<Pos | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const isRequest = entry.type === "business_request";
  const used = new Set(form.steps.flatMap((s) => s.fields.map((f) => (f.std ? `std:${f.key}` : f.key))));
  const free: { id: string; std: boolean; key: string; name: string }[] = [
    ...(isRequest ? cfg.standard_fields.filter((f) => !used.has(`std:${f.key}`)).map((f) => ({ id: `std:${f.key}`, std: true, key: f.key, name: f.name })) : []),
    ...typeFields.filter((f) => !used.has(f.key)).map((f) => ({ id: f.key, std: false, key: f.key, name: f.name })),
  ];
  const nameOf = (f: FormField) => (f.std ? cfg.standard_fields : typeFields).find((x) => x.key === f.key)?.name ?? f.key;
  const setStep = (si: number, patch: Partial<FormStep>) => setForm({ ...form, steps: form.steps.map((s, i) => (i === si ? { ...s, ...patch } : s)) });
  const setField = (si: number, fi: number, patch: Partial<FormField>) =>
    setStep(si, { fields: form.steps[si].fields.map((f, i) => (i === fi ? { ...f, ...patch } : f)) });
  const swapSteps = (i: number, d: number) => {
    const steps = [...form.steps];
    [steps[i], steps[i + d]] = [steps[i + d], steps[i]];
    setForm({ ...form, steps });
  };
  const arrow = (si: number, fi: number, d: -1 | 1) => {
    const steps = form.steps;
    if (d === -1) setForm(fi > 0 ? moveField(form, { s: si, f: fi }, { s: si, f: fi - 1 }) : moveField(form, { s: si, f: fi }, { s: si - 1, f: steps[si - 1].fields.length }));
    else setForm(fi < steps[si].fields.length - 1 ? moveField(form, { s: si, f: fi }, { s: si, f: fi + 1 }) : moveField(form, { s: si, f: fi }, { s: si + 1, f: 0 }));
  };
  const canUp = (si: number, fi: number) => fi > 0 || si > 0;
  const canDown = (si: number, fi: number) => fi < form.steps[si].fields.length - 1 || si < form.steps.length - 1;
  const drop = (si: number, fi: number | null) => {
    if (!drag) return;
    const len = form.steps[si].fields.length;
    let to = fi ?? len;
    if (drag.s === si && drag.f < to) to -= 1;
    if (!(drag.s === si && drag.f === to)) setForm(moveField(form, drag, { s: si, f: Math.max(0, to) }));
    setDrag(null);
  };
  return (
    <section className="flex flex-col gap-4" aria-label={t("configurazione.portale.sectionSteps")}>
      <SectionTitle>{t("configurazione.portale.sectionSteps")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{t("configurazione.portale.firstStepNote")}</p>
      <ol className="m-0 flex list-none flex-col gap-4 p-0">
        {form.steps.map((s, si) => (
          <li key={si} className="flex flex-col gap-3 border border-hair p-3" aria-label={t("configurazione.portale.stepNum", { n: si + 1 })}>
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-[12px] text-muted tabular">{si + 1}</span>
              <input className="field min-w-0 flex-1 basis-40 font-semibold" maxLength={80} aria-label={t("configurazione.portale.stepTitleOf", { n: si + 1 })}
                placeholder={stepTitle({ ...s, title: "" }, si)} value={s.title ?? ""} onChange={(e) => setStep(si, { title: e.target.value })} />
              <button type="button" className="btn" disabled={si === 0} aria-label={t("configurazione.portale.stepUp", { n: si + 1 })} onClick={() => swapSteps(si, -1)}>↑</button>
              <button type="button" className="btn" disabled={si === form.steps.length - 1} aria-label={t("configurazione.portale.stepDown", { n: si + 1 })} onClick={() => swapSteps(si, 1)}>↓</button>
              <button type="button" className="btn btn-quiet" aria-label={t("configurazione.portale.stepRemove", { n: si + 1 })} onClick={() => setForm({ ...form, steps: form.steps.filter((_, i) => i !== si) })}>{t("configurazione.portale.remove")}</button>
            </div>
            <textarea className="field min-h-14" maxLength={1000} aria-label={t("configurazione.portale.stepIntroOf", { n: si + 1 })} placeholder={t("configurazione.portale.stepIntro")}
              value={s.intro ?? ""} onChange={(e) => setStep(si, { intro: e.target.value })} />
            <ul
              className={`m-0 flex list-none flex-col p-0 ${drag ? "min-h-10" : ""}`}
              onDragOver={(e) => drag && e.preventDefault()}
              onDrop={(e) => {
                e.preventDefault();
                drop(si, null);
              }}
            >
              {s.fields.length === 0 && <li className="border border-dashed border-hair py-4 text-center text-[13px] text-muted">{t("configurazione.portale.noFields")}</li>}
              {s.fields.map((f, fi) => {
                const id = `${si}:${f.std ? "std:" : ""}${f.key}`;
                const name = nameOf(f);
                return (
                  <li
                    key={id}
                    className="flex flex-col gap-2 border-b border-hair py-2"
                    onDragOver={(e) => drag && e.preventDefault()}
                    onDrop={(e) => {
                      e.preventDefault();
                      e.stopPropagation();
                      drop(si, fi);
                    }}
                  >
                    <div className="flex flex-wrap items-center gap-2">
                      <span
                        draggable
                        role="img"
                        aria-label={t("configurazione.portale.dragHandle")}
                        title={t("configurazione.portale.dragHandle")}
                        className="cursor-grab select-none px-1 text-[16px] leading-none text-muted"
                        onDragStart={(e) => {
                          e.dataTransfer.setData("text/plain", f.key);
                          e.dataTransfer.effectAllowed = "move";
                          setDrag({ s: si, f: fi });
                        }}
                        onDragEnd={() => setDrag(null)}
                      >
                        ⋮⋮
                      </span>
                      <span className="min-w-0 flex-1 basis-32 font-semibold">
                        {name}
                        {f.std && <span className="ml-2 text-[11.5px] font-normal text-muted">{t("configurazione.portale.stdBadge")}</span>}
                      </span>
                      <label className="flex min-h-11 items-center gap-2">
                        <input type="checkbox" className="size-5 accent-[var(--accent)]" checked={!!f.required} aria-label={t("configurazione.portale.requiredNamed", { name })}
                          onChange={(e) => setField(si, fi, { required: e.target.checked })} />
                        <span>{t("configurazione.portale.required")}</span>
                      </label>
                      <button type="button" className="btn" disabled={!canUp(si, fi)} aria-label={t("configurazione.portale.fieldUp", { name })} onClick={() => arrow(si, fi, -1)}>↑</button>
                      <button type="button" className="btn" disabled={!canDown(si, fi)} aria-label={t("configurazione.portale.fieldDown", { name })} onClick={() => arrow(si, fi, 1)}>↓</button>
                      <button type="button" className="btn" aria-expanded={open === id} aria-label={t("configurazione.portale.detailsNamed", { name })} onClick={() => setOpen(open === id ? null : id)}>
                        {t("configurazione.portale.details")}
                        {(f.label || f.help || f.visible_when) && <span aria-hidden="true">•</span>}
                      </button>
                      <button type="button" className="btn btn-quiet" aria-label={t("configurazione.portale.removeNamed", { name })} onClick={() => setStep(si, { fields: s.fields.filter((_, i) => i !== fi) })}>
                        {t("configurazione.portale.remove")}
                      </button>
                    </div>
                    {open === id && (
                      <div className="flex flex-col gap-3 border-l-2 border-accent pl-3">
                        <label className="flex flex-col gap-1">
                          <span className="label">{t("configurazione.portale.fieldLabel")}</span>
                          <input className="field" maxLength={120} placeholder={name} value={f.label ?? ""} onChange={(e) => setField(si, fi, { label: e.target.value })} />
                        </label>
                        <label className="flex flex-col gap-1">
                          <span className="label">{t("configurazione.portale.fieldHelp")}</span>
                          <input className="field" maxLength={300} value={f.help ?? ""} onChange={(e) => setField(si, fi, { help: e.target.value })} />
                        </label>
                        <ConditionBuilder value={f.visible_when ?? ""} onChange={(v) => setField(si, fi, { visible_when: v })} type={entry.type}
                          label={t("configurazione.portale.visibleWhen")} empty={t("configurazione.portale.alwaysShown")} noun={t("configurazione.portale.noun")} />
                      </div>
                    )}
                  </li>
                );
              })}
            </ul>
            {free.length > 0 && (
              <Select className="field max-w-sm" aria-label={t("configurazione.portale.addFieldTo", { n: si + 1 })} value="" onChange={(e) => {
                const x = free.find((y) => y.id === e.target.value);
                if (x) setStep(si, { fields: [...s.fields, x.std ? { key: x.key, std: true } : { key: x.key }] });
              }}>
                <option value="">{t("configurazione.portale.addField")}</option>
                {free.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
              </Select>
            )}
          </li>
        ))}
      </ol>
      {form.steps.length < 8 && (
        <button type="button" className="btn self-start" onClick={() => setForm({ ...form, steps: [...form.steps, { title: "", fields: [] }] })}>{t("configurazione.portale.addStep")}</button>
      )}
    </section>
  );
}

function RoutingSection({ cfg, entry, form, setForm }: { cfg: CatalogConfig; entry: Entry; form: EntryForm; setForm: (f: EntryForm) => void }) {
  const t = useT();
  const setRoute = (i: number, patch: Partial<Route>) => setForm({ ...form, routes: form.routes.map((r, j) => (j === i ? { ...r, ...patch } : r)) });
  return (
    <section className="flex flex-col gap-4" aria-label={t("configurazione.portale.sectionRouting")}>
      <SectionTitle>{t("configurazione.portale.sectionRouting")}</SectionTitle>
      <p className="m-0 max-w-2xl text-[12.5px] text-muted">{t("configurazione.portale.routingHint")}</p>
      <ol className="m-0 flex list-none flex-col gap-4 p-0">
        {form.routes.map((r, i) => {
          return (
            <li key={i} className="flex flex-col gap-3 border border-hair p-3" aria-label={t("configurazione.portale.ruleN", { n: i + 1 })}>
              <ConditionBuilder value={r.when ?? ""} onChange={(v) => setRoute(i, { when: v })} type={entry.type} label={t("configurazione.portale.ruleIf")}
                empty={t("configurazione.portale.always")} noun={t("configurazione.portale.noun")} />
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <label className="flex flex-col gap-1">
                  <span className="label">{t("configurazione.portale.ruleRole")}</span>
                  <Select className="field" value={r.role ?? ""} onChange={(e) => setRoute(i, { role: e.target.value })}>
                    <option value="">{t("configurazione.portale.ruleNoAssign")}</option>
                    {cfg.roles.map((x) => <option key={x.key} value={x.key}>{x.name}</option>)}
                  </Select>
                </label>
                <label className="flex flex-col gap-1">
                  <span className="label">{t("configurazione.portale.ruleTarget")}</span>
                  <Select className="field" value={r.team_id || (r.group_id ? `g:${r.group_id}` : "")} onChange={(e) => {
                    const v = e.target.value;
                    setRoute(i, v.startsWith("g:") ? { team_id: "", group_id: v.slice(2) } : { team_id: v, group_id: "" });
                  }}>
                    <option value="">{t("configurazione.portale.chooseTarget")}</option>
                    <optgroup label={t("configurazione.portale.team")}>
                      {cfg.teams.map((x) => <option key={x.key} value={x.key}>{x.name}</option>)}
                    </optgroup>
                    <optgroup label={t("configurazione.portale.group")}>
                      {cfg.groups.map((x) => <option key={x.key} value={`g:${x.key}`}>{x.name}</option>)}
                    </optgroup>
                  </Select>
                </label>
              </div>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <label className="flex flex-col gap-1">
                  <span className="label">{t("configurazione.portale.priority")}</span>
                  <Select className="field" value={r.priority ?? ""} onChange={(e) => setRoute(i, { priority: e.target.value as Priority | "" })}>
                    <option value="">{t("configurazione.portale.noPriority")}</option>
                    {cfg.priorities.map((p) => <option key={p} value={p}>{t(`configurazione.portale.priority.${p}`)}</option>)}
                  </Select>
                </label>
                <label className="flex flex-col gap-1">
                  <span className="label">{t("configurazione.portale.initialLabel")}</span>
                  <input className="field" maxLength={40} value={r.label ?? ""} onChange={(e) => setRoute(i, { label: e.target.value })} />
                </label>
              </div>
              <button type="button" className="btn btn-quiet self-start" aria-label={t("configurazione.portale.removeRule", { n: i + 1 })} onClick={() => setForm({ ...form, routes: form.routes.filter((_, j) => j !== i) })}>
                {t("configurazione.portale.remove")}
              </button>
            </li>
          );
        })}
      </ol>
      {form.routes.length < 20 && (
        <button type="button" className="btn self-start" onClick={() => setForm({ ...form, routes: [...form.routes, { when: "", role: "", team_id: "", group_id: "", priority: "", label: "" }] })}>
          {t("configurazione.portale.addRule")}
        </button>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Anteprima dal vivo: il modulo come lo vedrebbe il richiedente (nulla si salva).

function PreviewPane({ data }: { data: EntryPreview }) {
  const t = useT();
  const stepTitle = useStepTitle();
  const entry = data.entry;
  const [f, setF] = useState<Form>({ ...EMPTY, requesting_unit_id: data.my_unit });
  const [custom, setCustom] = useState<Record<string, unknown>>({});
  const [idx, setIdx] = useState(0);
  const tabs = useMemo(
    () => [t("lavoro.portal.stepWhat"), ...entry.steps.map((s, i) => stepTitle(s, i)), t("lavoro.portal.stepReview")],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [entry.steps, t],
  );
  useEffect(() => {
    if (idx > tabs.length - 1) setIdx(tabs.length - 1);
  }, [idx, tabs.length]);
  const cur = Math.min(idx, tabs.length - 1);
  const stepView = cur >= 1 && cur <= entry.steps.length ? entry.steps[cur - 1] : undefined;
  const set = (k: keyof Form) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });
  return (
    <div className="flex flex-col gap-4 border border-hair p-4" data-testid="portal-preview">
      <div className="flex flex-col gap-1">
        <span className="flex items-center gap-2 text-[17px] font-bold">
          <span className="text-accent"><CatalogIcon name={entry.icon} className="size-5" /></span>
          {entry.label}
        </span>
        {entry.description && <span className="text-[13px] text-muted">{entry.description}</span>}
      </div>
      <div role="tablist" aria-label={t("configurazione.portale.previewSteps")} className="flex flex-wrap gap-1">
        {tabs.map((label, i) => (
          <button key={i} type="button" role="tab" aria-selected={i === cur} className={`btn ${i === cur ? "btn-primary" : ""}`} onClick={() => setIdx(i)}>
            <span className="font-mono tabular">{i + 1}</span>
            <span className="max-w-32 truncate">{label}</span>
          </button>
        ))}
      </div>
      <div className="flex flex-col gap-4" role="tabpanel">
        <h3 className="m-0 text-[18px] font-bold">{tabs[cur]}</h3>
        {cur === 0 && <FirstStep entry={entry} f={f} set={set} />}
        {stepView && <StepBody entry={entry} step={stepView} f={f} setF={setF} custom={custom} setCustom={(k, v) => setCustom((c) => ({ ...c, [k]: v }))} units={data.units} myUnit={data.my_unit} />}
        {cur === tabs.length - 1 && <ReviewStep entry={entry} f={f} custom={custom} units={data.units} />}
      </div>
    </div>
  );
}
