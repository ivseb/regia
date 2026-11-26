// Spazio dell'auditor (ondata 3, C6): la pagina iniziale di chi ha il profilo auditor. Mostra l'incarico
// (norma, perimetro, periodo), l'avanzamento dell'esame dei controlli, le richieste e i rilievi
// aperti; da qui si estrae un campione, si chiede un'evidenza, si apre un rilievo e si scarica il
// pacchetto sul perimetro e sul periodo dell'incarico. L'auditor scrive solo queste cose.
import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { EntitySelect, Select } from "../select";
import { PageHeader } from "../shell";
import { ModuleChip } from "../moduleIcon";
import { ErrorNote, SectionTitle, date, Loading } from "../ui";
import { grcApi, EFFECTIVENESS } from "../grcApi";
import { auditApi, type EngControl, type EngagementDetail, type Severity } from "../auditApi";
import { ControlLink, FindingList, RequestList, SampleBlock, useRefreshAudit } from "./AuditPanels";
import { tr } from "../i18n/core";
import { GrcTabs } from "./portafoglio-ui";

function Stat({ label, value, sub, tone, testId }: { label: string; value: string; sub?: string; tone?: "danger"; testId?: string }) {
  return (
    <div className="entra m-grc relative flex min-w-0 flex-col gap-0.5 border border-hair px-3 pb-2 pt-3" data-testid={testId}>
      <span aria-hidden="true" className="absolute -left-px -right-px -top-px h-1 bg-mc" />
      <span className="label flex items-center gap-2 text-muted"><ModuleChip module="grc" size="sm" />{label}</span>
      <span className={`text-[24px] leading-tight font-bold tabular ${tone === "danger" ? "text-danger" : ""}`}>{value}</span>
      {sub && <span className="text-[12px] text-muted">{sub}</span>}
    </div>
  );
}

const today = () => new Date().toISOString().slice(0, 10);
const inMonths = (m: number) => {
  const d = new Date();
  d.setMonth(d.getMonth() + m);
  return d.toISOString().slice(0, 10);
};

function NewEngagement({ onDone, onCancel }: { onDone: (id: string) => void; onCancel?: () => void }) {
  const refresh = useRefreshAudit();
  const fws = useQuery({ queryKey: ["grc", "dashboard"], queryFn: grcApi.dashboard });
  const [title, setTitle] = useState("");
  const [framework, setFramework] = useState("");
  const [from, setFrom] = useState(inMonths(-12));
  const [to, setTo] = useState(today());
  const [controls, setControls] = useState<string[]>([]);
  const [apps, setApps] = useState<string[]>([]);
  const [note, setNote] = useState("");
  const create = useMutation({
    mutationFn: () => auditApi.create({ title, framework, period_from: from, period_to: to, note, controls, apps }),
    onSuccess: (e) => {
      refresh();
      onDone(e.id);
    },
  });
  return (
    <form className="flex max-w-[640px] flex-col gap-3" aria-label={tr("governance.auditor.new_title")} onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
      <SectionTitle>{tr("governance.auditor.new_title")}</SectionTitle>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.auditor.new_name")}</span>
        <input className="field" value={title} onChange={(e) => setTitle(e.target.value)} required maxLength={200} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.auditor.new_framework")}</span>
        <Select className="field" value={framework} onChange={(e) => setFramework(e.target.value)}>
          <option value="">{tr("governance.auditor.new_framework_all")}</option>
          {(fws.data?.frameworks ?? []).map((f) => <option key={f.key} value={f.key}>{f.name}</option>)}
        </Select>
      </label>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{tr("governance.auditor.new_from")}</span>
          <input className="field" type="date" value={from} onChange={(e) => setFrom(e.target.value)} required />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{tr("governance.auditor.new_to")}</span>
          <input className="field" type="date" value={to} onChange={(e) => setTo(e.target.value)} required />
        </label>
      </div>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.auditor.new_controls")}</span>
        <EntitySelect kind="element" types={["control"]} multiple className="field" value={controls} onChange={setControls} />
        <span className="text-[12px] text-muted">{tr("governance.auditor.new_controls_help")}</span>
      </label>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.auditor.new_apps")}</span>
        <EntitySelect kind="application" multiple className="field" value={apps} onChange={setApps} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.auditor.new_note")}</span>
        <textarea className="field" rows={2} value={note} onChange={(e) => setNote(e.target.value)} />
      </label>
      <ErrorNote error={create.error} />
      <div className="flex flex-wrap gap-2">
        <button className="btn btn-primary" disabled={create.isPending || !title.trim()}>{tr("governance.auditor.new_create")}</button>
        {onCancel && <button type="button" className="btn" onClick={onCancel}>{tr("governance.auditor.cancel")}</button>}
      </div>
    </form>
  );
}

