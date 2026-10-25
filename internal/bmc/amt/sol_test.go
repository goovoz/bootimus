package amt

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestStartSessionRequestMatchesReference(t *testing.T) {
	want := []byte{0x10, 0x00, 0x00, 0x00, 0x53, 0x4F, 0x4C, 0x20}
	if got := startSessionRequest(); !bytes.Equal(got, want) {
		t.Fatalf("start session = % x, want % x", got, want)
	}
}

func TestAuthQueryRequestMatchesReference(t *testing.T) {
	want := []byte{0x13, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if got := authQueryRequest(); !bytes.Equal(got, want) {
		t.Fatalf("auth query = % x, want % x", got, want)
	}
}

func TestAuthDigestOpeningLayout(t *testing.T) {
	got := authDigestOpening("admin")
	wantLen := len("admin") + len(authURI) + 8
	if got[0] != 0x13 || got[4] != 0x04 {
		t.Fatalf("header = % x", got[:9])
	}
	if n := binary.LittleEndian.Uint32(got[5:9]); int(n) != wantLen {
		t.Fatalf("length = %d, want %d", n, wantLen)
	}
	body := got[9:]
	if int(body[0]) != len("admin") || string(body[1:6]) != "admin" {
		t.Fatalf("username element = % x", body[:6])
	}
	if body[6] != 0 || body[7] != 0 {
		t.Fatalf("realm and nonce must be empty: % x", body[6:8])
	}
	if int(body[8]) != len(authURI) || string(body[9:9+len(authURI)]) != authURI {
		t.Fatalf("uri element = % x", body[8:])
	}
	tail := body[9+len(authURI):]
	if !bytes.Equal(tail, []byte{0, 0, 0, 0}) {
		t.Fatalf("trailing empty elements = % x", tail)
	}
	if len(body) != wantLen {
		t.Fatalf("body length %d, want %d", len(body), wantLen)
	}
}

func TestDigestResponseMatchesRFC2617(t *testing.T) {
	got := digestResponse("admin", "Digest:ABCD", "P@ssw0rd", "nonce123", "cnonce456", "auth")
	ha1 := md5hex("admin:Digest:ABCD:P@ssw0rd")
	ha2 := md5hex("POST:/RedirectionService")
	want := md5hex(ha1 + ":nonce123:00000002:cnonce456:auth:" + ha2)
	if got != want {
		t.Fatalf("digest = %s, want %s", got, want)
	}
}

func TestStartSOLRequestMatchesAmtterm(t *testing.T) {
	got := startSOLRequest(3)
	if len(got) != 24 || got[0] != 0x20 {
		t.Fatalf("frame = % x", got)
	}
	if binary.LittleEndian.Uint32(got[4:8]) != 3 {
		t.Fatalf("sequence = %d", binary.LittleEndian.Uint32(got[4:8]))
	}
	values := []uint16{1000, 100, 0, 10000, 0, 5000}
	for i, want := range values {
		if v := binary.LittleEndian.Uint16(got[8+2*i:]); v != want {
			t.Errorf("parameter %d = %d, want %d", i, v, want)
		}
	}
	if !bytes.Equal(got[20:], []byte{0, 0, 0, 0}) {
		t.Fatalf("reserved tail = % x", got[20:])
	}
}

func TestDataToHostFrame(t *testing.T) {
	want := []byte{0x28, 0, 0, 0, 0x07, 0, 0, 0, 0x02, 0x00, 'h', 'i'}
	if got := dataToHost(7, []byte("hi")); !bytes.Equal(got, want) {
		t.Fatalf("data frame = % x, want % x", got, want)
	}
}

func TestReadFrameHandlesEveryHostMessage(t *testing.T) {
	var stream bytes.Buffer
	stream.Write(append([]byte{0x11, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x02}, 0xaa, 0xbb))
	stream.Write([]byte{0x2b, 0, 0, 0, 1, 0, 0, 0})
	stream.Write([]byte{0x29, 0, 0, 0, 2, 0, 0, 0, 0x03, 0x04})
	stream.Write([]byte{0x2a, 0, 0, 0, 3, 0, 0, 0, 0x05, 0x00, 'h', 'e', 'l', 'l', 'o'})
	stream.Write([]byte{0x21, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	stream.Write([]byte{0x14, 0x01, 0, 0, 0x04, 0x03, 0, 0, 0, 'a', 'b', 'c'})

	typ, frame, err := readFrame(&stream)
	if err != nil || typ != 0x11 || len(frame) != 15 {
		t.Fatalf("start reply: typ=0x%02x len=%d err=%v", typ, len(frame), err)
	}
	typ, frame, err = readFrame(&stream)
	if err != nil || typ != 0x2b || len(frame) != 8 {
		t.Fatalf("heartbeat: typ=0x%02x len=%d err=%v", typ, len(frame), err)
	}
	typ, frame, err = readFrame(&stream)
	if err != nil || typ != 0x29 || len(frame) != 10 {
		t.Fatalf("controls: typ=0x%02x len=%d err=%v", typ, len(frame), err)
	}
	typ, frame, err = readFrame(&stream)
	if err != nil || typ != 0x2a || string(dataFromHost(frame)) != "hello" {
		t.Fatalf("data: typ=0x%02x frame=% x err=%v", typ, frame, err)
	}
	typ, frame, err = readFrame(&stream)
	if err != nil || typ != 0x21 || len(frame) != 23 || frame[1] != 1 {
		t.Fatalf("sol reply: typ=0x%02x len=%d err=%v", typ, len(frame), err)
	}
	typ, frame, err = readFrame(&stream)
	if err != nil || typ != 0x14 {
		t.Fatalf("auth reply: typ=0x%02x err=%v", typ, err)
	}
	status, authType, data := parseAuthReply(frame)
	if status != 1 || authType != 4 || string(data) != "abc" {
		t.Fatalf("auth reply parsed as status=%d type=%d data=%q", status, authType, data)
	}
	if _, _, err := readFrame(&stream); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestReadFrameRejectsUnknownMessage(t *testing.T) {
	if _, _, err := readFrame(bytes.NewReader([]byte{0x99, 0, 0})); err == nil {
		t.Fatal("expected an error for an unknown message type")
	}
}

func TestParseChallenge(t *testing.T) {
	data := []byte{5, 'r', 'e', 'a', 'l', 'm', 3, 'n', 'o', 'n', 4, 'a', 'u', 't', 'h'}
	realm, nonce, qop, err := parseChallenge(data)
	if err != nil || realm != "realm" || nonce != "non" || qop != "auth" {
		t.Fatalf("parsed %q %q %q %v", realm, nonce, qop, err)
	}
	if _, _, _, err := parseChallenge([]byte{9, 'x'}); err == nil {
		t.Fatal("expected a truncation error")
	}
}

type fakeSOLHost struct {
	t        *testing.T
	username string
	password string
	realm    string
	nonce    string
	received chan []byte
}

func (h *fakeSOLHost) serve(conn net.Conn) {
	defer conn.Close()
	readN := func(n int) []byte {
		buf := make([]byte, n)
		if _, err := io.ReadFull(conn, buf); err != nil {
			h.t.Errorf("fake host read: %v", err)
			return nil
		}
		return buf
	}
	if got := readN(8); got == nil || !bytes.Equal(got, startSessionRequest()) {
		h.t.Errorf("fake host: bad start request % x", got)
		return
	}
	conn.Write([]byte{0x11, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x00})

	if got := readN(9); !bytes.Equal(got, authQueryRequest()) {
		h.t.Errorf("fake host: bad auth query % x", got)
		return
	}
	conn.Write([]byte{0x14, 0x00, 0, 0, 0x00, 0x02, 0, 0, 0, 0x01, 0x04})

	head := readN(9)
	if head == nil || head[0] != 0x13 || head[4] != 0x04 {
		h.t.Errorf("fake host: bad digest opening % x", head)
		return
	}
	readN(int(binary.LittleEndian.Uint32(head[5:9])))
	var challenge bytes.Buffer
	putElement(&challenge, h.realm)
	putElement(&challenge, h.nonce)
	putElement(&challenge, "auth")
	reply := []byte{0x14, 0x01, 0, 0, 0x04, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(reply[5:], uint32(challenge.Len()))
	conn.Write(append(reply, challenge.Bytes()...))

	head = readN(9)
	if head == nil {
		return
	}
	body := readN(int(binary.LittleEndian.Uint32(head[5:9])))
	fields := map[int]string{}
	for i := 0; len(body) > 0; i++ {
		n := int(body[0])
		fields[i] = string(body[1 : 1+n])
		body = body[1+n:]
	}
	want := digestResponse(h.username, h.realm, h.password, h.nonce, fields[4], fields[7])
	if fields[0] != h.username || fields[1] != h.realm || fields[2] != h.nonce || fields[3] != authURI || fields[5] != authNonceCount || fields[6] != want {
		h.t.Errorf("fake host: digest fields %v", fields)
		conn.Write([]byte{0x14, 0x01, 0, 0, 0x04, 0, 0, 0, 0})
		return
	}
	conn.Write([]byte{0x14, 0x00, 0, 0, 0x04, 0, 0, 0, 0})

	if got := readN(24); got == nil || got[0] != 0x20 {
		h.t.Errorf("fake host: bad start SOL % x", got)
		return
	}
	solReply := make([]byte, 23)
	solReply[0] = 0x21
	conn.Write(solReply)

	conn.Write([]byte{0x29, 0, 0, 0, 0, 0, 0, 0, 0x03, 0x04})
	conn.Write([]byte{0x2b, 0, 0, 0, 1, 0, 0, 0})
	conn.Write([]byte{0x2a, 0, 0, 0, 2, 0, 0, 0, 0x06, 0x00, 'l', 'o', 'g', 'i', 'n', ':'})

	for {
		head := make([]byte, 1)
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		switch head[0] {
		case msgDataToHost:
			rest := readN(9)
			if rest == nil {
				return
			}
			data := readN(int(binary.LittleEndian.Uint16(rest[7:9])))
			h.received <- data
			echo := append([]byte{0x2a, 0, 0, 0, 9, 0, 0, 0, 0, 0}, data...)
			binary.LittleEndian.PutUint16(echo[8:], uint16(len(data)))
			conn.Write(echo)
		case msgHeartbeat, msgKeepAlivePing, msgKeepAlivePong, msgEndSOL:
			readN(7)
			if head[0] == msgEndSOL {
				conn.Write([]byte{0x23, 0, 0, 0, 0, 0, 0, 0})
				return
			}
		case msgEndSession:
			return
		default:
			h.t.Errorf("fake host: unexpected console message 0x%02x", head[0])
			return
		}
	}
}

func bufioReader(conn net.Conn) *bufio.Reader {
	return bufio.NewReader(conn)
}

func TestOpenConsoleAgainstFakeHost(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	host := &fakeSOLHost{t: t, username: "admin", password: "P@ssw0rd", realm: "Digest:1234", nonce: "n0nce", received: make(chan []byte, 4)}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		host.serve(conn)
	}()

	addr := ln.Addr().(*net.TCPAddr)
	s := &solConn{closed: make(chan struct{})}
	conn, err := net.DialTimeout("tcp", addr.String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	s.conn = conn
	s.reader = bufioReader(conn)
	s.pr, s.pw = io.Pipe()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := s.handshake("admin", "P@ssw0rd"); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	conn.SetDeadline(time.Time{})
	go s.readLoop()

	buf := make([]byte, 64)
	n, err := s.Read(buf)
	if err != nil || string(buf[:n]) != "login:" {
		t.Fatalf("first read = %q, %v", buf[:n], err)
	}
	if _, err := s.Write([]byte("root\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-host.received:
		if string(got) != "root\r" {
			t.Fatalf("host received %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("host did not receive keystrokes")
	}
	n, err = s.Read(buf)
	if err != nil || string(buf[:n]) != "root\r" {
		t.Fatalf("echo read = %q, %v", buf[:n], err)
	}
	s.Close()
}

func TestHandshakeReportsRefusedSession(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.ReadFull(conn, make([]byte, 8))
		conn.Write([]byte{0x11, 0x03, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
		time.Sleep(100 * time.Millisecond)
	}()
	c := &Client{Host: "127.0.0.1", Username: "admin", Password: "x"}
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	s := &solConn{conn: conn, reader: bufioReader(conn), closed: make(chan struct{})}
	s.pr, s.pw = io.Pipe()
	err = s.handshake(c.Username, c.Password)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("disabled")) {
		t.Fatalf("expected a disabled SOL error, got %v", err)
	}
}
