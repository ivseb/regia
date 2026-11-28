# Identità aziendale: ingressi e gruppi dall'IdP

Guida per chi amministra un'organizzazione in Regia. In questa configurazione è l'IdP aziendale (Entra ID, Okta, Kanidm o un altro provider OIDC) a decidere chi entra e in quali gruppi sta. Regia decide cosa può fare ogni gruppo.

Si configura in **Configurazione → Persone e accessi → Identità aziendale**. Serve il permesso «Amministrare l'organizzazione».

## Il modello in breve

- **L'ingresso passa dall'OIDC dell'IdP.** L'accesso unico si configura una volta per l'istanza, con le variabili `REGIA_OIDC_*` (vedi [esercizio](sviluppo/esercizio.md)). Con `REGIA_OIDC_ONLY=true` la password resta solo agli amministratori di piattaforma, per le emergenze.
- **Nessuna registrazione libera.** Un account nasce in tre modi:
  - lo crea un amministratore;
  - lo crea l'IdP con SCIM;
  - si crea al primo accesso, se l'organizzazione lo accetta (vedi sotto).
- **Gli esterni sono sicuri per default.** Chi entra senza gruppi che diano un ruolo riceve **zero ruoli** e vede solo gli elementi su cui ha un ruolo, per esempio come stakeholder o contatto del fornitore. «Lettore» non si dà più per default, nemmeno negli inviti.
- **La mappatura è esplicita.** Un gruppo dell'IdP conta solo se lo si mappa su un gruppo di Regia: un gruppo non mappato non dà nulla. I ruoli si danno al gruppo di Regia, come sempre.
- **I gruppi mappati sono governati dall'IdP.** La loro appartenenza la decide solo l'IdP e non si cambia a mano, né dall'interfaccia né dall'API. I ruoli del gruppo restano gestiti in Regia.
- **La sospensione è una sola.** Che arrivi dall'IdP o da un amministratore, il membro è «sospeso»: non entra, i suoi token API sono revocati e le sue sessioni chiuse. Regia ricorda chi l'ha deciso.
  - L'IdP riattiva solo chi aveva sospeso lui.
  - Chi è stato sospeso da un amministratore lo riattiva un amministratore.
- **Nessuno dà diritti che non ha.** Chi amministra può dare o togliere solo ruoli i cui permessi ha anche lui. Lo stesso vale per le azioni su chi ha più permessi: sospenderlo, mandargli un link per la password, togliergli l'MFA. Così un ruolo personalizzato con «Amministrare l'organizzazione» non si promuove ad amministratore pieno.

## Primo accesso

Con **«Crea il membro al primo accesso con l'IdP»** acceso, chi entra con l'IdP e non è ancora membro diventa membro, purché valgano tutte e due le condizioni:
- l'IdP ha verificato la sua email (`email_verified`);
- il dominio dell'email è tra i **domini ammessi**, oppure la persona è in almeno un gruppo dell'IdP **mappato** in questa organizzazione.

Il membro nasce attivo, senza ruoli diretti, e i gruppi mappati gli danno subito i loro ruoli. Chi era già membro, anche sospeso, resta com'è: il primo accesso non riattiva nessuno.

Sulla stessa istanza possono stare più organizzazioni con lo stesso IdP. Ognuna accoglie solo le persone dei suoi domini o dei suoi gruppi mappati.

## Gruppi: claim o SCIM

I gruppi arrivano da una sola fonte per volta.
- **Con un token SCIM attivo fa fede SCIM.** L'IdP aggiorna i gruppi appena cambiano, e il claim al login si ignora.
- **Senza SCIM vale il claim dei gruppi dell'ID token**, letto a ogni accesso. Il nome del claim si imposta nella scheda; di solito è `groups`.
  - Il claim **presente** (anche vuoto) riallinea l'appartenenza ai gruppi governati: chi non è più nel gruppo dell'IdP ne esce.
  - Il claim **assente**, o spostato altrove dall'IdP perché troppo grande (l'«overage» di Entra ID), non cambia nulla.
  - Senza SCIM la revoca arriva **all'accesso successivo**. Per le uscite immediate serve SCIM.

I gruppi visti nel claim o arrivati da SCIM compaiono nella scheda, con il numero di persone, pronti da mappare. Se ne può anche scrivere uno a mano, con l'identificativo esatto che usa l'IdP, per mapparlo prima del primo accesso. Quando poi arriva da SCIM, il gruppo scritto a mano si adotta, e le mappature restano.

