package server

import (
	"strings"
	"testing"

	"bootimus/internal/models"
)

func sanTarget() *models.SANTarget {
	return &models.SANTarget{
		ID:           4,
		Name:         "lab-disk",
		URI:          "iscsi:10.0.0.5::::iqn.2024-01.lan:disk0",
		InitiatorIQN: "iqn.2024-01.lan:client-{{MAC}}",
	}
}

func TestBuildSANEntryWithoutCHAP(t *testing.T) {
	mb := testMenuBuilder(nil)
	mb.sanTarget = sanTarget()

	out := mb.Build()
	for _, want := range []string{
		"item --gap -- SAN:\n",
		"item san Boot from SAN: lab-disk\n",
		":san\n",
		"set initiator-iqn iqn.2024-01.lan:client-aabbccddeeff\n",
		"sanboot --no-describe --drive 0x80 iscsi:10.0.0.5::::iqn.2024-01.lan:disk0 || goto failed\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("menu missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"set username", "set password", "set reverse-username"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("menu must not emit %q without CHAP:\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, "choose --default local ") {
		t.Errorf("SAN entry must not become the default unless asked:\n%s", out)
	}
}

func TestBuildSANEntryWithCHAP(t *testing.T) {
	mb := testMenuBuilder(nil)
	target := sanTarget()
	target.InitiatorIQN = ""
	target.CHAPUsername = "boot"
	target.CHAPPassword = "s3cret"
	target.ReverseCHAPUsername = "server"
	target.ReverseCHAPPassword = "r3verse"
	mb.sanTarget = target

	out := mb.Build()
	section := out[strings.Index(out, ":san\n"):]
	for _, want := range []string{
		"set username boot\n",
		"set password s3cret\n",
		"set reverse-username server\n",
		"set reverse-password r3verse\n",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SAN section missing %q:\n%s", want, section)
		}
	}
	if strings.Contains(section, "set initiator-iqn") {
		t.Errorf("empty initiator IQN must not be set:\n%s", section)
	}
}

func TestBuildSANDefaultWinsOverThemeButNotNextBoot(t *testing.T) {
	mb := testMenuBuilder(nil)
	mb.sanTarget = sanTarget()
	mb.sanDefault = true
	mb.theme = &models.MenuTheme{DefaultMenuItem: "shell", MenuTimeout: 5}

	out := mb.Build()
	if !strings.Contains(out, "choose --default san --timeout 5000") {
		t.Fatalf("expected SAN to be the default item:\n%s", out)
	}

	mb.nextBootImageID = 7
	mb.images = []models.Image{{ID: 7, Name: "Ubuntu", Filename: "ubuntu.iso", Enabled: true}}
	out = mb.Build()
	if !strings.HasPrefix(out, "#!ipxe\n\ngoto iso7\n\n") {
		t.Fatalf("a pending next-boot image must still win:\n%s", out)
	}
}

func TestBuildNextBootSANBypassesMenu(t *testing.T) {
	mb := testMenuBuilder(nil)
	mb.sanTarget = sanTarget()
	mb.nextBootSAN = true

	out := mb.Build()
	if !strings.HasPrefix(out, "#!ipxe\n\ngoto san\n\n") {
		t.Fatalf("expected a one-shot SAN boot to jump straight to the SAN section:\n%s", out)
	}
	if !strings.Contains(out, ":san\n") {
		t.Fatalf("SAN section missing:\n%s", out)
	}
}

func TestBuildNoSANTargetEmitsNothing(t *testing.T) {
	out := testMenuBuilder(nil).Build()
	if strings.Contains(out, "san") {
		t.Fatalf("menu without a SAN target must not mention SAN:\n%s", out)
	}
}
