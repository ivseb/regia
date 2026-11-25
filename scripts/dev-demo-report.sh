#!/usr/bin/env bash
# Avanzamento di esempio nel tenant "demo" (solo sviluppo e prova): status report inviati
# e in bozza, una richiesta con il report in ritardo, il portale con benvenuto e testi
# delle fasi, una richiesta inviata dal portale e una bozza del richiedente.
# Uso: scripts/dev-demo-report.sh [url]  — dopo gli altri script di dev-demo-data.sh.
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
day() { python3 -c "import datetime,sys; print((datetime.date.today()+datetime.timedelta(days=int(sys.argv[1]))).isoformat())" "$1"; }
for u in admin dm sponsor richiedente; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
call admin POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null
req_key() { call dm GET "/api/v1/elements?q=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote("tipo: business_request " + sys.argv[1]))' "$1")" | py 'd["items"][0]["key"]'; }

# Lo script non è ripetibile (i report per lo stesso periodo esistono già): se ci sono, si ferma con un messaggio chiaro.
DORA0=$(req_key "registro informazioni fornitori")
if [ "$(call dm GET /api/v1/demand/requests/$DORA0/status-reports | py 'len(d if isinstance(d,list) else d.get("items",d.get("reports",[])))')" != "0" ]; then
  echo "Status report e portale di esempio già presenti: salto."; rm -rf $T; exit 0
fi

# Il report è atteso dalla valutazione in poi; promemoria al PM tre giorni prima.
call admin PUT /api/v1/report/settings '{"frequency":"biweekly","reminder_days":3,"phases":["Valutazione","Prioritizzazione","Approvazione","Pianificazione","In esecuzione"]}' > /dev/null

# report <chiave> <fine periodo> <semaforo generale o ""> <sintesi> <decisioni> [invia]
report() {
  local r; r=$(call dm POST /api/v1/demand/requests/$1/status-reports "{\"period_end\":\"$2\"}")
  local id; id=$(echo "$r" | py 'd["id"]')
  local body; body=$(echo "$r" | python3 -c "
import json,sys
r=json.load(sys.stdin); a=sys.argv
s=r['sections']; s['summary']=a[2]; s['decisions']=a[3]
out={'version':r['version'],'sections':s}
if a[1]: out['overall']=a[1]
print(json.dumps(out))" "$3" "$4" "$5")
  call dm PATCH /api/v1/status-reports/$id "$body" > /dev/null
  [ "${6:-}" = "invia" ] && call dm POST /api/v1/status-reports/$id/submit > /dev/null
  echo "$id"
}

DORA=$(req_key "registro informazioni fornitori")
FIRST=$(report "$DORA" "$(day -14)" "amber" \
  "Analisi tecnica chiusa sulle tre applicazioni coinvolte. La DPIA è in ritardo presso il DPO: senza esito non si avvia lo sviluppo del portale fornitori." \
  "- Sollecitare il DPO per l'esito della DPIA entro venerdì." invia)
call sponsor POST /api/v1/status-reports/$FIRST/acknowledge > /dev/null || true
report "$DORA" "$(day 0)" "" \
  "Il backend è in ritardo di una settimana; il consumo del budget è in linea. Rischio sulla scadenza di aprile se la DPIA non arriva." \
  "- Autorizzare due giornate di consulenza aggiuntive sul registro." invia > /dev/null

B2B=$(req_key "portale clienti B2B")
report "$B2B" "$(day -20)" "green" "Pianificazione completata, in attesa della decisione del comitato." "" invia > /dev/null
# Il report successivo del portale B2B è scaduto ieri: compare tra i «report in ritardo».
call dm PUT /api/v1/demand/requests/$B2B/status-reports/schedule "{\"frequency\":\"\",\"next_due\":\"$(day -1)\"}" > /dev/null

NIS2=$(req_key "incidenti significativi")
report "$NIS2" "$(day 0)" "" "Bozza da completare con l'esito del comitato." "" > /dev/null

# Portale: benvenuto, testi delle fasi; una richiesta inviata dal portale e una bozza.
call admin PUT /api/v1/portal/settings "$(python3 -c "
import json
print(json.dumps({
  'welcome': 'Qui raccogliamo le **richieste delle unità di business**: nuovi servizi, modifiche, adeguamenti normativi.\n\nRispondiamo entro due giorni lavorativi. Per urgenze scrivi a demand@demo.test.',
  'phase_texts': {'Inviata': 'L\'abbiamo ricevuta: entro due giorni lavorativi il demand management la prende in carico.'},
}))")" > /dev/null
UNIT=$(call richiedente GET /api/v1/portal | py 'next((u["key"] for u in d["units"] if u["name"]=="Commerciale"), "")')
DRAFT=$(call richiedente POST /api/v1/portal/drafts "$(python3 -c "
import json,sys
print(json.dumps({'type':'business_request','title':'Modulo online per i reclami dei clienti','step':3,'data':{
  'description':'Oggi i reclami arrivano solo per email e li registriamo a mano. Vorremmo un modulo nell\'area clienti con lo stato della pratica.',
  'requesting_unit_id':sys.argv[1],'driver':'operational','desired_date':sys.argv[2],'fields':{}}}))" "$UNIT" "$(day 120)")" | py 'd["id"]')
SENT=$(call richiedente POST /api/v1/portal/drafts/$DRAFT/submit | py 'd["key"]')
call richiedente POST /api/v1/elements/$SENT/comments '{"body":"Allego volentieri il processo attuale, se serve."}' > /dev/null
call richiedente POST /api/v1/portal/drafts \
  '{"type":"business_request","title":"Firma elettronica sui moduli di adesione","step":1,"data":{"description":"Da completare.","fields":{}}}' > /dev/null
rm -rf $T
echo "Avanzamento di esempio creato: report su $DORA, $B2B, $NIS2; richiesta dal portale $SENT."
