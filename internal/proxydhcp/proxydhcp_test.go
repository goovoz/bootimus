package proxydhcp

import (
	"net"
	"testing"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/iana"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	return &Server{cfg: Config{
		ServerIP:       net.ParseIP("192.168.203.111").To4(),
		ServerHTTPPort: 8080,
		BootfileBIOS:   DefaultBootfileBIOS,
		BootfileUEFI:   DefaultBootfileUEFI,
		BootfileARM64:  DefaultBootfileARM64,
	}}
}

func discover(t *testing.T, mac string, vci string, opts ...dhcpv4.Modifier) *dhcpv4.DHCPv4 {
	t.Helper()
	hw, err := net.ParseMAC(mac)
	if err != nil {
		t.Fatal(err)
	}
	modifiers := append([]dhcpv4.Modifier{
		dhcpv4.WithMessageType(dhcpv4.MessageTypeDiscover),
		dhcpv4.WithOption(dhcpv4.OptClassIdentifier(vci)),
	}, opts...)
	req, err := dhcpv4.New(modifiers...)
	if err != nil {
		t.Fatal(err)
	}
	req.ClientHWAddr = hw
	return req
}

func TestARM64ClientViaOption93(t *testing.T) {
	s := testServer(t)
	req := discover(t, "dc:a6:32:12:34:56", "PXEClient:Arch:00011:UNDI:003000",
		dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_ARM64)))

	resp, bootfile, ok := s.buildReply(req)
	if !ok {
		t.Fatal("expected a reply")
	}
	if bootfile != DefaultBootfileARM64 {
		t.Errorf("bootfile = %q, want %q", bootfile, DefaultBootfileARM64)
	}
	if resp.BootFileName != DefaultBootfileARM64 {
		t.Errorf("BootFileName = %q, want %q", resp.BootFileName, DefaultBootfileARM64)
	}
}

func TestARM64ClientWithoutOption93FallsBackToVCI(t *testing.T) {
	s := testServer(t)
	req := discover(t, "dc:a6:32:12:34:56", "PXEClient:Arch:00011:UNDI:003000")

	_, bootfile, ok := s.buildReply(req)
	if !ok {
		t.Fatal("expected a reply")
	}
	if bootfile != DefaultBootfileARM64 {
		t.Errorf("bootfile = %q, want %q (arch must come from the VCI when option 93 is absent)", bootfile, DefaultBootfileARM64)
	}
}

func TestX64UEFIClient(t *testing.T) {
	s := testServer(t)
	for _, arch := range []iana.Arch{iana.EFI_X86_64, iana.EFI_BC, iana.EFI_IA32} {
		req := discover(t, "00:11:22:33:44:55", "PXEClient:Arch:00007:UNDI:003016",
			dhcpv4.WithOption(dhcpv4.OptClientArch(arch)))
		_, bootfile, ok := s.buildReply(req)
		if !ok || bootfile != DefaultBootfileUEFI {
			t.Errorf("arch %d: bootfile = %q, want %q", arch, bootfile, DefaultBootfileUEFI)
		}
	}
}

func TestBIOSClientGetsBIOSFileAndPXEOpts(t *testing.T) {
	s := testServer(t)
	req := discover(t, "00:11:22:33:44:55", "PXEClient:Arch:00000:UNDI:002001",
		dhcpv4.WithOption(dhcpv4.OptClientArch(iana.INTEL_X86PC)))

	resp, bootfile, ok := s.buildReply(req)
	if !ok || bootfile != DefaultBootfileBIOS {
		t.Fatalf("bootfile = %q, want %q", bootfile, DefaultBootfileBIOS)
	}
	if string(resp.GetOneOption(dhcpv4.OptionVendorSpecificInformation)) == "Raspberry Pi Boot" {
		t.Error("non-Pi BIOS client must not receive the Raspberry Pi vendor option")
	}
}

func TestPiFirmwareStageGetsPiVendorOption(t *testing.T) {
	s := testServer(t)
	req := discover(t, "dc:a6:32:12:34:56", "PXEClient:Arch:00000:UNDI:002001",
		dhcpv4.WithOption(dhcpv4.OptClientArch(iana.INTEL_X86PC)))

	resp, _, ok := s.buildReply(req)
	if !ok {
		t.Fatal("expected a reply")
	}
	if string(resp.GetOneOption(dhcpv4.OptionVendorSpecificInformation)) != "Raspberry Pi Boot" {
		t.Error("Pi firmware stage must receive the Raspberry Pi Boot vendor option")
	}
}

