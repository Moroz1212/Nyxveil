namespace Nyxveil.Client.Core;

/// <summary>Payload the GUI sends to the service over Named Pipe (no License Credential).</summary>
public sealed class ConnectPreparation
{
    public required string DesiredLocationId { get; init; }
    public required string AccessTicket { get; init; }
    public required byte[] SignedCatalogJson { get; init; }
    public required IReadOnlyDictionary<string, string> CatalogKeys { get; init; }
    public required byte[] DevicePrivateKey { get; init; }
    public required string ControlPlaneHost { get; init; }
}

public delegate void CatalogDiagSink(string message);

/// <summary>
/// First-run / connect orchestration: license stays in GUI Credential Manager;
/// service receives only ticket + catalog + keys + ephemeral device key.
/// </summary>
public sealed class SessionBootstrap : IDisposable
{
    private readonly ControlPlaneClient _cp;
    private readonly ClientSettings _settings;
    private readonly bool _ownsClient;
    private CatalogDiagSink? _diag;

    public SessionBootstrap(ControlPlaneClient cp, ClientSettings settings, bool ownsClient = false)
    {
        _cp = cp;
        _settings = settings;
        _ownsClient = ownsClient;
    }

    public static SessionBootstrap FromSettings(ClientSettings settings) =>
        new(ControlPlaneHttp.CreateClient(settings), settings, ownsClient: true);

    public void SetCatalogDiagnostics(CatalogDiagSink? sink) => _diag = sink;

    public void Dispose()
    {
        if (_ownsClient)
            _cp.Dispose();
    }

    private void Diag(string message) => _diag?.Invoke(message);

    /// <summary>Validate license, activate device, persist credential. License never goes to the service.</summary>
    public async Task ActivateLicenseAsync(string licenseToken, CancellationToken ct = default)
    {
        licenseToken = licenseToken.Trim();
        if (string.IsNullOrWhiteSpace(licenseToken))
            throw new InvalidOperationException("Введите лицензионный ключ.");

        var validation = await _cp.ValidateAsync(licenseToken, ct).ConfigureAwait(false);
        if (!validation.Valid)
            throw new InvalidOperationException(validation.Message ?? "Лицензия недействительна.");

        var device = DeviceIdentityStore.LoadOrCreate();
        var activated = await _cp.ActivateDeviceAsync(new DeviceActivateRequest
        {
            LicenseToken = licenseToken,
            DeviceId = device.DeviceId,
            PublicKey = device.PublicKey,
            Platform = "windows",
            DeviceName = Environment.MachineName
        }, ct).ConfigureAwait(false);

        if (!activated.Activated)
            throw new InvalidOperationException("Не удалось активировать устройство.");

        LicenseCredentialStore.Save(licenseToken);
        _settings.Save();
    }

    /// <summary>
    /// Ensure a signature-valid, temporally-valid catalog.
    /// Uses cache only when still valid; otherwise mandatory network refresh.
    /// Never returns an expired cache after a failed refresh.
    /// </summary>
    public async Task<(SignedCatalogDto Catalog, byte[] RawJson, CatalogKeysResponse Keys)> FetchAndVerifyCatalogAsync(
        string licenseToken, CancellationToken ct = default)
    {
        var cached = CatalogCacheStore.TryLoad();
        if (cached is not null)
        {
            var rawCached = cached.TryGetRaw();
            if (rawCached is { Length: > 0 } && cached.Keys.Count > 0)
            {
                try
                {
                    var signedCached = CatalogVerifier.Parse(rawCached);
                    var report = CatalogVerifier.Verify(signedCached, cached.Keys);
                    LogReport("cache", report, refreshReason: null, http: null);
                    return (signedCached, rawCached, cached.ToKeysResponse());
                }
                catch (CatalogTemporalException tex)
                {
                    var reason = tex.Report.NowUtc < tex.Report.IssuedAt ? "not_yet_valid" : "expired";
                    Diag($"CATALOG refresh reason={reason}");
                    LogReport("cache", tex.Report, refreshReason: reason, http: null);
                    // fall through to network refresh
                }
                catch (InvalidOperationException ex) when (ex.Message.Contains("Подпись", StringComparison.Ordinal) ||
                                                           ex.Message.Contains("ключ", StringComparison.OrdinalIgnoreCase))
                {
                    Diag("CATALOG refresh reason=signature_or_key");
                    Diag("CATALOG signature=FAIL");
                    // fall through
                }
            }
            else
            {
                Diag("CATALOG refresh reason=missing");
            }
        }
        else
        {
            Diag("CATALOG refresh reason=missing");
        }

        return await FetchNetworkAndCacheAsync(licenseToken, ct).ConfigureAwait(false);
    }

