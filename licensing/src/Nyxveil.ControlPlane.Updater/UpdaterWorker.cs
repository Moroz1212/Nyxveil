using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Nyxveil.ControlPlane.Application.SelfUpdate;

namespace Nyxveil.ControlPlane.Updater;

/// <summary>
/// Polls ProgramData self-update request.json and applies the matching handoff with SYSTEM rights.
/// </summary>
public sealed class UpdaterWorker : BackgroundService
{
    private readonly ILogger<UpdaterWorker> _log;

    public UpdaterWorker(ILogger<UpdaterWorker> log) => _log = log;

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        var root = PrivilegedUpdaterContract.SelfUpdateRoot();
        Directory.CreateDirectory(root);
        _log.LogInformation("Nyxveil Control Plane updater service watching {Root}", root);

        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                var requestPath = Path.Combine(root, PrivilegedUpdaterContract.RequestFileName);
                var handoffPath = Path.Combine(root, PrivilegedUpdaterContract.HandoffFileName);
                if (File.Exists(requestPath) && File.Exists(handoffPath))
                {
                    string? requestTx = null;
                    try
                    {
                        using var doc = System.Text.Json.JsonDocument.Parse(File.ReadAllText(requestPath));
                        if (doc.RootElement.TryGetProperty("transactionId", out var tx))
                            requestTx = tx.GetString();
                    }
                    catch (Exception ex)
                    {
                        _log.LogWarning(ex, "Invalid request.json; removing");
                        TryDelete(requestPath);
                        await Task.Delay(TimeSpan.FromSeconds(2), stoppingToken).ConfigureAwait(false);
                        continue;
                    }

                    _log.LogInformation("Applying self-update request tx={Tx}", requestTx);
                    var code = Program.ApplyOnce(handoffPath);
                    _log.LogInformation("Self-update apply finished exit={Code} tx={Tx}", code, requestTx);
                    TryDelete(requestPath);
                }
            }
            catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested)
            {
                break;
            }
            catch (Exception ex)
            {
                _log.LogError(ex, "Updater worker iteration failed");
            }

            try
            {
                await Task.Delay(TimeSpan.FromSeconds(2), stoppingToken).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                break;
            }
        }
    }

    private static void TryDelete(string path)
    {
        try { if (File.Exists(path)) File.Delete(path); } catch { /* ignore */ }
    }
}
