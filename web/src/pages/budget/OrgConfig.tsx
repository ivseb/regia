// Configurazione → Organizzazione (replica/budget.md §6, passo 1): albero delle unità con aggiunta, spostamento
// (trascinando o con «Sposta in…»), archiviazione, codice di centro di costo, responsabile e persone con ruolo;
// ricerca di una persona; import CSV con anteprima e conferma. La visibilità dei budget segue questo albero.
import { useMemo, useRef, useState, type DragEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, orgApi, type OrgImportReport, type OrgKind, type OrgRole, type OrgUnitNode } from "../../api";
import { useT } from "../../i18n/react";
import { EntitySelect, OptionSelect } from "../../select";
import { PageHeader, useMe } from "../../shell";
import { ErrorNote, Loading, SectionTitle } from "../../ui";
import { Field } from "./budget-ui";

// i18n-ok: intestazione del file CSV, non un testo dell'interfaccia
const CSV_HEADER = "nome;codice;padre;tipo;responsabile;referente";
const KINDS: OrgKind[] = ["legal_entity", "business_unit", "department", "cost_center"];
const ROLES: OrgRole[] = ["manager", "finance_ref", "member"];

type Panel = { unit: string; kind: "add" | "edit" | "move" | "people" };

export function OrgConfig() {
  const t = useT();
  const qc = useQueryClient();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canEdit = perms.includes("*") || perms.includes("tenant.admin") || perms.includes("finance.write");
  const [archived, setArchived] = useState(false);
  const [panel, setPanel] = useState<Panel | null>(null);
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [dragId, setDragId] = useState<string | null>(null);
  const [dropOn, setDropOn] = useState<string | null>(null);
  const units = useQuery({ queryKey: ["org", "units", archived], queryFn: () => orgApi.units({ archived }) });
  const refresh = () => qc.invalidateQueries({ queryKey: ["org"] });
  const move = useMutation({ mutationFn: (v: { id: string; parent: string }) => orgApi.moveUnit(v.id, v.parent), onSuccess: refresh });
  const archive = useMutation({ mutationFn: (v: { id: string; archived: boolean }) => orgApi.updateUnit(v.id, { archived: v.archived }), onSuccess: refresh });

  const items = units.data?.items ?? [];
  const byId = useMemo(() => new Map(items.map((u) => [u.id, u])), [items]);
  // Righe visibili: si saltano i discendenti di un'unità chiusa (l'elenco è in ordine di visita).
  const visible = useMemo(() => {
    const out: OrgUnitNode[] = [];
    let skipBelow = -1;
    for (const u of items) {
      if (skipBelow >= 0 && u.depth > skipBelow) continue;
      skipBelow = -1;
      out.push(u);
      if (collapsed.has(u.id) && u.children > 0) skipBelow = u.depth;
    }
    return out;
  }, [items, collapsed]);

  /** Id dell'unità e di tutte quelle sotto. */
  const subtree = (id: string): Set<string> => {
    const out = new Set<string>();
    const start = items.findIndex((u) => u.id === id);
    if (start < 0) return out;
    out.add(id);
    for (let i = start + 1; i < items.length && items[i].depth > items[start].depth; i++) out.add(items[i].id);
    return out;
  };

  function drop(target: string | "") {
    const id = dragId;
    setDragId(null);
    setDropOn(null);
    if (!id || id === target) return;
    if (target && subtree(id).has(target)) return;
    if ((byId.get(id)?.parent_id ?? "") === target) return;
    move.mutate({ id, parent: target });
  }
  const overRow = (e: DragEvent, id: string) => {
    if (!dragId || subtree(dragId).has(id)) return;
    e.preventDefault();
    setDropOn(id);
  };

  const forbidden = units.error instanceof ApiError && units.error.status === 403;
  return (
    <>
      <PageHeader path={t("budget.org.path")} title={t("budget.org.title")} />
      <div className="flex max-w-[1100px] flex-col gap-8 px-4 pb-10 md:px-7">
        <p className="m-0 max-w-[70ch] text-muted">{t("budget.org.intro")}</p>
        {forbidden ? <p className="text-muted">{t("budget.org.forbidden")}</p> : <ErrorNote error={units.error ?? move.error ?? archive.error} />}
        {units.isPending && <Loading />}

        <section className="flex flex-col gap-3" aria-label={t("budget.org.tree")}>
          <SectionTitle
            aside={
              <span className="flex flex-wrap items-center gap-3">
                <label className="flex items-center gap-1.5 text-[12.5px]">
                  <input type="checkbox" checked={archived} onChange={(e) => setArchived(e.target.checked)} />
                  {t("budget.org.showArchived")}
                </label>
                {canEdit && (
                  <button className="btn" onClick={() => setPanel({ unit: "", kind: "add" })}>
                    {t("budget.org.addTop")}
                  </button>
                )}
              </span>
            }
          >
            {t("budget.org.tree")}
          </SectionTitle>
          {panel?.unit === "" && panel.kind === "add" && <UnitForm parent={null} onDone={() => { setPanel(null); refresh(); }} onCancel={() => setPanel(null)} />}
          {canEdit && dragId && (
            <div
              className={`border border-dashed px-3 py-2 text-[12.5px] ${dropOn === "" ? "border-accent bg-sel text-fg" : "border-rule text-muted"}`}
              onDragOver={(e) => { e.preventDefault(); setDropOn(""); }}
              onDragLeave={() => setDropOn(null)}
              onDrop={(e) => { e.preventDefault(); drop(""); }}
            >
              {t("budget.org.dropTop")}
            </div>
          )}
          {items.length === 0 && units.data && <p className="m-0 text-muted">{t("budget.org.empty")}</p>}
          <ul className="m-0 flex list-none flex-col p-0" role="tree">
            {visible.map((u) => (
              <li key={u.id} role="treeitem" aria-expanded={u.children > 0 ? !collapsed.has(u.id) : undefined} aria-level={u.depth + 1} className="border-b border-hair">
                <div
                  data-unit={u.name}
                  className={`flex flex-wrap items-center gap-x-3 gap-y-1 py-2 ${dropOn === u.id ? "bg-sel" : ""} ${dragId === u.id ? "opacity-50" : ""} ${u.archived ? "text-muted" : ""}`}
                  style={{ paddingLeft: `${Math.min(u.depth, 8) * 20}px` }}
                  draggable={canEdit && !u.archived}
                  onDragStart={(e) => { setDragId(u.id); e.dataTransfer.setData("text/plain", u.id); e.dataTransfer.effectAllowed = "move"; }}
                  onDragEnd={() => { setDragId(null); setDropOn(null); }}
                  onDragOver={(e) => overRow(e, u.id)}
                  onDragLeave={() => setDropOn((d) => (d === u.id ? null : d))}
                  onDrop={(e) => { e.preventDefault(); drop(u.id); }}
                >
                  {u.children > 0 ? (
                    <button
                      className="touch-44 flex size-6 items-center justify-center text-muted hover:text-fg"
                      aria-label={collapsed.has(u.id) ? t("budget.org.expand", { name: u.name }) : t("budget.org.collapse", { name: u.name })}
                      onClick={() => setCollapsed((c) => { const n = new Set(c); if (n.has(u.id)) n.delete(u.id); else n.add(u.id); return n; })}
                    >
                      <span aria-hidden="true">{collapsed.has(u.id) ? "▸" : "▾"}</span>
                    </button>
                  ) : (
                    <span className="size-6" aria-hidden="true" />
                  )}
                  <span className="flex min-w-0 flex-1 basis-56 flex-col">
                    <span className="flex flex-wrap items-baseline gap-x-2">
                      <span className="font-semibold">{u.name}</span>
                      {u.code && <span className="font-mono text-[11.5px] text-accent">{u.code}</span>}
                      <span className="label text-muted">{t(`budget.org.kind.${u.kind}`)}</span>
                      {u.archived && <span className="label">{t("budget.org.archived")}</span>}
                    </span>
                    <span className="text-[12px] text-muted">
                      {u.manager_name ? t("budget.org.managerIs", { name: u.manager_name }) : t("budget.org.noManager")}
                      {" · "}
                      {t("budget.org.peopleCount", { count: u.members })}
                    </span>
                  </span>
                  <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px]">
                    <button className="touch-44 text-accent underline" aria-label={t("budget.org.peopleOf", { name: u.name })} onClick={() => setPanel(panel?.unit === u.id && panel.kind === "people" ? null : { unit: u.id, kind: "people" })}>
                      {t("budget.org.people")}
                    </button>
                    {canEdit && !u.archived && (
                      <>
                        <button className="touch-44 text-accent underline" aria-label={t("budget.org.addChildOf", { name: u.name })} onClick={() => setPanel({ unit: u.id, kind: "add" })}>
                          {t("budget.org.addChild")}
                        </button>
                        <button className="touch-44 text-accent underline" aria-label={t("budget.org.editOf", { name: u.name })} onClick={() => setPanel({ unit: u.id, kind: "edit" })}>
                          {t("budget.org.edit")}
                        </button>
                        <button className="touch-44 text-accent underline" aria-label={t("budget.org.moveOf", { name: u.name })} onClick={() => setPanel({ unit: u.id, kind: "move" })}>
                          {t("budget.org.move")}
                        </button>
                      </>
                    )}
                    {canEdit && (
                      <button
                        className="touch-44 text-muted underline hover:text-fg disabled:no-underline disabled:opacity-50"
                        aria-label={u.archived ? t("budget.org.restoreOf", { name: u.name }) : t("budget.org.archiveOf", { name: u.name })}
                        disabled={archive.isPending}
                        onClick={() => archive.mutate({ id: u.id, archived: !u.archived })}
                      >
                        {u.archived ? t("budget.org.restore") : t("budget.org.archive")}
                      </button>
                    )}
                  </span>
                </div>
                {panel?.unit === u.id && (
                  <div className="mb-3 border-l-2 border-accent pl-4" style={{ marginLeft: `${Math.min(u.depth, 8) * 20 + 12}px` }}>
                    {panel.kind === "add" && <UnitForm parent={u} onDone={() => { setPanel(null); refresh(); }} onCancel={() => setPanel(null)} />}
                    {panel.kind === "edit" && <UnitForm unit={u} parent={byId.get(u.parent_id ?? "") ?? null} onDone={() => { setPanel(null); refresh(); }} onCancel={() => setPanel(null)} />}
                    {panel.kind === "move" && (
                      <MoveForm unit={u} items={items} exclude={subtree(u.id)} onDone={() => { setPanel(null); refresh(); }} onCancel={() => setPanel(null)} />
                    )}
                    {panel.kind === "people" && <People unit={u} canEdit={canEdit} onClose={() => setPanel(null)} />}
                  </div>
                )}
              </li>
            ))}
          </ul>
          {canEdit && items.length > 1 && <p className="m-0 text-[12px] text-muted">{t("budget.org.dragHint")}</p>}
        </section>

        <PersonSearch />
        {canEdit && <ImportCsv />}
      </div>
    </>
  );
}

