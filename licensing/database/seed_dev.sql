/*
================================================================================
  Nyxveil Control Plane — DEVELOPMENT seed data ONLY
================================================================================
  WARNING: DO NOT RUN THIS SCRIPT IN PRODUCTION.

  This file inserts non-secret reference data for local / CI development:
    - Plans: trial, standard, premium, master
    - Location: fi-helsinki

  It contains NO license secrets, signing keys, passwords, or payment tokens.
  Re-running is idempotent (IF NOT EXISTS / MERGE on stable codes).

  Prerequisites: create_database.sql has been applied to NyxveilControlPlane.
================================================================================
*/

SET NOCOUNT ON;
SET XACT_ABORT ON;
GO

USE [NyxveilControlPlane];
GO

PRINT N'[seed_dev] Starting development seed (NOT FOR PRODUCTION)...';
GO

/* -------------------------------------------------------------------------- */
/* Plans                                                                      */
/* -------------------------------------------------------------------------- */
DECLARE @now datetime2(7) = SYSUTCDATETIME();

MERGE dbo.Plans AS t
USING (VALUES
    (N'trial',    N'Trial',    N'Active', 7,  1, N'["fi-helsinki"]', N'["connect","catalog"]',               @now),
    (N'standard', N'Standard', N'Active', 30, 3, N'["*"]',           N'["connect","catalog","refresh"]',     @now),
    (N'premium',  N'Premium',  N'Active', 30, 5, N'["*"]',           N'["connect","catalog","refresh"]',     @now),
    (N'master',   N'Master',   N'Active', 365, 10, N'["*"]',         N'["connect","catalog","refresh","test_nodes","admin"]', @now)
) AS s (Code, Name, Status, DurationDays, MaxDevices, AllowedLocationsPolicy, Permissions, TouchedAt)
ON t.Code = s.Code
WHEN MATCHED THEN
    UPDATE SET
        t.Name = s.Name,
        t.Status = s.Status,
        t.DurationDays = s.DurationDays,
        t.MaxDevices = s.MaxDevices,
        t.AllowedLocationsPolicy = s.AllowedLocationsPolicy,
        t.Permissions = s.Permissions,
        t.UpdatedAt = s.TouchedAt
WHEN NOT MATCHED THEN
    INSERT (PlanId, Code, Name, Status, DurationDays, MaxDevices, AllowedLocationsPolicy, Permissions, CreatedAt, UpdatedAt)
    VALUES (NEWID(), s.Code, s.Name, s.Status, s.DurationDays, s.MaxDevices, s.AllowedLocationsPolicy, s.Permissions, s.TouchedAt, s.TouchedAt);

PRINT N'[seed_dev] Plans upserted: trial, standard, premium, master';
GO

/* -------------------------------------------------------------------------- */
/* Location: fi-helsinki                                                      */
/* -------------------------------------------------------------------------- */
DECLARE @now datetime2(7) = SYSUTCDATETIME();

MERGE dbo.Locations AS t
USING (VALUES
    (N'fi-helsinki', N'fi-helsinki', N'Finland', N'Helsinki', N'Helsinki, Finland', CAST(1 AS bit), 10, N'FI', @now)
) AS s (LocationId, Code, Country, City, DisplayName, Enabled, SortOrder, CountryCode, TouchedAt)
ON t.LocationId = s.LocationId
WHEN MATCHED THEN
    UPDATE SET
        t.Code = s.Code,
        t.Country = s.Country,
        t.City = s.City,
        t.DisplayName = s.DisplayName,
        t.Enabled = s.Enabled,
        t.SortOrder = s.SortOrder,
        t.CountryCode = s.CountryCode,
        t.UpdatedAt = s.TouchedAt
WHEN NOT MATCHED THEN
    INSERT (LocationId, Code, Country, City, DisplayName, Enabled, SortOrder, CreatedAt, UpdatedAt, CountryCode)
    VALUES (s.LocationId, s.Code, s.Country, s.City, s.DisplayName, s.Enabled, s.SortOrder, s.TouchedAt, s.TouchedAt, s.CountryCode);

PRINT N'[seed_dev] Location upserted: fi-helsinki';
GO

/* -------------------------------------------------------------------------- */
/* Dev-only system setting marker                                             */
/* -------------------------------------------------------------------------- */
MERGE dbo.SystemSettings AS t
USING (VALUES
    (N'dev.seed_applied', N'true', SYSUTCDATETIME(), N'seed_dev.sql')
) AS s ([Key], [Value], UpdatedAt, UpdatedBy)
ON t.[Key] = s.[Key]
WHEN MATCHED THEN
    UPDATE SET t.[Value] = s.[Value], t.UpdatedAt = s.UpdatedAt, t.UpdatedBy = s.UpdatedBy
WHEN NOT MATCHED THEN
    INSERT ([Key], [Value], UpdatedAt, UpdatedBy)
    VALUES (s.[Key], s.[Value], s.UpdatedAt, s.UpdatedBy);

PRINT N'[seed_dev] Done. Remember: production must not run this script.';
GO
