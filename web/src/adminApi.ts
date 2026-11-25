// API di Persone e accessi, Registro di audit, MFA, link monouso e webhook (piano, passo 15).
// Rispecchia internal/api/admin.go, mfa.go, webhooks.go.
import { call, type Me } from "./api";

const q = encodeURIComponent;

export type Ref = { id: string; name: string; system?: boolean };
export type RoleGrant = { id: string; name: string; key: string | null; assignment_id: string; via_group: string | null; admin: boolean };
export type MemberStatus = "invited" | "active" | "suspended" | "anonymized";
export type AdminMember = {
  id: string;
  display_name: string;
  email: string;
  member_type: string;
  status: MemberStatus;
  has_account: boolean;
  last_login_at: string | null;
  has_password: boolean;
  sso: boolean;
  mfa: boolean;
  admin: boolean;
  roles: RoleGrant[];
  groups: Ref[];
  teams: Ref[];
  invite_expires_at: string | null;
  created_at: string;
  sessions: number;
  tokens: number;
  other_tenants: boolean;
};
export type MemberList = { items: AdminMember[]; me: string; sso_only: boolean; email_available: boolean };
export type OneTimeLink = { kind: "invite" | "password"; path: string; url?: string; expires_at: string };

export type Holder = { assignment_id: string; id: string; name: string; status?: string };
export type AdminRole = {
  id: string;
  key: string | null;
  name: string;
  description: string;
  permissions: string[];
  system: boolean;
  admin: boolean;
  members: Holder[];
  groups: Holder[];
  created_at: string;
};
export type Permission = { key: string; name: string; description: string; area: string; sensitive?: boolean };

export type GroupUsage = { processes: string[]; review_teams: string[]; bodies: string[]; elements: number; approvals: number };
export type AdminGroup = {
  id: string;
  name: string;
  parent_id: string | null;
  system: boolean;
  members: Holder[];
  roles: Holder[];
  usage: GroupUsage;
  created_at: string;
};
export type StakeholderRole = { id: string; key: string; name: string; elements: number; processes: string[] };
export type Security = { mfa_policy: "off" | "admins" | "all"; group_inheritance: boolean; updated_at: string | null; pending_mfa: number };
export type AccessGrant = {
  role_id: string;
  role: string;
  permissions: string[];
  scope: "tenant" | "space";
  space: string | null;
  source: "direct" | "group" | "inherited";
  chain: string[];
};
export type AccessExplain = {
  inheritance: boolean;
  groups: { id: string; name: string; direct: boolean; chain: string[] }[];
  grants: AccessGrant[];
  permissions: { permission: string; space: string | null; from: number[] }[];
};

export type AuditItem = {
  seq: number;
  at: string;
  actor_account_id: string | null;
  actor_member_id: string | null;
  action: string;
  entity_type: string;
  entity_id: string | null;
  payload: Record<string, unknown>;
};
export type AuditPage = { items: AuditItem[]; next_before?: number; actors: Record<string, string> };
export type AuditFilter = { action?: string; entity_type?: string; entity_id?: string; actor?: string; from?: string; to?: string };
export type ChainStatus = { entries: number; broken_seq: number | null; last_seq: number | null; last_hash: string | null; intact: boolean };

