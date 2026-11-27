// «Aggiungi alla wiki» (replica/ai.md §7, ondata 4): da una scheda (l'elemento) o da una risposta
// dell'assistente (il testo). Si aggiunge in fondo a una pagina della wiki personale e resta come
// fonte per il prossimo aggiornamento dell'assistente. Funziona anche senza AI.
import { useId, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { wikiApi } from "../wikiApi";
import { Select } from "../select";
import { ErrorNote } from "../ui";
import { useT } from "../i18n/react";

const NEW = "";

export function WikiAdd({ element, text, compact = false }: { element?: string; text?: string; compact?: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const uid = useId();
  const [open, setOpen] = useState(false);
  const [page, setPage] = useState(NEW);
  const [note, setNote] = useState("");
  const home = useQuery({
    queryKey: ["wiki"],
    queryFn: wikiApi.home,
    enabled: open,
  });
  const pages = home.data?.pages ?? [];
  // Senza scelta si aggiunge alla pagina degli appunti (creata se manca).
  const defaultTitle = t("ai.wiki.addDefaultTitle");
  const notes = pages.find((p) => p.title.toLowerCase() === defaultTitle.toLowerCase());
  const value = page || notes?.id || NEW;
  const add = useMutation({
    mutationFn: () =>
      wikiApi.add({
        element,
        text,
        page: value || undefined,
        title: value ? undefined : defaultTitle,
        note: note.trim() || undefined,
      }),
    onSuccess: () => {
      setNote("");
      qc.invalidateQueries({ queryKey: ["wiki"] });
    },
  });
  return (
    <div className="flex min-w-0 flex-col gap-2" data-testid="aggiungi-wiki">
      <button
        type="button"
        className={`${compact ? "text-[12.5px] text-accent underline" : "btn"} touch-44 inline-flex items-center self-start`}
        aria-expanded={open}
        aria-controls={`${uid}-form`}
        onClick={() => {
          setOpen((o) => !o);
          add.reset();
        }}
      >
        {t("ai.wiki.add")}
      </button>
      {open && (
        <form
          id={`${uid}-form`}
          aria-label={t("ai.wiki.addTitle")}
          className="flex flex-col gap-2 border-l-2 border-hair pl-3"
          onSubmit={(e) => {
            e.preventDefault();
            add.mutate();
          }}
        >
          <label className="flex min-w-0 flex-col gap-1">
            <span className="label">{t("ai.wiki.addPage")}</span>
            <Select className="field" value={value} onChange={(e) => setPage(e.target.value)}>
              {!notes && <option value={NEW}>{t("ai.wiki.addNewPage", { title: defaultTitle })}</option>}
              {pages.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.title}
                </option>
              ))}
            </Select>
          </label>
          <label className="flex min-w-0 flex-col gap-1">
            <span className="label">{t("ai.wiki.addNote")}</span>
            <input className="field" maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
          </label>
          <p className="m-0 text-[12px] text-muted">{t("ai.wiki.addHelp")}</p>
          <ErrorNote error={add.error} />
          <div>
            <button className="btn btn-primary touch-44" disabled={add.isPending}>
              {t("ai.wiki.addGo")}
            </button>
          </div>
          {add.data && (
            <p className="m-0 flex flex-wrap gap-x-2 text-[13px]" role="status">
              {t("ai.wiki.added", { title: add.data.page.title })}
              <Link to="/profilo/wiki/$id" params={{ id: add.data.page.id }} className="touch-44 inline-flex items-center">
                {t("ai.wiki.addOpen")}
              </Link>
            </p>
          )}
        </form>
      )}
    </div>
  );
}
