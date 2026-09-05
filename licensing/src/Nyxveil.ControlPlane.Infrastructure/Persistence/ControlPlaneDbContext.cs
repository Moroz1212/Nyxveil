using Microsoft.AspNetCore.Identity.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Infrastructure.Identity;

namespace Nyxveil.ControlPlane.Infrastructure.Persistence;

public class ControlPlaneDbContext : IdentityDbContext<ApplicationUser>
{
    public ControlPlaneDbContext(DbContextOptions<ControlPlaneDbContext> options)
        : base(options)
    {
    }

    public DbSet<UserAccount> UserAccounts => Set<UserAccount>();
    public DbSet<Plan> Plans => Set<Plan>();
    public DbSet<License> Licenses => Set<License>();
    public DbSet<LicenseAllowedLocation> LicenseAllowedLocations => Set<LicenseAllowedLocation>();
    public DbSet<Device> Devices => Set<Device>();
    public DbSet<Location> Locations => Set<Location>();
    public DbSet<Node> Nodes => Set<Node>();
    public DbSet<NodeEndpoint> NodeEndpoints => Set<NodeEndpoint>();
    public DbSet<NodeTransport> NodeTransports => Set<NodeTransport>();
    public DbSet<NodeHealth> NodeHealth => Set<NodeHealth>();
    public DbSet<NodeMetric> NodeMetrics => Set<NodeMetric>();
    public DbSet<NodeCredential> NodeCredentials => Set<NodeCredential>();
    public DbSet<NodeConfig> NodeConfigs => Set<NodeConfig>();
    public DbSet<BootstrapToken> BootstrapTokens => Set<BootstrapToken>();
    public DbSet<TicketAudit> TicketAudits => Set<TicketAudit>();
    public DbSet<Revocation> Revocations => Set<Revocation>();
    public DbSet<CatalogVersion> CatalogVersions => Set<CatalogVersion>();
    public DbSet<SigningKeyMetadata> SigningKeysMetadata => Set<SigningKeyMetadata>();
    public DbSet<AuditLogEntry> AuditLog => Set<AuditLogEntry>();
    public DbSet<SystemSetting> SystemSettings => Set<SystemSetting>();
    public DbSet<PaymentEvent> PaymentEvents => Set<PaymentEvent>();

