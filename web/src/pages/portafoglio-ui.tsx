// Schede delle voci di menu che riuniscono più pagine (menu snello, nav.ts): «Portafoglio» con
// Graduatoria, Cabina e Governance; «Finanza e budget» con Finanza e Budget; «DevSecOps» con Panoramica,
// Aderenza, Metriche e Vulnerabilità; «Rischi e conformità» con Rischi, Conformità, Privacy e lo Spazio
// dell'auditor. Le schede sono le stesse
// pagine del menu completo (navPages), con gli stessi permessi e moduli: una pagina che il menu non
// raggiunge non ha la sua scheda. Con una sola pagina raggiungibile le schede non compaiono.
import { Link, useRouterState } from "@tanstack/react-router";
import { DEVSECOPS_TABS, GRC_TABS, navPages, type NavItem } from "../nav";
import { useMe } from "../shell";
import { useTypeNames } from "../typesApi";
import { useT } from "../i18n/react";

const under = (path: string, base: string) => path === base || path.startsWith(base + "/");

/** `labels`: il nome della scheda quando non è quello della voce del menu completo (Panoramica). */
function PageTabs({ paths, label, labels = {} }: { paths: string[]; label: string; labels?: Record<string, string> }) {
  const me = useMe();
  const tn = useTypeNames();
  const path = useRouterState({ select: (s) => s.location.pathname });
  const pages = me.data ? navPages(me.data, {}, tn) : [];
  const tabs = paths.map((p) => pages.find((it) => it.to === p)).filter((it): it is NavItem => !!it);
  if (tabs.length < 2) return null;
  // La scheda attiva è quella col percorso più lungo che contiene la pagina (/devsecops contiene /devsecops/metriche).
  const current = tabs.filter((it) => under(path, it.to!)).sort((a, b) => b.to!.length - a.to!.length)[0];
  return (
    <nav aria-label={label} className="flex gap-5 overflow-x-auto border-b border-hair px-4 md:px-7">
      {tabs.map((it) => {
        const active = it === current;
        return (
          <Link
            key={it.to}
            to={it.to!}
            aria-current={active ? "page" : undefined}
            // Il Link marcherebbe da solo /devsecops attiva anche su /devsecops/metriche: decide `current`.
            activeOptions={{ exact: true, includeSearch: false }}
            className={`touch-44 inline-flex shrink-0 items-center pb-2 no-underline ${active ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
          >
            {labels[it.to!] ?? it.label}
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

/** Schede di DevSecOps: Panoramica, Aderenza della pipeline, Metriche di consegna, Vulnerabilità. */
export function DevsecopsTabs() {
  const t = useT();
  return <PageTabs paths={DEVSECOPS_TABS} label={t("shell.nav.devsecops")} labels={{ "/devsecops": t("shell.menu.overview") }} />;
}

/** Schede di Rischi e conformità, secondo i permessi: Rischi e incidenti, Conformità, Privacy, Spazio dell'auditor. */
export function GrcTabs() {
  const t = useT();
  return <PageTabs paths={GRC_TABS} label={t("shell.colori.section.grc")} />;
}
