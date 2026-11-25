#!/usr/bin/env bash
# Dati di esempio del gate di rilascio nel tenant "demo" (piano, passo 21, fase 4; solo sviluppo e prova).
# Configura le regole per ambiente (produzione bloccante, staging in avviso), chi approva gli
# override e la mappatura dei controlli della pipeline sui controlli GRC; poi una pipeline
# immaginaria chiede il gate con un token API (permesso devsecops.gate), come farebbe uno
# step curl prima del deploy: qualche rilascio bloccato, uno superato con un override
# approvato, uno con l'override in attesa, qualche via libera in staging.
# Uso: scripts/dev-demo-gate.sh [url]  — dopo dev-demo-devsecops.sh e dev-demo-grc.sh
# (lo richiama dev-demo-data.sh).
set -euo pipefail
B=${1:-http://127.0.0.1:8080}
PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}
python3 - "$B" "$PW" <<'PY'
import http.cookiejar, json, sys, urllib.error, urllib.parse, urllib.request

base, pw = sys.argv[1], sys.argv[2]


class Session:
    def __init__(self, email):
        self.jar = http.cookiejar.CookieJar()
        self.web = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        me = self.call("POST", "/api/v1/auth/login", {"email": email, "password": pw})
        demo = next(t["tenant_id"] for t in me["tenants"] if t["slug"] == "demo")
        self.call("POST", "/api/v1/me/tenant", {"tenant_id": demo})

    def call(self, method, path, body=None, token=None, ok=(200, 201, 202, 204, 409)):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(base + path, data=data, method=method)
        if body is not None:
            req.add_header("Content-Type", "application/json")
        opener = self.web
        if token:
            req.add_header("Authorization", "Bearer " + token)
            opener = urllib.request.build_opener()
        try:
            with opener.open(req) as r:
                raw = r.read()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as e:
            detail = e.read().decode(errors="replace")
            if e.code in ok:
                return json.loads(detail) if detail else {}
            raise SystemExit(f"{method} {path}: {e.code} {detail}")


admin = Session("admin@demo.test")
dm = Session("dm@demo.test")
sec = Session("sicurezza@demo.test")
members = {m["display_name"]: m["id"] for m in admin.call("GET", "/api/v1/members")["items"]}

def q(text):
    return admin.call("GET", "/api/v1/elements?q=" + urllib.parse.quote(text))["items"]

b2b = q("tipo: business_request portale clienti B2B")
b2b = b2b[0]["key"] if b2b else ""

def rule(kind, level, params, enabled=True, label=""):
    r = {"kind": kind, "enabled": enabled, "level": level, "params": params}
    if label:
        r["label"] = label
    return r

# --- Regole: produzione bloccante, staging in avviso.
prod = [
    rule("requirements", "block", {}),
    rule("vulnerabilities", "block", {"severity": "high", "only_fixable": True, "max": 0, "require_report": True}),
    rule("change_approved", "block", {"phases": ["Pianificazione", "In esecuzione"], "require_change": True}),
    rule("checks", "block", {"names": ["secrets", "test"]}),
    rule("window", "warn", {"days": [1, 2, 3, 4, 5], "from": "07:00", "to": "20:00", "timezone": "Europe/Rome",
                            "blackouts": [{"from": "2026-12-24", "to": "2027-01-06", "label": "Congelamento di fine anno"}]}),
    rule("condition", "warn", {"when": "cve_critiche_produzione: 0", "on": "application"}, label="Nessuna CVE critica in produzione"),
]
staging = [
    rule("vulnerabilities", "block", {"severity": "critical", "only_fixable": True, "max": 0, "require_report": False}),
    rule("checks", "warn", {"names": ["test"]}),
]
admin.call("PUT", "/api/v1/devsecops/gate/policies/prod", {"mode": "block", "rules": prod, "override_enabled": True, "override_hours": 24})
admin.call("PUT", "/api/v1/devsecops/gate/policies/staging", {"mode": "warn", "rules": staging, "override_enabled": True, "override_hours": 72})
admin.call("PUT", "/api/v1/devsecops/gate/approvers", {"approvers": [{"member": members["Giulia Sala"]}]})