La regola dell'ultimo amministratore vale anche per l'IdP. Se un riallineamento lascerebbe l'organizzazione senza amministratori attivi, non si applica: SCIM risponde 409, il login prosegue.

## SCIM 2.0

1. Nella scheda, **Crea token**: il token (`scim_…`) si mostra una volta sola. Si possono avere al massimo 5 token attivi.
2. Nell'IdP si configura il provisioning SCIM:
   - URL di base: quello mostrato nella scheda (`https://<istanza>/scim/v2`);
   - autenticazione: token segreto o «Bearer».
3. Utenti:
   - **creazione**: membro senza ruoli. Se esiste già un account con quella email lo si riusa.
   - **`active: false` o cancellazione**: sospensione, mai cancellazione. Le registrazioni e la storia restano.
   - **`active: true`**: riattivazione, solo se la sospensione era dell'IdP.
4. Gruppi: entrano tra i gruppi dell'IdP e contano solo se mappati. SCIM non crea gruppi di Regia e non assegna ruoli.

Sono supportati i filtri `eq` su `userName`, `externalId`, `emails.value` e `id` per gli utenti, e su `displayName`, `externalId` e `id` per i gruppi. PATCH accetta sia la forma con percorso sia quella con valore oggetto; le operazioni in blocco (bulk) non ci sono. Ogni operazione finisce nel registro di audit (`scim.user.*`, `scim.group.*`) con il token come autore.

## Entra ID

- **Accesso:** un'app registrata con il redirect `https://<istanza>/api/v1/auth/oidc/callback`.
  - Per avere il claim dei gruppi, in «Token configuration» si aggiunge il **groups claim**. Entra manda gli **object ID** dei gruppi, ed è con quelli che si mappa.
  - Con molti gruppi conviene «Groups assigned to the application», per non superare il limite oltre il quale Entra non manda più il claim.
- **Provisioning:** nell'app aziendale (Enterprise application), sezione Provisioning, modalità automatica:
  - Tenant URL: l'URL SCIM;
  - Secret Token: il token.
  - La mappatura predefinita va bene: `userPrincipalName → userName` e `objectId → externalId`. Se l'UPN non coincide con l'email, va mappato `mail` su `emails[type eq "work"].value`.
- Entra manda `active` come testo (`"False"`): Regia lo accetta.

## Okta

- **Accesso:** un'app OIDC «Web» con il redirect della callback. Nella scheda «Sign On» si aggiunge un **Groups claim** con il nome scelto (per esempio `groups`) e un filtro sui gruppi da passare. Okta manda i nomi dei gruppi.
- **Provisioning:** un'app SCIM 2.0 (o l'opzione SCIM dell'app):
  - Base URL: l'URL SCIM;
  - autenticazione: «HTTP Header», con il token come Bearer.
  - Si attivano «Create Users», «Update User Attributes», «Deactivate Users» e i **Push Groups** per i gruppi da mandare.

## Kanidm e altri IdP OIDC

- **Accesso:** un client OAuth2 con PKCE e il redirect della callback. Lo scope `groups` aggiunge il claim dei gruppi, con i nomi nella forma `gruppo@dominio`. È quella forma che si mappa.
- **Provisioning:** dove l'IdP non offre un client SCIM verso le applicazioni, vale il claim dei gruppi al login. Le uscite si chiudono disattivando la persona nell'IdP: non potrà più entrare, e le sue sessioni aperte scadono da sole.

## Domande dell'auditor

- **Chi ha accesso a cosa:** in Persone e accessi, per ogni membro, «Da dove arrivano i permessi» mostra la catena completa, per esempio: gruppo dell'IdP → gruppo di Regia → ruolo → permesso.
- **Chi l'ha deciso:** il registro di audit riporta per ogni evento chi l'ha fatto e da dove:
  - creazioni: `member.provision`, `scim.user.create`;
  - sospensioni: `member.suspend` con la fonte, `scim.user.suspend`;
  - riallineamenti: `member.idp_sync`;
  - mappature e token: `idp.mapping.*`, `scim.token.*`.
- **Gli inviti del passato:** l'avviso «N persone hanno solo Lettore diretto» elenca le persone che hanno ricevuto il vecchio default. Va controllato chi di loro ne ha davvero bisogno.
