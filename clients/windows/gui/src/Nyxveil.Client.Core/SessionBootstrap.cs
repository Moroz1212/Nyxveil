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

/// <summary>
/// First-run / connect orchestration: license stays in GUI Credential Manager;
/// service receives only ticket + catalog + keys + ephemeral device key.
/// </summary>
public sealed class SessionBootstrap : IDisposable
{
    private readonly ControlPlaneClient _cp;
    private readonly ClientSettings _settings;
    private readonly bool _ownsClient;

    public SessionBootstrap(ControlPlaneClient cp, ClientSettings settings, bool ownsClient = false)
    {
        _cp = cp;
        _settings = settings;
        _ownsClient = ownsClient;
    }

    public static SessionBootstrap FromSettings(ClientSettings settings) =>
        new(ControlPlaneHttp.CreateClient(settings), settings, ownsClient: true);

    public void Dispose()
    {
        if (_ownsClient)
            _cp.Dispose();
    }

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

    public async Task<(SignedCatalogDto Catalog, byte[] RawJson, CatalogKeysResponse Keys)> FetchAndVerifyCatalogAsync(
        string licenseToken, CancellationToken ct = default)
    {
        var keys = await _cp.GetCatalogKeysAsync(licenseToken, ct).ConfigureAwait(false);
        if (keys.Keys.Count == 0)
            throw new InvalidOperationException("Control Plane не вернул ключи каталога (нужен CP ≥ 1.0.4).");

        var raw = await _cp.GetCatalogRawAsync(licenseToken, ct).ConfigureAwait(false);
        var signed = CatalogVerifier.Parse(raw);
        CatalogVerifier.Verify(signed, keys.Keys);
        return (signed, raw, keys);
    }

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
        var (signed, raw, keys) = await FetchAndVerifyCatalogAsync(license, ct).ConfigureAwait(false);

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
