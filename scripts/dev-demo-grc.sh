#!/usr/bin/env bash
# Conformità di esempio nel tenant "demo" (piano, passo 20; solo sviluppo e prova):
# pacchetti di partenza DORA, NIS2, ISO/IEC 27001, GDPR (abilitati) e AgID/ACN (in bozza),
# entità legale, ricalcolo dell'applicabilità sulle applicazioni del catalogo, controlli
# che coprono requisiti di più normative, evidenze caricate dai responsabili e validate
# dalla compliance (una in scadenza, una da validare), un'evidenza automatica di rilascio,
# una decisione di non applicabilità e un collegamento manuale di un requisito di progetto.
# La verifica TLS del portale B2B (dev-demo-requirements.sh) vale già come evidenza: la voce
# del catalogo dei requisiti dichiara iso27001:A.8.24, dora:Art. 9 e gdpr:Art. 32.
# Uso: scripts/dev-demo-grc.sh [url]  — dopo dev-demo-requirements.sh (lo richiama dev-demo-data.sh).
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
USERS="admin conformita lead arch dm"
for u in $USERS; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
for u in $USERS; do call $u POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null; done
member_id() { call admin GET /api/v1/members | py "next(m['id'] for m in d['items'] if m['display_name']=='$1')"; }
LEAD=$(member_id "Paolo Riva"); ARCH=$(member_id "Elena Conti"); CO=$(member_id "Laura Greco")
in_days() { date -v+${1}d +%F 2>/dev/null || date -d "+$1 days" +%F; }

# Normative dai pacchetti di partenza (con le corrispondenze e le regole sui fatti delle applicazioni).
for k in dora nis2 iso27001 gdpr agid_acn; do
  call conformita POST /api/v1/grc/starters/$k '{"enable":true}' | py "'$k: ' + (d.get('detail') or ('%s' % ((d.get('framework') or {}).get('current') or {}).get('version', 'bozza')))"
done
call admin POST /api/v1/org-units '{"name":"Demo S.p.A.","kind":"legal_entity","code":"DEMO"}' > /dev/null
call conformita POST /api/v1/grc/recompute | py '"Applicabilità: %d righe dalle regole" % d["rows"]'

# Controlli: ciascuno copre requisiti di più normative.
control() { # titolo | responsabile | frequenza | raccolte | requisiti separati da virgola
  python3 -c "import json,sys; a=sys.argv; print(json.dumps({'title':a[1],'text':a[2],'owner_id':a[3],'frequency':a[4],
    'collectors':[c for c in a[5].split(',') if c],'requirements':[{'ref':r.strip().removesuffix(' parziale'),'coverage':'partial' if r.strip().endswith(' parziale') else 'full'} for r in a[6].split(',')]}))" "$@" > $T/ctl.json
  call conformita POST /api/v1/grc/controls "$(cat $T/ctl.json)" | py 'd["key"] if "key" in d else sys.exit("risposta senza key: %s" % d)'
}
activate() { local id; id=$(call conformita GET /api/v1/elements/$1 | py 'd["element"]["id"]'); call conformita POST /api/v1/elements/$id/transitions '{"to":"In esercizio"}' > /dev/null; }
BK=$(control "Backup e prove di ripristino" "Backup giornalieri cifrati in un sito separato; prova di ripristino ogni trimestre con i tempi misurati." "$LEAD" quarterly "" "dora:Art. 12,nis2:Art. 21(2)(c),iso27001:A.8.13")
VU=$(control "Gestione delle vulnerabilità" "Scansione delle dipendenze e delle immagini a ogni build; correzione entro gli SLA per severità." "$ARCH" monthly "devsecops.release" "dora:Art. 9,nis2:Art. 21(2)(e),iso27001:A.8.8")
MFA=$(control "Autenticazione a più fattori" "Secondo fattore obbligatorio per accessi amministrativi e da remoto, tramite l'IdP aziendale." "$LEAD" semiannual "" "nis2:Art. 21(2)(j),iso27001:A.8.5,iso27001:A.8.2 parziale")
CIF=$(control "Cifratura dei dati in transito e a riposo" "TLS 1.2 o superiore su tutti gli endpoint, dischi e backup cifrati, chiavi nel vault." "$ARCH" annual "" "iso27001:A.8.24,gdpr:Art. 32,nis2:Art. 21(2)(h),dora:Art. 9 parziale")
INC=$(control "Gestione degli incidenti" "Procedura di rilevazione, classificazione e risposta con reperibilità e registro degli incidenti." "$LEAD" annual "" "dora:Art. 17,nis2:Art. 21(2)(b),iso27001:A.5.24,iso27001:A.5.26,gdpr:Art. 33")
CON=$(control "Continuità delle funzioni critiche" "Piano di continuità per le applicazioni a supporto di funzioni essenziali, provato ogni anno." "$LEAD" annual "" "dora:Art. 11,iso27001:A.5.30")
POL=$(control "Politica di sicurezza delle informazioni" "Politica approvata dal consiglio di amministrazione e riesaminata ogni anno." "$CO" annual "" "iso27001:A.5.1,nis2:Art. 21(2)(a),dora:Art. 6")
TPR=$(control "Registro dei fornitori ICT" "Censimento dei fornitori ICT con valutazione prima del contratto e strategia di uscita." "$CO" annual "" "dora:Art. 28,nis2:Art. 21(2)(d),iso27001:A.5.19")
for k in $BK $VU $MFA $CIF $INC $CON $POL; do activate $k; done
# Il registro dei fornitori resta in bozza: i suoi requisiti risultano senza controllo.

