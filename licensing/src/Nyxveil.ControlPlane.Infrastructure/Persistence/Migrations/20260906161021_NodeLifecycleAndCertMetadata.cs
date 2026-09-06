using System;
using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Nyxveil.ControlPlane.Infrastructure.Persistence.Migrations
{
    /// <inheritdoc />
    public partial class NodeLifecycleAndCertMetadata : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.AddColumn<bool>(
                name: "AcmeAutoRenew",
                table: "Nodes",
                type: "bit",
                nullable: false,
                defaultValue: false);

            migrationBuilder.AddColumn<string>(
                name: "CertIssuer",
                table: "Nodes",
                type: "nvarchar(512)",
                maxLength: 512,
                nullable: true);

            migrationBuilder.AddColumn<DateTime>(
                name: "CertNotAfter",
                table: "Nodes",
                type: "datetime2",
                nullable: true);

            migrationBuilder.AddColumn<DateTime>(
                name: "CertNotBefore",
                table: "Nodes",
                type: "datetime2",
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "CertSan",
                table: "Nodes",
                type: "nvarchar(1024)",
                maxLength: 1024,
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "CertSubject",
                table: "Nodes",
                type: "nvarchar(512)",
                maxLength: 512,
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "CertThumbprint",
                table: "Nodes",
                type: "nvarchar(128)",
                maxLength: 128,
                nullable: true);

            migrationBuilder.AddColumn<DateTime>(
                name: "DeletedAt",
                table: "Nodes",
                type: "datetime2",
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "DeletedBy",
                table: "Nodes",
                type: "nvarchar(256)",
                maxLength: 256,
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "DeletionReason",
                table: "Nodes",
                type: "nvarchar(512)",
                maxLength: 512,
                nullable: true);

            migrationBuilder.AddColumn<DateTime>(
                name: "LastRenewalAttempt",
                table: "Nodes",
                type: "datetime2",
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "LastRenewalError",
                table: "Nodes",
                type: "nvarchar(512)",
                maxLength: 512,
                nullable: true);

            migrationBuilder.AddColumn<DateTime>(
                name: "LastSuccessfulRenewal",
                table: "Nodes",
                type: "datetime2",
                nullable: true);

            migrationBuilder.AddColumn<int>(
                name: "LifecycleState",
                table: "Nodes",
                type: "int",
                nullable: false,
                defaultValue: 0);

            migrationBuilder.AddColumn<DateTime>(
                name: "NextPlannedRenewal",
                table: "Nodes",
                type: "datetime2",
                nullable: true);

            migrationBuilder.AddColumn<string>(
                name: "TlsMode",
                table: "Nodes",
                type: "nvarchar(32)",
                maxLength: 32,
                nullable: true);

            migrationBuilder.AddColumn<bool>(
                name: "BridgeOk",
                table: "NodeHealth",
                type: "bit",
                nullable: true);

            migrationBuilder.AddColumn<bool>(
                name: "CpConnected",
                table: "NodeHealth",
                type: "bit",
                nullable: true);

            migrationBuilder.AddColumn<bool>(
                name: "QuicOk",
                table: "NodeHealth",
                type: "bit",
                nullable: true);

            migrationBuilder.AddColumn<bool>(
                name: "RevocationStale",
                table: "NodeHealth",
                type: "bit",
                nullable: true);

            migrationBuilder.AddColumn<bool>(
                name: "TicketKeysLoaded",
                table: "NodeHealth",
                type: "bit",
                nullable: true);

            migrationBuilder.AddColumn<bool>(
                name: "TlsOk",
                table: "NodeHealth",
                type: "bit",
                nullable: true);

            migrationBuilder.AddColumn<bool>(
                name: "TunReady",
                table: "NodeHealth",
                type: "bit",
                nullable: true);

            migrationBuilder.CreateIndex(
                name: "IX_Nodes_LifecycleState",
                table: "Nodes",
                column: "LifecycleState");

            migrationBuilder.AddCheckConstraint(
                name: "CK_Nodes_LifecycleState",
                table: "Nodes",
                sql: "[LifecycleState] BETWEEN 0 AND 2");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropIndex(
                name: "IX_Nodes_LifecycleState",
                table: "Nodes");

            migrationBuilder.DropCheckConstraint(
                name: "CK_Nodes_LifecycleState",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "AcmeAutoRenew",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "CertIssuer",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "CertNotAfter",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "CertNotBefore",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "CertSan",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "CertSubject",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "CertThumbprint",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "DeletedAt",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "DeletedBy",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "DeletionReason",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "LastRenewalAttempt",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "LastRenewalError",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "LastSuccessfulRenewal",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "LifecycleState",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "NextPlannedRenewal",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "TlsMode",
                table: "Nodes");

            migrationBuilder.DropColumn(
                name: "BridgeOk",
                table: "NodeHealth");

            migrationBuilder.DropColumn(
                name: "CpConnected",
                table: "NodeHealth");

            migrationBuilder.DropColumn(
                name: "QuicOk",
                table: "NodeHealth");

            migrationBuilder.DropColumn(
                name: "RevocationStale",
                table: "NodeHealth");

            migrationBuilder.DropColumn(
                name: "TicketKeysLoaded",
                table: "NodeHealth");

            migrationBuilder.DropColumn(
                name: "TlsOk",
                table: "NodeHealth");

            migrationBuilder.DropColumn(
                name: "TunReady",
                table: "NodeHealth");
        }
    }
}
