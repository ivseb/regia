import { useMemo } from "react";
import type { Definition } from "../../api";
import { layout } from "./model";
import { useT } from "../../i18n/react";

/** Miniatura del diagramma: blocchi e passaggi in piccolo, senza testo né interazione. */
export function Miniatura({ def, className = "" }: { def: Definition; className?: string }) {
  const t = useT();
  const l = useMemo(() => layout({ ...def, transitions: def.transitions ?? [], phases: def.phases ?? [] }), [def]);
  const colors = new Map(def.phases.map((p) => [p.name, p.color]));
  const initial = def.initial.toLowerCase();
  return (
    <svg
      viewBox={`0 0 ${l.width} ${l.height}`}
      preserveAspectRatio="xMinYMid meet"
      role="img"
      aria-label={t("configurazione.mappa.miniatura", { phases: def.phases.map((p) => p.name).join(", ") })}
      className={`block h-auto w-full ${className}`}
    >
      {l.edges.map((e) => (
        <g key={e.id}>
          <path d={e.d} fill="none" className="stroke-muted" strokeWidth={3} strokeDasharray={e.system ? "10 8" : undefined} />
          <polygon points={e.arrow} className="fill-muted" />
        </g>
      ))}
      {l.nodes.map((n) => (
        <g key={n.name} transform={`translate(${n.x},${n.y})`}>
          <rect width={n.w} height={n.h} className={`fill-bg ${n.name.toLowerCase() === initial ? "stroke-fg" : "stroke-rule"}`} strokeWidth={n.name.toLowerCase() === initial ? 4 : 2.5} strokeDasharray={n.pseudo ? "8 6" : undefined} />
          {colors.get(n.name) && !n.pseudo && <rect width={10} height={n.h} fill={colors.get(n.name)} />}
        </g>
      ))}
    </svg>
  );
}
