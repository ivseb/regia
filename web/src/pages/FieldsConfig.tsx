// Configurazione → Campi personalizzati (sdlc.md §10): elenco dei campi, creazione e
// modifica (tipo di dato, opzioni con colori, multiplo, predefinito, aiuto, vincoli),
// associazione ai tipi di elemento (obbligatorio, da una fase in poi, visibile se, sezione,
// ordine), archiviazione, export e import in JSON. Serve fields.admin o tenant.admin.
import { Select } from "../select";
import { ConditionBuilder } from "./processEditor/ConditionBuilder";
import { useEffect, useMemo, useRef, useState, type ChangeEvent, type ReactNode } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";
import {
  COLOR_LABELS,
  DATA_TYPE_EXAMPLES,
  DATA_TYPE_LABELS,
  fieldsApi,
  type AssocInput,
  type DataType,
  type FieldCatalog,
  type FieldConfig,
  type FieldInput,
  type FieldSettings,
  type FieldView,
  type ImportReport,
  type OptionColor,
  type OptionInput,
} from "../fieldsApi";
import { COLOR_DOT, FieldEditor, FieldValue, OptionChip } from "../fields";
import { fieldsConfigRoute } from "../router";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, ago, mainPath } from "../ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { tr } from "../i18n/core";

const NEW = "nuovo";

export function FieldsConfig() {
  const { campo, tipo } = fieldsConfigRoute.useSearch();
  const navigate = useNavigate({ from: fieldsConfigRoute.fullPath });
  const list = useQuery({ queryKey: ["fields-admin"], queryFn: fieldsApi.list });
  const catalog = useQuery({ queryKey: ["fields-catalog"], queryFn: fieldsApi.catalog, staleTime: 60_000 });
  const [text, setText] = useState("");
  const [archived, setArchived] = useState(false);
  const go = (s: { campo?: string; tipo?: string }) => navigate({ search: { campo: s.campo, tipo: s.tipo ?? tipo } });
  // Sul telefono l'editor sta sotto l'elenco: scegliendo un campo lo si porta in vista.
  const editor = useRef<HTMLElement>(null);
  useEffect(() => {
    if (campo && window.innerWidth < 768) editor.current?.scrollIntoView({ block: "start" });
  }, [campo]);

  if (list.error instanceof ApiError && list.error.status === 403) {
    return (
      <>
        <PageHeader path={tr("configurazione.path.fields")} title={tr("configurazione.common.campiPersonalizzati")} />
        <p className="px-4 text-muted md:px-7">{tr("configurazione.fields.serveIlRuoloAmministratore")}</p>
      </>
    );
  }
  const items = list.data?.items ?? [];
  const t = text.trim().toLowerCase();
  const shown = items
    .filter((f) => archived || !f.archived)
    .filter((f) => !tipo || f.types.some((a) => a.type === tipo))
    .filter((f) => !t || f.name.toLowerCase().includes(t) || f.key.includes(t) || f.aliases.some((a) => a.includes(t)));
  if (tipo) shown.sort((a, b) => ordOf(a, tipo) - ordOf(b, tipo));
  const selected = campo && campo !== NEW ? items.find((f) => f.key === campo) : undefined;

  return (
    <>
      <PageHeader
        path={tr("configurazione.path.fields")}
        title={tr("configurazione.common.campiPersonalizzati")}
        actions={
          <>
            <ImportButton />
            <a href={fieldsApi.exportUrl} className="btn no-underline" download>
              {tr("configurazione.common.esporta")}
            </a>
            <button className="btn btn-primary" onClick={() => go({ campo: NEW })}>
              {tr("configurazione.fields.nuovoCampo")}
            </button>
          </>
        }
      />
      <div className="flex flex-wrap items-center gap-3 border-b border-hair px-4 pb-3 md:px-7">
        <label className="flex min-w-[200px] flex-1 items-center gap-2 sm:max-w-xs">
          <span className="sr-only">{tr("configurazione.common.cerca")}</span>
          <input className="field" placeholder={tr("configurazione.fields.cercaPerNomeChiave")} value={text} onChange={(e) => setText(e.target.value)} />
        </label>
        <label className="flex items-center gap-2">
          <span className="label">{tr("configurazione.common.tipo")}</span>
          <Select className="field w-auto" value={tipo ?? ""} onChange={(e) => navigate({ search: { campo, tipo: e.target.value || undefined } })}>
            <option value="">{tr("configurazione.fields.tuttiITipi")}</option>
            {(catalog.data?.types ?? []).map((ty) => (
              <option key={ty.key} value={ty.key}>{ty.name}</option>
            ))}
          </Select>
        </label>
        <label className="flex items-center gap-2">
          <input type="checkbox" className="size-4 accent-[var(--accent)]" checked={archived} onChange={(e) => setArchived(e.target.checked)} />
          <span>{tr("configurazione.common.mostraGliArchiviati")}</span>
        </label>
      </div>
      <div className="flex min-h-0 flex-1 flex-wrap">
        <div className="min-w-0 flex-[999_1_520px] px-4 md:px-7">
          <ErrorNote error={list.error} />
          {tipo && catalog.data && <TypeOrder type={tipo} fields={shown.filter((f) => !f.archived)} catalog={catalog.data} />}
          <FieldList items={shown} selected={campo} onSelect={(k) => go({ campo: k })} loading={list.isPending} />
        </div>
        {campo && catalog.data && (
          <aside ref={editor} aria-label={tr("configurazione.fields.modificaDelCampo")} className="flex min-w-0 flex-[1_1_420px] flex-col gap-5 border-l border-rule px-4 py-5 md:px-6">
            {campo === NEW ? (
              <FieldForm key={NEW} catalog={catalog.data} onSaved={(f) => go({ campo: f.key })} onClose={() => go({})} />
            ) : selected ? (
              <FieldForm key={selected.key + selected.updated_at} field={selected} catalog={catalog.data} onSaved={(f) => go({ campo: f.key })} onClose={() => go({})} />
            ) : (
              list.data && <p className="text-muted">{tr("configurazione.fields.fieldNotFound", { name: campo })}</p>
            )}
          </aside>
        )}
      </div>
    </>
  );
}