function UnitForm({ unit, parent, onDone, onCancel }: { unit?: OrgUnitNode; parent: OrgUnitNode | null; onDone: () => void; onCancel: () => void }) {
  const t = useT();
  const [name, setName] = useState(unit?.name ?? "");
  const [kind, setKind] = useState<OrgKind>(unit?.kind ?? (parent ? (parent.kind === "legal_entity" ? "business_unit" : parent.kind === "business_unit" ? "department" : "cost_center") : "legal_entity"));
  const [code, setCode] = useState(unit?.code ?? "");
  const [manager, setManager] = useState(unit?.manager_id ?? "");
  const save = useMutation({
    mutationFn: () =>
      unit
        ? orgApi.updateUnit(unit.id, { name, kind, code, manager_id: manager })
        : orgApi.createUnit({ name, kind, code: code || undefined, manager_id: manager || undefined, parent_id: parent?.id }),
    onSuccess: onDone,
  });
  const heading = unit ? t("budget.org.editHeading", { name: unit.name }) : parent ? t("budget.org.newUnderHeading", { name: parent.name }) : t("budget.org.newTopHeading");
  return (
    <form className="my-2 grid gap-3 sm:grid-cols-2 lg:grid-cols-4 lg:items-end" aria-label={heading} onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
      <h3 className="label m-0 sm:col-span-2 lg:col-span-4">{heading}</h3>
      <Field label={t("budget.org.field.name")}>
        <input className="field" required autoFocus value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label={t("budget.org.field.kind")}>
        <OptionSelect value={kind} onChange={(v) => setKind(v as OrgKind)} options={KINDS.map((k) => ({ value: k, label: t(`budget.org.kind.${k}`) }))} />
      </Field>
      <Field label={t("budget.org.field.code")}>
        <input className="field font-mono" value={code} onChange={(e) => setCode(e.target.value)} placeholder={t("budget.org.field.codeHint")} />
      </Field>
      <Field label={t("budget.org.field.manager")}>
        <EntitySelect kind="member" value={manager} onChange={setManager} noneLabel={t("budget.org.noManager")} />
      </Field>
      <div className="flex gap-2 sm:col-span-2 lg:col-span-4">
        <button className="btn btn-primary" disabled={save.isPending || !name.trim()}>
          {unit ? t("budget.org.save") : t("budget.org.create")}
        </button>
        <button type="button" className="btn" onClick={onCancel}>
          {t("common.cancel")}
        </button>
      </div>
      <div className="sm:col-span-2 lg:col-span-4"><ErrorNote error={save.error} /></div>
    </form>
  );
}

