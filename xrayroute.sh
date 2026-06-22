#!/usr/bin/env bash
#
# xrayroute — управление маршрутизацией сайтов в Xray без ручной правки config.json.
#
# Редактирует секцию routing.rules в /usr/local/etc/xray/config.json: позволяет
# пускать конкретные сайты direct (через IP сервера, без прокси/WARP), через warp,
# или блокировать. Каждая запись валидируется, бэкапится и (по умолчанию) ядро
# перезапускается.
#
# Установка:
#   curl -L -o /usr/local/bin/xrayroute https://.../xrayroute.sh
#   chmod +x /usr/local/bin/xrayroute
#
# Примеры:
#   xrayroute add direct reddit.com www.reddit.com   # reddit напрямую с IP сервера
#   xrayroute add warp   geosite:openai              # openai через WARP
#   xrayroute rm   reddit.com                         # убрать домен отовсюду
#   xrayroute list                                    # показать раскладку по outbound
#   xrayroute add direct reddit.com --no-restart      # только правка, рестарт потом

set -euo pipefail

CONFIG="/usr/local/etc/xray/config.json"
RESTART=1
VALID_OUTBOUNDS="direct warp block"

# ---- парсинг общих флагов (--no-restart / --config=) -------------------------
ARGS=()
for a in "$@"; do
  case "$a" in
    --no-restart)   RESTART=0 ;;
    --config=*)     CONFIG="${a#--config=}" ;;
    *)              ARGS+=("$a") ;;
  esac
done
set -- "${ARGS[@]:-}"

