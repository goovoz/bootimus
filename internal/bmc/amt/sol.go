package amt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"bootimus/internal/bmc"
)

const (
	SOLPort    = 16994
	SOLTLSPort = 16995
)

const (
	msgStartSession      = 0x10
	msgStartSessionReply = 0x11
	msgEndSession        = 0x12
	msgAuthenticate      = 0x13
	msgAuthenticateReply = 0x14
	msgStartSOL          = 0x20
	msgStartSOLReply     = 0x21
	msgEndSOL            = 0x22
	msgEndSOLReply       = 0x23
	msgKeepAlivePing     = 0x24
	msgKeepAlivePong     = 0x25
	msgDataToHost        = 0x28
	msgControlsFromHost  = 0x29
	msgDataFromHost      = 0x2a
	msgHeartbeat         = 0x2b
)

const (
	authQuery  = 0x00
	authDigest = 0x04

	authURI        = "/RedirectionService"
	authNonceCount = "00000002"
	authMethod     = "POST"

	statusSuccess   = 0x00
	statusChallenge = 0x01
)

const (
	solMaxTransmitBuffer = 1000
	solTransmitTimeout   = 100
	solTransmitOverflow  = 0
	solHostRxTimeout     = 10000
	solHostFIFORxFlush   = 0
	solHeartbeatInterval = 5000
	solDataChunk         = 512
	solHandshakeTimeout  = 20 * time.Second
)

var sessionStatusText = map[byte]string{
	0x01: "the redirection request failed",
	0x02: "the redirection session was refused because another console is connected",
	0x03: "the Serial-over-LAN feature is disabled in the AMT configuration",
}

func startSessionRequest() []byte {
	return []byte{msgStartSession, 0, 0, 0, 'S', 'O', 'L', ' '}
}

func endSessionRequest() []byte {
	return []byte{msgEndSession, 0, 0, 0}
}

func authQueryRequest() []byte {
	return []byte{msgAuthenticate, 0, 0, 0, authQuery, 0, 0, 0, 0}
}

