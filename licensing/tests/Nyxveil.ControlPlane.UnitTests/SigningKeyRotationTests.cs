using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Application.Options;
using Nyxveil.ControlPlane.Domain.Enums;
using Nyxveil.ControlPlane.Infrastructure.Security;
using Nyxveil.ControlPlane.UnitTests.Helpers;

namespace Nyxveil.ControlPlane.UnitTests;

public sealed class SigningKeyRotationTests : IAsyncDisposable
{
    private readonly ControlPlaneTestFixture _fx = new();
    private readonly FakeClock _clock;

    public SigningKeyRotationTests()
    {
        _clock = (FakeClock)_fx.Clock;
    }

    public async ValueTask DisposeAsync() => await _fx.DisposeAsync();

    private Ed25519SigningKeyStore CreateStore(TimeSpan? prepublish = null, TimeSpan? grace = null)
    {
        var rotation = Options.Create(new SigningKeyRotationOptions
        {
            NextPrepublishPeriod = prepublish ?? TimeSpan.Zero,
            RetiringGracePeriod = grace ?? TimeSpan.FromHours(2),
            CatalogLifetime = TimeSpan.FromMinutes(1),
            ClockSkewMargin = TimeSpan.Zero,
            PropagationMargin = TimeSpan.Zero
        });
        var tickets = Options.Create(new TicketOptions { TtlMinutes = 1 });
        return new Ed25519SigningKeyStore(
            _fx.Scope.ServiceProvider.GetRequiredService<IServiceScopeFactory>(),
            _fx.Scope.ServiceProvider.GetRequiredService<Microsoft.AspNetCore.DataProtection.IDataProtectionProvider>(),
            _clock,
            rotation,
            tickets);
    }

    [Fact]
    public async Task Rotate_KeepsPreviousInVerificationRing()
    {
        var store = CreateStore();
        var before = await store.GetCurrentSigningMaterialAsync();
        var nextKeys = await store.GetVerificationKeysAsync();
        Assert.Contains(nextKeys, k => k.Status == nameof(SigningKeyStatus.Next));

        // Allow immediate rotate (prepublish=0 for test).
        var result = await store.RotateAsync();
        Assert.Equal(before.KeyId, result.PreviousKeyId);
        Assert.NotEqual(before.KeyId, result.NewKeyId);

        var ring = await store.GetVerificationKeysAsync();
        Assert.Contains(ring, k => k.KeyId == before.KeyId && k.Status == nameof(SigningKeyStatus.Retiring));
        Assert.Contains(ring, k => k.KeyId == result.NewKeyId && k.Status == nameof(SigningKeyStatus.Current));
        Assert.Contains(ring, k => k.Status == nameof(SigningKeyStatus.Next));
    }

    [Fact]
    public async Task AfterGrace_RetiringKeyLeavesVerificationRing()
    {
        var store = CreateStore(grace: TimeSpan.FromMinutes(2));
        var before = await store.GetCurrentSigningMaterialAsync();
        await store.RotateAsync();

        _clock.Advance(TimeSpan.FromMinutes(3));
        var n = await store.FinalizeExpiredRetiringAsync();
        Assert.True(n >= 1);

        var ring = await store.GetVerificationKeysAsync();
        Assert.DoesNotContain(ring, k => k.KeyId == before.KeyId);
    }

    [Fact]
    public async Task DoubleRotate_BlockedWhileRetiringActive()
    {
        var store = CreateStore();
        await store.GetCurrentSigningMaterialAsync();
        await store.RotateAsync();
        await Assert.ThrowsAsync<ConflictException>(() => store.RotateAsync());
    }

    [Fact]
    public async Task NewContentUsesNewCurrent()
    {
        var store = CreateStore();
        var old = await store.GetCurrentSigningMaterialAsync();
        var rotated = await store.RotateAsync();
        var cur = await store.GetCurrentSigningMaterialAsync();
        Assert.Equal(rotated.NewKeyId, cur.KeyId);
        Assert.NotEqual(old.KeyId, cur.KeyId);
    }
}
