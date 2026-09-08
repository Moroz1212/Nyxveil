-- Validate Control Plane schema version 4
SET NOCOUNT ON;

DECLARE @v int = (SELECT TOP 1 Version FROM dbo.NyxveilSchemaVersion ORDER BY AppliedAt DESC);
IF @v IS NULL OR @v < 4
    THROW 54001, N'NyxveilSchemaVersion must be >= 4', 1;

IF COL_LENGTH(N'dbo.Nodes', N'ReportedServerVersion') IS NULL
    THROW 54010, N'Nodes.ReportedServerVersion missing', 1;
IF COL_LENGTH(N'dbo.Nodes', N'VersionReportedAt') IS NULL
    THROW 54011, N'Nodes.VersionReportedAt missing', 1;
IF COL_LENGTH(N'dbo.SigningKeysMetadata', N'PromotedAt') IS NULL
    THROW 54020, N'SigningKeysMetadata.PromotedAt missing', 1;
IF COL_LENGTH(N'dbo.SigningKeysMetadata', N'RetireAfter') IS NULL
    THROW 54021, N'SigningKeysMetadata.RetireAfter missing', 1;
IF COL_LENGTH(N'dbo.NodeCommands', N'ProgressPhase') IS NULL
    THROW 54030, N'NodeCommands.ProgressPhase missing', 1;
IF COL_LENGTH(N'dbo.NodeCommands', N'TargetVersion') IS NULL
    THROW 54031, N'NodeCommands.TargetVersion missing', 1;

PRINT N'validate_schema_v4: OK';