func TestPiUEFIStageKeepsStandardPXEOpts(t *testing.T) {
	s := testServer(t)
	req := discover(t, "dc:a6:32:12:34:56", "PXEClient:Arch:00011:UNDI:003000",
		dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_ARM64)))

	resp, bootfile, ok := s.buildReply(req)
	if !ok {
		t.Fatal("expected a reply")
	}
	if bootfile != DefaultBootfileARM64 {
		t.Errorf("bootfile = %q, want %q", bootfile, DefaultBootfileARM64)
	}
	if string(resp.GetOneOption(dhcpv4.OptionVendorSpecificInformation)) == "Raspberry Pi Boot" {
		t.Error("Pi UEFI stage must receive standard PXE vendor options, not the firmware-stage string")
	}
}

func TestNonPXEClientIgnored(t *testing.T) {
	s := testServer(t)
	req := discover(t, "00:11:22:33:44:55", "MSFT 5.0")
	if _, _, ok := s.buildReply(req); ok {
		t.Error("expected non-PXE client to be ignored")
	}
}

func TestArchFromVCI(t *testing.T) {
	cases := []struct {
		vci  string
		want iana.Arch
		ok   bool
	}{
		{"PXEClient:Arch:00011:UNDI:003000", iana.EFI_ARM64, true},
		{"PXEClient:Arch:00000:UNDI:002001", iana.INTEL_X86PC, true},
		{"PXEClient:Arch:00007:UNDI:003016", iana.EFI_X86_64, true},
		{"PXEClient", 0, false},
		{"PXEClient:Arch:bogus:UNDI:003000", 0, false},
	}
	for _, c := range cases {
		got, ok := archFromVCI(c.vci)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("archFromVCI(%q) = %d,%v want %d,%v", c.vci, got, ok, c.want, c.ok)
		}
	}
}

func TestHTTPClientGetsBootURL(t *testing.T) {
	s := testServer(t)
	cases := []struct {
		arch iana.Arch
		want string
	}{
		{iana.EFI_X86_64_HTTP, "http://192.168.203.111:8080/bootimus.efi"},
		{iana.EFI_X86_HTTP, "http://192.168.203.111:8080/bootimus.efi"},
		{iana.EFI_ARM64_HTTP, "http://192.168.203.111:8080/bootimus-arm64.efi"},
	}
	for _, c := range cases {
		req := discover(t, "00:11:22:33:44:55", "HTTPClient:Arch:00016:UNDI:003001",
			dhcpv4.WithOption(dhcpv4.OptClientArch(c.arch)))
		resp, bootfile, ok := s.buildReply(req)
		if !ok {
			t.Fatalf("arch %d: expected a reply", c.arch)
		}
		if bootfile != c.want || resp.BootFileName != c.want {
			t.Errorf("arch %d: bootfile = %q, BootFileName = %q, want %q", c.arch, bootfile, resp.BootFileName, c.want)
		}
		if got := resp.ClassIdentifier(); got != "HTTPClient" {
			t.Errorf("arch %d: class identifier = %q, want HTTPClient", c.arch, got)
		}
		if resp.Options.Has(dhcpv4.OptionTFTPServerName) || resp.Options.Has(dhcpv4.OptionVendorSpecificInformation) {
			t.Errorf("arch %d: HTTP clients must not receive TFTP or PXE vendor options", c.arch)
		}
	}
}

func TestHTTPClientWithoutOption93FallsBackToVCI(t *testing.T) {
	s := testServer(t)
	req := discover(t, "00:11:22:33:44:55", "HTTPClient:Arch:00016:UNDI:003001")

	_, bootfile, ok := s.buildReply(req)
	if !ok {
		t.Fatal("expected a reply")
	}
	if want := "http://192.168.203.111:8080/bootimus.efi"; bootfile != want {
		t.Errorf("bootfile = %q, want %q", bootfile, want)
	}
}

func TestHTTPBootURLNormalisesPath(t *testing.T) {
	s := testServer(t)
	s.cfg.BootfileUEFI = "/ipxe/snp boot.efi"
	req := discover(t, "00:11:22:33:44:55", "HTTPClient:Arch:00016:UNDI:003001",
		dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_X86_64_HTTP)))

	_, bootfile, ok := s.buildReply(req)
	if !ok {
		t.Fatal("expected a reply")
	}
	if want := "http://192.168.203.111:8080/ipxe/snp%20boot.efi"; bootfile != want {
		t.Errorf("bootfile = %q, want %q", bootfile, want)
	}
}

