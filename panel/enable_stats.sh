#!/bin/bash
set -euo pipefail
AX_DIR="${AX_DIR:-/usr/local/etc/xray}"
CFG="$AX_DIR/config.json"
[[ -f "$CFG" ]] || { echo "Нет $CFG"; exit 1; }

cp -f "$CFG" "$CFG.bak"
echo "Бэкап: $CFG.bak"

CFG="$CFG" python3 <<'PY'
import json, os
cfg_path = os.environ["CFG"]
with open(cfg_path) as f:
    cfg = json.load(f)

cfg["stats"] = cfg.get("stats", {})

api = cfg.get("api") or {}
api["tag"] = "api"
svc = set(api.get("services", []))
svc.add("StatsService")
api["services"] = sorted(svc)
cfg["api"] = api

pol = cfg.get("policy") or {}
levels = pol.get("levels") or {}
lvl0 = levels.get("0") or {}
lvl0["statsUserUplink"] = True
lvl0["statsUserDownlink"] = True
levels["0"] = lvl0
pol["levels"] = levels
cfg["policy"] = pol

inbounds = cfg.setdefault("inbounds", [])
if not any(ib.get("tag") == "api" for ib in inbounds):
    inbounds.append({
        "tag": "api", "protocol": "dokodemo-door",
        "listen": "127.0.0.1", "port": 10085,
        "settings": {"address": "127.0.0.1"},
    })

routing = cfg.setdefault("routing", {})
rules = routing.setdefault("rules", [])
if not any(r.get("inboundTag") == ["api"] or "api" in (r.get("inboundTag") or []) for r in rules):
    rules.insert(0, {"type": "field", "inboundTag": ["api"], "outboundTag": "api"})

with open(cfg_path, "w") as f:
    json.dump(cfg, f, indent=2)
print("config.json: stats/api/policy включены")
PY

if command -v xray >/dev/null && ! xray -test -config "$AX_DIR/config.json" >/dev/null 2>&1; then
    echo "ВНИМАНИЕ: xray -test не прошёл после правки"; exit 1
fi
echo "Готово. Перезапустите: systemctl restart xray"
