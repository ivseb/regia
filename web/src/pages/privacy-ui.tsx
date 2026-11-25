// Pezzi comuni della Vista DPO: etichette dei dati, stato della DPIA, campi di modulo e righe d'elenco.
// Telefono di primo livello: bersagli da 44 px, niente scorrimento orizzontale.
import type { ReactNode } from "react";
import { useT } from "../i18n/react";
import type { AppRef, DpiaStatus, Issue } from "../privacyApi";

/** Un testo con voci separate da virgole o a capo → vettore senza vuoti. */
export const splitList = (s: string): string[] => s.split(/[,\n;]/).map((x) => x.trim()).filter(Boolean);

/** Tipi di dato dichiarati nel catalogo per un'applicazione: personali, credenziali, finanziari. */
export function DataBadges({ app }: { app: Pick<AppRef, "personal" | "credentials" | "financial"> }) {
  const t = useT();
  const kinds = [
    app.personal && t("governance.privacy.data.personal"),
    app.credentials && t("governance.privacy.data.credentials"),
    app.financial && t("governance.privacy.data.financial"),
  ].filter(Boolean) as string[];
  if (kinds.length === 0) return null;
  return (
    <>
      {kinds.map((k) => (
        <span key={k} className="label border border-rule px-1.5 py-0.5 text-[10.5px]">
          {k}
        </span>
      ))}
    </>
  );
}

export function DpiaBadge({ status }: { status: DpiaStatus }) {
  const t = useT();
  const cls = status === "approved" ? "border-rule" : status === "in_review" ? "border-flag text-flag" : "border-rule text-muted";
  return <span className={`label border px-1.5 py-0.5 text-[10.5px] ${cls}`}>{t(`governance.privacy.dpia.status.${status}`)}</span>;
}

export function IssueBadge({ issue }: { issue: Issue }) {
  const t = useT();
  return <span className="label border border-flag px-1.5 py-0.5 text-[10.5px] text-flag">{t(`governance.privacy.issue.${issue}`)}</span>;
}

export function TField({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return (
    <label className="flex min-w-0 flex-col gap-1">
      <span className="label text-muted">{label}</span>
      {children}
      {hint && <span className="text-[12px] text-muted">{hint}</span>}
    </label>
  );
}

export function Check({ checked, onChange, children, disabled }: { checked: boolean; onChange: (v: boolean) => void; children: ReactNode; disabled?: boolean }) {
  return (
    <label className="flex min-h-[44px] items-center gap-3 md:min-h-[28px]">
      <input type="checkbox" className="h-5 w-5 shrink-0 md:h-4 md:w-4" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span className="text-[14px]">{children}</span>
    </label>
  );
}

/** Titolo di sezione della pagina, con un numero opzionale accanto. */
export function Block({ title, count, children, id }: { title: string; count?: number; children: ReactNode; id?: string }) {
  return (
    <section id={id} aria-label={title} className="flex min-w-0 flex-col gap-2">
      <h2 className="m-0 flex items-baseline gap-2 border-b border-hair pb-1 text-[15px] font-bold">
        {title}
        {count !== undefined && <span className="font-mono text-[12px] font-normal text-muted tabular">{count}</span>}
      </h2>
      {children}
    </section>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="m-0 border-l-4 border-rule px-3 py-2 text-[13px] text-muted">{children}</p>;
}
