// Schede delle voci di menu che riuniscono più pagine (menu snello, nav.ts): «Portafoglio» con
// Graduatoria, Cabina e Governance; «Finanza e budget» con Finanza e Budget. Le schede sono le stesse
// pagine del menu completo (navPages), con gli stessi permessi e moduli: una pagina che il menu non
// raggiunge non ha la sua scheda. Con una sola pagina raggiungibile le schede non compaiono.
import { Link, useRouterState } from "@tanstack/react-router";
import { navPages, type NavItem } from "../nav";
import { useMe } from "../shell";
import { useTypeNames } from "../typesApi";
import { useT } from "../i18n/react";

const under = (path: string, base: string) => path === base || path.startsWith(base + "/");

function PageTabs({ paths, label }: { paths: string[]; label: string }) {
  const me = useMe();
  const tn = useTypeNames();
  const path = useRouterState({ select: (s) => s.location.pathname });
  const pages = me.data ? navPages(me.data, {}, tn) : [];
  const tabs = paths.map((p) => pages.find((it) => it.to === p)).filter((it): it is NavItem => !!it);
  if (tabs.length < 2) return null;
  return (
    <nav aria-label={label} className="flex gap-5 overflow-x-auto border-b border-hair px-4 md:px-7">
      {tabs.map((it) => {
        const active = under(path, it.to!);
        return (
          <Link
            key={it.to}
            to={it.to!}
            aria-current={active ? "page" : undefined}
            className={`touch-44 inline-flex shrink-0 items-center pb-2 no-underline ${active ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
          >
            {it.label}
          </Link>
        );
      })}
    </nav>
  );
}

/** Schede del portafoglio: Graduatoria, Cabina di portafoglio, Governance. */
export function PortfolioTabs() {
  const t = useT();
  return <PageTabs paths={["/graduatoria", "/cabina", "/governance"]} label={t("shell.menu.portfolio")} />;
}

/** Schede di finanza e budget: Finanza (con finance.read) e l'albero dei budget. */
export function FinanceTabs() {
  const t = useT();
  return <PageTabs paths={["/finanza", "/budget"]} label={t("shell.menu.financeBudget")} />;
}
