#!/usr/bin/env bash
# Budget e costi di esempio nel tenant "demo" (solo sviluppo e prova): esercizi 2026 e 2027,
# cambi, tariffe per ruolo e per persona, budget approvati (anche una revisione in attesa
# dello sponsor e una proposta in bozza), un trasferimento, voci di costo manuali, ore,
# un import CSV con prova e conferma, l'earned value su un progetto con la sua storia.
# Uso: scripts/dev-demo-finance.sh [url]  — dopo dev-demo-governance.sh.
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; T=$(mktemp -d)
eval "$(grep -m1 '^PW=' "$(dirname "$0")/dev-seed.sh")"   # stessa password di sviluppo del seed
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
for u in admin dm sponsor lead arch; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
call admin POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null
me() { py 'd["active_tenant"]["member_id"]' < $T/$1.me; }
LEAD=$(me lead); ARCH=$(me arch); DM=$(me dm)
req_key() { call dm GET "/api/v1/elements?q=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote("tipo: business_request " + sys.argv[1]))' "$1")" | py 'd["items"][0]["key"]'; }
FIRMA=$(req_key "firma digitale"); NIS2=$(req_key "incidenti significativi"); DORA=$(req_key "registro informazioni fornitori"); B2B=$(req_key "portale clienti B2B")

OV=$(call admin GET /api/v1/finance/overview)
cat_id() { echo "$OV" | py "next(c['id'] for c in d['categories'] if c['code']=='$1')"; }
PERS=$(cat_id PERS); SVIL=$(cat_id SVIL); CONS=$(cat_id CONS); LIC=$(cat_id LIC); CLOUD=$(cat_id CLOUD)
FS=$(echo "$OV" | py 'd["funding_sources"][0]["id"]')

# Lo script non è ripetibile (gli esercizi si sovrapporrebbero): se i dati ci sono già, si ferma con un messaggio chiaro.
if [ "$(echo "$OV" | py 'len(d.get("fiscal_years") or [])')" != "0" ]; then
  echo "Budget e costi di esempio già presenti: salto (per rifarli servono dati nuovi con dev-seed.sh)."; rm -rf $T; exit 0
fi

# Esercizi: il 2026 in corso, il 2027 in pianificazione (alimenta la graduatoria con il suo plafond).
FY26=$(call admin POST /api/v1/finance/fiscal-years '{"name":"2026","start_date":"2026-01-01","end_date":"2026-12-31","envelope_cents":120000000}' | py 'd["id"]')
FY27=$(call admin POST /api/v1/finance/fiscal-years '{"name":"2027","start_date":"2027-01-01","end_date":"2027-12-31","envelope_cents":60000000}' | py 'd["id"]')
call admin PATCH /api/v1/finance/fiscal-years/$FY27 '{"status":"planning"}' >/dev/null
call admin PUT /api/v1/finance/settings "{\"portfolio_fiscal_year_id\":\"$FY27\",\"portfolio_basis\":\"envelope\"}" >/dev/null

# Cambi verso l'euro.
for x in "USD 2026-01-02 0.9312" "USD 2026-07-01 0.9195" "USD 2026-10-01 0.9268" "GBP 2026-01-02 1.1735" "CHF 2026-01-02 1.0642"; do
  set -- $x; call admin PUT /api/v1/finance/exchange-rates "{\"currency\":\"$1\",\"valid_on\":\"$2\",\"rate_to_base\":$3}" >/dev/null
done

# Tariffe per ruolo (con un aumento a luglio) e una tariffa personale.
for r in "backend 5200 2026-01-01 2026-06-30" "backend 5600 2026-07-01 " "frontend 4800 2026-01-01 " "analyst 6000 2026-01-01 " "architect 7500 2026-01-01 "; do
  set -- $r; to=${4:-}; [ -n "$to" ] && to="\"$to\"" || to=null
  call admin POST /api/v1/finance/rates "{\"role_key\":\"$1\",\"hourly_cents\":$2,\"valid_from\":\"$3\",\"valid_to\":$to}" >/dev/null
done
call admin POST /api/v1/finance/rates "{\"member_id\":\"$ARCH\",\"hourly_cents\":8200,\"valid_from\":\"2026-01-01\",\"note\":\"Contratto senior\"}" >/dev/null

