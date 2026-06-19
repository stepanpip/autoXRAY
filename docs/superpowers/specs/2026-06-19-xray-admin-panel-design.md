# Дизайн: минималистичная админ-панель autoXRAY

Дата: 2026-06-19

## Цель

Веб-панель для управления клиентами autoXRAY-multi: добавлять/удалять
пользователей и видеть их реальный расход трафика. Дизайн — пиксель-в-пиксель
из handoff-мокапа Claude Design (тёмная тема). Минимальный объём: экраны
**Пользователи** и **Детали пользователя** (без дашборда с общими графиками).

## Решения (зафиксированы с пользователем)

- **Стек:** Go, один статический бинарь. Фронт вшит через `embed.FS`. Без
  build-шага и без node/python-рантайма на сервере.
- **Трафик:** реальный, через xray Stats API (`StatsService`).
- **Объём UI:** минимум — список пользователей + детали. Дашборд/настройки —
  каркас на будущее, задизейблены.
- **Доступ:** панель слушает `127.0.0.1`, проксируется существующим nginx на
  скрытом пути с HTTP basic auth. TLS уже настроен autoXRAY.

## Архитектура

```
Браузер ──TLS──> nginx (/admin/, basic auth) ──proxy──> 127.0.0.1:8088 (xray-panel, root)
                                                              │
                          ┌───────────────────────────────────┼─────────────────────────┐
                          ▼                                     ▼                         ▼
                 clients.txt + clients/*.env          xray api statsquery        update_clients.sh
                 (модель юзеров)                       (трафик up/down)           (применить add/delete)
```

Панель переиспользует существующую модель autoXRAY-multi. Источник правды по
юзерам:
- `clients.txt` — редактируемый список имён (вход для add/delete).
- `clients/<имя>.env` — `CLIENT_NAME`, `xray_uuid_vrv`, `path_subpage`.
- `server.env` — `DOMAIN`, `WEB_PATH`, ключи, `NGINX_CONFIG`.

Менять юзеров = править `clients.txt` и звать `update_clients.sh` — тот же путь,
которым админ делает это руками сейчас. Панель НЕ дублирует логику генерации
xray-конфига/подписок/HTML.

**Привилегии:** `update_clients.sh` рестартит xray и reload nginx → нужен root.
Сервис работает от root на личном VPS. Security-trade-off принят осознанно
(панель за basic auth на localhost, личный сервер).

### Каталоги

```
panel/
  main.go
  internal/
    clients/      парс/правка clients.txt + чтение clients/*.env (порт модели из autoxray_lib.sh)
    stats/        опрос xray Stats API + persisted-аккумулятор
    runner/       запуск update_clients.sh, захват вывода/ошибок
    api/          HTTP-хендлеры
  web/
    index.html    вшит через embed.FS
  enable_stats.sh   one-time идемпотентный патч config.json
  xray-panel.service
  deploy.md
  Makefile
```

Плюс правка `autoXRAY-multi/autoxray_lib.sh` (добавить `email` клиентам).

### Конфигурация (env)

- `AX_DIR` — каталог autoXRAY (умолч. `/usr/local/etc/xray`).
- `PANEL_ADDR` — адрес прослушки (умолч. `127.0.0.1:8088`).
- `XRAY_API` — адрес Stats API (умолч. `127.0.0.1:10085`).

## REST API

| Метод | Путь | Действие |
|-------|------|----------|
| `GET` | `/api/users` | Список: имя, tag, uuid (маск.), subpath, статус, up/down/total, ссылки sub/html, дней до истечения |
| `GET` | `/api/users/{name}` | Детали: + proto, ip, port, конфиг-ссылка, ряд за 7 дней |
| `POST` | `/api/users` `{name}` | Валидация, добавить в clients.txt, запустить update_clients.sh, вернуть юзера |
| `DELETE` | `/api/users/{name}` | Убрать из clients.txt, запустить update_clients.sh |
| `GET` | `/api/node` | Нагрузка ноды (`/proc/loadavg`) для карточки sidebar |

**Сериализация:** add/delete защищены мьютексом — параллельные запуски
`update_clients.sh` (рестарт xray) запрещены. Один в момент времени.

**Валидация имени:** `^[a-zA-Z0-9][a-zA-Z0-9_-]*$` (как `ax_validate_client_name`).
Отказ при дубликате.

**Ошибки:** если `update_clients.sh` упал (в т.ч. `xray -test` не прошёл —
скрипт сам это проверяет на строках 380-384), вернуть 500 с stderr; clients.txt
откатить к состоянию до правки.

## Трафик — xray Stats API

### A. Включение статистики

`config.json` сейчас НЕ ведёт учёт. `enable_stats.sh` идемпотентно добавляет:
```jsonc
"stats": {},
"api": { "tag": "api", "services": ["StatsService"] },
"policy": { "levels": { "0": { "statsUserUplink": true, "statsUserDownlink": true } } }
```
+ api-inbound (dokodemo-door на `127.0.0.1:10085`)
+ routing-правило `{ "inboundTag": ["api"], "outboundTag": "api" }`.