# Evidenze: carica il responsabile, valida la compliance (mai la stessa persona).
evidence() { # chi | controllo | tipo | titolo | nota | url | perimetro | destinazione | scadenza
  python3 -c "import json,sys; a=sys.argv; d={'controls':[a[1]],'kind':a[2],'title':a[3],'note':a[4],'scope':a[6] or 'tenant'}
d.update({'url':a[5]} if a[5] else {}); d.update({'target':a[7]} if a[7] else {}); d.update({'valid_until':a[8]} if a[8] else {}); print(json.dumps(d))" "${@:2}" > $T/ev.json
  call $1 POST /api/v1/grc/evidence "$(cat $T/ev.json)" | py 'd["key"] if "key" in d else sys.exit("risposta senza key: %s" % d)'
}
validate() { call $1 POST /api/v1/grc/evidence/$2/validate "{\"note\":\"${3:-Controllata}\"}" > /dev/null; }
E1=$(evidence lead $BK note "Verbale della prova di ripristino del trimestre" "Ripristino completo del database clienti in 52 minuti (RTO 120). Esito positivo." "" "" "" "")
validate conformita $E1 "Tempi coerenti con l'RTO dichiarato"
E2=$(evidence arch $VU link "Report delle scansioni del mese" "Nessuna vulnerabilità critica aperta oltre lo SLA." "https://scanner.example.test/report/mensile" "" "" "")
validate conformita $E2
E3=$(evidence lead $MFA link "Configurazione dell'IdP con il secondo fattore obbligatorio" "Criterio di accesso condizionale per gli amministratori." "https://idp.example.test/policy/mfa-admin" "" "" "")
validate conformita $E3
E4=$(evidence arch $CIF note "Inventario dei certificati e delle chiavi" "Tutti gli endpoint pubblici con TLS 1.3; chiavi ruotate ogni 90 giorni." "" "" "" "")
validate conformita $E4
E5=$(evidence conformita $POL note "Delibera di approvazione della politica" "Politica approvata dal CdA; prossimo riesame entro fine anno." "" "" "" "$(in_days 12)")
validate admin $E5 "Delibera allegata al verbale del CdA"
E6=$(evidence lead $CON note "Prova di continuità dell'area clienti" "Scenario di indisponibilità del sito primario, ripartenza sul secondario in 3 ore." "" application WEB-AR "")
validate conformita $E6
E7=$(evidence lead $INC note "Registro degli incidenti del semestre" "Due incidenti minori, nessuno grave; analisi delle cause chiuse." "" "" "" "")
# E7 resta da validare.

# Efficacia valutata dalla compliance.
call conformita POST /api/v1/grc/controls/$BK/assessment '{"effectiveness":"effective","note":"Prove regolari e documentate"}' > /dev/null
call conformita POST /api/v1/grc/controls/$MFA/assessment '{"effectiveness":"partial","note":"Manca ancora sui sistemi on-premise dei pagamenti"}' > /dev/null

# Evidenza automatica di un rilascio (aggancio DevSecOps): si collega al controllo iscritto.
call admin POST /api/v1/grc/evidence/automatic '{"collector":"devsecops.release","external_ref":"api-clienti-3.2.0-prod","title":"Rilascio 3.2.0 di Backend API clienti in produzione","summary":"Controlli della pipeline superati: SCA bloccante senza CVE critiche correggibili.","url":"https://ci.example.test/rilasci/api-clienti-3.2.0","application":"API-CLI"}' > /dev/null

# Non applicabile, con motivazione: condivisione delle informazioni sulle minacce (DORA Art. 45).
ART45=$(call conformita GET /api/v1/grc/frameworks/dora/coverage | py 'next(i["id"] for i in d["items"] if i["ref"]=="Art. 45")')
call conformita PUT /api/v1/grc/requirements/$ART45/applicability '{"items":[{"scope":"tenant","status":"not_applicable","justification":"Nessuna adesione a comunità di condivisione: valutazione nel 2027"}]}' > /dev/null

# Un requisito di progetto inserito a mano collegato a un requisito normativo.
B2B=$(call dm GET "/api/v1/elements?q=$(python3 -c 'import urllib.parse; print(urllib.parse.quote("tipo: business_request portale clienti B2B"))')" | py 'd["items"][0]["key"]')
MAN=$(call dm GET /api/v1/elements/$B2B/requirements | py "next(r['key'] for r in d['rows'] if r['title'].startswith('Manuale operativo'))")
call conformita PUT /api/v1/grc/project-requirements/$MAN/links '{"refs":["iso27001:A.5.37"]}' > /dev/null

call conformita GET /api/v1/grc/dashboard | py '"Conformità: " + ", ".join("%s %d%%" % (f["key"], f["percent"]) for f in d["frameworks"])'
rm -rf $T
echo "Conformità di esempio: controlli $BK $VU $MFA $CIF $INC $CON $POL (in esercizio) e $TPR (bozza), evidenza $E7 da validare, $E5 in scadenza."