function MoveForm({ unit, items, exclude, onDone, onCancel }: { unit: OrgUnitNode; items: OrgUnitNode[]; exclude: Set<string>; onDone: () => void; onCancel: () => void }) {
  const t = useT();
  const [parent, setParent] = useState(unit.parent_id ?? "");
  const mv = useMutation({ mutationFn: () => orgApi.moveUnit(unit.id, parent), onSuccess: onDone });
  const options = items.filter((u) => !exclude.has(u.id) && !u.archived).map((u) => ({ value: u.id, label: u.path, detail: u.code ?? undefined }));
  return (
    <form className="my-2 flex flex-col gap-3 sm:max-w-xl" aria-label={t("budget.org.moveHeading", { name: unit.name })} onSubmit={(e) => { e.preventDefault(); mv.mutate(); }}>
      <h3 className="label m-0">{t("budget.org.moveHeading", { name: unit.name })}</h3>
      <Field label={t("budget.org.field.newParent")}>
        <OptionSelect value={parent} onChange={setParent} options={options} noneLabel={t("budget.org.onTop")} />
      </Field>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={mv.isPending || parent === (unit.parent_id ?? "")}>{t("budget.org.moveConfirm")}</button>
        <button type="button" className="btn" onClick={onCancel}>{t("common.cancel")}</button>
      </div>
      <ErrorNote error={mv.error} />
    </form>
  );
}

