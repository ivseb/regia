# Identità aziendale: OIDC completo, SCIM, esterni a zero ruoli, scalata chiusa

La guida per l'amministratore è in [docs/identita-aziendale.md](../identita-aziendale.md). Il codice nuovo sta nei punti qui sotto.
- **Migrazioni:** `0102_identita_aziendale.sql`, `0103_scim.sql`.
- **Regole:** `internal/platform/admin/idp.go`, `scim.go`, `escalation.go`.
- **OIDC:** `internal/platform/auth/oidc.go`.
- **HTTP:** `internal/api/idp.go`, `scim.go`.
- **Interfaccia:** `web/src/pages/AdminIdentity.tsx`.

## Dati (0102, 0103)
- **`tenant_idp_settings`**, una riga per organizzazione: `provisioning`, `allowed_domains`, `groups_claim` (predefinito `groups`). Nessuna riga vale «tutto spento».
- **`idp_groups`**: i gruppi dell'IdP, con `external_id` unico senza distinzione di maiuscole e `source` scim | oidc | manual.
  - `idp_group_members` lega i gruppi dell'IdP ai membri.
  - `idp_group_mappings` lega un gruppo dell'IdP a un gruppo di Regia.
  - Un gruppo di Regia che è destinazione di una mappatura è **governato**: `AddGroupMember`, `RemoveGroupMember` e `AddMember` con quel gruppo rispondono 409 `group.idp_managed`. `GET /admin/groups` porta `idp_managed`.
- **`members`**, colonne nuove:
  - `origin`: admin | oidc | scim;
  - `scim_external_id` e `scim_user_name` (unico per tenant), che si svuotano all'anonimizzazione;
  - `suspended_source`: admin | idp. Il vincolo `members_suspended_source_ck` la vuole valorizzata **se e solo se** `status = 'suspended'`, quindi ogni sospensione passa da `admin.suspendMember(…, source)`.
- **`idp_reconcile(membro)`**, sotto RLS: porta l'appartenenza ai gruppi governati a coincidere con i gruppi dell'IdP mappati, e restituisce i gruppi aggiunti e tolti. Dopo un riallineamento con modifiche si chiama sempre `ensureAdmin`.
- **Funzioni `security definer`**, usate prima di sapere il tenant:
  - `idp_provisioning_targets()`: le organizzazioni attive con la creazione accesa, i loro domini, il claim e le chiavi mappate;
  - `scim_token_use(hash)`: tenant e token. L'ultimo uso si aggiorna al più una volta al minuto.
- **`scim_tokens`**: solo lo sha256 e un suggerimento (`scim_xxxx`). Al massimo 5 token attivi. «SCIM attivo» vuol dire che esiste un token non revocato.

## Login OIDC
`auth.Identity` porta `Name` e tutti i `Claims`. Il metodo `Groups(claim)` legge il claim dei gruppi:
- accetta un elenco o una stringa singola;
- tiene al massimo 500 valori, lunghi al più 300 caratteri;
- restituisce `present = false` se il claim manca, se è nullo, se ha un tipo inatteso o se `_claim_names` lo dichiara in overage.

Il login segue questi passi:
1. Si collega l'account, come prima.
2. Se l'account non c'è e `OIDCHook.Eligible` dice di sì, lo si crea. L'audit registra `auth.account_provision`.
3. `OIDCHook.AfterLogin` (`admin.Service`) crea i membri mancanti nelle organizzazioni che accolgono l'identità e registra `member.provision`.
4. Poi, in ogni organizzazione dove il membro è attivo e SCIM non è attivo, riallinea i gruppi dal claim e registra `member.idp_sync`. Un riallineamento che lascerebbe l'organizzazione senza amministratori si salta, e il login prosegue.

Il gancio lo collega `api.New` con `Auth.SetOIDCHook`.

## SCIM (`/scim/v2`)
- È servito fuori da `withPrincipal`: `Server.Handler` lo instrada prima, sempre dentro `base`, con log, intestazioni e controllo dell'origine (il Bearer lo esenta).
- Risponde con `application/scim+json` e accetta qualunque Content-Type JSON. Il corpo è al massimo 2 MB.
- Risorse:
  - `ServiceProviderConfig`, `ResourceTypes`, `Schemas`;
  - `Users`: GET elenco/filtro/uno, POST, PUT, PATCH, DELETE;
  - `Groups`: lo stesso, più `excludedAttributes=members`.
