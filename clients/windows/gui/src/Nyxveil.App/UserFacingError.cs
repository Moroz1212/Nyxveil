namespace Nyxveil.App;

internal static class UserFacingError
{
    public static string? Map(string? technical)
    {
        if (string.IsNullOrWhiteSpace(technical))
            return null;
        var msg = technical;
        if (ContainsAny(msg, "certificate", "SSL", "TLS", "SPKI", "identity"))
            return "Ошибка доверия TLS / SPKI. Нужен доверенный сертификат узла и корректный pin из каталога.";
        if (ContainsAny(msg, "expired", "истёк", "истек"))
            return "Срок действия лицензии истёк";
        if (ContainsAny(msg, "invalid license", "license invalid", "unauthorized", "401", "403", "неверн"))
            return "Неверные данные";
        if (ContainsAny(msg, "license", "credential"))
            return "Лицензия недействительна";
        if (ContainsAny(msg, "verify", "full-tunnel", "network apply", "dataplane incomplete"))
            return "Не удалось применить маршруты/DNS VPN. Подключение отменено.";
        if (ContainsAny(msg, "Wintun", "ErrNotLinked", "CreateAdapter", "adapter"))
            return "Ошибка сетевого адаптера VPN (Wintun). Переустановите клиент или повторите подключение.";
        if (ContainsAny(msg, "TypeConfig", "config timeout"))
            return "Сервер не прислал сетевую конфигурацию.";
        if (ContainsAny(msg, "dns_servers") || (ContainsAny(msg, "routeplan") && ContainsAny(msg, "dns")))
            return "Сервер не передал DNS (TypeConfig). Подключение невозможно.";
        if (ContainsAny(msg, "unavailable", "refused", "timeout", "transport", "HttpRequest", "socket", "connect", "DNS"))
            return "Не удалось связаться с сервером";
        return msg;
    }

    public static string Format(Exception ex)
    {
        var mapped = Map(ex.Message);
        if (mapped is not null && mapped != ex.Message)
            return mapped;
        var msg = ex.Message;
        if (ex.InnerException is not null && !string.IsNullOrWhiteSpace(ex.InnerException.Message))
            msg += " — " + ex.InnerException.Message;
        return Map(msg) ?? Sanitize(msg);
    }

    /// <summary>Activation / first-run friendly messages (no stack traces / raw protocol text).</summary>
    public static string FormatLicense(Exception ex)
    {
        var mapped = Format(ex);
        if (mapped is "Неверные данные" or "Лицензия недействительна" or "Срок действия лицензии истёк"
            or "Не удалось связаться с сервером")
            return mapped + "\nПопробовать снова";
        if (LooksTechnical(mapped))
            return "Не удалось связаться с сервером\nПопробовать снова";
        return mapped;
    }

    private static string Sanitize(string msg)
    {
        if (LooksTechnical(msg))
            return "Не удалось связаться с сервером";
        return msg;
    }

    private static bool LooksTechnical(string msg) =>
        ContainsAny(msg, "Exception", "at ", "System.", "HttpRequest", "{", "}", "stack", "null reference");

    private static bool ContainsAny(string msg, params string[] parts)
    {
        foreach (var p in parts)
        {
            if (msg.Contains(p, StringComparison.OrdinalIgnoreCase))
                return true;
        }
        return false;
    }
}
