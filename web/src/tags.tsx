// Etichette (tag) in forma di chip compatti per liste, tabelle e card (stile Atlante: riquadro
// a spigoli vivi, testo piccolo). Oltre `max` chip compare «+N» con l'elenco nel titolo.
// Con `onPick` il chip è un pulsante che filtra la lista per quell'etichetta.
import { COLOR_DOT } from "./fields";
import { useT } from "./i18n/react";
import type { Tag } from "./api";

export function TagChips({ tags, max = 3, onPick, className = "" }: { tags?: Tag[]; max?: number; onPick?: (name: string) => void; className?: string }) {
  const t = useT();
  if (!tags || tags.length === 0) return null;
  const shown = tags.slice(0, max);
  const rest = tags.slice(max);
  const chip = "inline-flex min-w-0 max-w-[110px] items-center gap-1 border border-hair px-1.5 text-[11px] leading-[17px] text-muted";
  return (
    <span className={`inline-flex min-w-0 max-w-full flex-wrap items-center gap-1 ${className}`}>
      {shown.map((tg) => {
        const inner = (
          <>
            {tg.color && COLOR_DOT[tg.color] && <span className={`size-1.5 shrink-0 ${COLOR_DOT[tg.color]}`} aria-hidden="true" />}
            <span className="truncate">{tg.name}</span>
          </>
        );
        return onPick ? (
          <button
            key={tg.id}
            type="button"
            data-tag={tg.name}
            title={t("lavoro.etichette.filter", { name: tg.name })}
            aria-label={t("lavoro.etichette.filter", { name: tg.name })}
            className={`${chip} cursor-pointer bg-transparent hover:border-accent hover:text-accent`}
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              onPick(tg.name);
            }}
            draggable={false}
          >
            {inner}
          </button>
        ) : (
          <span key={tg.id} data-tag={tg.name} title={t("lavoro.etichette.plain", { name: tg.name })} className={chip}>
            {inner}
          </span>
        );
      })}
      {rest.length > 0 && (
        <span data-tag-more className={`${chip} font-mono tabular`} title={t("lavoro.etichette.more", { count: rest.length, names: rest.map((x) => x.name).join(", ") })}>
          +{rest.length}
        </span>
      )}
    </span>
  );
}
