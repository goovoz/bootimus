package storage

import (
	"reflect"
	"testing"

	"bootimus/internal/models"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return store
}

func TestDeleteClientHardDeletesAndAllowsRecreate(t *testing.T) {
	store := newTestStore(t)
	mac := "aa:bb:cc:dd:ee:ff"

	if err := store.CreateClient(&models.Client{MACAddress: mac, Name: "first"}); err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	if err := store.DeleteClient(mac); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}

	var count int64
	store.db.Unscoped().Model(&models.Client{}).Where("mac_address = ?", mac).Count(&count)
	if count != 0 {
		t.Fatalf("expected client row to be gone, found %d rows", count)
	}

	if err := store.CreateClient(&models.Client{MACAddress: mac, Name: "second"}); err != nil {
		t.Fatalf("CreateClient after delete: %v", err)
	}
	client, err := store.GetClient(mac)
	if err != nil {
		t.Fatalf("GetClient after recreate: %v", err)
	}
	if client.Name != "second" {
		t.Fatalf("expected recreated client, got %q", client.Name)
	}
}

func TestDeleteClientMissingMACIsNoop(t *testing.T) {
	store := newTestStore(t)
	if err := store.DeleteClient("00:00:00:00:00:00"); err != nil {
		t.Fatalf("DeleteClient on missing MAC: %v", err)
	}
}

func TestGetClientBootloaderSetsSelectsOnlyRequiredColumns(t *testing.T) {
	store := newTestStore(t)
	group := &models.ClientGroup{Name: "rack-a", BootloaderSet: "secureboot-official"}
	if err := store.CreateClientGroup(group); err != nil {
		t.Fatalf("CreateClientGroup: %v", err)
	}

	clients := []*models.Client{
		{MACAddress: "02:00:00:00:10:01", BootloaderSet: "se350", ClientGroupID: &group.ID},
		{MACAddress: "02:00:00:00:10:02", ClientGroupID: &group.ID},
		{MACAddress: "02:00:00:00:10:03"},
	}
	for _, client := range clients {
		if err := store.CreateClient(client); err != nil {
			t.Fatalf("CreateClient(%s): %v", client.MACAddress, err)
		}
	}

	tests := []struct {
		mac        string
		wantClient string
		wantGroup  string
	}{
		{mac: clients[0].MACAddress, wantClient: "se350", wantGroup: "secureboot-official"},
		{mac: clients[1].MACAddress, wantGroup: "secureboot-official"},
		{mac: clients[2].MACAddress},
	}
	for _, tt := range tests {
		clientSet, groupSet, err := store.GetClientBootloaderSets(tt.mac)
		if err != nil {
			t.Fatalf("GetClientBootloaderSets(%s): %v", tt.mac, err)
		}
		if clientSet != tt.wantClient || groupSet != tt.wantGroup {
			t.Fatalf("GetClientBootloaderSets(%s) = (%q, %q), want (%q, %q)", tt.mac, clientSet, groupSet, tt.wantClient, tt.wantGroup)
		}
	}
}

func TestDeleteClientDetachesBootLogsAndInventory(t *testing.T) {
	store := newTestStore(t)
	mac := "aa:bb:cc:dd:ee:01"

	if err := store.CreateClient(&models.Client{MACAddress: mac}); err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	client, err := store.GetClient(mac)
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	if err := store.db.Create(&models.BootLog{ClientID: &client.ID, MACAddress: mac}).Error; err != nil {
		t.Fatalf("create boot log: %v", err)
	}
	if err := store.db.Create(&models.HardwareInventory{ClientID: &client.ID, MACAddress: mac}).Error; err != nil {
		t.Fatalf("create inventory: %v", err)
	}

	if err := store.DeleteClient(mac); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}

	var bootLog models.BootLog
	if err := store.db.Where("mac_address = ?", mac).First(&bootLog).Error; err != nil {
		t.Fatalf("boot log should survive client deletion: %v", err)
	}
	if bootLog.ClientID != nil {
		t.Fatalf("expected boot log client_id to be nulled, got %d", *bootLog.ClientID)
	}

	var inv models.HardwareInventory
	if err := store.db.Where("mac_address = ?", mac).First(&inv).Error; err != nil {
		t.Fatalf("inventory should survive client deletion: %v", err)
	}
	if inv.ClientID != nil {
		t.Fatalf("expected inventory client_id to be nulled, got %d", *inv.ClientID)
	}
}

