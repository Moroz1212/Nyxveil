using System;
using Microsoft.EntityFrameworkCore.Migrations;

#nullable disable

namespace Nyxveil.ControlPlane.Infrastructure.Persistence.Migrations
{
    /// <inheritdoc />
    public partial class InitialCreate : Migration
    {
        /// <inheritdoc />
        protected override void Up(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.CreateTable(
                name: "AspNetRoles",
                columns: table => new
                {
                    Id = table.Column<string>(type: "nvarchar(450)", nullable: false),
                    Name = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    NormalizedName = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    ConcurrencyStamp = table.Column<string>(type: "nvarchar(max)", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_AspNetRoles", x => x.Id);
                });

            migrationBuilder.CreateTable(
                name: "AspNetUsers",
                columns: table => new
                {
                    Id = table.Column<string>(type: "nvarchar(450)", nullable: false),
                    DisplayName = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    UserName = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    NormalizedUserName = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    Email = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    NormalizedEmail = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    EmailConfirmed = table.Column<bool>(type: "bit", nullable: false),
                    PasswordHash = table.Column<string>(type: "nvarchar(max)", nullable: true),
                    SecurityStamp = table.Column<string>(type: "nvarchar(max)", nullable: true),
                    ConcurrencyStamp = table.Column<string>(type: "nvarchar(max)", nullable: true),
                    PhoneNumber = table.Column<string>(type: "nvarchar(max)", nullable: true),
                    PhoneNumberConfirmed = table.Column<bool>(type: "bit", nullable: false),
                    TwoFactorEnabled = table.Column<bool>(type: "bit", nullable: false),
                    LockoutEnd = table.Column<DateTimeOffset>(type: "datetimeoffset", nullable: true),
                    LockoutEnabled = table.Column<bool>(type: "bit", nullable: false),
                    AccessFailedCount = table.Column<int>(type: "int", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_AspNetUsers", x => x.Id);
                });

            migrationBuilder.CreateTable(
                name: "AuditLog",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    Actor = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    Action = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    EntityType = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    EntityId = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    Timestamp = table.Column<DateTime>(type: "datetime2", nullable: false),
                    IpAddress = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: true),
                    DetailsJson = table.Column<string>(type: "nvarchar(max)", maxLength: 8000, nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_AuditLog", x => x.Id);
                });

            migrationBuilder.CreateTable(
                name: "BootstrapTokens",
                columns: table => new
                {
                    BootstrapId = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    Verifier = table.Column<string>(type: "nvarchar(200)", maxLength: 200, nullable: false),
                    ExpiresAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    MaxUses = table.Column<int>(type: "int", nullable: false),
                    UsedCount = table.Column<int>(type: "int", nullable: false),
                    AllowedLocation = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: true),
                    Status = table.Column<int>(type: "int", nullable: false),
                    CreatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    CreatedBy = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    Note = table.Column<string>(type: "nvarchar(1024)", maxLength: 1024, nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_BootstrapTokens", x => x.BootstrapId);
                    table.CheckConstraint("CK_BootstrapTokens_MaxUses", "[MaxUses] >= 1");
                    table.CheckConstraint("CK_BootstrapTokens_Status", "[Status] BETWEEN 0 AND 3");
                    table.CheckConstraint("CK_BootstrapTokens_UsedCount", "[UsedCount] >= 0 AND [UsedCount] <= [MaxUses]");
                });

            migrationBuilder.CreateTable(
                name: "CatalogVersions",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    Version = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: false),
                    IssuedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    ExpiresAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    KeyId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    PayloadHash = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: true),
                    CreatedAt = table.Column<DateTime>(type: "datetime2", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_CatalogVersions", x => x.Id);
                });

            migrationBuilder.CreateTable(
                name: "Locations",
                columns: table => new
                {
                    LocationId = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: false),
                    Code = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: false),
                    Country = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    City = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    DisplayName = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    Enabled = table.Column<bool>(type: "bit", nullable: false),
                    SortOrder = table.Column<int>(type: "int", nullable: false),
                    CreatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    UpdatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    CountryCode = table.Column<string>(type: "nvarchar(8)", maxLength: 8, nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_Locations", x => x.LocationId);
                    table.UniqueConstraint("AK_Locations_Code", x => x.Code);
                });

