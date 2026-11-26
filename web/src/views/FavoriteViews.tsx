// Preferiti nel menu laterale (piano, passo 16): le viste salvate che l'utente ha messo in
// evidenza, ognuna con il numero dei risultati (si aggiorna con gli eventi in tempo reale).
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { viewsApi } from "./viewsApi";
import { listHref } from "./lists";
import { useT } from "../i18n/react";

export function FavoriteViews({ enabled }: { enabled: boolean }) {
  const t = useT();
  const favs = useQuery({ queryKey: ["views", "favorites"], queryFn: viewsApi.favorites, enabled, refetchInterval: 120_000, staleTime: 15_000 });
  const items = favs.data?.items ?? [];
  if (items.length === 0) return null;
  return (
    <div className="flex flex-col gap-0.5">
      <div className="flex items-center gap-2 pb-1.5">
        <span className="text-[13px] leading-none text-flag" aria-hidden="true">★</span>
        <span className="text-[12px] font-semibold">{t("altro.views.mine")}</span>
      </div>
      {items.map((f) => (
        <Link
          key={f.id}
          to={listHref(f.page)}
          search={{ v: f.id } as never}
          className="flex items-center gap-2 py-1 pl-4 text-fg no-underline hover:text-accent"
          activeOptions={{ exact: true, includeSearch: true }}
          activeProps={{ className: "font-semibold !text-accent" }}
        >
          <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{f.name}</span>
          <span className="font-mono text-[11px] text-muted tabular" aria-label={f.count == null ? t("altro.views.counterUnavailable") : t(f.more ? "altro.views.resultsOrMore" : "altro.views.results", { count: f.count })}>
            {f.count == null ? "—" : `${f.count}${f.more ? "+" : ""}`}
          </span>
        </Link>
      ))}
    </div>
  );
}
