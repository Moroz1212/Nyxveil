/*
  INTENTIONALLY BROKEN fixture — reproduces production Msg 207.
  Used only by regression tests. DO NOT apply to production.
*/
SET XACT_ABORT ON;
BEGIN TRANSACTION;
IF COL_LENGTH('dbo.Nodes', 'LifecycleState') IS NULL
    ALTER TABLE dbo.Nodes ADD LifecycleState int NOT NULL CONSTRAINT DF_Nodes_LifecycleState_Broken DEFAULT (0);
-- Static reference in the same batch as ADD COLUMN → SQL Server Msg 207.
IF NOT EXISTS (SELECT 1 FROM sys.check_constraints WHERE name = N'CK_Nodes_LifecycleState_Broken')
    ALTER TABLE dbo.Nodes WITH CHECK ADD CONSTRAINT CK_Nodes_LifecycleState_Broken CHECK (LifecycleState BETWEEN 0 AND 2);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE object_id = OBJECT_ID(N'dbo.Nodes') AND name = N'IX_Nodes_LifecycleState_Broken')
    CREATE INDEX IX_Nodes_LifecycleState_Broken ON dbo.Nodes(LifecycleState);
COMMIT TRANSACTION;
