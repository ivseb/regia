// Lavoro: richieste, dettaglio, board, backlog, sprint, tempo, graduatoria, portafoglio, portale.
// Le chiavi iniziano con «lavoro.»: vedi web/src/i18n/GLOSSARIO.md e docs/sviluppo/testi-e-lingue.md.
// Diviso in gruppi di pagine (it/lavoro/*.ts) per lavorare in parallelo; stesso schema in en/.
import type { Source } from "../types";
import areas from "./lavoro/areas.gen.ts";

export default areas satisfies Source;
