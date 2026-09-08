using System;
using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Nyxveil.ControlPlane.Infrastructure.Persistence.Migrations
{
    /// <inheritdoc />
    public partial class NodeCommandsAndCertRenewal : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<string>(
                name: "LastBootId",
                table: "Nodes",
                type: "nvarchar(128)",
                maxLength: 128,
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "ManagementCapabilities",
                table: "Nodes",
                type: "nvarchar(512)",
                maxLength: 512,
                nullable: true);

            migrationBuilder.AddColumn<bool>(
                name: "SupportsNodeCommands",
                table: "Nodes",
                type: "bit",
                nullable: false,
                defaultValue: false);

            migrationBuilder.CreateTable(
                name: "CertificateRenewalOperations",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    Status = table.Column<int>(type: "int", nullable: false),
                    Domain = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    CreatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    CreatedBy = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    UpdatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    AcmeOrderUrl = table.Column<string>(type: "nvarchar(1024)", maxLength: 1024, nullable: true),
                    ChallengeName = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    ChallengeValue = table.Column<string>(type: "nvarchar(512)", maxLength: 512, nullable: false),
                    ChallengeExpiresAt = table.Column<DateTime>(type: "datetime2", nullable: true),
                    NewThumbprint = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: true),
                    OldThumbprint = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: true),
                    ErrorMessage = table.Column<string>(type: "nvarchar(1024)", maxLength: 1024, nullable: true),
                    CompletedAt = table.Column<DateTime>(type: "datetime2", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_CertificateRenewalOperations", x => x.Id);
                    table.CheckConstraint("CK_CertificateRenewalOperations_Status", "[Status] BETWEEN 0 AND 7");
                });

            migrationBuilder.CreateTable(
                name: "NodeCommands",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    NodeId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    Type = table.Column<int>(type: "int", nullable: false),
                    Status = table.Column<int>(type: "int", nullable: false),
                    CreatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    CreatedBy = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    IssuedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    ExpiresAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    ClaimedAt = table.Column<DateTime>(type: "datetime2", nullable: true),
                    StartedAt = table.Column<DateTime>(type: "datetime2", nullable: true),
                    CompletedAt = table.Column<DateTime>(type: "datetime2", nullable: true),
                    ResultCode = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: true),
                    ResultMessage = table.Column<string>(type: "nvarchar(1024)", maxLength: 1024, nullable: true),
                    AttemptCount = table.Column<int>(type: "int", nullable: false),
                    CorrelationId = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    PayloadJson = table.Column<string>(type: "nvarchar(max)", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_NodeCommands", x => x.Id);
                    table.CheckConstraint("CK_NodeCommands_AttemptCount", "[AttemptCount] >= 0");
                    table.CheckConstraint("CK_NodeCommands_Status", "[Status] BETWEEN 0 AND 10");
                    table.CheckConstraint("CK_NodeCommands_Type", "[Type] BETWEEN 0 AND 2");
                    table.ForeignKey(
                        name: "FK_NodeCommands_Nodes_NodeId",
                        column: x => x.NodeId,
                        principalTable: "Nodes",
                        principalColumn: "NodeId",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "IX_CertificateRenewalOperations_Status_CreatedAt",
                table: "CertificateRenewalOperations",
                columns: new[] { "Status", "CreatedAt" });

            migrationBuilder.CreateIndex(
                name: "IX_NodeCommands_CorrelationId",
                table: "NodeCommands",
                column: "CorrelationId");

            migrationBuilder.CreateIndex(
                name: "IX_NodeCommands_ExpiresAt",
                table: "NodeCommands",
                column: "ExpiresAt");

            migrationBuilder.CreateIndex(
                name: "IX_NodeCommands_NodeId_Status_IssuedAt",
                table: "NodeCommands",
                columns: new[] { "NodeId", "Status", "IssuedAt" });
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "CertificateRenewalOperations");

            migrationBuilder.DropTable(
                name: "NodeCommands");

            migrationBuilder.DropColumn(
                name: "LastBootId",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "ManagementCapabilities",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "SupportsNodeCommands",
                table: "Nodes");
        }
    }
}
