using System;
using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Nyxveil.ControlPlane.Infrastructure.Persistence.Migrations
{
    /// <inheritdoc />
    public partial class VersionMgmtSigningRetiring : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropCheckConstraint(
                name: "CK_SigningKeysMetadata_Status",
                table: "SigningKeysMetadata");

            migrationBuilder.DropCheckConstraint(
                name: "CK_NodeCommands_Type",
                table: "NodeCommands");

            migrationBuilder.AddColumn<DateTime>(
                name: "PromotedAt",
                table: "SigningKeysMetadata",
                type: "datetime2",
                nullable: true);

            migrationBuilder.AddColumn<DateTime>(
                name: "RetireAfter",
                table: "SigningKeysMetadata",
                type: "datetime2",
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "ReportedServerVersion",
                table: "Nodes",
                type: "nvarchar(64)",
                maxLength: 64,
                nullable: true);

            migrationBuilder.AddColumn<DateTime>(
                name: "VersionReportedAt",
                table: "Nodes",
                type: "datetime2",
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "PreviousVersion",
                table: "NodeCommands",
                type: "nvarchar(64)",
                maxLength: 64,
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "ProgressMessage",
                table: "NodeCommands",
                type: "nvarchar(512)",
                maxLength: 512,
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "ProgressPhase",
                table: "NodeCommands",
                type: "nvarchar(64)",
                maxLength: 64,
                nullable: true);

            migrationBuilder.AddColumn<DateTime>(
                name: "ProgressUpdatedAt",
                table: "NodeCommands",
                type: "datetime2",
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "TargetVersion",
                table: "NodeCommands",
                type: "nvarchar(64)",
                maxLength: 64,
                nullable: true);

            migrationBuilder.AddCheckConstraint(
                name: "CK_SigningKeysMetadata_Status",
                table: "SigningKeysMetadata",
                sql: "[Status] BETWEEN 0 AND 3");

            migrationBuilder.AddCheckConstraint(
                name: "CK_NodeCommands_Type",
                table: "NodeCommands",
                sql: "[Type] BETWEEN 0 AND 3");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropCheckConstraint(
                name: "CK_SigningKeysMetadata_Status",
                table: "SigningKeysMetadata");

            migrationBuilder.DropCheckConstraint(
                name: "CK_NodeCommands_Type",
                table: "NodeCommands");

            migrationBuilder.DropColumn(
                name: "PromotedAt",
                table: "SigningKeysMetadata");

            migrationBuilder.DropColumn(
                name: "RetireAfter",
                table: "SigningKeysMetadata");

            migrationBuilder.DropColumn(
                name: "ReportedServerVersion",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "VersionReportedAt",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "PreviousVersion",
                table: "NodeCommands");

            migrationBuilder.DropColumn(
                name: "ProgressMessage",
                table: "NodeCommands");

            migrationBuilder.DropColumn(
                name: "ProgressPhase",
                table: "NodeCommands");

            migrationBuilder.DropColumn(
                name: "ProgressUpdatedAt",
                table: "NodeCommands");

            migrationBuilder.DropColumn(
                name: "TargetVersion",
                table: "NodeCommands");

            migrationBuilder.AddCheckConstraint(
                name: "CK_SigningKeysMetadata_Status",
                table: "SigningKeysMetadata",
                sql: "[Status] BETWEEN 0 AND 2");

            migrationBuilder.AddCheckConstraint(
                name: "CK_NodeCommands_Type",
                table: "NodeCommands",
                sql: "[Type] BETWEEN 0 AND 2");
        }
    }
}
