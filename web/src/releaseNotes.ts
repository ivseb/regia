// Note di rilascio in Markdown, generate dagli elementi di un rilascio e raggruppate in novità,
// correzioni e altro (pagina /rilasci/$id). Modulo puro: la lingua arriva con t.
import type { NoteGroup, ReleaseDetail } from "./rilasciApi";

export const NOTE_GROUPS: NoteGroup[] = ["new", "fix", "other"];

type Tr = (key: string, params?: Record<string, string | number>) => string;

export function releaseNotesMarkdown(d: ReleaseDetail, t: Tr, day: (iso: string) => string): string {
  const r = d.release;
  const lines = [`# ${r.service} ${r.version}`, "", t("rilasci.note.meta", { ambiente: r.environment, data: day(r.at) })];
  if (r.change) lines.push("", t("rilasci.note.change", { change: r.change.number }));
  for (const g of NOTE_GROUPS) {
    const items = d.items.filter((i) => i.group === g);
    if (items.length === 0) continue;
    lines.push("", `## ${t(`rilasci.gruppo.${g}`)}`, "");
    for (const i of items) lines.push(`- ${i.title.replace(/\s+/g, " ").trim()} (${i.key})`);
  }
  if (d.items.length === 0) lines.push("", t("rilasci.note.vuote"));
  return lines.join("\n") + "\n";
}

/** Nome del file .md: servizio e versione, senza caratteri strani. */
export function notesFileName(service: string, version: string, suffix = ""): string {
  return `note-${service}-${version}${suffix}.md`.replace(/[^\w.-]+/g, "-");
}
