namespace Nyxveil.ControlPlane.Application.Exceptions;

/// <summary>Base type for Control Plane application-layer failures.</summary>
public abstract class ApplicationException : Exception
{
    protected ApplicationException(string message)
        : base(message)
    {
    }

    protected ApplicationException(string message, Exception? innerException)
        : base(message, innerException)
    {
    }
}

/// <summary>Backward-compatible alias for hosts already referencing the base name.</summary>
public abstract class ApplicationExceptionBase : ApplicationException
{
    protected ApplicationExceptionBase(string message)
        : base(message)
    {
    }

    protected ApplicationExceptionBase(string message, Exception inner)
        : base(message, inner)
    {
    }
}

public sealed class NotFoundException : ApplicationException
{
    public NotFoundException(string message)
        : base(message)
    {
    }

    public NotFoundException(string entityName, object key)
        : base($"{entityName} '{key}' was not found.")
    {
        EntityName = entityName;
        Key = key;
    }

    public string? EntityName { get; }

    public object? Key { get; }
}

public sealed class ConflictException : ApplicationException
{
    public ConflictException(string message)
        : base(message)
    {
    }
}

public sealed class ForbiddenException : ApplicationException
{
    public ForbiddenException(string message)
        : base(message)
    {
    }
}

public sealed class UnauthorizedException : ApplicationException
{
    public UnauthorizedException(string message = "Unauthorized.")
        : base(message)
    {
    }
}

public sealed class ValidationException : ApplicationException
{
    public ValidationException(string message)
        : base(message)
    {
        Errors = Array.Empty<string>();
    }

    public ValidationException(string message, IEnumerable<string> errors)
        : base(message)
    {
        Errors = errors.ToArray();
    }

    public IReadOnlyList<string> Errors { get; }
}
