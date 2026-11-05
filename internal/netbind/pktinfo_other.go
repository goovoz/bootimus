//go:build !linux

package netbind

import (
	"errors"
	"net"
)

// [bootimus-fork L1] fork-patch: proxydhcp-reply-via-ingress
// Replying out of the ingress interface is a Linux-only optimisation; other
// platforms keep the plain socket behaviour.
func EnablePacketInfo(*net.UDPConn) error {
	return errors.New("IP_PKTINFO is only supported on Linux")
}

func ReadFromUDP(conn *net.UDPConn, buf []byte) (int, *net.UDPAddr, int, error) {
	n, src, err := conn.ReadFromUDP(buf)
	return n, src, 0, err
}

func WriteToUDP(conn *net.UDPConn, b []byte, dst *net.UDPAddr, _ int, _ net.IP) (int, error) {
	return conn.WriteToUDP(b, dst)
}

func InterfaceIPv4ByIndex(int) net.IP { return nil }
