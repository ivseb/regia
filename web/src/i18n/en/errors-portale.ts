import type { Translation } from "../types";
import type it from "../it/errors-portale";

export default {
  "errors.validation.portal.entry_text_limits": "In the entry the name is 1 to 80 characters, the description up to 400 and the category up to 60.",
  "errors.validation.portal.entry_key_taken": "An entry with the key “{key}” already exists.",
  "errors.validation.portal.group_unknown": "The chosen group does not exist.",
  "errors.validation.portal.nothing_to_publish": "There are no changes to publish.",
  "errors.validation.portal.form_text_limits": "A text in the form is too long (at most {max} characters).",
  "errors.validation.portal.form_too_many_steps": "The form can have at most {max} steps.",
  "errors.validation.portal.form_field_unknown": "The field “{field}” cannot be used in this entry.",
  "errors.validation.portal.form_field_repeated": "The field “{field}” appears twice in the form.",
  "errors.validation.portal.condition_invalid": "The condition of “{field}” is not valid.",
  "errors.validation.portal.route_too_many": "There can be at most {max} routing rules.",
  "errors.validation.portal.route_priority_invalid": "In rule {rule} the priority is not valid.",
  "errors.validation.portal.route_target_required": "In rule {rule} give the role and either a team or a group.",
  "errors.validation.portal.route_role_unknown": "In rule {rule} the chosen role does not exist.",
  "errors.validation.portal.route_target_unknown": "In rule {rule} the chosen team or group does not exist.",
  "errors.validation.portal.field_required": "The field “{field}” is required.",
  "errors.validation.portal.support_text": "Write a line saying you have the same need.",
} satisfies Translation<typeof it>;
