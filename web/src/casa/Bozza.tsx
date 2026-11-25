// Componente comune «modifica → anteprima → pubblica» (replica/usabilita.md, proposta 7A):
// bozza locale che sopravvive a un cambio di pagina, «n modifiche non salvate», «Cosa cambia»
// (differenze con la versione pubblicata), impatto previsto, Pubblica e Scarta.
import { useEffect, useRef, useState, type ReactNode } from "react";
import { ErrorNote } from "../ui";
import { useT } from "../i18n/react";
import type { Change } from "./differenze";

const KEY = "regia.bozza.";

function read<T>(key: string | undefined): T | null {
  if (!key) return null;
  try {
    const raw = sessionStorage.getItem(KEY + key);
    return raw ? (JSON.parse(raw) as T) : null;
  } catch {
    return null;
  }
}

function write(key: string | undefined, value: unknown) {
  if (!key) return;
  try {
    if (value === null) sessionStorage.removeItem(KEY + key);
    else sessionStorage.setItem(KEY + key, JSON.stringify(value));
  } catch {
    /* memoria del browser non disponibile: la bozza resta in pagina */
  }
}

/**
 * La bozza di una configurazione. Parte dalla versione pubblicata (`saved`); con `storageKey`
 * si ricorda nella sessione del browser finché non si pubblica o si scarta. Quando la versione
 * pubblicata cambia (dopo «Pubblica», o per un altro utente) la bozza riparte da lì.
 */
export function useBozza<T>(saved: T, storageKey?: string) {
  const sig = JSON.stringify(saved);
  const [draft, setDraft] = useState<T>(() => read<T>(storageKey) ?? saved);
  const last = useRef(sig);
  useEffect(() => {
    if (last.current === sig) return;
    last.current = sig;
    setDraft(saved);
    write(storageKey, null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sig]);
  const dirty = JSON.stringify(draft) !== sig;
  useEffect(() => write(storageKey, dirty ? draft : null), [draft, dirty, storageKey]);
  return {
    draft,
    setDraft,
    patch: (p: Partial<T>) => setDraft((d) => ({ ...d, ...p })),
    dirty,
    discard: () => {
      setDraft(saved);
      write(storageKey, null);
    },
  };
}

type BarProps = {
  changes: Change[];
  /** Impatto previsto: quante persone, elementi o regole cambiano di effetto. */
  impact?: ReactNode;
  onPublish: () => void;
  onDiscard: () => void;
  pending?: boolean;
  error?: unknown;
  /** Pubblicare non è possibile (dati mancanti o non validi). */
  blocked?: boolean;
  /** Testo del pulsante; «Pubblica» se manca. */
  publishLabel?: string;
  /** Subito dopo la pubblicazione: «Pubblicato.» */
  published?: boolean;
  /** Informazioni sulla versione pubblicata («v3, pubblicata da…»). */
  version?: ReactNode;
  testId?: string;
};

export function BozzaBar({ changes, impact, onPublish, onDiscard, pending, error, blocked, publishLabel, published, version, testId = "bozza" }: BarProps) {
  const t = useT();
  const n = changes.length;
  return (
    <section className="flex min-w-0 flex-col gap-2 border border-hair border-l-2 border-l-accent bg-sel/40 p-3" aria-label={t("casa.bozza.region")} data-testid={testId}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className="text-[13px] font-semibold" aria-live="polite">
          {n > 0 ? t("casa.bozza.unsaved", { count: n }) : t("casa.bozza.clean")}
        </span>
        {version && <span className="text-[12px] text-muted">{version}</span>}
        <span className="ml-auto flex flex-wrap items-center gap-2">
          {published && n === 0 && (
            <span role="status" className="text-[12.5px] text-ok">
              {t("casa.bozza.published")}
            </span>
          )}
          <button type="button" className="btn" disabled={n === 0 || pending} onClick={onDiscard}>
            {t("casa.bozza.discard")}
          </button>
          <button type="button" className="btn btn-primary" disabled={n === 0 || pending || blocked} onClick={onPublish}>
            {pending ? t("casa.bozza.publishing") : (publishLabel ?? t("casa.bozza.publish"))}
          </button>
        </span>
      </div>
      {n > 0 && (
        <details open className="text-[12.5px]">
          <summary className="cursor-pointer font-semibold">{t("casa.bozza.whatChanges")}</summary>
          <ul className="m-0 mt-1 flex list-none flex-col gap-1 p-0" data-testid="bozza-differenze">
            {changes.map((c, i) => (
              <li key={i} className="flex flex-wrap gap-x-2">
                <span className="font-medium">{c.label}</span>
                <span className="text-muted line-through decoration-1">{c.before}</span>
                <span aria-hidden="true">→</span>
                <span className="text-fg">{c.after}</span>
              </li>
            ))}
          </ul>
        </details>
      )}
      {n > 0 && impact && (
        <div className="text-[12.5px]" data-testid="bozza-impatto">
          <span className="font-semibold">{t("casa.bozza.impact")} </span>
          {impact}
        </div>
      )}
      <ErrorNote error={error} />
    </section>
  );
}
