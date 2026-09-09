using System.Collections.Concurrent;
using System.Data;
using Microsoft.Data.SqlClient;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Storage;
using Nyxveil.ControlPlane.Application.Exceptions;
using Nyxveil.ControlPlane.Infrastructure.Persistence;

namespace Nyxveil.ControlPlane.Infrastructure.Services;

/// <summary>Serializes management mutations across processes without holding a lock during node work.</summary>
internal sealed class ManagementOperationLock : IAsyncDisposable
{
    private static readonly ConcurrentDictionary<string, SemaphoreSlim> Gates = new(StringComparer.Ordinal);
    private readonly IDbContextTransaction _transaction;
    private readonly SemaphoreSlim? _gate;

    private ManagementOperationLock(IDbContextTransaction transaction, SemaphoreSlim? gate)
        => (_transaction, _gate) = (transaction, gate);

    internal static async Task<ManagementOperationLock> AcquireAsync(
        ControlPlaneDbContext db, string resource, CancellationToken ct)
    {
        SemaphoreSlim? gate = null;
        if (!db.Database.IsSqlServer())
        {
            gate = Gates.GetOrAdd(resource, _ => new SemaphoreSlim(1, 1));
            if (!await gate.WaitAsync(TimeSpan.FromSeconds(5), ct))
                throw new ConflictException("management operation is busy; retry shortly");
        }
        IDbContextTransaction? transaction = null;
        try
        {
            transaction = await db.Database.BeginTransactionAsync(IsolationLevel.ReadCommitted, ct);
            if (db.Database.IsSqlServer())
            {
                var result = new SqlParameter("@result", SqlDbType.Int) { Direction = ParameterDirection.Output };
                await db.Database.ExecuteSqlRawAsync(
                    "EXEC @result = sys.sp_getapplock @Resource=@resource, @LockMode='Exclusive', " +
                    "@LockOwner='Transaction', @LockTimeout=5000;",
                    new object[] { result, new SqlParameter("@resource", "nyxveil:" + resource) }, ct);
                if (result.Value is not int code || code < 0)
                    throw new ConflictException("management operation is busy; retry shortly");
            }
            return new ManagementOperationLock(transaction, gate);
        }
        catch
        {
            if (transaction is not null) await transaction.DisposeAsync();
            gate?.Release();
            throw;
        }
    }

    internal Task CommitAsync(CancellationToken ct) => _transaction.CommitAsync(ct);
    internal Task RollbackAsync(CancellationToken ct) => _transaction.RollbackAsync(ct);

    public async ValueTask DisposeAsync()
    {
        try { await _transaction.DisposeAsync(); }
        finally { _gate?.Release(); }
    }
}
