package config

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
)

const (
	DefaultPortMinimum uint16 = 20000
	DefaultPortMaximum uint16 = 60999
	portRandomAttempts        = 64
)

var ErrNoAvailablePort = errors.New("no available panel port")

type ListenerFactory func(network, address string) (net.Listener, error)

type PortSelector struct {
	Random io.Reader
	Listen ListenerFactory
	Min    uint16
	Max    uint16
}

func NewPortSelector() PortSelector {
	return PortSelector{
		Random: rand.Reader,
		Listen: net.Listen,
		Min:    DefaultPortMinimum,
		Max:    DefaultPortMaximum,
	}
}

func (selector PortSelector) Select(address netip.Addr, excluded map[uint16]struct{}) (uint16, error) {
	if !address.IsValid() {
		return 0, fmt.Errorf("listen address is invalid")
	}
	if selector.Random == nil || selector.Listen == nil || selector.Min == 0 || selector.Min > selector.Max {
		return 0, fmt.Errorf("port selector is invalid")
	}

	width := uint32(selector.Max) - uint32(selector.Min) + 1
	for attempt := 0; attempt < portRandomAttempts; attempt++ {
		var randomBytes [4]byte
		if _, err := io.ReadFull(selector.Random, randomBytes[:]); err != nil {
			return 0, fmt.Errorf("read secure randomness: %w", err)
		}
		candidate := uint16(uint32(selector.Min) + binary.BigEndian.Uint32(randomBytes[:])%width)
		if selector.available(address, candidate, excluded) {
			return candidate, nil
		}
	}

	for candidate := uint32(selector.Min); candidate <= uint32(selector.Max); candidate++ {
		port := uint16(candidate)
		if selector.available(address, port, excluded) {
			return port, nil
		}
	}
	return 0, ErrNoAvailablePort
}

func (selector PortSelector) available(address netip.Addr, port uint16, excluded map[uint16]struct{}) bool {
	if _, found := excluded[port]; found {
		return false
	}
	listener, err := selector.Listen("tcp", net.JoinHostPort(address.String(), fmt.Sprint(port)))
	if err != nil {
		return false
	}
	return listener.Close() == nil
}

func ProtectedInstallerPorts(sshPorts ...uint16) map[uint16]struct{} {
	excluded := map[uint16]struct{}{22: {}}
	for _, port := range sshPorts {
		if port != 0 {
			excluded[port] = struct{}{}
		}
	}
	return excluded
}