function ordOf(f: FieldConfig, type: string): number {
  return f.types.find((a) => a.type === type)?.ordinal ?? 9999;
}

function FieldList({ items, selected, onSelect, loading }: { items: FieldConfig[]; selected?: string; onSelect: (k: string) => void; loading: boolean }) {
  const t = useT();
  return (
    <table className="w-full border-collapse tabular">
      <thead>
        <tr className="label h-9 text-left">
          <th scope="col" className="border-b-2 border-rule pr-3">{t("configurazione.fields.campo")}</th>
          <th scope="col" className="hidden border-b-2 border-rule px-3 sm:table-cell">{t("configurazione.fields.chiaveEAlias")}</th>
          <th scope="col" className="border-b-2 border-rule px-3">{t("configurazione.common.tipo")}</th>
          <th scope="col" className="hidden border-b-2 border-rule pl-3 md:table-cell">{t("configurazione.common.tipiDiElemento")}</th>
        </tr>
      </thead>
      <tbody>
        {items.map((f) => {
          const on = selected === f.key;
          return (
            <tr key={f.id} className={`border-b border-hair align-top ${on ? "bg-sel" : "hover:bg-sel/50"}`}>
              <td className="py-2.5 pr-3">
                <button className="text-left font-medium text-fg hover:underline" aria-current={on ? "true" : undefined} onClick={() => onSelect(f.key)}>
                  {f.name}
                </button>
                {f.archived && <span className="ml-2 border border-hair px-1 text-[11px] text-muted">{t("configurazione.common.archiviato")}</span>}
                {f.help && <span className="block text-[12px] text-muted">{f.help}</span>}
                <span className="block font-mono text-[11.5px] text-muted sm:hidden">{f.key}</span>
              </td>
              <td className="hidden px-3 py-2.5 font-mono text-[12px] sm:table-cell">
                {f.key}
                {f.aliases.length > 0 && <span className="block text-muted">{f.aliases.join(", ")}</span>}
              </td>
              <td className="px-3 py-2.5 text-[12.5px]">
                {DATA_TYPE_LABELS[f.data_type] ?? f.data_type}
                {f.multi && <span className="text-muted"> {t("configurazione.fields.multiplo")}</span>}
                {f.options && f.options.length > 0 && (
                  <span className="mt-1 flex flex-wrap gap-1">
                    {f.options.filter((o) => !o.archived).slice(0, 4).map((o) => <OptionChip key={o.id} o={o} />)}
                    {f.options.filter((o) => !o.archived).length > 4 && <span className="text-muted">…</span>}
                  </span>
                )}
              </td>
              <td className="hidden py-2.5 pl-3 text-[12.5px] md:table-cell">
                {f.types.length === 0 ? (
                  <span className="text-muted">{t("configurazione.common.nessuno")}</span>
                ) : (
                  <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
                    {f.types.map((a) => (
                      <li key={a.type}>
                        {a.type_name}
                        <span className="text-muted">
                          {a.required ? t("configurazione.fields.obbligatorio") : a.required_from ? t("configurazione.fields.requiredFromSuffix", { stage: a.required_from }) : ""}
                          {a.visible_when ? t("configurazione.fields.condizionale") : ""}
                        </span>
                        {a.problem && <span className="block text-flag">{a.problem}</span>}
                      </li>
                    ))}
                  </ul>
                )}
              </td>
            </tr>
          );
        })}
        {!loading && items.length === 0 && (
          <tr>
            <td colSpan={4} className="py-8 text-muted">{t("configurazione.fields.nessunCampoCreaneUno")}</td>
          </tr>
        )}
      </tbody>
    </table>
  );
}