    protected override void OnModelCreating(ModelBuilder builder)
    {
        base.OnModelCreating(builder);

        builder.Entity<ApplicationUser>(e =>
        {
            e.Property(x => x.DisplayName).HasMaxLength(256);
        });

        builder.Entity<UserAccount>(e =>
        {
            e.ToTable("Users");
            e.HasKey(x => x.UserId);
            e.Property(x => x.ExternalId).HasMaxLength(256);
            e.Property(x => x.Email).HasMaxLength(320);
            e.Property(x => x.DisplayName).HasMaxLength(256);
            e.Property(x => x.Status).HasMaxLength(64).IsRequired();
            e.HasIndex(x => x.Email).HasFilter("[Email] IS NOT NULL");
            e.HasIndex(x => x.ExternalId).HasFilter("[ExternalId] IS NOT NULL");
            e.ToTable(t => t.HasCheckConstraint("CK_Users_Status", "[Status] IN (N'Active', N'Disabled', N'Deleted')"));
        });

        builder.Entity<Plan>(e =>
        {
            e.ToTable("Plans");
            e.HasKey(x => x.PlanId);
            e.Property(x => x.Code).HasMaxLength(64).IsRequired();
            e.Property(x => x.Name).HasMaxLength(128).IsRequired();
            e.Property(x => x.Status).HasMaxLength(64).IsRequired();
            e.Property(x => x.AllowedLocationsPolicy).HasMaxLength(4000).IsRequired();
            e.Property(x => x.Permissions).HasMaxLength(4000).IsRequired();
            e.HasIndex(x => x.Code).IsUnique();
            e.ToTable(t =>
            {
                t.HasCheckConstraint("CK_Plans_DurationDays", "[DurationDays] >= 0");
                t.HasCheckConstraint("CK_Plans_MaxDevices", "[MaxDevices] >= 1");
                t.HasCheckConstraint("CK_Plans_Status", "[Status] IN (N'Active', N'Disabled', N'Retired')");
            });
        });

        builder.Entity<License>(e =>
        {
            e.ToTable("Licenses");
            e.HasKey(x => x.LicenseId);
            e.Property(x => x.LicenseKeyVerifier).HasMaxLength(200).IsRequired();
            e.Property(x => x.Role).HasMaxLength(64).IsRequired();
            e.Property(x => x.Note).HasMaxLength(1024);
            e.Property(x => x.ExternalPaymentId).HasMaxLength(256);
            e.Property(x => x.CreatedBy).HasMaxLength(256).IsRequired();
            e.HasIndex(x => x.LicenseKeyVerifier).IsUnique();
            e.HasIndex(x => new { x.Status, x.ExpiresAt });
            e.HasIndex(x => x.UserId).HasFilter("[UserId] IS NOT NULL");
            e.HasIndex(x => x.PlanId);
            e.HasOne(x => x.Plan).WithMany().HasForeignKey(x => x.PlanId).OnDelete(DeleteBehavior.Restrict);
            e.HasOne<UserAccount>().WithMany().HasForeignKey(x => x.UserId).OnDelete(DeleteBehavior.SetNull);
            e.ToTable(t =>
            {
                t.HasCheckConstraint("CK_Licenses_MaxDevices", "[MaxDevices] >= 1");
                t.HasCheckConstraint("CK_Licenses_Status", "[Status] BETWEEN 0 AND 4");
            });
        });

        builder.Entity<LicenseAllowedLocation>(e =>
        {
            e.ToTable("LicenseAllowedLocations");
            e.HasKey(x => new { x.LicenseId, x.LocationId });
            e.Property(x => x.LocationId).HasMaxLength(64).IsRequired();
            e.HasIndex(x => x.LocationId);
            e.HasOne(x => x.License).WithMany(l => l.AllowedLocations).HasForeignKey(x => x.LicenseId)
                .OnDelete(DeleteBehavior.Cascade);
            e.HasOne<Location>().WithMany().HasForeignKey(x => x.LocationId)
                .OnDelete(DeleteBehavior.Restrict);
        });

        builder.Entity<Device>(e =>
        {
            e.ToTable("Devices");
            e.HasKey(x => x.Id);
            e.Property(x => x.ClientDeviceId).HasMaxLength(128).IsRequired();
            e.Property(x => x.PublicKey).HasMaxLength(64).IsRequired();
            e.Property(x => x.Platform).HasMaxLength(64);
            e.Property(x => x.DeviceName).HasMaxLength(256);
            e.HasIndex(x => new { x.LicenseId, x.ClientDeviceId }).IsUnique();
            e.HasIndex(x => x.LicenseId);
            e.HasIndex(x => x.Status);
            e.HasOne(x => x.License).WithMany(l => l.Devices).HasForeignKey(x => x.LicenseId)
                .OnDelete(DeleteBehavior.Cascade);
            e.ToTable(t =>
            {
                t.HasCheckConstraint("CK_Devices_Status", "[Status] BETWEEN 0 AND 2");
                t.HasCheckConstraint("CK_Devices_PublicKeyLen", "DATALENGTH([PublicKey]) >= 32 AND DATALENGTH([PublicKey]) <= 64");
            });
        });

        builder.Entity<Location>(e =>
        {
            e.ToTable("Locations");
            e.HasKey(x => x.LocationId);
            e.Property(x => x.LocationId).HasMaxLength(64);
            e.Property(x => x.Code).HasMaxLength(64).IsRequired();
            e.Property(x => x.Country).HasMaxLength(128).IsRequired();
            e.Property(x => x.City).HasMaxLength(128).IsRequired();
            e.Property(x => x.DisplayName).HasMaxLength(256).IsRequired();
            e.Property(x => x.CountryCode).HasMaxLength(8);
            e.HasIndex(x => x.Code).IsUnique();
            e.HasIndex(x => new { x.Enabled, x.SortOrder });
        });

        builder.Entity<Node>(e =>
        {
            e.ToTable("Nodes");
            e.HasKey(x => x.NodeId);
            e.Property(x => x.NodeId).HasMaxLength(128);
            e.Property(x => x.LocationId).HasMaxLength(64).IsRequired();
            e.Property(x => x.DisplayName).HasMaxLength(256).IsRequired();
            e.Property(x => x.ServerVersion).HasMaxLength(64);
            e.Property(x => x.ServerName).HasMaxLength(256);
            e.Property(x => x.HealthStatus).HasMaxLength(64);
            e.Property(x => x.SpkiPin).HasMaxLength(32);
            e.Property(x => x.PublicIdentity).HasMaxLength(32).IsRequired();
            e.HasIndex(x => x.LocationId);
            e.HasIndex(x => x.Status);
            e.HasIndex(x => new { x.Enabled, x.TestOnly });
            e.HasOne(x => x.Location).WithMany().HasForeignKey(x => x.LocationId).OnDelete(DeleteBehavior.Restrict);
            e.ToTable(t =>
            {
                t.HasCheckConstraint("CK_Nodes_Status", "[Status] BETWEEN 0 AND 3");
                t.HasCheckConstraint("CK_Nodes_ProtocolVersion", "[ProtocolVersion] >= 0 AND [ProtocolVersion] <= 65535");
                t.HasCheckConstraint("CK_Nodes_Capacity", "[Capacity] >= 0");
                t.HasCheckConstraint("CK_Nodes_CurrentSessions", "[CurrentSessions] >= 0");
                t.HasCheckConstraint("CK_Nodes_PublicIdentityLen", "DATALENGTH([PublicIdentity]) = 32");
            });
        });

        builder.Entity<NodeEndpoint>(e =>
        {
            e.ToTable("NodeEndpoints");
            e.HasKey(x => x.Id);
            e.Property(x => x.NodeId).HasMaxLength(128).IsRequired();
            e.Property(x => x.Host).HasMaxLength(256).IsRequired();
            e.Property(x => x.AddressFamily).HasMaxLength(16).IsRequired();
            e.HasIndex(x => x.NodeId);
            e.HasOne(x => x.Node).WithMany(n => n.Endpoints).HasForeignKey(x => x.NodeId)
                .OnDelete(DeleteBehavior.Cascade);
            e.ToTable(t =>
            {
                t.HasCheckConstraint("CK_NodeEndpoints_Port", "[Port] >= 1 AND [Port] <= 65535");
                t.HasCheckConstraint("CK_NodeEndpoints_AddressFamily", "[AddressFamily] IN (N'ipv4', N'ipv6', N'hostname')");
            });
        });

        builder.Entity<NodeTransport>(e =>
        {
            e.ToTable("NodeTransports");
            e.HasKey(x => x.Id);
            e.Property(x => x.NodeId).HasMaxLength(128).IsRequired();
            e.Property(x => x.TransportType).HasMaxLength(16).IsRequired();
            e.HasIndex(x => x.NodeId);
            e.HasOne(x => x.Node).WithMany(n => n.Transports).HasForeignKey(x => x.NodeId)
                .OnDelete(DeleteBehavior.Cascade);
            e.ToTable(t => t.HasCheckConstraint("CK_NodeTransports_TransportType", "[TransportType] IN (N'tls', N'quic')"));
        });

        builder.Entity<NodeHealth>(e =>
        {
            e.ToTable("NodeHealth");
            e.HasKey(x => x.NodeId);
            e.Property(x => x.NodeId).HasMaxLength(128);
            e.HasIndex(x => x.UpdatedAt);
            e.HasIndex(x => x.Healthy);
            e.HasOne(x => x.Node).WithOne().HasForeignKey<NodeHealth>(x => x.NodeId)
                .OnDelete(DeleteBehavior.Cascade);
            e.ToTable(t =>
            {
                t.HasCheckConstraint("CK_NodeHealth_CpuPercent", "[CpuPercent] >= 0 AND [CpuPercent] <= 100");
                t.HasCheckConstraint("CK_NodeHealth_MemoryPercent", "[MemoryPercent] >= 0 AND [MemoryPercent] <= 100");
                t.HasCheckConstraint("CK_NodeHealth_ActiveSessions", "[ActiveSessions] >= 0");
            });
        });

        builder.Entity<NodeMetric>(e =>
        {
            e.ToTable("NodeMetrics");
            e.HasKey(x => x.Id);
            e.Property(x => x.NodeId).HasMaxLength(128).IsRequired();
            e.Property(x => x.Timestamp).HasColumnName("Timestamp");
            e.HasIndex(x => new { x.NodeId, x.Timestamp });
            e.HasOne(x => x.Node).WithMany().HasForeignKey(x => x.NodeId).OnDelete(DeleteBehavior.Cascade);
            e.ToTable(t => t.HasCheckConstraint("CK_NodeMetrics_ActiveSessions", "[ActiveSessions] >= 0"));
        });

        builder.Entity<NodeCredential>(e =>
        {
            e.ToTable("NodeCredentials");
            e.HasKey(x => x.NodeId);
            e.Property(x => x.NodeId).HasMaxLength(128);
            e.Property(x => x.PublicKey).HasMaxLength(32).IsRequired();
            e.Property(x => x.NodeAuthSecretVerifier).HasMaxLength(200);
            e.Property(x => x.LastCoreTokenUnix);
            e.HasOne(x => x.Node).WithOne().HasForeignKey<NodeCredential>(x => x.NodeId)
                .OnDelete(DeleteBehavior.Cascade);
            e.ToTable(t => t.HasCheckConstraint("CK_NodeCredentials_PublicKeyLen", "DATALENGTH([PublicKey]) = 32"));
        });

        builder.Entity<NodeConfig>(e =>
        {
            e.ToTable("NodeConfigs");
            e.HasKey(x => x.NodeId);
            e.Property(x => x.NodeId).HasMaxLength(128);
            e.Property(x => x.TransportPolicyJson).IsRequired();
            e.Property(x => x.MinimumServerVersion).HasMaxLength(64);
            e.HasOne(x => x.Node).WithOne().HasForeignKey<NodeConfig>(x => x.NodeId)
                .OnDelete(DeleteBehavior.Cascade);
            e.ToTable(t =>
            {
                t.HasCheckConstraint("CK_NodeConfigs_Capacity", "[Capacity] >= 0");
                t.HasCheckConstraint("CK_NodeConfigs_ConfigVersion", "[ConfigVersion] >= 1");
                t.HasCheckConstraint("CK_NodeConfigs_Mtu", "[Mtu] IS NULL OR ([Mtu] >= 576 AND [Mtu] <= 9000)");
                t.HasCheckConstraint("CK_NodeConfigs_MinimumProtocolVersion",
                    "[MinimumProtocolVersion] IS NULL OR ([MinimumProtocolVersion] >= 0 AND [MinimumProtocolVersion] <= 65535)");
            });
        });

        builder.Entity<BootstrapToken>(e =>
        {
            e.ToTable("BootstrapTokens");
            e.HasKey(x => x.BootstrapId);
            e.Property(x => x.Verifier).HasMaxLength(200).IsRequired();
            e.Property(x => x.AllowedLocation).HasMaxLength(64);
            e.Property(x => x.CreatedBy).HasMaxLength(256).IsRequired();
            e.Property(x => x.Note).HasMaxLength(1024);
            e.HasIndex(x => x.Verifier).IsUnique();
            e.HasIndex(x => new { x.Status, x.ExpiresAt });
            e.ToTable(t =>
            {
                t.HasCheckConstraint("CK_BootstrapTokens_MaxUses", "[MaxUses] >= 1");
                t.HasCheckConstraint("CK_BootstrapTokens_UsedCount", "[UsedCount] >= 0 AND [UsedCount] <= [MaxUses]");
                t.HasCheckConstraint("CK_BootstrapTokens_Status", "[Status] BETWEEN 0 AND 3");
            });
        });

        builder.Entity<TicketAudit>(e =>
        {
            e.ToTable("TicketAudits");
            e.HasKey(x => x.Id);
            e.Property(x => x.TicketId).HasMaxLength(128).IsRequired();
            e.Property(x => x.DeviceId).HasMaxLength(128).IsRequired();
            e.Property(x => x.LocationsJson).IsRequired();
            e.Property(x => x.NodeScopeJson).IsRequired();
            e.Property(x => x.Action).HasMaxLength(32).IsRequired();
            e.HasIndex(x => x.TicketId);
            e.HasIndex(x => new { x.LicenseId, x.IssuedAt });
            e.ToTable(t => t.HasCheckConstraint("CK_TicketAudits_Action", "[Action] IN (N'issue', N'refresh')"));
        });

        builder.Entity<Revocation>(e =>
        {
            e.ToTable("Revocations");
            e.HasKey(x => x.Id);
            e.Property(x => x.Type).HasColumnName("Type");
            e.Property(x => x.TargetId).HasMaxLength(256).IsRequired();
            e.Property(x => x.Reason).HasMaxLength(1024);
            e.Property(x => x.CreatedBy).HasMaxLength(256).IsRequired();
            e.HasIndex(x => x.TargetId);
            e.HasIndex(x => x.Version);
            e.HasIndex(x => new { x.Type, x.TargetId });
            e.ToTable(t =>
            {
                t.HasCheckConstraint("CK_Revocations_Type", "[Type] BETWEEN 0 AND 2");
                t.HasCheckConstraint("CK_Revocations_Version", "[Version] >= 1");
            });
        });

        builder.Entity<CatalogVersion>(e =>
        {
            e.ToTable("CatalogVersions");
            e.HasKey(x => x.Id);
            e.Property(x => x.Version).HasMaxLength(64).IsRequired();
            e.Property(x => x.KeyId).HasMaxLength(128).IsRequired();
            e.Property(x => x.PayloadHash).HasMaxLength(128);
            e.HasIndex(x => x.Version).IsUnique();
            e.HasIndex(x => x.IssuedAt);
        });

        builder.Entity<SigningKeyMetadata>(e =>
        {
            e.ToTable("SigningKeysMetadata");
            e.HasKey(x => x.Id);
            e.Property(x => x.KeyId).HasMaxLength(128).IsRequired();
            e.Property(x => x.PublicKey).HasMaxLength(32).IsRequired();
            e.Property(x => x.ProtectedPrivateKey).IsRequired();
            e.HasIndex(x => x.KeyId).IsUnique();
            e.HasIndex(x => x.Status);
            e.ToTable(t =>
            {
                t.HasCheckConstraint("CK_SigningKeysMetadata_Status", "[Status] BETWEEN 0 AND 2");
                t.HasCheckConstraint("CK_SigningKeysMetadata_PublicKeyLen", "DATALENGTH([PublicKey]) = 32");
            });
        });

        builder.Entity<AuditLogEntry>(e =>
        {
            e.ToTable("AuditLog");
            e.HasKey(x => x.Id);
            e.Property(x => x.Actor).HasMaxLength(256).IsRequired();
            e.Property(x => x.Action).HasMaxLength(128).IsRequired();
            e.Property(x => x.EntityType).HasMaxLength(128).IsRequired();
            e.Property(x => x.EntityId).HasMaxLength(256);
            e.Property(x => x.Timestamp).HasColumnName("Timestamp");
            e.Property(x => x.IpAddress).HasMaxLength(64);
            e.HasIndex(x => x.Timestamp);
            e.HasIndex(x => new { x.EntityType, x.EntityId });
        });

        builder.Entity<SystemSetting>(e =>
        {
            e.ToTable("SystemSettings");
            e.HasKey(x => x.Key);
            e.Property(x => x.Key).HasMaxLength(128);
            e.Property(x => x.Value).IsRequired();
            e.Property(x => x.UpdatedBy).HasMaxLength(256);
        });

        builder.Entity<PaymentEvent>(e =>
        {
            e.ToTable("PaymentEvents");
            e.HasKey(x => x.Id);
            e.Property(x => x.Provider).HasMaxLength(64).IsRequired();
            e.Property(x => x.ExternalPaymentId).HasMaxLength(256).IsRequired();
            e.Property(x => x.Amount).HasPrecision(18, 4);
            e.Property(x => x.Currency).HasMaxLength(8);
            e.Property(x => x.PayloadHash).HasMaxLength(128);
            e.HasIndex(x => new { x.Provider, x.ExternalPaymentId }).IsUnique();
            e.HasIndex(x => new { x.Status, x.ReceivedAt });
            e.ToTable(t =>
            {
                t.HasCheckConstraint("CK_PaymentEvents_Status", "[Status] BETWEEN 0 AND 3");
                t.HasCheckConstraint("CK_PaymentEvents_Amount", "[Amount] IS NULL OR [Amount] >= 0");
            });
        });
    }
}
