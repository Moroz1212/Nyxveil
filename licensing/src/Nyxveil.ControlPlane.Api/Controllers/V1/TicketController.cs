using Microsoft.AspNetCore.Mvc;
using Nyxveil.ControlPlane.Api.RateLimiting;
using Nyxveil.ControlPlane.Application.Abstractions;
using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Api.Controllers.V1;

[ApiController]
[Route("api/v1")]
[Produces("application/json")]
public sealed class TicketController : ControllerBase
{
    private readonly ITicketService _tickets;

    public TicketController(ITicketService tickets)
    {
        _tickets = tickets;
    }

    /// <summary>POST /api/v1/ticket/issue — license token in body.</summary>
    [HttpPost("ticket/issue")]
    [RateLimit(RateLimitAttribute.TicketPolicy)]
    public async Task<ActionResult<TicketIssueResponse>> Issue(
        [FromBody] TicketIssueRequest request,
        CancellationToken cancellationToken)
    {
        var result = await _tickets.IssueAsync(request, cancellationToken).ConfigureAwait(false);
        return Ok(result);
    }

    /// <summary>POST /api/v1/ticket/refresh — rebuilds from current entitlements.</summary>
    [HttpPost("ticket/refresh")]
    [RateLimit(RateLimitAttribute.TicketPolicy)]
    public async Task<ActionResult<TicketIssueResponse>> Refresh(
        [FromBody] TicketRefreshRequest request,
        CancellationToken cancellationToken)
    {
        var result = await _tickets.RefreshAsync(request, cancellationToken).ConfigureAwait(false);
        return Ok(result);
    }
}
