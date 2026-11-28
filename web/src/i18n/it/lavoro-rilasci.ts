// Storia dell'elemento: la chiusura con le parole chiave dei commit (area «lavoro»).
import type { Source } from "../types";

export default {
  "lavoro.elementsections.h.commitClosed": "chiuso dal commit {sha} ({keyword}): fase {to}",
  "lavoro.elementsections.h.commitCloseSkipped": "il commit {sha} chiede la chiusura ({keyword}), ma il processo non consente il passaggio alla fase finale",
  "lavoro.elementsections.h.commitCloseAmbiguous": "il commit {sha} chiede la chiusura ({keyword}), ma ci sono più fasi finali possibili: scegli a mano",
} satisfies Source;
