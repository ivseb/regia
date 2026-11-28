// Mappa delle capability di business (replica/architettura-ea.md §2): livelli annidati,
// colore per copertura applicativa o per criticità, collegamenti con applicazioni e asset.
import { Select } from "../../select";
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api";
import { useMe } from "../../shell";
import { CatalogId } from "../../catalogId";
import { CRITICALITY, Criticality, ErrorNote, SectionTitle } from "../../ui";
import { TIME, canWriteEA, eaApi, type Capability } from "../../eaApi";
import { ImportBlock } from "./ImportBlock";
import { EaPage } from "./tabs";
import { ContinuityBlock } from "../continuita/ContinuityBlock";
import { useT } from "../../i18n/react";
import { tr } from "../../i18n/core";

type Mode = "copertura" | "criticita";

type Coverage = { direct: number; total: number; status: "scoperta" | "coperta" | "duplicata" };

const STATUS: Record<Coverage["status"], { border: string; text: string; label: string }> = {
  scoperta: {
    border: "border-danger",
    text: "text-danger",
    get label() {
      return tr("altro.ea.cap.statusNone");
    },
  },
  coperta: {
    border: "border-ok",
    text: "text-ok",
    get label() {
      return tr("altro.ea.cap.statusCovered");
    },
  },
  duplicata: {
    border: "border-flag",
    text: "text-flag",
    get label() {
      return tr("altro.ea.cap.statusMany");
    },
  },
};
const CRIT_BORDER: Record<string, string> = { critical: "border-danger", high: "border-flag", medium: "border-accent", low: "border-muted" };

export function CapabilityPage() {
  const t = useT();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canWrite = canWriteEA(perms);
  const caps = useQuery({ queryKey: ["ea", "capabilities"], queryFn: eaApi.capabilities });
  const settings = useQuery({ queryKey: ["ea", "settings"], queryFn: eaApi.settings, staleTime: 60_000 });
  const [mode, setMode] = useState<Mode>("copertura");
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState<string | null>(null); // "" = radice, id = figlia
  const list = useMemo(() => caps.data?.items ?? [], [caps.data]);
  const children = useMemo(() => {
    const m = new Map<string, Capability[]>();
    for (const c of list) {
      const k = c.parent_id ?? "";
      (m.get(k) ?? m.set(k, []).get(k)!).push(c);
    }
    return m;
  }, [list]);
  const dupMin = settings.data?.duplicate_min ?? 2;
  const coverage = useMemo(() => {
    const out = new Map<string, Coverage>();
    const walk = (c: Capability): Set<string> => {
      const apps = new Set(c.applications.filter((a) => !a.retired).map((a) => a.id));
      const direct = apps.size;
      for (const k of children.get(c.id) ?? []) for (const a of walk(k)) apps.add(a);
      out.set(c.id, { direct, total: apps.size, status: apps.size === 0 ? "scoperta" : direct >= dupMin ? "duplicata" : "coperta" });
      return apps;
    };
    for (const r of children.get("") ?? []) walk(r);
    return out;
  }, [children, dupMin]);
  const sel = list.find((c) => c.id === selected);
  const counts = useMemo(() => {
    const c = { scoperta: 0, coperta: 0, duplicata: 0 };
    for (const v of coverage.values()) c[v.status]++;
    return c;
  }, [coverage]);

  return (
    <EaPage active="capability" title={t("altro.ea.cap.title")}>
      <p className="m-0 max-w-3xl text-muted">{t("altro.ea.cap.intro")}</p>
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex w-48 flex-col gap-1">
          <span className="label">{t("altro.ea.cap.color")}</span>
          <Select className="field" value={mode} onChange={(e) => setMode(e.target.value as Mode)}>
            <option value="copertura">{t("altro.ea.cap.byCoverage")}</option>
            <option value="criticita">{t("altro.ea.cap.byCriticality")}</option>
          </Select>
        </label>
        <p className="m-0 flex flex-wrap gap-x-4 gap-y-1 pb-2 text-[12.5px]">
          {mode === "copertura" ? (
            (Object.keys(STATUS) as Coverage["status"][]).map((s) => (
              <span key={s} className="flex items-center gap-1.5">
                <span className={`inline-block h-3 w-1 border-l-4 ${STATUS[s].border}`} aria-hidden="true" />
                {STATUS[s].label} <span className="font-mono text-muted">{counts[s]}</span>
              </span>
            ))
          ) : (
            Object.entries(CRITICALITY).map(([k, v]) => (
              <span key={k} className="flex items-center gap-1.5">
                <span className={`inline-block h-3 w-1 border-l-4 ${CRIT_BORDER[k]}`} aria-hidden="true" />
                {v}
              </span>
            ))
          )}
        </p>
        {canWrite && (
          <button className="btn ml-auto" onClick={() => setCreating("")}>
            {t("altro.ea.cap.new")}
          </button>
        )}
      </div>
      {creating !== null && <NewCapability parent={creating} list={list} onClose={() => setCreating(null)} onCreated={(id) => setSelected(id)} />}
      <ErrorNote error={caps.error} />
      {caps.data && list.length === 0 && (
        <p className="text-muted">{t("altro.ea.cap.empty")}</p>
      )}
      <div className="flex min-w-0 flex-col gap-6 lg:flex-row lg:items-start">
        <div className="grid min-w-0 flex-1 grid-cols-[repeat(auto-fill,minmax(min(100%,280px),1fr))] gap-4">
          {(children.get("") ?? []).map((c) => (
            <CapBox key={c.id} cap={c} childrenOf={children} coverage={coverage} mode={mode} selected={selected} onSelect={setSelected} />
          ))}
        </div>
        {sel && (
          <CapDetail
            key={sel.id}
            cap={sel}
            list={list}
            coverage={coverage.get(sel.id)}
            canWrite={canWrite}
            onClose={() => setSelected(null)}
            onAddChild={() => setCreating(sel.id)}
          />
        )}
      </div>
      <ImportBlock kind="capabilities" canWrite={canWrite} />
    </EaPage>
  );
}

