package cmd

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"bootimus/internal/auth"
	"bootimus/internal/netbind"
	"bootimus/internal/profiles"
	"bootimus/internal/replica"
	"bootimus/internal/server"
	"bootimus/internal/storage"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	colorReset      = "\033[0m"
	colorLightGreen = "\033[92m"
	colorYellow     = "\033[33m"
)

var resetAdminPassword bool

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the PXE/HTTP boot server",
	Long:  `Start the TFTP and HTTP servers to provide network boot services`,
	Run:   runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().BoolVar(&resetAdminPassword, "reset-admin-password", false, "Reset admin password to a new random value")
}

func printBanner() {
	banner := `
   ____              __  _
  / __ )____  ____  / /_(_)___ ___  __  _______
 / __  / __ \/ __ \/ __/ / __ '__ \/ / / / ___/
/ /_/ / /_/ / /_/ / /_/ / / / / / / /_/ (__  )
\____/\____/\____/\__/_/_/ /_/ /_/\__,_/____/
`
	fmt.Printf("%s%s%s", colorLightGreen, banner, colorReset)
	fmt.Printf("%sVersion: %s%s\n", colorYellow, server.Version, colorReset)
	fmt.Printf("%sPXE/HTTP Boot Server%s\n\n", colorLightGreen, colorReset)
}

