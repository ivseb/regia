// Assistente alla creazione (replica/ai.md §2 punto 2, ondata 3): duplicati probabili e
// classificazione proposta per l'elemento che la persona sta scrivendo. La ricerca testuale trova i
// candidati, il modello li rilegge; la persona accetta ogni proposta campo per campo e sceglie gli
// asset da citare dopo il salvataggio. Senza AI accesa il form non cambia.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { aiApi, type TriageField, type TriageFormField, type TriageResult } from "../aiApi";
import { ErrorNote } from "../ui";
import { useT } from "../i18n/react";

export function CreateAssistant({
  type,
  title,
  description,
  onAccept,
  links = [],
  onLinks,
  fields: formFields,
}: {
  type: string;
  title: string;
  description: string;
  /** Elenchi propri del form (non campi del motore) per cui proporre un valore, con onAccept. */
  fields?: TriageFormField[];
  /** Senza: i valori proposti non si mostrano (il form non ha campi a elenco da riempire). */
  onAccept?: (f: TriageField) => void;
  links?: string[];
  /** Senza: gli asset da citare non si mostrano (solo le richieste citano asset alla creazione). */
  onLinks?: (keys: string[]) => void;
}) {
  const t = useT();
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["ai-status"], queryFn: aiApi.status, staleTime: 60_000, retry: false });
  const [accepted, setAccepted] = useState<string[]>([]);
  const [res, setRes] = useState<TriageResult | null>(null);
  const run = useMutation({
    mutationFn: () => aiApi.triage(type, title, description, formFields),
    onMutate: () => {
      setRes(null);
      setAccepted([]);
    },
    onSuccess: (r) => {
      qc.setQueryData(["ai-status"], r.status);
      setRes(r);
    },
  });
  const st = status.data;
  if (!st?.enabled || !st.features?.classificazione) return null;
  const blocked = st.budget.state === "blocked";
  const fields = onAccept ? (res?.fields ?? []) : [];
  const assets = onLinks ? (res?.links ?? []) : [];
  const empty = res && res.duplicates.length + fields.length + assets.length === 0;
  return (
    <section aria-labelledby="assistente-creazione" className="flex min-w-0 flex-col gap-3 border border-hair bg-side p-3 md:col-span-2" data-testid="assistente-creazione">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="assistente-creazione" className="label m-0">
          {t("ai.triage.title")}
        </h2>
        <button type="button" className="btn touch-44" disabled={blocked || run.isPending || title.trim().length < 3} onClick={() => run.mutate()}>
          {run.isPending ? t("ai.card.working") : onAccept ? t("ai.triage.run") : t("ai.triage.runDuplicates")}
        </button>
      </div>
      <p className="m-0 text-[12.5px] text-muted">{blocked
          ? t("ai.panel.budgetBlocked")
          : !onAccept
            ? t("ai.triage.helpDuplicates")
            : type === "business_request"
              ? t("ai.triage.help")
              : t("ai.triage.helpOther")}</p>
      <ErrorNote error={run.error} />
      <div aria-live="polite" className="flex min-w-0 flex-col gap-3">
        {empty && <p className="m-0" role="status">{t("ai.triage.none", { count: res.candidates })}</p>}
        {res && res.duplicates.length > 0 && (
          <div className="flex flex-col gap-1">
            <span className="label">{t("ai.triage.duplicates")}</span>
            <ul className="m-0 flex list-none flex-col p-0" aria-label={t("ai.triage.duplicates")} data-testid="duplicati">
              {res.duplicates.map((d) => (
                <li key={d.id} className="flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1.5">
                  <a href={`/e/${encodeURIComponent(d.key)}`} target="_blank" rel="noopener" className="touch-44 inline-flex items-center font-mono font-semibold">
                    {d.key}
                  </a>
                  <span className="min-w-0 flex-1 basis-48 [overflow-wrap:anywhere]">
                    {d.title} <span className="text-muted">· {d.state}</span>
                  </span>
                  <span className="basis-full text-[12.5px] text-muted [overflow-wrap:anywhere]">{d.reason}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
        {fields.length > 0 && (
          <div className="flex flex-col gap-1">
            <span className="label">{t("ai.triage.fields")}</span>
            <ul className="m-0 flex list-none flex-col p-0" aria-label={t("ai.triage.fields")} data-testid="campi-proposti">
              {fields.map((f) => {
                const on = accepted.includes(f.key);
                return (
                  <li key={f.key} className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair py-1.5">
                    <span className="min-w-0 flex-1 basis-48 [overflow-wrap:anywhere]">
                      <span className="font-semibold">{f.name}:</span> {f.display}
                      <span className="block text-[12.5px] text-muted">{f.reason}</span>
                    </span>
                    {on ? (
                      <span className="text-[12.5px] text-ok" role="status">
                        {t("ai.triage.accepted")}
                      </span>
                    ) : (
                      <button
                        type="button"
                        className="btn touch-44"
                        aria-label={t("ai.triage.acceptField", { name: f.name, value: f.display })}
                        onClick={() => {
                          onAccept?.(f);
                          setAccepted((a) => [...a, f.key]);
                        }}
                      >
                        {t("ai.triage.accept")}
                      </button>
                    )}
                  </li>
                );
              })}
            </ul>
          </div>
        )}
        {assets.length > 0 && (
          <fieldset className="m-0 flex flex-col gap-1 border-0 p-0" data-testid="asset-proposti">
            <legend className="label mb-1 p-0">{t("ai.triage.links")}</legend>
            {assets.map((l) => (
              <label key={l.id} className="touch-44 flex items-center gap-2">
                <input
                  type="checkbox"
                  className="size-4 shrink-0 accent-[var(--accent)]"
                  checked={links.includes(l.key)}
                  onChange={(e) => onLinks?.(e.target.checked ? [...links, l.key] : links.filter((k) => k !== l.key))}
                />
                <span className="min-w-0 [overflow-wrap:anywhere]">
                  <span className="font-mono">{l.key}</span> {l.title} <span className="text-[12.5px] text-muted">· {l.reason}</span>
                </span>
              </label>
            ))}
            <span className="text-[12px] text-muted">{t("ai.triage.linksHelp")}</span>
          </fieldset>
        )}
        {res && (res.unverified ?? 0) > 0 && <p className="m-0 text-[12.5px] text-muted">{t("ai.card.unverified", { count: res.unverified ?? 0 })}</p>}
      </div>
    </section>
  );
}
