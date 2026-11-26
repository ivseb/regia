import type { Translation } from "../types";
import type it from "../it/errors-organizzazioni";

export default {
  "errors.platform.admin_required": "Only the instance's super administrator can manage organizations.",
  "errors.validation.organizzazioni.name_invalid": "Write the organization's name (120 characters at most).",
  "errors.validation.organizzazioni.slug_invalid": "Invalid identifier: lowercase letters, digits and hyphens, 2 to 63 characters.",
  "errors.validation.organizzazioni.slug_taken": "An organization with this identifier already exists: choose another one.",
  "errors.validation.organizzazioni.language_invalid": "Unsupported language.",
  "errors.validation.organizzazioni.currency_invalid": "Invalid currency: a three-letter code, for example EUR.",
  "errors.validation.organizzazioni.admin_email_invalid": "The other administrator's email address is not valid.",
  "errors.platform.cannot_suspend_current": "You can't suspend the organization you are working in: enter another one first.",
  "errors.platform.tenant_not_found": "This organization no longer exists.",
  "errors.tenant.suspended": "The organization is suspended: contact the instance administrator.",
} satisfies Translation<typeof it>;