/** Ordine dei campi di un tipo (come compaiono nel dettaglio e nei form). */
function TypeOrder({ type, fields, catalog }: { type: string; fields: FieldConfig[]; catalog: FieldCatalog }) {
  const t = useT();
  const qc = useQueryClient();
  const [order, setOrder] = useState<string[]>(fields.map((f) => f.key));
  const sig = fields.map((f) => f.key).join("|");
  useEffect(() => setOrder(sig ? sig.split("|") : []), [sig]);
  const save = useMutation({
    mutationFn: () => fieldsApi.reorder(type, order),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["fields-admin"] }),
  });
  const name = catalog.types.find((t) => t.key === type)?.name ?? type;
  if (fields.length < 2) return null;
  const move = (i: number, d: number) => {
    const j = i + d;
    if (j < 0 || j >= order.length) return;
    const next = [...order];
    [next[i], next[j]] = [next[j], next[i]];
    setOrder(next);
  };
  const changed = order.join("|") !== sig;
  const byKey = new Map(fields.map((f) => [f.key, f]));
  return (
    <details className="mt-4 mb-2 border border-hair px-3 py-2">
      <summary className="cursor-pointer font-semibold">{t("configurazione.fields.fieldOrderOf", { name })}</summary>
      <ol className="m-0 mt-2 flex list-none flex-col p-0">
        {order.map((k, i) => (
          <li key={k} className="flex items-center gap-2 border-b border-hair py-1">
            <span className="w-6 font-mono text-[12px] text-muted tabular">{i + 1}</span>
            <span className="min-w-0 flex-1 truncate">
              {byKey.get(k)?.name}
              {byKey.get(k)?.types.find((a) => a.type === type)?.section && (
                <span className="text-[12px] text-muted"> · {byKey.get(k)?.types.find((a) => a.type === type)?.section}</span>
              )}
            </span>
            <button className="btn px-2 py-0.5" aria-label={t("configurazione.common.moveUpNamed", { name: byKey.get(k)?.name ?? "" })} disabled={i === 0} onClick={() => move(i, -1)}>↑</button>
            <button className="btn px-2 py-0.5" aria-label={t("configurazione.common.moveDownNamed", { name: byKey.get(k)?.name ?? "" })} disabled={i === order.length - 1} onClick={() => move(i, 1)}>↓</button>
          </li>
        ))}
      </ol>
      <div className="mt-2 flex gap-2">
        <button className="btn btn-primary" disabled={!changed || save.isPending} onClick={() => save.mutate()}>{t("configurazione.fields.salvaLOrdine")}</button>
        {changed && <button className="btn" onClick={() => setOrder(sig.split("|"))}>{t("configurazione.common.annulla")}</button>}
      </div>
      <ErrorNote error={save.error} />
    </details>
  );
}

// ---------------------------------------------------------------------
// Creazione e modifica

type Draft = {
  key: string;
  name: string;
  data_type: DataType;
  multi: boolean;
  help: string;
  aliases: string;
  settings: FieldSettings;
  defaultValue: unknown;
  option_set_id: string;
};

const KEY_FROM = (name: string) =>
  name
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .replace(/^(\d)/, "c_$1")
    .slice(0, 63);