# --- Evidenze: i controlli superati in block e le decisioni del gate vanno sul controllo "Gestione delle vulnerabilità".
cfg = admin.call("GET", "/api/v1/devsecops/gate/config")["evidence"]
ctl = next((c["key"] for c in cfg["controls"] if c["title"] == "Gestione delle vulnerabilità"), "")
cic = next((c["key"] for c in cfg["controls"] if c["title"] == "Continuità delle funzioni critiche"), ctl)
if ctl:
    admin.call("PUT", "/api/v1/devsecops/gate/evidence-map", {"items": [
        {"source": "check:secrets", "control": ctl, "valid_days": 90, "production_only": True},
        {"source": "check:cves", "control": ctl, "valid_days": 90, "production_only": True},
        {"source": "gate", "control": cic, "valid_days": 30, "production_only": True},
    ]})

# --- La pipeline chiede il gate con un token che ha solo questo permesso.
tok = admin.call("POST", "/api/v1/me/tokens", {"name": "Pipeline demo (gate)", "expires_in_days": 30, "scopes": ["devsecops.gate"]})["token"]

def gate(app, env, version, service="", request="", change=""):
    body = {"format": "regia.devsecops/v1", "application": app, "environment": env, "version": version}
    for k, v in (("service", service), ("request", request), ("change", change)):
        if v:
            body[k] = v
    return admin.call("POST", "/api/v1/devsecops/gate", body, token=tok)

n = 0
for app, env, ver, svc, req in [
    ("PAY", "prod", "2.5.1", "pagamenti-gateway", ""),
    ("API-CLI", "prod", "5.3.1", "clienti-api", b2b),
    ("WEB-AR", "prod", "3.9.0", "area-clienti-web", b2b),
    ("IAM", "prod", "24.0.5-1", "identity-broker", ""),
    ("APP-MOB", "prod", "0.19.0", "app-mobile-bff", ""),
    ("WEB-AR", "staging", "3.10.0-rc.2", "area-clienti-web", ""),
    ("API-CLI", "staging", "5.4.0-rc.1", "clienti-api", ""),
    ("PAY", "staging", "2.6.0-rc.1", "pagamenti-gateway", ""),
]:
    r = gate(app, env, ver, svc, req)
    n += 1
    print("  gate %-8s %-8s %-12s → %s" % (app, env, ver, r.get("result", "?")))

# --- Override: due richieste di persone diverse dall'approvatore; una approvata (poi il gate passa), una in attesa.
def step_of(s, key):
    el = s.call("GET", "/api/v1/elements/" + key)
    for a in el.get("approvals", []):
        if a["status"] == "pending":
            for st in a["steps"]:
                if st["decision"] == "pending":
                    return st["id"]
    return None

decisions = admin.call("GET", "/api/v1/devsecops/gate/decisions?result=fail&limit=50")["items"]
def decision(code, env="prod"):
    return next((d for d in decisions if d["environment"] == env and (d["application"].get("code") == code) and d["can_override"]), None)

iam = decision("IAM")
if iam:
    dm.call("POST", "/api/v1/devsecops/gate/overrides", {"decision": iam["id"], "reason": "Correzione di sicurezza dell'identity broker attesa nel rilascio successivo: si pubblica la 24.0.5-1 con la mitigazione sul reverse proxy.", "hours": 12})
    sid = step_of(sec, iam["application"]["key"])
    if sid:
        sec.call("POST", "/api/v1/approvals/steps/" + sid + "/decision", {"decision": "approved", "comment": "Mitigazione verificata sul proxy; la correzione entra nel rilascio di giovedì."})
    r = gate("IAM", "prod", "24.0.5-1", "identity-broker")
    print("  gate IAM prod dopo l'override → %s%s" % (r.get("result"), " (override)" if r.get("overridden") else ""))
pay = decision("PAY")
if pay:
    dm.call("POST", "/api/v1/devsecops/gate/overrides", {"decision": pay["id"], "reason": "Hotfix sul calcolo delle rate: la libreria JWT si aggiorna dopo il collaudo di giovedì.", "hours": 24})
print("Gate di rilascio: %d chiamate, regole per prod e staging, approvatori e mappatura delle evidenze." % n)
PY
