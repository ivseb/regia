// Elenco dei progetti (/progetti) e «Nuovo progetto» (/progetti/nuovo).
import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "../../shell";
import { EmptyState } from "../../emptyState";
import { ErrorNote, Loading, date } from "../../ui";
import { projectKeys, projectsApi, type ProjectSummary, type Template } from "../../projectsApi";
import { useT } from "../../i18n/react";
import { ItemLink, ProgressBar, SignalName } from "./shared";

function dates(p: ProjectSummary, none: string): string {
  if (p.start && p.end) return `${date(p.start)} – ${date(p.end)}`;
  return p.end || p.start ? date(p.end ?? p.start) : none;
}

const th = "label border-b-2 border-rule px-2 py-2 text-left font-semibold text-muted";
const td = "border-b border-hair px-2 py-2 align-top";

export function ProjectsPage() {
  const t = useT();
  // Con «anche i chiusi» l'elenco include i progetti completati o annullati.
  const [closed, setClosed] = useState(true);
  const q = useQuery({ queryKey: [...projectKeys.list, closed], queryFn: () => projectsApi.list(closed) });
  const none = t("progetti.common.none");
  const items = q.data?.items ?? [];
  return (
    <>
      <PageHeader
        path={t("progetti.list.path")}
        title={t("progetti.list.title")}
        actions={
          <Link to="/progetti/nuovo" className="btn btn-primary no-underline">
            {t("progetti.list.new")}
          </Link>
        }
      />
      <div className="flex flex-col gap-4 px-4 py-5 md:px-7">
        <label className="flex min-h-[44px] items-center gap-2 text-[12.5px] md:min-h-0">
          <input type="checkbox" checked={closed} onChange={(e) => setClosed(e.target.checked)} />
          {t("progetti.list.includeClosed")}
        </label>
        {q.error && <ErrorNote error={q.error} />}
        {!q.data && !q.error && <Loading className="m-0 text-muted" />}
        {q.data && items.length === 0 && (
          <EmptyState
            title={t("progetti.list.emptyTitle")}
            text={t("progetti.list.emptyText")}
            action={
              <Link to="/progetti/nuovo" className="btn no-underline">
                {t("progetti.list.new")}
              </Link>
            }
          />
        )}
        {items.length > 0 && (
          <>
            <table className="hidden w-full border-collapse text-[13px] md:table" aria-label={t("progetti.list.title")}>
              <thead>
                <tr>
                  <th className={th}>{t("progetti.list.project")}</th>
                  <th className={th}>{t("progetti.list.signal")}</th>
                  <th className={th}>{t("progetti.list.progress")}</th>
                  <th className={th}>{t("progetti.list.pm")}</th>
                  <th className={th}>{t("progetti.list.dates")}</th>
                  <th className={th}>{t("progetti.list.next")}</th>
                  <th className={th}>{t("progetti.list.late")}</th>
                </tr>
              </thead>
              <tbody>
                {items.map((p) => (
                  <tr key={p.id}>
                    <td className={td}>
                      <ItemLink itemKey={p.key} />
                      <div className="font-semibold">{p.title}</div>
                      <div className="text-[12px] text-muted">
                        {p.state} · {t("progetti.list.workPackages", { count: p.work_packages })}
                      </div>
                    </td>
                    <td className={td}>
                      <SignalName value={p.signal} />
                    </td>
                    <td className={`${td} min-w-[120px]`}>
                      <div className="flex items-center gap-2">
                        <ProgressBar percent={p.progress} />
                        <span className="tabular w-9 shrink-0 text-right">{Math.round(p.progress)}%</span>
                      </div>
                    </td>
                    <td className={td}>{p.project_manager ?? <span className="text-muted">{t("progetti.list.noPm")}</span>}</td>
                    <td className={`${td} tabular whitespace-nowrap`}>{dates(p, none)}</td>
                    <td className={td}>
                      {p.next_milestone ? (
                        <>
                          <div>{p.next_milestone.title}</div>
                          <div className="tabular text-[12px] text-muted">{date(p.next_milestone.date)}</div>
                        </>
                      ) : (
                        <span className="text-muted">{t("progetti.list.noMilestone")}</span>
                      )}
                    </td>
                    <td className={td}>{p.late > 0 ? <span className="font-semibold text-danger">{t("progetti.list.lateCount", { count: p.late })}</span> : <span className="text-muted">{t("progetti.list.onTime")}</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <ul className="m-0 flex list-none flex-col p-0 md:hidden" aria-label={t("progetti.list.title")}>
              {items.map((p) => (
                <li key={p.id} className="flex flex-col gap-1.5 border-b border-hair py-3">
                  <div className="flex items-baseline justify-between gap-3">
                    <ItemLink itemKey={p.key} />
                    <SignalName value={p.signal} className="text-[12.5px]" />
                  </div>
                  <span className="font-semibold">{p.title}</span>
                  <div className="flex items-center gap-2">
                    <ProgressBar percent={p.progress} />
                    <span className="tabular w-9 shrink-0 text-right text-[12.5px]">{Math.round(p.progress)}%</span>
                  </div>
                  <span className="text-[12.5px] text-muted">
                    {p.project_manager ?? t("progetti.list.noPm")} · <span className="tabular">{dates(p, none)}</span>
                  </span>
                  <span className="text-[12.5px]">
                    {t("progetti.list.next")}: {p.next_milestone ? `${p.next_milestone.title}, ${date(p.next_milestone.date)}` : t("progetti.list.noMilestone")}
                    {p.late > 0 && <span className="ml-2 font-semibold text-danger">{t("progetti.list.lateCount", { count: p.late })}</span>}
                  </span>
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
    </>
  );
}

const TEMPLATES: Template[] = ["agile", "waterfall", "mixed", "contract"];

export function NewProjectPage() {
  const t = useT();
  const nav = useNavigate();
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ["project-settings-default"], queryFn: () => projectsApi.settings(), retry: false });
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [template, setTemplate] = useState<Template>("mixed");
  const [touched, setTouched] = useState(false);
  const [start, setStart] = useState("");
  const [end, setEnd] = useState("");
  // Il modello predefinito viene dalle impostazioni, finché la persona non ne sceglie uno.
  useEffect(() => {
    if (settings.data && !touched) setTemplate(settings.data.default_template);
  }, [settings.data, touched]);
  const badRange = !!start && !!end && end < start;
  const create = useMutation({
    mutationFn: () => projectsApi.create({ title: title.trim(), body_text: body.trim() || undefined, template, start: start || undefined, end: end || undefined }),
    onSuccess: (el) => {
      qc.invalidateQueries({ queryKey: projectKeys.list });
      nav({ to: "/e/$key", params: { key: el.key } });
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (title.trim() && !badRange) create.mutate();
  };
  return (
    <>
      <PageHeader path={t("progetti.new.path")} title={t("progetti.new.title")} />
      <form onSubmit={submit} className="flex max-w-[640px] flex-col gap-5 px-4 py-5 md:px-7">
        <div className="flex flex-col gap-1">
          <label className="label text-muted" htmlFor="np-title">
            {t("progetti.new.name")}
          </label>
          <input id="np-title" className="field" value={title} onChange={(e) => setTitle(e.target.value)} required maxLength={200} />
        </div>
        <div className="flex flex-col gap-1">
          <label className="label text-muted" htmlFor="np-body">
            {t("progetti.new.description")}
          </label>
          <textarea id="np-body" className="field min-h-[96px]" value={body} onChange={(e) => setBody(e.target.value)} />
        </div>
        <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
          <legend className="label mb-1 p-0 text-muted">{t("progetti.new.template")}</legend>
          <p className="m-0 mb-1 text-[12.5px] text-muted">{t("progetti.new.templateHint")}</p>
          {TEMPLATES.map((k) => (
            <label key={k} className="flex min-h-[44px] cursor-pointer items-start gap-2.5 border-b border-hair py-2 md:min-h-0">
              <input type="radio" name="template" className="mt-1" checked={template === k} onChange={() => { setTemplate(k); setTouched(true); }} />
              <span className="flex flex-col">
                <span className="font-semibold">{t(`progetti.new.template.${k}`)}</span>
                <span className="text-[12.5px] text-muted">{t(`progetti.new.hint.${k}`)}</span>
              </span>
            </label>
          ))}
        </fieldset>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="flex flex-col gap-1">
            <label className="label text-muted" htmlFor="np-start">
              {t("progetti.new.start")}
            </label>
            <input id="np-start" type="date" className="field" value={start} onChange={(e) => setStart(e.target.value)} />
          </div>
          <div className="flex flex-col gap-1">
            <label className="label text-muted" htmlFor="np-end">
              {t("progetti.new.end")}
            </label>
            <input id="np-end" type="date" className="field" value={end} onChange={(e) => setEnd(e.target.value)} aria-invalid={badRange} />
          </div>
        </div>
        {badRange && <p role="alert" className="m-0 text-danger">{t("progetti.new.endBeforeStart")}</p>}
        <ErrorNote error={create.error} />
        <div className="flex flex-wrap gap-3">
          <button type="submit" className="btn btn-primary" disabled={create.isPending || !title.trim() || badRange}>
            {create.isPending ? t("progetti.new.creating") : t("progetti.new.create")}
          </button>
          <Link to="/progetti" className="btn no-underline">
            {t("progetti.new.cancel")}
          </Link>
        </div>
      </form>
    </>
  );
}
