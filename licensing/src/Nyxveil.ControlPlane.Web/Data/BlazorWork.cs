using Microsoft.Extensions.DependencyInjection;

namespace Nyxveil.ControlPlane.Web.Data;

/// <summary>
/// Creates a short-lived DI scope for Blazor Interactive Server operations.
/// Circuit-scoped DbContext / UserManager must not be shared across concurrent component work.
/// </summary>
public static class BlazorWork
{
    public static async Task RunAsync(IServiceScopeFactory scopes, Func<IServiceProvider, Task> work)
    {
        ArgumentNullException.ThrowIfNull(scopes);
        ArgumentNullException.ThrowIfNull(work);
        await using var scope = scopes.CreateAsyncScope();
        await work(scope.ServiceProvider).ConfigureAwait(false);
    }

    public static async Task<T> RunAsync<T>(IServiceScopeFactory scopes, Func<IServiceProvider, Task<T>> work)
    {
        ArgumentNullException.ThrowIfNull(scopes);
        ArgumentNullException.ThrowIfNull(work);
        await using var scope = scopes.CreateAsyncScope();
        return await work(scope.ServiceProvider).ConfigureAwait(false);
    }
}
