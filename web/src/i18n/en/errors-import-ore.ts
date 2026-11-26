import type { Translation } from "../types";
import type it from "../it/errors-import-ore";

export default {
  "errors.validation.finance.mapping_hours_or_minutes": "Map just one column to hours or minutes, not both.",
  "errors.validation.finance.import_target": "That import target isn't allowed. Choose costs or hours.",
} satisfies Translation<typeof it>;
