// Questionari precompilati dall'AI (funzione «questionari», docs/ai.md §11): in testa alla sezione
// «Proponi le risposte» e «Accetta le proposte della sezione», sotto la domanda la proposta con la
// fonte, la confidenza e i pulsanti Accetta, Modifica, Scarta. Compare solo con l'AI accesa e la
// funzione pronta: senza AI la pagina della valutazione è identica. Nulla si salva da sé.
import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { aiApi } from "../aiApi";
import { reviewsApi, type AIDecision, type AIProposeResult } from "../reviewsApi";
import type { ReviewAIProposal, ReviewAISource, ReviewView } from "../api";
import type { Question, QSection } from "../condition";
import { ErrorNote } from "../ui";
import { useT } from "../i18n/react";
import type { Key } from "../i18n/core";

/** Pronta solo con l'AI accesa e la funzione «questionari». */
export function useQuestionnaireAI() {
  const status = useQuery({ queryKey: ["ai-status"], queryFn: aiApi.status, staleTime: 60_000, retry: false });
  const st = status.data;
  return { ready: !!st?.enabled && !!st.features?.questionari, masked: !!st?.masked, blocked: st?.budget.state === "blocked" };
}

const isHighlighted = (v: ReviewView, key: string) => (v.highlights?.[key]?.length ?? 0) > 0;

/** Strumenti della sezione: proporre le risposte e accettarle tutte. */
export function SectionAI({
  v,
  reviewKey,
  section,
  editable,
  onDone,
}: {
  v: ReviewView;
  reviewKey: string;
  section: QSection;
  editable: boolean;
  onDone: (nv: ReviewView) => void;
}) {
  const t = useT();
  const { ready, masked, blocked } = useQuestionnaireAI();
  const [result, setResult] = useState<AIProposeResult | null>(null);
  const [confirming, setConfirming] = useState(false);
  const propose = useMutation({
    mutationFn: () => reviewsApi.aiPropose(reviewKey, section.key),
    onSuccess: (r) => {
      setResult(r.result);
      onDone(r.view);
    },
  });
  const keys = new Set(section.questions.map((q) => q.key));
  const pending = (v.ai_proposals ?? []).filter((p) => keys.has(p.question) && p.value != null);
  const careful = pending.filter((p) => isHighlighted(v, p.question));
  const acceptAll = useMutation({
    mutationFn: () => reviewsApi.aiDecide(reviewKey, pending.map((p) => ({ id: p.id, accept: true }))),
    onSuccess: (r) => {
      setConfirming(false);
      onDone(r.view);
    },
  });
  if (!ready || !editable) return null;
  const label = (k: string) => section.questions.find((q) => q.key === k)?.label ?? k;
  return (
    <div className="flex flex-col gap-2 pt-1" data-testid="questionari-ai">
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" className="btn touch-44 whitespace-normal" disabled={propose.isPending || blocked} onClick={() => propose.mutate()}>
          {t("ai.questionari.propose")}
        </button>
        {pending.length > 0 && !confirming && (
          <button
            type="button"
            className="btn touch-44 whitespace-normal"
            disabled={acceptAll.isPending}
            onClick={() => (careful.length > 0 ? setConfirming(true) : acceptAll.mutate())}
          >
            {t("ai.questionari.acceptSection", { n: pending.length })}
          </button>
        )}
        <span className="text-[12px] text-muted">{masked ? t("ai.card.noteMasked") : t("ai.card.note")}</span>
      </div>
      {!result && !propose.isPending && pending.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("ai.questionari.proposeHelp")}</p>}
      {propose.isPending && (
        <p className="m-0 text-[12.5px] text-muted" role="status">
          {t("ai.questionari.working")}
        </p>
      )}
      {result && !propose.isPending && (
        <p className="m-0 text-[12.5px]" role="status">
          {t("ai.questionari.result", { proposed: result.proposed, none: result.no_source })}
          {result.remaining > 0 && <> {t("ai.questionari.remaining", { n: result.remaining })}</>}
        </p>
      )}
      {confirming && (
        <div className="flex flex-col gap-2 border border-flag p-3" role="alertdialog" aria-labelledby={`conferma-${section.key}`}>
          <p id={`conferma-${section.key}`} className="m-0 text-[13px] font-semibold">
            {t("ai.questionari.confirmTitle")}
          </p>
          <ul className="m-0 pl-5 text-[13px]">
            {careful.map((p) => (
              <li key={p.id}>
                <a href={`#domanda-${p.question}`}>{label(p.question)}</a>
              </li>
            ))}
          </ul>
          <div className="flex flex-wrap gap-2">
            <button type="button" className="btn btn-primary touch-44 whitespace-normal" disabled={acceptAll.isPending} onClick={() => acceptAll.mutate()}>
              {t("ai.questionari.confirmAll")}
            </button>
            <button type="button" className="btn touch-44 whitespace-normal" onClick={() => setConfirming(false)}>
              {t("ai.questionari.cancel")}
            </button>
          </div>
        </div>
      )}
      {propose.error && <ErrorNote error={propose.error} />}
      {acceptAll.error && <ErrorNote error={acceptAll.error} />}
    </div>
  );
}

