// Assistente nella scheda (replica/ai.md §2 punto 2, ondata 3): riassunto, «cosa manca per
// decidere» con i link a campi, commenti ed elementi, bozze di testo che la persona rilegge e
// inserisce con un clic. Compare solo con l'AI accesa e le funzioni pronte: senza AI la scheda non
// cambia. Ogni risposta è del modello e si rilegge: nulla si scrive senza la persona.
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { aiApi, type CardGap, type CardResult, type DraftKind } from "../aiApi";
import type { ElementDetail } from "../api";
import { ErrorNote } from "../ui";
import { useT } from "../i18n/react";
import { lookup } from "../i18n/core";
import { LinkedText } from "./Assistente";
import type { TabKey } from "./ElementPage";

/** Porta la vista a un elemento della pagina (campo, descrizione, commento) e gli dà il fuoco. */
function reveal(id: string) {
  const go = (tries: number) => {
    const el = document.getElementById(id);
    if (el) {
      el.scrollIntoView({ block: "center" });
      if (!el.hasAttribute("tabindex")) el.setAttribute("tabindex", "-1");
      el.focus({ preventScroll: true });
    } else if (tries > 0) window.setTimeout(() => go(tries - 1), 100);
  };
  go(20);
}

function GapLink({ g, onTab }: { g: CardGap; onTab: (t: TabKey) => void }) {
  const t = useT();
  if (g.ref)
    return (
      <Link to="/e/$key" params={{ key: g.ref.key }} className="touch-44 inline-flex items-center font-mono text-[12.5px]">
        {g.ref.key}
      </Link>
    );
  if (g.comment_id)
    return (
      <button
        type="button"
        className="touch-44 text-[12.5px] text-accent underline"
        onClick={() => {
          onTab("attivita");
          reveal(`c-${g.comment_id}`);
        }}
      >
        {t("ai.card.gap.toComment", { who: g.comment_by ?? "" })}
      </button>
    );
  if (g.field === "descrizione")
    return (
      <button type="button" className="touch-44 text-[12.5px] text-accent underline" onClick={() => reveal("descrizione-scheda")}>
        {t("ai.card.gap.toDescription")}
      </button>
    );
  if (g.field)
    return (
      <button type="button" className="touch-44 text-[12.5px] text-accent underline" onClick={() => reveal(`campo-vista-${g.field}`)}>
        {t("ai.card.gap.toField", { name: g.field_name ?? g.field })}
      </button>
    );
  return null;
}

function Gaps({ r, onTab }: { r: CardResult; onTab: (t: TabKey) => void }) {
  const t = useT();
  const gaps = r.gaps ?? [];
  if (gaps.length === 0) return <p className="m-0" role="status">{r.text || t("ai.card.gaps.none")}</p>;
  return (
    <>
      <ul className="m-0 flex list-none flex-col p-0" aria-label={t("ai.card.gaps.list")} data-testid="mancanze">
        {gaps.map((g, i) => (
          <li key={i} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-hair py-2">
            <span className="label m-0 shrink-0">{lookup(`ai.card.kind.${g.kind}`) ?? g.kind}</span>
            <span className="min-w-0 flex-1 basis-60 [overflow-wrap:anywhere]">
              <LinkedText text={g.text} refs={g.ref ? [g.ref] : []} />
            </span>
            <span className="flex items-center gap-2">
              <GapLink g={g} onTab={onTab} />
              {g.source === "regia" && <span className="text-[12px] text-muted">{t("ai.card.gap.byRegia")}</span>}
            </span>
          </li>
        ))}
      </ul>
      {(r.unverified ?? 0) > 0 && <p className="m-0 text-[12.5px] text-muted">{t("ai.card.unverified", { count: r.unverified ?? 0 })}</p>}
    </>
  );
}

