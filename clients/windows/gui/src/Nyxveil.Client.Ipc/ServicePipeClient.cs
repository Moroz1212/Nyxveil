using System.IO.Pipes;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Nyxveil.Client.Ipc;

/// <summary>
/// Named-pipe client to Nyxveil Windows service (LocalSystem).
/// License Credential is never sent. Pipe ACL: SYSTEM + Admins + provisioned user SID.
/// </summary>
public sealed class ServicePipeClient : IAsyncDisposable
{
    private static readonly JsonSerializerOptions JsonOpts = new()
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        PropertyNameCaseInsensitive = true
    };

    private readonly string _pipeName;
    private NamedPipeClientStream? _stream;
    private StreamReader? _reader;
    private StreamWriter? _writer;
    private CancellationTokenSource? _readCts;
    private Task? _readLoop;
    private readonly SemaphoreSlim _writeLock = new(1, 1);

    public ServicePipeClient(string? pipeName = null) =>
        _pipeName = string.IsNullOrWhiteSpace(pipeName) ? IpcProtocol.PipeName : pipeName;

    public bool IsConnected => _stream is { IsConnected: true };

    public event Action<StatusSnapshotMessage>? StatusReceived;
    public event Action<NeedAccessTicketMessage>? NeedAccessTicket;
    public event Action<string>? ErrorReceived;
    public event Action? Disconnected;

    public async Task ConnectAsync(CancellationToken ct = default)
    {
        if (IsConnected)
            return;

        var pipe = new NamedPipeClientStream(
            serverName: ".",
            pipeName: StripPipePrefix(_pipeName),
            direction: PipeDirection.InOut,
            options: PipeOptions.Asynchronous);

        await pipe.ConnectAsync(5000, ct).ConfigureAwait(false);
        _stream = pipe;
        _reader = new StreamReader(pipe, Encoding.UTF8, detectEncodingFromByteOrderMarks: false, bufferSize: 65536, leaveOpen: true);
        _writer = new StreamWriter(pipe, new UTF8Encoding(encoderShouldEmitUTF8Identifier: false))
        {
            AutoFlush = true,
            NewLine = "\n"
        };

        _readCts = CancellationTokenSource.CreateLinkedTokenSource(ct);
        _readLoop = Task.Run(() => ReadLoopAsync(_readCts.Token), CancellationToken.None);

        await SendAsync(new IpcEnvelope { Type = IpcProtocol.TypeHello }, ct).ConfigureAwait(false);
    }

    public async Task SendConnectAsync(
        string desiredLocationId,
        string accessTicket,
        byte[] signedCatalogJson,
        IReadOnlyDictionary<string, string> catalogKeys,
        byte[] devicePrivateKey,
        string? controlPlaneHost,
        CancellationToken ct = default)
    {
        var cmd = new ConnectCommand
        {
            Id = Guid.NewGuid().ToString("N"),
            DesiredLocationId = desiredLocationId,
            AccessTicket = accessTicket,
            SignedCatalogJson = signedCatalogJson,
            CatalogKeys = new Dictionary<string, string>(catalogKeys),
            DevicePrivateKey = devicePrivateKey,
            ControlPlaneHost = controlPlaneHost
        };
        await SendAsync(cmd, ct).ConfigureAwait(false);
    }

    public Task SendDisconnectAsync(CancellationToken ct = default) =>
        SendAsync(new IpcEnvelope { Type = IpcProtocol.TypeDisconnect, Id = Guid.NewGuid().ToString("N") }, ct);

    public Task SendAccessTicketAsync(string requestId, string accessTicket, CancellationToken ct = default) =>
        SendAsync(new ProvideAccessTicketCommand
        {
            Id = requestId,
            RequestId = requestId,
            AccessTicket = accessTicket
        }, ct);

    public Task SendCancelAsync(CancellationToken ct = default) =>
        SendAsync(new IpcEnvelope { Type = IpcProtocol.TypeCancel }, ct);

    private async Task SendAsync(object message, CancellationToken ct)
    {
        if (_writer is null)
            throw new InvalidOperationException("IPC: нет соединения со службой Nyxveil.");

        var json = JsonSerializer.Serialize(message, message.GetType(), JsonOpts);
        await _writeLock.WaitAsync(ct).ConfigureAwait(false);
        try
        {
            await _writer.WriteLineAsync(json.AsMemory(), ct).ConfigureAwait(false);
        }
        finally
        {
            _writeLock.Release();
        }
    }

    private async Task ReadLoopAsync(CancellationToken ct)
    {
        try
        {
            while (!ct.IsCancellationRequested && _reader is not null)
            {
                string? line;
                try
                {
                    line = await _reader.ReadLineAsync(ct).ConfigureAwait(false);
                }
                catch (OperationCanceledException)
                {
                    break;
                }
                catch (IOException)
                {
                    break;
                }

                if (line is null)
                    break;
                if (string.IsNullOrWhiteSpace(line))
                    continue;

                DispatchLine(line);
            }
        }
        finally
        {
            Disconnected?.Invoke();
        }
    }

    private void DispatchLine(string line)
    {
        IpcEnvelope? env;
        try
        {
            env = JsonSerializer.Deserialize<IpcEnvelope>(line, JsonOpts);
        }
        catch
        {
            ErrorReceived?.Invoke("Некорректный ответ службы.");
            return;
        }

        if (env is null)
            return;

        switch (env.Type)
        {
            case IpcProtocol.TypeStatus:
            {
                var status = JsonSerializer.Deserialize<StatusSnapshotMessage>(line, JsonOpts);
                if (status is not null)
                    StatusReceived?.Invoke(status);
                break;
            }
            case IpcProtocol.TypeNeedAccessTicket:
            {
                var need = JsonSerializer.Deserialize<NeedAccessTicketMessage>(line, JsonOpts);
                if (need is not null)
                    NeedAccessTicket?.Invoke(need);
                break;
            }
            case IpcProtocol.TypeError:
                ErrorReceived?.Invoke(env.Error ?? "Ошибка службы.");
                break;
        }
    }

    private static string StripPipePrefix(string name)
    {
        const string prefix = @"\\.\pipe\";
        return name.StartsWith(prefix, StringComparison.OrdinalIgnoreCase)
            ? name[prefix.Length..]
            : name;
    }

    public async ValueTask DisposeAsync()
    {
        try { _readCts?.Cancel(); } catch { /* ignore */ }

        if (_readLoop is not null)
        {
            try { await Task.WhenAny(_readLoop, Task.Delay(1000)).ConfigureAwait(false); }
            catch { /* ignore */ }
        }

        _writer?.Dispose();
        _reader?.Dispose();
        if (_stream is not null)
            await _stream.DisposeAsync().ConfigureAwait(false);

        _writeLock.Dispose();
        _readCts?.Dispose();
    }
}
