package proxydhcp

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/iana"
)

func TestRecorderKeepsNewestEvents(t *testing.T) {
	r := NewRecorder(3)
	for i := 0; i < 5; i++ {
		r.Record(Event{MAC: string(rune('a' + i))})
	}
	got := r.Events()
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].MAC != "c" || got[2].MAC != "e" {
		t.Errorf("events = %v, want c..e", got)
	}
	if got[0].Time.IsZero() {
		t.Error("Record must stamp events without a time")
	}
	r.Clear()
	if len(r.Events()) != 0 {
		t.Error("Clear must empty the buffer")
	}
}

func TestRecorderSubscribersReceiveEvents(t *testing.T) {
	r := NewRecorder(10)
	ch := r.Subscribe()
	r.Record(Event{MAC: "aa:bb:cc:dd:ee:ff"})
	select {
	case ev := <-ch:
		if ev.MAC != "aa:bb:cc:dd:ee:ff" {
			t.Errorf("mac = %q", ev.MAC)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive the event")
	}
	r.Unsubscribe(ch)
	if _, open := <-ch; open {
		t.Error("channel must be closed after Unsubscribe")
	}
	r.Unsubscribe(ch)
}

func TestEvaluateReportsIgnoredVendorClass(t *testing.T) {
	s := testServer(t)
	req := discover(t, "00:11:22:33:44:55", "MSFT 5.0")
	_, ev, ok := s.evaluate(req)
	if ok {
		t.Fatal("non-PXE client must not get a reply")
	}
	if ev.Outcome != OutcomeIgnored || !strings.Contains(ev.Decision, "MSFT 5.0") {
		t.Errorf("event = %+v", ev)
	}
	if ev.MAC != "00:11:22:33:44:55" || ev.MessageType != "DISCOVER" {
		t.Errorf("event = %+v", ev)
	}
}

func TestEvaluateReportsIgnoredMessageType(t *testing.T) {
	s := testServer(t)
	req := discover(t, "00:11:22:33:44:55", "PXEClient:Arch:00000:UNDI:002001",
		dhcpv4.WithMessageType(dhcpv4.MessageTypeRelease))
	_, ev, ok := s.evaluate(req)
	if ok {
		t.Fatal("RELEASE must not get a reply")
	}
	if ev.Outcome != OutcomeIgnored || !strings.Contains(ev.Decision, "RELEASE") {
		t.Errorf("event = %+v", ev)
	}
}

func TestEvaluateDescribesReply(t *testing.T) {
	s := testServer(t)
	s.cfg.OmitVendorOption = func(net.HardwareAddr) bool { return true }
	guid := append([]byte{0}, []byte{0x44, 0x45, 0x44, 0x45, 0x44, 0x45, 0x44, 0x45, 0x44, 0x45, 0x44, 0x45, 0x44, 0x45, 0x44, 0x45}...)
	req := discover(t, "00:11:22:33:44:55", "PXEClient:Arch:00007:UNDI:003016",
		dhcpv4.WithOption(dhcpv4.OptClientArch(iana.EFI_X86_64)),
		dhcpv4.WithOption(dhcpv4.OptGeneric(dhcpv4.OptionClientMachineIdentifier, guid)))
	req.GatewayIPAddr = net.ParseIP("10.0.0.1").To4()
	_, ev, ok := s.evaluate(req)
	if !ok {
		t.Fatal("expected a reply")
	}
	if ev.Bootfile != DefaultBootfileUEFI || ev.ReplyType != "OFFER" || ev.Arch != int(iana.EFI_X86_64) {
		t.Errorf("event = %+v", ev)
	}
	if !ev.VendorOptionOmitted || ev.Relay != "10.0.0.1" || ev.ClientGUID == "" {
		t.Errorf("event = %+v", ev)
	}
}

func TestHandleRecordsSentReply(t *testing.T) {
	s := testServer(t)
	rec := NewRecorder(10)
	s.cfg.Observer = rec.Record
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skip("no loopback UDP available")
	}
	defer conn.Close()
	req := discover(t, "00:11:22:33:44:55", "PXEClient:Arch:00000:UNDI:002001")
	src := conn.LocalAddr().(*net.UDPAddr)
	s.handle(conn, src, req, false)
	events := rec.Events()
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.Outcome != OutcomeReplied || ev.Port != 4011 || ev.ReplyTo != src.String() || ev.Bootfile != DefaultBootfileBIOS {
		t.Errorf("event = %+v", ev)
	}
}
