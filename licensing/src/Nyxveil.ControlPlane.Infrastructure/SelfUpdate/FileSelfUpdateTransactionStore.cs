using System.Text.Json;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.SelfUpdate;
using Nyxveil.ControlPlane.Infrastructure.DependencyInjection;

namespace Nyxveil.ControlPlane.Infrastructure.SelfUpdate;

public sealed class FileSelfUpdateTransactionStore : ISelfUpdateTransactionStore
{
    private static readonly JsonSerializerOptions JsonOpts = new()
    {
        WriteIndented = true,
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase
    };

    private readonly string _root;
    private readonly object _gate = new();

    public FileSelfUpdateTransactionStore()
        : this(Path.Combine(ServiceCollectionExtensions.GetProgramDataRoot(), "self-update"))
    {
    }

    public FileSelfUpdateTransactionStore(string root)
    {
        _root = root;
        Directory.CreateDirectory(_root);
        Directory.CreateDirectory(Path.Combine(_root, "history"));
    }

    private string ActivePath => Path.Combine(_root, "transaction.json");

    public SelfUpdateTransaction? GetActive()
    {
        lock (_gate)
        {
            if (!File.Exists(ActivePath)) return null;
            try
            {
                return JsonSerializer.Deserialize<SelfUpdateTransaction>(File.ReadAllText(ActivePath), JsonOpts);
            }
            catch
            {
                return null;
            }
        }
    }

    public IReadOnlyList<SelfUpdateTransaction> ListHistory(int take = 50)
    {
        lock (_gate)
        {
            var dir = Path.Combine(_root, "history");
            if (!Directory.Exists(dir)) return Array.Empty<SelfUpdateTransaction>();
            return Directory.GetFiles(dir, "*.json")
                .Select(f =>
                {
                    try { return JsonSerializer.Deserialize<SelfUpdateTransaction>(File.ReadAllText(f), JsonOpts); }
                    catch { return null; }
                })
                .Where(t => t is not null)
                .Cast<SelfUpdateTransaction>()
                .OrderByDescending(t => t.CreatedAt)
                .Take(take)
                .ToList();
        }
    }

    public void Save(SelfUpdateTransaction tx)
    {
        lock (_gate)
        {
            Directory.CreateDirectory(_root);
            var tmp = ActivePath + ".tmp";
            File.WriteAllText(tmp, JsonSerializer.Serialize(tx, JsonOpts));
            File.Copy(tmp, ActivePath, overwrite: true);
            File.Delete(tmp);
        }
    }

    public void AppendHistory(SelfUpdateTransaction tx)
    {
        lock (_gate)
        {
            var dir = Path.Combine(_root, "history");
            Directory.CreateDirectory(dir);
            var path = Path.Combine(dir, $"{tx.CreatedAt:yyyyMMddHHmmss}-{tx.TransactionId:N}.json");
            File.WriteAllText(path, JsonSerializer.Serialize(tx, JsonOpts));
            if (File.Exists(ActivePath))
                File.Delete(ActivePath);
        }
    }
}