function DraftEditor({ refKey, r, onDone }: { refKey: string; r: CardResult; onDone: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const kind = r.draft ?? "commento";
  const [text, setText] = useState(r.text ?? "");
  const insert = useMutation({
    mutationFn: () => aiApi.cardInsert(refKey, kind, text),
    onSuccess: (p) => {
      if (p.status !== "applied") return;
      qc.invalidateQueries({ queryKey: ["element", refKey] });
      qc.invalidateQueries({ queryKey: ["comments", refKey] });
      qc.invalidateQueries({ queryKey: ["activity"] });
    },
  });
  const done = insert.data?.status === "applied";
  const id = `bozza-${refKey}`;
  return (
    <div className="flex flex-col gap-2" data-testid="bozza">
      <label htmlFor={id} className="label">
        {lookup(`ai.card.draftLabel.${kind}`) ?? kind}
      </label>
      <textarea id={id} className="field min-h-36 leading-relaxed" value={text} onChange={(e) => setText(e.target.value)} disabled={done} />
      <p className="m-0 text-[12px] text-muted">{t("ai.card.draftReview")}</p>
      <ErrorNote error={insert.error} />
      {insert.data && insert.data.status !== "applied" && <p className="m-0 text-[12.5px] text-danger">{insert.data.error}</p>}
      {done ? (
        <p className="m-0 text-ok" role="status">
          {t(kind === "descrizione" ? "ai.card.inserted.descrizione" : "ai.card.inserted.commento")}
        </p>
      ) : (
        <div className="flex flex-wrap gap-2">
          <button type="button" className="btn btn-primary touch-44" disabled={insert.isPending || !text.trim()} onClick={() => insert.mutate()}>
            {t(kind === "descrizione" ? "ai.card.insert.descrizione" : "ai.card.insert.commento")}
          </button>
          <button type="button" className="btn touch-44" onClick={onDone}>
            {t("ai.card.discard")}
          </button>
        </div>
      )}
    </div>
  );
}

export function CardAssistant({ detail, onTab }: { detail: ElementDetail; onTab: (t: TabKey) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["ai-status"], queryFn: aiApi.status, staleTime: 60_000, retry: false });
  const [draftOpen, setDraftOpen] = useState(false);
  const [kind, setKind] = useState<DraftKind>("commento");
  const [hint, setHint] = useState("");
  const [result, setResult] = useState<CardResult | null>(null);
  const el = detail.element;
  const refKey = el.key || el.id;
  const run = useMutation({
    mutationFn: (a: { action: "riassunto" | "mancanze" | "bozza"; draft?: DraftKind; hint?: string }) => aiApi.card(refKey, a.action, a.draft, a.hint),
    onMutate: () => setResult(null),
    onSuccess: (r) => {
      qc.setQueryData(["ai-status"], r.status);
      setResult(r);
    },
  });
  const st = status.data;
  const f = st?.enabled ? (st.features ?? {}) : {};
  if (!f.riassunti && !f.mancanze && !f.bozze) return null;
  const blocked = st?.budget.state === "blocked";
  const kinds: DraftKind[] = el.reporter_id ? ["commento", "risposta", "descrizione"] : ["commento", "descrizione"];
  return (
    <section aria-labelledby={`assistente-scheda-${refKey}`} className="flex min-w-0 max-w-3xl flex-col gap-3 border border-hair bg-side p-3" data-testid="assistente-scheda">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <h2 id={`assistente-scheda-${refKey}`} className="label m-0">
          {t("ai.card.title")}
        </h2>
        <span className="text-[12px] text-muted">{st?.masked ? t("ai.card.noteMasked") : t("ai.card.note")}</span>
      </div>
      {blocked ? (
        <p className="m-0 text-[12.5px] text-flag">{t("ai.panel.budgetBlocked")}</p>
      ) : (
        <div className="flex flex-wrap gap-2">
          {f.riassunti && (
            <button type="button" className="btn touch-44" disabled={run.isPending} onClick={() => run.mutate({ action: "riassunto" })}>
              {t("ai.card.summary")}
            </button>
          )}
          {f.mancanze && (
            <button type="button" className="btn touch-44" disabled={run.isPending} onClick={() => run.mutate({ action: "mancanze" })}>
              {t("ai.card.gaps")}
            </button>
          )}
          {f.bozze && (
            <button type="button" className="btn touch-44" aria-expanded={draftOpen} onClick={() => setDraftOpen((o) => !o)}>
              {t("ai.card.draft")}
            </button>
          )}
        </div>
      )}
      {draftOpen && f.bozze && !blocked && (
        <form
          className="flex flex-col gap-2 border-t border-hair pt-3"
          onSubmit={(e) => {
            e.preventDefault();
            run.mutate({ action: "bozza", draft: kind, hint: hint.trim() || undefined });
          }}
        >
          <fieldset className="m-0 flex flex-wrap gap-x-4 gap-y-1 border-0 p-0">
            <legend className="label mb-1 p-0">{t("ai.card.draftKind")}</legend>
            {kinds.map((k) => (
              <label key={k} className="touch-44 inline-flex items-center gap-2">
                <input type="radio" name={`tipo-bozza-${refKey}`} className="size-4 accent-[var(--accent)]" checked={kind === k} onChange={() => setKind(k)} />
                {lookup(`ai.card.kindDraft.${k}`) ?? k}
              </label>
            ))}
          </fieldset>
          <label className="flex flex-col gap-1">
            <span className="label">{t("ai.card.hint")}</span>
            <input className="field" maxLength={500} value={hint} onChange={(e) => setHint(e.target.value)} placeholder={t("ai.card.hintPh")} />
          </label>
          <div>
            <button className="btn btn-primary touch-44" disabled={run.isPending}>
              {t("ai.card.write")}
            </button>
          </div>
        </form>
      )}
      <div aria-live="polite" className="flex min-w-0 flex-col gap-2">
        {run.isPending && <p className="m-0 text-muted">{t("ai.card.working")}</p>}
        <ErrorNote error={run.error} />
        {result && (
          <div className="flex min-w-0 flex-col gap-2 border-t border-hair pt-3" data-testid="risposta-scheda">
            {result.action === "riassunto" && (
              <>
                <span className="label">{t("ai.card.summaryTitle")}</span>
                <LinkedText text={result.text ?? ""} refs={result.refs} />
              </>
            )}
            {result.action === "mancanze" && (
              <>
                <span className="label">{t("ai.card.gapsTitle")}</span>
                <Gaps r={result} onTab={onTab} />
              </>
            )}
            {result.action === "bozza" && <DraftEditor key={result.text} refKey={refKey} r={result} onDone={() => setResult(null)} />}
          </div>
        )}
      </div>
    </section>
  );
}
