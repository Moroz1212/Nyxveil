/*
  Validates Control Plane schema version 2 objects.
  Returns a single row: schema_ok=1 version=2 when healthy; otherwise throws.
*/
SET NOCOUNT ON;

IF OBJECT_ID(N'dbo.NyxveilSchemaVersion', N'U') IS NULL
    THROW 52001, N'schema_version table missing', 1;

DECLARE @ver int = (SELECT TOP (1) Version FROM dbo.NyxveilSchemaVersion WHERE Id = 1);
IF @ver IS NULL OR @ver <> 2
    THROW 52002, N'schema_version is not 2', 1;

IF COL_LENGTH(N'dbo.Nodes', N'LifecycleState') IS NULL
    THROW 52010, N'Nodes.LifecycleState missing', 1;
IF COL_LENGTH(N'dbo.Nodes', N'DeletedAt') IS NULL
    THROW 52011, N'Nodes.DeletedAt missing', 1;
IF COL_LENGTH(N'dbo.Nodes', N'CertNotAfter') IS NULL
    THROW 52012, N'Nodes.CertNotAfter missing', 1;
IF COL_LENGTH(N'dbo.Nodes', N'AcmeAutoRenew') IS NULL
    THROW 52013, N'Nodes.AcmeAutoRenew missing', 1;
IF COL_LENGTH(N'dbo.NodeHealth', N'TunReady') IS NULL
    THROW 52014, N'NodeHealth.TunReady missing', 1;
IF COL_LENGTH(N'dbo.NodeHealth', N'CpConnected') IS NULL
    THROW 52015, N'NodeHealth.CpConnected missing', 1;
IF NOT EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_Nodes_LifecycleState')
    THROW 52016, N'CK_Nodes_LifecycleState missing', 1;
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE object_id = OBJECT_ID(N'dbo.Nodes') AND name = N'IX_Nodes_LifecycleState')
    THROW 52017, N'IX_Nodes_LifecycleState missing', 1;

SELECT 1 AS schema_ok, @ver AS schema_version;
