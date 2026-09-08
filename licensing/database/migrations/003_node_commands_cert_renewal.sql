/*
  Nyxveil Control Plane schema version 3 (1.2.0).
  Additive: NodeCommands, CertificateRenewalOperations, Nodes management fields.
  Idempotent / recoverable. Schema version written ONLY after validation passes.

  CRITICAL SQL Server rule:
  Newly added columns must NOT be referenced by static T-SQL in the same batch
  (Msg 207 Invalid column name). Dependents use dynamic SQL (EXEC) when needed.
*/
SET NOCOUNT ON;
SET XACT_ABORT ON;

BEGIN TRY
    BEGIN TRANSACTION;

    /* ---- Nodes: additive management columns ---- */
    IF COL_LENGTH(N'dbo.Nodes', N'ManagementCapabilities') IS NULL
        ALTER TABLE dbo.Nodes ADD ManagementCapabilities nvarchar(512) NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'LastBootId') IS NULL
        ALTER TABLE dbo.Nodes ADD LastBootId nvarchar(128) NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'SupportsNodeCommands') IS NULL
        ALTER TABLE dbo.Nodes ADD SupportsNodeCommands bit NOT NULL
            CONSTRAINT DF_Nodes_SupportsNodeCommands DEFAULT (0);

    /* ---- NodeCommands ---- */
    IF OBJECT_ID(N'dbo.NodeCommands', N'U') IS NULL
    BEGIN
        CREATE TABLE dbo.NodeCommands
        (
            Id uniqueidentifier NOT NULL CONSTRAINT PK_NodeCommands PRIMARY KEY,
            NodeId nvarchar(128) NOT NULL,
            Type int NOT NULL,
            Status int NOT NULL,
            CreatedAt datetime2 NOT NULL,
            CreatedBy nvarchar(256) NOT NULL,
            IssuedAt datetime2 NOT NULL,
            ExpiresAt datetime2 NOT NULL,
            ClaimedAt datetime2 NULL,
            StartedAt datetime2 NULL,
            CompletedAt datetime2 NULL,
            ResultCode nvarchar(64) NULL,
            ResultMessage nvarchar(1024) NULL,
            AttemptCount int NOT NULL CONSTRAINT DF_NodeCommands_AttemptCount DEFAULT (0),
            CorrelationId uniqueidentifier NOT NULL,
            PayloadJson nvarchar(max) NULL,
            CONSTRAINT FK_NodeCommands_Nodes_NodeId FOREIGN KEY (NodeId)
                REFERENCES dbo.Nodes(NodeId) ON DELETE CASCADE
        );
    END;

    IF OBJECT_ID(N'dbo.NodeCommands', N'U') IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_NodeCommands_Type' AND parent_object_id = OBJECT_ID(N'dbo.NodeCommands'))
        EXEC(N'ALTER TABLE dbo.NodeCommands WITH CHECK ADD CONSTRAINT CK_NodeCommands_Type CHECK ([Type] BETWEEN 0 AND 2);');

    IF OBJECT_ID(N'dbo.NodeCommands', N'U') IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_NodeCommands_Status' AND parent_object_id = OBJECT_ID(N'dbo.NodeCommands'))
        EXEC(N'ALTER TABLE dbo.NodeCommands WITH CHECK ADD CONSTRAINT CK_NodeCommands_Status CHECK ([Status] BETWEEN 0 AND 10);');

    IF OBJECT_ID(N'dbo.NodeCommands', N'U') IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_NodeCommands_AttemptCount' AND parent_object_id = OBJECT_ID(N'dbo.NodeCommands'))
        EXEC(N'ALTER TABLE dbo.NodeCommands WITH CHECK ADD CONSTRAINT CK_NodeCommands_AttemptCount CHECK ([AttemptCount] >= 0);');

    IF OBJECT_ID(N'dbo.NodeCommands', N'U') IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE object_id = OBJECT_ID(N'dbo.NodeCommands') AND name = N'IX_NodeCommands_NodeId_Status_IssuedAt')
        EXEC(N'CREATE INDEX IX_NodeCommands_NodeId_Status_IssuedAt ON dbo.NodeCommands([NodeId], [Status], [IssuedAt]);');

    IF OBJECT_ID(N'dbo.NodeCommands', N'U') IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE object_id = OBJECT_ID(N'dbo.NodeCommands') AND name = N'IX_NodeCommands_ExpiresAt')
        EXEC(N'CREATE INDEX IX_NodeCommands_ExpiresAt ON dbo.NodeCommands([ExpiresAt]);');

    IF OBJECT_ID(N'dbo.NodeCommands', N'U') IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE object_id = OBJECT_ID(N'dbo.NodeCommands') AND name = N'IX_NodeCommands_CorrelationId')
        EXEC(N'CREATE INDEX IX_NodeCommands_CorrelationId ON dbo.NodeCommands([CorrelationId]);');

    /* ---- CertificateRenewalOperations ---- */
    IF OBJECT_ID(N'dbo.CertificateRenewalOperations', N'U') IS NULL
    BEGIN
        CREATE TABLE dbo.CertificateRenewalOperations
        (
            Id uniqueidentifier NOT NULL CONSTRAINT PK_CertificateRenewalOperations PRIMARY KEY,
            Status int NOT NULL,
            Domain nvarchar(256) NOT NULL,
            CreatedAt datetime2 NOT NULL,
            CreatedBy nvarchar(256) NOT NULL,
            UpdatedAt datetime2 NOT NULL,
            AcmeOrderUrl nvarchar(1024) NULL,
            ChallengeName nvarchar(256) NOT NULL,
            ChallengeValue nvarchar(512) NOT NULL,
            ChallengeExpiresAt datetime2 NULL,
            NewThumbprint nvarchar(128) NULL,
            OldThumbprint nvarchar(128) NULL,
            ErrorMessage nvarchar(1024) NULL,
            CompletedAt datetime2 NULL
        );
    END;

    IF OBJECT_ID(N'dbo.CertificateRenewalOperations', N'U') IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_CertificateRenewalOperations_Status' AND parent_object_id = OBJECT_ID(N'dbo.CertificateRenewalOperations'))
        EXEC(N'ALTER TABLE dbo.CertificateRenewalOperations WITH CHECK ADD CONSTRAINT CK_CertificateRenewalOperations_Status CHECK ([Status] BETWEEN 0 AND 7);');

    IF OBJECT_ID(N'dbo.CertificateRenewalOperations', N'U') IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE object_id = OBJECT_ID(N'dbo.CertificateRenewalOperations') AND name = N'IX_CertificateRenewalOperations_Status_CreatedAt')
        EXEC(N'CREATE INDEX IX_CertificateRenewalOperations_Status_CreatedAt ON dbo.CertificateRenewalOperations([Status], [CreatedAt]);');

    IF OBJECT_ID(N'dbo.NyxveilSchemaVersion', N'U') IS NULL
    BEGIN
        CREATE TABLE dbo.NyxveilSchemaVersion
        (
            Id int NOT NULL CONSTRAINT PK_NyxveilSchemaVersion PRIMARY KEY,
            Version int NOT NULL,
            AppliedAt datetime2 NOT NULL,
            AppliedBy nvarchar(128) NOT NULL
                CONSTRAINT DF_NyxveilSchemaVersion_AppliedBy DEFAULT (N'migration')
        );
    END;

    /* Final structural validation */
    IF COL_LENGTH(N'dbo.Nodes', N'ManagementCapabilities') IS NULL
        THROW 50020, N'Migration validation failed: Nodes.ManagementCapabilities missing.', 1;
    IF COL_LENGTH(N'dbo.Nodes', N'LastBootId') IS NULL
        THROW 50021, N'Migration validation failed: Nodes.LastBootId missing.', 1;
    IF COL_LENGTH(N'dbo.Nodes', N'SupportsNodeCommands') IS NULL
        THROW 50022, N'Migration validation failed: Nodes.SupportsNodeCommands missing.', 1;
    IF OBJECT_ID(N'dbo.NodeCommands', N'U') IS NULL
        THROW 50023, N'Migration validation failed: NodeCommands missing.', 1;
    IF OBJECT_ID(N'dbo.CertificateRenewalOperations', N'U') IS NULL
        THROW 50024, N'Migration validation failed: CertificateRenewalOperations missing.', 1;
    IF OBJECT_ID(N'dbo.NyxveilSchemaVersion', N'U') IS NULL
        THROW 50025, N'Migration validation failed: NyxveilSchemaVersion missing.', 1;

    /* Schema version is LAST — only after full validation. */
    IF EXISTS (SELECT 1 FROM dbo.NyxveilSchemaVersion WHERE Id = 1)
        UPDATE dbo.NyxveilSchemaVersion
        SET Version = 3, AppliedAt = SYSUTCDATETIME(), AppliedBy = N'003_node_commands_cert_renewal'
        WHERE Id = 1;
    ELSE
        INSERT INTO dbo.NyxveilSchemaVersion(Id, Version, AppliedAt, AppliedBy)
        VALUES (1, 3, SYSUTCDATETIME(), N'003_node_commands_cert_renewal');

    IF OBJECT_ID(N'dbo.__EFMigrationsHistory', N'U') IS NOT NULL
        EXEC(N'
IF NOT EXISTS (SELECT 1 FROM dbo.__EFMigrationsHistory WHERE MigrationId = N''20260908110155_NodeCommandsAndCertRenewal'')
    INSERT INTO dbo.__EFMigrationsHistory(MigrationId, ProductVersion)
    VALUES (N''20260908110155_NodeCommandsAndCertRenewal'', N''10.0.11'');
');

    COMMIT TRANSACTION;
END TRY
BEGIN CATCH
    IF XACT_STATE() <> 0
        ROLLBACK TRANSACTION;
    THROW;
END CATCH;
