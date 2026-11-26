import type { Translation } from "../types";
import type it from "../it/errors-lavoro-tipi";

export default {
  "errors.validation.work.item_type_invalid": "Type not allowed: choose one of {types}.",
  "errors.validation.type.archive_in_use": "The type has {count} items: it can only be archived when it has none.",
} satisfies Translation<typeof it>;
