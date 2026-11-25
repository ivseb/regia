// Pezzi comuni degli editor visuali di scorecard e catalogo dei requisiti: problemi del server
// accanto alla riga giusta, riquadro «Avanzato» con il JSON, spostamento delle righe.
import { useEffect, useState } from "react";
import type { Problem } from "../../api";
import { useT } from "../../i18n/react";

/** Problemi il cui `path` è `prefix` o sta sotto (`prefix.campo`, `prefix[3]`). */
export function problemsUnder(ps: Problem[], prefix: string): Problem[] {
  return ps.filter((p) => p.path === prefix || p.path.startsWith(prefix + ".") || p.path.startsWith(prefix + "["));
}

/** Problemi che non stanno sotto nessuna delle righe mostrate (il nome, i ruoli, «serve almeno un criterio»…). */
export function problemsOutside(ps: Problem[], rowPrefixes: RegExp): Problem[] {
  return ps.filter((p) => !rowPrefixes.test(p.path));
}

/** Elenco dei problemi di una riga: il campo (in coda al percorso) e il messaggio. */
export function RowProblems({ problems, prefix }: { problems: Problem[]; prefix: string }) {
  const mine = problemsUnder(problems, prefix);
  if (!mine.length) return null;
  return (
    <ul className="m-0 flex list-none flex-col gap-0.5 border-l-2 border-danger p-0 pl-2.5 text-[12.5px] text-danger" data-testid="riga-problemi">
      {mine.map((p, i) => (
        <li key={i}>
          <span className="font-mono">{p.path.slice(prefix.length).replace(/^\./, "") || "·"}</span> {p.message}
        </li>
      ))}
    </ul>
  );
}

export function move<T>(list: T[], from: number, to: number): T[] {
  if (to < 0 || to >= list.length) return list;
  const out = [...list];
  out.splice(to, 0, out.splice(from, 1)[0]);
  return out;
}

export function replaceAt<T>(list: T[], i: number, patch: Partial<T> | ((x: T) => T)): T[] {
  return list.map((x, a) => (a === i ? (typeof patch === "function" ? patch(x) : { ...x, ...patch }) : x));
}

/** Sposta su/giù e toglie una riga. */
/** Senza `onMove` (elenco filtrato) restano solo i pulsanti per togliere. */
export function RowTools({ i, count, onMove, onRemove, name }: { i: number; count: number; onMove?: (to: number) => void; onRemove: () => void; name: string }) {
  const t = useT();
  return (
    <span className="flex shrink-0 items-center gap-1">
      {onMove && (
        <>
          <button type="button" className="btn px-2 py-0.5" disabled={i === 0} aria-label={t("configurazione.editorCommon.moveUp", { name })} title={t("configurazione.editorCommon.moveUp", { name })} onClick={() => onMove(i - 1)}>
        ↑
          </button>
          <button type="button" className="btn px-2 py-0.5" disabled={i === count - 1} aria-label={t("configurazione.editorCommon.moveDown", { name })} title={t("configurazione.editorCommon.moveDown", { name })} onClick={() => onMove(i + 1)}>
        ↓
          </button>
        </>
      )}
      <button type="button" className="btn btn-danger px-2 py-0.5" aria-label={t("configurazione.editorCommon.remove", { name })} title={t("configurazione.editorCommon.remove", { name })} onClick={onRemove}>
        ×
      </button>
    </span>
  );
}

/**
 * Riquadro «Avanzato»: la definizione in JSON, chiuso di default. Le modifiche scritte qui si
 * applicano al modulo con il pulsante; il testo si riallinea da solo quando il modulo cambia.
 */
export function AdvancedJson<T>({ value, onApply, label }: { value: T; onApply: (v: T) => void; label: string }) {
  const t = useT();
  const shown = JSON.stringify(value, null, 2);
  const [text, setText] = useState(shown);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!dirty) setText(shown);
  }, [shown, dirty]);
  return (
    <details className="border border-hair px-3 py-2" data-testid="avanzato-json">
      <summary className="cursor-pointer text-[12.5px] font-semibold">{t("configurazione.editorCommon.advanced")}</summary>
      <div className="mt-2 flex flex-col gap-2">
        <p className="m-0 text-[12.5px] text-muted">{t("configurazione.editorCommon.advancedHelp")}</p>
        <textarea
          className="field min-h-[260px] font-mono text-[12px] leading-relaxed"
          spellCheck={false}
          aria-label={label}
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setDirty(true);
            setError("");
          }}
        />
        {error && <p className="m-0 text-[12.5px] text-danger">{error}</p>}
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            className="btn"
            disabled={!dirty}
            onClick={() => {
              try {
                onApply(JSON.parse(text) as T);
                setDirty(false);
                setError("");
              } catch (e) {
                setError(t("configurazione.editorCommon.invalidJson", { error: (e as Error).message }));
              }
            }}
          >
            {t("configurazione.editorCommon.applyJson")}
          </button>
          <button
            type="button"
            className="btn"
            disabled={!dirty}
            onClick={() => {
              setDirty(false);
              setError("");
            }}
          >
            {t("configurazione.editorCommon.discardJson")}
          </button>
        </div>
      </div>
    </details>
  );
}
