import type { Translation } from "../types";
import type it from "../it/errors-regole";

export default {
  "errors.validation.rule.key_invalid": "The rule key is not valid: use lowercase letters, digits and underscores.",
  "errors.validation.rule.key_duplicate": "Two rules have the same key.",
  "errors.validation.rule.event_invalid": "The rule's event is not valid: choose “When created”, “When it enters…” or “When it leaves…”.",
  "errors.validation.rule.phase_unknown": "The phase of the event does not exist in the process.",
  "errors.validation.rule.condition_invalid": "The rule's condition is not valid.",
  "errors.validation.rule.no_actions": "The rule does nothing: add at least one action.",
  "errors.validation.rule.kind_required": "Choose the type of action.",
  "errors.validation.rule.kind_invalid": "The type of action is not valid.",
  "errors.validation.rule.action_unknown": "This action is not available.",
  "errors.validation.rule.role_required": "Choose the role to assign.",
  "errors.validation.rule.target_required": "Choose a group or a member to assign the role to.",
  "errors.validation.rule.role_unknown": "The chosen role does not exist.",
  "errors.validation.rule.group_unknown": "The chosen group does not exist.",
  "errors.validation.rule.member_unknown": "The chosen member does not exist or is not active.",
  "errors.validation.rule.param_required": "An action is missing a required value.",
  "errors.validation.rule.param_invalid": "A value of an action is not valid.",
} satisfies Translation<typeof it>;
