import type { Translation } from "../types";
import type it from "../it/errors-prova";

export default {
  "errors.validation.sandbox.not_linked": "There is no test environment linked to this organization.",
  "errors.validation.sandbox.exists": "A linked test environment already exists: enter it or recreate it.",
  "errors.validation.sandbox.in_sandbox": "This is already a test environment: it is created from the main organization.",
  "errors.validation.sandbox.slug_taken": "An organization with the test environment identifier already exists: contact the platform administrator.",
  "errors.validation.sandbox.nothing_selected": "Choose at least one object to promote.",
  "errors.validation.sandbox.item_unknown": "One of the chosen objects is no longer among the differences: reload the page and choose again.",
  "errors.validation.sandbox.dependency": "An object needs something that is not in the main one: promote that too (usually the field or type it uses).",
} satisfies Translation<typeof it>;
