# DEPRECATED — Historical internal security audit (cycle 3)

> **DEPRECATED.** Kept for history only.  
> **Current source of truth:** [`docs/CORE-READINESS.md`](../CORE-READINESS.md)  
> Independent external security/cryptographic audit: **NOT PERFORMED**.

---

# Аудит безопасности NVP/1 (внутренний, циклический)

**Дата:** 2026-09-03 (цикл 3)  
**Версия кода:** текущее дерево Nyxveil Protocol Core  
**Аудитор:** реализация и разбор в одном контуре разработки (конфликт интересов признаётся)  
**Политика проекта:** внешние независимые лаборатории **не привлекаются**. Аудиты выполняются здесь же, на русском языке, повторно после существенных правок.

Это **не** сертификат ISO/SOC2 и **не** юридически независимая экспертиза.

---

## Вердикт

| Вопрос | Ответ |
|--------|--------|
| P0/P1 предыдущих циклов | Закрыты |
| P0 этого цикла | Сломанный replay-window (сдвиг битмапа) — **исправлен** |
| Готовность ядра протокола в репозитории | Высокая: тесты, vet, fuzz smoke зелёные |
| Коммерческий VPN-сервис | Не готов без DPI-стенда, полноценных клиентов и MASQUE |
| Невидимость / необнаружимость | **НЕ ГАРАНТИРУЕТСЯ** |

---

## Что проверено в этом цикле

Сессия, AEAD, epoch/seq, replay, rekey ECDH и overlap, билеты EdDSA + device_pub, Control Plane (секрет, activate, каталог), TLS 1.3 min / ECH / SPKI pin, connector AUTH.

Тесты: `go test ./...`, `go vet ./...`, fuzz `FuzzDecodeWireRecord` / `FuzzDecodeInner` / `FuzzTicketVerify` (~8–10 с). Race на этой Windows-машине без gcc не собирается; в CI (Ubuntu) race включён.

---

## Находки этого цикла (закрыты в коде)

1. **P0 replay.** Окно не сдвигало bitmap при росте `highest`. После seq 0,1 повтор seq 0 принимался. Исправлено: `bitmap[i]` = видели ли `highest-i`; есть флаг первого пакета.
2. **P0 rekey race.** Responder слал ACK, затем применял ключи. На быстом транспорте initiator уже слал новую эпоху. Теперь ключи применяются до ACK; ACK запечатывается старым AEAD.
3. **P1 одновременный rekey.** Обе стороны игнорировали чужой INIT. Тай-брейк по X25519 pub (при равенстве уступает клиент).
4. **P1 AUTH oracle.** `AUTH_FAIL` всегда с одним кодом.
5. **P1 max_devices.** Повторная активация того же устройства больше не занимает слот; пустой/короткий device_pub отвергается.
6. **P1 каталог stub.** `nodes[:0]` портил `cfg.Catalog`. Копирование среза.
7. **P1 билет без device_pub.** `Verify` требует 32-байтовый ключ.
8. **P1 failover.** Узлы без heartbeat (`LastSeen` zero) больше не отбрасываются как unhealthy.
9. **P2 padding.** Rejection sampling вместо `uint64 % n`.
10. **P2 секрет лицензии.** Опциональное шифрование at-rest (`NVP_LICENSE_KEK`, 64 hex).
11. **P2 connector.** AUTH по device key; каталог без `?role=`.
12. **Прочее.** SQLite WAL/busy_timeout; prune rate-limiter; лимит PONG; строгий padding flag.

---

## Остаточный риск (принимается)

| Тема | Почему не закрыто кодом |
|------|-------------------------|
| Аудитор = автор | Слепые зоны |
| DPI / TSPU | Нет стенда |
| ALPN h2/h3 без HTTP | Классификатор может заметить |
| MASQUE / полные TUN-клиенты | Заглушки / фундамент |
| AUTH не на TLS exporter | Транспорт не отдаёт exporter |
| Wipe X25519 в куче Go | Неполный |
| HA Control Plane | SQLite — один инстанс |

---

## Регламент

После каждой крупной правки протокола, крипты или Control Plane — повторный аудит на русском, тот же контур.