            migrationBuilder.CreateTable(
                name: "PaymentEvents",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    Provider = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: false),
                    ExternalPaymentId = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    Status = table.Column<int>(type: "int", nullable: false),
                    Amount = table.Column<decimal>(type: "decimal(18,4)", precision: 18, scale: 4, nullable: true),
                    Currency = table.Column<string>(type: "nvarchar(8)", maxLength: 8, nullable: true),
                    PayloadHash = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: true),
                    ReceivedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    ProcessedAt = table.Column<DateTime>(type: "datetime2", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_PaymentEvents", x => x.Id);
                    table.CheckConstraint("CK_PaymentEvents_Amount", "[Amount] IS NULL OR [Amount] >= 0");
                    table.CheckConstraint("CK_PaymentEvents_Status", "[Status] BETWEEN 0 AND 3");
                });

            migrationBuilder.CreateTable(
                name: "Plans",
                columns: table => new
                {
                    PlanId = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    Code = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: false),
                    Name = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    Status = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: false),
                    DurationDays = table.Column<int>(type: "int", nullable: false),
                    MaxDevices = table.Column<int>(type: "int", nullable: false),
                    AllowedLocationsPolicy = table.Column<string>(type: "nvarchar(4000)", maxLength: 4000, nullable: false),
                    Permissions = table.Column<string>(type: "nvarchar(4000)", maxLength: 4000, nullable: false),
                    CreatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    UpdatedAt = table.Column<DateTime>(type: "datetime2", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_Plans", x => x.PlanId);
                    table.CheckConstraint("CK_Plans_DurationDays", "[DurationDays] >= 0");
                    table.CheckConstraint("CK_Plans_MaxDevices", "[MaxDevices] >= 1");
                    table.CheckConstraint("CK_Plans_Status", "[Status] IN (N'Active', N'Disabled', N'Retired')");
                });

            migrationBuilder.CreateTable(
                name: "Revocations",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    Type = table.Column<int>(type: "int", nullable: false),
                    TargetId = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    Reason = table.Column<string>(type: "nvarchar(1024)", maxLength: 1024, nullable: true),
                    CreatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    CreatedBy = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    Version = table.Column<long>(type: "bigint", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_Revocations", x => x.Id);
                    table.CheckConstraint("CK_Revocations_Type", "[Type] BETWEEN 0 AND 2");
                    table.CheckConstraint("CK_Revocations_Version", "[Version] >= 1");
                });

            migrationBuilder.CreateTable(
                name: "SigningKeysMetadata",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    KeyId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    PublicKey = table.Column<byte[]>(type: "varbinary(32)", maxLength: 32, nullable: false),
                    ProtectedPrivateKey = table.Column<byte[]>(type: "varbinary(max)", nullable: false),
                    Status = table.Column<int>(type: "int", nullable: false),
                    CreatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    RetiredAt = table.Column<DateTime>(type: "datetime2", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_SigningKeysMetadata", x => x.Id);
                    table.CheckConstraint("CK_SigningKeysMetadata_PublicKeyLen", "DATALENGTH([PublicKey]) = 32");
                    table.CheckConstraint("CK_SigningKeysMetadata_Status", "[Status] BETWEEN 0 AND 2");
                });

            migrationBuilder.CreateTable(
                name: "SystemSettings",
                columns: table => new
                {
                    Key = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    Value = table.Column<string>(type: "nvarchar(max)", maxLength: 8000, nullable: false),
                    UpdatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    UpdatedBy = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_SystemSettings", x => x.Key);
                });

            migrationBuilder.CreateTable(
                name: "TicketAudits",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    TicketId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    LicenseId = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    DeviceId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    IssuedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    ExpiresAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    LocationsJson = table.Column<string>(type: "nvarchar(4000)", maxLength: 4000, nullable: false),
                    NodeScopeJson = table.Column<string>(type: "nvarchar(4000)", maxLength: 4000, nullable: false),
                    Action = table.Column<string>(type: "nvarchar(32)", maxLength: 32, nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_TicketAudits", x => x.Id);
                    table.CheckConstraint("CK_TicketAudits_Action", "[Action] IN (N'issue', N'refresh')");
                });

            migrationBuilder.CreateTable(
                name: "Users",
                columns: table => new
                {
                    UserId = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    ExternalId = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    Email = table.Column<string>(type: "nvarchar(320)", maxLength: 320, nullable: true),
                    DisplayName = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    Status = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: false),
                    CreatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    UpdatedAt = table.Column<DateTime>(type: "datetime2", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_Users", x => x.UserId);
                    table.CheckConstraint("CK_Users_Status", "[Status] IN (N'Active', N'Disabled', N'Deleted')");
                });

            migrationBuilder.CreateTable(
                name: "AspNetRoleClaims",
                columns: table => new
                {
                    Id = table.Column<int>(type: "int", nullable: false)
                        .Annotation("SqlServer:Identity", "1, 1"),
                    RoleId = table.Column<string>(type: "nvarchar(450)", nullable: false),
                    ClaimType = table.Column<string>(type: "nvarchar(max)", nullable: true),
                    ClaimValue = table.Column<string>(type: "nvarchar(max)", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_AspNetRoleClaims", x => x.Id);
                    table.ForeignKey(
                        name: "FK_AspNetRoleClaims_AspNetRoles_RoleId",
                        column: x => x.RoleId,
                        principalTable: "AspNetRoles",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "AspNetUserClaims",
                columns: table => new
                {
                    Id = table.Column<int>(type: "int", nullable: false)
                        .Annotation("SqlServer:Identity", "1, 1"),
                    UserId = table.Column<string>(type: "nvarchar(450)", nullable: false),
                    ClaimType = table.Column<string>(type: "nvarchar(max)", nullable: true),
                    ClaimValue = table.Column<string>(type: "nvarchar(max)", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_AspNetUserClaims", x => x.Id);
                    table.ForeignKey(
                        name: "FK_AspNetUserClaims_AspNetUsers_UserId",
                        column: x => x.UserId,
                        principalTable: "AspNetUsers",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "AspNetUserLogins",
                columns: table => new
                {
                    LoginProvider = table.Column<string>(type: "nvarchar(450)", nullable: false),
                    ProviderKey = table.Column<string>(type: "nvarchar(450)", nullable: false),
                    ProviderDisplayName = table.Column<string>(type: "nvarchar(max)", nullable: true),
                    UserId = table.Column<string>(type: "nvarchar(450)", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_AspNetUserLogins", x => new { x.LoginProvider, x.ProviderKey });
                    table.ForeignKey(
                        name: "FK_AspNetUserLogins_AspNetUsers_UserId",
                        column: x => x.UserId,
                        principalTable: "AspNetUsers",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "AspNetUserRoles",
                columns: table => new
                {
                    UserId = table.Column<string>(type: "nvarchar(450)", nullable: false),
                    RoleId = table.Column<string>(type: "nvarchar(450)", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_AspNetUserRoles", x => new { x.UserId, x.RoleId });
                    table.ForeignKey(
                        name: "FK_AspNetUserRoles_AspNetRoles_RoleId",
                        column: x => x.RoleId,
                        principalTable: "AspNetRoles",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "FK_AspNetUserRoles_AspNetUsers_UserId",
                        column: x => x.UserId,
                        principalTable: "AspNetUsers",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "AspNetUserTokens",
                columns: table => new
                {
                    UserId = table.Column<string>(type: "nvarchar(450)", nullable: false),
                    LoginProvider = table.Column<string>(type: "nvarchar(450)", nullable: false),
                    Name = table.Column<string>(type: "nvarchar(450)", nullable: false),
                    Value = table.Column<string>(type: "nvarchar(max)", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_AspNetUserTokens", x => new { x.UserId, x.LoginProvider, x.Name });
                    table.ForeignKey(
                        name: "FK_AspNetUserTokens_AspNetUsers_UserId",
                        column: x => x.UserId,
                        principalTable: "AspNetUsers",
                        principalColumn: "Id",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "Nodes",
                columns: table => new
                {
                    NodeId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    LocationId = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: false),
                    DisplayName = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    Status = table.Column<int>(type: "int", nullable: false),
                    Enabled = table.Column<bool>(type: "bit", nullable: false),
                    TestOnly = table.Column<bool>(type: "bit", nullable: false),
                    Draining = table.Column<bool>(type: "bit", nullable: false),
                    ProtocolVersion = table.Column<int>(type: "int", nullable: false),
                    ServerVersion = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: true),
                    ServerName = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    SpkiPin = table.Column<byte[]>(type: "varbinary(32)", maxLength: 32, nullable: true),
                    PublicIdentity = table.Column<byte[]>(type: "varbinary(32)", maxLength: 32, nullable: false),
                    Capacity = table.Column<int>(type: "int", nullable: false),
                    CurrentSessions = table.Column<int>(type: "int", nullable: false),
                    HealthStatus = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: true),
                    LastSeenAt = table.Column<DateTime>(type: "datetime2", nullable: true),
                    CreatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    UpdatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    ConfigVersion = table.Column<long>(type: "bigint", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_Nodes", x => x.NodeId);
                    table.CheckConstraint("CK_Nodes_Capacity", "[Capacity] >= 0");
                    table.CheckConstraint("CK_Nodes_CurrentSessions", "[CurrentSessions] >= 0");
                    table.CheckConstraint("CK_Nodes_ProtocolVersion", "[ProtocolVersion] >= 0 AND [ProtocolVersion] <= 65535");
                    table.CheckConstraint("CK_Nodes_PublicIdentityLen", "DATALENGTH([PublicIdentity]) = 32");
                    table.CheckConstraint("CK_Nodes_Status", "[Status] BETWEEN 0 AND 3");
                    table.ForeignKey(
                        name: "FK_Nodes_Locations_LocationId",
                        column: x => x.LocationId,
                        principalTable: "Locations",
                        principalColumn: "LocationId",
                        onDelete: ReferentialAction.Restrict);
                });

            migrationBuilder.CreateTable(
                name: "Licenses",
                columns: table => new
                {
                    LicenseId = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    UserId = table.Column<Guid>(type: "uniqueidentifier", nullable: true),
                    LicenseKeyVerifier = table.Column<string>(type: "nvarchar(200)", maxLength: 200, nullable: false),
                    Role = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: false),
                    PlanId = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    Status = table.Column<int>(type: "int", nullable: false),
                    CreatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    ActivatedAt = table.Column<DateTime>(type: "datetime2", nullable: true),
                    ExpiresAt = table.Column<DateTime>(type: "datetime2", nullable: true),
                    MaxDevices = table.Column<int>(type: "int", nullable: false),
                    Note = table.Column<string>(type: "nvarchar(1024)", maxLength: 1024, nullable: true),
                    ExternalPaymentId = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    CreatedBy = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    UpdatedAt = table.Column<DateTime>(type: "datetime2", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_Licenses", x => x.LicenseId);
                    table.CheckConstraint("CK_Licenses_MaxDevices", "[MaxDevices] >= 1");
                    table.CheckConstraint("CK_Licenses_Status", "[Status] BETWEEN 0 AND 4");
                    table.ForeignKey(
                        name: "FK_Licenses_Plans_PlanId",
                        column: x => x.PlanId,
                        principalTable: "Plans",
                        principalColumn: "PlanId",
                        onDelete: ReferentialAction.Restrict);
                    table.ForeignKey(
                        name: "FK_Licenses_Users_UserId",
                        column: x => x.UserId,
                        principalTable: "Users",
                        principalColumn: "UserId",
                        onDelete: ReferentialAction.SetNull);
                });

            migrationBuilder.CreateTable(
                name: "NodeConfigs",
                columns: table => new
                {
                    NodeId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    Enabled = table.Column<bool>(type: "bit", nullable: false),
                    Draining = table.Column<bool>(type: "bit", nullable: false),
                    MaintenanceMode = table.Column<bool>(type: "bit", nullable: false),
                    TransportPolicyJson = table.Column<string>(type: "nvarchar(max)", maxLength: 8000, nullable: false),
                    EchPolicyJson = table.Column<string>(type: "nvarchar(max)", maxLength: 8000, nullable: true),
                    Mtu = table.Column<int>(type: "int", nullable: true),
                    Capacity = table.Column<int>(type: "int", nullable: false),
                    MinimumServerVersion = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: true),
                    MinimumProtocolVersion = table.Column<int>(type: "int", nullable: true),
                    ConfigVersion = table.Column<long>(type: "bigint", nullable: false),
                    UpdatedAt = table.Column<DateTime>(type: "datetime2", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_NodeConfigs", x => x.NodeId);
                    table.CheckConstraint("CK_NodeConfigs_Capacity", "[Capacity] >= 0");
                    table.CheckConstraint("CK_NodeConfigs_ConfigVersion", "[ConfigVersion] >= 1");
                    table.CheckConstraint("CK_NodeConfigs_MinimumProtocolVersion", "[MinimumProtocolVersion] IS NULL OR ([MinimumProtocolVersion] >= 0 AND [MinimumProtocolVersion] <= 65535)");
                    table.CheckConstraint("CK_NodeConfigs_Mtu", "[Mtu] IS NULL OR ([Mtu] >= 576 AND [Mtu] <= 9000)");
                    table.ForeignKey(
                        name: "FK_NodeConfigs_Nodes_NodeId",
                        column: x => x.NodeId,
                        principalTable: "Nodes",
                        principalColumn: "NodeId",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "NodeCredentials",
                columns: table => new
                {
                    NodeId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    PublicKey = table.Column<byte[]>(type: "varbinary(32)", maxLength: 32, nullable: false),
                    CredentialIssuedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    NodeAuthSecretVerifier = table.Column<string>(type: "nvarchar(200)", maxLength: 200, nullable: true),
                    LastAuthAt = table.Column<DateTime>(type: "datetime2", nullable: true),
                    LastCoreTokenUnix = table.Column<long>(type: "bigint", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_NodeCredentials", x => x.NodeId);
                    table.CheckConstraint("CK_NodeCredentials_PublicKeyLen", "DATALENGTH([PublicKey]) = 32");
                    table.ForeignKey(
                        name: "FK_NodeCredentials_Nodes_NodeId",
                        column: x => x.NodeId,
                        principalTable: "Nodes",
                        principalColumn: "NodeId",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "NodeEndpoints",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    NodeId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    Host = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: false),
                    Port = table.Column<int>(type: "int", nullable: false),
                    AddressFamily = table.Column<string>(type: "nvarchar(16)", maxLength: 16, nullable: false),
                    Priority = table.Column<int>(type: "int", nullable: false),
                    Enabled = table.Column<bool>(type: "bit", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_NodeEndpoints", x => x.Id);
                    table.CheckConstraint("CK_NodeEndpoints_AddressFamily", "[AddressFamily] IN (N'ipv4', N'ipv6', N'hostname')");
                    table.CheckConstraint("CK_NodeEndpoints_Port", "[Port] >= 1 AND [Port] <= 65535");
                    table.ForeignKey(
                        name: "FK_NodeEndpoints_Nodes_NodeId",
                        column: x => x.NodeId,
                        principalTable: "Nodes",
                        principalColumn: "NodeId",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "NodeHealth",
                columns: table => new
                {
                    NodeId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    CpuPercent = table.Column<double>(type: "float", nullable: false),
                    MemoryPercent = table.Column<double>(type: "float", nullable: false),
                    MemoryBytes = table.Column<long>(type: "bigint", nullable: true),
                    UptimeSeconds = table.Column<long>(type: "bigint", nullable: true),
                    ActiveSessions = table.Column<int>(type: "int", nullable: false),
                    NetworkRxRate = table.Column<double>(type: "float", nullable: true),
                    NetworkTxRate = table.Column<double>(type: "float", nullable: true),
                    LoadAverage = table.Column<double>(type: "float", nullable: true),
                    Healthy = table.Column<bool>(type: "bit", nullable: false),
                    UpdatedAt = table.Column<DateTime>(type: "datetime2", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_NodeHealth", x => x.NodeId);
                    table.CheckConstraint("CK_NodeHealth_ActiveSessions", "[ActiveSessions] >= 0");
                    table.CheckConstraint("CK_NodeHealth_CpuPercent", "[CpuPercent] >= 0 AND [CpuPercent] <= 100");
                    table.CheckConstraint("CK_NodeHealth_MemoryPercent", "[MemoryPercent] >= 0 AND [MemoryPercent] <= 100");
                    table.ForeignKey(
                        name: "FK_NodeHealth_Nodes_NodeId",
                        column: x => x.NodeId,
                        principalTable: "Nodes",
                        principalColumn: "NodeId",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "NodeMetrics",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    NodeId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    Timestamp = table.Column<DateTime>(type: "datetime2", nullable: false),
                    CpuPercent = table.Column<double>(type: "float", nullable: false),
                    MemoryPercent = table.Column<double>(type: "float", nullable: false),
                    MemoryBytes = table.Column<long>(type: "bigint", nullable: true),
                    ActiveSessions = table.Column<int>(type: "int", nullable: false),
                    NetworkRxRate = table.Column<double>(type: "float", nullable: true),
                    NetworkTxRate = table.Column<double>(type: "float", nullable: true),
                    UptimeSeconds = table.Column<long>(type: "bigint", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_NodeMetrics", x => x.Id);
                    table.CheckConstraint("CK_NodeMetrics_ActiveSessions", "[ActiveSessions] >= 0");
                    table.ForeignKey(
                        name: "FK_NodeMetrics_Nodes_NodeId",
                        column: x => x.NodeId,
                        principalTable: "Nodes",
                        principalColumn: "NodeId",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "NodeTransports",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    NodeId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    TransportType = table.Column<string>(type: "nvarchar(16)", maxLength: 16, nullable: false),
                    Enabled = table.Column<bool>(type: "bit", nullable: false),
                    Priority = table.Column<int>(type: "int", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_NodeTransports", x => x.Id);
                    table.CheckConstraint("CK_NodeTransports_TransportType", "[TransportType] IN (N'tls', N'quic')");
                    table.ForeignKey(
                        name: "FK_NodeTransports_Nodes_NodeId",
                        column: x => x.NodeId,
                        principalTable: "Nodes",
                        principalColumn: "NodeId",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "Devices",
                columns: table => new
                {
                    Id = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    ClientDeviceId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    LicenseId = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    PublicKey = table.Column<byte[]>(type: "varbinary(64)", maxLength: 64, nullable: false),
                    Platform = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: true),
                    DeviceName = table.Column<string>(type: "nvarchar(256)", maxLength: 256, nullable: true),
                    Status = table.Column<int>(type: "int", nullable: false),
                    CreatedAt = table.Column<DateTime>(type: "datetime2", nullable: false),
                    LastSeenAt = table.Column<DateTime>(type: "datetime2", nullable: true),
                    RevokedAt = table.Column<DateTime>(type: "datetime2", nullable: true)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_Devices", x => x.Id);
                    table.CheckConstraint("CK_Devices_PublicKeyLen", "DATALENGTH([PublicKey]) >= 32 AND DATALENGTH([PublicKey]) <= 64");
                    table.CheckConstraint("CK_Devices_Status", "[Status] BETWEEN 0 AND 2");
                    table.ForeignKey(
                        name: "FK_Devices_Licenses_LicenseId",
                        column: x => x.LicenseId,
                        principalTable: "Licenses",
                        principalColumn: "LicenseId",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateTable(
                name: "LicenseAllowedLocations",
                columns: table => new
                {
                    LicenseId = table.Column<Guid>(type: "uniqueidentifier", nullable: false),
                    LocationId = table.Column<string>(type: "nvarchar(64)", maxLength: 64, nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_LicenseAllowedLocations", x => new { x.LicenseId, x.LocationId });
                    table.ForeignKey(
                        name: "FK_LicenseAllowedLocations_Licenses_LicenseId",
                        column: x => x.LicenseId,
                        principalTable: "Licenses",
                        principalColumn: "LicenseId",
                        onDelete: ReferentialAction.Cascade);
                    table.ForeignKey(
                        name: "FK_LicenseAllowedLocations_Locations_LocationId",
                        column: x => x.LocationId,
                        principalTable: "Locations",
                        principalColumn: "LocationId",
                        onDelete: ReferentialAction.Restrict);
                });

            migrationBuilder.CreateIndex(
                name: "IX_AspNetRoleClaims_RoleId",
                table: "AspNetRoleClaims",
                column: "RoleId");

            migrationBuilder.CreateIndex(
                name: "RoleNameIndex",
                table: "AspNetRoles",
                column: "NormalizedName",
                unique: true,
                filter: "[NormalizedName] IS NOT NULL");

            migrationBuilder.CreateIndex(
                name: "IX_AspNetUserClaims_UserId",
                table: "AspNetUserClaims",
                column: "UserId");

            migrationBuilder.CreateIndex(
                name: "IX_AspNetUserLogins_UserId",
                table: "AspNetUserLogins",
                column: "UserId");

            migrationBuilder.CreateIndex(
                name: "IX_AspNetUserRoles_RoleId",
                table: "AspNetUserRoles",
                column: "RoleId");

            migrationBuilder.CreateIndex(
                name: "EmailIndex",
                table: "AspNetUsers",
                column: "NormalizedEmail");

            migrationBuilder.CreateIndex(
                name: "UserNameIndex",
                table: "AspNetUsers",
                column: "NormalizedUserName",
                unique: true,
                filter: "[NormalizedUserName] IS NOT NULL");

            migrationBuilder.CreateIndex(
                name: "IX_AuditLog_EntityType_EntityId",
                table: "AuditLog",
                columns: new[] { "EntityType", "EntityId" });

            migrationBuilder.CreateIndex(
                name: "IX_AuditLog_Timestamp",
                table: "AuditLog",
                column: "Timestamp");

            migrationBuilder.CreateIndex(
                name: "IX_BootstrapTokens_Status_ExpiresAt",
                table: "BootstrapTokens",
                columns: new[] { "Status", "ExpiresAt" });

            migrationBuilder.CreateIndex(
                name: "IX_BootstrapTokens_Verifier",
                table: "BootstrapTokens",
                column: "Verifier",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_CatalogVersions_IssuedAt",
                table: "CatalogVersions",
                column: "IssuedAt");

            migrationBuilder.CreateIndex(
                name: "IX_CatalogVersions_Version",
                table: "CatalogVersions",
                column: "Version",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_Devices_LicenseId",
                table: "Devices",
                column: "LicenseId");

            migrationBuilder.CreateIndex(
                name: "IX_Devices_LicenseId_ClientDeviceId",
                table: "Devices",
                columns: new[] { "LicenseId", "ClientDeviceId" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_Devices_Status",
                table: "Devices",
                column: "Status");

            migrationBuilder.CreateIndex(
                name: "IX_LicenseAllowedLocations_LocationId",
                table: "LicenseAllowedLocations",
                column: "LocationId");

            migrationBuilder.CreateIndex(
                name: "IX_Licenses_LicenseKeyVerifier",
                table: "Licenses",
                column: "LicenseKeyVerifier",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_Licenses_PlanId",
                table: "Licenses",
                column: "PlanId");

            migrationBuilder.CreateIndex(
                name: "IX_Licenses_Status_ExpiresAt",
                table: "Licenses",
                columns: new[] { "Status", "ExpiresAt" });

            migrationBuilder.CreateIndex(
                name: "IX_Licenses_UserId",
                table: "Licenses",
                column: "UserId",
                filter: "[UserId] IS NOT NULL");

            migrationBuilder.CreateIndex(
                name: "IX_Locations_Code",
                table: "Locations",
                column: "Code",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_Locations_Enabled_SortOrder",
                table: "Locations",
                columns: new[] { "Enabled", "SortOrder" });

            migrationBuilder.CreateIndex(
                name: "IX_NodeEndpoints_NodeId",
                table: "NodeEndpoints",
                column: "NodeId");

            migrationBuilder.CreateIndex(
                name: "IX_NodeHealth_Healthy",
                table: "NodeHealth",
                column: "Healthy");

            migrationBuilder.CreateIndex(
                name: "IX_NodeHealth_UpdatedAt",
                table: "NodeHealth",
                column: "UpdatedAt");

            migrationBuilder.CreateIndex(
                name: "IX_NodeMetrics_NodeId_Timestamp",
                table: "NodeMetrics",
                columns: new[] { "NodeId", "Timestamp" });

            migrationBuilder.CreateIndex(
                name: "IX_Nodes_Enabled_TestOnly",
                table: "Nodes",
                columns: new[] { "Enabled", "TestOnly" });

            migrationBuilder.CreateIndex(
                name: "IX_Nodes_LocationId",
                table: "Nodes",
                column: "LocationId");

            migrationBuilder.CreateIndex(
                name: "IX_Nodes_Status",
                table: "Nodes",
                column: "Status");

            migrationBuilder.CreateIndex(
                name: "IX_NodeTransports_NodeId",
                table: "NodeTransports",
                column: "NodeId");

            migrationBuilder.CreateIndex(
                name: "IX_PaymentEvents_Provider_ExternalPaymentId",
                table: "PaymentEvents",
                columns: new[] { "Provider", "ExternalPaymentId" },
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_PaymentEvents_Status_ReceivedAt",
                table: "PaymentEvents",
                columns: new[] { "Status", "ReceivedAt" });

            migrationBuilder.CreateIndex(
                name: "IX_Plans_Code",
                table: "Plans",
                column: "Code",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_Revocations_TargetId",
                table: "Revocations",
                column: "TargetId");

            migrationBuilder.CreateIndex(
                name: "IX_Revocations_Type_TargetId",
                table: "Revocations",
                columns: new[] { "Type", "TargetId" });

            migrationBuilder.CreateIndex(
                name: "IX_Revocations_Version",
                table: "Revocations",
                column: "Version");

            migrationBuilder.CreateIndex(
                name: "IX_SigningKeysMetadata_KeyId",
                table: "SigningKeysMetadata",
                column: "KeyId",
                unique: true);

            migrationBuilder.CreateIndex(
                name: "IX_SigningKeysMetadata_Status",
                table: "SigningKeysMetadata",
                column: "Status");

            migrationBuilder.CreateIndex(
                name: "IX_TicketAudits_LicenseId_IssuedAt",
                table: "TicketAudits",
                columns: new[] { "LicenseId", "IssuedAt" });

            migrationBuilder.CreateIndex(
                name: "IX_TicketAudits_TicketId",
                table: "TicketAudits",
                column: "TicketId");

            migrationBuilder.CreateIndex(
                name: "IX_Users_Email",
                table: "Users",
                column: "Email",
                filter: "[Email] IS NOT NULL");

            migrationBuilder.CreateIndex(
                name: "IX_Users_ExternalId",
                table: "Users",
                column: "ExternalId",
                filter: "[ExternalId] IS NOT NULL");
            migrationBuilder.CreateTable(
                name: "NodeRequestNonces",
                columns: table => new
                {
                    NodeId = table.Column<string>(type: "nvarchar(128)", maxLength: 128, nullable: false),
                    NonceHash = table.Column<string>(type: "varchar(64)", unicode: false, maxLength: 64, nullable: false),
                    Timestamp = table.Column<DateTime>(type: "datetime2", nullable: false),
                    ExpiresAt = table.Column<DateTime>(type: "datetime2", nullable: false)
                },
                constraints: table =>
                {
                    table.PrimaryKey("PK_NodeRequestNonces", x => new { x.NodeId, x.NonceHash });
                    table.ForeignKey(
                        name: "FK_NodeRequestNonces_Nodes_NodeId",
                        column: x => x.NodeId,
                        principalTable: "Nodes",
                        principalColumn: "NodeId",
                        onDelete: ReferentialAction.Cascade);
                });

            migrationBuilder.CreateIndex(
                name: "IX_NodeRequestNonces_ExpiresAt",
                table: "NodeRequestNonces",
                column: "ExpiresAt");
        }

        /// <inheritdoc />
        protected override void Down(MigrationBuilder migrationBuilder)
        {
            migrationBuilder.DropTable(
                name: "NodeRequestNonces");
            migrationBuilder.DropTable(
                name: "AspNetRoleClaims");

            migrationBuilder.DropTable(
                name: "AspNetUserClaims");

            migrationBuilder.DropTable(
                name: "AspNetUserLogins");

            migrationBuilder.DropTable(
                name: "AspNetUserRoles");

            migrationBuilder.DropTable(
                name: "AspNetUserTokens");

            migrationBuilder.DropTable(
                name: "AuditLog");

            migrationBuilder.DropTable(
                name: "BootstrapTokens");

            migrationBuilder.DropTable(
                name: "CatalogVersions");

            migrationBuilder.DropTable(
                name: "Devices");

            migrationBuilder.DropTable(
                name: "LicenseAllowedLocations");

            migrationBuilder.DropTable(
                name: "NodeConfigs");

            migrationBuilder.DropTable(
                name: "NodeCredentials");

            migrationBuilder.DropTable(
                name: "NodeEndpoints");

            migrationBuilder.DropTable(
                name: "NodeHealth");

            migrationBuilder.DropTable(
                name: "NodeMetrics");

            migrationBuilder.DropTable(
                name: "NodeTransports");

            migrationBuilder.DropTable(
                name: "PaymentEvents");

            migrationBuilder.DropTable(
                name: "Revocations");

            migrationBuilder.DropTable(
                name: "SigningKeysMetadata");

            migrationBuilder.DropTable(
                name: "SystemSettings");

            migrationBuilder.DropTable(
                name: "TicketAudits");

            migrationBuilder.DropTable(
                name: "AspNetRoles");

            migrationBuilder.DropTable(
                name: "AspNetUsers");

            migrationBuilder.DropTable(
                name: "Licenses");

            migrationBuilder.DropTable(
                name: "Nodes");

            migrationBuilder.DropTable(
                name: "Plans");

            migrationBuilder.DropTable(
                name: "Users");

            migrationBuilder.DropTable(
                name: "Locations");
        }
    }
}
