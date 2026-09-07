using Xunit;
using Nyxveil.Client.Ipc;

namespace Nyxveil.Client.Ipc.Tests;

public class LogMessageSerializationTests
{
    [Fact]
    public void LogsSnapshot_RoundTripsEntries()
    {
        var snap = new LogsSnapshotMessage
        {
            Type = IpcProtocol.TypeLogsSnapshot,
            Id = "abc",
            Entries =
            {
                new LogLineDto
                {
                    Time = "2026-09-07 20:10:01.123",
                    Level = "INFO",
                    Component = "SESSION",
                    Event = "Connect",
                    Message = "loc=fi",
                    Line = "2026-09-07 20:10:01.123 INFO  SESSION      Connect loc=fi"
                }
            }
        };
        var json = System.Text.Json.JsonSerializer.Serialize(snap);
        var back = System.Text.Json.JsonSerializer.Deserialize<LogsSnapshotMessage>(json);
        Assert.NotNull(back);
        Assert.Equal(IpcProtocol.TypeLogsSnapshot, back!.Type);
        Assert.Single(back.Entries);
        Assert.Contains("SESSION", back.Entries[0].Line);
        Assert.DoesNotContain("nyx_lic_", back.Entries[0].Line);
    }

    [Fact]
    public void LogEvent_HasType()
    {
        var ev = new LogEventMessage
        {
            Type = IpcProtocol.TypeLogEvent,
            Entry = new LogLineDto { Line = "x", Level = "ERROR", Component = "TRANSPORT", Event = "EOF" }
        };
        var json = System.Text.Json.JsonSerializer.Serialize(ev);
        Assert.Contains("log_event", json);
    }
}
