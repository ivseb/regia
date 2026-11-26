import { useEffect, useState } from "react";
import { addDays, diffDays, fmt, isWorkday, monday, monthName, today } from "../calendar";
import { useT } from "../i18n/react";

// Pezzi comuni di Gantt e roadmap: scala, intestazione, giorni non lavorativi, linee.

export type Zoom = "giorni" | "settimane" | "mesi" | "trimestri";
export const PX_PER_DAY: Record<Zoom, number> = { giorni: 30, settimane: 13, mesi: 4.5, trimestri: 1.6 };

export function useNarrow(): boolean {
  const q = "(max-width: 767px)";
  const [narrow, setNarrow] = useState(() => window.matchMedia(q).matches);
  useEffect(() => {
    const m = window.matchMedia(q);
    const on = () => setNarrow(m.matches);
    m.addEventListener("change", on);
    return () => m.removeEventListener("change", on);
  }, []);
  return narrow;
}

export function ZoomSwitch({ value, options, onChange }: { value: Zoom; options: Zoom[]; onChange: (z: Zoom) => void }) {
  const t = useT();
  return (
    <div role="radiogroup" aria-label={t("lavoro.timeline.scale")} className="flex border border-rule text-[12px]">
      {options.map((z) => (
        <button
          key={z}
          role="radio"
          aria-checked={value === z}
          className={`px-2.5 py-1.5 capitalize ${value === z ? "bg-fg text-bg" : "text-muted hover:text-fg"}`}
          onClick={() => onChange(z)}
        >
          {t(`lavoro.timeline.zoom.${z}`)}
        </button>
      ))}
    </div>
  );
}

/** Intestazione a due righe: mesi (o anni) sopra, giorni o settimane sotto. */
export function TimeHeader({ from, days, px, zoom, label }: { from: number; days: number; px: number; zoom: Zoom; label: number }) {
  const top: { x: number; w: number; text: string }[] = [];
  const bottom: { x: number; w: number; text: string; weekend?: boolean }[] = [];
  const to = addDays(from, days);
  // Riga alta: mesi (anni in scala trimestri).
  for (let t = from; t < to; ) {
    const d = new Date(t);
    const next = zoom === "trimestri" ? Date.UTC(d.getUTCFullYear() + 1, 0, 1) : Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1);
    const end = Math.min(next, to);
    top.push({
      x: diffDays(from, t) * px,
      w: diffDays(t, end) * px,
      text: zoom === "trimestri" ? String(d.getUTCFullYear()) : `${monthName(t, true)} ${d.getUTCFullYear()}`,
    });
    t = end;
  }
  // Riga bassa.
  if (zoom === "giorni") {
    for (let t = from; t < to; t = addDays(t, 1)) {
      bottom.push({ x: diffDays(from, t) * px, w: px, text: String(new Date(t).getUTCDate()), weekend: !isWorkday(t) });
    }
  } else if (zoom === "settimane") {
    for (let t = monday(from); t < to; t = addDays(t, 7)) {
      bottom.push({ x: diffDays(from, t) * px, w: 7 * px, text: String(new Date(t).getUTCDate()) });
    }
  } else {
    for (let t = from; t < to; ) {
      const d = new Date(t);
      const step = zoom === "mesi" ? 1 : 3;
      const m0 = zoom === "mesi" ? d.getUTCMonth() : d.getUTCMonth() - (d.getUTCMonth() % 3);
      const next = Date.UTC(d.getUTCFullYear(), m0 + step, 1);
      const end = Math.min(next, to);
      bottom.push({
        x: diffDays(from, t) * px,
        w: diffDays(t, end) * px,
        text: zoom === "mesi" ? monthName(t) : `T${Math.floor(m0 / 3) + 1}`,
      });
      t = end;
    }
  }
  return (
    <div className="sticky top-0 z-20 flex border-b-2 border-rule bg-bg">
      <div className="sticky left-0 z-30 shrink-0 border-r border-hair bg-bg" style={{ width: label }} />
      <div className="relative h-[46px] shrink-0" style={{ width: days * px }}>
        {top.map((m) => (
          <div
            key={m.x}
            title={m.text}
            className="absolute top-0 h-[22px] overflow-hidden border-l border-hair pl-1.5 text-[11.5px] font-semibold whitespace-nowrap"
            style={{ left: m.x, width: m.w }}
          >
            {m.w > 40 ? m.text : ""}
          </div>
        ))}
        {bottom.map((b) => (
          <div
            key={b.x}
            className={`absolute top-[22px] h-6 overflow-hidden border-l border-hair text-center font-mono text-[10.5px] leading-6 ${b.weekend ? "text-muted/60" : "text-muted"}`}
            style={{ left: b.x, width: b.w }}
          >
            {b.w >= 14 ? b.text : ""}
          </div>
        ))}
      </div>
    </div>
  );
}

/** Colonne dei giorni non lavorativi (weekend e festività), solo nelle scale fini. */
export function NonWorkdays({ from, days, px, height }: { from: number; days: number; px: number; height: number }) {
  if (px < 8) return null;
  const cols: number[] = [];
  for (let i = 0; i < days; i++) if (!isWorkday(addDays(from, i))) cols.push(i);
  return (
    <>
      {cols.map((i) => (
        <div key={i} aria-hidden="true" className="absolute top-0 bg-hair/45" style={{ left: i * px, width: px, height }} />
      ))}
    </>
  );
}

export type Marker = { at: string; label: string; tone: "accent" | "flag" | "muted" };

/** Linee verticali: oggi, scadenze normative, date desiderate. */
export function Markers({ from, px, height, markers }: { from: number; px: number; height: number; markers: Marker[] }) {
  const t = useT();
  const all: Marker[] = [{ at: fmt(today()), label: t("lavoro.timeline.today"), tone: "accent" }, ...markers];
  return (
    <>
      {all.map((m) => {
        const x = diffDays(from, Date.parse(m.at + "T00:00:00Z")) * px + px / 2;
        const color = m.tone === "flag" ? "var(--flag)" : m.tone === "accent" ? "var(--accent)" : "var(--muted)";
        return (
          <div key={m.label + m.at} aria-hidden="true" className="pointer-events-none absolute top-0 z-10" style={{ left: x, height }}>
            <div className="h-full border-l-[1.5px]" style={{ borderColor: color, borderStyle: m.tone === "muted" ? "dashed" : "solid" }} />
            <span
              className="absolute top-1 left-1 bg-bg px-1 font-mono text-[10.5px] font-semibold whitespace-nowrap"
              style={{ color }}
            >
              {m.label}
            </span>
          </div>
        );
      })}
    </>
  );
}