/** Il lavoro dell'auditor su un controllo: campione, richiesta, rilievo, esame. */
function ControlWork({ eng, c }: { eng: EngagementDetail; c: EngControl }) {
  const refresh = useRefreshAudit();
  const q = useQuery({ queryKey: ["grc", "audit", "control", c.key], queryFn: () => auditApi.control(c.key) });
  const [size, setSize] = useState("5");
  const [seed, setSeed] = useState("");
  const [rTitle, setRTitle] = useState("");
  const [rBody, setRBody] = useState("");
  const [rDue, setRDue] = useState("");
  const [fSev, setFSev] = useState<Severity>("medium");
  const [fTitle, setFTitle] = useState("");
  const [fDesc, setFDesc] = useState("");
  const [rvNote, setRvNote] = useState("");
  const draw = useMutation({
    mutationFn: () => auditApi.draw(eng.id, c.key, Number(size), seed.trim() === "" ? undefined : Number(seed)),
    onSuccess: refresh,
  });
  const request = useMutation({
    mutationFn: () => auditApi.request(eng.id, { control: c.key, title: rTitle, body: rBody, due: rDue }),
    onSuccess: () => {
      setRTitle("");
      setRBody("");
      setRDue("");
      refresh();
    },
  });
  const finding = useMutation({
    mutationFn: () => auditApi.openFinding(eng.id, { control: c.key, severity: fSev, title: fTitle, description: fDesc }),
    onSuccess: () => {
      setFTitle("");
      setFDesc("");
      refresh();
    },
  });
  const review = useMutation({
    mutationFn: (conclusion: string) => auditApi.review(eng.id, c.key, conclusion, rvNote),
    onSuccess: refresh,
  });
  const mine = (q.data?.samples ?? []).filter((s) => s.engagement === eng.id);
  const reqs = (q.data?.requests ?? []).filter((r) => r.engagement === eng.id);
  const fnds = (q.data?.findings ?? []).filter((f) => f.engagement === eng.id);
  return (
    <div className="flex flex-col gap-5 border-t border-hair pt-3" data-testid="control-work">
      <section className="flex flex-col gap-2" aria-label={tr("governance.auditor.sample_section")}>
        <span className="label text-muted">{tr("governance.auditor.sample_section")}</span>
        <form className="flex flex-col gap-2" onSubmit={(e) => { e.preventDefault(); draw.mutate(); }}>
          <div className="grid grid-cols-2 gap-2">
            <label className="flex flex-col gap-1">
              <span className="label text-muted">{tr("governance.auditor.sample_size")}</span>
              <input className="field" type="number" min={1} max={200} inputMode="numeric" value={size} onChange={(e) => setSize(e.target.value)} />
            </label>
            <label className="flex flex-col gap-1">
              <span className="label text-muted">{tr("governance.auditor.sample_seed_label")}</span>
              <input className="field" type="number" inputMode="numeric" value={seed} onChange={(e) => setSeed(e.target.value)} placeholder={tr("governance.auditor.sample_seed_auto")} />
            </label>
          </div>
          <p className="m-0 text-[12px] text-muted">{tr("governance.auditor.sample_help")}</p>
          <ErrorNote error={draw.error} />
          <div><button className="btn btn-primary" disabled={draw.isPending || !(Number(size) >= 1)}>{tr("governance.auditor.sample_draw")}</button></div>
        </form>
        {mine.map((s) => <SampleBlock key={s.id} s={s} canAudit={eng.can.audit} />)}
      </section>

      <section className="flex flex-col gap-2" aria-label={tr("governance.auditor.request_section")}>
        <span className="label text-muted">{tr("governance.auditor.request_section")}</span>
        <form className="flex flex-col gap-2" onSubmit={(e) => { e.preventDefault(); request.mutate(); }}>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.request_title")}</span>
            <input className="field" value={rTitle} onChange={(e) => setRTitle(e.target.value)} maxLength={200} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.request_body")}</span>
            <textarea className="field" rows={2} value={rBody} onChange={(e) => setRBody(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.request_due")}</span>
            <input className="field" type="date" value={rDue} onChange={(e) => setRDue(e.target.value)} />
          </label>
          <ErrorNote error={request.error} />
          <div><button className="btn btn-primary" disabled={request.isPending || !rTitle.trim()}>{tr("governance.auditor.request_send")}</button></div>
        </form>
        {reqs.length > 0 && <RequestList items={reqs} showControl={false} />}
      </section>

      <section className="flex flex-col gap-2" aria-label={tr("governance.auditor.finding_section")}>
        <span className="label text-muted">{tr("governance.auditor.finding_section")}</span>
        <form className="flex flex-col gap-2" onSubmit={(e) => { e.preventDefault(); finding.mutate(); }}>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.finding_severity")}</span>
            <Select className="field" value={fSev} onChange={(e) => setFSev(e.target.value as Severity)}>
              <option value="low">{tr("governance.auditor.sev_low")}</option>
              <option value="medium">{tr("governance.auditor.sev_medium")}</option>
              <option value="high">{tr("governance.auditor.sev_high")}</option>
              <option value="critical">{tr("governance.auditor.sev_critical")}</option>
            </Select>
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.finding_title")}</span>
            <input className="field" value={fTitle} onChange={(e) => setFTitle(e.target.value)} maxLength={200} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.auditor.finding_description")}</span>
            <textarea className="field" rows={3} value={fDesc} onChange={(e) => setFDesc(e.target.value)} />
          </label>
          <ErrorNote error={finding.error} />
          <div><button className="btn btn-primary" disabled={finding.isPending || !fTitle.trim()}>{tr("governance.auditor.finding_open")}</button></div>
        </form>
        {fnds.length > 0 && <FindingList items={fnds} showControl={false} />}
      </section>

      <section className="flex flex-col gap-2" aria-label={tr("governance.auditor.review_section")}>
        <span className="label text-muted">{tr("governance.auditor.review_section")}</span>
        <input className="field" aria-label={tr("governance.auditor.review_note")} placeholder={tr("governance.auditor.review_note")} value={rvNote} onChange={(e) => setRvNote(e.target.value)} />
        <ErrorNote error={review.error} />
        <div className="flex flex-wrap gap-2">
          <button className="btn btn-primary" disabled={review.isPending} onClick={() => review.mutate("reviewed")}>{tr("governance.auditor.review_done")}</button>
          <button className="btn" disabled={review.isPending} onClick={() => review.mutate("to_clarify")}>{tr("governance.auditor.review_clarify")}</button>
        </div>
      </section>
    </div>
  );
}

function ControlCard({ eng, c }: { eng: EngagementDetail; c: EngControl }) {
  const [open, setOpen] = useState(false);
  const state = c.conclusion === "reviewed" ? tr("governance.auditor.state_reviewed") : c.conclusion === "to_clarify" ? tr("governance.auditor.state_clarify") : tr("governance.auditor.state_todo");
  return (
    <li className="flex flex-col gap-1.5 border-b border-hair py-3" data-testid="audit-control" data-state={c.conclusion || "todo"}>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <ControlLink k={c.key} title={c.title} />
      </div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[12.5px] text-muted">
        <span className={c.conclusion === "reviewed" ? "font-semibold text-ok" : c.conclusion === "to_clarify" ? "font-semibold text-flag" : "font-semibold"}>{state}</span>
        <span>{tr("governance.auditor.ctl_owner", { name: c.owner || "—" })}</span>
        <span>{EFFECTIVENESS[c.effectiveness] ?? c.effectiveness}</span>
        <span>{tr("governance.auditor.ctl_counts", { samples: c.samples, requests: c.open_requests, findings: c.open_findings })}</span>
      </div>
      {eng.can.audit && (
        <div>
          <button className="btn" aria-expanded={open} onClick={() => setOpen(!open)}>{open ? tr("governance.auditor.work_hide") : tr("governance.auditor.work")}</button>
        </div>
      )}
      {open && <ControlWork eng={eng} c={c} />}
    </li>
  );
}

export function AuditSpace({ incarico }: { incarico?: string }) {
  const navigate = useNavigate();
  const refresh = useRefreshAudit();
  const q = useQuery({ queryKey: ["grc", "audit", "home", incarico ?? ""], queryFn: () => auditApi.home(incarico) });
  const [creating, setCreating] = useState(false);
  const closeEng = useMutation({ mutationFn: (id: string) => auditApi.close(id), onSuccess: refresh });
  const h = q.data;
  const pick = (id: string) => navigate({ to: "/audit-spazio", search: { incarico: id } });
  return (
    <>
      <PageHeader path="/audit-spazio" title={tr("governance.auditor.title")} />
      <GrcTabs />
      <div className="flex max-w-[960px] flex-col gap-6 px-4 pb-10 md:px-7">
        {q.error && <ErrorNote error={q.error} />}
        {!h && !q.error && <Loading className="m-0 text-muted" />}
        {h && h.mine.length > 0 && (
          <section className="flex flex-col gap-2" aria-label={tr("governance.auditor.mine_title")}>
            <SectionTitle>{tr("governance.auditor.mine_title", { count: h.mine.length })}</SectionTitle>
            <RequestList items={h.mine} />
          </section>
        )}
        {h && !h.current && !creating && (
          <div className="flex flex-col gap-3">
            <p className="m-0 max-w-[60ch] text-muted">{h.can_audit ? tr("governance.auditor.empty_can") : tr("governance.auditor.empty_read")}</p>
            {h.can_audit && <div><button className="btn btn-primary" onClick={() => setCreating(true)}>{tr("governance.auditor.new_open")}</button></div>}
          </div>
        )}
        {h && creating && <NewEngagement onDone={(id) => { setCreating(false); pick(id); }} onCancel={() => setCreating(false)} />}
        {h && h.current && !creating && (
          <>
            <section className="flex flex-col gap-3" aria-label={tr("governance.auditor.engagement")}>
              <div className="flex flex-wrap items-end gap-3">
                {h.engagements.length > 1 && (
                  <label className="flex min-w-[220px] flex-1 flex-col gap-1">
                    <span className="label text-muted">{tr("governance.auditor.engagement")}</span>
                    <Select className="field" value={h.current.id} onChange={(e) => pick(e.target.value)}>
                      {h.engagements.map((e) => <option key={e.id} value={e.id}>{e.title}{e.status === "closed" ? ` · ${tr("governance.auditor.closed")}` : ""}</option>)}
                    </Select>
                  </label>
                )}
                {h.can_audit && <button className="btn" onClick={() => setCreating(true)}>{tr("governance.auditor.new_open")}</button>}
              </div>
              <div>
                <h2 className="m-0 text-[22px] font-bold" data-testid="engagement-title">{h.current.title}</h2>
                <p className="m-0 text-[13px] text-muted" data-testid="engagement-scope">
                  {tr("governance.auditor.scope_line", {
                    framework: h.current.framework ? h.current.framework.toUpperCase() : tr("governance.auditor.scope_all_fw"),
                    from: date(h.current.period_from),
                    to: date(h.current.period_to),
                  })}
                  {h.current.apps.length > 0 && <> · {tr("governance.auditor.scope_apps", { apps: h.current.apps.map((a) => a.title).join(", ") })}</>}
                  {h.current.status === "closed" && <> · {tr("governance.auditor.closed")}</>}
                </p>
                {h.current.note && <p className="m-0 mt-1 whitespace-pre-wrap">{h.current.note}</p>}
              </div>
              <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
                <Stat testId="stat-progress" label={tr("governance.auditor.stat_progress")} value={`${h.current.reviewed} / ${h.current.controls}`}
                  sub={tr("governance.auditor.stat_progress_sub", { todo: h.current.controls - h.current.reviewed })} />
                <Stat testId="stat-requests" label={tr("governance.auditor.stat_requests")} value={String(h.current.open_requests)}
                  sub={h.current.overdue_requests > 0 ? tr("governance.auditor.stat_overdue", { count: h.current.overdue_requests }) : undefined} tone={h.current.overdue_requests > 0 ? "danger" : undefined} />
                <Stat testId="stat-findings" label={tr("governance.auditor.stat_findings")} value={String(h.current.open_findings)} />
                <Stat testId="stat-samples" label={tr("governance.auditor.stat_samples")} value={String(h.current.samples)} />
              </div>
              <div className="flex flex-wrap gap-2">
                <a className="btn btn-primary no-underline" href={auditApi.packUrl(h.current.id)} download>{tr("governance.auditor.pack")}</a>
                {h.can_audit && h.current.status === "open" && (
                  <button className="btn" disabled={closeEng.isPending} onClick={() => { if (confirm(tr("governance.auditor.close_confirm"))) closeEng.mutate(h.current!.id); }}>{tr("governance.auditor.close_engagement")}</button>
                )}
              </div>
              <ErrorNote error={closeEng.error} />
            </section>

            <section className="flex flex-col gap-1" aria-label={tr("governance.auditor.controls_title")}>
              <SectionTitle>{tr("governance.auditor.controls_title")}</SectionTitle>
              {h.current.control_list.length === 0 ? (
                <p className="m-0 text-muted">{tr("governance.auditor.controls_empty")}</p>
              ) : (
                <ul className="m-0 flex list-none flex-col p-0">
                  {h.current.control_list.map((c) => <ControlCard key={c.id} eng={h.current!} c={c} />)}
                </ul>
              )}
            </section>
            <section className="flex flex-col gap-1" aria-label={tr("governance.auditor.requests")}>
              <SectionTitle>{tr("governance.auditor.requests")}</SectionTitle>
              <RequestList items={h.requests} />
            </section>
            <section className="flex flex-col gap-1" aria-label={tr("governance.auditor.findings")}>
              <SectionTitle>{tr("governance.auditor.findings")}</SectionTitle>
              <FindingList items={h.findings} />
            </section>
          </>
        )}
      </div>
    </>
  );
}