function People({ unit, canEdit, onClose }: { unit: OrgUnitNode; canEdit: boolean; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["org", "members", unit.id], queryFn: () => orgApi.members(unit.id) });
  const [who, setWho] = useState("");
  const [role, setRole] = useState<OrgRole>("member");
  const refresh = () => qc.invalidateQueries({ queryKey: ["org"] });
  const set = useMutation({
    mutationFn: (v: { member: string; role: OrgRole }) => orgApi.setMember(unit.id, v.member, v.role),
    onSuccess: () => { setWho(""); refresh(); },
  });
  const remove = useMutation({ mutationFn: (member: string) => orgApi.removeMember(unit.id, member), onSuccess: refresh });
  const items = list.data?.items ?? [];
  const roleOpts = ROLES.map((r) => ({ value: r, label: t(`budget.org.role.${r}`) }));
  return (
    <section className="my-2 flex flex-col gap-3" aria-label={t("budget.org.peopleOf", { name: unit.name })}>
      <div className="flex items-baseline gap-3">
        <h3 className="label m-0">{t("budget.org.peopleHeading", { name: unit.name })}</h3>
        <button className="ml-auto text-[12px] text-muted underline hover:text-fg" onClick={onClose}>{t("common.close")}</button>
      </div>
      <ErrorNote error={list.error ?? set.error ?? remove.error} />
      {list.data && items.length === 0 && <p className="m-0 text-muted">{t("budget.org.noPeople")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((m) => (
          <li key={m.member_id} className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair py-1.5">
            <span className="min-w-0 flex-1 basis-40">
              <span className="font-medium">{m.name}</span> <span className="text-[12px] text-muted">{m.email}</span>
            </span>
            {canEdit ? (
              <>
                <OptionSelect className="field w-44" aria-label={t("budget.org.roleOf", { name: m.name })} value={m.role} onChange={(r) => set.mutate({ member: m.member_id, role: r as OrgRole })} options={roleOpts} />
                <button className="touch-44 text-[12.5px] text-muted underline hover:text-fg" aria-label={t("budget.org.removeOf", { name: m.name })} onClick={() => remove.mutate(m.member_id)}>
                  {t("budget.org.remove")}
                </button>
              </>
            ) : (
              <span className="text-[12.5px]">{t(`budget.org.role.${m.role}`)}</span>
            )}
          </li>
        ))}
      </ul>
      {canEdit && (
        <form className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_12rem_auto] sm:items-end" onSubmit={(e) => { e.preventDefault(); if (who) set.mutate({ member: who, role }); }}>
          <Field label={t("budget.org.field.person")}>
            <EntitySelect kind="member" value={who} onChange={setWho} filter={(o) => !items.some((m) => m.member_id === o.value)} />
          </Field>
          <Field label={t("budget.org.field.role")}>
            <OptionSelect value={role} onChange={(r) => setRole(r as OrgRole)} options={roleOpts} />
          </Field>
          <button className="btn btn-primary" disabled={!who || set.isPending}>{t("budget.org.addPerson")}</button>
        </form>
      )}
    </section>
  );
}

