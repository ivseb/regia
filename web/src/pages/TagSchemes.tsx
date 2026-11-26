// Schema dei tag dei rilasci (pilota home lab, difetto 5): dopo la scoperta dei repository
// (GitHub e Gitea), per ogni repository che pubblica più schemi di tag per versione (chart e
// app) si chiede quale è il rilascio vero. La risposta è il tag_filter del profilo
// dell'integrazione per quel repository, così le metriche DORA non contano due rilasci.
import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import type { TagFilterResult, TagQuestion } from "../githubApi";
import { ErrorNote } from "../ui";
import { useT } from "../i18n/react";

export function TagSchemeQuestions({ questions, save, onSaved }: {
  questions: TagQuestion[];
  save: (repositoryId: string, pattern: string) => Promise<TagFilterResult>;
  onSaved: () => void;
}) {
  const t = useT();
  const [done, setDone] = useState<Record<string, TagFilterResult>>({});
  if (questions.length === 0) return null;
  return (
    <section aria-label={t("altro.schemaTag.title")} className="flex flex-col gap-3 border-l-2 border-accent pl-3">
      <div className="flex flex-col gap-1">
        <h3 className="m-0 text-[14px] font-semibold">{t("altro.schemaTag.title")}</h3>
        <p className="m-0 max-w-[720px] text-[13px] text-muted">{t("altro.schemaTag.intro")}</p>
      </div>
      {questions.map((q) =>
        done[q.repository_id] ? (
          <p key={q.repository_id} role="status" className="m-0 text-[13px]">
            <span className="font-mono">{q.full_name}</span>
            {" · "}
            {t(done[q.repository_id].profile_created ? "altro.schemaTag.savedNew" : "altro.schemaTag.saved", {
              profile: done[q.repository_id].profile_name,
              version: done[q.repository_id].profile_version,
            })}
            {done[q.repository_id].pruned > 0 && <> {t("altro.schemaTag.pruned", { count: done[q.repository_id].pruned })}</>}
          </p>
        ) : (
          <Question key={q.repository_id} q={q} save={save} onSaved={(r) => { setDone((d) => ({ ...d, [q.repository_id]: r })); onSaved(); }} />
        ),
      )}
    </section>
  );
}

function Question({ q, save, onSaved }: { q: TagQuestion; save: (repositoryId: string, pattern: string) => Promise<TagFilterResult>; onSaved: (r: TagFilterResult) => void }) {
  const t = useT();
  // Proposta: lo schema che inizia con «v» o senza prefisso (di solito l'applicazione), altrimenti il più usato.
  const [pick, setPick] = useState(q.schemes.find((s) => s.pattern === "v[0-9]*" || s.pattern === "[0-9]*")?.pattern ?? q.schemes[0]?.pattern ?? "");
  const [all, setAll] = useState(false);
  const m = useMutation({ mutationFn: () => save(q.repository_id, all ? "" : pick), onSuccess: onSaved });
  const name = `tag-${q.repository_id}`;
  return (
    <form className="flex flex-col gap-2" onSubmit={(e) => { e.preventDefault(); m.mutate(); }}>
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className="mb-1 p-0 text-[13px]">
          {t("altro.schemaTag.question", { repo: q.full_name })}
        </legend>
        {q.schemes.map((s) => (
          <label key={s.pattern} className="flex flex-wrap items-baseline gap-x-2 text-[13px]">
            <input type="radio" name={name} checked={!all && pick === s.pattern} onChange={() => { setAll(false); setPick(s.pattern); }} />
            <code className="font-mono">{s.pattern}</code>
            <span className="text-muted">{t("altro.schemaTag.scheme", { count: s.count, examples: s.examples.join(", ") })}</span>
          </label>
        ))}
        <label className="flex items-baseline gap-x-2 text-[13px]">
          <input type="radio" name={name} checked={all} onChange={() => setAll(true)} />
          <span>{t("altro.schemaTag.all")}</span>
        </label>
      </fieldset>
      <div className="flex items-center gap-3">
        <button className="btn py-1" disabled={m.isPending}>{t("altro.schemaTag.save")}</button>
        <span className="text-[12px] text-muted">{t("altro.schemaTag.note")}</span>
      </div>
      <ErrorNote error={m.error} />
    </form>
  );
}