die()  { echo "error: $*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

ensure_jq() {
  have jq && return 0
  echo "jq не найден, ставлю..." >&2
  if have apt-get; then apt-get update -qq && apt-get install -y -qq jq
  elif have dnf;   then dnf install -y -q jq
  elif have yum;   then yum install -y -q jq
  elif have apk;   then apk add --no-cache jq
  else die "не смог поставить jq автоматически — установите вручную"; fi
}

valid_outbound() {
  for o in $VALID_OUTBOUNDS; do [ "$1" = "$o" ] && return 0; done
  return 1
}

usage() {
cat <<'EOF'
xrayroute — управление маршрутизацией сайтов для autoXRAY

ИСПОЛЬЗОВАНИЕ:
  xrayroute add <outbound> <домен>...   пустить домены через outbound
  xrayroute rm  <домен>...              убрать домены из всех правил
  xrayroute list                        показать текущую маршрутизацию
  xrayroute help

OUTBOUND:
  direct   напрямую через IP сервера (без прокси и WARP)
  warp     через Cloudflare WARP
  block    блокировка / blackhole

ФЛАГИ:
  --no-restart    только правка конфига, без рестарта xray
  --config=PATH   нестандартный путь (по умолчанию /usr/local/etc/xray/config.json)

ПРИМЕРЫ:
  xrayroute add direct reddit.com www.reddit.com
  xrayroute add warp geosite:openai geosite:google-gemini
  xrayroute rm reddit.com
  xrayroute list
EOF
}

# ---- безопасная запись: валидация + бэкап + атомарная замена ------------------
# stdin: новый JSON конфига
save_config() {
  local new tmp backup
  new="$(cat)"
  echo "$new" | jq empty 2>/dev/null || die "получился невалидный JSON, отмена записи"

  if [ -f "$CONFIG" ]; then
    backup="${CONFIG}.bak-$(date +%Y%m%d-%H%M%S)"
    cp "$CONFIG" "$backup"
    echo "backup: $backup"
  fi

  tmp="$(dirname "$CONFIG")/.xrayroute.tmp"
  printf '%s\n' "$new" > "$tmp"
  mv "$tmp" "$CONFIG"
}

maybe_restart() {
  if [ "$RESTART" -eq 0 ]; then
    echo "(--no-restart) выполните вручную: systemctl restart xray"
    return 0
  fi
  echo "перезапускаю xray..."
  if systemctl restart xray; then
    echo "xray перезапущен"
  else
    die "рестарт не удался (конфиг сохранён; смотрите journalctl -u xray)"
  fi
}

# ---- команды ----------------------------------------------------------------

cmd_add() {
  [ "$#" -ge 2 ] || die "usage: xrayroute add <outbound> <домен>..."
  local outbound="$1"; shift
  valid_outbound "$outbound" || die "неизвестный outbound '$outbound' (ожидается: direct, warp, block)"

  [ -f "$CONFIG" ] || die "конфиг не найден: $CONFIG"
  jq empty "$CONFIG" 2>/dev/null || die "$CONFIG не является валидным JSON — исправьте перед запуском"

  # Передаём домены в jq как JSON-массив через аргумент.
  local domains_json
  domains_json="$(printf '%s\n' "$@" | jq -R . | jq -s .)"

  # Логика (целиком в jq, чтобы не парсить JSON руками в bash):
  #  1. Убираем эти домены из всех domain-правил (домен живёт ровно в одном месте).
  #  2. Прунем правила, у которых domain опустел.
  #  3. Находим domain-правило с нужным outboundTag:
  #     - есть  -> доливаем недостающие домены (без дублей);
  #     - нет   -> создаём новое правило в начале списка (первое совпадение выигрывает).
  jq \
    --arg ob "$outbound" \
    --argjson add "$domains_json" '
    ($add | map(ascii_downcase)) as $addlc
    | .routing.rules //= []
    | # 1. убрать добавляемые домены из всех правил (регистронезависимо)
      .routing.rules |= map(
        if (.domain // null) != null
        then .domain |= map(select((ascii_downcase) as $d | ($addlc | index($d)) | not))
        else . end)
    | # 2. выкинуть правила с опустевшим domain
      .routing.rules |= map(select((.domain // null) == null or (.domain | length) > 0))
    | # 3. индекс существующего domain-правила с нужным тегом
      ( [ .routing.rules | to_entries[]
          | select(.value.outboundTag == $ob and (.value.domain // null) != null)
          | .key ] | first ) as $idx
    | if $idx == null
      then # создать правило в начале (первое совпадение выигрывает)
        .routing.rules = ([{type:"field", outboundTag:$ob, domain:$add}] + .routing.rules)
      else # долить недостающие домены без дублей
        ( .routing.rules[$idx].domain | map(ascii_downcase) ) as $cur
        | .routing.rules[$idx].domain += ($add | map(select((ascii_downcase) as $d | ($cur | index($d)) | not)))
      end
  ' "$CONFIG" | save_config

  echo "пущено через $outbound: $*"
  maybe_restart
}

cmd_rm() {
  [ "$#" -ge 1 ] || die "usage: xrayroute rm <домен>..."
  [ -f "$CONFIG" ] || die "конфиг не найден: $CONFIG"
  jq empty "$CONFIG" 2>/dev/null || die "$CONFIG не является валидным JSON"

  local domains_json
  domains_json="$(printf '%s\n' "$@" | jq -R . | jq -s .)"

  jq --argjson del "$domains_json" '
    ($del | map(ascii_downcase)) as $dellc
    | .routing.rules //= []
    | .routing.rules |= map(
        if (.domain // null) != null
        then .domain |= map(select((ascii_downcase) as $d | ($dellc | index($d)) | not))
        else . end)
    | .routing.rules |= map(select((.domain // null) == null or (.domain | length) > 0))
  ' "$CONFIG" | save_config

  echo "убрано: $*"
  maybe_restart
}

cmd_list() {
  [ -f "$CONFIG" ] || die "конфиг не найден: $CONFIG"
  jq empty "$CONFIG" 2>/dev/null || die "$CONFIG не является валидным JSON"

  local out
  out="$(jq -r '
    (.routing.rules // [])
    | map(select((.domain // null) != null))
    | if length == 0 then "no-rules"
      else
        group_by(.outboundTag)
        | map("\n[" + ((.[0].outboundTag) // "(no tag)") + "]\n"
              + ( [.[].domain[]] | sort | map("   " + .) | join("\n")))
        | join("\n")
      end
  ' "$CONFIG")"

  if [ "$out" = "no-rules" ]; then
    echo "domain-правил маршрутизации не найдено"
  else
    echo "$out"
    echo
  fi
}

# ---- роутинг команд ---------------------------------------------------------
ensure_jq

cmd="${1:-}"; shift || true
case "$cmd" in
  add)            cmd_add "$@" ;;
  rm|remove)      cmd_rm "$@" ;;
  list|ls)        cmd_list ;;
  help|-h|--help|"") usage ;;
  *)              echo "неизвестная команда: $cmd" >&2; echo; usage; exit 1 ;;
esac