export const adminApi = {
  members: (search = "") => call<MemberList>("GET", `/admin/members${search ? `?q=${q(search)}` : ""}`),
  member: (id: string) => call<AdminMember>("GET", `/admin/members/${q(id)}`),
  addMember: (m: { email: string; display_name: string; member_type: string; mode: "invite" | "create"; role_ids: string[]; group_ids: string[] }) =>
    call<{ member_id: string; new_account: boolean; link: OneTimeLink | null; emailed: boolean }>("POST", "/admin/members", m),
  updateMember: (id: string, m: { display_name?: string; member_type?: string }) => call<void>("PATCH", `/admin/members/${q(id)}`, m),
  suspend: (id: string) => call<void>("POST", `/admin/members/${q(id)}/suspend`),
  reactivate: (id: string) => call<void>("POST", `/admin/members/${q(id)}/reactivate`),
  resendInvite: (id: string) => call<{ link: OneTimeLink; emailed: boolean }>("POST", `/admin/members/${q(id)}/invitation`),
  passwordLink: (id: string) => call<{ link: OneTimeLink; emailed: boolean }>("POST", `/admin/members/${q(id)}/password-link`),
  resetMfa: (id: string) => call<void>("POST", `/admin/members/${q(id)}/mfa-reset`),

  roles: () => call<{ items: AdminRole[]; permissions: Permission[] }>("GET", "/admin/roles"),
  createRole: (r: { name: string; description: string; permissions: string[]; copy_of?: string }) => call<{ id: string }>("POST", "/admin/roles", r),
  updateRole: (id: string, r: { name: string; description: string; permissions: string[] }) => call<void>("PATCH", `/admin/roles/${q(id)}`, r),
  deleteRole: (id: string) => call<void>("DELETE", `/admin/roles/${q(id)}`),
  assign: (roleID: string, to: { member_id?: string; group_id?: string }) => call<{ id: string }>("POST", `/admin/roles/${q(roleID)}/assignments`, to),
  unassign: (assignmentID: string) => call<void>("DELETE", `/admin/role-assignments/${q(assignmentID)}`),

  groups: () => call<{ items: AdminGroup[] }>("GET", "/admin/groups"),
  createGroup: (g: { name: string; parent_id: string | null }) => call<{ id: string }>("POST", "/admin/groups", g),
  updateGroup: (id: string, g: { name: string; parent_id: string | null }) => call<void>("PATCH", `/admin/groups/${q(id)}`, g),
  deleteGroup: (id: string) => call<void>("DELETE", `/admin/groups/${q(id)}`),
  addToGroup: (id: string, member_id: string) => call<void>("POST", `/admin/groups/${q(id)}/members`, { member_id }),
  removeFromGroup: (id: string, member: string) => call<void>("DELETE", `/admin/groups/${q(id)}/members/${q(member)}`),

  stakeholderRoles: () => call<{ items: StakeholderRole[] }>("GET", "/admin/stakeholder-roles"),
  createStakeholderRole: (r: { key: string; name: string }) => call<{ id: string }>("POST", "/admin/stakeholder-roles", r),
  renameStakeholderRole: (id: string, name: string) => call<void>("PATCH", `/admin/stakeholder-roles/${q(id)}`, { name }),

  security: () => call<Security>("GET", "/admin/security"),
  setGroupInheritance: (group_inheritance: boolean) => call<{ security: Security }>("PUT", "/admin/security", { group_inheritance }),
  memberAccess: (id: string) => call<AccessExplain>("GET", `/admin/members/${q(id)}/access`),
  setSecurity: (mfa_policy: Security["mfa_policy"]) => call<{ security: Security; sessions_closed: number }>("PUT", "/admin/security", { mfa_policy }),

  audit: (f: AuditFilter, before?: number) => {
    const p = new URLSearchParams();
    for (const [k, v] of Object.entries(f)) if (v) p.set(k, v);
    if (before) p.set("before", String(before));
    p.set("limit", "100");
    return call<AuditPage>("GET", `/audit?${p.toString()}`);
  },
  verifyAudit: () => call<ChainStatus>("GET", "/audit/verify"),
};

// ---------------------------------------------------------------------------
// MFA (profilo e login) e link monouso
// ---------------------------------------------------------------------------

export type MfaStatus = {
  available: boolean;
  enabled: boolean;
  enabled_at: string | null;
  recovery_codes_left: number;
  required: boolean;
  session_mfa: boolean;
};
export type MfaSetup = { secret: string; uri: string; qr_svg: string };
export type LoginResult = Me | { mfa_required: true; mfa_setup: boolean };

