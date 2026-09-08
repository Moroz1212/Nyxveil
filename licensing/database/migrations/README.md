# EF Core migrations live in:
#   src/Nyxveil.ControlPlane.Infrastructure/Persistence/Migrations/
#
# Initial schema is also available as idempotent T-SQL:
#   database/create_database.sql
#
# Apply EF migrations:
#   dotnet ef database update --project src/Nyxveil.ControlPlane.Infrastructure --startup-project src/Nyxveil.ControlPlane.Web
#
# Operational SQL migrations (idempotent):
#   002_node_lifecycle_cert_metadata.sql  → schema version 2
#   003_node_commands_cert_renewal.sql    → schema version 3
#   004_version_mgmt_signing_retiring.sql → schema version 4
# Validators: validate_schema_v2.sql (kept), validate_schema_v3.sql (current)
