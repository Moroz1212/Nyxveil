using Microsoft.EntityFrameworkCore.Infrastructure;
using Microsoft.EntityFrameworkCore.Migrations;

namespace Nyxveil.ControlPlane.Infrastructure.Persistence.Migrations;

[DbContext(typeof(ControlPlaneDbContext))]
[Migration("20260909160000_CertificateOperationStates")]
public sealed partial class CertificateOperationStates : Migration
{
    protected override void Up(MigrationBuilder migrationBuilder)
    {
        migrationBuilder.DropCheckConstraint("CK_CertificateRenewalOperations_Status", "CertificateRenewalOperations");
        migrationBuilder.AddCheckConstraint("CK_CertificateRenewalOperations_Status", "CertificateRenewalOperations", "[Status] BETWEEN 0 AND 9");
    }

    protected override void Down(MigrationBuilder migrationBuilder)
    {
        // Do not silently erase active operations to make a downgrade fit the old schema.
        migrationBuilder.Sql("IF EXISTS (SELECT 1 FROM dbo.CertificateRenewalOperations WHERE Status > 7) THROW 55003, 'Certificate states 8/9 must be resolved before schema downgrade.', 1;");
        migrationBuilder.DropCheckConstraint("CK_CertificateRenewalOperations_Status", "CertificateRenewalOperations");
        migrationBuilder.AddCheckConstraint("CK_CertificateRenewalOperations_Status", "CertificateRenewalOperations", "[Status] BETWEEN 0 AND 7");
    }
}
