#!/usr/bin/env bash
# Rischi, fornitori ICT e incidenti di esempio nel tenant "demo" (piano, passo 20; solo
# sviluppo e prova). Fornitori con nomi inventati, contratti (uno in scadenza), rischi in
# varie fasi (uno accettato, uno in accettazione, uno oltre la soglia), un incidente grave
# con le notifiche DORA e NIS2 (la prima inviata) e uno minore già chiuso.
# Uso: scripts/dev-demo-risk.sh [url]  — dopo dev-demo-architecture.sh (lo richiama dev-demo-data.sh).
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
USERS="admin rischi rischi2 incidenti sponsor"
for u in $USERS; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
for u in $USERS; do call $u POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null; done
day() { date -v${1}d +%F 2>/dev/null || date -d "${1} days" +%F; }
ago() { python3 -c "import datetime,sys; print((datetime.datetime.now(datetime.timezone.utc)-datetime.timedelta(hours=float(sys.argv[1]))).strftime('%Y-%m-%dT%H:%M:%SZ'))" "$1"; }
app() { call admin GET "/api/v1/architecture/applications?q=$1" | py "next(a['key'] for a in d['items'] if a['code']=='$1')"; }
member_id() { call admin GET /api/v1/members | py "next(m['id'] for m in d['items'] if m['display_name']=='$1')"; }
id_of() { call admin GET /api/v1/elements/$1 | py 'd["element"]["id"]'; }
move() { call $1 POST /api/v1/elements/$(id_of $2)/transitions "{\"to\":\"$3\"${4:+,\"comment\":\"$4\"}}" > /dev/null; }
PAY=$(app PAY); API=$(app API-CLI); IAM=$(app IAM); DWH=$(app DWH); WEB=$(app WEB-AR); FORN=$(app FORN)
SPONSOR=$(member_id "Marta Ferri"); RISCHI=$(member_id "Federica Gatti"); INCID=$(member_id "Stefano Longo")

# --- Fornitori ICT (nomi inventati) e contratti.
NUV=$(call rischi POST /api/v1/ict/providers "{\"name\":\"Nuvola Alpina\",\"legal_name\":\"Nuvola Alpina S.p.A.\",\"lei\":\"8156009ABCDE12345678\",\"country\":\"IT\",\"provider_type\":\"Infrastruttura cloud\",\"substitutability\":\"difficile\",\"owner_id\":\"$RISCHI\",\"applications\":[\"$API\",\"$WEB\"]}" | py 'd["key"]')
PAG=$(call rischi POST /api/v1/ict/providers "{\"name\":\"Varco Pagamenti\",\"legal_name\":\"Varco Pagamenti S.r.l.\",\"country\":\"IT\",\"provider_type\":\"Gateway di pagamento\",\"substitutability\":\"media\",\"applications\":[\"$PAY\"]}" | py 'd["key"]')
ARC=$(call rischi POST /api/v1/ict/providers "{\"name\":\"Archivio Boreale\",\"legal_name\":\"Archivio Boreale GmbH\",\"country\":\"DE\",\"provider_type\":\"Conservazione documentale\",\"substitutability\":\"facile\",\"applications\":[\"$DWH\"]}" | py 'd["key"]')
SEG=$(call rischi POST /api/v1/ict/providers "{\"name\":\"Sentinella Rete\",\"legal_name\":\"Sentinella Rete S.r.l.\",\"country\":\"IT\",\"provider_type\":\"SOC gestito\",\"is_intragroup\":false,\"substitutability\":\"media\",\"applications\":[\"$IAM\"]}" | py 'd["key"]')
call rischi POST /api/v1/ict/providers/$NUV/contracts "{\"reference\":\"NA-2024-031\",\"service_type\":\"IaaS e database gestiti\",\"services\":\"Calcolo, archiviazione e database per le API clienti e l'area riservata\",\"functions\":\"Servizi digitali ai clienti\",\"supports_critical_function\":true,\"criticality\":\"critica\",\"start_date\":\"2024-03-01\",\"end_date\":\"$(day +420)\",\"notice_days\":180,\"annual_cost_cents\":38000000,\"currency\":\"EUR\",\"data_locations\":[\"IT\",\"DE\"],\"processing_locations\":[\"IT\"],\"subcontractors\":[{\"name\":\"Centro Dati Lario\",\"country\":\"IT\",\"service\":\"Data center\"}],\"exit_strategy\":\"Migrazione su un secondo fornitore cloud europeo entro nove mesi, con copie dei dati in formato aperto.\",\"exit_tested_on\":\"2026-05-20\",\"governing_law\":\"italiana\",\"applications\":[\"$API\",\"$WEB\"]}" > /dev/null
call rischi POST /api/v1/ict/providers/$PAG/contracts "{\"reference\":\"VP-2023-007\",\"service_type\":\"Gateway di pagamento\",\"services\":\"Autorizzazione e regolamento dei pagamenti con carta\",\"functions\":\"Incassi online\",\"supports_critical_function\":true,\"criticality\":\"critica\",\"start_date\":\"2023-06-01\",\"end_date\":\"$(day +55)\",\"notice_days\":30,\"annual_cost_cents\":12000000,\"currency\":\"EUR\",\"data_locations\":[\"IT\"],\"processing_locations\":[\"IT\",\"IE\"],\"exit_strategy\":\"Secondo gateway già contrattualizzato in prova.\",\"applications\":[\"$PAY\"]}" > /dev/null
call rischi POST /api/v1/ict/providers/$ARC/contracts "{\"reference\":\"AB-2025-112\",\"service_type\":\"Conservazione\",\"services\":\"Conservazione degli estratti del data warehouse\",\"functions\":\"Reportistica direzionale\",\"criticality\":\"bassa\",\"start_date\":\"2025-01-01\",\"end_date\":\"$(day +700)\",\"notice_days\":90,\"data_locations\":[\"DE\"],\"applications\":[\"$DWH\"]}" > /dev/null
call rischi POST /api/v1/ict/providers/$SEG/contracts "{\"reference\":\"SR-2025-004\",\"service_type\":\"Monitoraggio di sicurezza\",\"services\":\"SOC 24 ore su 24 e gestione degli allarmi\",\"functions\":\"Sicurezza degli accessi\",\"criticality\":\"alta\",\"start_date\":\"2025-02-01\",\"end_date\":\"$(day +300)\",\"notice_days\":60,\"data_locations\":[\"IT\"],\"applications\":[\"$IAM\"]}" > /dev/null

