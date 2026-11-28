// Icone e colori dei moduli (replica/mockup/interfaccia-colori.html). Le icone sono le stesse del
// sito (site/build.mjs, costante ICONS): tratto 2 px, angoli vivi. Il colore lo dà la classe m-*
// (styles.css), che imposta --mc; le icone usano currentColor.
import type { ReactNode } from "react";
import { moduleOfPath } from "./modules";
import { initials, personTint } from "./tinte";

/** Moduli con un colore proprio (token --m-* in themes.css). */
export type UiModule = "demand" | "work" | "budget" | "knowledge" | "devsecops" | "grc" | "arch" | "platform";

/** Icone disponibili: una per modulo più quelle delle sezioni personali. */
export type IconKey = UiModule | "star" | "list" | "settings";

const PATHS: Record<IconKey, ReactNode> = {
  demand: <path d="M3 4h18l-7 8.5V19l-4 2v-8.5z" />,
  work: <path d="M3 3h5v18H3zM10 3h5v11h-5zM17 3h4v7h-4z" />,
  budget: (
    <>
      <path d="M11 4a8 8 0 1 0 8 8h-8z" />
      <path d="M14 2v7h7a7 7 0 0 0-7-7z" />
    </>
  ),
  knowledge: <path d="M3 4h6a3 3 0 0 1 3 3v14a2.5 2.5 0 0 0-2.5-2.5H3zM21 4h-6a3 3 0 0 0-3 3v14a2.5 2.5 0 0 1 2.5-2.5H21z" />,
  devsecops: (
    <>
      <path d="M12 2.5l8 3v6c0 5-3.4 8.6-8 10-4.6-1.4-8-5-8-10v-6z" />
      <path d="M8.5 12l2.5 2.5 4.5-5" />
    </>
  ),
  grc: (
    <>
      <circle cx="12" cy="12" r="9" />
      <circle cx="12" cy="12" r="4.5" />
      <path d="M12 12h.01" />
    </>
  ),
  arch: (
    <>
      <path d="M4 4h5v5H4zM15 4h5v5h-5zM9.5 15h5v5h-5z" />
      <path d="M6.5 9v3h11V9M12 12v3" />
    </>
  ),
  platform: (
    <>
      <path d="M12 3l9 4.5-9 4.5-9-4.5z" />
      <path d="M3 12l9 4.5 9-4.5M3 16.5l9 4.5 9-4.5" />
    </>
  ),
  star: <path d="M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1L3.2 9.5l6.1-.9z" />,
  list: <path d="M4 5h3v3H4zM10 6.5h10M4 10.5h3v3H4zM10 12h10M4 16h3v3H4zM10 17.5h10" />,
  settings: (
    <>
      <path d="M4 7h10M18 7h2M4 17h2M10 17h10" />
      <path d="M14 4.5h4v5h-4zM6 14.5h4v5H6z" />
    </>
  ),
};

/** Icona sola, nel colore del testo intorno (currentColor). */
export function ModuleIcon({ icon, className = "size-4" }: { icon: IconKey; className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} aria-hidden="true" focusable="false">
      <g fill="none" stroke="currentColor" strokeWidth="2" strokeLinejoin="miter" strokeLinecap="square">
        {PATHS[icon]}
      </g>
    </svg>
  );
}

/** Quadratino nel colore del modulo con l'icona in --chip-ink (barra laterale, percorso, riquadri). */
export function ModuleChip({ module, icon, size = "md", className = "" }: { module?: UiModule; icon?: IconKey; size?: "sm" | "md"; className?: string }) {
  return (
    <span className={`ichip ${size === "sm" ? "ichip-sm" : ""} ${module ? `m-${module}` : ""} ${className}`} aria-hidden="true">
      <ModuleIcon icon={icon ?? module ?? "platform"} className="" />
    </span>
  );
}

/** Modulo (per il colore) della pagina a questo percorso; le pagine della piattaforma sono «platform». */
export function uiModuleOfPath(path: string): UiModule {
  const under = (b: string) => path === b || path.startsWith(b + "/");
  if (under("/finanza") || under("/budget") || under("/da-fare") || under("/notifiche")) return "budget";
  const m = moduleOfPath(path);
  if (!m) return "platform";
  // I progetti hanno il colore del lavoro: il piano del PM sta sopra le board dei team.
  // La continuità sta dentro Architettura e ne prende il colore.
  return m === "architecture" || m === "continuity" ? "arch" : m === "projects" ? "work" : m;
}

/** Icona del percorso: come la sezione del menu (stella per le pagine personali, elenco, impostazioni). */
export function uiIconOfPath(path: string): IconKey {
  const under = (b: string) => path === b || path.startsWith(b + "/");
  if (under("/da-fare") || under("/notifiche") || under("/profilo")) return "star";
  if (under("/elenco")) return "list";
  if (under("/configurazione") || under("/processi")) return "settings";
  return uiModuleOfPath(path);
}

/** Avatar tondo con le iniziali, nel colore stabile della persona (tinte.ts). */
export function Avatar({ name, className = "" }: { name: string; className?: string }) {
  return (
    <span className={`avatar m-${personTint(name)} ${className}`} title={name} aria-hidden="true">
      {initials(name)}
    </span>
  );
}
