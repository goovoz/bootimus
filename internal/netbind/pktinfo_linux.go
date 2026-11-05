//go:build linux

package netbind

import (
	"encoding/binary"
	"net"
	"unsafe"

	"golang.org/x/sys/unix"
)

// [bootimus-fork L1] fork-patch: proxydhcp-reply-via-ingress
// EnablePacketInfo asks the kernel to attach IP_PKTINFO to every datagram, so a
// reply can be sent back out of the interface the request arrived on.
//
// Without it a DHCP OFFER/ACK addressed to 255.255.255.255:68 follows the
// routing table and leaves through the default-route interface. On a host with
// more than one NIC (docker bridges, a management port) that is not the LAN the
// client sits on, so the reply is logged as sent and never arrives — the client
// times out and falls back to local disk.
func EnablePacketInfo(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_PKTINFO, 1)
	}); err != nil {
		return err
	}
	return sockErr
}

// ReadFromUDP is (*net.UDPConn).ReadFromUDP plus the index of the interface the
// datagram arrived on. ifindex is 0 when the kernel did not report it (IP_PKTINFO
// not enabled, or a non-IPv4 socket).
func ReadFromUDP(conn *net.UDPConn, buf []byte) (int, *net.UDPAddr, int, error) {
	oob := make([]byte, unix.CmsgSpace(12))
	n, oobn, _, src, err := conn.ReadMsgUDP(buf, oob)
	if err != nil {
		return n, src, 0, err
	}
	msgs, perr := unix.ParseSocketControlMessage(oob[:oobn])
	if perr != nil {
		return n, src, 0, nil
	}
	for i := range msgs {
		if msgs[i].Header.Level != unix.IPPROTO_IP || msgs[i].Header.Type != unix.IP_PKTINFO {
			continue
		}
		if len(msgs[i].Data) < 12 {
			continue
		}
		return n, src, int(int32(nativeUint32(msgs[i].Data[0:4]))), nil
	}
	return n, src, 0, nil
}

// WriteToUDP sends b to dst out of the interface with the given index, using
// srcIP as the source address.
func WriteToUDP(conn *net.UDPConn, b []byte, dst *net.UDPAddr, ifindex int, srcIP net.IP) (int, error) {
	oob := make([]byte, unix.CmsgSpace(12))
	hdr := (*unix.Cmsghdr)(unsafe.Pointer(&oob[0]))
	hdr.Level = unix.IPPROTO_IP
	hdr.Type = unix.IP_PKTINFO
	hdr.SetLen(unix.CmsgLen(12))
	data := oob[unix.CmsgLen(0):]
	nativePutUint32(data[0:4], uint32(int32(ifindex)))
	if v4 := srcIP.To4(); v4 != nil {
		copy(data[4:8], v4)
	}
	n, _, err := conn.WriteMsgUDP(b, oob, dst)
	return n, err
}

// InterfaceIPv4ByIndex returns the first IPv4 address of an interface.
func InterfaceIPv4ByIndex(index int) net.IP {
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return nil
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if v4 := ipnet.IP.To4(); v4 != nil {
				return v4
			}
		}
	}
	return nil
}

// Linux on the platforms Bootimus ships (amd64, arm64) is little endian; the
// helpers below keep the decoding explicit instead of pulling in unsafe casts
// of the whole struct.
func nativeUint32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

func nativePutUint32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }
