// Configurazione → Import (docs/import.md): procedura guidata per portare in Regia i
// progetti di Jira, YouTrack o di un foglio di calcolo. Passi: origine (file o API),
// analisi, mappatura (salvabile come profilo), anteprima senza salvare, esecuzione in
// background con l'avanzamento, report e annullamento.
import { Select } from "../select";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";
import { adminApi } from "../adminApi";
import {
  DATA_TYPE_LABEL,
  PHASE_LABEL,
  SOURCE_LABEL,
  STATUS_LABEL,
  importApi,
  type Analysis,
  type Catalog,
  type FieldTarget,
  type Mapping,
  type Problem,
  type Report,
  type RowError,
  type Run,
  type Source,
} from "../importApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, date, Loading } from "../ui";
import { ConfirmButton, MutError, Tag, dateTime } from "./adminUi";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { loc, fmtNumber } from "../i18n/format";
import { tr, type Key } from "../i18n/core";

const STEPS: Key[] = ["altro.import.step.source", "altro.import.step.analysis", "altro.import.step.mapping", "altro.import.step.preview", "altro.import.step.run", "altro.import.step.report"];
const WORK_TYPES = [
  { key: "epic", name: "Epic" },
  { key: "story", name: "Story" },
  { key: "task", name: "Task" },
  { key: "bug", name: "Bug" },
];

function dataTypeLabel(t: ReturnType<typeof useT>, dataType: string): string {
  const k = DATA_TYPE_LABEL[dataType];
  return k ? t(k) : dataType;
}

export function ImportPage({ runId, embedded }: { runId?: string; embedded?: boolean }) {
  const t = useT();
  const navigate = useNavigate();
  const open = (run?: string) => navigate({ to: "/configurazione/import", search: { run } });
  const catalog = useQuery({ queryKey: ["imports", "catalog"], queryFn: importApi.catalog });
  if (catalog.error instanceof ApiError && catalog.error.status === 403) {
    return (
      <>
        {!embedded && <PageHeader path={t("altro.import.path")} title={t("altro.import.title")} />}
        <p className="px-4 text-muted md:px-7">{t("altro.import.noPermission")}</p>
      </>
    );
  }
  return runId ? (
    <RunView key={runId} id={runId} catalog={catalog.data} onBack={() => open()} />
  ) : (
    <ImportHome onOpen={open} catalog={catalog.data} embedded={embedded} />
  );
}

// ---------------------------------------------------------------------
// Pagina iniziale: origine ed elenco delle esecuzioni

function ImportHome({ onOpen, catalog, embedded }: { onOpen: (id: string) => void; catalog?: Catalog; embedded?: boolean }) {
  const t = useT();
  const runs = useQuery({ queryKey: ["imports", "runs"], queryFn: importApi.runs, refetchInterval: 10_000 });
  return (
    <>
      {!embedded && <PageHeader path={t("altro.import.path")} title={t("altro.import.title")} />}
      <div className="flex flex-col gap-6 px-4 pb-10 md:px-7">
        <p className="m-0 max-w-[760px] text-muted">
          {t("altro.import.intro")}
        </p>
        <SourceStep onCreated={onOpen} catalog={catalog} />
        <section className="flex flex-col gap-3">
          <SectionTitle>{t("altro.import.recent")}</SectionTitle>
          <ErrorNote error={runs.error} />
          <Table
            head={
              <>
                <Th>{t("altro.import.th.date")}</Th>
                <Th>{t("altro.import.th.source")}</Th>
                <Th hide="hidden md:table-cell">{t("altro.import.th.file")}</Th>
                <Th>{t("altro.import.th.status")}</Th>
                <Th right hide="hidden sm:table-cell">{t("altro.import.th.items")}</Th>
                <Th hide="hidden md:table-cell">{t("altro.import.th.space")}</Th>
              </>
            }
            empty={runs.data && runs.data.items.length === 0 ? t("altro.import.none") : undefined}
          >
            {runs.data?.items.map((r) => (
              <tr key={r.id} className="cursor-pointer border-b border-hair hover:bg-sel" onClick={() => onOpen(r.id)}>
                <Td mono className="whitespace-nowrap">
                  <button type="button" className="text-left text-accent underline" onClick={() => onOpen(r.id)}>
                    {date(r.created_at)}
                  </button>
                </Td>
                <Td>{SOURCE_LABEL[r.source] ? t(SOURCE_LABEL[r.source]) : r.source}</Td>
                <Td hide="hidden md:table-cell">
                  <span className="block max-w-64 truncate">{r.file_name}</span>
                </Td>
                <Td>
                  <RunStatusTag run={r} />
                </Td>
                <Td right mono hide="hidden sm:table-cell">
                  {r.status === "done" || r.status === "rolled_back" ? r.rows_ok : r.rows_total}
                </Td>
                <Td mono hide="hidden md:table-cell">{r.space_key ?? "—"}</Td>
              </tr>
            ))}
          </Table>
        </section>
      </div>
    </>
  );
}

function RunStatusTag({ run }: { run: Run }) {
  const t = useT();
  if (run.interrupted) return <Tag tone="flag">{t("altro.import.interrupted")}</Tag>;
  const tone = { done: "ok", failed: "danger", running: "accent", validating: "accent", preview: "accent", pending: "fg", rolled_back: "muted" }[run.status] as
    | "ok"
    | "danger"
    | "accent"
    | "fg"
    | "muted";
  return <Tag tone={tone}>{STATUS_LABEL[run.status] ? t(STATUS_LABEL[run.status]) : run.status}</Tag>;
}

