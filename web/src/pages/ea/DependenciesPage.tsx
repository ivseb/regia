// Dipendenze tra applicazioni e tipi di relazione configurabili (replica/architettura-ea.md
// §3): elenco con attributi (protocollo, criticità, dati scambiati e classificazione),
// creazione con i soli tipi ammessi, configurazione dei tipi, import ed export.
import { Select } from "../../select";
import { useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMe } from "../../shell";
import { CRITICALITY, ErrorNote, SectionTitle } from "../../ui";
import { canWriteEA, eaApi, type AttrField, type GraphNode, type Relation, type RelationType } from "../../eaApi";
import { ImportBlock } from "./ImportBlock";
import { EaPage } from "./tabs";
import { useT } from "../../i18n/react";
import { tr } from "../../i18n/core";

// Etichette lette al momento dell'uso, nella lingua corrente.
const DIRECTIONS = {
  get forward() {
    return tr("altro.ea.deps.dirForward");
  },
  get reverse() {
    return tr("altro.ea.deps.dirReverse");
  },
  get none() {
    return tr("altro.ea.deps.dirNone");
  },
} as Record<string, string>;
const CATEGORIES = {
  get dependency() {
    return tr("altro.ea.deps.catDependency");
  },
  get capability() {
    return tr("altro.ea.deps.catCapability");
  },
  get composition() {
    return tr("altro.ea.deps.catComposition");
  },
} as Record<string, string>;

export function attrLabel(f: AttrField, v: unknown): string {
  if (v === undefined || v === null || v === "") return "";
  if (f.key === "criticality" && typeof v === "string") return CRITICALITY[v] ?? v;
  if (typeof v === "boolean") return v ? tr("altro.ea.deps.yes") : tr("altro.ea.deps.no");
  return String(v);
}

