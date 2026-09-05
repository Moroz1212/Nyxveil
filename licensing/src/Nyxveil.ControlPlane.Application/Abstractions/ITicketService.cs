using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

public interface ITicketService
{
    Task<TicketIssueResponse> IssueAsync(TicketIssueRequest request, CancellationToken cancellationToken = default);

    Task<TicketIssueResponse> RefreshAsync(TicketRefreshRequest request, CancellationToken cancellationToken = default);
}
