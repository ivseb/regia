#!/usr/bin/env bash
# Dati di prova per lo sviluppo locale. Credenziali SOLO di sviluppo, uguali per tutti.
# Tenant "demo" e "altro" con lo stesso amministratore (un consulente su due clienti),
# più le persone della fetta verticale del demand nel tenant "demo".
set -euo pipefail
DATA=${DATA:-./data}
# Su un'istanza di prova raggiungibile da fuori si passa una password robusta in REGIA_DEV_PASSWORD.
PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}
export REGIA_ADMIN_PASSWORD=$PW REGIA_MEMBER_PASSWORD=$PW
M=${M:-./bin/regia}
$M tenant create --data "$DATA" --slug demo  --name "Demo S.p.A."   --admin-email admin@demo.test --admin-name "Admin Demo" || true
$M tenant create --data "$DATA" --slug altro --name "Altro Cliente" --admin-email admin@demo.test || true
# Tenant vuoto per provare il primo avvio guidato (nessun dato, solo il provisioning di partenza); solo l'account avvio@.
$M tenant create --data "$DATA" --slug avvio --name "Primo Avvio S.r.l." --admin-email avvio@demo.test --admin-name "Admin Avvio" || true
# Superamministratore dell'istanza (crea le organizzazioni dall'interfaccia): solo l'amministratore del seed.
$M platform-admin grant --data "$DATA" --email admin@demo.test
$M tenant provision --data "$DATA" --slug demo
$M tenant provision --data "$DATA" --slug altro
$M tenant provision --data "$DATA" --slug avvio
# Tenant per provare l'ambiente di prova (la prova «prova-prova» la crea il test e2e); solo l'account prova@.
$M tenant create --data "$DATA" --slug prova --name "Azienda con prova S.r.l." --admin-email prova@demo.test --admin-name "Admin Prova" || true
$M tenant provision --data "$DATA" --slug prova
$M member add --data "$DATA" --tenant demo --email richiedente@demo.test --name "Luca Neri"   --type requester --roles requester || true
$M member add --data "$DATA" --tenant demo --email dm@demo.test          --name "Sara Villa"  --roles editor --groups "Demand management" || true
$M member add --data "$DATA" --tenant demo --email sponsor@demo.test     --name "Marta Ferri" --roles viewer || true
$M member add --data "$DATA" --tenant demo --email pmo@demo.test         --name "Davide Costa" --roles editor,portfolio_manager --groups PMO || true
$M member add --data "$DATA" --tenant demo --email lead@demo.test        --name "Paolo Riva"  --roles viewer || true
$M member add --data "$DATA" --tenant demo --email arch@demo.test        --name "Elena Conti" --roles architect || true
$M member add --data "$DATA" --tenant demo --email finanza@demo.test     --name "Giulia Bassi" --roles editor,finance || true
# Sviluppatori dei team Web e Integrazioni (work management, piano passo 18).
$M member add --data "$DATA" --tenant demo --email dev@demo.test         --name "Anna Moretti" --roles editor || true
$M member add --data "$DATA" --tenant demo --email dev2@demo.test        --name "Marco Ricci"  --roles editor || true
# Team valutatori (piano, passo 4): i gruppi li crea il pacchetto di partenza delle valutazioni.
$M member add --data "$DATA" --tenant demo --email sicurezza@demo.test    --name "Giulia Sala"    --roles assessor --groups Sicurezza || true
$M member add --data "$DATA" --tenant demo --email architetture@demo.test --name "Marco Gallo"    --roles assessor --groups Architetture || true
$M member add --data "$DATA" --tenant demo --email dpo@demo.test          --name "Chiara Russo"   --roles assessor,dpo --groups "Privacy (DPO)" || true
$M member add --data "$DATA" --tenant demo --email compliance@demo.test   --name "Andrea Fontana" --roles assessor --groups Compliance || true
# Rischi e incidenti (piano, passo 20): i gruppi li crea il provisioning del modulo dei rischi.
$M member add --data "$DATA" --tenant demo --email rischi@demo.test       --name "Federica Gatti" --roles risk_manager --groups "Risk management" || true
$M member add --data "$DATA" --tenant demo --email rischi2@demo.test      --name "Luigi Marino"   --roles risk_manager --groups "Risk management" || true
$M member add --data "$DATA" --tenant demo --email incidenti@demo.test    --name "Stefano Longo"  --roles incident_manager --groups "Incident management" || true
# Conformità (piano, passo 20): compliance officer nel gruppo Compliance e auditor in sola lettura.
$M member add --data "$DATA" --tenant demo --email conformita@demo.test   --name "Laura Greco"    --roles compliance_officer --groups Compliance || true
$M member add --data "$DATA" --tenant demo --email revisore@demo.test     --name "Franco Bassi"   --type auditor --roles auditor || true
echo "Accessi di sviluppo (password in questo file): admin@, richiedente@, dm@, sponsor@, pmo@, lead@, arch@, finanza@, dev@, dev2@, sicurezza@, architetture@, dpo@, compliance@, conformita@, revisore@, rischi@, rischi2@, incidenti@demo.test"
