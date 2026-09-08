/*
  Validates Control Plane schema version 3 objects.
  Keeps v2 object checks and adds NodeCommands / CertificateRenewalOperations.
  Returns a single row: schema_ok=1 version=3 when healthy; otherwise throws.
*/
SET NOCOUNT ON;

IF OBJECT_ID(N'dbo.NyxveilSchemaVersion', N'U') IS NULL
    THROW 53001, N'schema_version table missing', 1;

DECLARE @ver int = (SELECT TOP (1) Version FROM dbo.NyxveilSchemaVersion WHERE Id = 1);
IF @ver IS NULL OR @ver <> 3
    THROW 53002, N'schema_version is not 3', 1;

/* ---- v2 baseline (still required) ---- */
IF COL_LENGTH(N'dbo.Nodes', N'LifecycleState') IS NULL
    THROW 53010, N'Nodes.LifecycleState missing', 1;
IF COL_LENGTH(N'dbo.Nodes', N'DeletedAt') IS NULL
    THROW 53011, N'Nodes.DeletedAt missing', 1;
IF COL_LENGTH(N'dbo.Nodes', N'CertNotAfter') IS NULL
    THROW 53012, N'Nodes.CertNotAfter missing', 1;
IF COL_LENGTH(N'dbo.Nodes', N'AcmeAutoRenew') IS NULL
    THROW 53013, N'Nodes.AcmeAutoRenew missing', 1;
IF COL_LENGTH(N'dbo.NodeHealth', N'TunReady') IS NULL
    THROW 53014, N'NodeHealth.TunReady missing', 1;
IF COL_LENGTH(N'dbo.NodeHealth', N'CpConnected') IS NULL
    THROW 53015, N'NodeHealth.CpConnected missing', 1;
IF NOT EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_Nodes_LifecycleState')
    THROW 53016, N'CK_Nodes_LifecycleState missing', 1;
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE object_id = OBJECT_ID(N'dbo.Nodes') AND name = N'IX_Nodes_LifecycleState')
    THROW 53017, N'IX_Nodes_LifecycleState missing', 1;

/* ---- v3 additive ---- */
IF COL_LENGTH(N'dbo.Nodes', N'ManagementCapabilities') IS NULL
    THROW 53020, N'Nodes.ManagementCapabilities missing', 1;
IF COL_LENGTH(N'dbo.Nodes', N'LastBootId') IS NULL
    THROW 53021, N'Nodes.LastBootId missing', 1;
IF COL_LENGTH(N'dbo.Nodes', N'SupportsNodeCommands') IS NULL
    THROW 53022, N'Nodes.SupportsNodeCommands missing', 1;
IF OBJECT_ID(N'dbo.NodeCommands', N'U') IS NULL
    THROW 53023, N'NodeCommands missing', 1;
IF OBJECT_ID(N'dbo.CertificateRenewalOperations', N'U') IS NULL
    THROW 53024, N'CertificateRenewalOperations missing', 1;

SELECT 1 AS schema_ok, @ver AS schema_version;
