// Errori dei tipi del lavoro: tipo non ammesso e archiviazione dei tipi di sistema.
import type { Source } from "../types";

export default {
  "errors.validation.work.item_type_invalid": "Tipo non ammesso: scegli tra {types}.",
  "errors.validation.type.archive_in_use": "Il tipo ha {count} elementi: si archivia solo quando non ne ha più.",
} satisfies Source;