function FieldForm({ field, catalog, onSaved, onClose }: { field?: FieldConfig; catalog: FieldCatalog; onSaved: (f: FieldConfig) => void; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const isNew = !field;
  const [d, setD] = useState<Draft>(() => ({
    key: field?.key ?? "",
    name: field?.name ?? "",
    // Il tipo non cambia dopo la creazione: per un campo nuovo va scelto, non lasciato al primo della lista.
    data_type: field?.data_type ?? ("" as DataType),
    multi: field?.multi ?? false,
    help: field?.help ?? "",
    aliases: (field?.aliases ?? []).join(", "),
    settings: field?.settings ?? {},
    defaultValue: field?.default ?? null,
    option_set_id: "",
  }));
  const [keyTouched, setKeyTouched] = useState(!isNew);
  const [options, setOptions] = useState<OptionInput[]>(() => (field?.options ?? []).map((o) => ({ id: o.id, name: o.name, color: o.color ?? "", archived: o.archived, labels: o.labels })));
  function set<K extends keyof Draft>(k: K, v: Draft[K]) {
    setD((x) => ({ ...x, [k]: v }));
  }
  const choice = d.data_type === "enum" || d.data_type === "version";
  const done = (f: FieldConfig) => {
    qc.invalidateQueries({ queryKey: ["fields-admin"] });
    qc.invalidateQueries({ queryKey: ["query-fields"] });
    qc.invalidateQueries({ queryKey: ["type-fields"] });
    qc.invalidateQueries({ queryKey: ["element"] });
    onSaved(f);
  };
  const save = useMutation({
    mutationFn: async () => {
      const body: FieldInput = {
        name: d.name,
        help: d.help,
        settings: d.settings,
        aliases: d.aliases.split(",").map((a) => a.trim()).filter(Boolean),
        default: d.defaultValue ?? null,
      };
      if (isNew) {
        body.key = d.key;
        body.data_type = d.data_type;
        body.multi = d.multi;
        if (choice) {
          if (d.option_set_id) body.option_set_id = d.option_set_id;
          else body.options = options.filter((o) => o.name.trim());
        }
        return fieldsApi.create(body);
      }
      if (d.key !== field.key) body.key = d.key;
      if (d.multi !== field.multi) body.multi = d.multi;
      let out = await fieldsApi.update(field.key, body);
      if (choice && optionsChanged(field, options)) out = await fieldsApi.saveOptions(out.key, options.filter((o) => o.name.trim()));
      return out;
    },
    onSuccess: done,
  });
  const archive = useMutation({ mutationFn: (v: boolean) => fieldsApi.update(field!.key, { archived: v }), onSuccess: done });
  const remove = useMutation({
    mutationFn: () => fieldsApi.remove(field!.key),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["fields-admin"] });
      onClose();
    },
  });
  // Anteprima del campo come lo vedrà chi compila (anche per scegliere il predefinito).
  const preview: FieldView = {
    key: d.key || "anteprima",
    name: d.name || t("configurazione.fields.nuovoCampo"),
    data_type: d.data_type,
    multi: d.multi,
    help: d.help,
    section: "",
    ordinal: 0,
    required: false,
    settings: d.settings,
    options: (choice ? options : []).map((o, i) => ({ id: o.id ?? `nuova-${i}`, name: o.name, color: o.color, archived: o.archived, labels: o.labels })),
    visible: true,
    required_now: false,
  };
  const used = field?.in_use ?? false;
  const canDefault = isNew ? !choice : !choice || !optionsChanged(field, options);
  return (
    <>
      <div className="flex items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <span className="font-mono text-[12px] text-accent">{isNew ? t("configurazione.fields.newFieldCaps") : field.key}</span>
          <h2 className="m-0 text-[20px] leading-[1.15] font-bold">{isNew ? t("configurazione.fields.nuovoCampo") : field.name}</h2>
          {!isNew && (
            <span className="text-[12.5px] text-muted">
              {field.elements > 0 ? t("configurazione.fields.usedIn", { count: field.elements }) : used ? t("configurazione.fields.inUso") : t("configurazione.fields.nonAncoraUsato")} · {t("configurazione.fields.updatedAgo", { when: ago(field.updated_at) })}
            </span>
          )}
        </div>
        <button className="btn py-1" onClick={onClose}>{t("configurazione.common.chiudi")}</button>
      </div>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <SectionTitle>{t("configurazione.fields.definizione")}</SectionTitle>
        <Labeled label={t("configurazione.common.nome")}>
          <input
            className="field"
            required
            maxLength={200}
            value={d.name}
            onChange={(e) => {
              set("name", e.target.value);
              if (!keyTouched) set("key", KEY_FROM(e.target.value));
            }}
          />
        </Labeled>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Labeled label={t("configurazione.common.chiave")} hint={used ? t("configurazione.fields.nonCambiaSuUn2") : t("configurazione.fields.perQueryCondizioniE")}>
            <input
              className="field font-mono"
              required
              pattern="[a-z][a-z0-9_]{0,62}"
              disabled={used || field?.system}
              value={d.key}
              onChange={(e) => {
                setKeyTouched(true);
                set("key", e.target.value);
              }}
            />
          </Labeled>
          <Labeled
            label={t("configurazione.fields.tipoDiDato")}
            hint={
              isNew
                ? t("configurazione.fields.typeHint", { example: (d.data_type && DATA_TYPE_EXAMPLES[d.data_type]) || t("configurazione.fields.chooseTypeExample") })
                : t("configurazione.fields.nonCambiaDopoLa")
            }
          >
            <Select
              className="field"
              required
              disabled={!isNew}
              value={d.data_type}
              onChange={(e) => {
                const dt = e.target.value as DataType;
                setD((x) => ({ ...x, data_type: dt, settings: {}, defaultValue: null, multi: x.multi && catalog.multi_types.includes(dt) }));
              }}
            >
              {isNew && <option value="">{t("configurazione.fields.scegliIlTipo")}</option>}
              {catalog.data_types.map((t) => (
                <option key={t} value={t}>{DATA_TYPE_LABELS[t] ?? t}</option>
              ))}
            </Select>
          </Labeled>
        </div>
        {catalog.multi_types.includes(d.data_type) && (
          <label className="flex items-center gap-2">
            <input type="checkbox" className="size-4 accent-[var(--accent)]" disabled={used} checked={d.multi}
              onChange={(e) => setD((x) => ({ ...x, multi: e.target.checked, defaultValue: null }))} />
            <span>{t("configurazione.fields.piuValori")}</span>
            {used && <span className="text-[12px] text-muted">{t("configurazione.fields.nonCambiaSuUn")}</span>}
          </label>
        )}
        <Labeled label={t("configurazione.fields.aiuto")} hint={t("configurazione.fields.siVedeSottoIl")}>
          <textarea className="field min-h-16" maxLength={1000} value={d.help} onChange={(e) => set("help", e.target.value)} />
        </Labeled>
        <Labeled label={t("configurazione.fields.alias")} hint={t("configurazione.fields.altriNomiPerLe")}>
          <input className="field" value={d.aliases} onChange={(e) => set("aliases", e.target.value)} />
        </Labeled>
        <Constraints d={d} catalog={catalog} onChange={(s) => set("settings", s)} />
        {choice && (
          <OptionsEditor
            options={options}
            onChange={setOptions}
            colors={catalog.colors}
            shared={field?.shared_with}
            sets={isNew ? catalog.option_sets.filter((s) => s.kind === d.data_type) : []}
            setId={d.option_set_id}
            onSet={(id) => set("option_set_id", id)}
          />
        )}
        <section className="flex flex-col gap-2">
          <SectionTitle>{t("configurazione.fields.valorePredefinito")}</SectionTitle>
          {canDefault ? (
            <>
              <FieldEditor f={preview} value={d.defaultValue} onChange={(v) => set("defaultValue", v)} baseCurrency={catalog.base_currency} />
              <span className="text-[12px] text-muted">{t("configurazione.fields.siProponeNeiForm")}</span>
            </>
          ) : (
            <p className="m-0 text-[12.5px] text-muted">
              {isNew ? t("configurazione.fields.ilPredefinitoDiUn") : t("configurazione.fields.salvaPrimaLeOpzioni")}
            </p>
          )}
        </section>
        <section className="flex flex-col gap-2">
          <SectionTitle>{t("configurazione.common.anteprima")}</SectionTitle>
          <div className="flex flex-col gap-1.5 border border-hair p-3">
            <span className="label">{preview.name}</span>
            <FieldEditor f={preview} value={d.defaultValue} onChange={() => undefined} baseCurrency={catalog.base_currency} />
            {preview.help && <span className="text-[12px] text-muted">{preview.help}</span>}
            <span className="text-[12px] text-muted"><Trans k="configurazione.fields.readingValue" slots={{ value: <FieldValue f={{ ...preview, labels: {} }} value={d.defaultValue} /> }} /></span>
          </div>
        </section>
        <ErrorNote error={save.error} />
        <div className="flex flex-wrap gap-2">
          <button className="btn btn-primary" disabled={save.isPending || !d.data_type}>{isNew ? t("configurazione.fields.creaIlCampo") : t("configurazione.common.salva")}</button>
          <button type="button" className="btn" onClick={onClose}>{t("configurazione.common.annulla")}</button>
        </div>
      </form>
      {!isNew && <Associations field={field} catalog={catalog} />}
      {!isNew && (
        <section className="flex flex-col gap-2">
          <SectionTitle>{t("configurazione.fields.archiviazione")}</SectionTitle>
          <p className="m-0 text-[12.5px] text-muted">
            {t("configurazione.fields.unCampoArchiviatoNon")}
            {used ? t("configurazione.fields.haGiaValoriNon") : t("configurazione.fields.senzaValoriSiPuo")}
          </p>
          <div className="flex flex-wrap gap-2">
            <button className="btn" disabled={archive.isPending} onClick={() => archive.mutate(!field.archived)}>
              {field.archived ? t("configurazione.fields.ripristina") : t("configurazione.common.archivia")}
            </button>
            {!used && !field.system && (
              <button
                className="btn btn-danger"
                disabled={remove.isPending}
                onClick={() => window.confirm(t("configurazione.fields.confirmDeleteField", { name: field.name })) && remove.mutate()}
              >
                {t("configurazione.common.elimina")}
              </button>
            )}
          </div>
          <ErrorNote error={archive.error ?? remove.error} />
        </section>
      )}
    </>
  );
}

