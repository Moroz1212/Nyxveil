# EF Core migrations live in:
#   src/Nyxveil.ControlPlane.Infrastructure/Persistence/Migrations/
#
# Initial schema is also available as idempotent T-SQL:
#   database/create_database.sql
#
# Apply EF migrations:
#   dotnet ef database update --project src/Nyxveil.ControlPlane.Infrastructure --startup-project src/Nyxveil.ControlPlane.Web