# --- Rischi.
R1=$(call rischi POST /api/v1/risks "{\"title\":\"Indisponibilità prolungata del gateway di pagamento\",\"description\":\"Un guasto del fornitore del gateway blocca gli incassi online.\",\"category\":\"Terze parti\",\"inherent_likelihood\":3,\"inherent_impact\":5,\"owner_id\":\"$SPONSOR\",\"targets\":[\"$PAY\",\"$PAG\"]}" | py 'd["key"]')
move rischi $R1 Valutato
call rischi PATCH /api/v1/risks/$R1 '{"residual_likelihood":2,"residual_impact":4,"treatment":"mitigare","treatment_plan":"Secondo gateway attivo in failover, prove trimestrali di commutazione.","treatment_due":"'$(day +45)'","review_due":"'$(day +120)'"}' > /dev/null
move rischi $R1 "In trattamento"
R2=$(call rischi POST /api/v1/risks "{\"title\":\"Concentrazione su un solo fornitore cloud\",\"description\":\"API clienti e area riservata dipendono da un unico fornitore di infrastruttura.\",\"category\":\"Terze parti\",\"inherent_likelihood\":3,\"inherent_impact\":4,\"residual_likelihood\":2,\"residual_impact\":3,\"treatment\":\"accettare\",\"owner_id\":\"$SPONSOR\",\"targets\":[\"$API\",\"$NUV\"]}" | py 'd["key"]')
move rischi $R2 Valutato
call rischi POST /api/v1/risks/$R2/acceptance "{\"until\":\"$(day +365)\",\"comment\":\"Strategia di uscita provata a maggio, costo della doppia infrastruttura non proporzionato.\"}" > /dev/null
STEP=$(call sponsor GET /api/v1/elements/$R2 | py 'next(s["id"] for a in d["approvals"] if a["status"]=="pending" for s in a["steps"] if s["decision"]=="pending")')
call sponsor POST /api/v1/approvals/steps/$STEP/decision '{"decision":"approved","comment":"Accetto per un anno, poi rivalutiamo."}' > /dev/null
R3=$(call rischi POST /api/v1/risks "{\"title\":\"Credenziali amministrative senza secondo fattore sul portale fornitori\",\"category\":\"Identità e accessi\",\"inherent_likelihood\":4,\"inherent_impact\":4,\"residual_likelihood\":4,\"residual_impact\":3,\"treatment\":\"mitigare\",\"treatment_plan\":\"MFA per tutti gli amministratori con la migrazione all'IdP.\",\"treatment_due\":\"$(day +30)\",\"owner_id\":\"$RISCHI\",\"targets\":[\"$FORN\",\"$IAM\"]}" | py 'd["key"]')
move rischi $R3 Valutato
move rischi $R3 "In trattamento"
R4=$(call rischi POST /api/v1/risks "{\"title\":\"Perdita di dati storici del data warehouse\",\"category\":\"Dati\",\"inherent_likelihood\":2,\"inherent_impact\":3,\"residual_likelihood\":1,\"residual_impact\":3,\"treatment\":\"accettare\",\"owner_id\":\"$RISCHI\",\"targets\":[\"$DWH\",\"$ARC\"]}" | py 'd["key"]')
move rischi $R4 Valutato
call rischi2 POST /api/v1/risks/$R4/acceptance "{\"until\":\"$(day +180)\",\"comment\":\"Copia mensile presso il fornitore di conservazione.\"}" > /dev/null
R5=$(call rischi POST /api/v1/risks "{\"title\":\"Ransomware sulle postazioni del back office\",\"category\":\"Sicurezza\",\"inherent_likelihood\":3,\"inherent_impact\":4,\"owner_id\":\"$RISCHI\"}" | py 'd["key"]')

