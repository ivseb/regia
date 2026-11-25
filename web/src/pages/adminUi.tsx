// Pezzi comuni delle pagine di amministrazione (persone, webhook, audit): segreti e link
// da copiare, conferme in due tempi, etichette di stato. Solo classi e variabili del tema.
import { useState, type ReactNode } from "react";
import { ErrorNote } from "../ui";
import { useT } from "../i18n/react";
import { fmtDateTime } from "../i18n/format";

/** Valore da copiare una volta sola (link monouso, segreto, codici di recupero). */
export function CopyBox({ title, value, note, onDone }: { title: string; value: string; note?: ReactNode; onDone?: () => void }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }
  return (
    <div role="status" className="flex flex-col gap-2 border-l-2 border-accent bg-sel px-4 py-3">
      <span className="font-semibold">{title}</span>
      <code className="block select-all break-all whitespace-pre-wrap font-mono text-[12.5px]">{value}</code>
      {note && <span className="text-[12.5px] text-muted">{note}</span>}
      <div className="flex flex-wrap gap-2">
        <button type="button" className="btn btn-primary py-1.5" onClick={copy}>{copied ? t("common.copied") : t("common.copy")}</button>
        {onDone && <button type="button" className="btn py-1.5" onClick={onDone}>{t("common.done")}</button>}
      </div>
    </div>
  );
}

/** Pulsante che chiede conferma prima di agire (azioni che non si annullano). */
export function ConfirmButton({
  label,
  confirm,
  onConfirm,
  pending,
  danger,
  disabled,
  title,
}: {
  label: string;
  confirm: string;
  onConfirm: () => void;
  pending?: boolean;
  danger?: boolean;
  disabled?: boolean;
  title?: string;
}) {
  const t = useT();
  const [ask, setAsk] = useState(false);
  if (!ask) {
    return (
      <button type="button" className={`btn py-1.5 ${danger ? "btn-danger" : ""}`} disabled={disabled} title={title} onClick={() => setAsk(true)}>
        {label}
      </button>
    );
  }
  return (
    <span className="inline-flex flex-wrap items-center gap-2">
      <button
        type="button"
        className={`btn py-1.5 ${danger ? "btn-danger" : "btn-primary"}`}
        disabled={pending}
        onClick={() => {
          onConfirm();
          setAsk(false);
        }}
      >
        {confirm}
      </button>
      <button type="button" className="btn py-1.5" onClick={() => setAsk(false)}>{t("common.cancel")}</button>
    </span>
  );
}

/** Etichetta piccola con bordo (stato, tipo, segnale). */
export function Tag({ children, tone = "muted", title }: { children: ReactNode; tone?: "muted" | "accent" | "ok" | "flag" | "danger" | "fg"; title?: string }) {
  const cls = {
    muted: "border-hair text-muted",
    accent: "border-accent text-accent",
    ok: "border-ok text-ok",
    flag: "border-flag text-flag",
    danger: "border-danger text-danger",
    fg: "border-rule text-fg",
  }[tone];
  return (
    <span title={title} className={`inline-flex items-center whitespace-nowrap border px-1 text-[11px] leading-[16px] font-semibold ${cls}`}>
      {children}
    </span>
  );
}

/** Pannello laterale di dettaglio: a destra sul desktop, sotto l'elenco sul telefono. */
export function SidePanel({ label, children, onClose }: { label: string; children: ReactNode; onClose: () => void }) {
  const t = useT();
  return (
    <aside aria-label={label} className="flex min-w-0 flex-[1_1_400px] flex-col gap-5 border-t border-rule px-4 py-5 md:border-t-0 md:border-l md:px-6">
      <div className="flex justify-end md:hidden">
        <button type="button" className="text-[12px] text-muted underline" onClick={onClose}>{t("common.close")}</button>
      </div>
      {children}
    </aside>
  );
}

/** Riga etichetta/valore nei pannelli. */
export function KV({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-0.5 border-b border-hair py-2 sm:grid-cols-[150px_minmax(0,1fr)] sm:gap-3">
      <dt className="text-muted">{label}</dt>
      <dd className="m-0 min-w-0 break-words">{children}</dd>
    </div>
  );
}

/** Errore di una mutazione accanto ai pulsanti. */
export function MutError({ errors }: { errors: unknown[] }) {
  const e = errors.find(Boolean);
  return e ? <ErrorNote error={e} /> : null;
}

export function dateTime(iso: string | null | undefined): string {
  return fmtDateTime(iso);
}
