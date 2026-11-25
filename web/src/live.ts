import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";

// Aggiornamenti in tempo reale: il server avvisa (SSE) quando qualcuno modifica qualcosa
// nel tenant e qui si ricaricano le query visibili. Le notifiche ravvicinate si raggruppano.
export function useLiveUpdates(tenantId: string | undefined) {
  const qc = useQueryClient();
  useEffect(() => {
    if (!tenantId || typeof EventSource === "undefined") return;
    const es = new EventSource("/api/v1/events");
    let timer: number | undefined;
    es.onmessage = () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        qc.invalidateQueries({ predicate: (q) => q.queryKey[0] !== "me" });
      }, 250);
    };
    return () => {
      window.clearTimeout(timer);
      es.close();
    };
  }, [tenantId, qc]);
}
