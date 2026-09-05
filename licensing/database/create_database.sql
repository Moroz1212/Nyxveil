/*
================================================================================
  Nyxveil Control Plane — Database bootstrap (idempotent)
================================================================================
  Product:   Nyxveil Licensing / Control Plane
  Version:   1.0.0 (see ../VERSION)
  Target:    Microsoft SQL Server 2019+ / Azure SQL

  TIMESTAMP CONVENTION
  --------------------
  All datetime2(7) columns store Coordinated Universal Time (UTC).
  Application code MUST write DateTime with Kind=Utc (or DateTime.UtcNow /
  DateTimeOffset converted to UTC). Do not store local wall-clock times.

  IDEMPOTENCY
  -----------
  Safe to re-run: creates the database if missing, creates tables only when
  OBJECT_ID IS NULL, and creates indexes only when absent. Does not drop or
  alter existing objects.

  USAGE
  -----
  See README.md in this folder (sqlcmd / SSMS).
================================================================================
*/

SET NOCOUNT ON;
SET XACT_ABORT ON;
GO

/* -------------------------------------------------------------------------- */
/* Configurable database name (sqlcmd)                                        */
/* Override: sqlcmd ... -v DatabaseName=MyDbName                              */
/* Default remains NyxveilControlPlane.                                       */
/* -------------------------------------------------------------------------- */
:setvar DatabaseName NyxveilControlPlane

IF DB_ID(N'$(DatabaseName)') IS NULL
BEGIN
    DECLARE @createSql nvarchar(500) =
        N'CREATE DATABASE ' + QUOTENAME(N'$(DatabaseName)') + N';';
    EXEC sys.sp_executesql @createSql;
    PRINT N'Created database: $(DatabaseName)';
END
ELSE
BEGIN
    PRINT N'Database already exists: $(DatabaseName)';
END
GO

USE [$(DatabaseName)];
GO

/* ========================================================================== */
/* Domain tables                                                              */
/* ========================================================================== */