- Filtri: solo `attributo eq "valore"`; il resto è 400 `invalidFilter`. Paginazione con `startIndex` (da 1) e `count` (al massimo 200).
- PATCH legge la risorsa, applica le operazioni in memoria e chiama il replace. Accetta:
  - i percorsi `active`, `userName`, `displayName`, `externalId`, `name.*`, `emails[...]`;
  - le operazioni senza percorso, con valore oggetto;
  - `members[value eq "…"]` per i gruppi;
  - i booleani come stringhe, come li manda Entra ID.
- Comportamento:
  - DELETE di un utente = `active: false` = sospensione con `suspended_source = idp`;
  - `active: true` riattiva solo le sospensioni dell'IdP;
  - l'ultimo amministratore attivo non si sospende (409).
- Un `POST /Groups` con un `externalId` (o `displayName`) già presente come gruppo `manual` o `oidc` lo adotta. Se il gruppo è già `scim`, la risposta è 409 `uniqueness`.

## Scalata chiusa (`escalation.go`)
- `memberPerms` calcola i permessi di tenant effettivi, come `auth.loadTenant`. `missing(have, want)` dice cosa manca; `*` copre tutto.
- `canGrant(perms)` vale per:
  - `Assign` e `Unassign` (letto prima di togliere);
  - `CreateRole`, `UpdateRole` (prima e dopo), `DeleteRole`;
  - `AddMember`, con i ruoli e i gruppi scelti.
- `canGrantGroups(gruppi)` usa i ruoli dei gruppi e, con l'ereditarietà, dei loro antenati. Vale per:
  - `AddGroupMember`, `RemoveGroupMember`, `DeleteGroup`;
  - il cambio del padre (vecchio e nuovo padre, solo con l'ereditarietà accesa);
  - `CreateMapping`, `DeleteMapping`, `DeleteIdPGroup`.
- `canActOn(membro)` chiede di avere almeno i permessi del membro su cui si agisce. Vale per `Suspend`, `Reactivate`, `UpdateMember` (tipo), `PasswordLink`, `ResendInvitation` e `ResetMFA`.
- Errore: 403 `admin.escalation`, con i permessi mancanti nel `detail`.

## Esterni a zero ruoli
- **`AddMember`:** un membro `standard` senza ruoli scelti non riceve nulla. Il richiedente del portale riceve ancora «Richiedente».
- **`UpdateMember`, da richiedente a membro:** toglie «Richiedente» e non aggiunge «Lettore».
- **Membri esistenti:** la migrazione non li tocca.
  - `Member.only_viewer` segnala chi ha come unico ruolo «Lettore» diretto.
  - `GET /admin/idp` porta il conteggio (`only_viewer`) e i ruoli dati a «Tutti i membri» (`all_members_roles`), che arrivano anche agli esterni.

## API di amministrazione
Letture con `tenant.admin`; modifiche con una sessione.
- `GET /api/v1/admin/idp`: impostazioni, gruppi dell'IdP con le mappature, token, `scim_active`, `scim_base_url`, `oidc`, avvisi.
- `PUT /api/v1/admin/idp/settings` `{"provisioning", "allowed_domains", "groups_claim"}`.
- `POST /api/v1/admin/idp/groups` `{"external_id", "display_name"}`, `DELETE /api/v1/admin/idp/groups/{id}`. I gruppi SCIM non si cancellano da qui: 409 `idp.group_scim`.
- `POST /api/v1/admin/idp/mappings` `{"idp_group_id", "group_id"}`: riallinea subito, e chi stava a mano nel gruppo e non è nel gruppo dell'IdP esce.
- `DELETE /api/v1/admin/idp/mappings/{id}`: se il gruppo resta governato si riallinea; altrimenti torna manuale con i membri che ha.
- `POST /api/v1/admin/idp/scim-tokens` `{"name"}` restituisce `{"token", "item"}` una volta sola. `DELETE /api/v1/admin/idp/scim-tokens/{id}` revoca.

## Test
- **Go:**
  - `internal/itest/identita_aziendale_test.go`: scalata, OIDC con l'IdP finto e i gruppi, SCIM con un client di prova e l'isolamento fra organizzazioni;
  - `internal/platform/auth/oidc_groups_test.go`;
  - `internal/platform/admin/escalation_test.go`.
- **e2e:** `web/e2e/identita-aziendale.spec.ts` (`pnpm e2e e2e/identita-aziendale.spec.ts --reporter=dot`).