Базовый `autoXRAY1.sh` НЕ трогаем (чтобы не ломать UPD5). `enable_stats.sh` —
отдельный one-time скрипт, правит уже сгенерённый config.json. Идемпотентен:
повторный запуск ничего не дублирует.

### B. Ключ статистики = email

Трафик в xray keyed по `email`, а клиенты сейчас только UUID. Правка
`ax_patch_xray_clients` в `autoxray_lib.sh`: добавить `"email": "<имя_клиента>"`
в каждую запись `clients`. Тогда `xray api statsquery` отдаёт
`user>>>имя>>>traffic>>>uplink` и `>>>downlink`.

### C. Обнуление при рестарте

`update_clients.sh` рестартит xray на каждом add/delete → сырые счётчики
недостоверны. Решение: панель раз в ~30с опрашивает stats с `reset:true` и
**аккумулирует** в persisted-файл `AX_DIR/panel_traffic.json`:
```jsonc
{ "<имя>": { "up": 0, "down": 0, "lastSeen": "<ts>", "lastDelta": 0 } }
```
Накопление переживает рестарты; reset-on-read закрывает окно гонки.

**Статус:** `online` если в последнем опросе delta>0, иначе `offline`. `idle` из
мокапа схлопнут в offline (нет надёжного источника «ожидания»). Пульс-точка в UI
для online.

### Вне объёма (YAGNI)

- Реальные исторические графики за 30д: ряд за 7 дней строится из дневных
  снапшотов аккумулятора, накапливается со временем; до накопления — плоско.
- Лимит трафика и срок подписки: полей `days`/`limit` в текущей `.env`-модели
  нет. UI показывает «∞» / прочерк, пока поля не добавят.
- Локация юзера: нет в модели. Показываем proto или прочерк.

## Фронт

Один `web/index.html`, вшит в бинарь. Ванильный JS (`fetch`), без сборки.
Пиксель-в-пиксель из мокапа:
- фон `#15171c`, карточки `#1b1e24`, бордеры `rgba(255,255,255,.07)`;
- акценты: лаванда `#b4bee0`, шалфей `#a3c7b5`, глина `#d6b9a1`;
- шрифты Hanken Grotesk (текст) + JetBrains Mono (цифры/моно).

### Экраны

- **Sidebar:** лого «Xray Panel», пункт «Пользователи» (активен), «Дашборд» и
  «Настройки» задизейблены (каркас). Карточка «Нагрузка ноды» — реальная из
  `GET /api/node`.
- **Пользователи:** таблица из мокапа (аватар-инициалы, имя, tag, статус-точка,
  proto/локация, входящий, исходящий, всего, истекает). Поиск в хедере
  (клиентская фильтрация). Кнопка «+ Добавить» → модалка (имя + валидация) →
  `POST`. Клик по строке → детали.
- **Детали:** профиль (инициалы, имя, бейджи статус/proto), 3 KPI
  (вход/исход/всего), SVG-график за 7 дней (smooth-path как в мокапе), блок
  «Подключение» (UUID маск., proto, inbound, IP, last seen), конфиг-ссылка +
  «Копировать» + «QR» (qrcodejs, как в текущих HTML-страницах), кнопка
  «Удалить» → подтверждение → `DELETE`.

Модалка add и кнопки add/delete отрисованы в стиле мокапа (мокап сам read-only,
этих элементов в нём нет).

## Тестирование

Go-тесты без живого xray:
- `clients` — add/delete/parse на временном `AX_DIR` с фикстурами; валидация
  имён, дубликаты, идемпотентность.
- `stats` — юнит логики аккумулятора: серия снапшотов с обнулением → корректный
  нарастающий total.
- `runner` — мок `update_clients.sh` (stub в tmp), захват ошибки.
- `api` — `httptest`, JSON-контракты хендлеров.

Живой путь (реальный xray api, update_clients.sh, nginx) — ручная проверка на
VPS по чеклисту в `panel/deploy.md`. На Windows-машине разработки не тестируется
(нет xray и серверного bash-окружения).

## Деплой

- `GOOS=linux GOARCH=amd64 go build` на машине разработки (Makefile).
- Бинарь → `/usr/local/bin/xray-panel`; env → `/usr/local/etc/xray/panel.env`.
- systemd-юнит `xray-panel.service` (root, `Restart=always`).
- nginx: `location /admin/ { auth_basic; auth_basic_user_file /etc/nginx/.htpasswd;
  proxy_pass http://127.0.0.1:8088/; }`.
- Один раз: `enable_stats.sh`, затем `update_clients.sh` (применит email
  клиентам), затем старт сервиса.
- Полный чеклист — в `panel/deploy.md`.