func authHeader(authType byte, length int) []byte {
	out := []byte{msgAuthenticate, 0, 0, 0, authType, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(out[5:], uint32(length))
	return out
}

func putElement(buf *bytes.Buffer, s string) {
	buf.WriteByte(byte(len(s)))
	buf.WriteString(s)
}

func authDigestOpening(username string) []byte {
	var body bytes.Buffer
	putElement(&body, username)
	body.Write([]byte{0, 0})
	putElement(&body, authURI)
	body.Write([]byte{0, 0, 0, 0})
	return append(authHeader(authDigest, body.Len()), body.Bytes()...)
}

func authDigestResponse(username, realm, nonce, cnonce, qop, digest string) []byte {
	var body bytes.Buffer
	putElement(&body, username)
	putElement(&body, realm)
	putElement(&body, nonce)
	putElement(&body, authURI)
	putElement(&body, cnonce)
	putElement(&body, authNonceCount)
	putElement(&body, digest)
	putElement(&body, qop)
	return append(authHeader(authDigest, body.Len()), body.Bytes()...)
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func digestResponse(username, realm, password, nonce, cnonce, qop string) string {
	ha1 := md5hex(username + ":" + realm + ":" + password)
	ha2 := md5hex(authMethod + ":" + authURI)
	return md5hex(ha1 + ":" + nonce + ":" + authNonceCount + ":" + cnonce + ":" + qop + ":" + ha2)
}

func newCnonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

func startSOLRequest(seq uint32) []byte {
	out := make([]byte, 24)
	out[0] = msgStartSOL
	binary.LittleEndian.PutUint32(out[4:], seq)
	binary.LittleEndian.PutUint16(out[8:], solMaxTransmitBuffer)
	binary.LittleEndian.PutUint16(out[10:], solTransmitTimeout)
	binary.LittleEndian.PutUint16(out[12:], solTransmitOverflow)
	binary.LittleEndian.PutUint16(out[14:], solHostRxTimeout)
	binary.LittleEndian.PutUint16(out[16:], solHostFIFORxFlush)
	binary.LittleEndian.PutUint16(out[18:], solHeartbeatInterval)
	return out
}

func endSOLRequest(seq uint32) []byte {
	out := make([]byte, 8)
	out[0] = msgEndSOL
	binary.LittleEndian.PutUint32(out[4:], seq)
	return out
}

func dataToHost(seq uint32, data []byte) []byte {
	out := make([]byte, 10+len(data))
	out[0] = msgDataToHost
	binary.LittleEndian.PutUint32(out[4:], seq)
	binary.LittleEndian.PutUint16(out[8:], uint16(len(data)))
	copy(out[10:], data)
	return out
}

func readFrame(r io.Reader) (byte, []byte, error) {
	head := make([]byte, 1)
	if _, err := io.ReadFull(r, head); err != nil {
		return 0, nil, err
	}
	typ := head[0]
	readMore := func(frame []byte, n int) ([]byte, error) {
		more := make([]byte, n)
		if _, err := io.ReadFull(r, more); err != nil {
			return nil, err
		}
		return append(frame, more...), nil
	}
	frame := []byte{typ}
	var err error
	switch typ {
	case msgStartSessionReply:
		if frame, err = readMore(frame, 12); err != nil {
			return typ, nil, err
		}
		if frame[1] != statusSuccess {
			return typ, frame, nil
		}
		frame, err = readMore(frame, int(frame[12]))
	case msgAuthenticateReply:
		if frame, err = readMore(frame, 8); err != nil {
			return typ, nil, err
		}
		n := binary.LittleEndian.Uint32(frame[5:9])
		if n > 4096 {
			return typ, nil, fmt.Errorf("authentication reply of %d bytes is implausible", n)
		}
		frame, err = readMore(frame, int(n))
	case msgStartSOLReply:
		frame, err = readMore(frame, 22)
	case msgEndSOLReply, msgKeepAlivePing, msgKeepAlivePong, msgHeartbeat:
		frame, err = readMore(frame, 7)
	case msgControlsFromHost:
		frame, err = readMore(frame, 9)
	case msgDataFromHost:
		if frame, err = readMore(frame, 9); err != nil {
			return typ, nil, err
		}
		frame, err = readMore(frame, int(binary.LittleEndian.Uint16(frame[8:10])))
	default:
		return typ, nil, fmt.Errorf("unexpected redirection message 0x%02x", typ)
	}
	return typ, frame, err
}

func parseAuthReply(frame []byte) (status, authType byte, data []byte) {
	return frame[1], frame[4], frame[9:]
}

func parseChallenge(data []byte) (realm, nonce, qop string, err error) {
	next := func() (string, error) {
		if len(data) == 0 {
			return "", errors.New("authentication challenge is truncated")
		}
		n := int(data[0])
		if len(data) < 1+n {
			return "", errors.New("authentication challenge is truncated")
		}
		s := string(data[1 : 1+n])
		data = data[1+n:]
		return s, nil
	}
	if realm, err = next(); err != nil {
		return
	}
	if nonce, err = next(); err != nil {
		return
	}
	if len(data) > 0 {
		qop, err = next()
	}
	return
}

func dataFromHost(frame []byte) []byte {
	return frame[10:]
}

type solConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
	seq     uint32
	pr      *io.PipeReader
	pw      *io.PipeWriter
	closed  chan struct{}
	once    sync.Once
}

func (c *Client) OpenConsole(ctx context.Context) (bmc.Console, error) {
	port := SOLPort
	if c.UseTLS {
		port = SOLTLSPort
	}
	dialer := &net.Dialer{Timeout: solHandshakeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(c.Host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("connect to Intel AMT redirection port %d: %w", port, err)
	}
	if c.UseTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: c.Host, InsecureSkipVerify: c.Insecure})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("TLS handshake with Intel AMT redirection port: %w", err)
		}
		conn = tlsConn
	}
	s := &solConn{conn: conn, reader: bufio.NewReader(conn), closed: make(chan struct{})}
	s.pr, s.pw = io.Pipe()
	deadline := time.Now().Add(solHandshakeTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	if err := s.handshake(c.Username, c.Password); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	go s.readLoop()
	return s, nil
}

func (s *solConn) send(frame []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.conn.Write(frame)
	s.seq++
	return err
}