# Budget: bozza → invio (politica @budget) → decisione dello sponsor (fino a 50 k€).
decide() { # $1 chiave della richiesta, $2 decisione, $3 commento
  local step; step=$(call sponsor GET /api/v1/elements/$1 | py 'next((s["id"] for r in d["approvals"] if r["status"]=="pending" for s in r["steps"] if s["can_decide"]), "")')
  [ -n "$step" ] && call sponsor POST /api/v1/approvals/steps/$step/decision "{\"decision\":\"$2\",\"comment\":\"$3\"}" >/dev/null || true
}
budget() { # $1 chiave, $2 esercizio, $3 righe JSON → id della bozza
  call admin POST /api/v1/finance/budgets "{\"element\":\"$1\",\"fiscal_year_id\":\"$2\",\"note\":\"Proposta iniziale\",\"lines\":$3}" | py 'd["id"]'
}
submit() { call admin POST /api/v1/finance/budgets/$1/submit >/dev/null; }

BF=$(budget $FIRMA $FY26 "[{\"cost_category_id\":\"$PERS\",\"amount_cents\":2200000,\"funding_source_id\":\"$FS\"},{\"cost_category_id\":\"$LIC\",\"amount_cents\":1200000},{\"cost_category_id\":\"$CONS\",\"amount_cents\":800000}]")
submit $BF; decide $FIRMA approved ""
BN=$(budget $NIS2 $FY26 "[{\"cost_category_id\":\"$PERS\",\"amount_cents\":2500000},{\"cost_category_id\":\"$CONS\",\"amount_cents\":2000000}]")
submit $BN; decide $NIS2 approved ""
BD=$(budget $DORA $FY26 "[{\"cost_category_id\":\"$PERS\",\"amount_cents\":1800000},{\"cost_category_id\":\"$CONS\",\"amount_cents\":2400000}]")
submit $BD; decide $DORA approved ""
# Revisione della NIS2: copiata dall'approvata, alzata e mandata allo sponsor (resta in attesa).
RN=$(call admin POST /api/v1/finance/budgets "{\"element\":\"$NIS2\",\"fiscal_year_id\":\"$FY26\",\"kind\":\"revised\"}" | py 'd["id"]')
call admin PUT /api/v1/finance/budgets/$RN/lines "{\"note\":\"Consulenza aggiuntiva per il piano di risposta\",\"lines\":[{\"cost_category_id\":\"$PERS\",\"amount_cents\":2500000},{\"cost_category_id\":\"$CONS\",\"amount_cents\":2400000}]}" >/dev/null
submit $RN
# Portale B2B: proposta 2027 ancora in bozza.
budget $B2B $FY27 "[{\"cost_category_id\":\"$SVIL\",\"amount_cents\":18000000,\"period\":\"2027-03\"},{\"cost_category_id\":\"$CONS\",\"amount_cents\":7000000},{\"cost_category_id\":\"$CLOUD\",\"amount_cents\":3000000}]" >/dev/null

# Trasferimento sotto soglia: dalle consulenze DORA a quelle della firma digitale.
line_of() { call admin GET /api/v1/finance/budgets/$1 | py "next(l['id'] for l in d['lines'] if l['cost_category_id']=='$2')"; }
call admin POST /api/v1/finance/transfers "{\"from_line_id\":\"$(line_of $BD $CONS)\",\"to_line_id\":\"$(line_of $BF $CONS)\",\"amount_cents\":400000,\"reason\":\"Analisi legale sulla firma remota anticipata a dicembre\"}" >/dev/null

