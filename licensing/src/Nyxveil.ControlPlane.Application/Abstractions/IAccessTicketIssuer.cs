using Nyxveil.ControlPlane.Application.Contracts.V1;

namespace Nyxveil.ControlPlane.Application.Abstractions;

/// <summary>Issues and verifies NVP/1 EdDSA access tickets.</summary>
public interface IAccessTicketIssuer
{
    Task<string> IssueJwtAsync(IssueTicketCommand command, CancellationToken cancellationToken = default);

    /// <summary>Full cryptographic + claim verification (Frozen Core equivalent of ticket.Verify).</summary>
    AccessTicketClaims VerifyAccessTicket(string token);

    /// <summary>Alias for <see cref="VerifyAccessTicket"/> (legacy name).</summary>
    AccessTicketClaims ParseJwt(string token) => VerifyAccessTicket(token);
}
