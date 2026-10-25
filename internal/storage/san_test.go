package storage

import (
	"strings"
	"testing"

	"bootimus/internal/models"
)

func TestSANTargetRoundTrip(t *testing.T) {
	store := newTestStore(t)
	target := &models.SANTarget{Name: "lab", URI: "iscsi:10.0.0.5::::iqn.2024-01.lan:disk0", CHAPUsername: "u", CHAPPassword: "p"}
	if err := store.CreateSANTarget(target); err != nil {
		t.Fatalf("CreateSANTarget: %v", err)
	}
	if target.ID == 0 {
		t.Fatal("expected an ID after create")
	}

	got, err := store.GetSANTarget(target.ID)
	if err != nil {
		t.Fatalf("GetSANTarget: %v", err)
	}
	if got.URI != target.URI || got.CHAPPassword != "p" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	if err := store.UpdateSANTarget(target.ID, &models.SANTarget{Name: "lab", URI: "aoe:e0.0", Description: "updated"}); err != nil {
		t.Fatalf("UpdateSANTarget: %v", err)
	}
	got, _ = store.GetSANTarget(target.ID)
	if got.URI != "aoe:e0.0" || got.Description != "updated" || got.CHAPUsername != "" {
		t.Fatalf("update not applied: %+v", got)
	}

	list, err := store.ListSANTargets()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListSANTargets = %v, %v", list, err)
	}
}

func TestSANTargetValidation(t *testing.T) {
	store := newTestStore(t)
	cases := map[string]*models.SANTarget{
		"missing name":      {URI: "iscsi:10.0.0.5::::iqn.x"},
		"missing uri":       {Name: "x"},
		"bad scheme":        {Name: "x", URI: "nfs:10.0.0.5:/export"},
		"whitespace":        {Name: "x", URI: "iscsi:10.0.0.5::::iqn.x y"},
		"password no user":  {Name: "x", URI: "iscsi:10.0.0.5::::iqn.x", CHAPPassword: "p"},
		"newline in secret": {Name: "x", URI: "iscsi:10.0.0.5::::iqn.x", CHAPUsername: "u", CHAPPassword: "p\nq"},
	}
	for name, target := range cases {
		if err := store.CreateSANTarget(target); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	ok := &models.SANTarget{Name: " ok ", URI: " http://10.0.0.5/disk.img "}
	if err := store.CreateSANTarget(ok); err != nil {
		t.Fatalf("http URI should be accepted: %v", err)
	}
	if ok.Name != "ok" || strings.HasPrefix(ok.URI, " ") {
		t.Fatalf("fields should be trimmed: %+v", ok)
	}
}

func TestDeleteSANTargetClearsReferences(t *testing.T) {
	store := newTestStore(t)
	target := &models.SANTarget{Name: "lab", URI: "iscsi:10.0.0.5::::iqn.x"}
	if err := store.CreateSANTarget(target); err != nil {
		t.Fatal(err)
	}
	group := &models.ClientGroup{Name: "rack", SANTargetID: &target.ID, BootFromSAN: true}
	if err := store.CreateClientGroup(group); err != nil {
		t.Fatal(err)
	}
	client := &models.Client{MACAddress: "aa:bb:cc:dd:ee:01", SANTargetID: &target.ID, BootFromSAN: true, ClientGroupID: &group.ID}
	if err := store.CreateClient(client); err != nil {
		t.Fatal(err)
	}
	if err := store.SetNextBootImage(client.MACAddress, target.NextBootValue()); err != nil {
		t.Fatal(err)
	}
	clients, groups, err := store.CountSANTargetUsers(target.ID)
	if err != nil || clients != 1 || groups != 1 {
		t.Fatalf("CountSANTargetUsers = %d, %d, %v", clients, groups, err)
	}

	if err := store.DeleteSANTarget(target.ID); err != nil {
		t.Fatalf("DeleteSANTarget: %v", err)
	}
	if _, err := store.GetSANTarget(target.ID); err == nil {
		t.Fatal("target still exists after delete")
	}
	c, _ := store.GetClient(client.MACAddress)
	if c.SANTargetID != nil || c.BootFromSAN || c.NextBootImage != "" {
		t.Fatalf("client references not cleared: %+v", c)
	}
	g, _ := store.GetClientGroup(group.ID)
	if g.SANTargetID != nil || g.BootFromSAN {
		t.Fatalf("group references not cleared: %+v", g)
	}
}
