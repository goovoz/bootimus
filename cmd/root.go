package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"bootimus/internal/proxydhcp"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "bootimus",
	Short: "A PXE and HTTP boot server with MAC address access control",
	Long: `Bootimus is a network boot server that provides:
- TFTP server for PXE boot
- HTTP server for iPXE and ISO serving
- Database-backed MAC address and image access control (SQLite or PostgreSQL)
- Auto-generated boot menus based on client permissions`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./bootimus.yaml)")

	rootCmd.PersistentFlags().Int("tftp-port", 69, "TFTP server port")
	rootCmd.PersistentFlags().Bool("tftp-single-port", false, "Enable TFTP single port")
	rootCmd.PersistentFlags().Int("http-port", 8080, "HTTP server port")
	rootCmd.PersistentFlags().Int("admin-port", 8081, "Admin interface port")
	rootCmd.PersistentFlags().Bool("nbd-enabled", true, "Enable NBD server for network block device ISO mounting")
	rootCmd.PersistentFlags().Int("nbd-port", 10809, "NBD server port")
	rootCmd.PersistentFlags().Bool("nfs-enabled", false, "Enable in-process NFS server for streamed live-distro roots (low memory)")
	rootCmd.PersistentFlags().Int("nfs-port", 2049, "NFS server port (also used as mountport)")
	rootCmd.PersistentFlags().Int("install-timeout-hours", 6, "Hours before an unattended install that has not reported completion is marked as failed")
	rootCmd.PersistentFlags().String("data-dir", "./data", "Base data directory (subdirs: isos/, bootloaders/)")
	rootCmd.PersistentFlags().String("server-addr", "", "Server IP address (auto-detected if not specified)")
	rootCmd.PersistentFlags().String("bind-interface", "", "Network interface to bind all listeners to, e.g. eth0 (Linux only; all interfaces if not specified)")

	rootCmd.PersistentFlags().String("db-host", "", "PostgreSQL host (if empty, uses SQLite)")
	rootCmd.PersistentFlags().Int("db-port", 5432, "PostgreSQL port")
	rootCmd.PersistentFlags().String("db-user", "bootimus", "PostgreSQL user")
	rootCmd.PersistentFlags().String("db-password", "", "PostgreSQL password")
	rootCmd.PersistentFlags().String("db-name", "bootimus", "PostgreSQL database name")
	rootCmd.PersistentFlags().String("db-sslmode", "disable", "PostgreSQL SSL mode")

	rootCmd.PersistentFlags().String("ldap-host", "", "LDAP server hostname (enables LDAP auth)")
	rootCmd.PersistentFlags().Int("ldap-port", 389, "LDAP server port")
	rootCmd.PersistentFlags().Bool("ldap-tls", false, "Use LDAPS (TLS)")
	rootCmd.PersistentFlags().Bool("ldap-starttls", false, "Use StartTLS")
	rootCmd.PersistentFlags().Bool("ldap-skip-verify", false, "Skip TLS certificate verification")
	rootCmd.PersistentFlags().String("ldap-bind-dn", "", "LDAP bind DN for search")
	rootCmd.PersistentFlags().String("ldap-bind-password", "", "LDAP bind password")
	rootCmd.PersistentFlags().String("ldap-base-dn", "", "LDAP base DN for user search")
	rootCmd.PersistentFlags().String("ldap-user-filter", "(sAMAccountName=%s)", "LDAP user search filter (%s = username)")
	rootCmd.PersistentFlags().String("ldap-group-filter", "", "LDAP group filter for admin access (optional)")
	rootCmd.PersistentFlags().String("ldap-group-base-dn", "", "LDAP base DN for group search")

	rootCmd.PersistentFlags().String("oidc-issuer", "", "OIDC issuer URL (enables OIDC single sign-on)")
	rootCmd.PersistentFlags().String("oidc-client-id", "", "OIDC client ID")
	rootCmd.PersistentFlags().String("oidc-client-secret", "", "OIDC client secret")
	rootCmd.PersistentFlags().String("oidc-redirect-url", "", "OIDC redirect URL, e.g. https://bootimus.example.com/api/oidc/callback")
	rootCmd.PersistentFlags().String("oidc-scopes", "openid,profile,email", "Comma-separated OIDC scopes to request")
	rootCmd.PersistentFlags().String("oidc-username-claim", "preferred_username", "OIDC claim used as the Bootimus username")
	rootCmd.PersistentFlags().String("oidc-groups-claim", "groups", "OIDC claim listing the user's groups")
	rootCmd.PersistentFlags().String("oidc-admin-groups", "", "Comma-separated OIDC groups whose members are administrators")
	rootCmd.PersistentFlags().String("oidc-allowed-groups", "", "Comma-separated OIDC groups allowed to sign in (empty allows anyone the provider authenticates)")
	rootCmd.PersistentFlags().String("oidc-admin-users", "", "Comma-separated OIDC usernames, or @domain entries, who are administrators")
	rootCmd.PersistentFlags().String("oidc-allowed-users", "", "Comma-separated OIDC usernames, or @domain entries, allowed to sign in")
	rootCmd.PersistentFlags().String("oidc-display-name", "SSO", "Name shown on the OIDC sign-in button")
	rootCmd.PersistentFlags().Bool("oidc-skip-verify", false, "Skip TLS certificate verification when talking to the OIDC provider")

	rootCmd.PersistentFlags().Bool("disable-remote-profiles", false, "Disable remote distro profile updates")

	rootCmd.PersistentFlags().Bool("proxy-dhcp", false, "Enable in-process proxyDHCP server (answers PXE requests without handing out IPs; requires root or CAP_NET_BIND_SERVICE)")
	rootCmd.PersistentFlags().String("proxy-dhcp-bootfile-bios", proxydhcp.DefaultBootfileBIOS, "Bootfile advertised to legacy BIOS PXE clients (default follows the active bootloader set's manifest)")
	rootCmd.PersistentFlags().String("proxy-dhcp-bootfile-uefi", proxydhcp.DefaultBootfileUEFI, "Bootfile advertised to UEFI x64 PXE clients (default follows the active bootloader set's manifest)")
	rootCmd.PersistentFlags().String("proxy-dhcp-bootfile-arm64", proxydhcp.DefaultBootfileARM64, "Bootfile advertised to UEFI ARM64 PXE clients (default follows the active bootloader set's manifest)")
	rootCmd.PersistentFlags().Bool("proxy-dhcp-no-bootfile-option", false, "Disable advertising the bootfile name using a DHCP option (for buggy/old clients)")
	rootCmd.PersistentFlags().Bool("proxy-dhcp-no-vendor-option", false, "Omit the PXE vendor option (43) from proxyDHCP offers (for firmware that ignores offers carrying it)")

	rootCmd.PersistentFlags().Bool("admin-tls", false, "Serve the admin interface over HTTPS")
	rootCmd.PersistentFlags().String("admin-tls-cert", "", "PEM certificate file for the admin interface (with --admin-tls-key); omit for a self-signed certificate")
	rootCmd.PersistentFlags().String("admin-tls-key", "", "PEM private key file for the admin interface")
	rootCmd.PersistentFlags().Bool("admin-tls-self-signed", false, "Generate and reuse a self-signed certificate under <data-dir>/tls even when cert and key files are set")
	rootCmd.PersistentFlags().Int("admin-tls-redirect-port", 0, "Also listen on this plain HTTP port and redirect to the HTTPS admin interface (0 disables)")

	rootCmd.PersistentFlags().String("replica-primary", "", "Run as a read-only replica of this primary Bootimus admin URL, e.g. https://bootimus.example.com:8081")
	rootCmd.PersistentFlags().String("replica-token", "", "API token created on the primary for the replica to authenticate with")
	rootCmd.PersistentFlags().Duration("replica-interval", 5*time.Minute, "How often the replica syncs from the primary")
	rootCmd.PersistentFlags().Bool("replica-sync-isos", true, "Copy ISOs and extracted boot files from the primary; when off, clients fetch them from the primary directly")

	rootCmd.PersistentFlags().Bool("windows-smb", false, "Enable Samba share for unattended Windows PXE installs (requires smbd in PATH)")
	rootCmd.PersistentFlags().Int("windows-smb-port", 445, "SMB port (Windows 'net use' always uses 445; override only for testing)")

	viper.BindPFlag("replica.primary_url", rootCmd.PersistentFlags().Lookup("replica-primary"))
	viper.BindPFlag("replica.token", rootCmd.PersistentFlags().Lookup("replica-token"))
	viper.BindPFlag("replica.interval", rootCmd.PersistentFlags().Lookup("replica-interval"))
	viper.BindPFlag("replica.sync_isos", rootCmd.PersistentFlags().Lookup("replica-sync-isos"))
	viper.BindPFlag("tftp_port", rootCmd.PersistentFlags().Lookup("tftp-port"))
	viper.BindPFlag("tftp_single_port", rootCmd.PersistentFlags().Lookup("tftp-single-port"))
	viper.BindPFlag("http_port", rootCmd.PersistentFlags().Lookup("http-port"))
	viper.BindPFlag("admin_port", rootCmd.PersistentFlags().Lookup("admin-port"))
	viper.BindPFlag("nbd_enabled", rootCmd.PersistentFlags().Lookup("nbd-enabled"))
	viper.BindPFlag("nbd_port", rootCmd.PersistentFlags().Lookup("nbd-port"))
	viper.BindPFlag("nfs_enabled", rootCmd.PersistentFlags().Lookup("nfs-enabled"))
	viper.BindPFlag("nfs_port", rootCmd.PersistentFlags().Lookup("nfs-port"))
	viper.BindPFlag("install_timeout_hours", rootCmd.PersistentFlags().Lookup("install-timeout-hours"))
	viper.BindPFlag("data_dir", rootCmd.PersistentFlags().Lookup("data-dir"))
	viper.BindPFlag("server_addr", rootCmd.PersistentFlags().Lookup("server-addr"))
	viper.BindPFlag("bind_interface", rootCmd.PersistentFlags().Lookup("bind-interface"))
	viper.BindPFlag("db.host", rootCmd.PersistentFlags().Lookup("db-host"))
	viper.BindPFlag("db.port", rootCmd.PersistentFlags().Lookup("db-port"))
	viper.BindPFlag("db.user", rootCmd.PersistentFlags().Lookup("db-user"))
	viper.BindPFlag("db.password", rootCmd.PersistentFlags().Lookup("db-password"))
	viper.BindPFlag("db.name", rootCmd.PersistentFlags().Lookup("db-name"))
	viper.BindPFlag("db.sslmode", rootCmd.PersistentFlags().Lookup("db-sslmode"))

	viper.BindPFlag("ldap.host", rootCmd.PersistentFlags().Lookup("ldap-host"))
	viper.BindPFlag("ldap.port", rootCmd.PersistentFlags().Lookup("ldap-port"))
	viper.BindPFlag("ldap.tls", rootCmd.PersistentFlags().Lookup("ldap-tls"))
	viper.BindPFlag("ldap.starttls", rootCmd.PersistentFlags().Lookup("ldap-starttls"))
	viper.BindPFlag("ldap.skip_verify", rootCmd.PersistentFlags().Lookup("ldap-skip-verify"))
	viper.BindPFlag("ldap.bind_dn", rootCmd.PersistentFlags().Lookup("ldap-bind-dn"))
	viper.BindPFlag("ldap.bind_password", rootCmd.PersistentFlags().Lookup("ldap-bind-password"))
	viper.BindPFlag("ldap.base_dn", rootCmd.PersistentFlags().Lookup("ldap-base-dn"))
	viper.BindPFlag("ldap.user_filter", rootCmd.PersistentFlags().Lookup("ldap-user-filter"))
	viper.BindPFlag("ldap.group_filter", rootCmd.PersistentFlags().Lookup("ldap-group-filter"))
	viper.BindPFlag("ldap.group_base_dn", rootCmd.PersistentFlags().Lookup("ldap-group-base-dn"))

	viper.BindPFlag("oidc.issuer", rootCmd.PersistentFlags().Lookup("oidc-issuer"))
	viper.BindPFlag("oidc.client_id", rootCmd.PersistentFlags().Lookup("oidc-client-id"))
	viper.BindPFlag("oidc.client_secret", rootCmd.PersistentFlags().Lookup("oidc-client-secret"))
	viper.BindPFlag("oidc.redirect_url", rootCmd.PersistentFlags().Lookup("oidc-redirect-url"))
	viper.BindPFlag("oidc.scopes", rootCmd.PersistentFlags().Lookup("oidc-scopes"))
	viper.BindPFlag("oidc.username_claim", rootCmd.PersistentFlags().Lookup("oidc-username-claim"))
	viper.BindPFlag("oidc.groups_claim", rootCmd.PersistentFlags().Lookup("oidc-groups-claim"))
	viper.BindPFlag("oidc.admin_groups", rootCmd.PersistentFlags().Lookup("oidc-admin-groups"))
	viper.BindPFlag("oidc.allowed_groups", rootCmd.PersistentFlags().Lookup("oidc-allowed-groups"))
	viper.BindPFlag("oidc.admin_users", rootCmd.PersistentFlags().Lookup("oidc-admin-users"))
	viper.BindPFlag("oidc.allowed_users", rootCmd.PersistentFlags().Lookup("oidc-allowed-users"))
	viper.BindPFlag("oidc.display_name", rootCmd.PersistentFlags().Lookup("oidc-display-name"))
	viper.BindPFlag("oidc.skip_verify", rootCmd.PersistentFlags().Lookup("oidc-skip-verify"))

	viper.BindPFlag("disable_remote_profiles", rootCmd.PersistentFlags().Lookup("disable-remote-profiles"))

	viper.BindPFlag("proxy_dhcp.enabled", rootCmd.PersistentFlags().Lookup("proxy-dhcp"))
	viper.BindPFlag("proxy_dhcp.bootfile_bios", rootCmd.PersistentFlags().Lookup("proxy-dhcp-bootfile-bios"))
	viper.BindPFlag("proxy_dhcp.bootfile_uefi", rootCmd.PersistentFlags().Lookup("proxy-dhcp-bootfile-uefi"))
	viper.BindPFlag("proxy_dhcp.bootfile_arm64", rootCmd.PersistentFlags().Lookup("proxy-dhcp-bootfile-arm64"))
	viper.BindPFlag("proxy_dhcp.no_bootfile_option", rootCmd.PersistentFlags().Lookup("proxy-dhcp-no-bootfile-option"))
	viper.BindPFlag("proxy_dhcp.no_vendor_option", rootCmd.PersistentFlags().Lookup("proxy-dhcp-no-vendor-option"))

	viper.BindPFlag("admin_tls.enabled", rootCmd.PersistentFlags().Lookup("admin-tls"))
	viper.BindPFlag("admin_tls.cert_file", rootCmd.PersistentFlags().Lookup("admin-tls-cert"))
	viper.BindPFlag("admin_tls.key_file", rootCmd.PersistentFlags().Lookup("admin-tls-key"))
	viper.BindPFlag("admin_tls.self_signed", rootCmd.PersistentFlags().Lookup("admin-tls-self-signed"))
	viper.BindPFlag("admin_tls.redirect_port", rootCmd.PersistentFlags().Lookup("admin-tls-redirect-port"))

	viper.BindPFlag("windows_smb.enabled", rootCmd.PersistentFlags().Lookup("windows-smb"))
	viper.BindPFlag("windows_smb.port", rootCmd.PersistentFlags().Lookup("windows-smb-port"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.AddConfigPath(".")
		viper.AddConfigPath("/etc/bootimus/")
		viper.SetConfigType("yaml")
		viper.SetConfigName("bootimus")
	}

	viper.SetEnvPrefix("BOOTIMUS")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}
