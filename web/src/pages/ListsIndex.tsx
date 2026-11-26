// Indice «Elenchi» (menu snello, nav.ts): una voce sola nel menu al posto dei sette elenchi con
// filtri e viste salvate (impatti, applicazioni, asset, rischi, incidenti, controlli, documenti).
// Compaiono gli elenchi che il menu completo raggiunge (permessi e moduli accesi), ognuno con le sue viste.
import { Link } from "@tanstack/react-router";
import { useQueries } from "@tanstack/react-query";
import { PageHeader, useMe } from "../shell";
import { navPages } from "../nav";
import { LISTS } from "../views/lists";
import { viewsApi } from "../views/viewsApi";
import { useTypeNames } from "../typesApi";
import { useT } from "../i18n/react";

const ORDER = ["impatti", "applicazioni", "asset", "rischi", "incidenti", "controlli", "documenti"];

export function ListsIndex() {
  const t = useT();
  const me = useMe();
  const tn = useTypeNames();
  const reachable = new Set((me.data ? navPages(me.data, {}, tn) : []).map((it) => it.to));
  const lists = ORDER.map((k) => LISTS[k]).filter((c) => reachable.has(c.href));
  const views = useQueries({ queries: lists.map((c) => ({ queryKey: ["views", c.page], queryFn: () => viewsApi.list(c.page), staleTime: 30_000 })) });
  return (
    <>
      <PageHeader path={t("shell.menu.lists.path")} title={t("shell.menu.lists.title")} />
      <div className="flex flex-col gap-6 px-4 pb-12 md:px-7">
        <p className="m-0 max-w-3xl text-muted">{t("shell.menu.lists.intro")}</p>
        <ul className="m-0 grid list-none grid-cols-1 gap-x-8 gap-y-6 p-0 md:grid-cols-2 xl:grid-cols-3" data-testid="elenchi">
          {lists.map((c, i) => {
            const name = tn.plural(c.typeKey, c.title);
            const saved = views[i]?.data?.items ?? [];
            return (
              <li key={c.page} className="flex flex-col border-t-2 border-rule pt-2">
                <Link to={c.href} className="flex min-h-11 items-center text-[18px] font-bold text-fg no-underline hover:text-accent" aria-label={`${t("shell.menu.lists.open")}: ${name}`}>
                  {name}
                </Link>
                <span className="label pt-1 text-muted">{t("shell.menu.lists.views")}</span>
                {views[i]?.isSuccess && saved.length === 0 && <p className="m-0 py-1 text-[12.5px] text-muted">{t("shell.menu.lists.noViews")}</p>}
                {saved.length > 0 && (
                  <ul className="m-0 flex list-none flex-col p-0">
                    {saved.map((v) => (
                      <li key={v.id} className="border-b border-hair">
                        <Link to={c.href} search={{ v: v.id } as never} className="flex min-h-11 items-center gap-2 py-1.5 text-fg no-underline hover:text-accent">
                          <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{v.name}</span>
                          {v.favorite && <span className="font-mono text-[11px] text-muted">★ {t("shell.menu.lists.favorite")}</span>}
                        </Link>
                      </li>
                    ))}
                  </ul>
                )}
              </li>
            );
          })}
        </ul>
      </div>
    </>
  );
}
