// Blocchi del work management nella pagina di dettaglio (ElementPage): epic dei team con
// l'avanzamento su richiesta, impatto ed epic; assegnatario, sprint e ore per story, task e
// bug. Ogni blocco si mostra solo per i tipi a cui serve.
import { EntitySelect } from "../select";
import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type ElementDetail } from "../api";
import { useCanWrite } from "../fields";
import { ErrorNote } from "../ui";
import { ProgressBar, TimeLogForm } from "./work-ui";
import { fmtMinutes, workApi } from "../workApi";
import { useT } from "../i18n/react";
import { useWorkTypes } from "../typesApi";
import { loc } from "../i18n/format";
import { ModuleChip } from "../moduleIcon";
import { InProduzione } from "./InProduzione";


function Side({ title, aside, open = true, children }: { title: string; aside?: ReactNode; open?: boolean; children: ReactNode }) {
  return (
    <details open={open} className="group flex flex-col">
      <summary className="flex cursor-pointer list-none items-baseline justify-between border-b-2 border-rule pb-1.5 [&::-webkit-details-marker]:hidden">
        <span className="label m-0">
          <span aria-hidden="true" className="mr-1.5 inline-block w-2 text-muted group-open:rotate-90">›</span>
          <ModuleChip module="work" size="sm" className="mr-1.5 align-middle" />
          {title}
        </span>
        {aside}
      </summary>
      <div className="flex flex-col gap-2 pt-2">{children}</div>
    </details>
  );
}

/** Colonna laterale: delivery (epic e avanzamento), lavoro (assegnatario, sprint) e ore. */
export function WorkSidebar({ detail, narrow }: { detail: ElementDetail; narrow: boolean }) {
  const el = detail.element;
  const key = el.key || el.id;
  const workTypes = useWorkTypes();
  // Progetti: sulle attività e sui work package si registrano le ore (gli epic li mostra la scheda «Dettagli»).
  const planItem = el.type === "work_package" || el.type === "plan_activity";
  if (!["business_request", "impact"].includes(el.type) && !workTypes.isWork(el.type) && !planItem) return null;
  return (
    <>
      {!planItem && <DeliveryBlock refKey={key} detail={detail} open={!narrow} />}
      {(workTypes.isWork(el.type) || planItem) && <TimeBlock refKey={key} open={!narrow} />}
    </>
  );
}

function DeliveryBlock({ refKey, detail, open }: { refKey: string; detail: ElementDetail; open: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const el = detail.element;
  const canWrite = useCanWrite(detail);
  const del = useQuery({ queryKey: ["work", "delivery", refKey], queryFn: () => workApi.delivery(refKey) });
  const members = useQuery({ queryKey: ["members"], queryFn: api.members, staleTime: 300_000, enabled: el.type !== "business_request" && el.type !== "impact" });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["work"] });
    qc.invalidateQueries({ queryKey: ["element", refKey] });
    qc.invalidateQueries({ queryKey: ["links", refKey] });
    qc.invalidateQueries({ queryKey: ["activity"] });
  };
  const epic = useMutation({ mutationFn: () => workApi.createEpic(refKey), onSuccess: refresh });
  const assign = useMutation({ mutationFn: (m: string) => workApi.assign(refKey, m), onSuccess: refresh });
  const d = del.data;
  if (!d) return del.error ? <ErrorNote error={del.error} /> : null;
  const isItem = d.kind === "item" || d.kind === "epic";
  const title = d.kind === "request" ? t("lavoro.worksections.epicsTeams") : d.kind === "impact" ? t("lavoro.worksections.epicTeam") : t("lavoro.worksections.work");
  return (
    <Side
      title={title}
      open={open}
      aside={d.progress ? <span className="font-mono text-[11.5px] text-muted tabular">{d.progress.percent.toLocaleString(loc())}%</span> : undefined}
    >
      {isItem && (
        <dl className="m-0 grid grid-cols-[minmax(84px,38%)_minmax(0,1fr)] items-center text-[13px]">
          <dt className="border-b border-hair py-1.5 text-muted">{t("lavoro.worksections.assignee")}</dt>
          <dd className="m-0 border-b border-hair py-1">
            {canWrite ? (
              <EntitySelect kind="member" noneLabel={t("lavoro.worksections.none")} aria-label={t("lavoro.worksections.assignee")} className="field py-1" value={el.assignee_id ?? ""} onChange={(v) => assign.mutate(v)} />
            ) : (
              (members.data?.items.find((m) => m.id === el.assignee_id)?.display_name ?? "—")
            )}
          </dd>
          {d.kind === "item" && (
            <>
              <dt className="border-b border-hair py-1.5 text-muted">{t("lavoro.worksections.sprint")}</dt>
              <dd className="m-0 border-b border-hair py-1.5">
                {d.sprint ? (
                  <Link to="/e/$key" params={{ key: d.sprint.key }} className="no-underline hover:underline">
                    {d.sprint.title} <span className="text-[12px] text-muted">· {d.sprint.state}</span>
                  </Link>
                ) : (
                  <span className="text-muted">{t("lavoro.worksections.backlogLower")}</span>
                )}
              </dd>
            </>
          )}
          {d.space && (
            <>
              <dt className="border-b border-hair py-1.5 text-muted">{t("lavoro.worksections.space")}</dt>
              <dd className="m-0 flex gap-3 border-b border-hair py-1.5">
                <Link to="/delivery/board" search={{ spazio: d.space.key }} className="text-accent no-underline hover:underline">
                  {t("lavoro.worksections.board", { key: d.space.key })}
                </Link>
                <Link to="/delivery/backlog" search={{ spazio: d.space.key }} className="text-accent no-underline hover:underline">
                  {t("lavoro.worksections.backlog")}
                </Link>
              </dd>
            </>
          )}
        </dl>
      )}
      <ErrorNote error={assign.error} />
      {d.kind === "epic" && d.progress && (
        <div className="flex flex-col gap-1">
          <ProgressBar percent={d.progress.percent} label={t("lavoro.worksections.progress", { percent: d.progress.percent })} />
          <span className="text-[12px] text-muted">
            {t("lavoro.worksections.storiesClosed", { done: d.progress.done, total: d.progress.total })}
          </span>
          <InProduzione refKey={refKey} />
        </div>
      )}
      {(d.kind === "request" || d.kind === "impact") && (
        <>
          {d.progress && d.epics.length > 1 && (
            <div className="flex flex-col gap-1">
              <ProgressBar percent={d.progress.percent} label={t("lavoro.worksections.progressTotal", { percent: d.progress.percent })} />
              <span className="text-[12px] text-muted">
                {t("lavoro.worksections.storiesClosedIn", { done: d.progress.done, total: d.progress.total, epics: d.progress.epics })}
              </span>
            </div>
          )}
          {d.epics.length === 0 && (
            <p className="m-0 text-[12.5px] text-muted">
              {d.kind === "request"
                ? t("lavoro.worksections.noEpicRequest")
                : t("lavoro.worksections.noEpicImpact")}
            </p>
          )}
          <ul className="m-0 flex list-none flex-col gap-2.5 p-0">
            {d.epics.map((e) => (
              <li key={e.id} className="flex flex-col gap-1">
                <span className="flex min-w-0 items-baseline gap-1.5 text-[13px]">
                  <Link
                    to="/e/$key"
                    params={{ key: e.key }}
                    className={`shrink-0 font-mono text-[12px] no-underline hover:underline ${e.state_category === "done" ? "text-muted line-through" : "text-accent"}`}
                  >
                    {e.key}
                  </Link>
                  <span className="min-w-0 break-words" title={e.title}>
                    {e.team ?? e.space_key}
                    {e.application ? ` · ${e.application}` : ""}
                  </span>
                  <span className="ml-auto shrink-0 text-[11.5px] text-muted">{e.state}</span>
                </span>
                <ProgressBar percent={e.progress.percent} label={`${e.key}: ${e.progress.percent}%`} />
              </li>
            ))}
          </ul>
          {d.can_create_epic && (
            <>
              <button className="btn self-start" disabled={epic.isPending} onClick={() => epic.mutate()}>
                {t("lavoro.worksections.createEpic")}
              </button>
              <ErrorNote error={epic.error} />
            </>
          )}
        </>
      )}
    </Side>
  );
}

