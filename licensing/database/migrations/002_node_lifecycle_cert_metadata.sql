/*
  Nyxveil Control Plane schema version 2 (RC 1.1.0).
  Idempotent: safe to rerun. Back up the database before production use.
*/
SET XACT_ABORT ON;
BEGIN TRANSACTION;

IF COL_LENGTH('dbo.Nodes', 'LifecycleState') IS NULL ALTER TABLE dbo.Nodes ADD LifecycleState int NOT NULL CONSTRAINT DF_Nodes_LifecycleState DEFAULT (0);
IF COL_LENGTH('dbo.Nodes', 'DeletedAt') IS NULL ALTER TABLE dbo.Nodes ADD DeletedAt datetime2 NULL;
IF COL_LENGTH('dbo.Nodes', 'DeletedBy') IS NULL ALTER TABLE dbo.Nodes ADD DeletedBy nvarchar(256) NULL;
IF COL_LENGTH('dbo.Nodes', 'DeletionReason') IS NULL ALTER TABLE dbo.Nodes ADD DeletionReason nvarchar(512) NULL;
IF COL_LENGTH('dbo.Nodes', 'TlsMode') IS NULL ALTER TABLE dbo.Nodes ADD TlsMode nvarchar(32) NULL;
IF COL_LENGTH('dbo.Nodes', 'CertSubject') IS NULL ALTER TABLE dbo.Nodes ADD CertSubject nvarchar(512) NULL;
IF COL_LENGTH('dbo.Nodes', 'CertIssuer') IS NULL ALTER TABLE dbo.Nodes ADD CertIssuer nvarchar(512) NULL;
IF COL_LENGTH('dbo.Nodes', 'CertSan') IS NULL ALTER TABLE dbo.Nodes ADD CertSan nvarchar(1024) NULL;
IF COL_LENGTH('dbo.Nodes', 'CertNotBefore') IS NULL ALTER TABLE dbo.Nodes ADD CertNotBefore datetime2 NULL;
IF COL_LENGTH('dbo.Nodes', 'CertNotAfter') IS NULL ALTER TABLE dbo.Nodes ADD CertNotAfter datetime2 NULL;
IF COL_LENGTH('dbo.Nodes', 'CertThumbprint') IS NULL ALTER TABLE dbo.Nodes ADD CertThumbprint nvarchar(128) NULL;
IF COL_LENGTH('dbo.Nodes', 'AcmeAutoRenew') IS NULL ALTER TABLE dbo.Nodes ADD AcmeAutoRenew bit NOT NULL CONSTRAINT DF_Nodes_AcmeAutoRenew DEFAULT (0);
IF COL_LENGTH('dbo.Nodes', 'LastRenewalAttempt') IS NULL ALTER TABLE dbo.Nodes ADD LastRenewalAttempt datetime2 NULL;
IF COL_LENGTH('dbo.Nodes', 'LastSuccessfulRenewal') IS NULL ALTER TABLE dbo.Nodes ADD LastSuccessfulRenewal datetime2 NULL;
IF COL_LENGTH('dbo.Nodes', 'NextPlannedRenewal') IS NULL ALTER TABLE dbo.Nodes ADD NextPlannedRenewal datetime2 NULL;
IF COL_LENGTH('dbo.Nodes', 'LastRenewalError') IS NULL ALTER TABLE dbo.Nodes ADD LastRenewalError nvarchar(512) NULL;

IF COL_LENGTH('dbo.NodeHealth', 'TunReady') IS NULL ALTER TABLE dbo.NodeHealth ADD TunReady bit NULL;
IF COL_LENGTH('dbo.NodeHealth', 'TlsOk') IS NULL ALTER TABLE dbo.NodeHealth ADD TlsOk bit NULL;
IF COL_LENGTH('dbo.NodeHealth', 'QuicOk') IS NULL ALTER TABLE dbo.NodeHealth ADD QuicOk bit NULL;
IF COL_LENGTH('dbo.NodeHealth', 'BridgeOk') IS NULL ALTER TABLE dbo.NodeHealth ADD BridgeOk bit NULL;
IF COL_LENGTH('dbo.NodeHealth', 'TicketKeysLoaded') IS NULL ALTER TABLE dbo.NodeHealth ADD TicketKeysLoaded bit NULL;
IF COL_LENGTH('dbo.NodeHealth', 'RevocationStale') IS NULL ALTER TABLE dbo.NodeHealth ADD RevocationStale bit NULL;
IF COL_LENGTH('dbo.NodeHealth', 'CpConnected') IS NULL ALTER TABLE dbo.NodeHealth ADD CpConnected bit NULL;

IF NOT EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_Nodes_LifecycleState')
    ALTER TABLE dbo.Nodes WITH CHECK ADD CONSTRAINT CK_Nodes_LifecycleState CHECK (LifecycleState BETWEEN 0 AND 2);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE object_id = OBJECT_ID(N'dbo.Nodes') AND name = N'IX_Nodes_LifecycleState')
    CREATE INDEX IX_Nodes_LifecycleState ON dbo.Nodes(LifecycleState);

IF OBJECT_ID(N'dbo.__EFMigrationsHistory', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM dbo.__EFMigrationsHistory WHERE MigrationId = N'20260906161021_NodeLifecycleAndCertMetadata')
    INSERT INTO dbo.__EFMigrationsHistory(MigrationId, ProductVersion)
    VALUES (N'20260906161021_NodeLifecycleAndCertMetadata', N'10.0.11');

COMMIT TRANSACTION;
