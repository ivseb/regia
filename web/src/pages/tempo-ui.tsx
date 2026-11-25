// Pezzi comuni delle pagine del tempo: schede di navigazione e barra di avanzamento sulla capacità.
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { tempoApi } from "../tempoApi";
import { useT } from "../i18n/react";
import type { Key } from "../i18n/core";

const TABS: [string, Key, string][] = [
  ["/tempo", "lavoro.tempoui.timesheet", "timesheet"],
  ["/tempo/report", "lavoro.tempoui.report", "report"],
  ["/tempo/team", "lavoro.tempoui.team", "team"],
];

/** Schede del tempo: unica navigazione tra Timesheet, Report e Team; il contatore dice che cosa conta. */
export function TempoTabs({ active }: { active: "timesheet" | "report" | "team" }) {
  const t = useT();
  const pending = useQuery({ queryKey: ["tempo", "pending"], queryFn: tempoApi.pending, staleTime: 60_000 });
  const toApprove = pending.data?.items.length ?? 0;
  return (
    <nav aria-label={t("lavoro.tempoui.nav")} className="flex gap-5 overflow-x-auto border-b border-hair px-4 md:px-7">
      {TABS.map(([to, label, key]) => (
        <Link
          key={to}
          to={to}
          aria-current={active === key ? "page" : undefined}
          className={`touch-44 inline-flex shrink-0 items-center gap-2 pb-2 no-underline ${active === key ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
        >
          {t(label)}
          {key === "timesheet" && toApprove > 0 && (
            <span className="border border-flag px-1.5 text-[11.5px] font-semibold text-flag tabular">{t("lavoro.tempoui.toApprove", { count: toApprove })}</span>
          )}
        </Link>
      ))}
    </nav>
  );
}

/** Barra del registrato rispetto alla capacità: oltre il 100% è arancio. */
export function LoadBar({ minutes, capacity, label }: { minutes: number; capacity: number; label: string }) {
  const pct = capacity > 0 ? (minutes / capacity) * 100 : 0;
  return (
    <span className="flex items-center gap-2" aria-label={label}>
      <span className="relative block h-1.5 min-w-[48px] flex-1 bg-hair" aria-hidden="true">
        <span className={`absolute inset-y-0 left-0 ${pct > 100 ? "bg-flag" : "bg-accent"}`} style={{ width: `${Math.min(pct, 100)}%` }} />
      </span>
      <span className={`w-10 text-right font-mono text-[11.5px] tabular ${pct > 100 ? "text-flag" : "text-muted"}`}>{capacity > 0 ? `${Math.round(pct)}%` : "—"}</span>
    </span>
  );
}
