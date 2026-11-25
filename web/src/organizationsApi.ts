// Organizzazioni (tenant) dall'interfaccia, solo per il superamministratore: vedi internal/api/platform.go.
import { call } from "./api";

export type OrgRow = {
  id: string;
  slug: string;
  name: string;
  language: "it" | "en";
  base_currency: string;
  members: number;
  created_at: string;
};

export type NewOrg = { name: string; slug: string; language: "it" | "en"; base_currency: string; admin_email?: string };

export type NewOrgResult = {
  tenant_id: string;
  slug: string;
  name: string;
  /** Presente se si è chiesto di invitare un altro amministratore. */
  invite?: { email: string; emailed: boolean; link: { path: string; url?: string; expires_at: string } | null };
  invite_error?: boolean;
};

export const organizationsApi = {
  list: () => call<{ items: OrgRow[] }>("GET", "/platform/tenants"),
  create: (body: NewOrg) => call<NewOrgResult>("POST", "/platform/tenants", body),
};
