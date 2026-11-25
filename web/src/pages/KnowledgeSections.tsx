import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { kb } from "../knowledgeApi";
import { ErrorNote } from "../ui";
import { useT } from "../i18n/react";

// «Documenti collegati» nel dettaglio di ogni elemento (piano, passo 19): le pagine che lo
// documentano (relazione «documenta») o lo citano nel testo, che chi guarda può leggere.

export function LinkedDocuments({ refKey, open }: { refKey: string; open: boolean }) {
  const t = useT();
  const docs = useQuery({ queryKey: ["kb", "documents", refKey], queryFn: () => kb.documents(refKey), staleTime: 30_000 });
  const items = docs.data?.items ?? [];
  if (docs.data && items.length === 0) return null;
  return (
    <details open={open} className="group flex flex-col">
      <summary className="flex cursor-pointer list-none items-baseline justify-between border-b-2 border-rule pb-1.5 [&::-webkit-details-marker]:hidden">
        <span className="label m-0">
          <span aria-hidden="true" className="mr-1.5 inline-block w-2 text-muted group-open:rotate-90">›</span>
          {t("knowledge.sections.title")}
        </span>
        <span className="font-mono text-[11.5px] text-muted tabular">{items.length || ""}</span>
      </summary>
      <div className="flex flex-col gap-1 pt-2">
        <ErrorNote error={docs.error} />
        {items.map((d) => (
          <Link key={d.key} to="/documenti/$key" params={{ key: d.key }} className="flex min-w-0 flex-col py-0.5 text-[13px] text-fg no-underline hover:text-accent">
            <span className="min-w-0 truncate">{d.title}</span>
            <span className="text-[11.5px] text-muted">
              <span className="font-mono">{d.key}</span> · {d.space_name} · {d.via === "link" ? t("knowledge.sections.documents") : t("knowledge.sections.cited")}
              {d.controlled && d.state ? ` · ${d.state}${d.approved_version ? ` (${t("knowledge.sections.inForce", { version: d.approved_version })})` : ""}` : ""}
            </span>
          </Link>
        ))}
      </div>
    </details>
  );
}
