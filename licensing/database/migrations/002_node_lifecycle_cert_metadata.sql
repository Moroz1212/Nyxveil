/*
  Nyxveil Control Plane schema version 2 (RC 1.1.1).
  Idempotent / recoverable: converges exact-v1, exact-v2, and partial-v2 states.

  CRITICAL SQL Server rule:
  Newly added columns must NOT be referenced by static T-SQL in the same batch
  (Msg 207 Invalid column name). All dependents use dynamic SQL (EXEC).

  Schema version (dbo.NyxveilSchemaVersion) is written ONLY after validation passes.
*/
SET NOCOUNT ON;
SET XACT_ABORT ON;

BEGIN TRY
    BEGIN TRANSACTION;

    /* ---- Nodes: additive columns (guards via COL_LENGTH) ---- */
    IF COL_LENGTH(N'dbo.Nodes', N'LifecycleState') IS NULL
        ALTER TABLE dbo.Nodes ADD LifecycleState int NOT NULL
            CONSTRAINT DF_Nodes_LifecycleState DEFAULT (0);

    IF COL_LENGTH(N'dbo.Nodes', N'DeletedAt') IS NULL
        ALTER TABLE dbo.Nodes ADD DeletedAt datetime2 NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'DeletedBy') IS NULL
        ALTER TABLE dbo.Nodes ADD DeletedBy nvarchar(256) NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'DeletionReason') IS NULL
        ALTER TABLE dbo.Nodes ADD DeletionReason nvarchar(512) NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'TlsMode') IS NULL
        ALTER TABLE dbo.Nodes ADD TlsMode nvarchar(32) NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'CertSubject') IS NULL
        ALTER TABLE dbo.Nodes ADD CertSubject nvarchar(512) NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'CertIssuer') IS NULL
        ALTER TABLE dbo.Nodes ADD CertIssuer nvarchar(512) NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'CertSan') IS NULL
        ALTER TABLE dbo.Nodes ADD CertSan nvarchar(1024) NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'CertNotBefore') IS NULL
        ALTER TABLE dbo.Nodes ADD CertNotBefore datetime2 NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'CertNotAfter') IS NULL
        ALTER TABLE dbo.Nodes ADD CertNotAfter datetime2 NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'CertThumbprint') IS NULL
        ALTER TABLE dbo.Nodes ADD CertThumbprint nvarchar(128) NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'AcmeAutoRenew') IS NULL
        ALTER TABLE dbo.Nodes ADD AcmeAutoRenew bit NOT NULL
            CONSTRAINT DF_Nodes_AcmeAutoRenew DEFAULT (0);
    IF COL_LENGTH(N'dbo.Nodes', N'LastRenewalAttempt') IS NULL
        ALTER TABLE dbo.Nodes ADD LastRenewalAttempt datetime2 NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'LastSuccessfulRenewal') IS NULL
        ALTER TABLE dbo.Nodes ADD LastSuccessfulRenewal datetime2 NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'NextPlannedRenewal') IS NULL
        ALTER TABLE dbo.Nodes ADD NextPlannedRenewal datetime2 NULL;
    IF COL_LENGTH(N'dbo.Nodes', N'LastRenewalError') IS NULL
        ALTER TABLE dbo.Nodes ADD LastRenewalError nvarchar(512) NULL;

    /* ---- NodeHealth readiness columns ---- */
    IF COL_LENGTH(N'dbo.NodeHealth', N'TunReady') IS NULL
        ALTER TABLE dbo.NodeHealth ADD TunReady bit NULL;
    IF COL_LENGTH(N'dbo.NodeHealth', N'TlsOk') IS NULL
        ALTER TABLE dbo.NodeHealth ADD TlsOk bit NULL;
    IF COL_LENGTH(N'dbo.NodeHealth', N'QuicOk') IS NULL
        ALTER TABLE dbo.NodeHealth ADD QuicOk bit NULL;
    IF COL_LENGTH(N'dbo.NodeHealth', N'BridgeOk') IS NULL
        ALTER TABLE dbo.NodeHealth ADD BridgeOk bit NULL;
    IF COL_LENGTH(N'dbo.NodeHealth', N'TicketKeysLoaded') IS NULL
        ALTER TABLE dbo.NodeHealth ADD TicketKeysLoaded bit NULL;
    IF COL_LENGTH(N'dbo.NodeHealth', N'RevocationStale') IS NULL
        ALTER TABLE dbo.NodeHealth ADD RevocationStale bit NULL;
    IF COL_LENGTH(N'dbo.NodeHealth', N'CpConnected') IS NULL
        ALTER TABLE dbo.NodeHealth ADD CpConnected bit NULL;

    /* ---- Fail-closed type checks for partial/incompatible objects ---- */
    IF COL_LENGTH(N'dbo.Nodes', N'LifecycleState') IS NOT NULL
       AND NOT EXISTS (
            SELECT 1
            FROM sys.columns c
            INNER JOIN sys.types t ON c.user_type_id = t.user_type_id
            WHERE c.object_id = OBJECT_ID(N'dbo.Nodes')
              AND c.name = N'LifecycleState'
              AND t.name = N'int'
              AND c.is_nullable = 0)
        THROW 50001, N'Incompatible dbo.Nodes.LifecycleState definition (expected int NOT NULL).', 1;

    IF COL_LENGTH(N'dbo.Nodes', N'AcmeAutoRenew') IS NOT NULL
       AND NOT EXISTS (
            SELECT 1
            FROM sys.columns c
            INNER JOIN sys.types t ON c.user_type_id = t.user_type_id
            WHERE c.object_id = OBJECT_ID(N'dbo.Nodes')
              AND c.name = N'AcmeAutoRenew'
              AND t.name = N'bit'
              AND c.is_nullable = 0)
        THROW 50002, N'Incompatible dbo.Nodes.AcmeAutoRenew definition (expected bit NOT NULL).', 1;

    /*
      Dependents that reference LifecycleState MUST use dynamic SQL so SQL Server
      does not compile them against the pre-ALTER schema (Msg 207).
    */
    IF NOT EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_Nodes_LifecycleState' AND parent_object_id = OBJECT_ID(N'dbo.Nodes'))
        EXEC(N'ALTER TABLE dbo.Nodes WITH CHECK ADD CONSTRAINT CK_Nodes_LifecycleState CHECK ([LifecycleState] BETWEEN 0 AND 2);');

    IF EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_Nodes_LifecycleState' AND parent_object_id = OBJECT_ID(N'dbo.Nodes'))
       AND NOT EXISTS (
            SELECT 1 FROM sys.check_constraints
            WHERE name = N'CK_Nodes_LifecycleState'
              AND parent_object_id = OBJECT_ID(N'dbo.Nodes')
              AND definition LIKE N'%LifecycleState%'
              AND definition LIKE N'%0%'
              AND definition LIKE N'%2%')
        THROW 50003, N'Incompatible CK_Nodes_LifecycleState definition.', 1;

    IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE object_id = OBJECT_ID(N'dbo.Nodes') AND name = N'IX_Nodes_LifecycleState')
        EXEC(N'CREATE INDEX IX_Nodes_LifecycleState ON dbo.Nodes([LifecycleState]);');

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

    /* Final structural validation — all required v2 objects must exist before version bump. */
    IF COL_LENGTH(N'dbo.Nodes', N'LifecycleState') IS NULL
        THROW 50010, N'Migration validation failed: Nodes.LifecycleState missing.', 1;
    IF COL_LENGTH(N'dbo.Nodes', N'DeletedAt') IS NULL
        THROW 50011, N'Migration validation failed: Nodes.DeletedAt missing.', 1;
    IF COL_LENGTH(N'dbo.Nodes', N'CertNotAfter') IS NULL
        THROW 50012, N'Migration validation failed: Nodes.CertNotAfter missing.', 1;
    IF COL_LENGTH(N'dbo.Nodes', N'AcmeAutoRenew') IS NULL
        THROW 50013, N'Migration validation failed: Nodes.AcmeAutoRenew missing.', 1;
    IF COL_LENGTH(N'dbo.NodeHealth', N'TunReady') IS NULL
        THROW 50014, N'Migration validation failed: NodeHealth.TunReady missing.', 1;
    IF COL_LENGTH(N'dbo.NodeHealth', N'CpConnected') IS NULL
        THROW 50015, N'Migration validation failed: NodeHealth.CpConnected missing.', 1;
    IF NOT EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_Nodes_LifecycleState')
        THROW 50016, N'Migration validation failed: CK_Nodes_LifecycleState missing.', 1;
    IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE object_id = OBJECT_ID(N'dbo.Nodes') AND name = N'IX_Nodes_LifecycleState')
        THROW 50017, N'Migration validation failed: IX_Nodes_LifecycleState missing.', 1;
    IF OBJECT_ID(N'dbo.NyxveilSchemaVersion', N'U') IS NULL
        THROW 50018, N'Migration validation failed: NyxveilSchemaVersion missing.', 1;

    /* Schema version is LAST — only after full validation. */
    IF EXISTS (SELECT 1 FROM dbo.NyxveilSchemaVersion WHERE Id = 1)
        UPDATE dbo.NyxveilSchemaVersion
        SET Version = 2, AppliedAt = SYSUTCDATETIME(), AppliedBy = N'002_node_lifecycle_cert_metadata'
        WHERE Id = 1;
    ELSE
        INSERT INTO dbo.NyxveilSchemaVersion(Id, Version, AppliedAt, AppliedBy)
        VALUES (1, 2, SYSUTCDATETIME(), N'002_node_lifecycle_cert_metadata');

    /* Dynamic SQL: static SELECT against optional __EFMigrationsHistory still compiles (Msg 208). */
    IF OBJECT_ID(N'dbo.__EFMigrationsHistory', N'U') IS NOT NULL
        EXEC(N'
IF NOT EXISTS (SELECT 1 FROM dbo.__EFMigrationsHistory WHERE MigrationId = N''20260906161021_NodeLifecycleAndCertMetadata'')
    INSERT INTO dbo.__EFMigrationsHistory(MigrationId, ProductVersion)
    VALUES (N''20260906161021_NodeLifecycleAndCertMetadata'', N''10.0.11'');
');

    COMMIT TRANSACTION;
END TRY
BEGIN CATCH
    IF XACT_STATE() <> 0
        ROLLBACK TRANSACTION;
    THROW;
END CATCH;
