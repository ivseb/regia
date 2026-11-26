// Celle colorate delle liste (replica/mockup/interfaccia-colori.html): fase, punteggio, persona, unità, scadenza.
// Le tinte servono per fondi e quadratini; il testo resta nel colore normale.
import type { State } from "./api";
import { useT } from "./i18n/react";
import { Avatar } from "./moduleIcon";
import { phaseTint, unitTint } from "./tinte";
import { mainPath } from "./ui";
import { useStateName } from "./stateLabels";

/** Chip di fase: tinta per fase e, se si conoscono le fasi del tipo, i quadratini di avanzamento. */
export function FaseChip({ states, current }: { states?: State[] | null; current: string }) {
  const t = useT();
  const stateName = useStateName();
  const path = mainPath(states);
  const idx = path.findIndex((s) => s.name === current);
  const known = path.length > 0;
  const offPath = known && idx < 0;
  const tint = offPath ? "grc" : phaseTint(current, idx, idx >= 0 ? path[idx].category : states?.find((s) => s.name === current)?.category);
  const label = stateName(current, (idx >= 0 ? path[idx] : states?.find((s) => s.name === current))?.labels);
  return (
    <span role="group" className={`chip-fase m-${tint}`} aria-label={t("common.phaseLabel", { name: label })}>
      {label}
      {known && (
        <span className="gates" aria-hidden="true">
          {path.map((s, i) => (
            <i key={s.id} className={!offPath && i <= idx ? "d" : undefined} />
          ))}
        </span>
      )}
    </span>
  );
}

/** Punteggio 0-100: mini-barra e numero; verde da 60 in su, altrimenti budget. */
export function Punteggio({ value, text }: { value: number; text: string }) {
  const w = Math.max(0, Math.min(100, value));
  return (
    <span className="inline-flex items-center gap-[7px]">
      <span className="mini-barra" aria-hidden="true">
        <i style={{ width: `${w}%`, ["--sc" as string]: value >= 60 ? "var(--m-devsecops)" : "var(--m-budget)" }} />
      </span>
      <span className="tabular">{text}</span>
    </span>
  );
}

export function Persona({ name }: { name: string }) {
  return (
    <span className="inline-flex items-center gap-2">
      <Avatar name={name} />
      <span>{name}</span>
    </span>
  );
}

export function Unita({ name }: { name: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <i aria-hidden="true" className="inline-block h-2 w-2 shrink-0" style={{ background: `var(--m-${unitTint(name)})` }} />
      {name}
    </span>
  );
}

/** Icona orologio per la scadenza normativa. */
export function IconaOrologio() {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="2" className="mr-1 inline-block align-[-1px]">
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </svg>
  );
}
