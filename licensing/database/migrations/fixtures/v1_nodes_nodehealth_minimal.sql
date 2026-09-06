SET NOCOUNT ON;
SET XACT_ABORT ON;

CREATE TABLE dbo.Nodes
(
    Id int IDENTITY(1,1) NOT NULL
        CONSTRAINT PK_Nodes_Minimal PRIMARY KEY
);

CREATE TABLE dbo.NodeHealth
(
    Id int IDENTITY(1,1) NOT NULL
        CONSTRAINT PK_NodeHealth_Minimal PRIMARY KEY
);

CREATE TABLE dbo.NyxveilSchemaVersion
(
    Id int NOT NULL CONSTRAINT PK_NyxveilSchemaVersion PRIMARY KEY,
    Version int NOT NULL,
    AppliedAt datetime2 NOT NULL,
    AppliedBy nvarchar(128) NOT NULL
);

INSERT INTO dbo.NyxveilSchemaVersion(Id, Version, AppliedAt, AppliedBy)
VALUES (1, 1, SYSUTCDATETIME(), N'v1-test-fixture');