func runServe(cmd *cobra.Command, args []string) {
	server.InitGlobalLogger()

	printBanner()

	dataDir := viper.GetString("data_dir")

	isoDir := dataDir + "/isos"
	bootloadersDir := dataDir + "/bootloaders"

	for _, dir := range []string{dataDir, isoDir, bootloadersDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("Failed to create directory %s: %v", dir, err)
		}
	}

	log.Printf("Data directory structure initialised at: %s", dataDir)
	log.Printf("  - ISOs: %s", isoDir)
	log.Printf("  - Bootloaders: %s", bootloadersDir)

	serverAddr := viper.GetString("server_addr")
	bindInterface := viper.GetString("bind_interface")
	if serverAddr == "" {
		if bindInterface != "" {
			ip, err := netbind.InterfaceIPv4(bindInterface)
			if err != nil {
				log.Fatalf("Failed to determine server IP from interface %s: %v", bindInterface, err)
			}
			serverAddr = ip.String()
			log.Printf("Using server IP %s from interface %s", serverAddr, bindInterface)
		} else {
			serverAddr = server.GetOutboundIP()
			log.Printf("Auto-detected server IP: %s", serverAddr)
		}
	}

	var store storage.Storage
	var err error

	pgHost := viper.GetString("db.host")
	var dbCfg *storage.Config
	if pgHost != "" {
		dbCfg = &storage.Config{
			Host:     pgHost,
			Port:     viper.GetInt("db.port"),
			User:     viper.GetString("db.user"),
			Password: viper.GetString("db.password"),
			DBName:   viper.GetString("db.name"),
			SSLMode:  viper.GetString("db.sslmode"),
		}
	}

	applyPendingRestore(dataDir, dbCfg)

	if pgHost != "" {
		log.Printf("Connecting to PostgreSQL database at %s:%d...", pgHost, viper.GetInt("db.port"))

		maxRetries := 10
		for i := 0; i < maxRetries; i++ {
			store, err = storage.NewPostgresStore(dbCfg)
			if err == nil {
				break
			}

			if i < maxRetries-1 {
				waitTime := (1 << uint(i))
				log.Printf("Database connection failed (attempt %d/%d): %v", i+1, maxRetries, err)
				log.Printf("Retrying in %d seconds...", waitTime)
				time.Sleep(time.Duration(waitTime) * time.Second)
			}
		}

		if err != nil {
			log.Fatalf("Failed to connect to database after %d attempts: %v", maxRetries, err)
		}

		if err := store.AutoMigrate(); err != nil {
			log.Fatalf("Failed to run database migrations: %v", err)
		}

		log.Println("Database connected and migrations completed (PostgreSQL)")
	} else {
		log.Printf("No PostgreSQL configuration found, using local SQLite database")
		store, err = storage.NewSQLiteStore(dataDir)
		if err != nil {
			log.Fatalf("Failed to initialise SQLite store: %v", err)
		}

		if err := store.AutoMigrate(); err != nil {
			log.Fatalf("Failed to run database migrations: %v", err)
		}

		log.Printf("Local database initialised at %s/bootimus.db (SQLite)", dataDir)
	}

	if resetAdminPassword {
		password, err := store.ResetAdminPassword()
		if err != nil {
			log.Fatalf("Failed to reset admin password: %v", err)
		}

		log.Println("╔════════════════════════════════════════════════════════════════╗")
		log.Println("║                  ADMIN PASSWORD RESET                          ║")
		log.Println("╠════════════════════════════════════════════════════════════════╣")
		log.Printf("║  Username: %-51s ║\n", "admin")
		log.Printf("║  New Password: %-47s ║\n", password)
		log.Println("╠════════════════════════════════════════════════════════════════╣")
		log.Println("║  This password will NOT be shown again!                        ║")
		log.Println("║  Save it now before continuing.                                ║")
		log.Println("╚════════════════════════════════════════════════════════════════╝")
		log.Println("\nContinuing to start server...")
	}

	var ldapConfig *auth.LDAPConfig
	if ldapHost := viper.GetString("ldap.host"); ldapHost != "" {
		ldapConfig = &auth.LDAPConfig{
			Host:         ldapHost,
			Port:         viper.GetInt("ldap.port"),
			UseTLS:       viper.GetBool("ldap.tls"),
			StartTLS:     viper.GetBool("ldap.starttls"),
			SkipVerify:   viper.GetBool("ldap.skip_verify"),
			BindDN:       viper.GetString("ldap.bind_dn"),
			BindPassword: viper.GetString("ldap.bind_password"),
			BaseDN:       viper.GetString("ldap.base_dn"),
			UserFilter:   viper.GetString("ldap.user_filter"),
			GroupFilter:  viper.GetString("ldap.group_filter"),
			GroupBaseDN:  viper.GetString("ldap.group_base_dn"),
		}
	}

	var oidcConfig *auth.OIDCConfig
	if issuer := viper.GetString("oidc.issuer"); issuer != "" {
		oidcConfig = &auth.OIDCConfig{
			Issuer:        issuer,
			ClientID:      viper.GetString("oidc.client_id"),
			ClientSecret:  viper.GetString("oidc.client_secret"),
			RedirectURL:   viper.GetString("oidc.redirect_url"),
			Scopes:        splitList(viper.GetString("oidc.scopes")),
			UsernameClaim: viper.GetString("oidc.username_claim"),
			GroupsClaim:   viper.GetString("oidc.groups_claim"),
			AdminGroups:   splitList(viper.GetString("oidc.admin_groups")),
			AllowedGroups: splitList(viper.GetString("oidc.allowed_groups")),
			AdminUsers:    splitList(viper.GetString("oidc.admin_users")),
			AllowedUsers:  splitList(viper.GetString("oidc.allowed_users")),
			DisplayName:   viper.GetString("oidc.display_name"),
			SkipVerify:    viper.GetBool("oidc.skip_verify"),
		}
	}

	authMgr, err := auth.NewManager(store, ldapConfig, oidcConfig)
	if err != nil {
		log.Fatalf("Failed to initialise authentication: %v", err)
	}

	profileMgr := profiles.NewManager(store)
	profileMgr.DisableRemoteCheck = viper.GetBool("disable_remote_profiles")
	if err := profileMgr.SeedProfiles(); err != nil {
		log.Printf("Warning: Failed to seed distro profiles: %v", err)
	}
	if profileMgr.DisableRemoteCheck {
		log.Println("Remote distro profile and tool catalogue updates disabled")
	}

	cfg := &server.Config{
		TFTPPort:         viper.GetInt("tftp_port"),
		TFTPSinglePort:   viper.GetBool("tftp_single_port"),
		TFTPBlockSize:    viper.GetInt("tftp_block_size"),
		HTTPPort:         viper.GetInt("http_port"),
		AdminPort:        viper.GetInt("admin_port"),
		BootDir:          bootloadersDir,
		DataDir:          dataDir,
		ISODir:           isoDir,
		ServerAddr:       serverAddr,
		BindInterface:    bindInterface,
		Storage:          store,
		Auth:             authMgr,
		NBDEnabled:       viper.GetBool("nbd_enabled"),
		NBDPort:          viper.GetInt("nbd_port"),
		NFSEnabled:       viper.GetBool("nfs_enabled"),
		NFSPort:          viper.GetInt("nfs_port"),
		WOLBroadcastAddr: viper.GetString("wol_broadcast_addr"),
		ProfileManager:   profileMgr,

		ProxyDHCPEnabled:          viper.GetBool("proxy_dhcp.enabled"),
		ProxyDHCPBootfileBIOS:     viper.GetString("proxy_dhcp.bootfile_bios"),
		ProxyDHCPBootfileUEFI:     viper.GetString("proxy_dhcp.bootfile_uefi"),
		ProxyDHCPBootfileARM:      viper.GetString("proxy_dhcp.bootfile_arm64"),
		ProxyDHCPNoBootfileOption: viper.GetBool("proxy_dhcp.no_bootfile_option"),
		ProxyDHCPNoVendorOption:   viper.GetBool("proxy_dhcp.no_vendor_option"),

		WindowsSMBEnabled: viper.GetBool("windows_smb.enabled"),
		WindowsSMBPort:    viper.GetInt("windows_smb.port"),

		AdminTLS: server.AdminTLSConfig{
			Enabled:      viper.GetBool("admin_tls.enabled"),
			CertFile:     viper.GetString("admin_tls.cert_file"),
			KeyFile:      viper.GetString("admin_tls.key_file"),
			SelfSigned:   viper.GetBool("admin_tls.self_signed"),
			RedirectPort: viper.GetInt("admin_tls.redirect_port"),
		},
	}
	if err := cfg.AdminTLS.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}
	replicaCfg := &replica.Config{
		PrimaryURL: viper.GetString("replica.primary_url"),
		Token:      viper.GetString("replica.token"),
		Interval:   viper.GetDuration("replica.interval"),
		SyncISOs:   viper.GetBool("replica.sync_isos"),
	}
	if err := replicaCfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}
	if replicaCfg.Enabled() {
		cfg.Replica = replicaCfg
	}
	cfg.InstallTimeoutHours = viper.GetInt("install_timeout_hours")

	srv := server.New(cfg)
	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	for sig := range sigChan {
		if sig != syscall.SIGHUP {
			break
		}
		if err := srv.ReloadTLS(); err != nil {
			log.Printf("Admin TLS: reload failed, keeping the current certificate: %v", err)
		}
	}

	log.Println("Received shutdown signal...")
	if err := srv.Shutdown(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
}

func splitList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func applyPendingRestore(dataDir string, dbCfg *storage.Config) {
	summary, err := storage.ApplyPendingRestore(dataDir, dbCfg)
	if errors.Is(err, storage.ErrNoPendingRestore) {
		return
	}
	if err != nil {
		log.Fatalf("Failed to apply staged backup restore (left in place under %s/.restore-pending): %v", dataDir, err)
	}
	log.Printf("Restored backup staged at %s (%s database, %d files)", summary.StagedAt.Format(time.RFC3339), summary.Database, summary.FileCount)
}
