-- Schema v5: allow durable switching/expiry states used by the certificate wizard.
SET NOCOUNT ON;
SET XACT_ABORT ON;
BEGIN TRY
    BEGIN TRAN;
    IF OBJECT_ID(N'dbo.CertificateRenewalOperations', N'U') IS NULL
        THROW 55001, N'CertificateRenewalOperations missing; apply schemas 2-4 first.', 1;
    IF NOT EXISTS (SELECT 1 FROM dbo.NyxveilSchemaVersion WHERE Version BETWEEN 4 AND 5)
        THROW 55002, N'Expected schema 4 or 5.', 1;
    IF EXISTS (SELECT 1 FROM sys.check_constraints WHERE parent_object_id=OBJECT_ID(N'dbo.CertificateRenewalOperations') AND name=N'CK_CertificateRenewalOperations_Status')
        ALTER TABLE dbo.CertificateRenewalOperations DROP CONSTRAINT CK_CertificateRenewalOperations_Status;
    ALTER TABLE dbo.CertificateRenewalOperations WITH CHECK ADD CONSTRAINT CK_CertificateRenewalOperations_Status CHECK ([Status] BETWEEN 0 AND 9);
    UPDATE dbo.NyxveilSchemaVersion SET Version=5, AppliedAt=SYSUTCDATETIME();
    COMMIT TRAN;
END TRY
BEGIN CATCH
    IF @@TRANCOUNT > 0 ROLLBACK TRAN;
    THROW;
END CATCH;
