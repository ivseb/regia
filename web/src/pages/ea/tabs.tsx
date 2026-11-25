// Schede dell'architettura enterprise accanto a quelle del catalogo (Architecture.tsx).
import type { ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { PageHeader } from "../../shell";
import { useT } from "../../i18n/react";
import type { Key } from "../../i18n/core";
import { crumbs } from "../../crumbs";

export type EaTab = "mappa" | "capability" | "dipendenze" | "razionalizzazione" | "diagrammi";

export const EA_TABS: { id: EaTab; label: Key; to: string }[] = [
  { id: "mappa", label: "altro.ea.tabs.map", to: "/architettura/mappa" },
  { id: "capability", label: "altro.ea.tabs.capability", to: "/architettura/capability" },
  { id: "dipendenze", label: "altro.ea.tabs.dependencies", to: "/architettura/dipendenze" },
  { id: "razionalizzazione", label: "altro.ea.tabs.rationalization", to: "/architettura/razionalizzazione" },
  { id: "diagrammi", label: "altro.ea.tabs.diagrams", to: "/architettura/diagrammi" },
];

const tabClass = (on: boolean) => `shrink-0 pb-2.5 whitespace-nowrap no-underline ${on ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`;

/** Le schede EA, da mettere nella barra delle schede del catalogo. */
export function EaTabLinks({ active }: { active?: EaTab }) {
  const t = useT();
  return (
    <>
      {EA_TABS.map((tab) => (
        <Link key={tab.id} role="tab" aria-selected={active === tab.id} to={tab.to} className={tabClass(active === tab.id)}>
          {t(tab.label)}
        </Link>
      ))}
    </>
  );
}

const CATALOG: { id: string; label: Key }[] = [
  { id: "asset", label: "altro.ea.tabs.assets" },
  { id: "applicazioni", label: "altro.ea.tabs.applications" },
  { id: "team", label: "altro.ea.tabs.teams" },
];

/** Intestazione e schede delle pagine dell'architettura enterprise. */
export function EaPage({ active, title, actions, children, wide }: { active: EaTab; title?: string; actions?: ReactNode; children: ReactNode; wide?: boolean }) {
  const t = useT();
  return (
    <>
      <PageHeader path={`${crumbs("governance", "architecture")} / ${active}`} title={title ?? t("altro.ea.tabs.title")} actions={actions} />
      <div role="tablist" aria-label={t("altro.ea.tabs.section")} className="flex gap-6 overflow-x-auto border-b border-hair px-4 md:px-7">
        {CATALOG.map((c) => (
          <Link key={c.id} role="tab" aria-selected={false} to="/architettura" search={{ tab: c.id as "asset" }} className={tabClass(false)}>
            {t(c.label)}
          </Link>
        ))}
        <EaTabLinks active={active} />
      </div>
      <div className={wide ? "flex min-w-0 flex-col" : "flex min-w-0 flex-col gap-8 px-4 py-6 md:px-7"}>{children}</div>
    </>
  );
}
