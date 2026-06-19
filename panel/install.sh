#!/bin/bash
# Установщик xray-panel на VPS. Запускать НА СЕРВЕРЕ от root из папки panel/.
#
#   scp -r panel root@SERVER:/root/      # (или git pull на сервере)
#   ssh root@SERVER
#   cd /root/panel && ./install.sh
#
# Идемпотентно: повторный запуск безопасен.
#
# Переменные (можно переопределить):
#   AX_DIR       каталог autoXRAY            (умолч. /usr/local/etc/xray)
#   PANEL_ADDR   адрес прослушки панели      (умолч. 127.0.0.1:8088)
#   ADMIN_PATH   путь в nginx                (умолч. /admin/)
#   PANEL_USER   логин basic auth           (умолч. admin)
#   PANEL_PASS   пароль basic auth          (умолч. сгенерируется и будет выведен)

set -euo pipefail

GRN='\033[1;32m'; RED='\033[1;31m'; YEL='\033[1;33m'; NC='\033[0m'
info(){ echo -e "${YEL}==>${NC} $*"; }
ok(){ echo -e "${GRN}✓${NC} $*"; }
die(){ echo -e "${RED}✗ $*${NC}"; exit 1; }

[[ $EUID -eq 0 ]] || die "Нужны root права"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AX_DIR="${AX_DIR:-/usr/local/etc/xray}"
PANEL_ADDR="${PANEL_ADDR:-127.0.0.1:8088}"
XRAY_API="${XRAY_API:-127.0.0.1:10085}"
ADMIN_PATH="${ADMIN_PATH:-/admin/}"
PANEL_USER="${PANEL_USER:-admin}"
PANEL_PASS="${PANEL_PASS:-}"

MULTI_DIR="$AX_DIR/autoXRAY-multi"
SERVER_ENV="$AX_DIR/server.env"
ENV_FILE="$AX_DIR/panel.env"
BIN_SRC="$SCRIPT_DIR/xray-panel"
UPDATE_SCRIPT="$MULTI_DIR/update_clients.sh"

# --- 0. Проверки окружения ---
[[ -f "$AX_DIR/config.json" ]] || die "Нет $AX_DIR/config.json — сначала поставьте базовый autoXRAY"
[[ -f "$SERVER_ENV" ]]         || die "Нет $SERVER_ENV — запустите autoXRAY-multi/init_server_env.sh"
[[ -f "$UPDATE_SCRIPT" ]]      || die "Нет $UPDATE_SCRIPT — скопируйте папку autoXRAY-multi в $AX_DIR"
command -v xray  >/dev/null || die "xray не найден в PATH"
command -v nginx >/dev/null || die "nginx не найден"

if [[ ! -f "$BIN_SRC" ]]; then
    if command -v go >/dev/null; then
        info "Бинарь не найден — собираю из исходников ($SCRIPT_DIR)"
        ( cd "$SCRIPT_DIR" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o xray-panel . )
    else
        die "Нет $BIN_SRC и нет go для сборки. Соберите 'make build' на машине разработки и положите бинарь рядом."
    fi
fi

# shellcheck source=/dev/null
source "$SERVER_ENV"
NGINX_CONFIG="${NGINX_CONFIG:-}"
DOMAIN="${DOMAIN:-}"
[[ -n "$NGINX_CONFIG" && -f "$NGINX_CONFIG" ]] || die "NGINX_CONFIG из server.env не найден: '$NGINX_CONFIG'"

# --- 1. Бинарь ---
info "Ставлю бинарь -> /usr/local/bin/xray-panel"
install -m 0755 "$BIN_SRC" /usr/local/bin/xray-panel
ok "бинарь установлен"

# --- 2. panel.env ---
if [[ ! -f "$ENV_FILE" ]]; then
    info "Создаю $ENV_FILE"
    cat > "$ENV_FILE" <<EOF
AX_DIR=$AX_DIR
PANEL_ADDR=$PANEL_ADDR
XRAY_API=$XRAY_API
UPDATE_SCRIPT=$UPDATE_SCRIPT
EOF
    ok "panel.env создан"
else
    ok "panel.env уже есть — не трогаю"
fi

# --- 3. enable_stats.sh: включить xray Stats API (идемпотентно) ---
info "Копирую и запускаю enable_stats.sh"
install -m 0755 "$SCRIPT_DIR/enable_stats.sh" "$MULTI_DIR/enable_stats.sh"
AX_DIR="$AX_DIR" bash "$MULTI_DIR/enable_stats.sh"

# --- 4. Применить клиентов (проставит email для статистики) ---
info "Прогоняю update_clients.sh (email-тег + рестарт xray)"
AX_DIR="$AX_DIR" bash "$UPDATE_SCRIPT"
ok "клиенты применены"