func TestAutoMigratePurgesLegacySoftDeletedClients(t *testing.T) {
	store := newTestStore(t)
	mac := "aa:bb:cc:dd:ee:02"

	if err := store.CreateClient(&models.Client{MACAddress: mac}); err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	if err := store.db.Where("mac_address = ?", mac).Delete(&models.Client{}).Error; err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	var count int64
	store.db.Unscoped().Model(&models.Client{}).Where("mac_address = ?", mac).Count(&count)
	if count != 1 {
		t.Fatalf("expected soft-deleted row to exist before migration, found %d", count)
	}

	if err := store.AutoMigrate(); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	store.db.Unscoped().Model(&models.Client{}).Where("mac_address = ?", mac).Count(&count)
	if count != 0 {
		t.Fatalf("expected soft-deleted row to be purged, found %d", count)
	}

	if err := store.CreateClient(&models.Client{MACAddress: mac}); err != nil {
		t.Fatalf("CreateClient after purge: %v", err)
	}
}

func TestUpdateClientPersistsAutoInstallFile(t *testing.T) {
	store := newTestStore(t)
	mac := "aa:bb:cc:dd:ee:03"

	if err := store.CreateClient(&models.Client{MACAddress: mac, Name: "node"}); err != nil {
		t.Fatalf("CreateClient: %v", err)
	}

	if err := store.UpdateClient(mac, &models.Client{Name: "node", AutoInstallFile: "ubuntu/user-data.yaml"}); err != nil {
		t.Fatalf("UpdateClient: %v", err)
	}
	client, err := store.GetClient(mac)
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	if client.AutoInstallFile != "ubuntu/user-data.yaml" {
		t.Fatalf("expected auto install file to persist, got %q", client.AutoInstallFile)
	}

	if err := store.UpdateClient(mac, &models.Client{Name: "node"}); err != nil {
		t.Fatalf("UpdateClient clear: %v", err)
	}
	client, err = store.GetClient(mac)
	if err != nil {
		t.Fatalf("GetClient after clear: %v", err)
	}
	if client.AutoInstallFile != "" {
		t.Fatalf("expected auto install file to clear, got %q", client.AutoInstallFile)
	}
}

func TestUpdateClientGroupPersistsAutoInstallFile(t *testing.T) {
	store := newTestStore(t)

	group := &models.ClientGroup{Name: "fleet"}
	if err := store.CreateClientGroup(group); err != nil {
		t.Fatalf("CreateClientGroup: %v", err)
	}

	if err := store.UpdateClientGroup(group.ID, &models.ClientGroup{Name: "fleet", AutoInstallFile: "debian/preseed.cfg"}); err != nil {
		t.Fatalf("UpdateClientGroup: %v", err)
	}
	got, err := store.GetClientGroup(group.ID)
	if err != nil {
		t.Fatalf("GetClientGroup: %v", err)
	}
	if got.AutoInstallFile != "debian/preseed.cfg" {
		t.Fatalf("expected group auto install file to persist, got %q", got.AutoInstallFile)
	}

	if err := store.UpdateClientGroup(group.ID, &models.ClientGroup{Name: "fleet"}); err != nil {
		t.Fatalf("UpdateClientGroup clear: %v", err)
	}
	got, err = store.GetClientGroup(group.ID)
	if err != nil {
		t.Fatalf("GetClientGroup after clear: %v", err)
	}
	if got.AutoInstallFile != "" {
		t.Fatalf("expected group auto install file to clear, got %q", got.AutoInstallFile)
	}
}

