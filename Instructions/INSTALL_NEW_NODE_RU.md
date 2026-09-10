# Установка новой Nyxveil-ноды

Требования:

- Ubuntu 24.04
- публичный IPv4
- FQDN с A-записью на IP сервера
- созданная Location в Control Plane
- новый одноразовый Bootstrap Token

1. Перейти в root:

```bash
sudo -i
```

2. Указать параметры:

```bash
VERSION="1.1.12"
CP="https://cp.nyxveil.ru:18443"
LOCATION="fi-helsinki"
NODE_NAME="fi-hel-02"
FQDN="fi-hel-02.nyxveil.ru"
```

Замените `LOCATION`, `NODE_NAME` и `FQDN` на значения новой ноды.

3. Скачать production installer из GitHub Release:

```bash
curl -fL "https://github.com/Moroz1212/Nyxveil/releases/download/server-v${VERSION}/install.sh" -o /root/install.sh
chmod 755 /root/install.sh
```

4. Скрыто ввести Bootstrap Token:

```bash
read -rsp "Bootstrap token: " BT; echo
```

5. Установить ноду:

```bash
printf '%s\n' "$BT" | NYXVEIL_VERSION="$VERSION" bash /root/install.sh \
  --control-plane "$CP" \
  --location "$LOCATION" \
  --name "$NODE_NAME" \
  --public-host "$FQDN" \
  --dns-servers 1.1.1.1,1.0.0.1 \
  --tls-domain "$FQDN" \
  --bootstrap-stdin \
  --non-interactive
unset BT
```

6. Проверка после установки:

```bash
serv_status
```

При успехе установщик выводит `Nyxveil node installed successfully`, а нода появляется в Control Plane.

**Внимание:** если установка завершилась ошибкой после регистрации в Control Plane, не используйте тот же Bootstrap Token повторно.