function SourceStep({ onCreated, catalog }: { onCreated: (id: string) => void; catalog?: Catalog }) {
  const t = useT();
  const [source, setSource] = useState<Source>("jira_csv");
  const [file, setFile] = useState<File | null>(null);
  const [delimiter, setDelimiter] = useState("");
  const [pct, setPct] = useState<number | null>(null);
  const [conn, setConn] = useState({ base_url: "", deployment: "cloud" as "cloud" | "server", email: "", token: "", project: "", query: "" });
  const qc = useQueryClient();
  const isAPI = source.endsWith("_api");
  const upload = useMutation({
    mutationFn: () => importApi.upload(source, file!, delimiter, setPct),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["imports"] });
      onCreated(r.id);
    },
    onSettled: () => setPct(null),
  });
  const connect = useMutation({
    mutationFn: () =>
      importApi.connect({
        source: source as "jira_api" | "youtrack_api",
        base_url: conn.base_url,
        deployment: source === "jira_api" ? conn.deployment : undefined,
        email: source === "jira_api" && conn.deployment === "cloud" ? conn.email : undefined,
        token: conn.token,
        project: conn.project,
        query: conn.query || undefined,
      }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["imports"] });
      onCreated(r.id);
    },
  });
  const sources = catalog?.sources ?? (Object.keys(SOURCE_LABEL) as Source[]).map((k) => ({ key: k, name: t(SOURCE_LABEL[k]) }));
  return (
    <section className="flex flex-col gap-4">
      <SectionTitle>{t("altro.import.new.heading")}</SectionTitle>
      <fieldset className="m-0 flex flex-wrap gap-2 border-0 p-0">
        <legend className="sr-only">{t("altro.import.new.legend")}</legend>
        {sources.map((s) => (
          <label key={s.key} className={`btn cursor-pointer py-1.5 ${source === s.key ? "btn-primary" : ""}`}>
            <input type="radio" name="source" className="sr-only" checked={source === s.key} onChange={() => setSource(s.key)} />
            {s.name}
          </label>
        ))}
      </fieldset>
      {!isAPI ? (
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (file) upload.mutate();
          }}
        >
          <p className="m-0 max-w-[720px] text-[13px] text-muted">
            {source === "jira_csv" && t("altro.import.hint.jira_csv")}
            {source === "youtrack_csv" && t("altro.import.hint.youtrack_csv")}
            {source === "csv" && t("altro.import.hint.csv")}
          </p>
          <div className="flex flex-wrap items-end gap-3">
            <Field label={t("altro.import.csvFile")}>
              <input className="field w-auto max-w-full" type="file" accept=".csv,text/csv,text/plain" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
            </Field>
            <Field label={t("altro.import.delimiter")}>
              <Select className="field w-auto" value={delimiter} onChange={(e) => setDelimiter(e.target.value)}>
                <option value="">{t("altro.import.delimiter.auto")}</option>
                <option value=",">{t("altro.import.delimiter.comma")}</option>
                <option value=";">{t("altro.import.delimiter.semicolon")}</option>
                <option value={"\t"}>{t("altro.import.delimiter.tab")}</option>
              </Select>
            </Field>
            <button className="btn btn-primary" disabled={!file || upload.isPending}>
              {upload.isPending ? (pct != null && pct < 1 ? t("altro.import.uploading", { pct: Math.round(pct * 100) }) : t("altro.import.analyzing")) : t("altro.import.upload")}
            </button>
          </div>
          <ErrorNote error={upload.error} />
        </form>
      ) : (
        <form
          className="flex flex-col gap-3"
          autoComplete="off"
          onSubmit={(e) => {
            e.preventDefault();
            connect.mutate();
          }}
        >
          <p className="m-0 max-w-[720px] text-[13px] text-muted">
            {t("altro.import.apiNote")}
          </p>
          <div className="grid max-w-[760px] grid-cols-1 gap-3 sm:grid-cols-2">
            <Field label={t("altro.import.address")}>
              <input
                className="field"
                type="url"
                required
                placeholder={source === "jira_api" ? t("altro.import.urlJira") : t("altro.import.urlYoutrack")}
                value={conn.base_url}
                onChange={(e) => setConn({ ...conn, base_url: e.target.value })}
              />
            </Field>
            <Field label={t("altro.import.projectKey")}>
              <input className="field font-mono" required value={conn.project} onChange={(e) => setConn({ ...conn, project: e.target.value.toUpperCase() })} />
            </Field>
            {source === "jira_api" && (
              <Field label={t("altro.import.installation")}>
                <Select className="field" value={conn.deployment} onChange={(e) => setConn({ ...conn, deployment: e.target.value as "cloud" | "server" })}>
                  <option value="cloud">{t("altro.import.jiraCloud")}</option>
                  <option value="server">{t("altro.import.jiraServer")}</option>
                </Select>
              </Field>
            )}
            {source === "jira_api" && conn.deployment === "cloud" && (
              <Field label={t("altro.import.tokenEmail")}>
                <input className="field" type="email" required value={conn.email} onChange={(e) => setConn({ ...conn, email: e.target.value })} />
              </Field>
            )}
            <Field label={source === "jira_api" ? t("altro.import.tokenApi") : t("altro.import.tokenPermanent")}>
              <input className="field font-mono" type="password" required autoComplete="off" value={conn.token} onChange={(e) => setConn({ ...conn, token: e.target.value })} />
            </Field>
            <Field label={source === "jira_api" ? t("altro.import.jql") : t("altro.import.queryExtra")}>
              <input className="field font-mono" value={conn.query} onChange={(e) => setConn({ ...conn, query: e.target.value })} />
            </Field>
          </div>
          <div>
            <button className="btn btn-primary" disabled={connect.isPending}>
              {connect.isPending ? t("altro.import.verifying") : t("altro.import.connect")}
            </button>
          </div>
          <ErrorNote error={connect.error} />
        </form>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Un'esecuzione: analisi, mappatura, anteprima, avanzamento, report

function stepOf(r: Run): number {
  if (r.status === "validating") return 0;
  if (r.status === "pending") return 2;
  if (r.status === "preview") return 3;
  if (r.status === "running" && !r.interrupted) return r.progress.phase === "annullamento" ? 5 : 4;
  if (r.status === "running") return 4;
  if (r.status === "failed") return r.started_at ? 4 : 2;
  return 5;
}

function RunView({ id, catalog, onBack }: { id: string; catalog?: Catalog; onBack: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const run = useQuery({
    queryKey: ["imports", "run", id],
    queryFn: () => importApi.run(id),
    refetchInterval: (q) => (q.state.data && (q.state.data.status === "running" || q.state.data.status === "validating") ? 1000 : false),
  });
  const r = run.data;
  const [editing, setEditing] = useState(false);
  const refresh = () => qc.invalidateQueries({ queryKey: ["imports"] });
  if (!r) {
    return (
      <>
        <PageHeader path={t("altro.import.path")} title={t("altro.import.importTitle")} />
        <div className="px-4 md:px-7">
          <ErrorNote error={run.error} />
          {run.isPending && <Loading className="text-muted" />}
        </div>
      </>
    );
  }
  const step = stepOf(r);
  const canMap = (r.status === "pending" || r.status === "preview" || (r.status === "failed" && !r.started_at)) && !!r.analysis && !!r.mapping;
  const showMapping = canMap && (r.status !== "preview" || editing);
  return (
    <>
      <PageHeader
        path={t("altro.import.path")}
        title={r.file_name}
        actions={
          <button type="button" className="btn" onClick={onBack}>
            {t("altro.import.all")}
          </button>
        }
      />
      <div className="flex flex-col gap-6 px-4 pb-12 md:px-7">
        <Stepper step={step} />
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px] text-muted">
          <span>{t(SOURCE_LABEL[r.source])}</span>
          <RunStatusTag run={r} />
          <span>{t("altro.import.createdAt", { when: dateTime(r.created_at) })}</span>
          {r.created_by && <span>{t("altro.import.createdBy", { name: r.created_by })}</span>}
          {r.profile && <span>{t("altro.import.profileTag", { name: r.profile })}</span>}
        </div>
        {r.status === "validating" && <ProgressBar p={r.progress} label={t("altro.import.readingApi")} indeterminate />}
        {r.status === "failed" && <FailedNote run={r} onDone={refresh} />}
        {r.analysis && <AnalysisSummary a={r.analysis} />}
        {showMapping && catalog && (
          <MappingEditor
            key={r.id + (r.mapping ? JSON.stringify(r.mapping).length : 0)}
            run={r}
            catalog={catalog}
            onSaved={() => {
              setEditing(false);
              refresh();
            }}
          />
        )}
        {r.status === "preview" && r.report && (
          <PreviewSection run={r} editing={editing} onEdit={() => setEditing((v) => !v)} onStarted={refresh} />
        )}
        {r.status === "running" && <RunningSection run={r} onResumed={refresh} />}
        {(r.status === "done" || r.status === "rolled_back" || (r.status === "failed" && r.report)) && r.report && <ReportSection run={r} onDone={refresh} />}
      </div>
    </>
  );
}

function Stepper({ step }: { step: number }) {
  const t = useT();
  return (
    <ol className="m-0 flex list-none flex-wrap gap-x-1 gap-y-2 p-0" aria-label={t("altro.import.stepsLabel")}>
      {STEPS.map((s, i) => (
        <li key={s} className="flex items-center gap-1" aria-current={i === step ? "step" : undefined}>
          <span
            className={`inline-flex items-center gap-1.5 border px-2 py-0.5 text-[12px] ${
              i === step ? "border-accent bg-accent font-semibold text-accent-fg" : i < step ? "border-rule text-fg" : "border-hair text-muted"
            }`}
          >
            <span className="font-mono tabular">{i + 1}</span>
            {t(s)}
          </span>
          {i < STEPS.length - 1 && <span className="text-muted" aria-hidden="true">›</span>}
        </li>
      ))}
    </ol>
  );
}

function ProgressBar({ p, label, indeterminate }: { p: Run["progress"]; label?: string; indeterminate?: boolean }) {
  const t = useT();
  const pct = p.total > 0 ? Math.min(100, Math.round((p.done / p.total) * 100)) : 0;
  return (
    <div className="flex max-w-[640px] flex-col gap-1.5" role="status" aria-live="polite">
      <div className="flex justify-between text-[13px]">
        <span className="font-semibold">{label ?? (PHASE_LABEL[p.phase ?? ""] ? t(PHASE_LABEL[p.phase ?? ""]) : p.phase)}</span>
        <span className="font-mono tabular text-muted">
          {p.total > 0 ? t("altro.import.ofTotal", { done: fmtNumber(p.done), total: fmtNumber(p.total) }) : indeterminate ? t("altro.import.readCount", { done: fmtNumber(p.done) }) : ""}
        </span>
      </div>
      <div className="h-2 w-full bg-hair" aria-hidden="true">
        <div className="h-2 bg-accent" style={{ width: `${indeterminate && p.total === 0 ? 30 : pct}%` }} />
      </div>
      {p.message && <span className="text-[13px] text-danger">{p.message}</span>}
    </div>
  );
}

function FailedNote({ run, onDone }: { run: Run; onDone: () => void }) {
  const t = useT();
  return (
    <div role="alert" className="flex flex-col gap-2 border-l-2 border-danger px-4 py-2">
      <span className="font-semibold text-danger">{t("altro.import.failed")}</span>
      <span>{run.progress.message ?? t("altro.import.failedDefault")}</span>
      {run.started_at && (
        <>
          <span className="text-[13px] text-muted">
            {t("altro.import.failedKeep")}
          </span>
          <ResumeControls run={run} onDone={onDone} />
        </>
      )}
    </div>
  );
}

function ResumeControls({ run, onDone }: { run: Run; onDone: () => void }) {
  const t = useT();
  const [token, setToken] = useState("");
  const start = useMutation({ mutationFn: () => importApi.start(run.id, token ? { token } : undefined), onSuccess: onDone });
  const rollback = useMutation({ mutationFn: () => importApi.rollback(run.id), onSuccess: onDone });
  return (
    <div className="flex flex-wrap items-end gap-2">
      {run.needs_token && (
        <Field label={t("altro.import.tokenAgain")}>
          <input className="field font-mono" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />
        </Field>
      )}
      <button type="button" className="btn btn-primary py-1.5" disabled={start.isPending} onClick={() => start.mutate()}>
        {t("altro.import.resume")}
      </button>
      <ConfirmButton label={t("altro.import.cancelImport")} confirm={t("altro.import.cancelConfirm")} danger pending={rollback.isPending} onConfirm={() => rollback.mutate()} />
      <MutError errors={[start.error, rollback.error]} />
    </div>
  );
}

// ---------------------------------------------------------------------
// Analisi

function AnalysisSummary({ a }: { a: Analysis }) {
  const t = useT();
  const stats: [string, number][] = [
    [t("altro.import.stat.items"), a.issues],
    [t("altro.import.stat.types"), a.types.length],
    [t("altro.import.stat.states"), a.states.length],
    [t("altro.import.stat.people"), a.users.length],
    [t("altro.import.stat.fields"), a.fields.length],
    [t("altro.import.stat.sprints"), a.sprints.length],
    [t("altro.import.stat.comments"), a.comments],
    [t("altro.import.stat.worklogs"), a.worklogs],
    [t("altro.import.stat.links"), a.links],
    [t("altro.import.stat.attachments"), a.attachments],
  ];
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle aside={<span className="text-[12px] text-muted">{a.projects.map((p) => p.name).join(", ")}</span>}>{t("altro.import.analysis")}</SectionTitle>
      <Stats items={stats} />
      {a.warnings.length > 0 && (
        <ul className="m-0 flex flex-col gap-1 pl-5 text-[13px] text-flag">
          {a.warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      )}
    </section>
  );
}

function Stats({ items, danger }: { items: [string, number | string][]; danger?: string[] }) {
  return (
    <dl className="m-0 grid grid-cols-2 gap-x-6 gap-y-2 sm:flex sm:flex-wrap sm:gap-x-8">
      {items.map(([k, v]) => (
        <div key={k} className="flex flex-col">
          <dt className="label text-muted">{k}</dt>
          <dd className={`m-0 font-mono text-[17px] font-semibold tabular ${danger?.includes(k) && Number(v) > 0 ? "text-danger" : ""}`}>
            {typeof v === "number" ? fmtNumber(v) : v}
          </dd>
        </div>
      ))}
    </dl>
  );
}

// ---------------------------------------------------------------------
// Mappatura

function MappingEditor({ run, catalog, onSaved }: { run: Run; catalog: Catalog; onSaved: () => void }) {
  const t = useT();
  const a = run.analysis!;
  const [m, setM] = useState<Mapping>(() => structuredClone(run.mapping!));
  const [problems, setProblems] = useState<Problem[]>([]);
  const [profileName, setProfileName] = useState("");
  const [profileID, setProfileID] = useState("");
  const qc = useQueryClient();
  const set = (patch: Partial<Mapping>) => setM((cur) => ({ ...cur, ...patch }));
  const save = useMutation({
    mutationFn: () => importApi.saveMapping(run.id, m),
    onSuccess: (res) => {
      setProblems(res.problems);
      qc.setQueryData(["imports", "run", run.id], res.run);
    },
  });
  const preview = useMutation({
    mutationFn: async () => {
      const res = await importApi.saveMapping(run.id, m);
      setProblems(res.problems);
      if (res.problems.length > 0) throw new ApiError(422, "mapping", tr("altro.import.fixProblems"));
      return importApi.preview(run.id);
    },
    onSuccess: (r) => {
      qc.setQueryData(["imports", "run", run.id], r);
      onSaved();
    },
  });
  const apply = useMutation({
    mutationFn: () => importApi.applyProfile(run.id, profileID),
    onSuccess: (res) => {
      setProblems(res.problems);
      if (res.run.mapping) setM(structuredClone(res.run.mapping));
      qc.setQueryData(["imports", "run", run.id], res.run);
    },
  });
  const saveProfile = useMutation({
    mutationFn: async () => {
      await importApi.saveMapping(run.id, m);
      return importApi.saveProfile({ name: profileName, run_id: run.id });
    },
    onSuccess: () => {
      setProfileName("");
      qc.invalidateQueries({ queryKey: ["imports", "catalog"] });
    },
  });
  const usedTypes = useMemo(() => WORK_TYPES.filter((w) => Object.values(m.types).includes(w.key)), [m.types]);
  const problemsOf = (section: string) => problems.filter((p) => p.section === section || p.section.startsWith(section + "."));
  return (
    <section className="flex flex-col gap-6" aria-label={t("altro.import.mapping")}>
      <SectionTitle aside={<span className="text-[12px] text-muted">{t("altro.import.mappingAside")}</span>}>{t("altro.import.mappingTitle")}</SectionTitle>
      <p className="m-0 max-w-[760px] border-l-2 border-flag px-3 text-[13px]">{t("altro.import.noHistory")}</p>

      {catalog.profiles.length > 0 && (
        <div className="flex flex-wrap items-end gap-2">
          <Field label={t("altro.import.profile")}>
            <Select className="field w-auto min-w-52" value={profileID} onChange={(e) => setProfileID(e.target.value)}>
              <option value="">{t("altro.import.chooseProfile")}</option>
              {catalog.profiles.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </Select>
          </Field>
          <button type="button" className="btn py-1.5" disabled={!profileID || apply.isPending} onClick={() => apply.mutate()}>
            {t("altro.import.apply")}
          </button>
          <MutError errors={[apply.error]} />
        </div>
      )}

      <Block title={t("altro.import.destination")} problems={problemsOf("space")}>
        <fieldset className="m-0 flex flex-wrap gap-4 border-0 p-0">
          <legend className="sr-only">{t("altro.import.destinationLegend")}</legend>
          <label className="flex items-center gap-2">
            <input type="radio" checked={m.space.mode === "existing"} onChange={() => set({ space: { mode: "existing", key: catalog.spaces[0]?.key ?? "" } })} />
            {t("altro.import.existingSpace")}
          </label>
          <label className="flex items-center gap-2">
            <input
              type="radio"
              checked={m.space.mode === "new"}
              onChange={() => set({ space: { mode: "new", key: a.key_prefix ?? "", team_name: a.key_prefix ?? "", keep_numbers: !!a.key_prefix } })}
            />
            {t("altro.import.newSpace")}
          </label>
        </fieldset>
        {m.space.mode === "existing" ? (
          <Field label={t("altro.import.space")}>
            <Select className="field w-auto min-w-60" value={m.space.key} onChange={(e) => set({ space: { mode: "existing", key: e.target.value } })}>
              <option value="">{t("altro.import.choose")}</option>
              {catalog.spaces.map((s) => (
                <option key={s.key} value={s.key}>
                  {s.key} · {s.name}
                </option>
              ))}
            </Select>
          </Field>
        ) : (
          <div className="flex flex-wrap items-end gap-3">
            <Field label={t("altro.import.teamName")}>
              <input className="field w-60 max-w-full" value={m.space.team_name ?? ""} onChange={(e) => set({ space: { ...m.space, team_name: e.target.value } })} />
            </Field>
            <Field label={t("altro.import.spaceKey")}>
              <input
                className="field w-32 font-mono"
                value={m.space.key}
                onChange={(e) => set({ space: { ...m.space, key: e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "") } })}
              />
            </Field>
            {a.key_prefix && (
              <label className="flex items-center gap-2 pb-2">
                <input type="checkbox" checked={!!m.space.keep_numbers} onChange={(e) => set({ space: { ...m.space, keep_numbers: e.target.checked } })} />
                {t("altro.import.keepNumbers", { prefix: a.key_prefix, key: m.space.key || a.key_prefix })}
              </label>
            )}
          </div>
        )}
        <div className="flex flex-wrap items-end gap-3">
          <Field label={t("altro.import.dateFormat")}>
            <Select className="field w-auto" value={m.date_format} onChange={(e) => set({ date_format: e.target.value })}>
              <option value="auto">{t("altro.import.auto")}</option>
              {catalog.date_formats.map((f) => (
                <option key={f.key} value={f.key}>
                  {f.name} · {f.example}
                </option>
              ))}
            </Select>
          </Field>
          <span className="pb-2 text-[12.5px] text-muted">
            {t("altro.import.recognized", { format: catalog.date_formats.find((f) => f.key === a.date_format)?.name ?? a.date_format, tz: m.timezone ?? "Europe/Rome" })}
          </span>
        </div>
      </Block>

      {a.header && m.columns && (
        <ColumnsBlock
          header={a.header}
          columns={m.columns}
          roles={catalog.roles}
          open={run.source === "csv"}
          problems={problemsOf("columns")}
          onChange={(columns) => set({ columns })}
        />
      )}

      <Block title={t("altro.import.types")} problems={problemsOf("types")}>
        <MapTable head={[t("altro.import.typeSource"), t("altro.import.typeTarget")]}>
          {a.types.map((ty) => (
            <MapRow key={ty.name} label={ty.name} count={ty.count}>
              <Select className="field" value={m.types[ty.name] ?? ""} onChange={(e) => set({ types: { ...m.types, [ty.name]: e.target.value } })}>
                {WORK_TYPES.map((w) => (
                  <option key={w.key} value={w.key}>
                    {w.name}
                  </option>
                ))}
                <option value="">{t("altro.import.skip")}</option>
              </Select>
            </MapRow>
          ))}
        </MapTable>
      </Block>

      <Block title={t("altro.import.statesPhases")} problems={problemsOf("states")}>
        {usedTypes.map((ut) => {
          const phases = catalog.types.find((x) => x.key === ut.key)?.phases ?? [];
          const srcTypes = Object.entries(m.types).filter(([, v]) => v === ut.key).map(([k]) => k);
          const states = a.states.filter((s) => s.types.some((x) => srcTypes.includes(x)));
          if (states.length === 0) return null;
          const initial = phases.find((p) => p.category === "todo")?.name ?? phases[0]?.name;
          return (
            <div key={ut.key} className="flex flex-col gap-1">
              <h4 className="label m-0 text-muted">{ut.name}</h4>
              <MapTable head={[t("altro.import.stateSource"), t("altro.import.phase")]}>
                {states.map((s) => {
                  const v = m.states[ut.key]?.[s.name] ?? "";
                  return (
                    <MapRow key={s.name} label={s.name} count={s.count} note={!v ? t("altro.import.noMatch", { phase: initial ?? "" }) : undefined}>
                      <Select
                        className={`field ${!v ? "border-flag" : ""}`}
                        value={v}
                        onChange={(e) => set({ states: { ...m.states, [ut.key]: { ...(m.states[ut.key] ?? {}), [s.name]: e.target.value } } })}
                      >
                        <option value="">{t("altro.import.none.option")}</option>
                        {phases.map((p) => (
                          <option key={p.id} value={p.name}>
                            {p.name}
                          </option>
                        ))}
                      </Select>
                    </MapRow>
                  );
                })}
              </MapTable>
            </div>
          );
        })}
      </Block>

      <UsersBlock analysis={a} m={m} catalog={catalog} set={set} problems={problemsOf("users")} />

      {a.fields.length > 0 && (
        <Block title={t("altro.import.customFields")} problems={problemsOf("fields")}>
          <MapTable head={[t("altro.import.fieldSource"), t("altro.import.fieldTarget")]}>
            {a.fields.map((f) => (
              <MapRow
                key={f.name}
                label={f.name}
                count={f.count}
                note={`${dataTypeLabel(t, f.data_type)}${f.multi ? t("altro.import.multiValues") : ""} · ${t("altro.import.e.g.", { samples: f.samples.slice(0, 3).join(", ") })}`}
              >
                <FieldChooser
                  value={m.fields[f.name] ?? { mode: "ignore" }}
                  catalog={catalog}
                  onChange={(v) => set({ fields: { ...m.fields, [f.name]: v } })}
                />
              </MapRow>
            ))}
          </MapTable>
        </Block>
      )}

      {(a.priorities.length > 0 || a.labels_total > 0) && (
        <Block title={t("altro.import.prioLabels")} problems={[...problemsOf("priority"), ...problemsOf("labels")]}>
          {a.priorities.length > 0 && (
            <PriorityChooser analysis={a} m={m} catalog={catalog} onChange={(priority) => set({ priority })} />
          )}
          {a.labels_total > 0 && (
            <div className="flex flex-col gap-2">
              <span className="text-[13px]">
                {t("altro.import.labelsIntro", { count: a.labels_total, names: a.labels.slice(0, 6).map((l) => l.name).join(", "), more: a.labels_total > 6 ? "…" : "" })}
              </span>
              <FieldChooser value={m.labels} catalog={catalog} labels onChange={(labels) => set({ labels })} />
            </div>
          )}
        </Block>
      )}

      {a.sprints.length > 0 && m.include.sprints && (
        <Block title={t("altro.import.sprints")} problems={problemsOf("sprints")}>
          {a.format === "csv" && <p className="m-0 text-[13px] text-muted">{t("altro.import.sprintsCsv")}</p>}
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-[13px] tabular">
              <thead>
                <tr className="label">
                  <Th>{t("altro.import.sprint.name")}</Th>
                  <Th right>{t("altro.import.sprint.items")}</Th>
                  <Th>{t("altro.import.sprint.state")}</Th>
                  <Th>{t("altro.import.sprint.start")}</Th>
                  <Th>{t("altro.import.sprint.end")}</Th>
                  <Th>{t("altro.import.sprint.skip")}</Th>
                </tr>
              </thead>
              <tbody>
                {a.sprints.map((s) => {
                  const sp = m.sprints[s.name] ?? { state: "planned" };
                  const upd = (patch: Partial<typeof sp>) => set({ sprints: { ...m.sprints, [s.name]: { ...sp, ...patch } } });
                  return (
                    <tr key={s.name} className="border-b border-hair">
                      <Td>{s.name}</Td>
                      <Td right mono>
                        {s.count}
                        {s.open > 0 && <span className="text-muted">{t("altro.import.sprint.open", { count: s.open })}</span>}
                      </Td>
                      <Td>
                        <Select className="field w-auto" value={sp.state} onChange={(e) => upd({ state: e.target.value as typeof sp.state })}>
                          <option value="planned">{t("altro.import.sprint.planned")}</option>
                          <option value="active">{t("altro.import.sprint.active")}</option>
                          <option value="closed">{t("altro.import.sprint.closed")}</option>
                        </Select>
                      </Td>
                      <Td>
                        <input className="field w-auto font-mono" type="date" value={sp.start ?? ""} onChange={(e) => upd({ start: e.target.value || undefined })} />
                      </Td>
                      <Td>
                        <input className="field w-auto font-mono" type="date" value={sp.end ?? ""} onChange={(e) => upd({ end: e.target.value || undefined })} />
                      </Td>
                      <Td>
                        <input type="checkbox" aria-label={t("altro.import.sprint.skipLabel", { name: s.name })} checked={!!sp.skip} onChange={(e) => upd({ skip: e.target.checked })} />
                      </Td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </Block>
      )}

      {a.link_types.length > 0 && m.include.links && (
        <Block title={t("altro.import.linksRel")} problems={problemsOf("links")}>
          <MapTable head={[t("altro.import.linkType"), t("altro.import.relation")]}>
            {a.link_types.map((l) => (
              <MapRow key={l.name} label={l.name} count={l.count}>
                <Select className="field" value={m.links[l.name] ?? ""} onChange={(e) => set({ links: { ...m.links, [l.name]: e.target.value } })}>
                  {catalog.relations.map((r) => (
                    <option key={r.key} value={r.key}>
                      {r.name} ({r.key})
                    </option>
                  ))}
                  <option value="">{t("altro.import.skip")}</option>
                </Select>
              </MapRow>
            ))}
          </MapTable>
        </Block>
      )}

      <Block title={t("altro.import.carry")}>
        <div className="flex flex-wrap gap-x-6 gap-y-2">
          {(
            [
              ["comments", t("altro.import.inc.comments", { n: a.comments })],
              ["worklogs", t("altro.import.inc.worklogs", { n: a.worklogs })],
              ["links", t("altro.import.inc.links", { n: a.links })],
              ["sprints", t("altro.import.inc.sprints", { n: a.sprints.length })],
              ...(a.format === "api" ? ([["attachments", t("altro.import.inc.attachments", { n: a.attachments })]] as const) : []),
            ] as const
          ).map(([k, label]) => (
            <label key={k} className="flex items-center gap-2">
              <input type="checkbox" checked={m.include[k]} onChange={(e) => set({ include: { ...m.include, [k]: e.target.checked } })} />
              {label}
            </label>
          ))}
        </div>
        {a.format === "csv" && a.attachments > 0 && (
          <p className="m-0 text-[13px] text-muted">{t("altro.import.csvAttachments")}</p>
        )}
      </Block>

      {problems.length > 0 && (
        <div role="alert" className="flex flex-col gap-1 border-l-2 border-danger px-3 text-[13px]">
          <span className="font-semibold text-danger">{t("altro.import.toFix")}</span>
          {problems.map((p, i) => (
            <span key={i}>
              {p.item ? <span className="font-semibold">{p.item}: </span> : null}
              {p.message}
            </span>
          ))}
        </div>
      )}
      <div className="flex flex-wrap items-center gap-3 border-t border-hair pt-4">
        <button type="button" className="btn btn-primary" disabled={preview.isPending} onClick={() => preview.mutate()}>
          {preview.isPending ? t("altro.import.trying") : t("altro.import.tryNoSave")}
        </button>
        <button type="button" className="btn" disabled={save.isPending} onClick={() => save.mutate()}>
          {save.isSuccess && !save.isPending ? t("altro.import.mappingSaved") : t("altro.import.saveMapping")}
        </button>
        <span className="flex flex-wrap items-center gap-2">
          <input
            className="field w-48"
            aria-label={t("altro.import.profileName")}
            placeholder={t("altro.import.profileNamePh")}
            value={profileName}
            onChange={(e) => setProfileName(e.target.value)}
          />
          <button type="button" className="btn py-1.5" disabled={!profileName.trim() || saveProfile.isPending} onClick={() => saveProfile.mutate()}>
            {saveProfile.isSuccess ? t("altro.import.profileSaved") : t("altro.import.saveProfile")}
          </button>
        </span>
      </div>
      <MutError errors={[preview.error, save.error, saveProfile.error]} />
    </section>
  );
}

function Block({ title, children, problems }: { title: string; children: ReactNode; problems?: Problem[] }) {
  return (
    <div className="flex flex-col gap-3">
      <h3 className="m-0 border-b border-hair pb-1 text-[15px] font-semibold">{title}</h3>
      {children}
      {problems && problems.length > 0 && (
        <ul className="m-0 pl-5 text-[13px] text-danger">
          {problems.map((p, i) => (
            <li key={i}>
              {p.item ? `${p.item}: ` : ""}
              {p.message}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function MapTable({ head, children }: { head: [string, string]; children: ReactNode }) {
  return (
    <div className="flex max-w-[880px] flex-col">
      <div className="label hidden grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)] gap-3 border-b-2 border-rule pb-1 text-muted sm:grid">
        <span>{head[0]}</span>
        <span>{head[1]}</span>
      </div>
      {children}
    </div>
  );
}

function MapRow({ label, count, note, children }: { label: string; count?: number; note?: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-1 items-center gap-1.5 border-b border-hair py-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)] sm:gap-3">
      <div className="flex min-w-0 flex-col">
        <span className="break-words">
          {label}
          {count != null && <span className="ml-1.5 font-mono text-[12px] text-muted tabular">{fmtNumber(count)}</span>}
        </span>
        {note && <span className="text-[12px] text-muted">{note}</span>}
      </div>
      <div className="flex min-w-0 flex-col gap-1.5">{children}</div>
    </div>
  );
}

function ColumnsBlock({
  header,
  columns,
  roles,
  open,
  problems,
  onChange,
}: {
  header: string[];
  columns: string[];
  roles: Record<string, string>;
  open: boolean;
  problems: Problem[];
  onChange: (c: string[]) => void;
}) {
  const t = useT();
  const fixed = Object.entries(roles).sort((x, y) => x[1].localeCompare(y[1], loc()));
  return (
    <details open={open || problems.length > 0} className="flex flex-col gap-3">
      <summary className="cursor-pointer text-[15px] font-semibold">{t("altro.import.columnsTitle", { count: header.length })}</summary>
      <p className="m-0 mt-2 text-[13px] text-muted">{t("altro.import.columnsNote")}</p>
      <MapTable head={[t("altro.import.column"), t("altro.import.role")]}>
        {header.map((h, i) => {
          const v = columns[i] ?? "ignore";
          const custom = "custom:" + h;
          return (
            <MapRow key={i} label={h || t("altro.import.columnN", { n: i + 1 })}>
              <Select
                className="field"
                value={v}
                onChange={(e) => {
                  const next = [...columns];
                  next[i] = e.target.value;
                  onChange(next);
                }}
              >
                {fixed.map(([k, name]) => (
                  <option key={k} value={k}>
                    {name}
                  </option>
                ))}
                <option value={custom}>{t("altro.import.customField", { name: h })}</option>
                {v !== custom && !roles[v] && <option value={v}>{v.replace("link_out:", t("altro.import.role.linkOut")).replace("link_in:", t("altro.import.role.linkIn")).replace("custom:", t("altro.import.role.custom"))}</option>}
              </Select>
            </MapRow>
          );
        })}
      </MapTable>
      {problems.length > 0 && (
        <ul className="m-0 pl-5 text-[13px] text-danger">
          {problems.map((p, i) => (
            <li key={i}>{p.message}</li>
          ))}
        </ul>
      )}
    </details>
  );
}

function UsersBlock({
  analysis,
  m,
  catalog,
  set,
  problems,
}: {
  analysis: Analysis;
  m: Mapping;
  catalog: Catalog;
  set: (p: Partial<Mapping>) => void;
  problems: Problem[];
}) {
  const t = useT();
  const [inviting, setInviting] = useState<string | null>(null);
  const missing = analysis.users.filter((u) => m.users[u.ref]?.mode !== "member").length;
  return (
    <Block title={t("altro.import.people")} problems={problems}>
      <p className="m-0 text-[13px] text-muted">
        {t("altro.import.peopleNote", { missing: missing > 0 ? t("altro.import.peopleMissing", { count: missing }) : "" })}
      </p>
      <MapTable head={[t("altro.import.personSource"), t("altro.import.member")]}>
        {analysis.users.map((u) => {
          const tg = m.users[u.ref] ?? { mode: "none" };
          const value = tg.mode === "member" ? `member:${tg.member_id}` : tg.mode;
          return (
            <MapRow key={u.ref} label={u.name} count={u.count} note={[u.ref !== u.name ? u.ref : "", u.email ?? ""].filter(Boolean).join(" · ") || undefined}>
              <Select
                className={`field ${tg.mode !== "member" ? "border-flag" : ""}`}
                value={value}
                onChange={(e) => {
                  const v = e.target.value;
                  const next = v.startsWith("member:") ? { mode: "member" as const, member_id: v.slice(7) } : { mode: v as "placeholder" | "none" };
                  set({ users: { ...m.users, [u.ref]: next } });
                }}
              >
                <option value="placeholder">{t("altro.import.placeholderMember")}</option>
                <option value="none">{t("altro.import.noneEmpty")}</option>
                {catalog.members.map((mm) => (
                  <option key={mm.id} value={`member:${mm.id}`}>
                    {mm.name}
                    {mm.status === "invited" ? t("altro.import.invitedSuffix") : ""}
                  </option>
                ))}
              </Select>
              {tg.mode !== "member" &&
                (inviting === u.ref ? (
                  <InviteForm
                    user={u}
                    onDone={(memberID) => {
                      setInviting(null);
                      if (memberID) set({ users: { ...m.users, [u.ref]: { mode: "member", member_id: memberID } } });
                    }}
                  />
                ) : (
                  <button type="button" className="self-start text-[12.5px] text-accent underline" onClick={() => setInviting(u.ref)}>
                    {t("altro.import.inviteAs")}
                  </button>
                ))}
            </MapRow>
          );
        })}
      </MapTable>
    </Block>
  );
}

function InviteForm({ user, onDone }: { user: { name: string; email?: string }; onDone: (memberID?: string) => void }) {
  const t = useT();
  const [email, setEmail] = useState(user.email ?? "");
  const [name, setName] = useState(user.name);
  const qc = useQueryClient();
  const invite = useMutation({
    mutationFn: async () => {
      const roles = await adminApi.roles();
      const editor = roles.items.find((r) => r.key === "editor");
      return adminApi.addMember({ email, display_name: name, member_type: "standard", mode: "invite", role_ids: editor ? [editor.id] : [], group_ids: [] });
    },
    onSuccess: async (res) => {
      await qc.invalidateQueries({ queryKey: ["imports", "catalog"] });
      onDone(res.member_id);
    },
  });
  return (
    <form
      className="flex flex-wrap items-end gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        invite.mutate();
      }}
    >
      <input className="field w-44" aria-label={t("altro.import.inviteName")} value={name} onChange={(e) => setName(e.target.value)} required />
      <input className="field w-52" aria-label={t("altro.import.inviteEmail")} type="email" placeholder={t("altro.import.inviteEmailPh")} value={email} onChange={(e) => setEmail(e.target.value)} required />
      <button className="btn btn-primary py-1" disabled={invite.isPending}>
        {t("altro.import.invite")}
      </button>
      <button type="button" className="btn py-1" onClick={() => onDone()}>
        {t("altro.import.cancel")}
      </button>
      <MutError errors={[invite.error]} />
      <span className="w-full text-[12px] text-muted">{t("altro.import.inviteNote")}</span>
    </form>
  );
}

function FieldChooser({ value, catalog, labels, onChange }: { value: FieldTarget; catalog: Catalog; labels?: boolean; onChange: (v: FieldTarget) => void }) {
  const t = useT();
  const fields = catalog.fields.filter((f) => !labels || f.data_type === "enum" || f.data_type === "string");
  return (
    <div className="flex flex-col gap-1.5">
      <Select
        className="field"
        value={value.mode === "existing" ? `existing:${value.key}` : value.mode}
        onChange={(e) => {
          const v = e.target.value;
          if (v.startsWith("existing:")) onChange({ mode: "existing", key: v.slice(9) });
          else if (v === "new")
            onChange(labels ? { mode: "new", key: t("altro.import.labelsKey"), name: t("altro.import.labelsName"), data_type: "enum", multi: true } : { mode: "new", key: value.key ?? "", name: value.name ?? "", data_type: value.data_type ?? "string", multi: value.multi });
          else onChange({ mode: "ignore" });
        }}
      >
        <option value="ignore">{t("altro.import.skip")}</option>
        <option value="new">{t("altro.import.newField")}</option>
        <optgroup label={t("altro.import.existingFields")}>
          {fields.map((f) => (
            <option key={f.key} value={`existing:${f.key}`}>
              {f.name} ({dataTypeLabel(t, f.data_type)}
              {f.multi ? t("altro.import.multiple") : ""})
            </option>
          ))}
        </optgroup>
      </Select>
      {value.mode === "new" && (
        <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
          <input className="field" aria-label={t("altro.import.fieldName")} placeholder={t("altro.import.fieldNamePh")} value={value.name ?? ""} onChange={(e) => onChange({ ...value, name: e.target.value })} />
          <input
            className="field font-mono"
            aria-label={t("altro.import.fieldKey")}
            placeholder={t("altro.import.fieldKeyPh")}
            value={value.key ?? ""}
            onChange={(e) => onChange({ ...value, key: e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, "_") })}
          />
          {!labels && (
            <Select className="field" aria-label={t("altro.import.dataType")} value={value.data_type ?? "string"} onChange={(e) => onChange({ ...value, data_type: e.target.value })}>
              {catalog.data_types.map((d) => (
                <option key={d} value={d}>
                  {dataTypeLabel(t, d)}
                </option>
              ))}
            </Select>
          )}
          {!labels && (
            <label className="flex items-center gap-2 text-[13px]">
              <input type="checkbox" checked={!!value.multi} onChange={(e) => onChange({ ...value, multi: e.target.checked })} />
              {t("altro.import.multiValue")}
            </label>
          )}
        </div>
      )}
    </div>
  );
}

function PriorityChooser({ analysis, m, catalog, onChange }: { analysis: Analysis; m: Mapping; catalog: Catalog; onChange: (p: Mapping["priority"]) => void }) {
  const t = useT();
  const enums = catalog.fields.filter((f) => f.data_type === "enum" && !f.multi);
  const field = enums.find((f) => f.key === m.priority.field);
  return (
    <div className="flex flex-col gap-2">
      <Field label={t("altro.import.prioField")}>
        <Select className="field w-auto min-w-52" value={m.priority.field} onChange={(e) => onChange({ field: e.target.value, values: {} })}>
          <option value="">{t("altro.import.skip")}</option>
          {enums.map((f) => (
            <option key={f.key} value={f.key}>
              {f.name}
            </option>
          ))}
        </Select>
      </Field>
      {field && (
        <MapTable head={[t("altro.import.prioSource"), t("altro.import.option")]}>
          {analysis.priorities.map((p) => (
            <MapRow key={p.name} label={p.name} count={p.count}>
              <Select
                className="field"
                value={m.priority.values?.[p.name] ?? ""}
                onChange={(e) => onChange({ ...m.priority, values: { ...(m.priority.values ?? {}), [p.name]: e.target.value } })}
              >
                <option value="">{t("altro.import.prioAuto", { name: p.name })}</option>
                {(field.options ?? []).map((o) => (
                  <option key={o} value={o}>
                    {o}
                  </option>
                ))}
              </Select>
            </MapRow>
          ))}
        </MapTable>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Anteprima, esecuzione e report

function reportStats(r: Report, t: ReturnType<typeof useT>): [string, number | string][] {
  return [
    [r.dry_run ? t("altro.import.rs.toCreate") : t("altro.import.rs.created"), r.created],
    [r.dry_run ? t("altro.import.rs.toUpdate") : t("altro.import.rs.updated"), r.updated],
    [t("altro.import.rs.errors"), r.errors],
    [t("altro.import.rs.warnings"), r.warnings],
    [t("altro.import.rs.hierarchy"), r.hierarchy],
    [t("altro.import.rs.comments"), r.comments],
    [t("altro.import.rs.worklogs"), r.worklogs],
    [t("altro.import.rs.relations"), r.relations],
    [t("altro.import.rs.sprints"), r.sprints],
    [t("altro.import.rs.attachments"), r.attachments + r.attachment_links],
  ];
}

function PreviewSection({ run, editing, onEdit, onStarted }: { run: Run; editing: boolean; onEdit: () => void; onStarted: () => void }) {
  const t = useT();
  const r = run.report!;
  const [token, setToken] = useState("");
  const start = useMutation({ mutationFn: () => importApi.start(run.id, token ? { token } : undefined), onSuccess: onStarted });
  const types = Object.entries(r.by_type).filter(([, n]) => n > 0);
  return (
    <section className="flex flex-col gap-4" aria-live="polite">
      <SectionTitle aside={<span className="text-[12px] text-muted">{t("altro.import.previewAside")}</span>}>{t("altro.import.previewTitle")}</SectionTitle>
      <Stats items={reportStats(r, t)} danger={[t("altro.import.rs.errors")]} />
      <div className="flex flex-wrap gap-x-6 gap-y-1 text-[13px]">
        <span>
          <Trans k="altro.import.spaceIs" slots={{ key: <span className="font-mono">{r.space}</span>, new: r.space_new ? t("altro.import.spaceNew") : "" }} />
        </span>
        {types.length > 0 && <span>{types.map(([t, n]) => `${n} ${t}`).join(", ")}</span>}
        {r.skipped > 0 && <span className="text-muted">{t("altro.import.skipped", { count: r.skipped })}</span>}
        {r.fields_created.length > 0 && <span>{t("altro.import.newFields", { names: r.fields_created.join(", ") })}</span>}
        {r.options_created > 0 && <span>{t("altro.import.newOptions", { count: r.options_created })}</span>}
        {r.placeholder && <span>{t("altro.import.placeholderIs", { name: r.placeholder })}</span>}
      </div>
      <p className={`m-0 text-[13px] ${r.trial === "ok" ? "text-ok" : "text-danger"}`}>
        {r.trial === "ok" ? t("altro.import.trialOk") : t("altro.import.trialOther", { result: r.trial ?? t("altro.import.trialNone") })}
      </p>
      <p className="m-0 max-w-[760px] text-[13px] text-muted">{t("altro.import.noHistory")}</p>
      <Messages list={r.messages} />
      <RowErrors errors={run.errors} />
      <div className="flex flex-wrap items-end gap-3 border-t border-hair pt-4">
        {run.needs_token && run.mapping?.include.attachments && (
          <Field label={t("altro.import.tokenAttach")}>
            <input className="field font-mono" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} />
          </Field>
        )}
        <button type="button" className="btn btn-primary" disabled={start.isPending || r.created + r.updated === 0} onClick={() => start.mutate()}>
          {t("altro.import.importN", { count: r.created + r.updated })}
        </button>
        <button type="button" className="btn" onClick={onEdit}>
          {editing ? t("altro.import.closeMapping") : t("altro.import.changeMapping")}
        </button>
        {r.errors > 0 && <span className="text-[13px] text-muted">{t("altro.import.errorsSkipped")}</span>}
      </div>
      <MutError errors={[start.error]} />
    </section>
  );
}

function RunningSection({ run, onResumed }: { run: Run; onResumed: () => void }) {
  const t = useT();
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle>{run.progress.phase === "annullamento" ? t("altro.import.running.rollback") : t("altro.import.running.title")}</SectionTitle>
      {run.interrupted ? (
        <div role="alert" className="flex flex-col gap-2 border-l-2 border-flag px-4 py-2">
          <span className="font-semibold">{t("altro.import.running.interrupted")}</span>
          <span className="text-[13px] text-muted">{t("altro.import.running.interruptedNote")}</span>
          <ResumeControls run={run} onDone={onResumed} />
        </div>
      ) : (
        <>
          <ProgressBar p={run.progress} />
          <p className="m-0 text-[13px] text-muted">
            {t("altro.import.running.note")}
          </p>
        </>
      )}
    </section>
  );
}

function ReportSection({ run, onDone }: { run: Run; onDone: () => void }) {
  const t = useT();
  const r = run.report!;
  const rollback = useMutation({ mutationFn: () => importApi.rollback(run.id), onSuccess: onDone });
  const rb = r.rollback;
  return (
    <section className="flex flex-col gap-4">
      <SectionTitle aside={<span className="font-mono text-[12px] text-muted">{t("altro.import.seconds", { value: fmtNumber(r.duration_ms / 1000, { maximumFractionDigits: 1 }) })}</span>}>
        {t("altro.import.report")}
      </SectionTitle>
      <Stats items={reportStats(r, t)} danger={[t("altro.import.rs.errors")]} />
      {run.space_key && run.status === "done" && (
        <div className="flex flex-wrap gap-3">
          <Link className="btn no-underline" to="/delivery/board" search={{ spazio: run.space_key, sprint: "tutti" }}>
            {t("altro.import.openBoard", { key: run.space_key })}
          </Link>
          <Link className="btn no-underline" to="/delivery/backlog" search={{ spazio: run.space_key }}>
            {t("altro.import.backlog")}
          </Link>
        </div>
      )}
      <Messages list={r.messages} />
      <RowErrors errors={run.errors} />
      {rb && (
        <div className="flex flex-col gap-2">
          <h3 className="m-0 text-[15px] font-semibold">{t("altro.import.rollbackTitle")}</h3>
          <Stats
            items={[
              [t("altro.import.rb.deleted"), rb.deleted],
              [t("altro.import.rb.keptModified"), rb.kept_modified],
              [t("altro.import.rb.keptUpdated"), rb.kept_updated],
              [t("altro.import.rb.comments"), rb.comments],
              [t("altro.import.rb.worklogs"), rb.worklogs],
              [t("altro.import.rb.fields"), rb.fields],
            ]}
          />
          {rb.not_reverted > 0 && (
            <p className="m-0 text-[13px] text-muted">{t("altro.import.rb.notReverted", { count: rb.not_reverted })}</p>
          )}
          {rb.space_deleted && <p className="m-0 text-[13px] text-muted">{t("altro.import.rb.spaceDeleted")}</p>}
        </div>
      )}
      {run.status === "done" && (
        <div className="flex flex-col gap-2 border-t border-hair pt-4">
          <p className="m-0 max-w-[760px] text-[13px] text-muted">
            {t("altro.import.rollbackNote")}
          </p>
          <div>
            <ConfirmButton label={t("altro.import.cancelImport")} confirm={t("altro.import.cancelConfirm")} danger pending={rollback.isPending} onConfirm={() => rollback.mutate()} />
          </div>
          <MutError errors={[rollback.error]} />
        </div>
      )}
    </section>
  );
}

function Messages({ list }: { list: string[] }) {
  if (list.length === 0) return null;
  return (
    <ul className="m-0 flex flex-col gap-1 pl-5 text-[13px] text-flag">
      {list.map((m, i) => (
        <li key={i}>{m}</li>
      ))}
    </ul>
  );
}

function RowErrors({ errors }: { errors: RowError[] }) {
  const t = useT();
  const [level, setLevel] = useState<"all" | "error" | "warning">("all");
  const [limit, setLimit] = useState(50);
  useEffect(() => setLimit(50), [level]);
  if (errors.length === 0) return null;
  const list = errors.filter((e) => level === "all" || e.level === level);
  const nErr = errors.filter((e) => e.level === "error").length;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-3">
        <h3 className="m-0 text-[15px] font-semibold">{t("altro.import.rows.title")}</h3>
        <Select className="field w-auto py-1" aria-label={t("altro.import.rows.filter")} value={level} onChange={(e) => setLevel(e.target.value as typeof level)}>
          <option value="all">{t("altro.import.rows.all", { n: errors.length })}</option>
          <option value="error">{t("altro.import.rows.errors", { n: nErr })}</option>
          <option value="warning">{t("altro.import.rows.warnings", { n: errors.length - nErr })}</option>
        </Select>
      </div>
      <Table
        head={
          <>
            <Th>{t("altro.import.rows.line")}</Th>
            <Th>{t("altro.import.rows.key")}</Th>
            <Th hide="hidden sm:table-cell">{t("altro.import.rows.field")}</Th>
            <Th>{t("altro.import.rows.problem")}</Th>
          </>
        }
      >
        {list.slice(0, limit).map((e, i) => (
          <tr key={i} className="border-b border-hair">
            <Td mono>{e.line ?? "—"}</Td>
            <Td mono>{e.key ?? "—"}</Td>
            <Td hide="hidden sm:table-cell">{e.field ?? "—"}</Td>
            <Td className={e.level === "error" ? "text-danger" : ""}>
              {e.level === "error" && <span className="font-semibold">{t("altro.import.rows.error")}</span>}
              {e.message}
            </Td>
          </tr>
        ))}
      </Table>
      {list.length > limit && (
        <button type="button" className="self-start text-[12.5px] text-accent underline" onClick={() => setLimit(limit + 200)}>
          {t("altro.import.rows.more", { n: list.length - limit })}
        </button>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Pezzi comuni

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="flex min-w-0 flex-col gap-1">
      <span className="label text-muted">{label}</span>
      {children}
    </label>
  );
}

function Th({ children, right, hide }: { children?: ReactNode; right?: boolean; hide?: string }) {
  return (
    <th scope="col" className={`border-b-2 border-rule px-2 py-1.5 first:pl-0 ${right ? "text-right" : "text-left"} ${hide ?? ""}`}>
      {children}
    </th>
  );
}

function Td({ children, right, mono, hide, className }: { children?: ReactNode; right?: boolean; mono?: boolean; hide?: string; className?: string }) {
  return <td className={`px-2 py-1.5 align-top first:pl-0 ${right ? "text-right" : ""} ${mono ? "font-mono" : ""} ${hide ?? ""} ${className ?? ""}`}>{children}</td>;
}

function Table({ head, children, empty }: { head: ReactNode; children: ReactNode; empty?: string }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-[13px] tabular">
        <thead>
          <tr className="label">{head}</tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
      {empty && <p className="m-0 py-2 text-muted">{empty}</p>}
    </div>
  );
}
