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