function optionsChanged(field: FieldConfig, options: OptionInput[]): boolean {
  const a = (field.options ?? []).map((o) => `${o.id}|${o.name}|${o.color ?? ""}|${!!o.archived}|${o.labels?.en?.trim() ?? ""}`).join(";");
  const b = options.map((o) => `${o.id ?? ""}|${o.name}|${o.color ?? ""}|${!!o.archived}|${o.labels?.en?.trim() ?? ""}`).join(";");
  return a !== b;
}

function Labeled({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="flex min-w-0 flex-col gap-1.5">
      <span className="label">{label}</span>
      {children}
      {hint && <span className="text-[12px] text-muted">{hint}</span>}
    </label>
  );
}

/** Vincoli per tipo: tipi ammessi, gruppo dei membri, gruppi ammessi, valuta, limiti. */
function Constraints({ d, catalog, onChange }: { d: Draft; catalog: FieldCatalog; onChange: (s: FieldSettings) => void }) {
  const t = useT();
  const s = d.settings;
  const num = (v: string) => (v.trim() === "" ? undefined : Number(v.replace(",", ".")));
  const toggle = (list: string[] | undefined, v: string) => ((list ?? []).includes(v) ? (list ?? []).filter((x) => x !== v) : [...(list ?? []), v]);
  const numeric = ["integer", "decimal", "money", "duration"].includes(d.data_type);
  if (!numeric && !["element", "member", "group"].includes(d.data_type)) return null;
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle>{t("configurazione.fields.vincoli")}</SectionTitle>
      {d.data_type === "element" && (
        <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
          <legend className="label mb-1.5 p-0">{t("configurazione.fields.tipiDiElementoAmmessi")}</legend>
          <div className="flex flex-wrap gap-x-4 gap-y-1.5">
            {catalog.types.map((t) => (
              <label key={t.key} className="flex items-center gap-2">
                <input type="checkbox" className="size-4 accent-[var(--accent)]" checked={(s.types ?? []).includes(t.key)} onChange={() => onChange({ ...s, types: toggle(s.types, t.key) })} />
                {t.name}
              </label>
            ))}
          </div>
        </fieldset>
      )}
      {d.data_type === "member" && (
        <Labeled label={t("configurazione.fields.soloIMembriDel")} hint={t("configurazione.fields.vuotoQualunqueMembroAttivo")}>
          <Select className="field" value={s.group ?? ""} onChange={(e) => onChange({ ...s, group: e.target.value || undefined })}>
            <option value="">{t("configurazione.fields.qualunque")}</option>
            {catalog.groups.map((g) => <option key={g} value={g}>{g}</option>)}
          </Select>
        </Labeled>
      )}
      {d.data_type === "group" && (
        <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
          <legend className="label mb-1.5 p-0">{t("configurazione.fields.gruppiAmmessiNessunoTutti")}</legend>
          <div className="flex flex-wrap gap-x-4 gap-y-1.5">
            {catalog.groups.map((g) => (
              <label key={g} className="flex items-center gap-2">
                <input type="checkbox" className="size-4 accent-[var(--accent)]" checked={(s.groups ?? []).includes(g)} onChange={() => onChange({ ...s, groups: toggle(s.groups, g) })} />
                {g}
              </label>
            ))}
          </div>
        </fieldset>
      )}
      {d.data_type === "money" && (
        <Labeled label={t("configurazione.fields.valutaProposta")} hint={t("configurazione.fields.baseCurrencyHint", { currency: catalog.base_currency })}>
          <input className="field w-28 font-mono uppercase" maxLength={3} value={s.currency ?? ""} onChange={(e) => onChange({ ...s, currency: e.target.value.toUpperCase() || undefined })} />
        </Labeled>
      )}
      {numeric && (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          <Labeled label={d.data_type === t("configurazione.fields.duration") ? t("configurazione.fields.minimoMinuti") : t("configurazione.fields.minimo")}>
            <input className="field tabular" inputMode="decimal" value={s.min ?? ""} onChange={(e) => onChange({ ...s, min: num(e.target.value) })} />
          </Labeled>
          <Labeled label={d.data_type === t("configurazione.fields.duration") ? t("configurazione.fields.massimoMinuti") : t("configurazione.fields.massimo")}>
            <input className="field tabular" inputMode="decimal" value={s.max ?? ""} onChange={(e) => onChange({ ...s, max: num(e.target.value) })} />
          </Labeled>
          {d.data_type === "duration" && (
            <Labeled label={t("configurazione.fields.orePerGiornata")}>
              <input className="field tabular" inputMode="decimal" placeholder="8" value={s.hours_per_day ?? ""} onChange={(e) => onChange({ ...s, hours_per_day: num(e.target.value) })} />
            </Labeled>
          )}
        </div>
      )}
    </section>
  );
}