export function needsMfa(r: LoginResult): r is { mfa_required: true; mfa_setup: boolean } {
  return (r as { mfa_required?: boolean }).mfa_required === true;
}

export const mfaApi = {
  login: (email: string, password: string) => call<LoginResult>("POST", "/auth/login", { email, password }),
  verify: (code: string) => call<Me>("POST", "/auth/mfa/verify", { code }),
  challengeSetup: () => call<MfaSetup>("POST", "/auth/mfa/setup"),
  challengeConfirm: (code: string) => call<{ me: Me; recovery_codes: string[] }>("POST", "/auth/mfa/setup/confirm", { code }),

  status: () => call<MfaStatus>("GET", "/me/mfa"),
  setup: () => call<MfaSetup>("POST", "/me/mfa/setup"),
  enable: (code: string) => call<{ recovery_codes: string[] }>("POST", "/me/mfa/enable", { code }),
  disable: (code: string) => call<void>("POST", "/me/mfa/disable", { code }),
  recoveryCodes: (code: string) => call<{ recovery_codes: string[] }>("POST", "/me/mfa/recovery-codes", { code }),
};

export type InviteInfo = { tenant: string; display_name: string; email: string; needs_password: boolean; sso_only: boolean; expires_at: string };

export const linksApi = {
  invitation: (token: string) => call<InviteInfo>("GET", `/auth/invitations/${q(token)}`),
  accept: (token: string, body: { display_name: string; password: string }) => call<void>("POST", `/auth/invitations/${q(token)}/accept`, body),
  reset: (token: string) => call<{ email: string; expires_at: string }>("GET", `/auth/password-reset/${q(token)}`),
  setPassword: (token: string, password: string) => call<void>("POST", `/auth/password-reset/${q(token)}`, { password }),
};

// ---------------------------------------------------------------------------
// Webhook
// ---------------------------------------------------------------------------

export type WebhookEvent = { key: string; name: string; description: string };
export type Webhook = {
  id: string;
  name: string;
  url: string;
  events: string[];
  filter: string;
  active: boolean;
  secret_hint: string;
  created_at: string;
  updated_at: string;
  last_status: "pending" | "delivered" | "failed" | null;
  last_delivery_at: string | null;
  failing: number;
  pending: number;
};
export type WebhookInput = { name: string; url: string; events: string[]; filter: string; active: boolean };
export type Delivery = {
  id: string;
  event_id: string;
  event: string;
  status: "pending" | "delivered" | "failed";
  attempts: number;
  response_status: number | null;
  last_error: string | null;
  duration_ms: number | null;
  next_attempt_at: string | null;
  last_attempt_at: string | null;
  created_at: string;
  redelivery_of: string | null;
  body?: string;
};

export const webhookApi = {
  list: () => call<{ items: Webhook[]; events: WebhookEvent[]; version: number; allow_private: boolean }>("GET", "/admin/webhooks"),
  create: (w: WebhookInput) => call<{ id: string; secret: string }>("POST", "/admin/webhooks", w),
  update: (id: string, w: WebhookInput) => call<void>("PATCH", `/admin/webhooks/${q(id)}`, w),
  remove: (id: string) => call<void>("DELETE", `/admin/webhooks/${q(id)}`),
  rotate: (id: string) => call<{ secret: string }>("POST", `/admin/webhooks/${q(id)}/secret`),
  ping: (id: string) => call<{ delivery_id: string }>("POST", `/admin/webhooks/${q(id)}/ping`),
  deliveries: (id: string) => call<{ items: Delivery[] }>("GET", `/admin/webhooks/${q(id)}/deliveries`),
  redeliver: (deliveryID: string) => call<{ delivery_id: string }>("POST", `/admin/webhook-deliveries/${q(deliveryID)}/redeliver`),
};

/** Indirizzo completo di un link monouso (con REGIA_BASE_URL arriva già dal server). */
export function absoluteLink(l: OneTimeLink): string {
  return l.url ?? window.location.origin + l.path;
}
