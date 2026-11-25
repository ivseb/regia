import { Select } from "../select";
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { kb, type AudienceRef, type Campaign, type PageDetail, type Readings } from "../knowledgeApi";
import { ago, ErrorNote, SectionTitle } from "../ui";
import { useScrollToHash } from "./Collaboration";
import { Dialog } from "./work-ui";
import { useT } from "../i18n/react";
import { fmtDate } from "../i18n/format";

// Conferma di lettura dei documenti controllati (replica/knowledge.md): una campagna dice chi
// deve leggere (persone, gruppi, ruoli), entro quando e quale versione; ogni persona conferma
// con «Ho letto e compreso». Chi gestisce vede lo stato di ciascuno, sollecita, esporta il CSV
// per l'audit e può far riaprire la campagna a ogni nuova versione approvata.

const fmt = (iso: string | null) => (iso ? fmtDate(new Date(iso.length === 10 ? iso + "T00:00:00" : iso)) : "");

function CampaignState({ c }: { c: Campaign }) {
  const tr = useT();
  if (c.status === "closed") return <span className="label text-muted">{tr("knowledge.readings.closed")}</span>;
  if (c.complete) return <span className="label text-ok">{tr("knowledge.readings.complete")}</span>;
  if (c.overdue) return <span className="label text-danger">{tr("knowledge.readings.overdue")}</span>;
  return <span className="label text-accent">{tr("knowledge.readings.active")}</span>;
}

const READER_KEY = { confirmed: "knowledge.readings.reader.confirmed", pending: "knowledge.readings.reader.pending", outdated: "knowledge.readings.reader.outdated" } as const;

/** In testa al documento: una riga che rimanda alla conferma di lettura, che sta sotto il testo. */
export function ReadingsNotice({ pageKey }: { pageKey: string }) {
  const tr = useT();
  const q = useQuery({ queryKey: ["kb", "readings", pageKey], queryFn: () => kb.readings(pageKey), refetchInterval: 60_000 });
  const mine = (q.data?.items ?? []).filter((c) => c.status === "active" && (c.my_state === "pending" || c.my_state === "outdated"));
  if (mine.length === 0) return null;
  return (
    <p role="status" className="m-0 border-l-2 border-flag pl-3 text-[13px]">
      <span className="font-semibold">{tr("knowledge.readings.mustConfirm")}</span>{" "}
      <a href="#letture" className="text-accent underline">{tr("knowledge.readings.goConfirm")}</a>
    </p>
  );
}

/** Sezione «Conferma di lettura» di un documento controllato. */
export function ReadingsSection({ d, showingVersion, onShowVersion }: { d: PageDetail; showingVersion: number; onShowVersion: (v: number) => void }) {
  const tr = useT();
  const key = d.page.key;
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["kb", "readings", key], queryFn: () => kb.readings(key), refetchInterval: 60_000 });
  const [dialog, setDialog] = useState<Campaign | "new" | null>(null);
  const r = q.data;
  const anchor = useScrollToHash<HTMLElement>("letture", !!r);
  const set = (x: Readings) => {
    qc.setQueryData(["kb", "readings", key], x);
    qc.invalidateQueries({ queryKey: ["kb", "home"] });
  };
  if (!r) return q.error ? <ErrorNote error={q.error} /> : null;
  const mine = r.items.filter((c) => c.status === "active" && (c.my_state === "pending" || c.my_state === "outdated"));
  if (r.items.length === 0 && !r.can_manage) return null;
  const canNew = r.can_manage && d.page.controlled;
  return (
    <section ref={anchor} aria-label={tr("knowledge.readings.title")} id="letture" className="flex flex-col gap-3 border-b border-hair pb-4">
      <SectionTitle
        aside={
          canNew && (
            <button className="btn" onClick={() => setDialog("new")}>
              {tr("knowledge.readings.newCampaign")}
            </button>
          )
        }
      >
        {tr("knowledge.readings.title")}
      </SectionTitle>
      {mine.map((c) => (
        <MyConfirm key={c.id} c={c} showingVersion={showingVersion} onShowVersion={onShowVersion} onDone={set} />
      ))}
      {r.items.length === 0 && (
        <p className="m-0 text-[13px] text-muted">
          {tr("knowledge.readings.empty")}
        </p>
      )}
      <ul className="m-0 flex list-none flex-col p-0">
        {r.items.map((c) => (
          <CampaignItem key={c.id} c={c} canManage={r.can_manage} onChange={set} onEdit={() => setDialog(c)} />
        ))}
      </ul>
      {dialog && <CampaignDialog d={d} campaign={dialog === "new" ? undefined : dialog} onClose={() => setDialog(null)} onSaved={set} />}
    </section>
  );
}

