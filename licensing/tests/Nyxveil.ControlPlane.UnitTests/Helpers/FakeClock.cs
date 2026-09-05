using Nyxveil.ControlPlane.Application.Abstractions;

namespace Nyxveil.ControlPlane.UnitTests.Helpers;

public sealed class FakeClock : IClock
{
    public FakeClock(DateTime? utcNow = null) => UtcNow = utcNow ?? DateTime.UtcNow;

    public DateTime UtcNow { get; set; }

    public void Advance(TimeSpan delta) => UtcNow = UtcNow.Add(delta);
}
