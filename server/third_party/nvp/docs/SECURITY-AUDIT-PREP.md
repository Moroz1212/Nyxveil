# Подготовка и регламент аудита безопасности NVP/1

Внешние независимые лаборатории **не заказываются**. Аудиты выполняет тот же контур разработки, на русском языке, после существенных правок протокола, крипты или Control Plane.

Отчёт текущего цикла: [SECURITY-AUDIT-REPORT.md](SECURITY-AUDIT-REPORT.md).

Это **не** стороннее заключение и **не** юридическая сертификация. Конфликт интересов (автор кода = аудитор) признаётся и не маскируется.

## Область разбора

### Внутри
- Сессионный слой NVP/1 (X25519, HKDF-SHA256, ChaCha20-Poly1305)
- JWT билеты (Ed25519, строгий alg)
- Replay (epoch + скользящее окно)
- Конечный автомат сессии
- TLS 1.3 / QUIC
- Выдача билетов и подпись каталога Control Plane
- Свойства отпечатка до AUTH

### Вне v1
- Платежи
- HSM / WAF
- Полноценные GUI Windows/Android
- MASQUE (заглушка)
- DPI на целевых сетях

## Криптоинвентарь

| Компонент | Алгоритм | Библиотека |
|-----------|----------|------------|
| Согласование | X25519 | golang.org/x/crypto, crypto/ecdh |
| KDF | HKDF-SHA256 | golang.org/x/crypto |
| AEAD | ChaCha20-Poly1305 | golang.org/x/crypto |
| Билеты | Ed25519 JWT | crypto/ed25519, golang-jwt |
| Транспорт | TLS 1.3 | crypto/tls |
| Случайность | CSPRNG | crypto/rand |

Своих шифров, MAC, кривых и KDF нет.

## Чеклист каждого цикла

1. Nonce: epoch + seq, раздельные направления
2. Rekey: ECDH, ACK под старой эпохой, сброс счётчиков, окно overlap
3. Билеты: alg, aud/iss/exp/nbf, device_pub, подпись транскрипта
4. Replay vs reorder
5. DATA до AUTH
6. Парсер: лимиты, без panic
7. ECH fail-closed
8. Компрометация ноды: blast radius

## Доказательства

```bash
go test ./...
go test -race ./core/integration/... ./core/replay/... ./core/auth/ticket/...
go test -fuzz=FuzzDecodeWireRecord -fuzztime=30s ./core/packet/
go test -fuzz=FuzzTicketVerify -fuzztime=30s ./core/auth/ticket/
```

Ограничения: [SECURITY-LIMITATIONS.md](SECURITY-LIMITATIONS.md).