function CapBox({
  cap,
  childrenOf,
  coverage,
  mode,
  selected,
  onSelect,
}: {
  cap: Capability;
  childrenOf: Map<string, Capability[]>;
  coverage: Map<string, Coverage>;
  mode: Mode;
  selected: string | null;
  onSelect: (id: string) => void;
}) {
  const t = useT();
  const kids = childrenOf.get(cap.id) ?? [];
  const cov = coverage.get(cap.id)!;
  const border = mode === "copertura" ? STATUS[cov.status].border : cap.criticality ? CRIT_BORDER[cap.criticality] : "border-hair";
  const on = selected === cap.id;
  const retired = cap.state_category === "done";
  const label = (
    <button
      className={`flex w-full min-w-0 items-baseline gap-2 text-left ${on ? "text-accent" : "text-fg hover:text-accent"}`}
      aria-pressed={on}
      onClick={(e) => {
        e.stopPropagation();
        onSelect(cap.id);
      }}
    >
      <span className={`min-w-0 flex-1 ${cap.level === 1 ? "text-[15px] font-bold" : "font-medium"} ${retired ? "line-through" : ""}`}>{cap.name}</span>
      <span className="shrink-0 font-mono text-[11px] text-muted" title={t("altro.ea.cap.counts", { count: cov.direct, total: cov.total })}>
        {cov.total}
      </span>
    </button>
  );
  if (cap.level === 1)
    return (
      <section className={`flex min-w-0 flex-col gap-2 border-l-4 ${border} ${on ? "bg-sel" : ""} border-b border-b-hair py-2 pr-2 pl-3`}>
        {label}
        {cap.code && <span className="-mt-1.5 font-mono text-[11px] text-muted">{cap.code}</span>}
        {kids.length > 0 && (
          <div className="flex flex-col gap-1.5">
            {kids.map((k) => (
              <CapBox key={k.id} cap={k} childrenOf={childrenOf} coverage={coverage} mode={mode} selected={selected} onSelect={onSelect} />
            ))}
          </div>
        )}
        {kids.length === 0 && <AppChips cap={cap} />}
      </section>
    );
  return (
    <div className={`flex min-w-0 flex-col gap-1 border-l-4 ${border} ${on ? "bg-sel" : "bg-bg"} border border-y-hair border-r-hair px-2 py-1.5`}>
      {label}
      {kids.length > 0 ? (
        <div className="flex flex-wrap gap-1.5 pl-1">
          {kids.map((k) => (
            <CapBox key={k.id} cap={k} childrenOf={childrenOf} coverage={coverage} mode={mode} selected={selected} onSelect={onSelect} />
          ))}
        </div>
      ) : (
        <AppChips cap={cap} />
      )}
    </div>
  );
}