function SourceLink({ s }: { s: ReviewAISource }) {
  const t = useT();
  const key = s.key ?? "";
  const link = (text: string, hash?: string) =>
    key ? (
      s.kind === "answer" || s.kind === "similar" ? (
        <Link to="/valutazioni/$key" params={{ key }} hash={hash}>
          {text}
        </Link>
      ) : (
        <Link to="/e/$key" params={{ key }}>
          {text}
        </Link>
      )
    ) : (
      text
    );
  switch (s.kind) {
    case "answer":
      return link(t("ai.questionari.source.answer", { key }), s.question ? `domanda-${s.question}` : undefined);
    case "similar":
      return link(t("ai.questionari.source.similar", { key, app: s.detail ?? "" }), s.question ? `domanda-${s.question}` : undefined);
    case "page":
      return link(t("ai.questionari.source.page", { key, title: s.title ?? "" }));
    case "attachment":
      return link(t("ai.questionari.source.attachment", { key, name: s.detail ?? "" }));
    default:
      return link(t("ai.questionari.source.fact", { key, detail: s.detail ?? "" }));
  }
}

const confCls: Record<string, string> = { alta: "text-ok", media: "text-fg", bassa: "text-flag" };

/** La proposta sotto la domanda. editor: il campo della domanda con un valore di prova. */
export function ProposalBox({
  v,
  reviewKey,
  q,
  editable,
  format,
  editor,
  onDone,
}: {
  v: ReviewView;
  reviewKey: string;
  q: Question;
  editable: boolean;
  format: (q: Question, value: unknown) => string;
  editor: (q: Question, value: unknown, onChange: (value: unknown) => void) => ReactNode;
  onDone: (nv: ReviewView) => void;
}) {
  const t = useT();
  const { ready } = useQuestionnaireAI();
  const p: ReviewAIProposal | undefined = v.ai_proposals?.find((x) => x.question === q.key);
  const [draft, setDraft] = useState<{ value: unknown } | null>(null);
  const decide = useMutation({
    mutationFn: (d: AIDecision) => reviewsApi.aiDecide(reviewKey, [d]),
    onSuccess: (r) => {
      setDraft(null);
      onDone(r.view);
    },
  });
  if (!ready || !editable || !p) return null;
  const careful = isHighlighted(v, q.key);
  const id = `proposta-${q.key}`;
  return (
    <section aria-labelledby={id} className={`flex flex-col gap-1.5 border bg-side p-2.5 text-[13px] ${careful ? "border-flag" : "border-hair"}`} data-testid="proposta-ai">
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <h4 id={id} className="label m-0">
          {t("ai.questionari.boxLabel")}
        </h4>
        {p.value != null && <span className={`text-[12px] ${confCls[p.confidence] ?? "text-muted"}`}>{t(`ai.questionari.confidence.${p.confidence}` as Key)}</span>}
      </div>
      {p.value == null ? (
        <p className="m-0 text-muted">{t("ai.questionari.none")}</p>
      ) : (
        <>
          <p className="m-0 break-words font-semibold">{format(q, p.value)}</p>
          {p.reason && <p className="m-0 text-[12.5px] text-muted">{p.reason}</p>}
          <p className="m-0 flex flex-wrap gap-x-2 text-[12.5px]">
            <span className="text-muted">{t("ai.questionari.sources")}</span>
            {p.sources.map((s, i) => (
              <span key={i}>
                <SourceLink s={s} />
                {i < p.sources.length - 1 ? ";" : ""}
              </span>
            ))}
          </p>
          {careful && <p className="m-0 text-[12.5px] text-flag">{t("ai.questionari.careful")}</p>}
        </>
      )}
      {draft && <div className="flex flex-col gap-2 pt-1">{editor(q, draft.value, (value) => setDraft({ value }))}</div>}
      <div className="flex flex-wrap gap-2 pt-0.5">
        {p.value != null && !draft && (
          <>
            <button type="button" className="btn btn-primary touch-44 whitespace-normal" disabled={decide.isPending} onClick={() => decide.mutate({ id: p.id, accept: true })}>
              {t("ai.questionari.accept")}
            </button>
            <button type="button" className="btn touch-44 whitespace-normal" onClick={() => setDraft({ value: p.value })}>
              {t("ai.questionari.edit")}
            </button>
          </>
        )}
        {draft && (
          <>
            <button
              type="button"
              className="btn btn-primary touch-44 whitespace-normal"
              disabled={decide.isPending || draft.value == null || draft.value === ""}
              onClick={() => decide.mutate({ id: p.id, accept: true, value: draft.value })}
            >
              {t("ai.questionari.acceptEdited")}
            </button>
            <button type="button" className="btn touch-44 whitespace-normal" onClick={() => setDraft(null)}>
              {t("ai.questionari.cancel")}
            </button>
          </>
        )}
        <button type="button" className="btn touch-44 whitespace-normal" disabled={decide.isPending} onClick={() => decide.mutate({ id: p.id, accept: false })}>
          {t("ai.questionari.discard")}
        </button>
      </div>
      {decide.error && <ErrorNote error={decide.error} />}
    </section>
  );
}
