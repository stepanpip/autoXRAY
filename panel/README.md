# xray-panel

Минималистичная админ-панель для autoXRAY-multi: добавление/удаление клиентов
и просмотр реального расхода трафика (xray Stats API).

- Стек: Go (один бинарь, фронт вшит), nginx basic auth, systemd.
- Модель: переиспользует `clients.txt` + `update_clients.sh` из autoXRAY-multi.
- Деплой и проверка: см. [deploy.md](deploy.md).
- Дизайн/архитектура: см. `docs/superpowers/specs/2026-06-19-xray-admin-panel-design.md`.

## Сборка
```bash
cd panel && make build      # -> ./xray-panel (linux/amd64)
make test                   # go test ./...
```

## Конфигурация (env)
| Переменная | Умолчание | Назначение |
|------------|-----------|------------|
| `AX_DIR` | `/usr/local/etc/xray` | каталог autoXRAY (clients.txt, clients/, config.json) |
| `PANEL_ADDR` | `127.0.0.1:8088` | адрес прослушки |
| `XRAY_API` | `127.0.0.1:10085` | адрес xray Stats API |
| `UPDATE_SCRIPT` | `$AX_DIR/autoXRAY-multi/update_clients.sh` | скрипт синхронизации клиентов |
