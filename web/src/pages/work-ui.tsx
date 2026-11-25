// Pezzi comuni del work management: scelta dello spazio, segno del tipo, priorità, persona,
// finestra di dialogo (foglio dal basso sul telefono), registrazione rapida delle ore.
import { Select } from "../select";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ErrorNote } from "../ui";
import { COLOR_DOT } from "../fields";
import { useT } from "../i18n/react";
import { loc } from "../i18n/format";
import { useTypeNames } from "../typesApi";
import { TYPE_MARK, fmtMinutes, lastSpace, rememberSpace, workApi, type Option } from "../workApi";

/** Spazi dei team e quello scelto (dalla query, poi l'ultimo usato, poi il primo). */
export function useSpaces(wanted?: string) {
  const spaces = useQuery({ queryKey: ["work", "spaces"], queryFn: workApi.spaces, staleTime: 60_000 });
  const items = spaces.data?.items ?? [];
  const fallback = lastSpace();
  const current =
    items.find((s) => s.key === wanted)?.key ?? items.find((s) => s.key === fallback)?.key ?? items.find((s) => s.active_sprint)?.key ?? items[0]?.key ?? undefined;
  useEffect(() => {
    if (current) rememberSpace(current);
  }, [current]);
  return { spaces, items, current };
}

export function SpacePicker({ value, items, onChange }: { value?: string; items: { key: string; team: string }[]; onChange: (k: string) => void }) {
  const t = useT();
  return (
    <label className="flex items-center gap-2">
      <span className="label text-muted">{t("lavoro.workui.team")}</span>
      <Select className="field w-auto py-1.5" value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
        {items.map((s) => (
          <option key={s.key} value={s.key}>
            {s.team} · {s.key}
          </option>
        ))}
      </Select>
    </label>
  );
}

/** Segno del tipo: lettera in un quadratino (epic e bug marcati). */
export function TypeMark({ type }: { type: string }) {
  const tn = useTypeNames();
  const base = TYPE_MARK[type] ?? { letter: type.slice(0, 1).toUpperCase(), name: type };
  const m = { ...base, name: tn.name(type, base.name) };
  const tone = type === "bug" ? "border-danger text-danger" : type === "epic" ? "border-fg bg-fg text-bg" : "border-rule text-fg";
  return (
    <span title={m.name} aria-label={m.name} className={`inline-flex h-[16px] min-w-[16px] shrink-0 items-center justify-center border px-[2px] font-mono text-[10px] leading-none font-semibold ${tone}`}>
      {m.letter}
    </span>
  );
}

export function PriorityDot({ opt, label = false }: { opt?: Option; label?: boolean }) {
  const t = useT();
  if (!opt) return null;
  return (
    <span className="inline-flex min-w-0 items-center gap-1 text-[11.5px]" title={t("lavoro.workui.priorityTitle", { name: opt.name })}>
      <span className={`size-2 shrink-0 ${COLOR_DOT[opt.color] ?? "bg-hair"}`} aria-hidden="true" />
      {label ? <span className="truncate">{opt.name}</span> : <span className="sr-only">{t("lavoro.workui.priority", { name: opt.name })}</span>}
    </span>
  );
}

export function Person({ name, size = 20 }: { name?: string; size?: number }) {
  const t = useT();
  if (!name) return null; // senza assegnatario non si ripete un testo su ogni scheda
  const ini = name
    .split(/\s+/)
    .slice(0, 2)
    .map((p) => p[0])
    .join("")
    .toUpperCase();
  return (
    <span
      title={name}
      aria-label={t("lavoro.workui.assignedTo", { name })}
      style={{ width: size, height: size }}
      className="inline-flex shrink-0 items-center justify-center rounded-full bg-accent text-[9.5px] font-bold text-accent-fg"
    >
      {ini}
    </span>
  );
}

/** Finestra modale: al centro su desktop, foglio dal basso sotto i 768 px. Esc chiude. */
export function Dialog({ title, onClose, children, wide }: { title: string; onClose: () => void; children: ReactNode; wide?: boolean }) {
  const t = useT();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    ref.current?.querySelector<HTMLElement>("input, select, textarea, button")?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      prev?.focus?.();
    };
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-[60] flex items-end justify-center bg-fg/30 md:items-center" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={`flex max-h-[85dvh] w-full flex-col gap-3 overflow-y-auto border-t-2 border-rule bg-bg p-4 md:border-2 ${wide ? "md:w-[560px]" : "md:w-[420px]"}`}
      >
        <div className="flex items-baseline justify-between gap-3">
          <h2 className="m-0 text-[17px] font-bold">{title}</h2>
          <button type="button" className="text-[12px] text-muted underline hover:text-fg" onClick={onClose}>
            {t("lavoro.workui.close")}
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

