import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { EntitySelect } from "../select";
import { kb, type LinkedElement, type PageDetail } from "../knowledgeApi";
import { ErrorNote, SectionTitle } from "../ui";
import { useT } from "../i18n/react";
import type { Key } from "../i18n/core";

// «Questo documento è evidenza di…»: gli elementi che la pagina documenta (relazione «documenta»
// del grafo, links.go), raggruppati per ciò che interessa a chi risponde a un audit: controlli,
// applicazioni, rischi, il resto. «Collega» aggiunge un collegamento da un menu con ricerca;
// «Scollega» lo toglie. Non scrive nulla nei moduli di conformità e dei rischi: il legame è
// della pagina, quei moduli lo leggono dal grafo (i «Documenti collegati» dell'elemento).

const GROUPS = [
  { id: "controls", types: ["control"] },
  { id: "applications", types: ["application"] },
  { id: "risks", types: ["risk"] },
] as const;

function groupOf(l: LinkedElement): string {
  return GROUPS.find((g) => (g.types as readonly string[]).includes(l.type))?.id ?? "others";
}

export function EvidencePanel({ d }: { d: PageDetail }) {
  const t = useT();
  const qc = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [pick, setPick] = useState("");
  const done = (r: PageDetail) => qc.setQueryData(["kb", "page", d.page.key], r);
  const link = useMutation({
    mutationFn: (element: string) => kb.link(d.page.key, element),
    onSuccess: (r) => {
      done(r);
      setAdding(false);
      setPick("");
    },
  });
  const unlink = useMutation({ mutationFn: (key: string) => kb.unlink(d.page.key, key), onSuccess: done });
  if (d.links.length === 0 && !d.rights.write) return null;
  const groups = [...GROUPS.map((g) => g.id), "others" as const]
    .map((id) => ({ id, items: d.links.filter((l) => groupOf(l) === id) }))
    .filter((g) => g.items.length > 0);
  return (
    <section data-testid="evidence-panel" className="flex flex-col gap-2" aria-label={t("knowledge.evidence.title")}>
      <SectionTitle
        aside={
          d.rights.write && (
            <button type="button" className="text-[12px] text-accent underline max-md:min-h-11" aria-expanded={adding} onClick={() => setAdding((a) => !a)}>
              {t("knowledge.evidence.link")}
            </button>
          )
        }
      >
        {t("knowledge.evidence.title")}
      </SectionTitle>
      {adding && (
        <div className="flex max-w-[560px] flex-col gap-1">
          <span className="label">{t("knowledge.evidence.field")}</span>
          <EntitySelect
            kind="element"
            aria-label={t("knowledge.evidence.field")}
            value={pick}
            placeholder={t("knowledge.evidence.placeholder")}
            filter={(o) => !d.links.some((l) => l.id === o.value) && o.value !== d.page.id}
            onChange={(id) => {
              setPick(id);
              if (id) link.mutate(id);
            }}
          />
        </div>
      )}
      {groups.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("knowledge.evidence.empty")}</p>}
      {groups.map((g) => (
        <div key={g.id} className="flex flex-col gap-0.5">
          <span className="label text-muted">{t(`knowledge.evidence.group.${g.id}` as Key)}</span>
          <ul className="m-0 flex list-none flex-col p-0">
            {g.items.map((l) => (
              <li key={l.id} className="flex items-baseline gap-2 py-1 max-md:min-h-11 max-md:items-center">
                <Link to="/e/$key" params={{ key: l.key }} className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2 text-fg no-underline hover:text-accent">
                  <span className="shrink-0 font-mono text-[12px] text-accent">{l.key}</span>
                  <span className="min-w-0 truncate">{l.title}</span>
                  <span className="shrink-0 text-[11.5px] text-muted">{l.type_name}{l.state ? ` · ${l.state}` : ""}</span>
                </Link>
                {d.rights.write && (
                  <button type="button" aria-label={t("knowledge.evidence.removeOf", { key: l.key })} className="text-[12px] text-muted underline hover:text-danger max-md:min-h-11 max-md:px-2" onClick={() => unlink.mutate(l.key)}>
                    {t("knowledge.evidence.remove")}
                  </button>
                )}
              </li>
            ))}
          </ul>
        </div>
      ))}
      <ErrorNote error={link.error ?? unlink.error} />
    </section>
  );
}