# --- Incidente grave: pagamenti fermi per tre ore, classificato DORA e NIS2.
INC=$(call incidenti POST /api/v1/incidents "{\"title\":\"Pagamenti online non disponibili\",\"description\":\"Il gateway rifiuta le autorizzazioni: i clienti non riescono a pagare dall'area riservata e dall'app.\",\"detected_at\":\"$(ago 5)\",\"clients_affected\":180000,\"clients_affected_pct\":14,\"downtime_minutes\":190,\"economic_impact_cents\":21000000,\"cause\":\"provider\",\"impacted\":[\"$PAY\",\"$WEB\"]}" | py 'd["key"]')
call incidenti POST /api/v1/incidents/$INC/links "{\"target\":\"$R1\"}" > /dev/null
move incidenti $INC Classificato
call incidenti POST /api/v1/incidents/$INC/events "{\"kind\":\"communication\",\"text\":\"Avviso ai clienti sull'area riservata e sui canali social\",\"at\":\"$(ago 4)\"}" > /dev/null
move incidenti $INC "In gestione"
call incidenti POST /api/v1/incidents/$INC/events "{\"kind\":\"contained\",\"text\":\"Commutazione sul gateway secondario\",\"at\":\"$(ago 2)\"}" > /dev/null
FIRST=$(call incidenti GET /api/v1/incidents/$INC | py 'next(n["id"] for n in d["notifications"] if n["scheme_key"]=="dora" and n["stage_key"]=="iniziale")')
call incidenti POST /api/v1/regulatory-notifications/$FIRST/submit "{\"content\":\"Indisponibilità del servizio di pagamento online dalle ore indicate; circa il 14% dei clienti coinvolto; commutazione sul gateway secondario in corso.\",\"reference\":\"DEMO-0001\",\"channel\":\"portale dell'autorità\",\"submitted_at\":\"$(ago 3)\"}" > /dev/null

# --- Incidente minore, chiuso senza postmortem.
MIN=$(call incidenti POST /api/v1/incidents "{\"title\":\"Rallentamento dei cruscotti direzionali\",\"detected_at\":\"$(ago 30)\",\"downtime_minutes\":25,\"cause\":\"system\",\"impacted\":[\"$(app BI)\"]}" | py 'd["key"]')
move incidenti $MIN Classificato
move incidenti $MIN Risolto
move incidenti $MIN Chiuso "Nessun impatto sui clienti, indice ricostruito."

rm -rf $T
echo "Rischi e incidenti di esempio: fornitori $NUV $PAG $ARC $SEG, rischi $R1..$R5 ($R2 accettato, $R4 in accettazione), incidente grave $INC, minore $MIN."
