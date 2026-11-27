// «In arrivo dai progetti» (replica/progetti.md §6 «Team», passo 5): nel backlog del team, il lavoro che i
// progetti hanno pianificato su di lui nelle prossime settimane, prima che diventi lavoro sulla board. Work
// package con il suo team (quelli ancora da accettare sono «proposti») e le attività di quei work package;
// il work package agile già accettato porta all'epic sulla board. Le voci che la persona non legge si contano.
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { projectKeys, projectsApi, type IncomingItem } from "../../projectsApi";
import { useT } from "../../i18n/react";
import { useTypeNames } from "../../typesApi";
import { Select } from "../../select";
import { ErrorNote, date } from "../../ui";
import { ItemLink } from "./shared";

const WEEKS = [4, 8, 12];

export function IncomingWork({ spaceKey }: { spaceKey: string }) {
  const t = useT();
  const tn = useTypeNames();
  const [weeks, setWeeks] = useState(8);
  const q = useQuery({ queryKey: projectKeys.incoming(spaceKey, weeks), queryFn: () => projectsApi.incoming({ space: spaceKey, weeks }), retry: false });
  // Uno spazio che non è di un team non ha niente in arrivo: il pannello non compare.
  if (q.data && q.data.teams.length === 0) return null;
  const items = q.data?.items ?? [];
  const proposed = items.filter((i) => i.proposed).length;
  return (
    <section className="flex flex-col gap-2" aria-label={t("progetti.passo5.incoming.title")} data-testid="in-arrivo">
      <div className="flex flex-wrap items-baseline justify-between gap-2 border-b-2 border-rule pb-1.5">
        <h2 className="m-0 text-[15px] font-bold">{t("progetti.passo5.incoming.title")}</h2>
        <label className="flex items-center gap-1.5 text-[12px]">
          <span className="text-muted">{t("progetti.passo5.incoming.window")}</span>
          <Select className="field w-auto py-0.5 text-[12px]" value={String(weeks)} onChange={(e) => setWeeks(Number(e.target.value))}>
            {WEEKS.map((w) => (
              <option key={w} value={w}>
                {t("progetti.passo5.incoming.weeks", { count: w })}
              </option>
            ))}
          </Select>
        </label>
      </div>
      <p className="m-0 text-[12px] text-muted">
        {t("progetti.passo5.incoming.help", { name: tn.lower("work_package", t("progetti.passo5.incoming.wp")) })}
      </p>
      <ErrorNote error={q.error} />
      {q.data && items.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("progetti.passo5.incoming.none")}</p>}
      {proposed > 0 && <p className="m-0 text-[12.5px] font-semibold">{t("progetti.passo5.incoming.proposed", { count: proposed })}</p>}
      {items.length > 0 && (
        <ul className="m-0 flex list-none flex-col p-0">
          {items.map((it) => (
            <Row key={it.id} it={it} />
          ))}
        </ul>
      )}
      {q.data && q.data.restricted > 0 && <p className="m-0 text-[12px] text-muted">{t("progetti.passo5.incoming.restricted", { count: q.data.restricted })}</p>}
    </section>
  );
}

function Row({ it }: { it: IncomingItem }) {
  const t = useT();
  return (
    <li className="flex flex-col gap-0.5 border-b border-hair py-2 text-[13px]" data-kind={it.kind}>
      <div className="flex flex-wrap items-baseline gap-x-2">
        <ItemLink itemKey={it.key} title={it.title} />
        {it.proposed && <span className="text-[12px] font-semibold text-accent">{t("progetti.passo5.incoming.toAccept")}</span>}
      </div>
      <span className="text-[12px] text-muted">
        <span className="tabular">
          {date(it.start)} → {date(it.end)}
        </span>
        {it.estimate_effort_days != null && ` · ${t("progetti.passo5.incoming.effort", { count: it.estimate_effort_days })}`}
        {it.delivery_mode && ` · ${t(`progetti.common.mode.${it.delivery_mode}`)}`}
      </span>
      <span className="text-[12px] text-muted">
        {t("progetti.passo5.incoming.project")} <ItemLink itemKey={it.project.key} title={it.project.title || undefined} />
        {it.phase && ` · ${it.phase}`}
        {it.work_package && (
          <>
            {" · "}
            <ItemLink itemKey={it.work_package.key} title={it.work_package.title || undefined} />
          </>
        )}
      </span>
      {it.epics.length > 0 && (
        <span className="text-[12px]">
          {t("progetti.passo5.incoming.onBoard")} {it.epics.map((e) => <ItemLink key={e.id} itemKey={e.key} title={e.title} className="mr-2" />)}
        </span>
      )}
    </li>
  );
}