function TimeBlock({ refKey, open }: { refKey: string; open: boolean }) {
  const tr = useT();
  const qc = useQueryClient();
  const t = useQuery({ queryKey: ["work", "time", refKey], queryFn: () => workApi.time(refKey) });
  const [adding, setAdding] = useState(false);
  const remove = useMutation({
    mutationFn: (id: string) => workApi.deleteTime(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["work", "time", refKey] }),
  });
  const d = t.data;
  if (!d) return t.error ? <ErrorNote error={t.error} /> : null;
  return (
    <Side title={tr("lavoro.worksections.hoursWorked")} open={open} aside={<span className="font-mono text-[11.5px] text-muted tabular">{fmtMinutes(d.total_minutes)}</span>}>
      <p className="m-0 text-[12.5px] text-muted">
        {tr("lavoro.worksections.here")} <span className="font-mono text-fg tabular">{fmtMinutes(d.minutes)}</span>
        {d.total_minutes > d.minutes && (
          <>
            {" "}· {tr("lavoro.worksections.withChildren")} <span className="font-mono text-fg tabular">{fmtMinutes(d.total_minutes)}</span>
          </>
        )}
        {d.estimate != null && (
          <>
            {" "}· {tr("lavoro.worksections.estimate")} <span className="font-mono text-fg tabular">{fmtMinutes(d.estimate)}</span>
          </>
        )}
      </p>
      {d.entries.length > 0 && (
        <ul className="m-0 flex list-none flex-col p-0 text-[12.5px]">
          {d.entries.slice(0, 8).map((e) => (
            <li key={e.id} className="flex items-baseline gap-2 border-b border-hair py-1">
              <span className="w-[72px] shrink-0 text-muted tabular">{new Date(e.date + "T00:00:00").toLocaleDateString(loc(), { day: "numeric", month: "short" })}</span>
              <span className="min-w-0 flex-1 break-words" title={e.text ?? undefined}>
                {e.member}
                {e.text ? <span className="text-muted"> · {e.text}</span> : null}
              </span>
              <span className="font-mono tabular">{fmtMinutes(e.minutes)}</span>
              {e.mine && (
                <button className="text-muted hover:text-danger" aria-label={tr("lavoro.worksections.deleteEntry", { time: fmtMinutes(e.minutes), date: e.date })} onClick={() => remove.mutate(e.id)}>
                  ×
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      <ErrorNote error={remove.error} />
      {d.can_log &&
        (adding ? (
          <TimeLogForm refKey={refKey} compact onDone={() => setAdding(false)} />
        ) : (
          <button className="btn self-start py-1" onClick={() => setAdding(true)}>
            {tr("lavoro.worksections.logHours")}
          </button>
        ))}
    </Side>
  );
}
