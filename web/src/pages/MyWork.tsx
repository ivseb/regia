// Il mio lavoro: gli elementi assegnati a me in tutti gli spazi (story, task, bug, ma anche
// impatti e valutazioni), raggruppati per fase: prima in corso, poi da fare, poi i chiusi
// degli ultimi sette giorni.
import { useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { PageHeader } from "../shell";
import { ElementTitle, ErrorNote, date } from "../ui";
import { COLOR_DOT } from "../fields";
import { TimeLogForm, TypeMark, Dialog } from "./work-ui";
import { BulkBar, SelectBox, useSelection } from "./BulkBar";
import { fmtPoints, workApi, type MyItem } from "../workApi";
import { useT } from "../i18n/react";
import type { Key } from "../i18n/core";
import { useTypeNames, useWorkTypes } from "../typesApi";
import { useStateName } from "../stateLabels";
import { TagChips } from "../tags";

const CATEGORY: Record<string, Key> = { in_progress: "lavoro.mywork.cat.in_progress", todo: "lavoro.mywork.cat.todo", done: "lavoro.mywork.cat.done", cancelled: "lavoro.mywork.cat.cancelled" };
const ORDER = ["in_progress", "todo", "done", "cancelled", ""];

export function MyWork() {
  const t = useT();
  const tn = useTypeNames();
  const workTypes = useWorkTypes();
  const stateName = useStateName();
  const mine = useQuery({ queryKey: ["work", "me"], queryFn: workApi.mine });
  const [logFor, setLogFor] = useState<MyItem | null>(null);
  const [showDone, setShowDone] = useState(false);
  const workIds = useMemo(() => (mine.data?.items ?? []).filter((i) => workTypes.isWork(i.type)).map((i) => i.id), [mine.data, workTypes]);
  const selection = useSelection(workIds);
  const groups = useMemo(() => {
    const byCat = new Map<string, Map<string, MyItem[]>>();
    for (const it of mine.data?.items ?? []) {
      const cat = ORDER.includes(it.category) ? it.category : "";
      let states = byCat.get(cat);
      if (!states) byCat.set(cat, (states = new Map()));
      states.set(it.state, [...(states.get(it.state) ?? []), it]);
    }
    return ORDER.filter((c) => byCat.has(c)).map((c) => ({ cat: c, states: [...byCat.get(c)!.entries()] }));
  }, [mine.data]);
  const open = (mine.data?.items ?? []).filter((i) => i.category !== "done" && i.category !== "cancelled").length;
  return (
    <>
      <PageHeader path={t("lavoro.mywork.path")} title={t("lavoro.mywork.title")} />
      <div className="flex max-w-5xl flex-col gap-6 px-4 pb-10 md:px-7">
        <p className="m-0 flex flex-wrap items-center gap-x-4 gap-y-1 text-muted">
          <span>
            {mine.isPending ? t("lavoro.mywork.loading") : t("lavoro.mywork.openCount", { count: open })}
          </span>
          <label className="flex items-center gap-1.5 text-[12.5px]">
            <input type="checkbox" checked={showDone} onChange={(e) => setShowDone(e.target.checked)} />
            {t("lavoro.mywork.showDone")}
          </label>
        </p>
        <ErrorNote error={mine.error} />
        {groups
          .filter((g) => showDone || (g.cat !== "done" && g.cat !== "cancelled"))
          .map((g) => (
            <section key={g.cat || "altro"} aria-label={t(CATEGORY[g.cat] ?? "lavoro.mywork.cat.other")} className="flex flex-col gap-3">
              <h2 className="m-0 border-b-2 border-rule pb-1.5 text-[15px] font-bold">{t(CATEGORY[g.cat] ?? "lavoro.mywork.cat.noStage")}</h2>
              {g.states.map(([state, items]) => (
                <div key={state} className="flex flex-col">
                  <h3 className="label m-0 flex items-baseline gap-2 pb-1 text-muted">
                    {state ? stateName(state) : "—"} <span className="font-mono tabular">{items.length}</span>
                  </h3>
                  <ul className="m-0 flex list-none flex-col p-0">
                    {items.map((it) => (
                      <li key={it.id} className={`flex min-h-[44px] flex-wrap items-center gap-x-2.5 gap-y-0.5 border-b border-hair py-1.5 ${selection.sel.has(it.id) ? "bg-sel" : ""}`}>
                        {workTypes.isWork(it.type) ? (
                          <SelectBox id={it.id} checked={selection.sel.has(it.id)} label={t("lavoro.mywork.select", { key: it.key })} onToggle={selection.toggle} />
                        ) : (
                          <span className="size-4 shrink-0" aria-hidden="true" />
                        )}
                        <TypeMark type={it.type} />
                        <Link to="/e/$key" params={{ key: it.key }} className="shrink-0 font-mono text-[12px] text-accent no-underline hover:underline">
                          {it.key}
                        </Link>
                        <span className="min-w-0 flex-[1_1_240px] break-words"><ElementTitle type={it.type} title={it.title} /></span>
                        {it.tags && it.tags.length > 0 && <TagChips tags={it.tags} />}
                        <span className="flex shrink-0 items-center gap-2.5 text-[12px] text-muted">
                          {it.priority && (
                            <span className="inline-flex items-center gap-1">
                              <span className={`size-2 ${COLOR_DOT[it.priority_color ?? ""] ?? "bg-hair"}`} aria-hidden="true" />
                              {it.priority}
                            </span>
                          )}
                          {it.points != null && <span className="tinta m-work px-1 font-mono text-fg tabular">{t("lavoro.mywork.points", { points: fmtPoints(it.points) })}</span>}
                          {it.sprint && (
                            <Link to="/e/$key" params={{ key: it.sprint }} className="font-mono text-muted no-underline hover:text-fg" title="Sprint" /* i18n-ok: termine uguale in inglese */>
                              {t("lavoro.mywork.sprintLink", { key: it.sprint })}
                            </Link>
                          )}
                          {it.due && <span className="tabular">{t("lavoro.mywork.dueBy", { date: date(it.due) })}</span>}
                          <span>{tn.name(it.type, it.type_name)}</span>
                          {workTypes.isWork(it.type) && it.category !== "done" && (
                            <button className="inline-flex min-h-6 min-w-6 items-center justify-center px-1 text-accent underline" onClick={() => setLogFor(it)}>
                              {t("lavoro.mywork.hours")}
                            </button>
                          )}
                        </span>
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </section>
          ))}
        {mine.data && open === 0 && <p className="m-0 text-muted">{t("lavoro.mywork.noOpen")}</p>}
      </div>
      <BulkBar ids={[...selection.sel]} onClear={selection.clear} />
      {logFor && (
        <Dialog title={t("lavoro.mywork.hoursOn", { key: logFor.key })} onClose={() => setLogFor(null)}>
          <TimeLogForm refKey={logFor.key} compact />
        </Dialog>
      )}
    </>
  );
}