    /// <summary>Connect path: always prefer a fresh network catalog; valid cache is fallback only if fetch fails.</summary>
    public async Task<(SignedCatalogDto Catalog, byte[] RawJson, CatalogKeysResponse Keys)> EnsureCatalogForConnectAsync(
        string licenseToken, CancellationToken ct = default)
    {
        try
        {
            Diag("CATALOG refresh reason=forced");
            return await FetchNetworkAndCacheAsync(licenseToken, ct).ConfigureAwait(false);
        }
        catch (Exception fetchEx) when (fetchEx is not CatalogTemporalException)
        {
            // Network/HTTP failure: allow still-valid cache; never expired cache.
            var cached = CatalogCacheStore.TryLoad();
            var rawCached = cached?.TryGetRaw();
            if (cached is null || rawCached is not { Length: > 0 } || cached.Keys.Count == 0)
                throw;

            try
            {
                var signedCached = CatalogVerifier.Parse(rawCached);
                var report = CatalogVerifier.Verify(signedCached, cached.Keys);
                Diag("CATALOG fetch failed; using valid cache fallback");
                LogReport("cache", report, refreshReason: "fetch_failed_valid_cache", http: null);
                Diag("CATALOG fetch error=" + Truncate(fetchEx.Message, 160));
                return (signedCached, rawCached, cached.ToKeysResponse());
            }
            catch (CatalogTemporalException)
            {
                // Expired cache + fetch failure → real fetch error (not "catalog expired").
                throw new InvalidOperationException(
                    "Не удалось обновить каталог: " + Truncate(fetchEx.Message, 200), fetchEx);
            }
        }
    }

    private async Task<(SignedCatalogDto Catalog, byte[] RawJson, CatalogKeysResponse Keys)> FetchNetworkAndCacheAsync(
        string licenseToken, CancellationToken ct)
    {
        CatalogKeysResponse keys;
        try
        {
            keys = await _cp.GetCatalogKeysAsync(licenseToken, ct).ConfigureAwait(false);
        }
        catch (Exception ex)
        {
            Diag("CATALOG fetch HTTP=keys_failed");
            throw new InvalidOperationException("Не удалось получить ключи каталога: " + Truncate(ex.Message, 160), ex);
        }

        if (keys.Keys.Count == 0)
            throw new InvalidOperationException("Control Plane не вернул ключи каталога (нужен CP ≥ 1.0.4).");

        byte[] raw;
        try
        {
            raw = await _cp.GetCatalogRawAsync(licenseToken, ct).ConfigureAwait(false);
            Diag("CATALOG fetch HTTP=200");
        }
        catch (Exception ex)
        {
            Diag("CATALOG fetch HTTP=catalog_failed");
            throw new InvalidOperationException("Не удалось получить каталог: " + Truncate(ex.Message, 160), ex);
        }

        var signed = CatalogVerifier.Parse(raw);
        try
        {
            var report = CatalogVerifier.Verify(signed, keys.Keys);
            LogReport("network", report, refreshReason: null, http: 200);
            CatalogCacheStore.Save(raw, keys);
            return (signed, raw, keys);
        }
        catch (CatalogTemporalException tex)
        {
            LogReport("network", tex.Report, refreshReason: null, http: 200);
            throw;
        }
        catch (InvalidOperationException)
        {
            Diag("CATALOG source=network");
            Diag("CATALOG signature=FAIL");
            throw;
        }
    }

