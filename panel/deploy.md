# Деплой xray-panel

## Быстрый старт — одним скриптом (рекомендуется)

Всё делает `install.sh` сам: бинарь, env, Stats API, nginx-локейшн, basic-auth,
systemd. Идемпотентно. Требует: уже установленный базовый autoXRAY и папку
`autoXRAY-multi/` в `/usr/local/etc/xray/`.

```bash
# 1. На машине разработки — собрать и закинуть всю папку panel/
cd panel && make build
scp -r . root@SERVER:/root/panel/

# 2. На сервере — запустить установщик от root
ssh root@SERVER
cd /root/panel && ./install.sh
```

Скрипт выведет URL панели, логин и (если пароль не задан) сгенерированный пароль.
Переопределить можно через переменные: `PANEL_USER`, `PANEL_PASS`, `ADMIN_PATH`,
`PANEL_ADDR`. Пример: `PANEL_USER=me PANEL_PASS=secret ./install.sh`.

Если `go` есть на сервере, бинарь соберётся прямо там (можно не делать `make build`).

---

## Ручная установка (по шагам)

## Сборка (на машине разработки)
```bash
cd panel && make build      # -> ./xray-panel (linux/amd64)
```

## Установка на VPS (root)
```bash
scp xray-panel root@SERVER:/usr/local/bin/xray-panel
scp panel.env.example root@SERVER:/usr/local/etc/xray/panel.env   # отредактируйте
scp enable_stats.sh root@SERVER:/usr/local/etc/xray/autoXRAY-multi/
scp xray-panel.service root@SERVER:/etc/systemd/system/
```

## Один раз: включить статистику и применить email клиентам
```bash
AX_DIR=/usr/local/etc/xray bash /usr/local/etc/xray/autoXRAY-multi/enable_stats.sh
systemctl restart xray
/usr/local/etc/xray/autoXRAY-multi/update_clients.sh   # перезапишет clients с email
```

## nginx: location за basic auth
```nginx
location /admin/ {
    auth_basic "panel";
    auth_basic_user_file /etc/nginx/.htpasswd;
    proxy_pass http://127.0.0.1:8088/;
    proxy_set_header Host $host;
}
```
```bash
apt-get install -y apache2-utils
htpasswd -c /etc/nginx/.htpasswd admin
nginx -t && systemctl reload nginx
```

## Запуск панели
```bash
systemctl daemon-reload
systemctl enable --now xray-panel
systemctl status xray-panel
```

## Ручной чеклист проверки (на сервере)
1. `curl -s 127.0.0.1:8088/api/users | jq` — список клиентов с трафиком.
2. Открыть `https://ДОМЕН/admin/` (basic auth) — таблица рендерится.
3. Добавить юзера через "+ Добавить" → появился в `clients.txt`, создан `clients/<имя>.env`, страница `https://ДОМЕН/<имя>.html` доступна.
4. Прогнать трафик через нового юзера → через ~30с в панели растут up/down.
5. Удалить юзера → исчез из таблицы и `clients.txt`, `xray -test` прошёл, xray перезапущен.
6. `xray api statsquery --server=127.0.0.1:10085 -pattern "user>>>"` — счётчики по email.