func TestPXEClientKeepsPXEClassAndTFTPServer(t *testing.T) {
	s := testServer(t)
	req := discover(t, "00:11:22:33:44:55", "PXEClient:Arch:00007:UNDI:003016",
		dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_X86_64)))

	resp, _, ok := s.buildReply(req)
	if !ok {
		t.Fatal("expected a reply")
	}
	if got := resp.ClassIdentifier(); got != "PXEClient" {
		t.Errorf("class identifier = %q, want PXEClient", got)
	}
	if got := resp.TFTPServerName(); got != "192.168.203.111" {
		t.Errorf("TFTP server name = %q, want 192.168.203.111", got)
	}
}

func TestReplyAddr(t *testing.T) {
	src := &net.UDPAddr{IP: net.ParseIP("10.0.5.20"), Port: 68}
	relay := net.ParseIP("10.0.5.1").To4()

	direct := discover(t, "aa:bb:cc:dd:ee:ff", "PXEClient:Arch:00007")
	if got := replyAddr(src, direct, true); !got.IP.Equal(net.IPv4bcast) || got.Port != 68 {
		t.Errorf("direct broadcast reply went to %v, want 255.255.255.255:68", got)
	}

	relayed := discover(t, "aa:bb:cc:dd:ee:ff", "PXEClient:Arch:00007")
	relayed.GatewayIPAddr = relay
	if got := replyAddr(src, relayed, true); !got.IP.Equal(relay) || got.Port != 67 {
		t.Errorf("relayed reply went to %v, want %v:67", got, relay)
	}

	if got := replyAddr(src, relayed, false); got != src {
		t.Errorf("port 4011 reply went to %v, want the requester %v", got, src)
	}
}

func TestVendorOptionCanBeOmitted(t *testing.T) {
	uefi := func() *dhcpv4.DHCPv4 {
		return discover(t, "00:23:24:c2:9f:c8", "PXEClient:Arch:00007:UNDI:003016",
			dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_X86_64)))
	}
	hasVendorOption := func(s *Server, req *dhcpv4.DHCPv4) bool {
		resp, _, ok := s.buildReply(req)
		if !ok {
			t.Fatal("expected a reply")
		}
		return resp.GetOneOption(dhcpv4.OptionVendorSpecificInformation) != nil
	}

	s := testServer(t)
	if !hasVendorOption(s, uefi()) {
		t.Error("option 43 should be sent by default")
	}

	s.cfg.NoVendorOption = true
	if hasVendorOption(s, uefi()) {
		t.Error("option 43 should be omitted when disabled globally")
	}

	s = testServer(t)
	s.cfg.OmitVendorOption = func(mac net.HardwareAddr) bool {
		return mac.String() == "00:23:24:c2:9f:c8"
	}
	if hasVendorOption(s, uefi()) {
		t.Error("option 43 should be omitted for a client that opts out")
	}
	other := discover(t, "00:11:22:33:44:55", "PXEClient:Arch:00007:UNDI:003016",
		dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_X86_64)))
	if !hasVendorOption(s, other) {
		t.Error("option 43 should still be sent to other clients")
	}

	s = testServer(t)
	s.cfg.NoVendorOption = true
	pi := discover(t, "dc:a6:32:12:34:56", "PXEClient:Arch:00000:UNDI:002001",
		dhcpv4.WithOption(dhcpv4.OptClientArch(iana.INTEL_X86PC)))
	if !hasVendorOption(s, pi) {
		t.Error("Raspberry Pi firmware must keep its vendor option even when option 43 is disabled")
	}
}

func TestBootfileForPassesClientHardwareAddress(t *testing.T) {
	mac, err := net.ParseMAC("02:00:00:00:00:01")
	if err != nil {
		t.Fatalf("ParseMAC: %v", err)
	}
	request, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_X86_64)),
	)
	if err != nil {
		t.Fatalf("New DHCP request: %v", err)
	}

	server := &Server{cfg: Config{
		BootfileBIOS:  DefaultBootfileBIOS,
		BootfileUEFI:  DefaultBootfileUEFI,
		BootfileARM64: DefaultBootfileARM64,
		Bootfiles: func(clientHWAddr net.HardwareAddr) (string, string, string) {
			if clientHWAddr.String() != mac.String() {
				t.Fatalf("callback MAC = %q, want %q", clientHWAddr, mac)
			}
			return "custom/legacy.kpxe", "custom/client.efi", "custom/client-arm64.efi"
		},
	}}

	if got := server.bootfileFor(request); got != "custom/client.efi" {
		t.Fatalf("bootfileFor = %q, want custom/client.efi", got)
	}
}