func TestUpdateClientPersistsProxyDHCPNoVendorOption(t *testing.T) {
	store := newTestStore(t)
	mac := "aa:bb:cc:dd:ee:04"

	if err := store.CreateClient(&models.Client{MACAddress: mac, Name: "i219"}); err != nil {
		t.Fatalf("CreateClient: %v", err)
	}

	for _, want := range []bool{true, false} {
		if err := store.UpdateClient(mac, &models.Client{Name: "i219", ProxyDHCPNoVendorOption: want}); err != nil {
			t.Fatalf("UpdateClient(%v): %v", want, err)
		}
		client, err := store.GetClient(mac)
		if err != nil {
			t.Fatalf("GetClient: %v", err)
		}
		if client.ProxyDHCPNoVendorOption != want {
			t.Fatalf("ProxyDHCPNoVendorOption = %v, want %v", client.ProxyDHCPNoVendorOption, want)
		}
	}
}

func TestSyncImagesSetsUKIDefaultsOnCreateOnly(t *testing.T) {
	store := newTestStore(t)
	files := []models.SyncFile{
		{Name: "hello-world", Filename: "hello-world.efi", Size: 10, BootMethod: "uki", BootParams: "audit=0"},
		{Name: "ubuntu", Filename: "ubuntu.iso", Size: 20},
	}
	if err := store.SyncImages(files); err != nil {
		t.Fatal(err)
	}

	img, err := store.GetImage("hello-world.efi")
	if err != nil {
		t.Fatal(err)
	}
	if img.BootMethod != "uki" || img.BootParams != "audit=0" {
		t.Errorf("UKI image = %q/%q, want uki/audit=0", img.BootMethod, img.BootParams)
	}
	iso, err := store.GetImage("ubuntu.iso")
	if err != nil {
		t.Fatal(err)
	}
	if iso.BootMethod != "sanboot" {
		t.Errorf("ISO boot method = %q, want sanboot", iso.BootMethod)
	}

	img.BootParams = "audit=0 console=ttyS0"
	if err := store.UpdateImage(img.Filename, img); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncImages(files); err != nil {
		t.Fatal(err)
	}
	img, _ = store.GetImage("hello-world.efi")
	if img.BootParams != "audit=0 console=ttyS0" {
		t.Errorf("rescan overwrote edited boot params: %q", img.BootParams)
	}
}

func TestClientAndGroupVariablesRoundTrip(t *testing.T) {
	store := newTestStore(t)

	group := &models.ClientGroup{Name: "lab", Enabled: true, Variables: models.StringMap{"role": "worker", "rack": "3"}}
	if err := store.CreateClientGroup(group); err != nil {
		t.Fatal(err)
	}
	client := &models.Client{MACAddress: "aa:bb:cc:dd:ee:40", Enabled: true, ClientGroupID: &group.ID, Variables: models.StringMap{"role": "control-plane"}}
	if err := store.CreateClient(client); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		update    models.StringMap
		wantAfter models.StringMap
	}{
		{"replace", models.StringMap{"rack": "9"}, models.StringMap{"rack": "9"}},
		{"clear to empty map", models.StringMap{}, models.StringMap{}},
		{"clear with nil", nil, models.StringMap{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client.Variables = tt.update
			if err := store.UpdateClient(client.MACAddress, client); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetClient(client.MACAddress)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Variables, tt.wantAfter) {
				t.Errorf("client variables = %#v, want %#v", got.Variables, tt.wantAfter)
			}

			group.Variables = tt.update
			if err := store.UpdateClientGroup(group.ID, group); err != nil {
				t.Fatal(err)
			}
			gotGroup, err := store.GetClientGroup(group.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotGroup.Variables, tt.wantAfter) {
				t.Errorf("group variables = %#v, want %#v", gotGroup.Variables, tt.wantAfter)
			}
		})
	}
}
