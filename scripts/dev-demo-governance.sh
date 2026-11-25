#!/usr/bin/env bash
# Valutazione e governance di esempio nel tenant "demo" (solo sviluppo e prova):
# voti della scorecard sulle richieste, budget della graduatoria, comitato investimenti
# con segretario e quorum, una seduta verbalizzata il mese scorso e una convocata la
# settimana prossima con l'ordine del giorno proposto dalle approvazioni in attesa.
# Uso: scripts/dev-demo-governance.sh [url]  — dopo dev-demo-planning.sh.
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
for u in admin dm pmo sponsor; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
call admin POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null
me() { py 'd["active_tenant"]["member_id"]' < $T/$1.me; }
ADMIN=$(call admin GET /api/v1/me | py 'd["active_tenant"]["member_id"]'); DM=$(me dm); PMO=$(me pmo); SPONSOR=$(me sponsor)
req_key() { call dm GET "/api/v1/elements?q=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote("tipo: business_request " + sys.argv[1]))' "$1")" | py 'd["items"][0]["key"]'; }

# Stime di massima anche per le richieste ancora in valutazione, così la graduatoria le somma.
estimate() { call dm PATCH /api/v1/demand/requests/$(call dm GET /api/v1/elements/$(req_key "$1") | py 'd["element"]["id"]') \
  "{\"estimate_low_cents\":$(($2*100000)),\"estimate_high_cents\":$(($3*100000))}" > /dev/null; }
estimate "registro informazioni fornitori" 80 120
estimate "conservazione a norma" 25 35
estimate "migrazione data warehouse" 400 550

