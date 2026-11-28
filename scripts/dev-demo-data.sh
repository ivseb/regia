#!/usr/bin/env bash
# Richieste di esempio nel tenant "demo" per provare l'interfaccia (solo sviluppo).
# Uso: scripts/dev-demo-data.sh [url]   — richiede scripts/dev-seed.sh già eseguito.
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
for u in admin richiedente dm sponsor pmo; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
call admin POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null
SPONSOR=$(py 'd["active_tenant"]["member_id"]' < $T/sponsor.me)
unit() { call admin POST /api/v1/org-units "{\"name\":\"$1\"}" | py 'd.get("id","")'; }
U_COMP=$(unit Compliance); U_SIC=$(unit Sicurezza); U_COMM=$(unit Commerciale); U_IT=$(unit "IT Operations"); U_AMM=$(unit Amministrazione)
# titolo | unità | driver | obbligo | scadenza | stima min k€ | max k€ | fase di arrivo
unit_id() { case "$1" in Compliance) echo "$U_COMP";; Sicurezza) echo "$U_SIC";; Commerciale) echo "$U_COMM";; "IT Operations") echo "$U_IT";; *) echo "$U_AMM";; esac; }
while IFS='|' read -r title uname drv mand dl lo hi target; do
  u=$(unit_id "$uname")
  body=$(python3 -c "import json,sys; a=sys.argv; d={'title':a[1],'description':'Richiesta di esempio per lo sviluppo.','sponsor_id':a[2],'requesting_unit_id':a[3],'driver':a[4],'is_mandatory':a[5]=='si','fields':{'urgenza':'Alta' if a[5]=='si' else 'Media'}}; d.update({'regulatory_deadline':a[6]} if a[6] else {}); print(json.dumps(d))" "$title" "$SPONSOR" "$u" "$drv" "$mand" "$dl")
  ID=$(call richiedente POST /api/v1/demand/requests "$body" | py 'd["id"]')
  [ "$target" = "Bozza" ] && continue
  call richiedente POST /api/v1/elements/$ID/transitions '{"to":"Inviata"}' >/dev/null
  [ "$target" = "Inviata" ] && continue
  for s in Triage Valutazione; do call dm POST /api/v1/elements/$ID/transitions "{\"to\":\"$s\"}" >/dev/null; [ "$target" = "$s" ] && break; done
  [ "$target" = "Triage" ] || [ "$target" = "Valutazione" ] && continue
  call dm PATCH /api/v1/demand/requests/$ID "{\"estimate_low_cents\":$((lo*100000)),\"estimate_high_cents\":$((hi*100000))}" >/dev/null
  call dm POST /api/v1/elements/$ID/transitions '{"to":"Prioritizzazione"}' >/dev/null
  [ "$target" = "Prioritizzazione" ] && continue
  call dm POST /api/v1/elements/$ID/transitions '{"to":"Approvazione"}' >/dev/null
done <<'DATA'
Adeguamento DORA: registro delle informazioni sui fornitori ICT|Compliance|regulatory|si|2027-04-30|80|120|Valutazione
Notifica degli incidenti significativi (NIS2)|Sicurezza|regulatory|si|2027-01-17|60|90|Approvazione
Portale clienti B2B, seconda fase|Commerciale|strategic|no||250|320|Prioritizzazione
Migrazione del data warehouse su cloud europeo|IT Operations|efficiency|no||400|550|Triage
Firma digitale dei contratti con i fornitori|Amministrazione|efficiency|no||30|45|Approvazione
Conservazione a norma delle fatture passive|Amministrazione|regulatory|si|2027-06-30|25|35|Inviata
Razionalizzazione delle licenze di produttività|IT Operations|efficiency|no||15|20|Bozza
DATA
bash "$(dirname "$0")/dev-demo-architecture.sh" "$B"
bash "$(dirname "$0")/dev-demo-planning.sh" "$B"
bash "$(dirname "$0")/dev-demo-governance.sh" "$B"
bash "$(dirname "$0")/dev-demo-finance.sh" "$B"
bash "$(dirname "$0")/dev-demo-reviews.sh" "$B"
bash "$(dirname "$0")/dev-demo-fields.sh" "$B"
bash "$(dirname "$0")/dev-demo-work.sh" "$B"
bash "$(dirname "$0")/dev-demo-tempo.sh" "$B"
bash "$(dirname "$0")/dev-demo-report.sh" "$B"
bash "$(dirname "$0")/dev-demo-requirements.sh" "$B"
bash "$(dirname "$0")/dev-demo-devsecops.sh" "$B"
bash "$(dirname "$0")/dev-demo-politica.sh" "$B"
bash "$(dirname "$0")/dev-demo-knowledge.sh" "$B"
bash "$(dirname "$0")/dev-demo-ea.sh" "$B"
bash "$(dirname "$0")/dev-demo-risk.sh" "$B"
bash "$(dirname "$0")/dev-demo-continuita.sh" "$B"
bash "$(dirname "$0")/dev-demo-dora.sh" "$B"
bash "$(dirname "$0")/dev-demo-grc.sh" "$B"
bash "$(dirname "$0")/dev-demo-gate.sh" "$B"
bash "$(dirname "$0")/dev-demo-viste.sh" "$B"
bash "$(dirname "$0")/dev-demo-vulnerabilita.sh" "$B"
rm -rf $T
echo "Richieste di esempio create."
