-- Schema v5: allow durable switching/expiry states used by the certificate wizard.
-- Idempotent: safe to re-run on schema 4 or 5. Refuses when schema < 4.
-- Also keeps __EFMigrationsHistory aligned with EF CertificateOperationStates.
SET NOCOUNT ON;
SET XACT_ABORT ON;
BEGIN TRY
    BEGIN TRAN;

    IF OBJECT_ID(N'dbo.CertificateRenewalOperations', N'U') IS NULL
        THROW 55001, N'CertificateRenewalOperations missing; apply schemas 2-4 first.', 1;

    IF OBJECT_ID(N'dbo.NyxveilSchemaVersion', N'U') IS NULL
        THROW 55002, N'NyxveilSchemaVersion missing; apply schemas 2-4 first.', 1;

    IF NOT EXISTS (SELECT 1 FROM dbo.NyxveilSchemaVersion WHERE Version BETWEEN 4 AND 5)
        THROW 55002, N'Expected schema 4 or 5.', 1;

    IF EXISTS (
        SELECT 1 FROM sys.check_constraints
        WHERE parent_object_id = OBJECT_ID(N'dbo.CertificateRenewalOperations')
          AND name = N'CK_CertificateRenewalOperations_Status')
    BEGIN
        ALTER TABLE dbo.CertificateRenewalOperations DROP CONSTRAINT CK_CertificateRenewalOperations_Status;
    END

    ALTER TABLE dbo.CertificateRenewalOperations WITH CHECK
        ADD CONSTRAINT CK_CertificateRenewalOperations_Status CHECK ([Status] BETWEEN 0 AND 9);

    -- Keep dual migration systems aligned (operational version + EF history).
    IF OBJECT_ID(N'dbo.__EFMigrationsHistory', N'U') IS NOT NULL
    BEGIN
        EXEC(N'
            IF NOT EXISTS (
                SELECT 1 FROM dbo.__EFMigrationsHistory
                WHERE MigrationId = N''20260909160000_CertificateOperationStates''
            )
            BEGIN
                INSERT INTO dbo.__EFMigrationsHistory (MigrationId, ProductVersion)
                VALUES (N''20260909160000_CertificateOperationStates'', N''10.0.11'');
            END
        ');
    END

    IF COL_LENGTH(N'dbo.NyxveilSchemaVersion', N'Id') IS NOT NULL
    BEGIN
        IF EXISTS (SELECT 1 FROM dbo.NyxveilSchemaVersion WHERE Id = 1)
            UPDATE dbo.NyxveilSchemaVersion
            SET Version = 5, AppliedAt = SYSUTCDATETIME(), AppliedBy = N'migration_005'
            WHERE Id = 1;
        ELSE
            INSERT INTO dbo.NyxveilSchemaVersion (Id, Version, AppliedAt, AppliedBy)
            VALUES (1, 5, SYSUTCDATETIME(), N'migration_005');
    END
    ELSE
    BEGIN
        UPDATE dbo.NyxveilSchemaVersion SET Version = 5, AppliedAt = SYSUTCDATETIME();
        IF @@ROWCOUNT = 0
            INSERT INTO dbo.NyxveilSchemaVersion (Version) VALUES (5);
    END

    COMMIT TRAN;
END TRY
BEGIN CATCH
    IF @@TRANCOUNT > 0 ROLLBACK TRAN;
    THROW;
END CATCH;
