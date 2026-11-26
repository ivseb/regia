// «Piano» del progetto: Gantt con fasi, work package, attività e milestone. Segnaposto: lo riempie l'agente A.
import { useParams } from "@tanstack/react-router";

export function ProjectPlanTab({ refKey }: { refKey: string }) {
  void refKey;
  return null;
}

export function ProjectPlanPage({ projectKey }: { projectKey: string }) {
  void projectKey;
  return null;
}

/** Rotta /progetti/$key/piano: il piano a tutta pagina. */
export function ProjectPlanRoute() {
  const { key } = useParams({ strict: false }) as { key: string };
  return <ProjectPlanPage key={key} projectKey={key} />;
}