export function DependenciesPage() {
  const t = useT();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canWrite = canWriteEA(perms);
  const rels = useQuery({ queryKey: ["ea", "relations", "dependency"], queryFn: () => eaApi.relations("dependency") });
  const types = useQuery({ queryKey: ["ea", "relation-types"], queryFn: eaApi.relationTypes });
  const [filter, setFilter] = useState("");
  const [adding, setAdding] = useState(false);
  const depTypes = (types.data?.items ?? []).filter((x) => x.category === "dependency");
  const schemaOf = useMemo(() => new Map((types.data?.items ?? []).map((x) => [x.key, x.attrs_schema?.fields ?? []])), [types.data]);
  const shown = (rels.data?.items ?? []).filter((r) => {
    const f = filter.trim().toLowerCase();
    return !f || `${r.source_name} ${r.source_code ?? ""} ${r.type_name} ${r.target_name} ${r.target_code ?? ""} ${Object.values(r.attrs).join(" ")}`.toLowerCase().includes(f);
  });
  return (
    <EaPage active="dipendenze" title={t("altro.ea.deps.title")}>
      <p className="m-0 max-w-3xl text-muted">{t("altro.ea.deps.intro")}</p>
      {canWrite && adding && <NewDependency types={depTypes} onDone={() => setAdding(false)} />}
      <div className="flex flex-wrap items-end gap-3">
        {canWrite && !adding && (
          <button className="btn btn-primary" onClick={() => setAdding(true)}>
            {t("altro.ea.deps.new")}
          </button>
        )}
        <label className="flex min-w-0 flex-[1_1_280px] flex-col gap-1">
          <span className="label">{t("altro.ea.deps.filter")}</span>
          <input className="field" value={filter} onChange={(e) => setFilter(e.target.value)} placeholder={t("altro.ea.deps.filterPlaceholder")} />
        </label>
        <span className="pb-2 font-mono text-[12px] text-muted">
          {t("altro.ea.deps.shownOf", { shown: shown.length, total: rels.data?.items.length ?? 0 })}
        </span>
      </div>
      <ErrorNote error={rels.error ?? types.error} />
      <div className="overflow-x-auto">
        <table className="w-full min-w-[820px] border-collapse">
          <thead>
            <tr className="label h-9 text-left">
              <th scope="col" className="border-b-2 border-rule pr-3">{t("altro.ea.deps.source")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.deps.relation")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.deps.target")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.deps.attributes")}</th>
              <th scope="col" className="border-b-2 border-rule pl-3" />
            </tr>
          </thead>
          <tbody>
            {shown.map((r) => (
              <DependencyRow key={r.id} rel={r} fields={schemaOf.get(r.type) ?? []} canWrite={canWrite} />
            ))}
            {rels.data && shown.length === 0 && (
              <tr>
                <td colSpan={5} className="py-8 text-muted">
                  {filter ? t("altro.ea.deps.noneFiltered") : t("altro.ea.deps.noneEmpty")}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <ImportBlock kind="dependencies" canWrite={canWrite} />
      <RelationTypes types={types.data?.items ?? []} canWrite={canWrite} />
    </EaPage>
  );
}

function NodeLink({ id, keyStr, name, code }: { id: string; keyStr: string; name: string; code: string | null }) {
  return (
    <Link to="/e/$key" params={{ key: keyStr || id }} className="font-medium text-fg no-underline hover:underline">
      {name}
      {code && <span className="ml-1.5 font-mono text-[11px] text-muted">{code}</span>}
    </Link>
  );
}

function DependencyRow({ rel, fields, canWrite }: { rel: Relation; fields: AttrField[]; canWrite: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [attrs, setAttrs] = useState<Record<string, unknown>>(rel.attrs);
  const done = () => qc.invalidateQueries({ queryKey: ["ea"] });
  const save = useMutation({
    mutationFn: () => eaApi.updateRelation(rel.id, attrs),
    onSuccess: () => {
      setEditing(false);
      done();
    },
  });
  const remove = useMutation({ mutationFn: () => eaApi.deleteRelation(rel.id), onSuccess: done });
  return (
    <>
      <tr className="border-b border-hair align-top">
        <td className="py-2 pr-3">
          <NodeLink id={rel.source_id} keyStr={rel.source_key} name={rel.source_name} code={rel.source_code} />
        </td>
        <td className="px-3 py-2 text-[12.5px] whitespace-nowrap text-muted">{rel.type_name}</td>
        <td className="px-3 py-2">
          <NodeLink id={rel.target_id} keyStr={rel.target_key} name={rel.target_name} code={rel.target_code} />
        </td>
        <td className="px-3 py-2 text-[12.5px]">
          {fields
            .map((f) => [f, attrLabel(f, rel.attrs[f.key])] as const)
            .filter(([, v]) => v)
            .map(([f, v]) => (
              <span key={f.key} className="mr-3 inline-block">
                <span className="text-muted">{f.name}:</span> {v}
              </span>
            ))}
        </td>
        <td className="py-2 pl-3 text-right whitespace-nowrap">
          {canWrite && (
            <span className="flex justify-end gap-3 text-[12px]">
              {fields.length > 0 && (
                <button className="text-accent underline" onClick={() => setEditing(!editing)}>
                  {editing ? t("altro.ea.deps.close") : t("altro.ea.deps.edit")}
                </button>
              )}
              <button className="text-muted underline hover:text-danger" disabled={remove.isPending} onClick={() => remove.mutate()}>
                {t("altro.ea.deps.delete")}
              </button>
            </span>
          )}
        </td>
      </tr>
      {editing && (
        <tr className="border-b border-hair">
          <td colSpan={5} className="py-2">
            <form
              className="flex flex-wrap items-end gap-3"
              onSubmit={(e) => {
                e.preventDefault();
                save.mutate();
              }}
            >
              <AttrInputs fields={fields} value={attrs} onChange={setAttrs} idPrefix={rel.id} />
              <button className="btn btn-primary" disabled={save.isPending}>
                {t("altro.ea.deps.save")}
              </button>
              <div className="basis-full">
                <ErrorNote error={save.error} />
              </div>
            </form>
          </td>
        </tr>
      )}
      {remove.error && (
        <tr>
          <td colSpan={5}>
            <ErrorNote error={remove.error} />
          </td>
        </tr>
      )}
    </>
  );
}

export function AttrInputs({ fields, value, onChange, idPrefix }: { fields: AttrField[]; value: Record<string, unknown>; onChange: (v: Record<string, unknown>) => void; idPrefix: string }) {
  const t = useT();
  return (
    <>
      {fields.map((f) => {
        const v = value[f.key];
        const set = (x: unknown) => onChange({ ...value, [f.key]: x === "" ? undefined : x });
        return (
          <label key={f.key} className="flex w-44 flex-col gap-1">
            <span className="label">{f.name}</span>
            {f.kind === "enum" ? (
              <Select className="field" value={String(v ?? "")} onChange={(e) => set(e.target.value)}>
                <option value="">—</option>
                {f.values?.map((o) => (
                  <option key={o} value={o}>
                    {f.key === "criticality" ? (CRITICALITY[o] ?? o) : o}
                  </option>
                ))}
              </Select>
            ) : f.kind === "bool" ? (
              <Select className="field" value={v === undefined ? "" : v ? "yes" : "no"} onChange={(e) => set(e.target.value === "" ? "" : e.target.value === "yes")}>
                <option value="">—</option>
                <option value="yes">{t("altro.ea.deps.yes")}</option>
                <option value="no">{t("altro.ea.deps.no")}</option>
              </Select>
            ) : (
              <>
                <input
                  className="field"
                  inputMode={f.kind === "number" ? "decimal" : undefined}
                  list={f.values?.length ? `${idPrefix}-${f.key}` : undefined}
                  value={String(v ?? "")}
                  onChange={(e) => set(e.target.value)}
                />
                {f.values?.length ? (
                  <datalist id={`${idPrefix}-${f.key}`}>
                    {f.values.map((o) => (
                      <option key={o} value={o} />
                    ))}
                  </datalist>
                ) : null}
              </>
            )}
          </label>
        );
      })}
    </>
  );
}

/** Nodi del grafo ammessi da un tipo (null = tutti). */
function allowed(nodes: GraphNode[], keys: string[] | null): GraphNode[] {
  return keys ? nodes.filter((n) => keys.includes(n.type)) : nodes;
}

export function NewDependency({ types, from, onDone }: { types: RelationType[]; from?: string; onDone?: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const graph = useQuery({ queryKey: ["ea", "graph"], queryFn: eaApi.graph, staleTime: 30_000 });
  const active = types.filter((t) => !t.archived);
  const [type, setType] = useState("");
  const [source, setSource] = useState(from ?? "");
  const [target, setTarget] = useState("");
  const [attrs, setAttrs] = useState<Record<string, unknown>>({});
  const rt = active.find((x) => x.key === type) ?? active[0];
  const nodes = (graph.data?.nodes ?? []).filter((n) => n.state_category !== "done");
  const create = useMutation({
    mutationFn: () => eaApi.createRelation(rt!.key, source, target, attrs),
    onSuccess: () => {
      setTarget("");
      setAttrs({});
      qc.invalidateQueries({ queryKey: ["ea"] });
      onDone?.();
    },
  });
  if (!rt) return null;
  const opts = (list: GraphNode[]) =>
    list.map((n) => (
      <option key={n.id} value={n.id}>
        {n.name}
        {n.code ? ` (${n.code})` : ""} · {n.type_name}
      </option>
    ));
  return (
    <form
      className="flex flex-col gap-3 border-l-2 border-accent pl-4"
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <SectionTitle>{t("altro.ea.deps.new")}</SectionTitle>
      <div className="flex flex-wrap items-end gap-3">
        {!from && (
          <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
            <span className="label">{t("altro.ea.deps.source")}</span>
            <Select className="field" required value={source} onChange={(e) => setSource(e.target.value)}>
              <option value="">{t("altro.ea.deps.choose")}</option>
              {opts(allowed(nodes, rt.source_types))}
            </Select>
          </label>
        )}
        <label className="flex w-52 flex-col gap-1">
          <span className="label">{t("altro.ea.deps.relation")}</span>
          <Select
            className="field"
            value={rt.key}
            onChange={(e) => {
              setType(e.target.value);
              setAttrs({});
            }}
          >
            {active.map((x) => (
              <option key={x.key} value={x.key}>
                {x.outward_name}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
          <span className="label">{t("altro.ea.deps.target")}</span>
          <Select className="field" required value={target} onChange={(e) => setTarget(e.target.value)}>
            <option value="">{t("altro.ea.deps.choose")}</option>
            {opts(allowed(nodes, rt.target_types).filter((n) => n.id !== source))}
          </Select>
        </label>
      </div>
      {rt.attrs_schema && rt.attrs_schema.fields.length > 0 && (
        <div className="flex flex-wrap items-end gap-3">
          <AttrInputs fields={rt.attrs_schema.fields} value={attrs} onChange={setAttrs} idPrefix={`nuova-${rt.key}`} />
        </div>
      )}
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={create.isPending || !source || !target}>
          {t("altro.ea.deps.add")}
        </button>
      </div>
      <ErrorNote error={create.error} />
    </form>
  );
}

function RelationTypes({ types, canWrite }: { types: RelationType[]; canWrite: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const graph = useQuery({ queryKey: ["ea", "graph"], queryFn: eaApi.graph, staleTime: 30_000 });
  const nodeTypes = graph.data?.node_types ?? [];
  const typeName = (k: string) => nodeTypes.find((x) => x.key === k)?.name ?? k;
  const [open, setOpen] = useState(false);
  const [f, setF] = useState({ key: "", outward_name: "", inward_name: "", impact_direction: "forward", source: "application", target: "application", attrs: true });
  const create = useMutation({
    mutationFn: () => {
      const std = types.find((x) => x.key === "calls_api")?.attrs_schema ?? undefined;
      return eaApi.createRelationType({
        key: f.key,
        outward_name: f.outward_name,
        inward_name: f.inward_name,
        category: "dependency",
        impact_direction: f.impact_direction,
        source_types: f.source ? [f.source] : [],
        target_types: f.target ? [f.target] : [],
        attrs_schema: f.attrs ? std : undefined,
      });
    },
    onSuccess: () => {
      setOpen(false);
      qc.invalidateQueries({ queryKey: ["ea"] });
    },
  });
  const archive = useMutation({
    mutationFn: (x: RelationType) => eaApi.updateRelationType(x.key, { archived: !x.archived }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["ea"] }),
  });
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle
        aside={
          canWrite &&
          !open && (
            <button className="text-[12px] text-accent underline" onClick={() => setOpen(true)}>
              {t("altro.ea.deps.newType")}
            </button>
          )
        }
      >
        {t("altro.ea.deps.types")}
      </SectionTitle>
      {open && (
        <form
          className="flex flex-wrap items-end gap-3 border-l-2 border-accent pl-4"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
        >
          <label className="flex w-40 flex-col gap-1">
            <span className="label">{t("altro.ea.deps.key")}</span>
            <input className="field font-mono" required pattern="[a-z][a-z0-9_]*" value={f.key} onChange={(e) => setF({ ...f, key: e.target.value })} placeholder={t("altro.ea.deps.keyPlaceholder")} />
          </label>
          <label className="flex w-48 flex-col gap-1">
            <span className="label">{t("altro.ea.deps.outwardName")}</span>
            <input className="field" required value={f.outward_name} onChange={(e) => setF({ ...f, outward_name: e.target.value })} placeholder={t("altro.ea.deps.outwardPlaceholder")} />
          </label>
          <label className="flex w-48 flex-col gap-1">
            <span className="label">{t("altro.ea.deps.inwardName")}</span>
            <input className="field" required value={f.inward_name} onChange={(e) => setF({ ...f, inward_name: e.target.value })} placeholder={t("altro.ea.deps.inwardPlaceholder")} />
          </label>
          <label className="flex w-64 flex-col gap-1">
            <span className="label">{t("altro.ea.deps.whoDepends")}</span>
            <Select className="field" value={f.impact_direction} onChange={(e) => setF({ ...f, impact_direction: e.target.value })}>
              {Object.entries(DIRECTIONS).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </Select>
          </label>
          {(["source", "target"] as const).map((end) => (
            <label key={end} className="flex w-44 flex-col gap-1">
              <span className="label">{end === "source" ? t("altro.ea.deps.allowedSource") : t("altro.ea.deps.allowedTarget")}</span>
              <Select className="field" value={f[end]} onChange={(e) => setF({ ...f, [end]: e.target.value })}>
                <option value="">{t("altro.ea.deps.anyType")}</option>
                {nodeTypes.map((nt) => (
                  <option key={nt.key} value={nt.key}>
                    {nt.name}
                  </option>
                ))}
              </Select>
            </label>
          ))}
          <label className="flex items-center gap-2 pb-2">
            <input type="checkbox" checked={f.attrs} onChange={(e) => setF({ ...f, attrs: e.target.checked })} />
            {t("altro.ea.deps.standardAttrs")}
          </label>
          <button className="btn btn-primary" disabled={create.isPending}>
            {t("altro.ea.deps.create")}
          </button>
          <button type="button" className="btn" onClick={() => setOpen(false)}>
            {t("altro.ea.deps.cancel")}
          </button>
          <div className="basis-full">
            <ErrorNote error={create.error} />
          </div>
        </form>
      )}
      <div className="overflow-x-auto">
        <table className="w-full min-w-[760px] border-collapse text-[12.5px]">
          <thead>
            <tr className="label h-9 text-left">
              <th scope="col" className="border-b-2 border-rule pr-3">{t("altro.ea.deps.relation")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.deps.category")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.deps.allowedTypes")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.deps.impact")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{t("altro.ea.deps.attributes")}</th>
              <th scope="col" className="border-b-2 border-rule px-3 text-right">{t("altro.ea.deps.uses")}</th>
              <th scope="col" className="border-b-2 border-rule pl-3" />
            </tr>
          </thead>
          <tbody>
            {types.map((rt) => (
              <tr key={rt.key} className={`border-b border-hair ${rt.archived ? "text-muted" : ""}`}>
                <td className="py-1.5 pr-3">
                  <span className="font-medium">{rt.outward_name}</span> <span className="text-muted">/ {rt.inward_name}</span>
                  <span className="ml-2 font-mono text-[11px] text-muted">{rt.key}</span>
                </td>
                <td className="px-3 py-1.5">{CATEGORIES[rt.category] ?? rt.category}</td>
                <td className="px-3 py-1.5">
                  {(rt.source_types ?? []).map(typeName).join(", ") || t("altro.ea.deps.any")} → {(rt.target_types ?? []).map(typeName).join(", ") || t("altro.ea.deps.any")}
                </td>
                <td className="px-3 py-1.5">{DIRECTIONS[rt.impact_direction]}</td>
                <td className="px-3 py-1.5">{rt.attrs_schema?.fields.map((x) => x.name).join(", ") || "—"}</td>
                <td className="px-3 py-1.5 text-right font-mono">{rt.relations}</td>
                <td className="py-1.5 pl-3 text-right">
                  {canWrite && (
                    <button className="text-[12px] text-muted underline hover:text-fg" disabled={archive.isPending} onClick={() => archive.mutate(rt)}>
                      {rt.archived ? t("altro.ea.deps.reactivate") : t("altro.ea.deps.archive")}
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <ErrorNote error={archive.error} />
    </section>
  );
}