const today = () => {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
};

/** Registrazione rapida delle ore: durata ("1h 30m", "2", "1g"), data, nota. */
export function TimeLogForm({ refKey, onDone, compact }: { refKey: string; onDone?: () => void; compact?: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const [duration, setDuration] = useState("");
  const [day, setDay] = useState(today());
  const [text, setText] = useState("");
  const log = useMutation({
    mutationFn: () => workApi.logTime(refKey, { duration, date: day, text }),
    onSuccess: (r) => {
      setDuration("");
      setText("");
      qc.setQueryData(["work", "time", refKey], r.time);
      qc.invalidateQueries({ queryKey: ["activity"] });
      onDone?.();
    },
  });
  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        log.mutate();
      }}
    >
      <div className={`grid gap-2 ${compact ? "grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)]" : "grid-cols-[minmax(0,1fr)_minmax(0,1fr)]"}`}>
        <label className="flex min-w-0 flex-col gap-1">
          <span className="label text-muted">{t("lavoro.workui.time")}</span>
          <input
            className="field font-mono"
            required
            inputMode="text"
            placeholder="1h 30m"
            aria-describedby={`aiuto-ore-${refKey}`}
            value={duration}
            onChange={(e) => setDuration(e.target.value)}
          />
        </label>
        <label className="flex min-w-0 flex-col gap-1">
          <span className="label text-muted">{t("lavoro.workui.day")}</span>
          <input className="field" type="date" required max={today()} value={day} onChange={(e) => setDay(e.target.value)} />
        </label>
      </div>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{t("lavoro.workui.noteOptional")}</span>
        <input className="field" maxLength={1000} value={text} onChange={(e) => setText(e.target.value)} />
      </label>
      <span id={`aiuto-ore-${refKey}`} className="text-[12px] text-muted">
        {t("lavoro.workui.timeHelp")}
      </span>
      <ErrorNote error={log.error} />
      <span className="flex items-center gap-3">
        <button className="btn btn-primary" disabled={log.isPending || !duration.trim()}>
          {log.isPending ? t("lavoro.workui.logging") : t("lavoro.workui.log")}
        </button>
        {log.isSuccess && <span className="text-[12.5px] text-ok">{t("lavoro.workui.logged", { time: fmtMinutes(log.data.time.entries[0]?.minutes ?? 0) })}</span>}
      </span>
    </form>
  );
}

/** Barra di avanzamento sottile (percentuale 0-100). */
export function ProgressBar({ percent, label }: { percent: number; label: string }) {
  const p = Math.max(0, Math.min(100, percent));
  return (
    <span className="flex items-center gap-2" aria-label={label}>
      <span className="relative block h-1.5 flex-1 bg-hair" aria-hidden="true">
        <span className={`absolute inset-y-0 left-0 ${p >= 100 ? "bg-ok" : "bg-accent"}`} style={{ width: `${p}%` }} />
      </span>
      <span className="w-12 text-right font-mono text-[11.5px] tabular">{p.toLocaleString(loc(), { maximumFractionDigits: 1 })}%</span>
    </span>
  );
}

/** Chip «Solo i miei»: aggiunge o toglie `assegnatario: me` dalla query del filtro. */
const MINE = "assegnatario: me";
export function MineChip({ q, onChange }: { q?: string; onChange: (q: string | undefined) => void }) {
  const t = useT();
  const on = (q ?? "").includes(MINE);
  return (
    <button
      type="button"
      aria-pressed={on}
      className={`touch-44 border px-3 py-1 text-[12.5px] font-semibold ${on ? "border-fg bg-fg text-bg" : "border-rule text-fg hover:bg-sel/60"}`}
      onClick={() => {
        const rest = (q ?? "").replace(MINE, "").replace(/\s+/g, " ").trim();
        onChange(on ? rest || undefined : `${rest} ${MINE}`.trim());
      }}
    >
      {t("lavoro.workui.mine")}
    </button>
  );
}

/** Banner per chi apre una pagina di lavoro senza poterla modificare. */
export function ReadOnlyBanner({ what }: { what: string }) {
  const t = useT();
  return (
    <p role="note" className="mx-4 mb-3 mt-0 border-l-[3px] border-muted bg-sel/40 px-3 py-2 text-[12.5px] md:mx-7">
      <span className="font-semibold">{t("lavoro.workui.readOnly")}</span> {what} {t("lavoro.workui.readOnlyHelp")}
    </p>
  );
}
