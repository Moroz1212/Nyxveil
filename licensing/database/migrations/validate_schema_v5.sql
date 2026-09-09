-- Validate Control Plane schema version 5
SET NOCOUNT ON;

DECLARE @v int = (SELECT TOP 1 Version FROM dbo.NyxveilSchemaVersion ORDER BY AppliedAt DESC);
IF @v IS NULL OR @v < 5
    THROW 54001, N'NyxveilSchemaVersion must be >= 5', 1;

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

IF NOT EXISTS (SELECT 1 FROM sys.check_constraints
    WHERE parent_object_id=OBJECT_ID(N'dbo.CertificateRenewalOperations')
    AND name=N'CK_CertificateRenewalOperations_Status' AND is_disabled=0 AND is_not_trusted=0
    AND REPLACE(REPLACE(REPLACE(definition, N' ', N''), N'(', N''), N')', N'') = N'[Status]>=0AND[Status]<=9')
    THROW 55004, N'Certificate renewal state constraint must allow 0 through 9 and be trusted.', 1;
PRINT N'validate_schema_v5: OK';
