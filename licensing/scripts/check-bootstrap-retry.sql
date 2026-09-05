-- Read-only check for retrying this failed FIRST installation.
-- Select the intended database with sqlcmd -d. Never run against master.
SET NOCOUNT ON;

IF DB_NAME() IN (N'master', N'model', N'msdb', N'tempdb')
    THROW 51000, 'Select the dedicated Nyxveil database with sqlcmd -d.', 1;

IF EXISTS (
    SELECT 1 FROM sys.tables
    WHERE is_ms_shipped = 0
      AND NOT (name = N'__EFMigrationsHistory' AND schema_id = SCHEMA_ID(N'dbo'))
)
BEGIN
    SELECT SCHEMA_NAME(schema_id) AS SchemaName, name AS TableName
    FROM sys.tables WHERE is_ms_shipped = 0 ORDER BY SchemaName, TableName;
    THROW 51001, 'STOP: application tables remain. Inspect the schema before retrying; do not drop data.', 1;
END;

IF OBJECT_ID(N'dbo.__EFMigrationsHistory', N'U') IS NOT NULL
    EXEC(N'IF EXISTS (SELECT 1 FROM dbo.__EFMigrationsHistory)
        THROW 51002, ''STOP: a migration marker already exists. Inspect the installation state.'', 1;');

PRINT N'SAFE_TO_RETRY_FRESH_BOOTSTRAP: no application tables or migration rows remain.';
