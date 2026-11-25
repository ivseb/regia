// Catalogo inglese: unisce i file di area, elencati in automatico da scripts/gen-i18n.mjs
// (areas.gen.ts). Una nuova area: it/<area>.ts e en/<area>.ts, nient'altro.
import areas from "./areas.gen.ts";
import type { Translation } from "../types";
import type itCatalog from "../it";

export default areas satisfies Translation<typeof itCatalog>;