/* --- Users (VPN end-user accounts; distinct from AspNetUsers) ------------- */
IF OBJECT_ID(N'dbo.Users', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.Users
    (
        UserId        uniqueidentifier NOT NULL
            CONSTRAINT PK_Users PRIMARY KEY
            CONSTRAINT DF_Users_UserId DEFAULT (NEWSEQUENTIALID()),
        ExternalId    nvarchar(256)    NULL,
        Email         nvarchar(320)    NULL,
        DisplayName   nvarchar(256)    NULL,
        Status        nvarchar(64)     NOT NULL
            CONSTRAINT DF_Users_Status DEFAULT (N'Active'),
        CreatedAt     datetime2(7)     NOT NULL
            CONSTRAINT DF_Users_CreatedAt DEFAULT (SYSUTCDATETIME()),
        UpdatedAt     datetime2(7)     NOT NULL
            CONSTRAINT DF_Users_UpdatedAt DEFAULT (SYSUTCDATETIME()),
        CONSTRAINT CK_Users_Status CHECK (Status IN (N'Active', N'Disabled', N'Deleted'))
    );
END
GO

IF OBJECT_ID(N'dbo.Users', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Users_Email' AND object_id = OBJECT_ID(N'dbo.Users'))
    CREATE NONCLUSTERED INDEX IX_Users_Email ON dbo.Users (Email) WHERE Email IS NOT NULL;
GO

IF OBJECT_ID(N'dbo.Users', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Users_ExternalId' AND object_id = OBJECT_ID(N'dbo.Users'))
    CREATE NONCLUSTERED INDEX IX_Users_ExternalId ON dbo.Users (ExternalId) WHERE ExternalId IS NOT NULL;
GO

/* --- Plans ---------------------------------------------------------------- */
IF OBJECT_ID(N'dbo.Plans', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.Plans
    (
        PlanId                   uniqueidentifier NOT NULL
            CONSTRAINT PK_Plans PRIMARY KEY
            CONSTRAINT DF_Plans_PlanId DEFAULT (NEWSEQUENTIALID()),
        Code                     nvarchar(64)     NOT NULL,
        Name                     nvarchar(128)    NOT NULL,
        Status                   nvarchar(64)     NOT NULL
            CONSTRAINT DF_Plans_Status DEFAULT (N'Active'),
        DurationDays             int              NOT NULL
            CONSTRAINT DF_Plans_DurationDays DEFAULT (30),
        MaxDevices               int              NOT NULL
            CONSTRAINT DF_Plans_MaxDevices DEFAULT (1),
        AllowedLocationsPolicy   nvarchar(4000)   NOT NULL
            CONSTRAINT DF_Plans_AllowedLocationsPolicy DEFAULT (N'[]'),
        Permissions              nvarchar(4000)   NOT NULL
            CONSTRAINT DF_Plans_Permissions DEFAULT (N'[]'),
        CreatedAt                datetime2(7)     NOT NULL
            CONSTRAINT DF_Plans_CreatedAt DEFAULT (SYSUTCDATETIME()),
        UpdatedAt                datetime2(7)     NOT NULL
            CONSTRAINT DF_Plans_UpdatedAt DEFAULT (SYSUTCDATETIME()),
        CONSTRAINT UQ_Plans_Code UNIQUE (Code),
        CONSTRAINT CK_Plans_DurationDays CHECK (DurationDays >= 0),
        CONSTRAINT CK_Plans_MaxDevices CHECK (MaxDevices >= 1),
        CONSTRAINT CK_Plans_Status CHECK (Status IN (N'Active', N'Disabled', N'Retired'))
    );
END
GO

/* --- Licenses ------------------------------------------------------------- */
IF OBJECT_ID(N'dbo.Licenses', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.Licenses
    (
        LicenseId           uniqueidentifier NOT NULL
            CONSTRAINT PK_Licenses PRIMARY KEY
            CONSTRAINT DF_Licenses_LicenseId DEFAULT (NEWSEQUENTIALID()),
        UserId              uniqueidentifier NULL,
        LicenseKeyVerifier  nvarchar(200)    NOT NULL,
        Role                nvarchar(64)     NOT NULL
            CONSTRAINT DF_Licenses_Role DEFAULT (N'user'),
        PlanId              uniqueidentifier NOT NULL,
        /* LicenseStatus: 0=Active, 1=Disabled, 2=Revoked, 3=Expired, 4=Pending */
        Status              int              NOT NULL
            CONSTRAINT DF_Licenses_Status DEFAULT (4),
        CreatedAt           datetime2(7)     NOT NULL
            CONSTRAINT DF_Licenses_CreatedAt DEFAULT (SYSUTCDATETIME()),
        ActivatedAt         datetime2(7)     NULL,
        ExpiresAt           datetime2(7)     NULL,
        MaxDevices          int              NOT NULL
            CONSTRAINT DF_Licenses_MaxDevices DEFAULT (1),
        Note                nvarchar(1024)   NULL,
        ExternalPaymentId   nvarchar(256)    NULL,
        CreatedBy           nvarchar(256)    NOT NULL
            CONSTRAINT DF_Licenses_CreatedBy DEFAULT (N'system'),
        UpdatedAt           datetime2(7)     NOT NULL
            CONSTRAINT DF_Licenses_UpdatedAt DEFAULT (SYSUTCDATETIME()),
        CONSTRAINT UQ_Licenses_LicenseKeyVerifier UNIQUE (LicenseKeyVerifier),
        CONSTRAINT CK_Licenses_MaxDevices CHECK (MaxDevices >= 1),
        CONSTRAINT CK_Licenses_Status CHECK (Status BETWEEN 0 AND 4),
        CONSTRAINT FK_Licenses_Users FOREIGN KEY (UserId)
            REFERENCES dbo.Users (UserId),
        CONSTRAINT FK_Licenses_Plans FOREIGN KEY (PlanId)
            REFERENCES dbo.Plans (PlanId)
    );
END
GO

IF OBJECT_ID(N'dbo.Licenses', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Licenses_LicenseKeyVerifier' AND object_id = OBJECT_ID(N'dbo.Licenses'))
    CREATE NONCLUSTERED INDEX IX_Licenses_LicenseKeyVerifier
        ON dbo.Licenses (LicenseKeyVerifier);
GO

IF OBJECT_ID(N'dbo.Licenses', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Licenses_Status_ExpiresAt' AND object_id = OBJECT_ID(N'dbo.Licenses'))
    CREATE NONCLUSTERED INDEX IX_Licenses_Status_ExpiresAt
        ON dbo.Licenses (Status, ExpiresAt);
GO

IF OBJECT_ID(N'dbo.Licenses', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Licenses_UserId' AND object_id = OBJECT_ID(N'dbo.Licenses'))
    CREATE NONCLUSTERED INDEX IX_Licenses_UserId ON dbo.Licenses (UserId) WHERE UserId IS NOT NULL;
GO

IF OBJECT_ID(N'dbo.Licenses', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Licenses_PlanId' AND object_id = OBJECT_ID(N'dbo.Licenses'))
    CREATE NONCLUSTERED INDEX IX_Licenses_PlanId ON dbo.Licenses (PlanId);
GO

/* --- Locations (before LicenseAllowedLocations for optional FK) ----------- */
IF OBJECT_ID(N'dbo.Locations', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.Locations
    (
        LocationId    nvarchar(64)  NOT NULL
            CONSTRAINT PK_Locations PRIMARY KEY,
        Code          nvarchar(64)  NOT NULL,
        Country       nvarchar(128) NOT NULL
            CONSTRAINT DF_Locations_Country DEFAULT (N''),
        City          nvarchar(128) NOT NULL
            CONSTRAINT DF_Locations_City DEFAULT (N''),
        DisplayName   nvarchar(256) NOT NULL,
        Enabled       bit           NOT NULL
            CONSTRAINT DF_Locations_Enabled DEFAULT (1),
        SortOrder     int           NOT NULL
            CONSTRAINT DF_Locations_SortOrder DEFAULT (0),
        CreatedAt     datetime2(7)  NOT NULL
            CONSTRAINT DF_Locations_CreatedAt DEFAULT (SYSUTCDATETIME()),
        UpdatedAt     datetime2(7)  NOT NULL
            CONSTRAINT DF_Locations_UpdatedAt DEFAULT (SYSUTCDATETIME()),
        CountryCode   nvarchar(8)   NULL,
        CONSTRAINT UQ_Locations_Code UNIQUE (Code)
    );
END
GO

IF OBJECT_ID(N'dbo.Locations', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Locations_Enabled_SortOrder' AND object_id = OBJECT_ID(N'dbo.Locations'))
    CREATE NONCLUSTERED INDEX IX_Locations_Enabled_SortOrder
        ON dbo.Locations (Enabled, SortOrder);
GO

/* --- LicenseAllowedLocations ---------------------------------------------- */
IF OBJECT_ID(N'dbo.LicenseAllowedLocations', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.LicenseAllowedLocations
    (
        LicenseId     uniqueidentifier NOT NULL,
        LocationId    nvarchar(64)     NOT NULL,
        CONSTRAINT PK_LicenseAllowedLocations PRIMARY KEY (LicenseId, LocationId),
        CONSTRAINT FK_LicenseAllowedLocations_Licenses FOREIGN KEY (LicenseId)
            REFERENCES dbo.Licenses (LicenseId) ON DELETE CASCADE,
        CONSTRAINT FK_LicenseAllowedLocations_Locations FOREIGN KEY (LocationId)
            REFERENCES dbo.Locations (LocationId)
    );
END
GO

IF OBJECT_ID(N'dbo.LicenseAllowedLocations', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_LicenseAllowedLocations_LocationId' AND object_id = OBJECT_ID(N'dbo.LicenseAllowedLocations'))
    CREATE NONCLUSTERED INDEX IX_LicenseAllowedLocations_LocationId
        ON dbo.LicenseAllowedLocations (LocationId);
GO

/* --- Devices -------------------------------------------------------------- */
IF OBJECT_ID(N'dbo.Devices', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.Devices
    (
        Id              uniqueidentifier NOT NULL
            CONSTRAINT PK_Devices PRIMARY KEY
            CONSTRAINT DF_Devices_Id DEFAULT (NEWSEQUENTIALID()),
        ClientDeviceId  nvarchar(128)    NOT NULL,
        LicenseId       uniqueidentifier NOT NULL,
        /* Ed25519 public key is 32 bytes; column sized to 64 for future algos */
        PublicKey       varbinary(64)    NOT NULL,
        Platform        nvarchar(64)     NULL,
        DeviceName      nvarchar(256)    NULL,
        /* DeviceStatus: 0=Active, 1=Disabled, 2=Revoked */
        Status          int              NOT NULL
            CONSTRAINT DF_Devices_Status DEFAULT (0),
        CreatedAt       datetime2(7)     NOT NULL
            CONSTRAINT DF_Devices_CreatedAt DEFAULT (SYSUTCDATETIME()),
        LastSeenAt      datetime2(7)     NULL,
        RevokedAt       datetime2(7)     NULL,
        CONSTRAINT UQ_Devices_LicenseId_ClientDeviceId UNIQUE (LicenseId, ClientDeviceId),
        CONSTRAINT CK_Devices_Status CHECK (Status BETWEEN 0 AND 2),
        CONSTRAINT CK_Devices_PublicKeyLen CHECK (DATALENGTH(PublicKey) >= 32 AND DATALENGTH(PublicKey) <= 64),
        CONSTRAINT FK_Devices_Licenses FOREIGN KEY (LicenseId)
            REFERENCES dbo.Licenses (LicenseId) ON DELETE CASCADE
    );
END
GO

IF OBJECT_ID(N'dbo.Devices', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Devices_LicenseId' AND object_id = OBJECT_ID(N'dbo.Devices'))
    CREATE NONCLUSTERED INDEX IX_Devices_LicenseId ON dbo.Devices (LicenseId);
GO

IF OBJECT_ID(N'dbo.Devices', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Devices_Status' AND object_id = OBJECT_ID(N'dbo.Devices'))
    CREATE NONCLUSTERED INDEX IX_Devices_Status ON dbo.Devices (Status);
GO

/* --- Nodes ---------------------------------------------------------------- */
IF OBJECT_ID(N'dbo.Nodes', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.Nodes
    (
        NodeId            nvarchar(128)    NOT NULL
            CONSTRAINT PK_Nodes PRIMARY KEY,
        LocationId        nvarchar(64)     NOT NULL,
        DisplayName       nvarchar(256)    NOT NULL,
        /* NodeRuntimeStatus: 0=Healthy, 1=Degraded, 2=Offline, 3=Maintenance */
        Status            int              NOT NULL
            CONSTRAINT DF_Nodes_Status DEFAULT (2),
        Enabled           bit              NOT NULL
            CONSTRAINT DF_Nodes_Enabled DEFAULT (1),
        TestOnly          bit              NOT NULL
            CONSTRAINT DF_Nodes_TestOnly DEFAULT (0),
        Draining          bit              NOT NULL
            CONSTRAINT DF_Nodes_Draining DEFAULT (0),
        ProtocolVersion   int              NOT NULL
            CONSTRAINT DF_Nodes_ProtocolVersion DEFAULT (1),
        ServerVersion     nvarchar(64)     NULL,
        ServerName        nvarchar(256)    NULL,
        SpkiPin           varbinary(32)    NULL,
        PublicIdentity    varbinary(32)    NOT NULL,
        Capacity          int              NOT NULL
            CONSTRAINT DF_Nodes_Capacity DEFAULT (100),
        CurrentSessions   int              NOT NULL
            CONSTRAINT DF_Nodes_CurrentSessions DEFAULT (0),
        HealthStatus      nvarchar(64)     NULL,
        LastSeenAt        datetime2(7)     NULL,
        CreatedAt         datetime2(7)     NOT NULL
            CONSTRAINT DF_Nodes_CreatedAt DEFAULT (SYSUTCDATETIME()),
        UpdatedAt         datetime2(7)     NOT NULL
            CONSTRAINT DF_Nodes_UpdatedAt DEFAULT (SYSUTCDATETIME()),
        ConfigVersion     bigint           NOT NULL
            CONSTRAINT DF_Nodes_ConfigVersion DEFAULT (0),
        CONSTRAINT CK_Nodes_Status CHECK (Status BETWEEN 0 AND 3),
        CONSTRAINT CK_Nodes_ProtocolVersion CHECK (ProtocolVersion >= 0 AND ProtocolVersion <= 65535),
        CONSTRAINT CK_Nodes_Capacity CHECK (Capacity >= 0),
        CONSTRAINT CK_Nodes_CurrentSessions CHECK (CurrentSessions >= 0),
        CONSTRAINT CK_Nodes_PublicIdentityLen CHECK (DATALENGTH(PublicIdentity) = 32),
        CONSTRAINT FK_Nodes_Locations FOREIGN KEY (LocationId)
            REFERENCES dbo.Locations (LocationId)
    );
END
GO

IF OBJECT_ID(N'dbo.Nodes', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Nodes_LocationId' AND object_id = OBJECT_ID(N'dbo.Nodes'))
    CREATE NONCLUSTERED INDEX IX_Nodes_LocationId ON dbo.Nodes (LocationId);
GO

IF OBJECT_ID(N'dbo.Nodes', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Nodes_Status' AND object_id = OBJECT_ID(N'dbo.Nodes'))
    CREATE NONCLUSTERED INDEX IX_Nodes_Status ON dbo.Nodes (Status);
GO

IF OBJECT_ID(N'dbo.Nodes', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Nodes_Enabled_TestOnly' AND object_id = OBJECT_ID(N'dbo.Nodes'))
    CREATE NONCLUSTERED INDEX IX_Nodes_Enabled_TestOnly ON dbo.Nodes (Enabled, TestOnly);
GO

/* --- NodeEndpoints -------------------------------------------------------- */
IF OBJECT_ID(N'dbo.NodeEndpoints', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.NodeEndpoints
    (
        Id             uniqueidentifier NOT NULL
            CONSTRAINT PK_NodeEndpoints PRIMARY KEY
            CONSTRAINT DF_NodeEndpoints_Id DEFAULT (NEWSEQUENTIALID()),
        NodeId         nvarchar(128)    NOT NULL,
        Host           nvarchar(256)    NOT NULL,
        Port           int              NOT NULL,
        AddressFamily  nvarchar(16)     NOT NULL
            CONSTRAINT DF_NodeEndpoints_AddressFamily DEFAULT (N'hostname'),
        Priority       int              NOT NULL
            CONSTRAINT DF_NodeEndpoints_Priority DEFAULT (0),
        Enabled        bit              NOT NULL
            CONSTRAINT DF_NodeEndpoints_Enabled DEFAULT (1),
        CONSTRAINT CK_NodeEndpoints_Port CHECK (Port >= 1 AND Port <= 65535),
        CONSTRAINT CK_NodeEndpoints_AddressFamily CHECK (AddressFamily IN (N'ipv4', N'ipv6', N'hostname')),
        CONSTRAINT FK_NodeEndpoints_Nodes FOREIGN KEY (NodeId)
            REFERENCES dbo.Nodes (NodeId) ON DELETE CASCADE
    );
END
GO

IF OBJECT_ID(N'dbo.NodeEndpoints', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_NodeEndpoints_NodeId' AND object_id = OBJECT_ID(N'dbo.NodeEndpoints'))
    CREATE NONCLUSTERED INDEX IX_NodeEndpoints_NodeId ON dbo.NodeEndpoints (NodeId);
GO

/* --- NodeTransports ------------------------------------------------------- */
IF OBJECT_ID(N'dbo.NodeTransports', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.NodeTransports
    (
        Id              uniqueidentifier NOT NULL
            CONSTRAINT PK_NodeTransports PRIMARY KEY
            CONSTRAINT DF_NodeTransports_Id DEFAULT (NEWSEQUENTIALID()),
        NodeId          nvarchar(128)    NOT NULL,
        TransportType   nvarchar(16)     NOT NULL
            CONSTRAINT DF_NodeTransports_TransportType DEFAULT (N'tls'),
        Enabled         bit              NOT NULL
            CONSTRAINT DF_NodeTransports_Enabled DEFAULT (1),
        Priority        int              NOT NULL
            CONSTRAINT DF_NodeTransports_Priority DEFAULT (0),
        CONSTRAINT CK_NodeTransports_TransportType CHECK (TransportType IN (N'tls', N'quic')),
        CONSTRAINT FK_NodeTransports_Nodes FOREIGN KEY (NodeId)
            REFERENCES dbo.Nodes (NodeId) ON DELETE CASCADE
    );
END
GO

IF OBJECT_ID(N'dbo.NodeTransports', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_NodeTransports_NodeId' AND object_id = OBJECT_ID(N'dbo.NodeTransports'))
    CREATE NONCLUSTERED INDEX IX_NodeTransports_NodeId ON dbo.NodeTransports (NodeId);
GO

/* --- NodeHealth (latest snapshot per node) -------------------------------- */
IF OBJECT_ID(N'dbo.NodeHealth', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.NodeHealth
    (
        NodeId           nvarchar(128) NOT NULL
            CONSTRAINT PK_NodeHealth PRIMARY KEY,
        CpuPercent       float         NOT NULL
            CONSTRAINT DF_NodeHealth_CpuPercent DEFAULT (0),
        MemoryPercent    float         NOT NULL
            CONSTRAINT DF_NodeHealth_MemoryPercent DEFAULT (0),
        MemoryBytes      bigint        NULL,
        UptimeSeconds    bigint        NULL,
        ActiveSessions   int           NOT NULL
            CONSTRAINT DF_NodeHealth_ActiveSessions DEFAULT (0),
        NetworkRxRate    float         NULL,
        NetworkTxRate    float         NULL,
        LoadAverage      float         NULL,
        Healthy          bit           NOT NULL
            CONSTRAINT DF_NodeHealth_Healthy DEFAULT (0),
        UpdatedAt        datetime2(7)  NOT NULL
            CONSTRAINT DF_NodeHealth_UpdatedAt DEFAULT (SYSUTCDATETIME()),
        CONSTRAINT CK_NodeHealth_CpuPercent CHECK (CpuPercent >= 0 AND CpuPercent <= 100),
        CONSTRAINT CK_NodeHealth_MemoryPercent CHECK (MemoryPercent >= 0 AND MemoryPercent <= 100),
        CONSTRAINT CK_NodeHealth_ActiveSessions CHECK (ActiveSessions >= 0),
        CONSTRAINT FK_NodeHealth_Nodes FOREIGN KEY (NodeId)
            REFERENCES dbo.Nodes (NodeId) ON DELETE CASCADE
    );
END
GO

IF OBJECT_ID(N'dbo.NodeHealth', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_NodeHealth_UpdatedAt' AND object_id = OBJECT_ID(N'dbo.NodeHealth'))
    CREATE NONCLUSTERED INDEX IX_NodeHealth_UpdatedAt ON dbo.NodeHealth (UpdatedAt);
GO

IF OBJECT_ID(N'dbo.NodeHealth', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_NodeHealth_Healthy' AND object_id = OBJECT_ID(N'dbo.NodeHealth'))
    CREATE NONCLUSTERED INDEX IX_NodeHealth_Healthy ON dbo.NodeHealth (Healthy);
GO

/* --- NodeMetrics (time-series samples) ------------------------------------ */
IF OBJECT_ID(N'dbo.NodeMetrics', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.NodeMetrics
    (
        Id               uniqueidentifier NOT NULL
            CONSTRAINT PK_NodeMetrics PRIMARY KEY
            CONSTRAINT DF_NodeMetrics_Id DEFAULT (NEWSEQUENTIALID()),
        NodeId           nvarchar(128)    NOT NULL,
        [Timestamp]      datetime2(7)     NOT NULL
            CONSTRAINT DF_NodeMetrics_Timestamp DEFAULT (SYSUTCDATETIME()),
        CpuPercent       float            NOT NULL
            CONSTRAINT DF_NodeMetrics_CpuPercent DEFAULT (0),
        MemoryPercent    float            NOT NULL
            CONSTRAINT DF_NodeMetrics_MemoryPercent DEFAULT (0),
        MemoryBytes      bigint           NULL,
        ActiveSessions   int              NOT NULL
            CONSTRAINT DF_NodeMetrics_ActiveSessions DEFAULT (0),
        NetworkRxRate    float            NULL,
        NetworkTxRate    float            NULL,
        UptimeSeconds    bigint           NULL,
        CONSTRAINT CK_NodeMetrics_ActiveSessions CHECK (ActiveSessions >= 0),
        CONSTRAINT FK_NodeMetrics_Nodes FOREIGN KEY (NodeId)
            REFERENCES dbo.Nodes (NodeId) ON DELETE CASCADE
    );
END
GO

IF OBJECT_ID(N'dbo.NodeMetrics', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_NodeMetrics_NodeId_Timestamp' AND object_id = OBJECT_ID(N'dbo.NodeMetrics'))
    CREATE NONCLUSTERED INDEX IX_NodeMetrics_NodeId_Timestamp
        ON dbo.NodeMetrics (NodeId, [Timestamp] DESC);
GO

/* --- NodeCredentials ------------------------------------------------------ */
IF OBJECT_ID(N'dbo.NodeCredentials', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.NodeCredentials
    (
        NodeId                   nvarchar(128)  NOT NULL
            CONSTRAINT PK_NodeCredentials PRIMARY KEY,
        PublicKey                varbinary(32)  NOT NULL,
        CredentialIssuedAt       datetime2(7)   NOT NULL
            CONSTRAINT DF_NodeCredentials_CredentialIssuedAt DEFAULT (SYSUTCDATETIME()),
        NodeAuthSecretVerifier   nvarchar(200)  NULL,
        LastAuthAt               datetime2(7)   NULL,
        LastCoreTokenUnix        bigint         NULL,
        CONSTRAINT CK_NodeCredentials_PublicKeyLen CHECK (DATALENGTH(PublicKey) = 32),
        CONSTRAINT FK_NodeCredentials_Nodes FOREIGN KEY (NodeId)
            REFERENCES dbo.Nodes (NodeId) ON DELETE CASCADE
    );
END
GO

IF OBJECT_ID(N'dbo.NodeCredentials', N'U') IS NOT NULL
   AND COL_LENGTH(N'dbo.NodeCredentials', N'LastCoreTokenUnix') IS NULL
BEGIN
    ALTER TABLE dbo.NodeCredentials ADD LastCoreTokenUnix bigint NULL;
END
GO

/* --- NodeConfigs ---------------------------------------------------------- */
IF OBJECT_ID(N'dbo.NodeConfigs', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.NodeConfigs
    (
        NodeId                    nvarchar(128)   NOT NULL
            CONSTRAINT PK_NodeConfigs PRIMARY KEY,
        Enabled                   bit             NOT NULL
            CONSTRAINT DF_NodeConfigs_Enabled DEFAULT (1),
        Draining                  bit             NOT NULL
            CONSTRAINT DF_NodeConfigs_Draining DEFAULT (0),
        MaintenanceMode           bit             NOT NULL
            CONSTRAINT DF_NodeConfigs_MaintenanceMode DEFAULT (0),
        TransportPolicyJson       nvarchar(max)   NOT NULL
            CONSTRAINT DF_NodeConfigs_TransportPolicyJson DEFAULT (N'{}'),
        EchPolicyJson             nvarchar(max)   NULL,
        Mtu                       int             NULL,
        Capacity                  int             NOT NULL
            CONSTRAINT DF_NodeConfigs_Capacity DEFAULT (100),
        MinimumServerVersion      nvarchar(64)    NULL,
        MinimumProtocolVersion    int             NULL,
        ConfigVersion             bigint          NOT NULL
            CONSTRAINT DF_NodeConfigs_ConfigVersion DEFAULT (1),
        UpdatedAt                 datetime2(7)    NOT NULL
            CONSTRAINT DF_NodeConfigs_UpdatedAt DEFAULT (SYSUTCDATETIME()),
        CONSTRAINT CK_NodeConfigs_Capacity CHECK (Capacity >= 0),
        CONSTRAINT CK_NodeConfigs_ConfigVersion CHECK (ConfigVersion >= 1),
        CONSTRAINT CK_NodeConfigs_Mtu CHECK (Mtu IS NULL OR (Mtu >= 576 AND Mtu <= 9000)),
        CONSTRAINT CK_NodeConfigs_MinimumProtocolVersion
            CHECK (MinimumProtocolVersion IS NULL OR (MinimumProtocolVersion >= 0 AND MinimumProtocolVersion <= 65535)),
        CONSTRAINT FK_NodeConfigs_Nodes FOREIGN KEY (NodeId)
            REFERENCES dbo.Nodes (NodeId) ON DELETE CASCADE
    );
END
GO

/* --- BootstrapTokens ------------------------------------------------------ */
IF OBJECT_ID(N'dbo.BootstrapTokens', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.BootstrapTokens
    (
        BootstrapId       uniqueidentifier NOT NULL
            CONSTRAINT PK_BootstrapTokens PRIMARY KEY
            CONSTRAINT DF_BootstrapTokens_BootstrapId DEFAULT (NEWSEQUENTIALID()),
        Verifier          nvarchar(200)    NOT NULL,
        ExpiresAt         datetime2(7)     NOT NULL,
        MaxUses           int              NOT NULL
            CONSTRAINT DF_BootstrapTokens_MaxUses DEFAULT (1),
        UsedCount         int              NOT NULL
            CONSTRAINT DF_BootstrapTokens_UsedCount DEFAULT (0),
        AllowedLocation   nvarchar(64)     NULL,
        /* BootstrapTokenStatus: 0=Active, 1=Exhausted, 2=Expired, 3=Revoked */
        Status            int              NOT NULL
            CONSTRAINT DF_BootstrapTokens_Status DEFAULT (0),
        CreatedAt         datetime2(7)     NOT NULL
            CONSTRAINT DF_BootstrapTokens_CreatedAt DEFAULT (SYSUTCDATETIME()),
        CreatedBy         nvarchar(256)    NOT NULL
            CONSTRAINT DF_BootstrapTokens_CreatedBy DEFAULT (N'system'),
        Note              nvarchar(1024)   NULL,
        CONSTRAINT UQ_BootstrapTokens_Verifier UNIQUE (Verifier),
        CONSTRAINT CK_BootstrapTokens_MaxUses CHECK (MaxUses >= 1),
        CONSTRAINT CK_BootstrapTokens_UsedCount CHECK (UsedCount >= 0 AND UsedCount <= MaxUses),
        CONSTRAINT CK_BootstrapTokens_Status CHECK (Status BETWEEN 0 AND 3)
    );
END
GO

IF OBJECT_ID(N'dbo.BootstrapTokens', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_BootstrapTokens_Verifier' AND object_id = OBJECT_ID(N'dbo.BootstrapTokens'))
    CREATE NONCLUSTERED INDEX IX_BootstrapTokens_Verifier ON dbo.BootstrapTokens (Verifier);
GO

IF OBJECT_ID(N'dbo.BootstrapTokens', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_BootstrapTokens_Status_ExpiresAt' AND object_id = OBJECT_ID(N'dbo.BootstrapTokens'))
    CREATE NONCLUSTERED INDEX IX_BootstrapTokens_Status_ExpiresAt
        ON dbo.BootstrapTokens (Status, ExpiresAt);
GO

/* --- TicketAudits --------------------------------------------------------- */
IF OBJECT_ID(N'dbo.TicketAudits', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.TicketAudits
    (
        Id              uniqueidentifier NOT NULL
            CONSTRAINT PK_TicketAudits PRIMARY KEY
            CONSTRAINT DF_TicketAudits_Id DEFAULT (NEWSEQUENTIALID()),
        TicketId        nvarchar(128)    NOT NULL,
        LicenseId       uniqueidentifier NOT NULL,
        DeviceId        nvarchar(128)    NOT NULL,
        IssuedAt        datetime2(7)     NOT NULL
            CONSTRAINT DF_TicketAudits_IssuedAt DEFAULT (SYSUTCDATETIME()),
        ExpiresAt       datetime2(7)     NOT NULL,
        LocationsJson   nvarchar(max)    NOT NULL
            CONSTRAINT DF_TicketAudits_LocationsJson DEFAULT (N'[]'),
        NodeScopeJson   nvarchar(max)    NOT NULL
            CONSTRAINT DF_TicketAudits_NodeScopeJson DEFAULT (N'[]'),
        Action          nvarchar(32)     NOT NULL,
        CONSTRAINT CK_TicketAudits_Action CHECK (Action IN (N'issue', N'refresh')),
        CONSTRAINT FK_TicketAudits_Licenses FOREIGN KEY (LicenseId)
            REFERENCES dbo.Licenses (LicenseId)
    );
END
GO

IF OBJECT_ID(N'dbo.TicketAudits', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_TicketAudits_TicketId' AND object_id = OBJECT_ID(N'dbo.TicketAudits'))
    CREATE NONCLUSTERED INDEX IX_TicketAudits_TicketId ON dbo.TicketAudits (TicketId);
GO

IF OBJECT_ID(N'dbo.TicketAudits', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_TicketAudits_LicenseId_IssuedAt' AND object_id = OBJECT_ID(N'dbo.TicketAudits'))
    CREATE NONCLUSTERED INDEX IX_TicketAudits_LicenseId_IssuedAt
        ON dbo.TicketAudits (LicenseId, IssuedAt DESC);
GO

/* --- Revocations ---------------------------------------------------------- */
IF OBJECT_ID(N'dbo.Revocations', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.Revocations
    (
        Id          uniqueidentifier NOT NULL
            CONSTRAINT PK_Revocations PRIMARY KEY
            CONSTRAINT DF_Revocations_Id DEFAULT (NEWSEQUENTIALID()),
        /* RevocationType: 0=Ticket, 1=License, 2=Device */
        [Type]      int              NOT NULL,
        TargetId    nvarchar(256)    NOT NULL,
        Reason      nvarchar(1024)   NULL,
        CreatedAt   datetime2(7)     NOT NULL
            CONSTRAINT DF_Revocations_CreatedAt DEFAULT (SYSUTCDATETIME()),
        CreatedBy   nvarchar(256)    NOT NULL
            CONSTRAINT DF_Revocations_CreatedBy DEFAULT (N'system'),
        Version     bigint           NOT NULL
            CONSTRAINT DF_Revocations_Version DEFAULT (1),
        CONSTRAINT CK_Revocations_Type CHECK ([Type] BETWEEN 0 AND 2),
        CONSTRAINT CK_Revocations_Version CHECK (Version >= 1)
    );
END
GO

IF OBJECT_ID(N'dbo.Revocations', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Revocations_TargetId' AND object_id = OBJECT_ID(N'dbo.Revocations'))
    CREATE NONCLUSTERED INDEX IX_Revocations_TargetId ON dbo.Revocations (TargetId);
GO

IF OBJECT_ID(N'dbo.Revocations', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Revocations_Version' AND object_id = OBJECT_ID(N'dbo.Revocations'))
    CREATE NONCLUSTERED INDEX IX_Revocations_Version ON dbo.Revocations (Version DESC);
GO

IF OBJECT_ID(N'dbo.Revocations', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Revocations_Type_TargetId' AND object_id = OBJECT_ID(N'dbo.Revocations'))
    CREATE NONCLUSTERED INDEX IX_Revocations_Type_TargetId ON dbo.Revocations ([Type], TargetId);
GO

/* --- CatalogVersions ------------------------------------------------------ */
IF OBJECT_ID(N'dbo.CatalogVersions', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.CatalogVersions
    (
        Id           uniqueidentifier NOT NULL
            CONSTRAINT PK_CatalogVersions PRIMARY KEY
            CONSTRAINT DF_CatalogVersions_Id DEFAULT (NEWSEQUENTIALID()),
        Version      nvarchar(64)     NOT NULL,
        IssuedAt     datetime2(7)     NOT NULL
            CONSTRAINT DF_CatalogVersions_IssuedAt DEFAULT (SYSUTCDATETIME()),
        ExpiresAt    datetime2(7)     NOT NULL,
        KeyId        nvarchar(128)    NOT NULL,
        PayloadHash  nvarchar(128)    NULL,
        CreatedAt    datetime2(7)     NOT NULL
            CONSTRAINT DF_CatalogVersions_CreatedAt DEFAULT (SYSUTCDATETIME()),
        CONSTRAINT UQ_CatalogVersions_Version UNIQUE (Version)
    );
END
GO

IF OBJECT_ID(N'dbo.CatalogVersions', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_CatalogVersions_IssuedAt' AND object_id = OBJECT_ID(N'dbo.CatalogVersions'))
    CREATE NONCLUSTERED INDEX IX_CatalogVersions_IssuedAt
        ON dbo.CatalogVersions (IssuedAt DESC);
GO

/* --- SigningKeysMetadata -------------------------------------------------- */
IF OBJECT_ID(N'dbo.SigningKeysMetadata', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.SigningKeysMetadata
    (
        Id                   uniqueidentifier NOT NULL
            CONSTRAINT PK_SigningKeysMetadata PRIMARY KEY
            CONSTRAINT DF_SigningKeysMetadata_Id DEFAULT (NEWSEQUENTIALID()),
        KeyId                nvarchar(128)    NOT NULL,
        PublicKey            varbinary(32)    NOT NULL,
        /* DPAPI- or KMS-wrapped private key material — never store plaintext */
        ProtectedPrivateKey  varbinary(max)   NOT NULL,
        /* SigningKeyStatus: 0=Current, 1=Next, 2=Retired */
        Status               int              NOT NULL
            CONSTRAINT DF_SigningKeysMetadata_Status DEFAULT (0),
        CreatedAt            datetime2(7)     NOT NULL
            CONSTRAINT DF_SigningKeysMetadata_CreatedAt DEFAULT (SYSUTCDATETIME()),
        RetiredAt            datetime2(7)     NULL,
        CONSTRAINT UQ_SigningKeysMetadata_KeyId UNIQUE (KeyId),
        CONSTRAINT CK_SigningKeysMetadata_Status CHECK (Status BETWEEN 0 AND 2),
        CONSTRAINT CK_SigningKeysMetadata_PublicKeyLen CHECK (DATALENGTH(PublicKey) = 32)
    );
END
GO

IF OBJECT_ID(N'dbo.SigningKeysMetadata', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_SigningKeysMetadata_Status' AND object_id = OBJECT_ID(N'dbo.SigningKeysMetadata'))
    CREATE NONCLUSTERED INDEX IX_SigningKeysMetadata_Status ON dbo.SigningKeysMetadata (Status);
GO

/* --- AuditLog ------------------------------------------------------------- */
IF OBJECT_ID(N'dbo.AuditLog', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.AuditLog
    (
        Id           uniqueidentifier NOT NULL
            CONSTRAINT PK_AuditLog PRIMARY KEY
            CONSTRAINT DF_AuditLog_Id DEFAULT (NEWSEQUENTIALID()),
        Actor        nvarchar(256)    NOT NULL,
        Action       nvarchar(128)    NOT NULL,
        EntityType   nvarchar(128)    NOT NULL,
        EntityId     nvarchar(256)    NULL,
        [Timestamp]  datetime2(7)     NOT NULL
            CONSTRAINT DF_AuditLog_Timestamp DEFAULT (SYSUTCDATETIME()),
        IpAddress    nvarchar(64)     NULL,
        DetailsJson  nvarchar(max)    NULL
    );
END
GO

IF OBJECT_ID(N'dbo.AuditLog', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_AuditLog_Timestamp' AND object_id = OBJECT_ID(N'dbo.AuditLog'))
    CREATE NONCLUSTERED INDEX IX_AuditLog_Timestamp ON dbo.AuditLog ([Timestamp] DESC);
GO

IF OBJECT_ID(N'dbo.AuditLog', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_AuditLog_EntityType_EntityId' AND object_id = OBJECT_ID(N'dbo.AuditLog'))
    CREATE NONCLUSTERED INDEX IX_AuditLog_EntityType_EntityId
        ON dbo.AuditLog (EntityType, EntityId);
GO

/* --- SystemSettings ------------------------------------------------------- */
IF OBJECT_ID(N'dbo.SystemSettings', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.SystemSettings
    (
        [Key]        nvarchar(128)  NOT NULL
            CONSTRAINT PK_SystemSettings PRIMARY KEY,
        [Value]      nvarchar(max)  NOT NULL,
        UpdatedAt    datetime2(7)   NOT NULL
            CONSTRAINT DF_SystemSettings_UpdatedAt DEFAULT (SYSUTCDATETIME()),
        UpdatedBy    nvarchar(256)  NULL
    );
END
GO

/* --- PaymentEvents -------------------------------------------------------- */
IF OBJECT_ID(N'dbo.PaymentEvents', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.PaymentEvents
    (
        Id                  uniqueidentifier NOT NULL
            CONSTRAINT PK_PaymentEvents PRIMARY KEY
            CONSTRAINT DF_PaymentEvents_Id DEFAULT (NEWSEQUENTIALID()),
        Provider            nvarchar(64)     NOT NULL,
        ExternalPaymentId   nvarchar(256)    NOT NULL,
        /* PaymentEventStatus: 0=Received, 1=Processed, 2=Failed, 3=Ignored */
        Status              int              NOT NULL
            CONSTRAINT DF_PaymentEvents_Status DEFAULT (0),
        Amount              decimal(18, 4)   NULL,
        Currency            nvarchar(8)      NULL,
        PayloadHash         nvarchar(128)    NULL,
        ReceivedAt          datetime2(7)     NOT NULL
            CONSTRAINT DF_PaymentEvents_ReceivedAt DEFAULT (SYSUTCDATETIME()),
        ProcessedAt         datetime2(7)     NULL,
        CONSTRAINT UQ_PaymentEvents_Provider_ExternalPaymentId UNIQUE (Provider, ExternalPaymentId),
        CONSTRAINT CK_PaymentEvents_Status CHECK (Status BETWEEN 0 AND 3),
        CONSTRAINT CK_PaymentEvents_Amount CHECK (Amount IS NULL OR Amount >= 0)
    );
END
GO

IF OBJECT_ID(N'dbo.PaymentEvents', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_PaymentEvents_Status_ReceivedAt' AND object_id = OBJECT_ID(N'dbo.PaymentEvents'))
    CREATE NONCLUSTERED INDEX IX_PaymentEvents_Status_ReceivedAt
        ON dbo.PaymentEvents (Status, ReceivedAt DESC);
GO

/* ========================================================================== */
/* ASP.NET Core Identity schema (EF Core compatible)                          */
/* Admin operators — separate from dbo.Users (VPN end-users).                 */
/* ========================================================================== */

IF OBJECT_ID(N'dbo.AspNetRoles', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.AspNetRoles
    (
        Id               nvarchar(450) NOT NULL
            CONSTRAINT PK_AspNetRoles PRIMARY KEY,
        Name             nvarchar(256) NULL,
        NormalizedName   nvarchar(256) NULL,
        ConcurrencyStamp nvarchar(max) NULL
    );
END
GO

IF OBJECT_ID(N'dbo.AspNetRoles', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'RoleNameIndex' AND object_id = OBJECT_ID(N'dbo.AspNetRoles'))
    CREATE UNIQUE NONCLUSTERED INDEX RoleNameIndex
        ON dbo.AspNetRoles (NormalizedName) WHERE NormalizedName IS NOT NULL;
GO

IF OBJECT_ID(N'dbo.AspNetUsers', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.AspNetUsers
    (
        Id                   nvarchar(450)     NOT NULL
            CONSTRAINT PK_AspNetUsers PRIMARY KEY,
        UserName             nvarchar(256)     NULL,
        NormalizedUserName   nvarchar(256)     NULL,
        Email                nvarchar(256)     NULL,
        NormalizedEmail      nvarchar(256)     NULL,
        EmailConfirmed       bit               NOT NULL
            CONSTRAINT DF_AspNetUsers_EmailConfirmed DEFAULT (0),
        PasswordHash         nvarchar(max)     NULL,
        SecurityStamp        nvarchar(max)     NULL,
        ConcurrencyStamp     nvarchar(max)     NULL,
        PhoneNumber          nvarchar(max)     NULL,
        PhoneNumberConfirmed bit               NOT NULL
            CONSTRAINT DF_AspNetUsers_PhoneNumberConfirmed DEFAULT (0),
        TwoFactorEnabled     bit               NOT NULL
            CONSTRAINT DF_AspNetUsers_TwoFactorEnabled DEFAULT (0),
        LockoutEnd           datetimeoffset(7) NULL,
        LockoutEnabled       bit               NOT NULL
            CONSTRAINT DF_AspNetUsers_LockoutEnabled DEFAULT (1),
        AccessFailedCount    int               NOT NULL
            CONSTRAINT DF_AspNetUsers_AccessFailedCount DEFAULT (0)
    );
END
GO

IF OBJECT_ID(N'dbo.AspNetUsers', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'UserNameIndex' AND object_id = OBJECT_ID(N'dbo.AspNetUsers'))
    CREATE UNIQUE NONCLUSTERED INDEX UserNameIndex
        ON dbo.AspNetUsers (NormalizedUserName) WHERE NormalizedUserName IS NOT NULL;
GO

IF OBJECT_ID(N'dbo.AspNetUsers', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'EmailIndex' AND object_id = OBJECT_ID(N'dbo.AspNetUsers'))
    CREATE NONCLUSTERED INDEX EmailIndex ON dbo.AspNetUsers (NormalizedEmail);
GO

IF OBJECT_ID(N'dbo.AspNetRoleClaims', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.AspNetRoleClaims
    (
        Id         int            NOT NULL IDENTITY(1, 1)
            CONSTRAINT PK_AspNetRoleClaims PRIMARY KEY,
        RoleId     nvarchar(450)  NOT NULL,
        ClaimType  nvarchar(max)  NULL,
        ClaimValue nvarchar(max)  NULL,
        CONSTRAINT FK_AspNetRoleClaims_AspNetRoles_RoleId FOREIGN KEY (RoleId)
            REFERENCES dbo.AspNetRoles (Id) ON DELETE CASCADE
    );
END
GO

IF OBJECT_ID(N'dbo.AspNetRoleClaims', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_AspNetRoleClaims_RoleId' AND object_id = OBJECT_ID(N'dbo.AspNetRoleClaims'))
    CREATE NONCLUSTERED INDEX IX_AspNetRoleClaims_RoleId ON dbo.AspNetRoleClaims (RoleId);
GO

IF OBJECT_ID(N'dbo.AspNetUserClaims', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.AspNetUserClaims
    (
        Id         int            NOT NULL IDENTITY(1, 1)
            CONSTRAINT PK_AspNetUserClaims PRIMARY KEY,
        UserId     nvarchar(450)  NOT NULL,
        ClaimType  nvarchar(max)  NULL,
        ClaimValue nvarchar(max)  NULL,
        CONSTRAINT FK_AspNetUserClaims_AspNetUsers_UserId FOREIGN KEY (UserId)
            REFERENCES dbo.AspNetUsers (Id) ON DELETE CASCADE
    );
END
GO

IF OBJECT_ID(N'dbo.AspNetUserClaims', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_AspNetUserClaims_UserId' AND object_id = OBJECT_ID(N'dbo.AspNetUserClaims'))
    CREATE NONCLUSTERED INDEX IX_AspNetUserClaims_UserId ON dbo.AspNetUserClaims (UserId);
GO

IF OBJECT_ID(N'dbo.AspNetUserLogins', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.AspNetUserLogins
    (
        LoginProvider       nvarchar(450) NOT NULL,
        ProviderKey         nvarchar(450) NOT NULL,
        ProviderDisplayName nvarchar(max) NULL,
        UserId              nvarchar(450) NOT NULL,
        CONSTRAINT PK_AspNetUserLogins PRIMARY KEY (LoginProvider, ProviderKey),
        CONSTRAINT FK_AspNetUserLogins_AspNetUsers_UserId FOREIGN KEY (UserId)
            REFERENCES dbo.AspNetUsers (Id) ON DELETE CASCADE
    );
END
GO

IF OBJECT_ID(N'dbo.AspNetUserLogins', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_AspNetUserLogins_UserId' AND object_id = OBJECT_ID(N'dbo.AspNetUserLogins'))
    CREATE NONCLUSTERED INDEX IX_AspNetUserLogins_UserId ON dbo.AspNetUserLogins (UserId);
GO

IF OBJECT_ID(N'dbo.AspNetUserRoles', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.AspNetUserRoles
    (
        UserId nvarchar(450) NOT NULL,
        RoleId nvarchar(450) NOT NULL,
        CONSTRAINT PK_AspNetUserRoles PRIMARY KEY (UserId, RoleId),
        CONSTRAINT FK_AspNetUserRoles_AspNetUsers_UserId FOREIGN KEY (UserId)
            REFERENCES dbo.AspNetUsers (Id) ON DELETE CASCADE,
        CONSTRAINT FK_AspNetUserRoles_AspNetRoles_RoleId FOREIGN KEY (RoleId)
            REFERENCES dbo.AspNetRoles (Id) ON DELETE CASCADE
    );
END
GO

IF OBJECT_ID(N'dbo.AspNetUserRoles', N'U') IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_AspNetUserRoles_RoleId' AND object_id = OBJECT_ID(N'dbo.AspNetUserRoles'))
    CREATE NONCLUSTERED INDEX IX_AspNetUserRoles_RoleId ON dbo.AspNetUserRoles (RoleId);
GO

IF OBJECT_ID(N'dbo.AspNetUserTokens', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.AspNetUserTokens
    (
        UserId        nvarchar(450) NOT NULL,
        LoginProvider nvarchar(450) NOT NULL,
        Name          nvarchar(450) NOT NULL,
        Value         nvarchar(max) NULL,
        CONSTRAINT PK_AspNetUserTokens PRIMARY KEY (UserId, LoginProvider, Name),
        CONSTRAINT FK_AspNetUserTokens_AspNetUsers_UserId FOREIGN KEY (UserId)
            REFERENCES dbo.AspNetUsers (Id) ON DELETE CASCADE
    );
END
GO

/* ========================================================================== */
/* Sanity checks                                                              */
/* ========================================================================== */
PRINT N'';
PRINT N'=== NyxveilControlPlane sanity checks ===';
PRINT N'';

DECLARE @expected TABLE (TableName sysname NOT NULL);
INSERT INTO @expected (TableName) VALUES
    (N'Users'),
    (N'Plans'),
    (N'Licenses'),
    (N'LicenseAllowedLocations'),
    (N'Devices'),
    (N'Locations'),
    (N'Nodes'),
    (N'NodeEndpoints'),
    (N'NodeTransports'),
    (N'NodeHealth'),
    (N'NodeMetrics'),
    (N'NodeCredentials'),
    (N'NodeConfigs'),
    (N'BootstrapTokens'),
    (N'TicketAudits'),
    (N'Revocations'),
    (N'CatalogVersions'),
    (N'SigningKeysMetadata'),
    (N'AuditLog'),
    (N'SystemSettings'),
    (N'PaymentEvents'),
    (N'AspNetUsers'),
    (N'AspNetRoles'),
    (N'AspNetUserRoles'),
    (N'AspNetUserClaims'),
    (N'AspNetRoleClaims'),
    (N'AspNetUserLogins'),
    (N'AspNetUserTokens');

DECLARE @missing int = 0;
DECLARE @name sysname;

DECLARE missing_cursor CURSOR LOCAL FAST_FORWARD FOR
    SELECT e.TableName
    FROM @expected e
    WHERE OBJECT_ID(N'dbo.' + e.TableName, N'U') IS NULL;

OPEN missing_cursor;
FETCH NEXT FROM missing_cursor INTO @name;
WHILE @@FETCH_STATUS = 0
BEGIN
    PRINT N'MISSING TABLE: dbo.' + @name;
    SET @missing += 1;
    FETCH NEXT FROM missing_cursor INTO @name;
END
CLOSE missing_cursor;
DEALLOCATE missing_cursor;

IF @missing = 0
    PRINT N'All expected tables exist.';
ELSE
    PRINT N'Missing table count: ' + CAST(@missing AS nvarchar(11));

PRINT N'';
PRINT N'Table row counts:';

DECLARE @sql nvarchar(max) = N'';
SELECT @sql = @sql +
    N'SELECT ''' + e.TableName + N''' AS TableName, COUNT_BIG(*) AS [RowCount] FROM dbo.' + QUOTENAME(e.TableName) + N' UNION ALL '
FROM @expected e
WHERE OBJECT_ID(N'dbo.' + e.TableName, N'U') IS NOT NULL;

IF LEN(@sql) > 0
BEGIN
    SET @sql = LEFT(@sql, LEN(@sql) - LEN(N' UNION ALL '));
    SET @sql = @sql + N' ORDER BY TableName;';
    EXEC sys.sp_executesql @sql;
END

PRINT N'';
PRINT N'Database: ' + DB_NAME();

/* -------------------------------------------------------------------------- */
/* EF Core migrations history (aligns SQL bootstrap with InitialCreate)       */
/* -------------------------------------------------------------------------- */
IF OBJECT_ID(N'dbo.__EFMigrationsHistory', N'U') IS NULL
BEGIN
    CREATE TABLE dbo.__EFMigrationsHistory
    (
        MigrationId    nvarchar(150) NOT NULL,
        ProductVersion nvarchar(32)  NOT NULL,
        CONSTRAINT PK___EFMigrationsHistory PRIMARY KEY (MigrationId)
    );
    PRINT N'Created dbo.__EFMigrationsHistory';
END
GO

IF OBJECT_ID(N'dbo.__EFMigrationsHistory', N'U') IS NOT NULL
   AND NOT EXISTS (
        SELECT 1
        FROM dbo.__EFMigrationsHistory
        WHERE MigrationId = N'20260904155703_InitialCreate')
BEGIN
    INSERT INTO dbo.__EFMigrationsHistory (MigrationId, ProductVersion)
    VALUES (N'20260904155703_InitialCreate', N'10.0.11');
    PRINT N'Inserted EF migration marker 20260904155703_InitialCreate (10.0.11)';
END
GO

PRINT N'Bootstrap complete (idempotent).';
GO
