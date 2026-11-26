import type { Translation } from "../types";
import type it from "../it/organizzazioni";

export default {
  "organizzazioni.path": "platform / organizations",
  "organizzazioni.title": "Organizations",
  "organizzazioni.gestisci": "Manage organizations",
  "organizzazioni.riservato": "This page is only for the instance's super administrator.",
  "organizzazioni.intro": "An organization is a separate space with its own people, data and settings: nothing in one is visible in the others. When you create one you become its administrator with your own account.",
  "organizzazioni.entra": "Enter",

  "organizzazioni.nuova.title": "New organization",
  "organizzazioni.nuova.nome": "Name",
  "organizzazioni.nuova.slug": "Identifier",
  "organizzazioni.nuova.slugHelp": "Lowercase letters, digits and hyphens (2 to 63 characters). It cannot be changed later.",
  "organizzazioni.nuova.lingua": "Language",
  "organizzazioni.nuova.linguaHelp": "The language of the starting names of types, phases, roles and processes.",
  "organizzazioni.nuova.valuta": "Base currency",
  "organizzazioni.nuova.altroAdmin": "Another administrator (optional)",
  "organizzazioni.nuova.altroAdminHelp": "If you write an address, that person is invited as an administrator.",
  "organizzazioni.nuova.crea": "Create organization",
  "organizzazioni.nuova.creando": "Creating…",

  "organizzazioni.fatto.title": "“{name}” is ready",
  "organizzazioni.fatto.testo": "You are already an administrator: enter to get started. We take you to “Start here”, where you can also pick a starting template for your type of company.",
  "organizzazioni.fatto.altra": "Create another organization",

  "organizzazioni.persone.title": "How to bring people in",
  "organizzazioni.persone.passo1": "Enter the organization with “Enter”.",
  "organizzazioni.persone.passo2": "Open Configuration → People and access.",
  "organizzazioni.persone.passo3": "Press “Add person”, choose “Invite”, write the email and the role: the person receives a link to sign in.",
  "organizzazioni.persone.nota": "Anyone who already has an account on this instance keeps it: with the same email they also join the new organization and switch between them from the menu at the top.",
  "organizzazioni.persone.vai": "Enter and open People and access",

  "organizzazioni.invito.inviato": "Invitation emailed to {email}.",
  "organizzazioni.invito.copia": "Invitation ready for {email}: this instance does not send email, so copy the link and hand it over yourself.",
  "organizzazioni.invito.link": "Invitation link",
  "organizzazioni.invito.errore": "The organization was created, but inviting the other administrator failed: you can retry from People and access.",

  "organizzazioni.elenco.title": "Organizations on this instance",
  "organizzazioni.elenco.persone": {"one": "{count} person", "other": "{count} people"},
  "organizzazioni.elenco.attiva": "You are here",
  "organizzazioni.stato.attiva": "Active",
  "organizzazioni.stato.sospesa": "Suspended",
  "organizzazioni.stato.etichetta": "Status",
  "organizzazioni.rinomina": "Rename",
  "organizzazioni.rinomina.nome": "Name of the organization {name}",
  "organizzazioni.rinomina.salva": "Save",
  "organizzazioni.rinomina.annulla": "Cancel",
  "organizzazioni.sospendi": "Suspend",
  "organizzazioni.sospendi.sicuro": "Yes, suspend",
  "organizzazioni.sospendi.effetto": "Members will not be able to enter until you reactivate it. The data stays: nothing is deleted.",
  "organizzazioni.sospendi.stessa": "You can't suspend the organization you are working in: enter another one.",
  "organizzazioni.riattiva": "Reactivate",
  "organizzazioni.sospesaTra": "{name} (suspended)",

  "organizzazioni.sospesa.title": "Organization suspended",
  "organizzazioni.sospesa.testo": {
    "one": "The organization “{name}” is suspended. Contact the instance administrator.",
    "other": "The organizations {name} are suspended. Contact the instance administrator.",
  },

  "organizzazioni.elenco.nonMembro": "You are not a member",
} satisfies Translation<typeof it>;
