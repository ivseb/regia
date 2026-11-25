#!/usr/bin/env bash
# Tempo e team di esempio nel tenant "demo" (piano, passo 18; solo sviluppo e prova): persone
# nei team Web e Integrazioni con ruolo, allocazione e ore, qualche assenza, il timesheet delle
# ultime due settimane di Anna e Marco, l'approvazione del responsabile attiva con una
# settimana di Marco in attesa.
# Uso: scripts/dev-demo-tempo.sh [url]  — dopo dev-demo-work.sh.
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; T=$(mktemp -d)
eval "$(grep -m1 '^PW=' "$(dirname "$0")/dev-seed.sh")"   # stessa password di sviluppo del seed
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
for u in admin lead dev dev2; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
for u in admin lead dev dev2; do call $u POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null; done
me() { py 'd["active_tenant"]["member_id"]' < $T/$1.me; }
DEV=$(me dev); DEV2=$(me dev2); LEAD=$(me lead)
day() { python3 -c "import datetime,sys; print((datetime.date.today()+datetime.timedelta(days=int(sys.argv[1]))).isoformat())" "$1"; }
monday() { python3 -c "import datetime,sys; t=datetime.date.today(); m=t-datetime.timedelta(days=t.weekday()); print((m+datetime.timedelta(days=int(sys.argv[1]))).isoformat())" "$1"; }

# Persone nei team: Web con Anna a tempo pieno e Marco al 60%, Integrazioni con Marco al 40%.
call admin GET /api/v1/work/teams | py '"\n".join(t["id"]+"|"+t["name"] for t in d["items"])' > $T/teams
while IFS='|' read -r tid tname; do
  case "$tname" in
    Web)
      call admin PUT /api/v1/work/teams/$tid/members "{\"member_id\":\"$LEAD\",\"lead\":true,\"role\":\"responsabile\",\"allocation_pct\":20}" > /dev/null
      call admin PUT /api/v1/work/teams/$tid/members "{\"member_id\":\"$DEV\",\"role\":\"backend\",\"allocation_pct\":100}" > /dev/null
      call admin PUT /api/v1/work/teams/$tid/members "{\"member_id\":\"$DEV2\",\"role\":\"frontend\",\"allocation_pct\":60}" > /dev/null
      echo "Team Web: persone e capacità";;
    Integrazioni)
      call admin PUT /api/v1/work/teams/$tid/members "{\"member_id\":\"$DEV2\",\"role\":\"integrazioni\",\"allocation_pct\":40}" > /dev/null
      echo "Team Integrazioni: persone e capacità";;
  esac
done < $T/teams

# Assenze: ferie di Anna la prossima settimana (due giorni), un permesso di Marco.
call dev POST /api/v1/work/absences "{\"from\":\"$(monday 8)\",\"to\":\"$(monday 9)\",\"kind\":\"ferie\",\"note\":\"Ponte\"}" > /dev/null
call dev2 POST /api/v1/work/absences "{\"from\":\"$(monday 3)\",\"hours_per_day\":4,\"kind\":\"permesso\",\"note\":\"Visita medica\"}" > /dev/null

# Timesheet: le ore di Anna e Marco sugli elementi che hanno assegnati, due settimane fa e la scorsa.
cells_for() {  # persona, settimana (offset in giorni dal lunedì corrente), durate per i cinque giorni
  local who=$1 off=$2; shift 2
  call $who GET /api/v1/work/me | py '"\n".join(i["key"] for i in d["items"] if i["type"] in ("story","task","bug") and i["category"]!="done")' > $T/$who.keys
  python3 - "$B" "$T/$who.keys" "$(monday $off)" "$@" > $T/$who.cells <<'PY'
import json, sys, datetime
keys = [k for k in open(sys.argv[2]).read().split() if k]
week = datetime.date.fromisoformat(sys.argv[3])
rows = [x.split(",") for x in sys.argv[4:]]  # una riga per elemento: durate per Lun..Ven
cells = []
for r, key in zip(rows, keys):
    for i, d in enumerate(r):
        if d: cells.append({"element": key, "date": (week + datetime.timedelta(days=i)).isoformat(), "duration": d})
print(json.dumps({"week": week.isoformat(), "cells": cells}))
PY
  curl -s -b $T/$who -X PUT -H 'Content-Type: application/json' -d @$T/$who.cells $B/api/v1/work/timesheet/cells > /dev/null
}
cells_for dev -14 "2h,3h,1h30,,4h" "4h,2h,3h,5h," ",2h,2h,3h,3h"
cells_for dev2 -14 "3h,4h,4h,3h,2h" "4h,3h,2h,4h,4h"
cells_for dev -7 "3h,2h30,4h,2h,1h" "3h,3h,2h,4h,3h" "2h,2h,1h,2h,4h"
cells_for dev2 -7 "4h,4h,3h,4h,2h" "3h,3h,4h,3h,4h"
echo "Timesheet: due settimane di ore per Anna e Marco"

# Approvazione facoltativa attiva, a cura del responsabile del team; Marco ha già inviato la settimana scorsa.
call admin PUT /api/v1/work/time/settings '{"require_approval":true,"approver_mode":"team_lead"}' > /dev/null
call dev2 POST /api/v1/work/timesheet/submit "{\"week\":\"$(monday -7)\"}" > /dev/null || true
call dev POST /api/v1/work/timesheet/submit "{\"week\":\"$(monday -14)\"}" > /dev/null || true
call lead POST /api/v1/work/timesheet/decide "{\"member\":\"$DEV\",\"week\":\"$(monday -14)\",\"approve\":true}" > /dev/null || true
echo "Approvazione dei timesheet: responsabile del team (una settimana di Marco in attesa)"
rm -rf $T
