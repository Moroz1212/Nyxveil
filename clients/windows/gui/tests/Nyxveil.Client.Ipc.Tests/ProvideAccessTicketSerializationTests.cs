using Xunit;

namespace Nyxveil.Client.Ipc.Tests;

public class ProvideAccessTicketSerializationTests
{
    [Fact]
    public void ProvideAccessTicket_RoundTripsRequestId()
    {
        var cmd = new ProvideAccessTicketCommand
        {
            RequestId = "abc123",
            AccessTicket = "ticket-value"
        };
        var json = System.Text.Json.JsonSerializer.Serialize(cmd);
        var back = System.Text.Json.JsonSerializer.Deserialize<ProvideAccessTicketCommand>(json);
        Assert.NotNull(back);
        Assert.Equal("provide_access_ticket", back!.Type);
        Assert.Equal("abc123", back.RequestId);
        Assert.Equal("ticket-value", back.AccessTicket);
    }

    [Fact]
    public void NeedAccessTicket_HasLocationReasonState()
    {
        var json = """{"v":1,"type":"need_access_ticket","request_id":"r1","location_id":"fi-hel","reason":"failover","state":"Reconnecting"}""";
        var msg = System.Text.Json.JsonSerializer.Deserialize<NeedAccessTicketMessage>(json);
        Assert.NotNull(msg);
        Assert.Equal("r1", msg!.RequestId);
        Assert.Equal("fi-hel", msg.DesiredLocationId);
        Assert.Equal("failover", msg.Reason);
        Assert.Equal("Reconnecting", msg.State);
    }
}