function AppChips({ cap }: { cap: Capability }) {
  if (cap.applications.length === 0) return null;
  return (
    <p className="m-0 flex flex-wrap gap-x-2 gap-y-0.5 text-[11.5px] text-muted">
      {cap.applications.slice(0, 6).map((a) => (
        <span key={a.id} className={a.retired ? "line-through" : ""}>
          {a.code ?? a.name}
        </span>
      ))}
      {cap.applications.length > 6 && <span>+{cap.applications.length - 6}</span>}
    </p>
  );
}

function NewCapability({ parent, list, onClose, onCreated }: { parent: string; list: Capability[]; onClose: () => void; onCreated: (id: string) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [f, setF] = useState({ name: "", code: "", parent_id: parent, criticality: "" });
  const create = useMutation({
    mutationFn: () => eaApi.createCapability({ name: f.name, code: f.code || undefined, parent_id: f.parent_id || undefined, criticality: f.criticality || undefined }),
    onSuccess: (c) => {
      qc.invalidateQueries({ queryKey: ["ea"] });
      onCreated(c.id);
      onClose();
    },
  });
  return (
    <form
      className="flex flex-wrap items-end gap-3 border-l-2 border-accent pl-4"
      onSubmit={(e: FormEvent) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <label className="flex min-w-0 flex-[1_1_220px] flex-col gap-1">
        <span className="label">{t("altro.ea.cap.name")}</span>
        <input className="field" required autoFocus value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} placeholder={t("altro.ea.cap.namePlaceholder")} />
      </label>
      <label className="flex w-32 flex-col gap-1">
        <span className="label">{t("altro.ea.cap.code")}</span>
        <input className="field font-mono" value={f.code} onChange={(e) => setF({ ...f, code: e.target.value })} />
      </label>
      <label className="flex min-w-0 flex-[1_1_200px] flex-col gap-1">
        <span className="label">{t("altro.ea.cap.parent")}</span>
        <ParentSelect list={list} value={f.parent_id} onChange={(v) => setF({ ...f, parent_id: v })} />
      </label>
      <label className="flex w-32 flex-col gap-1">
        <span className="label">{t("altro.ea.cap.criticality")}</span>
        <Select className="field" value={f.criticality} onChange={(e) => setF({ ...f, criticality: e.target.value })}>
          <option value="">—</option>
          {Object.entries(CRITICALITY).map(([k, v]) => (
            <option key={k} value={k}>
              {v}
            </option>
          ))}
        </Select>
      </label>
      <button className="btn btn-primary" disabled={create.isPending}>
        {t("altro.ea.cap.create")}
      </button>
      <button type="button" className="btn" onClick={onClose}>
        {t("altro.ea.cap.cancel")}
      </button>
      <div className="basis-full">
        <ErrorNote error={create.error} />
      </div>
    </form>
  );
}

function ParentSelect({ list, value, onChange, exclude }: { list: Capability[]; value: string; onChange: (v: string) => void; exclude?: string }) {
  const t = useT();
  // Non si propone la capability stessa né le sue discendenti.
  const banned = new Set<string>();
  if (exclude) {
    banned.add(exclude);
    let grew = true;
    while (grew) {
      grew = false;
      for (const c of list)
        if (c.parent_id && banned.has(c.parent_id) && !banned.has(c.id)) {
          banned.add(c.id);
          grew = true;
        }
    }
  }
  return (
    <Select className="field" value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">{t("altro.ea.cap.topLevel")}</option>
      {list
        .filter((c) => !banned.has(c.id))
        .map((c) => (
          <option key={c.id} value={c.id}>
            {"  ".repeat(c.level - 1)}
            {c.name}
          </option>
        ))}
    </Select>
  );
}

function CapDetail({
  cap,
  list,
  coverage,
  canWrite,
  onClose,
  onAddChild,
}: {
  cap: Capability;
  list: Capability[];
  coverage?: Coverage;
  canWrite: boolean;
  onClose: () => void;
  onAddChild: () => void;
}) {
  const t = useT();
  const qc = useQueryClient();
  const [f, setF] = useState({ name: cap.name, code: cap.code ?? "", parent_id: cap.parent_id ?? "", criticality: cap.criticality ?? "", description: cap.description });
  const [link, setLink] = useState("");
  const apps = useQuery({ queryKey: ["applications", ""], queryFn: () => api.applications(), enabled: canWrite });
  const assets = useQuery({ queryKey: ["assets"], queryFn: api.assets, enabled: canWrite });
  const done = () => qc.invalidateQueries({ queryKey: ["ea"] });
  const save = useMutation({
    mutationFn: () => eaApi.updateCapability(cap.id, { name: f.name, code: f.code, parent_id: f.parent_id, criticality: f.criticality, description: f.description }),
    onSuccess: done,
  });
  const add = useMutation({
    mutationFn: () => eaApi.linkCapability(cap.id, link),
    onSuccess: () => {
      setLink("");
      done();
    },
  });
  const remove = useMutation({ mutationFn: (id: string) => eaApi.unlinkCapability(cap.id, id), onSuccess: done });
  const linked = new Set([...cap.applications.map((a) => a.id), ...cap.assets.map((a) => a.id)]);
  // Sui telefoni il pannello sta sotto la mappa: ci si porta lì quando si sceglie.
  const panel = useRef<HTMLElement>(null);
  useEffect(() => {
    if (window.innerWidth < 1024) panel.current?.scrollIntoView({ block: "start", behavior: "smooth" });
  }, []);
  return (
    <aside ref={panel} className="flex w-full shrink-0 flex-col gap-3 border-t-2 border-rule pt-3 lg:sticky lg:top-4 lg:w-[380px]" aria-label={t("altro.ea.cap.ariaCapability", { name: cap.name })}>
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <span className="font-mono text-[11.5px] text-muted">{cap.path.slice(0, -1).join(" / ") || t("altro.ea.cap.topLevelShort")}</span>
          <h2 className="m-0 text-[20px] leading-tight font-bold">{cap.name}</h2>
          <span className="flex gap-3 text-[12px]">
            <Link to="/e/$key" params={{ key: cap.key }} className="font-mono text-accent no-underline hover:underline">
              {cap.key}
            </Link>
            <Link to="/architettura/mappa" search={{ nodo: cap.key }} className="text-accent underline">
              {t("altro.ea.cap.onMap")}
            </Link>
          </span>
        </div>
        <button className="btn px-2 py-0.5" aria-label={t("altro.ea.cap.close")} onClick={onClose}>
          ×
        </button>
      </div>
      {coverage && (
        <p className={`m-0 text-[12.5px] ${STATUS[coverage.status].text}`}>
          {t("altro.ea.cap.coverageLine", { label: STATUS[coverage.status].label, count: coverage.direct, total: coverage.total })}
        </p>
      )}
      <SectionTitle>{t("altro.ea.cap.supportedBy")}</SectionTitle>
      {cap.applications.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("altro.ea.cap.noApps")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {cap.applications.map((a) => (
          <li key={a.id} className="flex items-baseline gap-2 border-b border-hair py-1.5 text-[12.5px]">
            <Link to="/e/$key" params={{ key: a.key }} className={`min-w-0 flex-1 break-words font-medium text-fg no-underline hover:underline ${a.retired ? "line-through" : ""}`}>
              {a.name}
            </Link>
            <CatalogId code={a.code} itemKey={a.key} className="shrink-0 text-[11.5px]" />
            <Criticality value={a.criticality} critical={a.dora} />
            <span className="w-16 shrink-0 text-right text-muted">{a.time ? TIME[a.time] : "—"}</span>
            {canWrite && (
              <button className="text-[12px] text-muted underline hover:text-danger" onClick={() => remove.mutate(a.id)}>
                {t("altro.ea.cap.unlink")}
              </button>
            )}
          </li>
        ))}
        {cap.assets.map((a) => (
          <li key={a.id} className="flex items-baseline gap-2 border-b border-hair py-1.5 text-[12.5px]">
            <span className="min-w-0 flex-1 break-words">
              {a.name} <span className="text-muted">· {t("altro.ea.cap.logicalAsset")}</span>
            </span>
            {canWrite && (
              <button className="text-[12px] text-muted underline hover:text-danger" onClick={() => remove.mutate(a.id)}>
                {t("altro.ea.cap.unlink")}
              </button>
            )}
          </li>
        ))}
      </ul>
      {canWrite && (
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (link) add.mutate();
          }}
        >
          <Select aria-label={t("altro.ea.cap.linkLabel")} className="field min-w-0 flex-1" value={link} onChange={(e) => setLink(e.target.value)}>
            <option value="">{t("altro.ea.cap.linkPlaceholder")}</option>
            <optgroup label={t("altro.ea.cap.applications")}>
              {apps.data?.items
                .filter((a) => !linked.has(a.id))
                .map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                    {a.code ? ` (${a.code})` : ""}
                  </option>
                ))}
            </optgroup>
            <optgroup label={t("altro.ea.cap.logicalAssets")}>
              {assets.data?.items
                .filter((a) => !linked.has(a.id))
                .map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
            </optgroup>
          </Select>
          <button className="btn btn-primary" disabled={!link || add.isPending}>
            {t("altro.ea.cap.link")}
          </button>
        </form>
      )}
      <ErrorNote error={add.error ?? remove.error} />
      <ContinuityBlock kind="capability" refKey={cap.key} />
      {canWrite ? (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <SectionTitle aside={<button type="button" className="text-[12px] text-accent underline" onClick={onAddChild}>{t("altro.ea.cap.addChild")}</button>}>
            {t("altro.ea.cap.data")}
          </SectionTitle>
          <label className="flex flex-col gap-1">
            <span className="label">{t("altro.ea.cap.name")}</span>
            <input className="field" required value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
          </label>
          <div className="flex gap-2">
            <label className="flex w-32 flex-col gap-1">
              <span className="label">{t("altro.ea.cap.code")}</span>
              <input className="field font-mono" value={f.code} onChange={(e) => setF({ ...f, code: e.target.value })} />
            </label>
            <label className="flex min-w-0 flex-1 flex-col gap-1">
              <span className="label">{t("altro.ea.cap.criticality")}</span>
              <Select className="field" value={f.criticality} onChange={(e) => setF({ ...f, criticality: e.target.value })}>
                <option value="">—</option>
                {Object.entries(CRITICALITY).map(([k, v]) => (
                  <option key={k} value={k}>
                    {v}
                  </option>
                ))}
              </Select>
            </label>
          </div>
          <label className="flex flex-col gap-1">
            <span className="label">{t("altro.ea.cap.parent")}</span>
            <ParentSelect list={list} value={f.parent_id} onChange={(v) => setF({ ...f, parent_id: v })} exclude={cap.id} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label">{t("altro.ea.cap.description")}</span>
            <textarea className="field min-h-20" value={f.description} onChange={(e) => setF({ ...f, description: e.target.value })} />
          </label>
          <button className="btn btn-primary self-start" disabled={save.isPending}>
            {t("altro.ea.cap.save")}
          </button>
          <ErrorNote error={save.error} />
        </form>
      ) : (
        cap.description && <p className="m-0 whitespace-pre-wrap">{cap.description}</p>
      )}
    </aside>
  );
}
