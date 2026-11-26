import { Select } from "../select";
import { useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { reviewsApi } from "../reviewsApi";
import { ErrorNote, SectionTitle } from "../ui";
import { Due, Outcome } from "./Reviews";
import { useT } from "../i18n/react";
import { ModuleChip } from "../moduleIcon";

// Sezione «Valutazioni» nel dettaglio della richiesta e dell'impatto (piano, passo 4):
// valutazioni aperte dalle regole o a mano, con team, ambito, fase, scadenza ed esito.

export default function ReviewsSection({ refKey, isImpact }: { refKey: string; isImpact: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const list = useQuery({ queryKey: ["reviews", "element", refKey], queryFn: () => reviewsApi.forElement(refKey) });
  const [opening, setOpening] = useState(false);
  const [type, setType] = useState("");
  const [impact, setImpact] = useState("");
  const open = useMutation({
    mutationFn: () => reviewsApi.open(refKey, type, impact || undefined),
    onSuccess: (v) => {
      qc.invalidateQueries({ queryKey: ["reviews"] });
      qc.invalidateQueries({ queryKey: ["element", refKey] });
      navigate({ to: "/valutazioni/$key", params: { key: v.review.key } });
    },
  });
  const data = list.data;
  const items = (data?.items ?? []).filter((r) => !r.superseded);
  const f = data?.facts ?? {};
  const chosen = data?.types.find((x) => x.key === type);
  const n = (k: string) => Number(f[k] ?? 0);
  return (
    <section className="flex flex-col gap-2" aria-label={t("lavoro.reviewssection.title")}>
      <SectionTitle
        aside={
          data && data.types.length > 0 && !opening ? (
            <button className="text-[12px] text-accent underline" onClick={() => setOpening(true)}>
              {t("lavoro.reviewssection.openOne")}
            </button>
          ) : undefined
        }
      >
        <ModuleChip module="grc" size="sm" className="mr-1.5 align-middle" />
        {t("lavoro.reviewssection.title")}
      </SectionTitle>
      <ErrorNote error={list.error} />
      {data && (
        <p className="m-0 flex flex-wrap gap-x-3 text-[12.5px] text-muted tabular">
          <span>{t("lavoro.reviewssection.total", { n: n("valutazioni") })}</span>
          <span>{t("lavoro.reviewssection.open", { n: n("valutazioni_aperte") })}</span>
          {n("valutazioni_obbligatorie_aperte") > 0 && <span className="text-fg">{t("lavoro.reviewssection.mandatoryOpen", { n: n("valutazioni_obbligatorie_aperte") })}</span>}
          {n("valutazioni_in_ritardo") > 0 && <span className="font-semibold text-danger">{t("lavoro.reviewssection.late", { n: n("valutazioni_in_ritardo") })}</span>}
          {n("valutazioni_non_favorevoli") > 0 && <span className="font-semibold text-danger">{t("lavoro.reviewssection.unfavorable", { n: n("valutazioni_non_favorevoli") })}</span>}
          {n("prescrizioni") > 0 && <span>{t("lavoro.reviewssection.prescriptions", { n: n("prescrizioni") })}</span>}
        </p>
      )}
      {data && items.length === 0 && (
        <p className="m-0 text-[12.5px] text-muted">
          {t(isImpact ? "lavoro.reviewssection.noneImpact" : "lavoro.reviewssection.none")}
        </p>
      )}
      {items.length > 0 && (
        <ul className="m-0 list-none p-0">
          {items.map((r) => (
            <li key={r.id} className="border-b border-hair">
              <Link to="/valutazioni/$key" params={{ key: r.key }} className="flex flex-col gap-0.5 py-2 text-fg no-underline hover:bg-sel/50">
                <span className="flex items-baseline justify-between gap-3">
                  <span className="min-w-0 break-words">
                    <span className="font-mono text-[12px] text-accent">{r.key}</span> <span className="font-semibold">{r.type_name}</span>
                    {r.mandatory && <span className="text-muted"> {t("lavoro.reviewssection.mandatory")}</span>}
                  </span>
                  <span className="shrink-0 text-[12.5px]">
                    {r.outcome_label ? <Outcome label={r.outcome_label} polarity={r.outcome_polarity} /> : <Due due={r.due_on} late={r.late_days} paused={r.paused} stage={r.stage} />}
                  </span>
                </span>
                <span className="flex justify-between gap-3 text-[12.5px] text-muted">
                  <span className="min-w-0 break-words">
                    {r.scope === "impact" ? r.application ?? r.impact_key : t("lavoro.reviewssection.wholeRequest")} · {r.team}
                  </span>
                  <span className="shrink-0">{r.state}</span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
      {opening && data && (
        <form
          className="flex flex-col gap-2 border-l-2 border-accent pl-3"
          onSubmit={(e) => {
            e.preventDefault();
            open.mutate();
          }}
        >
          <label className="flex flex-col gap-1">
            <span className="label">{t("lavoro.reviewssection.reviewType")}</span>
            <Select className="field" value={type} onChange={(e) => setType(e.target.value)} required>
              <option value="">{t("lavoro.reviewssection.choose")}</option>
              {data.types.map((x) => (
                <option key={x.key} value={x.key}>
                  {x.name} · {x.team} ({t(x.scope === "impact" ? "lavoro.reviewssection.perApplication" : "lavoro.reviewssection.perRequest")})
                </option>
              ))}
            </Select>
          </label>
          {chosen?.scope === "impact" && !isImpact && (
            <label className="flex flex-col gap-1">
              <span className="label">{t("lavoro.reviewssection.application")}</span>
              <Select className="field" value={impact} onChange={(e) => setImpact(e.target.value)} required>
                <option value="">{t("lavoro.reviewssection.chooseImpact")}</option>
                {data.impacts.map((i) => (
                  <option key={i.id} value={i.key}>
                    {i.application} ({i.key})
                  </option>
                ))}
              </Select>
            </label>
          )}
          <div className="flex gap-2">
            <button className="btn btn-primary" disabled={!type || open.isPending}>
              {t("lavoro.reviewssection.openAction")}
            </button>
            <button type="button" className="btn" onClick={() => setOpening(false)}>
              {t("lavoro.reviewssection.cancel")}
            </button>
          </div>
          <ErrorNote error={open.error} />
        </form>
      )}
    </section>
  );
}