# --- 5. htpasswd ---
if [[ ! -f /etc/nginx/.htpasswd ]] || ! grep -q "^${PANEL_USER}:" /etc/nginx/.htpasswd 2>/dev/null; then
    if ! command -v htpasswd >/dev/null; then
        info "Ставлю htpasswd"
        if command -v apt-get >/dev/null; then
            apt-get update -qq && apt-get install -y apache2-utils >/dev/null
        elif command -v dnf >/dev/null; then
            dnf install -y httpd-tools >/dev/null
        elif command -v yum >/dev/null; then
            yum install -y httpd-tools >/dev/null
        else
            die "Не нашёл apt/dnf/yum — поставьте htpasswd вручную (apache2-utils / httpd-tools)"
        fi
        command -v htpasswd >/dev/null || die "htpasswd не установился"
    fi
    if [[ -z "$PANEL_PASS" ]]; then
        PANEL_PASS="$(openssl rand -base64 12)"
        GEN_PASS=1
    fi
    info "Создаю basic-auth пользователя '$PANEL_USER'"
    htpasswd -bc /etc/nginx/.htpasswd "$PANEL_USER" "$PANEL_PASS" >/dev/null
    ok "htpasswd готов"
else
    ok "basic-auth пользователь '$PANEL_USER' уже есть — не меняю пароль"
fi

# --- 6. nginx location (идемпотентно, через маркеры) ---
info "Добавляю location $ADMIN_PATH в $NGINX_CONFIG"
ADMIN_PATH="$ADMIN_PATH" PANEL_ADDR="$PANEL_ADDR" NGINX_CONFIG="$NGINX_CONFIG" python3 <<'PY'
import os, re
cfg = os.environ["NGINX_CONFIG"]
admin = os.environ["ADMIN_PATH"]
addr = os.environ["PANEL_ADDR"]
START = "# AUTOXRAY_PANEL_START"
END = "# AUTOXRAY_PANEL_END"

# Redirect the slash-less form (/admin -> /admin/) so the frontend, which
# resolves API calls relative to its directory, always has a trailing slash.
redirect = ""
if admin.endswith("/") and admin != "/":
    redirect = f"\n    location = {admin.rstrip('/')} {{ return 301 {admin}; }}"

block = f"""{START}{redirect}
    location {admin} {{
        auth_basic "panel";
        auth_basic_user_file /etc/nginx/.htpasswd;
        proxy_pass http://{addr}/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }}
    {END}"""

with open(cfg) as f:
    text = f.read()

if START in text:
    text = re.sub(re.escape(START) + r".*?" + re.escape(END), block, text, count=1, flags=re.DOTALL)
elif "# AUTOXRAY_SUB_LOCATIONS_START" in text:
    # вставить перед блоком подписок
    text = text.replace("# AUTOXRAY_SUB_LOCATIONS_START", block + "\n\n    # AUTOXRAY_SUB_LOCATIONS_START", 1)
else:
    # фолбэк: перед последней закрывающей скобкой server-блока с listen 443
    m = list(re.finditer(r"\n}\s*$", text))
    if not m:
        raise SystemExit("Не нашёл куда вставить location — добавьте вручную")
    pos = m[-1].start()
    text = text[:pos] + "\n    " + block + "\n" + text[pos:]

with open(cfg, "w") as f:
    f.write(text)
print("nginx: location панели добавлен")
PY

nginx -t || die "nginx -t не прошёл — проверьте $NGINX_CONFIG"
systemctl reload nginx
ok "nginx обновлён"

# --- 7. systemd ---
info "Ставлю systemd-юнит"
install -m 0644 "$SCRIPT_DIR/xray-panel.service" /etc/systemd/system/xray-panel.service
systemctl daemon-reload
systemctl enable --now xray-panel
sleep 1
systemctl is-active --quiet xray-panel && ok "сервис xray-panel запущен" || die "сервис не запустился: journalctl -u xray-panel"

# --- Итог ---
echo
echo -e "${GRN}=== Готово ===${NC}"
URL_HOST="${DOMAIN:-ВАШ_ДОМЕН}"
echo "Панель:  https://${URL_HOST}${ADMIN_PATH}"
echo "Логин:   $PANEL_USER"
if [[ "${GEN_PASS:-0}" == "1" ]]; then
    echo -e "Пароль:  ${YEL}${PANEL_PASS}${NC}   (сохраните — показан один раз)"
else
    echo "Пароль:  (заданный ранее)"
fi
echo
echo "Проверка локально:"
echo "  curl -s ${PANEL_ADDR}/api/users | jq"
echo "  systemctl status xray-panel"