    private void LogReport(string source, CatalogVerifyReport report, string? refreshReason, int? http)
    {
        Diag($"CATALOG source={source}");
        Diag($"CATALOG key_id={report.KeyId}");
        Diag($"CATALOG issued_at={report.IssuedAt:yyyy-MM-dd'T'HH:mm:ss.fff'Z'}");
        Diag($"CATALOG not_before={report.IssuedAt:yyyy-MM-dd'T'HH:mm:ss.fff'Z'}");
        Diag($"CATALOG expires_at={report.ExpiresAt:yyyy-MM-dd'T'HH:mm:ss.fff'Z'}");
        Diag($"CATALOG now_utc={report.NowUtc:yyyy-MM-dd'T'HH:mm:ss.fff'Z'}");
        Diag($"CATALOG remaining_seconds={report.RemainingSeconds}");
        Diag($"CATALOG temporal_validation={report.TemporalValidation}");
        Diag($"CATALOG signature={report.Signature}");
        if (refreshReason is not null)
            Diag($"CATALOG refresh reason={refreshReason}");
        if (http is not null)
            Diag($"CATALOG fetch HTTP={http}");
    }

    private static string Truncate(string s, int max) =>
        string.IsNullOrEmpty(s) ? "" : s.Length <= max ? s : s[..max] + "…";

    public async Task<IReadOnlyList<LocationDto>> LoadEnabledLocationsAsync(CancellationToken ct = default)
    {
        var license = LicenseCredentialStore.Load()
            ?? throw new InvalidOperationException("Лицензия не сохранена.");
        var (signed, _, _) = await FetchAndVerifyCatalogAsync(license, ct).ConfigureAwait(false);
        return signed.Catalog.Locations.Where(l => l.Enabled).ToList();
    }

    public async Task<ConnectPreparation> PrepareConnectAsync(string locationId, CancellationToken ct = default)
    {
        if (string.IsNullOrWhiteSpace(locationId))
            throw new InvalidOperationException("Выберите локацию.");

        var license = LicenseCredentialStore.Load()
            ?? throw new InvalidOperationException("Лицензия не сохранена. Пройдите первый запуск.");

        var device = DeviceIdentityStore.LoadOrCreate();
        var (signed, raw, keys) = await EnsureCatalogForConnectAsync(license, ct).ConfigureAwait(false);

        if (!signed.Catalog.Locations.Any(l => l.Enabled && l.LocationId == locationId))
            throw new InvalidOperationException("Выбранная локация недоступна.");

        var ticket = await _cp.IssueTicketAsync(new TicketIssueRequest
        {
            LicenseToken = license,
            DeviceId = device.DeviceId,
            LocationId = locationId
        }, ct).ConfigureAwait(false);

        if (string.IsNullOrWhiteSpace(ticket.AccessTicket))
            throw new InvalidOperationException("Control Plane не выдал access ticket.");

        return new ConnectPreparation
        {
            DesiredLocationId = locationId,
            AccessTicket = ticket.AccessTicket,
            SignedCatalogJson = raw,
            CatalogKeys = keys.Keys,
            DevicePrivateKey = device.PrivateKeyGoFormat,
            ControlPlaneHost = _settings.GetControlPlaneHost()
        };
    }

    /// <summary>Issue a fresh ticket for NeedAccessTicket IPC (license stays in GUI).</summary>
    public async Task<string> RefreshAccessTicketAsync(string locationId, CancellationToken ct = default)
    {
        var license = LicenseCredentialStore.Load()
            ?? throw new InvalidOperationException("Лицензия не сохранена.");
        var device = DeviceIdentityStore.LoadOrCreate();
        var ticket = await _cp.IssueTicketAsync(new TicketIssueRequest
        {
            LicenseToken = license,
            DeviceId = device.DeviceId,
            LocationId = locationId
        }, ct).ConfigureAwait(false);
        if (string.IsNullOrWhiteSpace(ticket.AccessTicket))
            throw new InvalidOperationException("Не удалось обновить access ticket.");
        return ticket.AccessTicket;
    }
}
