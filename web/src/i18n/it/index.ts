// Catalogo italiano (sorgente): unisce i file di area, elencati in automatico da
// scripts/gen-i18n.mjs (areas.gen.ts). Una nuova area: it/<area>.ts e en/<area>.ts, nient'altro.
import areas from "./areas.gen.ts";
import type { Source } from "../types";

export default areas satisfies Source;
