package admin

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"bootimus/internal/models"
	"bootimus/internal/storage"
)

func importClientsCSV(t *testing.T, h *Handler, csv string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "clients.csv")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte(csv))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/clients/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ImportClientsCSV(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import returned %d: %s", rec.Code, rec.Body.String())
	}
}

func TestImportClientsCSVKeepsFieldsItDoesNotCarry(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	h := &Handler{storage: store}

	mac := "aa:bb:cc:dd:ee:03"
	if err := store.CreateClient(&models.Client{MACAddress: mac, Name: "bmc-node"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateClient(mac, &models.Client{
		Name:            "bmc-node",
		IPMIHost:        "10.0.0.100",
		IPMIPort:        443,
		IPMIUsername:    "admin",
		IPMIPassword:    "secret",
		IPMIInsecure:    true,
		AutoInstallFile: "ubuntu/node.yaml",
	}); err != nil {
		t.Fatal(err)
	}

	importClientsCSV(t, h, "mac_address,name\n"+mac+",bmc-node-renamed\n")

	c, err := store.GetClient(mac)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "bmc-node-renamed" {
		t.Errorf("name = %q, want the imported name", c.Name)
	}
	if c.IPMIHost != "10.0.0.100" || c.IPMIPort != 443 || c.IPMIUsername != "admin" || c.IPMIPassword != "secret" || !c.IPMIInsecure {
		t.Errorf("IPMI settings were not kept: %+v", c)
	}
	if c.AutoInstallFile != "ubuntu/node.yaml" {
		t.Errorf("auto-install file = %q, want it kept", c.AutoInstallFile)
	}
}

func TestImportClientsCSVNoVendorOption(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	h := &Handler{storage: store}

	if err := store.CreateClient(&models.Client{MACAddress: "aa:bb:cc:dd:ee:01", Name: "i219", ProxyDHCPNoVendorOption: true}); err != nil {
		t.Fatal(err)
	}

	importClientsCSV(t, h, "mac_address,name\naa:bb:cc:dd:ee:01,i219-renamed\n")
	if c, _ := store.GetClient("aa:bb:cc:dd:ee:01"); c == nil || !c.ProxyDHCPNoVendorOption {
		t.Error("import without the column must keep the existing setting")
	}

	importClientsCSV(t, h, "mac_address,name,proxy_dhcp_no_vendor_option\naa:bb:cc:dd:ee:01,i219,false\naa:bb:cc:dd:ee:02,new,true\n")
	if c, _ := store.GetClient("aa:bb:cc:dd:ee:01"); c == nil || c.ProxyDHCPNoVendorOption {
		t.Error("import with false must clear the setting")
	}
	if c, _ := store.GetClient("aa:bb:cc:dd:ee:02"); c == nil || !c.ProxyDHCPNoVendorOption {
		t.Error("import with true must set it on a new client")
	}
}