func (s *solConn) nextSeq() uint32 {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.seq
}

func (s *solConn) expect(want byte) ([]byte, error) {
	typ, frame, err := readFrame(s.reader)
	if err != nil {
		return nil, fmt.Errorf("waiting for Intel AMT reply 0x%02x: %w", want, err)
	}
	if typ != want {
		return nil, fmt.Errorf("Intel AMT answered with message 0x%02x instead of 0x%02x", typ, want)
	}
	return frame, nil
}

func (s *solConn) handshake(username, password string) error {
	if err := s.send(startSessionRequest()); err != nil {
		return err
	}
	reply, err := s.expect(msgStartSessionReply)
	if err != nil {
		return err
	}
	if reply[1] != statusSuccess {
		reason, ok := sessionStatusText[reply[1]]
		if !ok {
			reason = fmt.Sprintf("status %d", reply[1])
		}
		return fmt.Errorf("Intel AMT refused the redirection session: %s; check that Serial-over-LAN and the redirection listener are enabled under Remote Control in MEBx or the AMT web UI", reason)
	}

	if err := s.send(authQueryRequest()); err != nil {
		return err
	}
	reply, err = s.expect(msgAuthenticateReply)
	if err != nil {
		return err
	}
	status, authType, methods := parseAuthReply(reply)
	if status != statusSuccess || authType != authQuery {
		return errors.New("Intel AMT did not list its authentication methods")
	}
	if !bytes.Contains(methods, []byte{authDigest}) {
		return errors.New("Intel AMT offers no digest authentication for redirection, which Bootimus requires")
	}

	if err := s.send(authDigestOpening(username)); err != nil {
		return err
	}
	reply, err = s.expect(msgAuthenticateReply)
	if err != nil {
		return err
	}
	status, authType, data := parseAuthReply(reply)
	if status != statusChallenge || authType != authDigest {
		return errors.New("Intel AMT did not issue a digest challenge")
	}
	realm, nonce, qop, err := parseChallenge(data)
	if err != nil {
		return err
	}
	cnonce := newCnonce()
	digest := digestResponse(username, realm, password, nonce, cnonce, qop)
	if err := s.send(authDigestResponse(username, realm, nonce, cnonce, qop, digest)); err != nil {
		return err
	}
	reply, err = s.expect(msgAuthenticateReply)
	if err != nil {
		return err
	}
	if status, _, _ := parseAuthReply(reply); status != statusSuccess {
		return errors.New("Intel AMT rejected the username or password for the redirection session")
	}

	if err := s.send(startSOLRequest(s.nextSeq())); err != nil {
		return err
	}
	reply, err = s.expect(msgStartSOLReply)
	if err != nil {
		return err
	}
	if reply[1] != statusSuccess {
		return fmt.Errorf("Intel AMT refused to start Serial-over-LAN (status %d); SOL may be disabled or another console may already be connected", reply[1])
	}
	return nil
}

func (s *solConn) readLoop() {
	for {
		typ, frame, err := readFrame(s.reader)
		if err != nil {
			select {
			case <-s.closed:
				s.pw.Close()
			default:
				s.pw.CloseWithError(fmt.Errorf("Intel AMT connection lost: %w", err))
			}
			return
		}
		switch typ {
		case msgDataFromHost:
			if _, err := s.pw.Write(dataFromHost(frame)); err != nil {
				return
			}
		case msgHeartbeat, msgKeepAlivePing:
			if err := s.send(frame); err != nil {
				s.pw.CloseWithError(err)
				return
			}
		case msgEndSOLReply:
			s.pw.Close()
			return
		}
	}
}

func (s *solConn) Read(p []byte) (int, error) {
	return s.pr.Read(p)
}

func (s *solConn) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > solDataChunk {
			n = solDataChunk
		}
		if err := s.send(dataToHost(s.nextSeq(), p[:n])); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

func (s *solConn) Close() error {
	s.once.Do(func() {
		close(s.closed)
		s.conn.SetDeadline(time.Now().Add(2 * time.Second))
		s.send(endSOLRequest(s.nextSeq()))
		s.send(endSessionRequest())
		s.conn.Close()
		s.pr.Close()
	})
	return nil
}