function PersonSearch() {
  const t = useT();
  const [text, setText] = useState("");
  const q = text.trim();
  const res = useQuery({ queryKey: ["org", "people", q], queryFn: () => orgApi.people({ q, limit: 20 }), enabled: q.length >= 2 });
  return (
    <section className="flex flex-col gap-3" aria-label={t("budget.org.findPerson")}>
      <SectionTitle>{t("budget.org.findPerson")}</SectionTitle>
      <Field label={t("budget.org.findPersonLabel")} className="max-w-md">
        <input className="field" type="search" value={text} onChange={(e) => setText(e.target.value)} />
      </Field>
      <ErrorNote error={res.error} />
      {q.length >= 2 && res.data && res.data.items.length === 0 && <p className="m-0 text-muted">{t("budget.org.noPersonFound")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {res.data?.items.map((p) => (
          <li key={p.member_id} className="border-b border-hair py-2">
            <span className="font-medium">{p.name}</span> <span className="text-[12px] text-muted">{p.email}</span>
            {p.units.length === 0 ? (
              <p className="m-0 text-[12.5px] text-muted">{t("budget.org.noUnits")}</p>
            ) : (
              <ul className="m-0 mt-1 flex list-none flex-wrap gap-x-5 gap-y-0.5 p-0 text-[12.5px]">
                {p.units.map((u) => (
                  <li key={u.unit_id}>
                    {u.path} <span className="label text-muted">{t(`budget.org.role.${u.role}`)}</span>
                  </li>
                ))}
              </ul>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

export function ImportCsv() {
  const t = useT();
  const qc = useQueryClient();
  const [csv, setCsv] = useState("");
  const [report, setReport] = useState<OrgImportReport | null>(null);
  const file = useRef<HTMLInputElement>(null);
  const preview = useMutation({ mutationFn: () => orgApi.importPreview(csv), onSuccess: setReport });
  const confirm = useMutation({
    mutationFn: () => orgApi.importConfirm(csv, report!.sha256),
    onSuccess: (r) => { setReport(r); qc.invalidateQueries({ queryKey: ["org"] }); },
  });
  const apply = report && !report.applied && report.errors === 0 && report.create + report.update > 0;
  return (
    <section className="flex flex-col gap-3" aria-label={t("budget.org.import.title")}>
      <SectionTitle>{t("budget.org.import.title")}</SectionTitle>
      <p className="m-0 max-w-[70ch] text-[12.5px] text-muted">{t("budget.org.import.help")}</p>
      <Field label={t("budget.org.import.csv")}>
        <textarea
          className="field min-h-28 font-mono text-[12.5px]"
          spellCheck={false}
          value={csv}
          placeholder={CSV_HEADER}
          onChange={(e) => { setCsv(e.target.value); setReport(null); }}
        />
      </Field>
      <div className="flex flex-wrap items-center gap-2">
        <input
          ref={file}
          type="file"
          accept=".csv,text/csv,text/plain"
          className="sr-only"
          aria-label={t("budget.org.import.file")}
          onChange={async (e) => {
            const f = e.target.files?.[0];
            if (f) { setCsv(await f.text()); setReport(null); }
            e.target.value = "";
          }}
        />
        <button type="button" className="btn" onClick={() => file.current?.click()}>{t("budget.org.import.pick")}</button>
        <button className="btn btn-primary" disabled={!csv.trim() || preview.isPending} onClick={() => preview.mutate()}>{t("budget.org.import.preview")}</button>
        {apply && (
          <button className="btn btn-primary" disabled={confirm.isPending} onClick={() => confirm.mutate()}>{t("budget.org.import.confirm")}</button>
        )}
      </div>
      <ErrorNote error={preview.error ?? confirm.error} />
      {report && (
        <div className="flex flex-col gap-2" aria-live="polite">
          <p className="m-0 flex flex-wrap gap-x-5 gap-y-1 text-[13px]">
            <span>{t("budget.org.import.create", { count: report.create })}</span>
            <span>{t("budget.org.import.update", { count: report.update })}</span>
            <span>{t("budget.org.import.unchanged", { count: report.unchanged })}</span>
            <span className={report.errors > 0 ? "font-semibold text-danger" : ""}>{t("budget.org.import.errors", { count: report.errors })}</span>
          </p>
          {report.applied && <p className="m-0 font-semibold text-ok">{t("budget.org.import.applied")}</p>}
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-[12.5px] tabular">
              <thead>
                <tr className="label text-left">
                  <th scope="col" className="border-b-2 border-rule py-1 pr-2">{t("budget.org.import.line")}</th>
                  <th scope="col" className="border-b-2 border-rule px-2 py-1">{t("budget.org.import.action")}</th>
                  <th scope="col" className="border-b-2 border-rule px-2 py-1">{t("budget.org.field.code")}</th>
                  <th scope="col" className="border-b-2 border-rule px-2 py-1">{t("budget.org.field.name")}</th>
                  <th scope="col" className="border-b-2 border-rule px-2 py-1">{t("budget.org.import.parent")}</th>
                  <th scope="col" className="border-b-2 border-rule py-1 pl-2">{t("budget.org.import.note")}</th>
                </tr>
              </thead>
              <tbody>
                {report.rows.map((r) => (
                  <tr key={r.line} className="border-b border-hair">
                    <td className="py-1 pr-2 font-mono">{r.line}</td>
                    <td className={`px-2 py-1 ${r.action === "error" ? "font-semibold text-danger" : r.action === "unchanged" ? "text-muted" : ""}`}>{t(`budget.org.import.act.${r.action}`)}</td>
                    <td className="px-2 py-1 font-mono">{r.code}</td>
                    <td className="px-2 py-1">{r.name}</td>
                    <td className="px-2 py-1">{r.parent}</td>
                    <td className="py-1 pl-2 text-muted">{r.message}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </section>
  );
}
