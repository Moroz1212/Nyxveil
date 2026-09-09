# EF Core migrations live in:
#   src/Nyxveil.ControlPlane.Infrastructure/Persistence/Migrations/
#
# Initial schema is also available as idempotent T-SQL:
#   database/create_database.sql
#   (EF baseline + operational NyxveilSchemaVersion seed = schema 5)
#
# Apply EF migrations:
#   dotnet ef database update --project src/Nyxveil.ControlPlane.Infrastructure --startup-project src/Nyxveil.ControlPlane.Web
#
# Operational SQL migrations (idempotent):
#   002_node_lifecycle_cert_metadata.sql  → schema version 2
#   003_node_commands_cert_renewal.sql    → schema version 3
#   004_version_mgmt_signing_retiring.sql → schema version 4
#   005_certificate_operation_states.sql  → schema version 5 (current)
#
# Validators:
#   validate_schema_v2.sql … validate_schema_v4.sql (historical)
#   validate_schema_v5.sql (current — used by production-deploy / production-gate)
