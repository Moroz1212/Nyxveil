namespace Nyxveil.ControlPlane.Application.SelfUpdate;

public enum SelfUpdatePhase
{
    Idle = 0,
    CheckingRelease = 1,
    Downloading = 2,
    VerifyingPackage = 3,
    Preflight = 4,
    BackingUpDatabase = 5,
    VerifyingBackup = 6,
    BackingUpInstallation = 7,
    MigrationRehearsal = 8,
    ReadyForHandoff = 9,
    StoppingControlPlane = 10,
    Installing = 11,
    StartingControlPlane = 12,
    HealthVerification = 13,
    Committing = 14,
    Completed = 15,
    RollbackStarting = 20,
    RollbackDatabase = 21,
    RollbackFiles = 22,
    RollbackConfiguration = 23,
    RollbackStartingOldVersion = 24,
    RollbackHealthVerification = 25,
    RolledBackHealthy = 26,
    Failed = 30
}

public enum SelfUpdateStatus
{
    None = 0,
    InProgress = 1,
    Completed = 2,
    Failed = 3,
    RolledBack = 4
}

public static class SelfUpdateResultCodes
{
    public const string UpdatedHealthy = "updated_healthy";
    public const string RolledBackHealthy = "rolled_back_healthy";
    public const string RollbackFailed = "rollback_failed";
    public const string PreflightFailed = "preflight_failed";
    public const string PackageVerificationFailed = "package_verification_failed";
    public const string MigrationRehearsalFailed = "migration_rehearsal_failed";
    public const string HealthFailed = "health_failed";
    public const string AmbiguousState = "ambiguous_state";
    public const string ConcurrentUpdate = "concurrent_update";
}

public sealed class SelfUpdateTransaction
{
    public Guid TransactionId { get; set; }
    public string RequestedBy { get; set; } = "";
    public DateTime CreatedAt { get; set; }
    public DateTime? StartedAt { get; set; }
    public string CurrentVersion { get; set; } = "";
    public string TargetVersion { get; set; } = "";
    public string ReleaseTag { get; set; } = "";
    public string PackageSha256 { get; set; } = "";
    public string? PackageUrl { get; set; }
    public string? ChecksumUrl { get; set; }
    public SelfUpdatePhase Phase { get; set; }
    public SelfUpdateStatus Status { get; set; }
    public string ProgressMessage { get; set; } = "";
    public DateTime LastUpdatedAt { get; set; }
    public string? BackupPath { get; set; }
    public string? StagingPath { get; set; }
    public string? PreviousInstallFingerprint { get; set; }
    public string? TargetInstallFingerprint { get; set; }
    public DateTime? CompletedAt { get; set; }
    public string? ResultCode { get; set; }
    public string? ResultMessage { get; set; }
    public string? PrimaryFailure { get; set; }
    public bool RollbackAttempted { get; set; }
    public bool? RollbackSucceeded { get; set; }
    public string? RollbackFailure { get; set; }
    public List<SelfUpdateTimelineEntry> Timeline { get; set; } = new();
}

public sealed class SelfUpdateTimelineEntry
{
    public DateTime At { get; set; }
    public SelfUpdatePhase Phase { get; set; }
    public string Message { get; set; } = "";
    public bool Ok { get; set; }
}

public sealed class ControlPlaneReleaseInfo
{
    public string? LatestVersion { get; set; }
    public string? ReleaseTag { get; set; }
    public string? ReleaseUrl { get; set; }
    public DateTimeOffset? PublishedAt { get; set; }
    public string? PackageName { get; set; }
    public string? PackageUrl { get; set; }
    public string? ChecksumName { get; set; }
    public string? ChecksumUrl { get; set; }
    public string? ExpectedSha256 { get; set; }
    public string SourceStatus { get; set; } = "never_checked";
    public string? ErrorMessage { get; set; }
    public DateTimeOffset? LastCheckedAt { get; set; }
}

public enum ControlPlaneUpdateAvailability
{
    Current = 0,
    UpdateAvailable = 1,
    InstalledNewer = 2,
    Unknown = 3
}

public sealed class ControlPlaneUpdateStatusDto
{
    public string InstalledVersion { get; set; } = "";
    public string? LatestStableVersion { get; set; }
    public string? ReleaseTag { get; set; }
    public DateTimeOffset? PublishedAt { get; set; }
    public ControlPlaneUpdateAvailability Availability { get; set; }
    public string StatusLabelRu { get; set; } = "";
    public string PackageVerification { get; set; } = "unknown";
    public string? PackageSha256 { get; set; }
    public bool CanUpdate { get; set; }
    public string? BlockReason { get; set; }
    public SelfUpdateTransaction? ActiveTransaction { get; set; }
    public IReadOnlyList<SelfUpdateTransaction> History { get; set; } = Array.Empty<SelfUpdateTransaction>();
}

public sealed class ControlPlaneUpdatePreflightDto
{
    public bool Allowed { get; set; }
    public string InstalledVersion { get; set; } = "";
    public string TargetVersion { get; set; } = "";
    public string ReleaseTag { get; set; } = "";
    public DateTimeOffset? ReleaseDate { get; set; }
    public string? PackageSha256 { get; set; }
    public string SchemaVersion { get; set; } = "5";
    public string ExpectedSchema { get; set; } = "5";
    public string ServiceState { get; set; } = "unknown";
    public string? InstallDirectory { get; set; }
    public bool BackupAvailable { get; set; }
    public long FreeDiskBytes { get; set; }
    public bool HttpsConfigured { get; set; }
    public bool DatabaseConnected { get; set; }
    public string HealthStatus { get; set; } = "unknown";
    public IReadOnlyList<string> BlockingReasons { get; set; } = Array.Empty<string>();
    public IReadOnlyList<string> Warnings { get; set; } = Array.Empty<string>();
}