function MyConfirm({ c, showingVersion, onShowVersion, onDone }: { c: Campaign; showingVersion: number; onShowVersion: (v: number) => void; onDone: (r: Readings) => void }) {
  const tr = useT();
  const confirm = useMutation({ mutationFn: () => kb.confirmReading(c.id, c.version), onSuccess: onDone });
  const sameVersion = showingVersion === c.version;
  return (
    <div role="status" className="flex flex-col gap-2 border-l-2 border-flag bg-sel/40 p-3">
      <p className="m-0 text-[13.5px]">
        <span className="font-semibold">{tr("knowledge.readings.mustConfirmVersion", { version: c.version })}</span>
        {c.due_date ? <> {tr("knowledge.readings.dueBy")} <span className={c.overdue ? "font-semibold text-danger" : "font-semibold"}>{fmt(c.due_date)}</span></> : null}
        {c.my_state === "outdated" ? <span className="text-muted"> · {tr("knowledge.readings.readOlder")}</span> : null}.
      </p>
      <div className="flex flex-wrap items-center gap-3">
        {sameVersion ? (
          <button className="btn btn-primary" disabled={confirm.isPending} onClick={() => confirm.mutate()}>
            {confirm.isPending ? tr("knowledge.readings.recording") : tr("knowledge.readings.iHaveRead")}
          </button>
        ) : (
          <button className="btn" onClick={() => onShowVersion(c.version)}>
            {tr("knowledge.readings.openVersion", { version: c.version })}
          </button>
        )}
        <span className="text-[12px] text-muted">
          {sameVersion ? tr("knowledge.readings.willRecord", { version: c.version }) : tr("knowledge.readings.readingOther", { shown: showingVersion, version: c.version })}
        </span>
      </div>
      <ErrorNote error={confirm.error} />
    </div>
  );
}

