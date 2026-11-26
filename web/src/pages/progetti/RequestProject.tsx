// Sulla scheda «Piano» della richiesta: il suo progetto, oppure «Crea il progetto» se è approvata.
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { projectKeys, projectsApi } from "../../projectsApi";
import { ErrorNote } from "../../ui";
import { Trans } from "../../i18n/Trans";
import { useT } from "../../i18n/react";

export function RequestProjectBanner({ refKey }: { refKey: string }) {
  const t = useT();
  const nav = useNavigate();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: projectKeys.ofRequest(refKey), queryFn: () => projectsApi.ofRequest(refKey) });
  const create = useMutation({
    mutationFn: () => projectsApi.fromRequest(refKey),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: projectKeys.ofRequest(refKey) });
      qc.invalidateQueries({ queryKey: projectKeys.list });
      nav({ to: "/e/$key", params: { key: r.project.key }, search: { scheda: "piano" } });
    },
  });
  if (q.error || !q.data) return null;
  const { project, approved } = q.data;
  if (project) {
    return (
      <div className="mb-3 flex flex-wrap items-center gap-x-4 gap-y-1 border-l-2 border-accent pl-3" data-testid="progetto-della-richiesta">
        <p className="m-0">
          <Trans k="progetti.request.is" slots={{ key: <span className="font-mono text-[12px] font-semibold">{project.key}</span>, title: project.title }} />
        </p>
        <Link to="/e/$key" params={{ key: project.key }} search={{ scheda: "piano" }} className="touch-44 inline-flex items-center text-accent underline">
          {t("progetti.request.open")}
        </Link>
      </div>
    );
  }
  if (approved) {
    return (
      <div className="mb-3 flex flex-wrap items-center gap-3 border-l-2 border-accent pl-3">
        <button type="button" className="btn btn-primary" disabled={create.isPending} onClick={() => create.mutate()}>
          {create.isPending ? t("progetti.request.creating") : t("progetti.request.create")}
        </button>
        <span className="text-[12.5px] text-muted">{t("progetti.request.createHint")}</span>
        <ErrorNote error={create.error} />
      </div>
    );
  }
  return <p className="m-0 mb-3 text-[12.5px] text-muted">{t("progetti.request.notYet")}</p>;
}
