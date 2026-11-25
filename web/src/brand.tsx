// Marchio di Regia: il logo (quadrato con la R; la gamba blu è il passo che avanza) e il link al
// codice sorgente della versione in esecuzione (AGPL-3.0 §13, docs/licenza.md), letto da
// GET /api/v1/health, che risponde anche senza sessione.
import { useQuery } from "@tanstack/react-query";
import { call } from "./api";
import { useT } from "./i18n/react";

/** Il blu della gamba resta fisso nei due temi: sul quadrato chiaro e su quello scuro ha contrasto. */
const LEG = "#2b3ff2";

export function Logo({ size = 24, inverse = false }: { size?: number; inverse?: boolean }) {
  return (
    <svg viewBox="0 0 32 32" width={size} height={size} aria-hidden="true" focusable="false" className="shrink-0">
      <rect width="32" height="32" className={inverse ? "fill-bg" : "fill-fg"} />
      <path className={inverse ? "fill-fg" : "fill-bg"} fillRule="evenodd" d="M8 6h14v13h-10v7h-4zM12 10v5h6v-5z" />
      <path fill={LEG} d="M14 19h5l5 7h-5z" />
    </svg>
  );
}

export type Health = { status: string; version: string; commit?: string; source?: string };

export const healthQuery = { queryKey: ["health"], queryFn: () => call<Health>("GET", "/health"), staleTime: Infinity };

/** «Regia <versione> · Codice sorgente»: il link porta al commit in esecuzione sul repository. */
export function SourceLink({ className = "" }: { className?: string }) {
  const t = useT();
  const h = useQuery(healthQuery);
  if (!h.data) return null;
  return (
    <span className={`font-mono text-[11.5px] ${className}`}>
      Regia {h.data.version}
      {h.data.source && (
        <>
          {" · "}
          <a href={h.data.source} target="_blank" rel="noreferrer noopener" title={t("marchio.codiceSorgenteTitolo")} className="text-inherit underline">
            {t("marchio.codiceSorgente")}
          </a>
        </>
      )}
    </span>
  );
}
