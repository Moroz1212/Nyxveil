using System.Buffers.Binary;
using System.Net;
using System.Net.Sockets;
using System.Security.Cryptography;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Playwright;

namespace Nyxveil.ControlPlane.BrowserE2E;

public sealed class OperatorBrowserTests : IAsyncLifetime
{
    private BrowserWebApplicationFactory _factory = null!;
    private IPlaywright _playwright = null!;
    private IBrowser _browser = null!;
    private string _baseUrl = "";

    public async Task InitializeAsync()
    {
        _factory = new BrowserWebApplicationFactory();
        var port = GetFreeTcpPort();
        _factory.UseKestrel(port);
        using var client = _factory.CreateClient(new WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false
        });
        _baseUrl = $"http://127.0.0.1:{port}";
        await _factory.SeedAsync();

        _playwright = await Playwright.CreateAsync();
        _browser = await _playwright.Chromium.LaunchAsync(new BrowserTypeLaunchOptions
        {
            Headless = true
        });
    }

    [Fact]
    public async Task OperatorButtons_RenderAndInvokeBlazorHandlers()
    {
        var page = await _browser.NewPageAsync();

        await page.GotoAsync(_baseUrl + "/account/login");
        await Assertions.Expect(page.GetByRole(AriaRole.Heading, new() { Name = "Войти" }))
            .ToBeVisibleAsync();
        await Assertions.Expect(page.Locator("input[name=email]")).ToBeVisibleAsync();
        await Assertions.Expect(page.Locator("input[name=password]")).ToBeVisibleAsync();

        await page.Locator("input[name=email]").FillAsync(BrowserWebApplicationFactory.AdminEmail);
        await page.Locator("input[name=password]").FillAsync(BrowserWebApplicationFactory.AdminPassword);
        await page.GetByRole(AriaRole.Button, new() { Name = "Войти" }).ClickAsync();
        // Prefer element visibility over navigation race (CI runners can be slow past NetworkIdle).
        var totpInput = page.Locator("input[name=code]");
        await totpInput.WaitForAsync(new LocatorWaitForOptions
        {
            State = WaitForSelectorState.Visible,
            Timeout = 60_000
        });
        await totpInput.FillAsync(GenerateTotp(_factory.TotpSecret));
        await page.GetByRole(AriaRole.Button, new() { Name = "Подтвердить" }).ClickAsync();
        await page.WaitForURLAsync(
            url => !url.Contains("/account/", StringComparison.OrdinalIgnoreCase),
            new PageWaitForURLOptions { Timeout = 60_000 });

        page.SetDefaultTimeout(60_000);

        await page.GotoAsync(_baseUrl + "/admin/control-plane");
        await Assertions.Expect(page.GetByRole(AriaRole.Heading, new() { Name = "Control Plane" }))
            .ToBeVisibleAsync();
        var controlPlaneUpdate = page.GetByTestId("control-plane-update");
        await Assertions.Expect(controlPlaneUpdate).ToBeVisibleAsync();
        await Assertions.Expect(controlPlaneUpdate).ToBeEnabledAsync();
        await page.WaitForTimeoutAsync(1_000);
        await controlPlaneUpdate.ClickAsync();
        await Assertions.Expect(
                page.GetByText("Требуется повторная проверка MFA (step-up).")
                    .Or(page.GetByRole(AriaRole.Heading, new() { Name = "Pre-flight" })))
            .ToBeVisibleAsync();

        await page.GotoAsync(_baseUrl + "/admin/nodes/" + BrowserWebApplicationFactory.NodeId);
        var nodeUpdate = page.GetByTestId("node-update");
        await Assertions.Expect(nodeUpdate).ToBeVisibleAsync();
        await Assertions.Expect(nodeUpdate).ToBeEnabledAsync();
        await page.WaitForTimeoutAsync(1_000);
        await nodeUpdate.ClickAsync();
        var nodePreflight = page.GetByRole(
            AriaRole.Heading, new() { Name = "Проверка перед обновлением" });
        await Assertions.Expect(
                page.GetByText("Требуется повторная проверка MFA (step-up).")
                    .Or(nodePreflight))
            .ToBeVisibleAsync();
        if (await nodePreflight.IsVisibleAsync())
            await page.GotoAsync(_baseUrl + "/admin/nodes/" + BrowserWebApplicationFactory.NodeId);

        var certificateRenew = page.GetByTestId("node-certificate-renew");
        await Assertions.Expect(certificateRenew).ToBeVisibleAsync();
        await Assertions.Expect(certificateRenew).ToBeEnabledAsync();
        await page.WaitForTimeoutAsync(1_000);
        await certificateRenew.ClickAsync();
        var commandPersisted = false;
        for (var attempt = 0; attempt < 20 && !commandPersisted; attempt++)
        {
            commandPersisted = await _factory.HasCertificateRenewCommandAsync();
            if (!commandPersisted)
                await Task.Delay(100);
        }
        Assert.True(commandPersisted,
            "Certificate button must enqueue through INodeCommandService, not merely render.");
    }

    public async Task DisposeAsync()
    {
        if (_browser is not null)
            await _browser.DisposeAsync();
        _playwright?.Dispose();
        _factory?.Dispose();
    }

    private static string GenerateTotp(string base32Secret)
    {
        var key = DecodeBase32(base32Secret);
        Span<byte> counter = stackalloc byte[8];
        BinaryPrimitives.WriteInt64BigEndian(
            counter, DateTimeOffset.UtcNow.ToUnixTimeSeconds() / 30);
        var hash = HMACSHA1.HashData(key, counter);
        var offset = hash[^1] & 0x0f;
        var binary = ((hash[offset] & 0x7f) << 24)
                     | (hash[offset + 1] << 16)
                     | (hash[offset + 2] << 8)
                     | hash[offset + 3];
        return (binary % 1_000_000).ToString("D6");
    }

    private static int GetFreeTcpPort()
    {
        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        try
        {
            return ((IPEndPoint)listener.LocalEndpoint).Port;
        }
        finally
        {
            listener.Stop();
        }
    }

    private static byte[] DecodeBase32(string text)
    {
        const string alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
        var output = new List<byte>();
        var buffer = 0;
        var bits = 0;
        foreach (var c in text.TrimEnd('=').ToUpperInvariant())
        {
            var value = alphabet.IndexOf(c);
            if (value < 0)
                throw new FormatException("Invalid Base32 authenticator secret.");
            buffer = (buffer << 5) | value;
            bits += 5;
            if (bits < 8)
                continue;
            bits -= 8;
            output.Add((byte)(buffer >> bits));
            buffer &= (1 << bits) - 1;
        }
        return output.ToArray();
    }
}
