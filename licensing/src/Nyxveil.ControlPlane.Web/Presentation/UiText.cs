using System.Globalization;
using Microsoft.AspNetCore.Identity;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Web.Presentation;

public static class UiText
{
    public static CultureInfo Culture { get; } = CultureInfo.GetCultureInfo("ru-RU");
    public static string Status(string? value) => value?.ToLowerInvariant() switch
    {
        "healthy" => "Работает штатно",
        "active" => "Активно",
        "online" => "В сети",
        "degraded" => "С ограничениями",
        "offline" => "Не в сети",
        "disabled" => "Отключено",
        "revoked" => "Отозвано",
        "expired" => "Срок истёк",
        "pending" => "Ожидает",
        "claimed" => "Принято сервером",
        "running" or "executing" => "Выполняется",
        "accepted" => "Ожидание подтверждения",
        "succeeded" => "Выполнено",
        "failed" => "Не выполнено",
        "cancelled" => "Отменено",
        "expiring" => "Скоро истекает",
        "maintenance" => "Обслуживание",
        "draining" => "Завершение сеансов",
        "exhausted" => "Лимит исчерпан",
        "current" => "Текущий",
        "next" => "Следующий",
        "retired" => "Выведен из обращения",
        null or "" => "Неизвестно",
        _ => value
    };
    public static string Role(string value) => value switch
    {
        "SuperAdmin" => "Главный администратор",
        "Operator" => "Оператор",
        "ReadOnly" => "Только просмотр",
        "user" => "Пользователь",
        "master" => "Мастер",
        "test" => "Тестирование",
        _ => value
    };
    public static string Revocation(RevocationType value) => value switch
    {
        RevocationType.Ticket => "Билет доступа",
        RevocationType.License => "Лицензия",
        RevocationType.Device => "Устройство",
        _ => value.ToString()
    };
    public static string Number(double? value, string format = "0.0") =>
        value is { } number && double.IsFinite(number) ? number.ToString(format, Culture) : "—";
    public static string IdentityError(IdentityError error) => error.Code switch
    {
        "PasswordTooShort" => "Пароль должен содержать не менее 12 символов.",
        "PasswordRequiresNonAlphanumeric" => "Добавьте в пароль специальный символ.",
        "PasswordRequiresDigit" => "Добавьте в пароль цифру.",
        "PasswordRequiresLower" => "Добавьте в пароль строчную латинскую букву.",
        "PasswordRequiresUpper" => "Добавьте в пароль заглавную латинскую букву.",
        "PasswordRequiresUniqueChars" => "В пароле слишком мало различных символов.",
        "DuplicateUserName" or "DuplicateEmail" => "Учётная запись с такой почтой уже существует.",
        "InvalidUserName" or "InvalidEmail" => "Укажите корректный адрес электронной почты.",
        _ => "Не удалось сохранить учётную запись. Проверьте введённые данные."
    };
    public static string Error(string message) => message switch
    {
        "node has not advertised support for this command" => "Сервер не подтвердил поддержку этой операции. Проверьте его версию и последний отчёт.",
        "refusing disruptive command: this is the last eligible healthy node in the location" => "Операция запрещена: в этой локации нет другого здорового сервера со свободной ёмкостью.",
        "another disruptive command is already active in this location; wait for it to finish" => "В этой локации уже выполняется операция. Дождитесь её завершения.",
        "another command is already active on this node" => "На сервере уже есть незавершённая команда. Проверьте журнал операций.",
        "location has an active management command; wait for completion before changing node configuration" => "Настройки локации защищены на время выполнения команды. Дождитесь результата.",
        "management operation is busy; retry shortly" => "Другая операция удерживает блокировку. Повторите через несколько секунд.",
        "latest stable server release is unknown; refresh Server Releases and retry" => "Не удалось подтвердить стабильный выпуск. Проверьте GitHub Releases в разделе обновлений.",
        "Issued PFX is missing; certificate activation was not performed" => "Файл выпущенного сертификата отсутствует. Активация не выполнялась; создайте новый запрос.",
        "verify an unexpired DNS challenge before issuing" => "Перед выпуском проверьте действующую TXT-запись.",
        "DNS challenge is not active or has expired" => "Запрос DNS уже завершён или истёк. Создайте новый запрос.",
        "capacity must be >= 0" => "Лимит сеансов не может быть отрицательным.",
        "location is required" => "Укажите локацию.",
        "location not found" => "Локация не найдена.",
        "target location is disabled" => "Выбранная локация отключена.",
        "node config not found" => "Конфигурация сервера не найдена.",
        "node_id is required" => "Укажите идентификатор сервера.",
        "node not found" => "Сервер не найден.",
        "node config changed concurrently; reload and retry" => "Настройки уже изменены другим администратором. Обновите страницу и повторите действие.",
        "plan not found" => "Тариф не найден.",
        "license not found" => "Лицензия не найдена.",
        "cannot enable revoked license" => "Нельзя включить отозванную лицензию.",
        _ when message.StartsWith("location not found: ", StringComparison.Ordinal) => "Локация не найдена: " + message[20..],
        _ => "Не удалось выполнить действие. Проверьте введённые данные."
    };
}