# Firma digitale: finestra da giugno, voci, ore e earned value con la storia dei mesi passati.
call admin PUT /api/v1/demand/requests/$FIRMA/schedule '{"start":"2026-06-01","end":"2027-03-31"}' >/dev/null
cost() { call admin POST /api/v1/finance/costs "$1" >/dev/null; }
cost "{\"element\":\"$FIRMA\",\"cost_category_id\":\"$LIC\",\"source\":\"invoice\",\"source_ref\":\"INV-2026-0611\",\"entry_date\":\"2026-06-15\",\"amount_cents\":650000,\"currency\":\"USD\",\"description\":\"Licenze piattaforma di firma, primo anno\",\"supplier\":\"SignCloud Inc.\"}"
cost "{\"element\":\"$FIRMA\",\"cost_category_id\":\"$CONS\",\"source\":\"purchase_order\",\"source_ref\":\"ODA-2026-118\",\"entry_date\":\"2026-07-03\",\"kind\":\"committed\",\"amount_cents\":900000,\"description\":\"Ordine per l'integrazione con il gestionale\",\"supplier\":\"Studio Integra\"}"
cost "{\"element\":\"$FIRMA\",\"cost_category_id\":\"$CONS\",\"source\":\"invoice\",\"source_ref\":\"FT-412/2026\",\"entry_date\":\"2026-09-30\",\"amount_cents\":420000,\"description\":\"Integrazione, primo stato di avanzamento\",\"supplier\":\"Studio Integra\"}"
cost "{\"element\":\"$NIS2\",\"cost_category_id\":\"$CLOUD\",\"source\":\"recurring\",\"entry_date\":\"2026-11-01\",\"amount_cents\":180000,\"description\":\"Ambiente di raccolta degli eventi\"}"
wl() { call admin POST /api/v1/finance/work-logs "{\"element\":\"$1\",\"member_id\":\"$2\",\"work_date\":\"$3\",\"hours\":$4,\"role_key\":\"$5\"}" >/dev/null; }
for m in 06 07 08 09 10 11; do
  wl $FIRMA $LEAD 2026-$m-10 16 backend
  wl $FIRMA $DM 2026-$m-12 8 analyst
  if [ $((10#$m)) -ge 8 ]; then wl $FIRMA $ARCH 2026-$m-14 6 architect; fi
done
wl $NIS2 $LEAD 2026-11-05 12 backend; wl $NIS2 $ARCH 2026-11-06 8 architect; wl $DORA $DM 2026-11-09 10 analyst
call admin PUT /api/v1/demand/requests/$FIRMA/ev '{"ev_enabled":true,"ev_method":"percent_complete","percent_complete":34}' >/dev/null
for s in "2026-06-30 4" "2026-07-31 9" "2026-08-31 15" "2026-09-30 22" "2026-10-31 28"; do
  set -- $s; call admin POST /api/v1/demand/requests/$FIRMA/ev/snapshots "{\"as_of\":\"$1\",\"percent_complete\":$2}" >/dev/null
done
call admin POST /api/v1/demand/requests/$FIRMA/ev/snapshots '{}' >/dev/null

# Import CSV: profilo "Fatture ERP", prova (una riga con errore) e conferma delle righe valide.
call admin POST /api/v1/finance/import-profiles '{"name":"Fatture ERP","mapping":{"columns":{"Data documento":"entry_date","Progetto":"element","Importo":"amount","Valuta":"currency","Conto":"category","Fornitore":"supplier","Numero":"source_ref","Causale":"description"},"delimiter":";","date_format":"GG/MM/AAAA","decimal":","}}' > $T/profile
PROFILE=$(py 'd["id"]' < $T/profile)
cat > $T/fatture.csv <<CSV
Data documento;Progetto;Importo;Valuta;Conto;Fornitore;Numero;Causale
12/10/2026;$NIS2;3.850,00;EUR;CONS;Cyber Advisory srl;FE-2026-0981;Gap analysis NIS2
28/10/2026;$NIS2;1.240,50;EUR;CONS;Cyber Advisory srl;FE-2026-1022;Workshop con i fornitori critici
04/11/2026;$DORA;2.100,00;GBP;CONS;RegTech Ltd;RT-5531;Modello del registro delle informazioni
06/11/2026;$DORA;980,00;EUR;LICENZE;Data Vault spa;DV-77;Licenza repository documentale
CSV
python3 -c "import json,sys; print(json.dumps({'profile_id':sys.argv[1],'file_name':'fatture-ottobre.csv','csv':open(sys.argv[2]).read()}))" "$PROFILE" $T/fatture.csv > $T/import.json
RUN=$(curl -s -b $T/admin -H 'Content-Type: application/json' -d @$T/import.json $B/api/v1/finance/imports | py 'd["id"]')
python3 -c "import json,sys; print(json.dumps({'csv':open(sys.argv[1]).read(),'skip_errors':True}))" $T/fatture.csv > $T/confirm.json
curl -s -b $T/admin -H 'Content-Type: application/json' -d @$T/confirm.json $B/api/v1/finance/imports/$RUN/confirm >/dev/null
rm -rf $T
echo "Budget e costi di esempio creati."
