// Guide allegate a un processo o a una sua fase: un documento, un link o una pagina Knowledge,
// che chi lavora sulla richiesta trova nel percorso (migrazione 0069, docs/sviluppo/versioni-vigore.md).
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Guide, type ProcessInfo } from "../api";
import { Select } from "../select";
import { ErrorNote, SectionTitle } from "../ui";
import { useT } from "../i18n/react";

export function guideHref(g: { kind: string; target: string }): string {
  return g.kind === "knowledge" ? `/documenti/${encodeURIComponent(g.target)}` : g.target;
}

export function ProcessGuides({ proc }: { proc: ProcessInfo }) {
  const t = useT();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["process", proc.key, "guides"], queryFn: () => api.guides(proc.key) });
  const [phase, setPhase] = useState("");
  const [kind, setKind] = useState<Guide["kind"]>("link");
  const isKb = kind === "knowledge";
  const [title, setTitle] = useState("");
  const [target, setTarget] = useState("");
  const phases = (proc.draft ?? proc.current)?.phases ?? [];
  const refresh = () => qc.invalidateQueries({ queryKey: ["process", proc.key, "guides"] });
  const add = useMutation({
    mutationFn: () => api.addGuide(proc.key, { phase: phase || null, kind, title: title.trim(), target: target.trim() }),
    onSuccess: () => {
      setTitle("");
      setTarget("");
      refresh();
    },
  });
  const remove = useMutation({ mutationFn: (id: string) => api.removeGuide(proc.key, id), onSuccess: refresh });
  const items = q.data?.items ?? [];
  const where = (g: Guide) => (g.phase ? g.phase : t("configurazione.versioni.guides.wholeProcess"));
  return (
    <section id="guide" className="flex scroll-mt-4 flex-col gap-3" aria-label={t("configurazione.versioni.guides.title")}>
      <SectionTitle>{t("configurazione.versioni.guides.title")}</SectionTitle>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">{t("configurazione.versioni.guides.intro")}</p>
      <ErrorNote error={q.error} />
      {q.data && items.length === 0 && <p className="m-0 text-muted">{t("configurazione.versioni.guides.empty")}</p>}
      {items.length > 0 && (
        <ul className="m-0 flex list-none flex-col border-y border-hair p-0">
          {items.map((g) => (
            <li key={g.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-hair py-2 last:border-b-0">
              <span className="label w-full text-muted md:w-44">{where(g)}</span>
              <span className="text-[12px] text-muted">{t(`configurazione.versioni.guides.kind.${g.kind}`)}</span>
              <a className="min-w-0 flex-1 break-words text-accent" href={guideHref(g)} target={g.kind === "knowledge" ? undefined : "_blank"} rel="noopener noreferrer">
                {g.title}
              </a>
              <button className="text-[12.5px] text-danger underline" disabled={remove.isPending} onClick={() => remove.mutate(g.id)} aria-label={t("configurazione.versioni.guides.removeNamed", { title: g.title })}>
                {t("configurazione.versioni.guides.remove")}
              </button>
            </li>
          ))}
        </ul>
      )}
      <form
        className="grid max-w-3xl gap-3 md:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          add.mutate();
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.versioni.guides.field.phase")}</span>
          <Select className="field" value={phase} onChange={(e) => setPhase(e.target.value)}>
            <option value="">{t("configurazione.versioni.guides.wholeProcess")}</option>
            {phases.map((p) => (
              <option key={p.name} value={p.name}>{p.name}</option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.versioni.guides.field.kind")}</span>
          <Select className="field" value={kind} onChange={(e) => setKind(e.target.value as Guide["kind"])}>
            {(["link", "document", "knowledge"] as const).map((k) => (
              <option key={k} value={k}>{t(`configurazione.versioni.guides.kind.${k}`)}</option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.versioni.guides.field.title")}</span>
          <input className="field" required maxLength={200} value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("configurazione.versioni.guides.field.target")}</span>
          <input
            className="field"
            required
            value={target}
            placeholder={t(isKb ? "configurazione.versioni.guides.targetKnowledge" : "configurazione.versioni.guides.targetUrl")}
            onChange={(e) => setTarget(e.target.value)}
          />
        </label>
        <div className="md:col-span-2">
          <button className="btn btn-primary" type="submit" disabled={add.isPending || !title.trim() || !target.trim()}>
            {t("configurazione.versioni.guides.add")}
          </button>
        </div>
        <div className="md:col-span-2">
          <ErrorNote error={add.error ?? remove.error} />
        </div>
      </form>
    </section>
  );
}