function CampaignItem({ c, canManage, onChange, onEdit }: { c: Campaign; canManage: boolean; onChange: (r: Readings) => void; onEdit: () => void }) {
  const tr = useT();
  const [people, setPeople] = useState(false);
  const remind = useMutation({ mutationFn: () => kb.remindReading(c.id), onSuccess: onChange });
  const status = useMutation({ mutationFn: () => kb.updateReading(c.id, { status: c.status === "active" ? "closed" : "active" }), onSuccess: onChange });
  const pct = c.total ? Math.round((c.confirmed * 100) / c.total) : 0;
  const missing = c.total - c.confirmed;
  return (
    <li className="flex flex-col gap-2 border-t border-hair py-3">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="font-semibold">{c.name}</span>
        <CampaignState c={c} />
        <span className="font-mono text-[12px] text-muted">v{c.version}</span>
        {c.due_date && <span className={`text-[12.5px] ${c.overdue ? "font-semibold text-danger" : "text-muted"}`}>{tr("knowledge.readings.dueOn", { date: fmt(c.due_date) })}</span>}
      </div>
      <div className="flex items-center gap-3">
        <div className="h-1.5 min-w-0 flex-1 bg-hair" role="img" aria-label={tr("knowledge.readings.progress", { confirmed: c.confirmed, total: c.total })}>
          <div className={`h-full ${c.complete ? "bg-ok" : "bg-accent"}`} style={{ width: `${pct}%` }} />
        </div>
        <span className="shrink-0 font-mono text-[12px] tabular">{c.confirmed} / {c.total}</span>
      </div>
      <p className="m-0 text-[12.5px] text-muted">
        {c.audience.map((a) => a.name).join(", ") || "—"}
        {missing > 0 && c.status === "active" ? ` · ${tr("knowledge.readings.missing", { count: missing })}` : ""}
        {c.reopen_on_approval ? ` · ${tr("knowledge.readings.reopens")}` : ""}
      </p>
      {canManage && (
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-[12.5px]">
          <button type="button" className="text-accent underline" aria-expanded={people} onClick={() => setPeople((v) => !v)}>
            {people ? tr("knowledge.readings.hidePeople") : tr("knowledge.readings.showPeople")}
          </button>
          {c.status === "active" && missing > 0 && (
            <button type="button" className="text-accent underline" disabled={remind.isPending} onClick={() => remind.mutate()} title={tr("knowledge.readings.remindHint")}>
              {tr("knowledge.readings.remind")}
            </button>
          )}
          <a className="text-accent underline" href={kb.readingCsvUrl(c.id)} download>
            {tr("knowledge.readings.exportCsv")}
          </a>
          <button type="button" className="text-accent underline" onClick={onEdit}>
            {tr("knowledge.readings.edit")}
          </button>
          <button type="button" className="text-accent underline" disabled={status.isPending} onClick={() => status.mutate()}>
            {c.status === "active" ? tr("knowledge.readings.close") : tr("knowledge.readings.reopen")}
          </button>
        </div>
      )}
      {people && canManage && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[320px] border-collapse text-[13px]">
            <thead>
              <tr className="text-left">
                <th className="label py-1 pr-3 font-normal text-muted">{tr("knowledge.readings.person")}</th>
                <th className="label py-1 pr-3 font-normal text-muted">{tr("knowledge.readings.state")}</th>
                <th className="label hidden py-1 font-normal text-muted sm:table-cell">{tr("knowledge.readings.when")}</th>
              </tr>
            </thead>
            <tbody>
              {c.people.map((p) => (
                <tr key={p.member_id} className="border-t border-hair">
                  <td className="py-1 pr-3">{p.name}</td>
                  <td className={`py-1 pr-3 ${p.state === "confirmed" ? "text-ok" : p.state === "outdated" ? "text-flag" : c.overdue ? "text-danger" : ""}`}>
                    {tr(READER_KEY[p.state as keyof typeof READER_KEY])}
                    {p.confirmed_version != null && p.state !== "pending" ? ` v${p.confirmed_version}` : ""}
                  </td>
                  <td className="hidden py-1 text-muted sm:table-cell">{p.confirmed_at ? ago(p.confirmed_at) : ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <ErrorNote error={remind.error ?? status.error} />
    </li>
  );
}

// ---------------------------------------------------------------------
// Creazione e modifica guidata
// ---------------------------------------------------------------------

function inDays(n: number): string {
  const x = new Date();
  x.setDate(x.getDate() + n);
  return `${x.getFullYear()}-${String(x.getMonth() + 1).padStart(2, "0")}-${String(x.getDate()).padStart(2, "0")}`;
}

const KIND_KEY = { member: "knowledge.readings.kind.member", group: "knowledge.readings.kind.group", role: "knowledge.readings.kind.role" } as const;

function AudiencePicker({ value, onChange }: { value: AudienceRef[]; onChange: (v: AudienceRef[]) => void }) {
  const tr = useT();
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000 });
  const opts = useQuery({ queryKey: ["kb", "audience"], queryFn: kb.audience, staleTime: 120_000 });
  const [kind, setKind] = useState<AudienceRef["kind"]>("member");
  const [filter, setFilter] = useState("");
  const all = useMemo(() => {
    const m = (members.data?.items ?? []).filter((x) => x.member_type !== "requester").map((x) => ({ id: x.id, name: x.display_name, hint: "" }));
    const g = (opts.data?.groups ?? []).map((x) => ({ id: x.id, name: x.name, hint: tr("knowledge.readings.peopleCount", { count: x.members }) }));
    const r = (opts.data?.roles ?? []).map((x) => ({ id: x.id, name: x.name, hint: "" }));
    return { member: m, group: g, role: r };
  }, [members.data, opts.data, tr]);
  const list = all[kind].filter((x) => x.name.toLowerCase().includes(filter.toLowerCase()));
  const has = (k: string, id: string) => value.some((v) => v.kind === k && v.id === id);
  const toggle = (id: string, name: string) =>
    onChange(has(kind, id) ? value.filter((v) => !(v.kind === kind && v.id === id)) : [...value, { kind, id, name }]);
  return (
    <fieldset className="m-0 flex min-w-0 flex-col gap-2 border-0 p-0">
      <legend className="label mb-1">{tr("knowledge.readings.whoReads")}</legend>
      {value.length > 0 && (
        <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
          {value.map((v) => (
            <li key={v.kind + v.id} className="inline-flex items-center gap-1.5 border border-hair px-2 py-0.5 text-[12.5px]">
              <span className="text-muted">{v.kind === "member" ? "" : v.kind === "group" ? `${tr("knowledge.readings.groupPrefix")} · ` : `${tr("knowledge.readings.rolePrefix")} · `}</span>
              {v.name ?? v.id}
              <button type="button" aria-label={tr("knowledge.readings.remove", { name: v.name ?? "" })} className="text-muted hover:text-danger" onClick={() => onChange(value.filter((x) => x !== v))}>
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
      <div role="tablist" aria-label={tr("knowledge.readings.audienceType")} className="flex gap-4 border-b border-hair">
        {(Object.keys(KIND_KEY) as AudienceRef["kind"][]).map((k) => (
          <button
            key={k}
            type="button"
            role="tab"
            aria-selected={kind === k}
            className={`-mb-px border-b-2 py-1 text-[13px] ${kind === k ? "border-accent font-semibold" : "border-transparent text-muted"}`}
            onClick={() => {
              setKind(k);
              setFilter("");
            }}
          >
            {tr(KIND_KEY[k])}
          </button>
        ))}
      </div>
      <input className="field" type="search" placeholder={tr("knowledge.readings.search")} aria-label={tr("knowledge.readings.searchAudience")} value={filter} onChange={(e) => setFilter(e.target.value)} />
      <div className="flex max-h-[168px] flex-col overflow-y-auto border border-hair px-2 py-1">
        {list.length === 0 && <span className="py-1 text-[12.5px] text-muted">{kind === "member" ? tr("knowledge.readings.noPerson") : tr("knowledge.readings.noEntry")}</span>}
        {list.map((x) => (
          <label key={x.id} className="flex items-center gap-2 py-1">
            <input type="checkbox" checked={has(kind, x.id)} onChange={() => toggle(x.id, x.name)} />
            <span className="min-w-0 flex-1 truncate">{x.name}</span>
            {x.hint && <span className="text-[12px] text-muted">{x.hint}</span>}
          </label>
        ))}
      </div>
    </fieldset>
  );
}

function CampaignDialog({ d, campaign, onClose, onSaved }: { d: PageDetail; campaign?: Campaign; onClose: () => void; onSaved: (r: Readings) => void }) {
  const tr = useT();
  const p = d.page;
  const versions = useQuery({ queryKey: ["kb", "versions", p.key], queryFn: () => kb.versions(p.key) });
  const [name, setName] = useState(campaign?.name ?? "");
  const [version, setVersion] = useState<number>(campaign?.version ?? p.approved_version ?? p.version);
  const [due, setDue] = useState(campaign?.due_date ?? inDays(14));
  const [audience, setAudience] = useState<AudienceRef[]>(campaign?.audience ?? []);
  const [reopen, setReopen] = useState(campaign?.reopen_on_approval ?? true);
  const [renew, setRenew] = useState(campaign?.renew_days ?? 14);
  const [remind, setRemind] = useState(campaign?.remind_days ?? 3);
  const audKey = JSON.stringify(audience.map((a) => a.kind + a.id).sort());
  const preview = useQuery({
    queryKey: ["kb", "aud-preview", p.key, audKey],
    queryFn: () => kb.previewReading(p.key, audience),
    enabled: audience.length > 0,
    staleTime: 30_000,
  });
  const save = useMutation({
    mutationFn: () => {
      const base = { name: name.trim() || undefined, version, due_date: due || "", audience, reopen_on_approval: reopen, renew_days: renew, remind_days: remind };
      return campaign ? kb.updateReading(campaign.id, base) : kb.createReading(p.key, base);
    },
    onSuccess: (r) => {
      onSaved(r);
      onClose();
    },
  });
  const vlist = versions.data?.items ?? [];
  return (
    <Dialog title={campaign ? tr("knowledge.readings.editTitle") : tr("knowledge.readings.newTitle")} onClose={onClose} wide>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="label">{tr("knowledge.readings.nameOptional")}</span>
          <input className="field" value={name} maxLength={200} placeholder={tr("knowledge.readings.namePlaceholder", { title: p.title })} onChange={(e) => setName(e.target.value)} />
        </label>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label className="flex flex-col gap-1">
            <span className="label">{tr("knowledge.readings.versionToRead")}</span>
            <Select className="field" value={version} onChange={(e) => setVersion(Number(e.target.value))}>
              {(vlist.length ? vlist : [{ version: p.version, in_force: false, current: true, title: "", note: "", created_at: "" }]).map((v) => (
                <option key={v.version} value={v.version}>
                  v{v.version}
                  {v.in_force ? ` · ${tr("knowledge.readings.approvedInForce")}` : v.current ? ` · ${tr("knowledge.readings.workingVersion")}` : ""}
                </option>
              ))}
            </Select>
          </label>
          <div className="flex flex-col gap-1">
            <label htmlFor="kb-scadenza" className="label">{tr("knowledge.readings.deadline")}</label>
            <input id="kb-scadenza" className="field" type="date" value={due} min={campaign ? undefined : inDays(0)} onChange={(e) => setDue(e.target.value)} />
            <div className="flex flex-wrap gap-x-3 text-[12.5px]">
              {[7, 14, 30].map((n) => (
                <button key={n} type="button" className="text-accent underline" onClick={() => setDue(inDays(n))}>
                  {tr("knowledge.readings.plusDays", { count: n })}
                </button>
              ))}
              <button type="button" className="text-accent underline" onClick={() => setDue("")}>
                {tr("knowledge.readings.noDeadline")}
              </button>
            </div>
          </div>
        </div>
        {version !== p.approved_version && p.approved_version != null && (
          <p className="m-0 text-[12.5px] text-flag">{tr("knowledge.readings.otherVersion", { version: p.approved_version })}</p>
        )}
        <AudiencePicker value={audience} onChange={setAudience} />
        {audience.length > 0 && (
          <div className="flex flex-col gap-1 border-l-2 border-hair pl-3 text-[13px]" aria-live="polite">
            {preview.isPending ? (
              <span className="text-muted">{tr("knowledge.readings.computing")}</span>
            ) : preview.data ? (
              <>
                <span>
                  <span className="font-semibold">{tr("knowledge.readings.willReceive", { count: preview.data.people.length })}</span> {tr("knowledge.readings.theRequest")}
                  {preview.data.people.length > 0 && <span className="text-muted">: {preview.data.people.slice(0, 6).map((x) => x.name).join(", ")}{preview.data.people.length > 6 ? tr("knowledge.readings.andOthers", { count: preview.data.people.length - 6 }) : ""}</span>}
                </span>
                {preview.data.excluded.length > 0 && (
                  <span className="text-flag">
                    {tr("knowledge.readings.excluded", { count: preview.data.excluded.length })}:{" "}
                    {preview.data.excluded.map((x) => x.name).join(", ")}.
                  </span>
                )}
              </>
            ) : (
              <ErrorNote error={preview.error} />
            )}
          </div>
        )}
        <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
          <legend className="label mb-1">{tr("knowledge.readings.remindersTitle")}</legend>
          <label className="flex flex-wrap items-center gap-2">
            {tr("knowledge.readings.reminder")}
            <Select className="field w-auto" value={remind} onChange={(e) => setRemind(Number(e.target.value))} aria-label={tr("knowledge.readings.reminderDays")}>
              {[0, 1, 3, 7, 14].map((n) => <option key={n} value={n}>{n === 0 ? tr("knowledge.readings.onDueDay") : tr("knowledge.readings.daysBefore", { count: n })}</option>)}
            </Select>
            <span className="text-[12.5px] text-muted">{tr("knowledge.readings.weeklyAfter")}</span>
          </label>
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-1" checked={reopen} onChange={(e) => setReopen(e.target.checked)} />
            <span>
              {tr("knowledge.readings.reopenOnApproval")}
              <span className="block text-[12.5px] text-muted">{tr("knowledge.readings.reopenHint")}</span>
            </span>
          </label>
          {reopen && (
            <label className="flex flex-wrap items-center gap-2 pl-6">
              {tr("knowledge.readings.newDeadlineAfter")}
              <Select className="field w-auto" value={renew} onChange={(e) => setRenew(Number(e.target.value))}>
                {[7, 14, 30, 60].map((n) => <option key={n} value={n}>{tr("knowledge.readings.days", { count: n })}</option>)}
              </Select>
            </label>
          )}
        </fieldset>
        <ErrorNote error={save.error} />
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>{tr("knowledge.readings.cancel")}</button>
          <button className="btn btn-primary" disabled={save.isPending || audience.length === 0}>
            {save.isPending ? tr("knowledge.readings.saving") : campaign ? tr("knowledge.readings.save") : tr("knowledge.readings.start")}
          </button>
        </div>
      </form>
    </Dialog>
  );
}
