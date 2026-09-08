/*
  Nyxveil Control Plane schema v4 — additive:
  - Node reported runtime version
  - NodeCommand progress / version fields + UpdateNodeLatest type
  - SigningKeys Retiring lifecycle fields + status 3
*/
SET NOCOUNT ON;
SET XACT_ABORT ON;

BEGIN TRY
    BEGIN TRAN;

    IF COL_LENGTH(N'dbo.Nodes', N'ReportedServerVersion') IS NULL
        ALTER TABLE dbo.Nodes ADD ReportedServerVersion nvarchar(64) NULL;

    IF COL_LENGTH(N'dbo.Nodes', N'VersionReportedAt') IS NULL
        ALTER TABLE dbo.Nodes ADD VersionReportedAt datetime2 NULL;

    IF COL_LENGTH(N'dbo.SigningKeysMetadata', N'PromotedAt') IS NULL
        ALTER TABLE dbo.SigningKeysMetadata ADD PromotedAt datetime2 NULL;

    IF COL_LENGTH(N'dbo.SigningKeysMetadata', N'RetireAfter') IS NULL
        ALTER TABLE dbo.SigningKeysMetadata ADD RetireAfter datetime2 NULL;

    IF COL_LENGTH(N'dbo.NodeCommands', N'ProgressPhase') IS NULL
        ALTER TABLE dbo.NodeCommands ADD ProgressPhase nvarchar(64) NULL;

    IF COL_LENGTH(N'dbo.NodeCommands', N'ProgressMessage') IS NULL
        ALTER TABLE dbo.NodeCommands ADD ProgressMessage nvarchar(512) NULL;

    IF COL_LENGTH(N'dbo.NodeCommands', N'ProgressUpdatedAt') IS NULL
        ALTER TABLE dbo.NodeCommands ADD ProgressUpdatedAt datetime2 NULL;

    IF COL_LENGTH(N'dbo.NodeCommands', N'PreviousVersion') IS NULL
        ALTER TABLE dbo.NodeCommands ADD PreviousVersion nvarchar(64) NULL;

    IF COL_LENGTH(N'dbo.NodeCommands', N'TargetVersion') IS NULL
        ALTER TABLE dbo.NodeCommands ADD TargetVersion nvarchar(64) NULL;

    IF EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_SigningKeysMetadata_Status')
        ALTER TABLE dbo.SigningKeysMetadata DROP CONSTRAINT CK_SigningKeysMetadata_Status;
    ALTER TABLE dbo.SigningKeysMetadata WITH CHECK
        ADD CONSTRAINT CK_SigningKeysMetadata_Status CHECK ([Status] BETWEEN 0 AND 3);

    IF EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_NodeCommands_Type')
        ALTER TABLE dbo.NodeCommands DROP CONSTRAINT CK_NodeCommands_Type;
    ALTER TABLE dbo.NodeCommands WITH CHECK
        ADD CONSTRAINT CK_NodeCommands_Type CHECK ([Type] BETWEEN 0 AND 3);

    IF OBJECT_ID(N'dbo.NyxveilSchemaVersion', N'U') IS NULL
    BEGIN
        CREATE TABLE dbo.NyxveilSchemaVersion (
            Version int NOT NULL,
            AppliedAt datetime2 NOT NULL CONSTRAINT DF_NyxveilSchemaVersion_AppliedAt DEFAULT (SYSUTCDATETIME())
        );
        INSERT INTO dbo.NyxveilSchemaVersion (Version) VALUES (4);
    END
    ELSE
    BEGIN
        UPDATE dbo.NyxveilSchemaVersion SET Version = 4, AppliedAt = SYSUTCDATETIME();
        IF @@ROWCOUNT = 0
            INSERT INTO dbo.NyxveilSchemaVersion (Version) VALUES (4);
    END

    IF COL_LENGTH(N'dbo.Nodes', N'ReportedServerVersion') IS NULL
        THROW 50040, N'Migration validation failed: Nodes.ReportedServerVersion missing.', 1;
    IF COL_LENGTH(N'dbo.SigningKeysMetadata', N'RetireAfter') IS NULL
        THROW 50041, N'Migration validation failed: SigningKeysMetadata.RetireAfter missing.', 1;
    IF COL_LENGTH(N'dbo.NodeCommands', N'ProgressPhase') IS NULL
        THROW 50042, N'Migration validation failed: NodeCommands.ProgressPhase missing.', 1;

    COMMIT TRAN;
END TRY
BEGIN CATCH
    IF @@TRANCOUNT > 0 ROLLBACK TRAN;
    THROW;
END CATCH;