# Voti: valore strategico, beneficio economico, urgenza normativa, rischio di esecuzione
# (più alto pesa contro), allineamento architetturale. Nota sul primo criterio.
score() {
  local key; key=$(req_key "$1")
  local body; body=$(python3 -c "
import json,sys
keys=['valore_strategico','beneficio_economico','urgenza_normativa','rischio_esecuzione','allineamento_architetturale']
v=[int(x) for x in sys.argv[1].split(',')]
vals={k:{'value':v[i]} for i,k in enumerate(keys)}
vals['valore_strategico']['note']=sys.argv[2]
print(json.dumps({'values':vals,'comment':'Valutazione di esempio'}))" "$2" "$3")
  call ${4:-dm} POST /api/v1/demand/requests/$key/evaluation "$body" > /dev/null
}
score "registro informazioni fornitori" 4,3,5,3,4 "Prerequisito per la vigilanza DORA"
score "incidenti significativi" 4,2,5,3,3 "Obbligo NIS2 con scadenza a gennaio" pmo
score "portale clienti B2B" 5,5,1,4,4 "Leva principale del piano commerciale 2027"
score "migrazione data warehouse" 3,4,1,4,5 "Riduce i costi di esercizio del 30%"
score "firma digitale" 3,4,1,2,4 "Tempi di firma da giorni a minuti" pmo
score "conservazione a norma" 2,2,4,1,3 "Obbligo con scadenza a giugno"

# Budget disponibile per la graduatoria: 600 k€ (la linea taglia a metà portafoglio).
call admin PUT /api/v1/portfolio/settings '{"budget_cents":60000000}' > /dev/null

# Comitato investimenti: presidente l'amministratore, segretario il PMO (senza voto),
# sponsor e demand manager votanti, quorum 2.
BODY=$(call admin GET /api/v1/governance/bodies | py 'next(b["id"] for b in d["items"] if b["kind"]=="investment_committee")')
call admin PATCH /api/v1/governance/bodies/$BODY "{\"name\":\"Comitato investimenti\",\"kind\":\"investment_committee\",
  \"description\":\"Decide le iniziative oltre 250 k€ e le riallocazioni di budget.\",\"chair_id\":\"$ADMIN\",\"secretary_id\":\"$PMO\",\"quorum\":2,
  \"members\":[{\"member_id\":\"$SPONSOR\"},{\"member_id\":\"$DM\"},{\"member_id\":\"$PMO\",\"voting\":false}]}" > /dev/null

# Il portale B2B (320 k€) chiede l'approvazione: sponsor e PMO approvano, poi tocca al comitato.
B2B=$(req_key "portale clienti B2B")
B2B_ID=$(call dm GET /api/v1/elements/$B2B | py 'd["element"]["id"]')
call dm POST /api/v1/elements/$B2B_ID/transitions '{"to":"Approvazione"}' > /dev/null
step() { call admin GET /api/v1/elements/$B2B | py "[s['id'] for s in d['approvals'][0]['steps'] if s['ordinal']==$1][0]"; }
call sponsor POST /api/v1/approvals/steps/$(step 1)/decision '{"decision":"approved","comment":"Coerente con il piano commerciale"}' > /dev/null
call pmo POST /api/v1/approvals/steps/$(step 2)/decision '{"decision":"approved","comment":"Capacità dei team verificata"}' > /dev/null

# Seduta del mese scorso, già verbalizzata: presa d'atto sul piano di conservazione a norma.
read -r PAST NEXT <<< "$(python3 -c "
import datetime as d
from zoneinfo import ZoneInfo
t=d.date.today(); p=(t.replace(day=1)-d.timedelta(days=1)).replace(day=12)
n=t+d.timedelta(days=(1-t.weekday())%7 or 7)
at=lambda x: d.datetime(x.year,x.month,x.day,15,tzinfo=ZoneInfo('Europe/Rome')).isoformat()
print(at(p), at(n))")"
M1=$(call pmo POST /api/v1/governance/meetings "{\"body_id\":\"$BODY\",\"title\":\"Comitato investimenti, seduta ordinaria\",\"scheduled_at\":\"$PAST\",
  \"location\":\"Sala del consiglio, 3° piano\",\"propose\":false}" | py 'd["id"]')
call pmo POST /api/v1/governance/meetings/$M1/items "{\"element\":\"$(req_key "conservazione a norma")\"}" > /dev/null
call pmo POST /api/v1/governance/meetings/$M1/status '{"status":"convened"}' > /dev/null
call pmo POST /api/v1/governance/meetings/$M1/status '{"status":"held"}' > /dev/null
call pmo PUT /api/v1/governance/meetings/$M1/attendance "{\"attendance\":[{\"member_id\":\"$ADMIN\",\"present\":true},{\"member_id\":\"$SPONSOR\",\"present\":true},{\"member_id\":\"$PMO\",\"present\":true}]}" > /dev/null
call pmo POST /api/v1/governance/meetings/$M1/items/$(req_key "conservazione a norma")/decision \
  '{"decision":"informational","note":"Il comitato prende atto del piano di adeguamento e chiede un aggiornamento a febbraio."}' > /dev/null
call admin POST /api/v1/governance/meetings/$M1/minutes/approve > /dev/null

# Seduta di martedì prossimo, convocata: l'ordine del giorno parte dalle richieste in
# attesa del comitato (il portale B2B); la NIS2 si aggiunge a mano per informativa.
M2=$(call pmo POST /api/v1/governance/meetings "{\"body_id\":\"$BODY\",\"title\":\"Comitato investimenti di $(python3 -c "
import datetime as d,sys
m=['gennaio','febbraio','marzo','aprile','maggio','giugno','luglio','agosto','settembre','ottobre','novembre','dicembre']
print(m[d.date.fromisoformat(sys.argv[1][:10]).month-1])" "$NEXT")\",\"scheduled_at\":\"$NEXT\",
  \"location\":\"Sala del consiglio, 3° piano\",\"link\":\"https://meet.example.test/comitato-investimenti\"}" | py 'd["id"]')
call pmo POST /api/v1/governance/meetings/$M2/items "{\"element\":\"$(req_key "incidenti significativi")\"}" > /dev/null
call pmo POST /api/v1/governance/meetings/$M2/status '{"status":"convened"}' > /dev/null
rm -rf $T
echo "Valutazioni e governance di esempio create."