/** Editor delle opzioni: nome, colore (token del tema), ordine, archiviazione. */
function OptionsEditor({
  options,
  onChange,
  colors,
  shared,
  sets,
  setId,
  onSet,
}: {
  options: OptionInput[];
  onChange: (o: OptionInput[]) => void;
  colors: OptionColor[];
  shared?: string[];
  sets: { id: string; name: string; fields: number }[];
  setId: string;
  onSet: (id: string) => void;
}) {
  const t = useT();
  const last = useRef<HTMLInputElement>(null);
  const [focusNew, setFocusNew] = useState(false);
  useEffect(() => {
    if (focusNew) {
      last.current?.focus();
      setFocusNew(false);
    }
  }, [focusNew]);
  const upd = (i: number, p: Partial<OptionInput>) => onChange(options.map((o, j) => (j === i ? { ...o, ...p } : o)));
  const move = (i: number, d: number) => {
    const j = i + d;
    if (j < 0 || j >= options.length) return;
    const next = [...options];
    [next[i], next[j]] = [next[j], next[i]];
    onChange(next);
  };
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{options.filter((o) => !o.archived).length || ""}</span>}>{t("configurazione.fields.valori")}</SectionTitle>
      {sets.length > 0 && (
        <Labeled label={t("configurazione.fields.insiemeDiValori")} hint={t("configurazione.fields.siPuoCondividereL")}>
          <Select className="field" value={setId} onChange={(e) => onSet(e.target.value)}>
            <option value="">{t("configurazione.fields.nuovoInsiemeLeOpzioni")}</option>
            {sets.map((s) => <option key={s.id} value={s.id}>{s.name} ({t("configurazione.types.fieldCount", { count: s.fields })})</option>)}
          </Select>
        </Labeled>
      )}
      {shared && shared.length > 0 && (
        <p className="m-0 text-[12.5px] text-flag">{t("configurazione.fields.sharedSet", { names: shared.join(", ") })}</p>
      )}
      {!setId && (
        <>
          <ol className="m-0 flex list-none flex-col p-0">
            {options.map((o, i) => (
              <li key={o.id ?? `n${i}`} className="flex flex-wrap items-center gap-1.5 border-b border-hair py-1.5">
                <span className={`size-3 shrink-0 ${COLOR_DOT[o.color ?? ""] ?? "bg-hair"}`} aria-hidden="true" />
                <input
                  ref={i === options.length - 1 ? last : undefined}
                  className={`field min-w-0 flex-1 py-1 ${o.archived ? "text-muted line-through" : ""}`}
                  aria-label={t("configurazione.fields.optionName", { n: i + 1 })}
                  value={o.name}
                  maxLength={100}
                  onChange={(e) => upd(i, { name: e.target.value })}
                />
                <Select className="field w-auto py-1 text-[12.5px]" aria-label={t("configurazione.fields.optionColor", { name: o.name || t("configurazione.fields.option") })} value={o.color ?? ""} onChange={(e) => upd(i, { color: e.target.value as OptionColor })}>
                  {colors.map((c) => <option key={c} value={c}>{COLOR_LABELS[c] ?? c}</option>)}
                </Select>
                <span className="flex gap-1">
                  <button type="button" className="btn px-2 py-0.5" aria-label={t("configurazione.common.moveUpNamed", { name: o.name })} disabled={i === 0} onClick={() => move(i, -1)}>↑</button>
                  <button type="button" className="btn px-2 py-0.5" aria-label={t("configurazione.common.moveDownNamed", { name: o.name })} disabled={i === options.length - 1} onClick={() => move(i, 1)}>↓</button>
                  {o.id ? (
                    <button type="button" className="btn px-2 py-0.5 text-[12px]" onClick={() => upd(i, { archived: !o.archived })}>
                      {o.archived ? t("configurazione.fields.ripristina") : t("configurazione.common.archivia")}
                    </button>
                  ) : (
                    <button type="button" className="btn px-2 py-0.5" aria-label={t("configurazione.common.removeNamed", { name: o.name })} onClick={() => onChange(options.filter((_, j) => j !== i))}>×</button>
                  )}
                </span>
              </li>
            ))}
          </ol>
          <button
            type="button"
            className="btn self-start"
            onClick={() => {
              onChange([...options, { name: "", color: "" }]);
              setFocusNew(true);
            }}
          >
            {t("configurazione.fields.aggiungiUnOpzione")}
          </button>
          <p className="m-0 text-[12px] text-muted">
            {t("configurazione.fields.archiviareUnOpzioneLa")}
          </p>
          {options.some((o) => o.name.trim()) && (
            <details className="group border border-hair px-3 py-2">
              <summary className="flex cursor-pointer select-none items-baseline gap-2 marker:content-none">
                <span aria-hidden className="inline-block w-3 text-[11px] text-muted transition-transform group-open:rotate-90">▸</span>
                <span className="label">{t("configurazione.etichette.titolo")}</span>
                <span className="min-w-0 flex-1 truncate text-right text-[12px] text-muted group-open:hidden">
                  {t("configurazione.etichette.riassunto", { n: options.filter((o) => o.labels?.en?.trim()).length, total: options.filter((o) => o.name.trim()).length })}
                </span>
              </summary>
              <div className="mt-2 flex flex-col gap-2 pl-5">
                <p className="m-0 text-[12.5px] text-muted">{t("configurazione.etichette.opzioniGuida")}</p>
                {options.map((o, i) =>
                  o.name.trim() ? (
                    <label key={o.id ?? `t${i}`} className="grid grid-cols-1 items-center gap-1 sm:grid-cols-2 sm:gap-3">
                      <span className={`truncate text-[13px] ${o.archived ? "text-muted line-through" : ""}`}>{o.name}</span>
                      <input
                        className="field py-1"
                        aria-label={t("configurazione.etichette.opzioneInglese", { name: o.name })}
                        maxLength={100}
                        value={o.labels?.en ?? ""}
                        onChange={(e) => upd(i, { labels: { ...o.labels, en: e.target.value } })}
                      />
                    </label>
                  ) : null,
                )}
              </div>
            </details>
          )}
        </>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Associazioni ai tipi di elemento

function Associations({ field, catalog }: { field: FieldConfig; catalog: FieldCatalog }) {
  const t = useT();
  if (field.archived) {
    return (
      <section className="flex flex-col gap-2">
        <SectionTitle>{t("configurazione.common.tipiDiElemento")}</SectionTitle>
        <p className="m-0 text-[12.5px] text-muted">{t("configurazione.fields.ilCampoEArchiviato")}</p>
      </section>
    );
  }
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle>{t("configurazione.common.tipiDiElemento")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">
        {t("configurazione.fields.doveCompareIlCampo")}
      </p>
      {catalog.types.map((t) => (
        <AssocRow key={t.key} field={field} type={t} />
      ))}
    </section>
  );
}

function AssocRow({ field, type }: { field: FieldConfig; type: FieldCatalog["types"][number] }) {
  const t = useT();
  const qc = useQueryClient();
  const a = field.types.find((x) => x.type === type.key);
  const [open, setOpen] = useState(false);
  const [v, setV] = useState<AssocInput>({ required: a?.required ?? false, required_from: a?.required_from ?? "", visible_when: a?.visible_when ?? "", section: a?.section ?? "" });
  const done = () => {
    qc.invalidateQueries({ queryKey: ["fields-admin"] });
    qc.invalidateQueries({ queryKey: ["type-fields"] });
    qc.invalidateQueries({ queryKey: ["element"] });
    setOpen(false);
  };
  const save = useMutation({ mutationFn: () => fieldsApi.associate(field.key, type.key, v), onSuccess: done });
  const drop = useMutation({ mutationFn: () => fieldsApi.dissociate(field.key, type.key), onSuccess: done });
  const phases = mainPath(type.phases);
  const sections = useSections(type.key);
  return (
    <div className="flex flex-col gap-2 border border-hair p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-semibold">{type.name}</span>
        <span className="font-mono text-[11.5px] text-muted">{type.key}</span>
        <span className="ml-auto flex gap-2">
          {a ? (
            <>
              <button className="btn py-1 text-[12.5px]" onClick={() => setOpen((o) => !o)}>{open ? t("configurazione.common.chiudi") : t("configurazione.common.regole")}</button>
              <button className="btn py-1 text-[12.5px]" disabled={drop.isPending}
                onClick={() => window.confirm(t("configurazione.fields.confirmDropField", { field: field.name, type: type.name })) && drop.mutate()}>
                {t("configurazione.common.togli")}
              </button>
            </>
          ) : (
            <button className="btn py-1 text-[12.5px]" onClick={() => setOpen((o) => !o)}>{open ? t("configurazione.common.chiudi") : t("configurazione.fields.associa")}</button>
          )}
        </span>
      </div>
      {a && !open && (
        <p className="m-0 text-[12.5px] text-muted">
          {a.required ? t("configurazione.fields.obbligatorio2") : a.required_from ? t("configurazione.fields.requiredFrom", { stage: a.required_from }) : t("configurazione.fields.facoltativo")}
          {a.section ? t("configurazione.fields.sectionSuffix", { section: a.section }) : ""}
          {a.visible_when ? t("configurazione.fields.visibleIfSuffix", { condition: a.visible_when }) : ""}
          {a.problem && <span className="block text-flag">{a.problem}</span>}
        </p>
      )}
      {open && (
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <label className="flex items-center gap-2">
            <input type="checkbox" className="size-4 accent-[var(--accent)]" checked={v.required} onChange={(e) => setV({ ...v, required: e.target.checked })} />
            <span>{t("configurazione.fields.sempreObbligatorioAncheAlla")}</span>
          </label>
          {phases.length > 0 && (
            <Labeled label={t("configurazione.fields.obbligatorioDallaFase")} hint={t("configurazione.fields.daQuestaFaseIn")}>
              <Select className="field" value={v.required_from} disabled={v.required} onChange={(e) => setV({ ...v, required_from: e.target.value })}>
                <option value="">{t("configurazione.fields.nessuna")}</option>
                {phases.map((p) => <option key={p.id} value={p.name}>{p.name}</option>)}
              </Select>
            </Labeled>
          )}
          <ConditionBuilder
            label={t("configurazione.fields.visibileSe")}
            value={v.visible_when}
            onChange={(c) => setV({ ...v, visible_when: c })}
            type={type.key}
            empty={t("casa.fields.alwaysVisible")}
          />
          <Labeled label={t("configurazione.fields.sezione")} hint={t("configurazione.fields.raggruppaICampiNel")}>
            <input className="field" list={`sezioni-${type.key}`} maxLength={60} value={v.section} onChange={(e) => setV({ ...v, section: e.target.value })} />
            <datalist id={`sezioni-${type.key}`}>
              {sections.map((s) => <option key={s} value={s} />)}
            </datalist>
          </Labeled>
          <ErrorNote error={save.error} />
          <div className="flex gap-2">
            <button className="btn btn-primary" disabled={save.isPending}>{a ? t("configurazione.common.salvaLeRegole") : t("configurazione.fields.associa")}</button>
            <button type="button" className="btn" onClick={() => setOpen(false)}>{t("configurazione.common.annulla")}</button>
          </div>
        </form>
      )}
      <ErrorNote error={drop.error} />
    </div>
  );
}

/** Sezioni già usate dai campi di un tipo (per proporle). */
function useSections(type: string): string[] {
  const list = useQuery({ queryKey: ["fields-admin"], queryFn: fieldsApi.list });
  return useMemo(() => {
    const s = new Set<string>();
    for (const f of list.data?.items ?? []) for (const a of f.types) if (a.type === type && a.section) s.add(a.section);
    return [...s].sort();
  }, [list.data, type]);
}

// ---------------------------------------------------------------------
// Import

function ImportButton() {
  const t = useT();
  const qc = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [bundle, setBundle] = useState<unknown>(null);
  const [report, setReport] = useState<ImportReport | null>(null);
  const [error, setError] = useState<unknown>(null);
  const dry = useMutation({
    mutationFn: (b: unknown) => fieldsApi.importBundle(b, true),
    onSuccess: (r) => setReport(r.report),
    onError: setError,
  });
  const run = useMutation({
    mutationFn: () => fieldsApi.importBundle(bundle, false),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["fields-admin"] });
      qc.invalidateQueries({ queryKey: ["fields-catalog"] });
      setBundle(null);
      setReport(null);
    },
    onError: setError,
  });
  const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;
    setError(null);
    setReport(null);
    try {
      const b = JSON.parse(await file.text());
      setBundle(b);
      dry.mutate(b);
    } catch {
      setError(new ApiError(400, "json", t("configurazione.common.ilFileNonE")));
    }
  };
  return (
    <>
      <button className="btn" onClick={() => input.current?.click()}>{t("configurazione.common.importa")}</button>
      <input ref={input} type="file" accept="application/json,.json" className="hidden" onChange={onFile} />
      {(report || error) && (
        <div role="dialog" aria-label={t("configurazione.fields.importDeiCampi")} className="fixed inset-x-0 top-16 z-50 mx-auto flex w-[min(560px,calc(100%-2rem))] flex-col gap-3 border border-rule bg-bg p-4">
          <SectionTitle>{t("configurazione.fields.importDeiCampi")}</SectionTitle>
          <ErrorNote error={error} />
          {report && (
            <>
              <p className="m-0">
                {t("configurazione.fields.importDryRun", { created: report.created.length, updated: report.updated.length, associations: report.associations })}
              </p>
              {report.created.length > 0 && <p className="m-0 text-[12.5px] text-muted">{t("configurazione.fields.newList", { list: report.created.join(", ") })}</p>}
              {report.warnings.length > 0 && (
                <ul className="m-0 pl-4 text-[12.5px] text-flag">
                  {report.warnings.map((w) => <li key={w}>{w}</li>)}
                </ul>
              )}
              <p className="m-0 text-[12px] text-muted">{t("configurazione.fields.lImportNonElimina")}</p>
            </>
          )}
          <div className="flex gap-2">
            {report && (
              <button className="btn btn-primary" disabled={run.isPending} onClick={() => run.mutate()}>
                {run.isPending ? t("configurazione.fields.import") : t("configurazione.fields.importaDavvero")}
              </button>
            )}
            <button
              className="btn"
              onClick={() => {
                setReport(null);
                setError(null);
                setBundle(null);
              }}
            >
              {t("configurazione.common.chiudi")}
            </button>
          </div>
        </div>
      )}
    </>
  );
}

