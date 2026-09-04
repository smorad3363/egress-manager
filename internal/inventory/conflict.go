package inventory

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

type Binding struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     uint16 `json:"port"`
}

type Conflict struct {
	Requested Binding  `json:"requested"`
	Existing  Listener `json:"existing"`
	Reason    string   `json:"reason"`
}

func DetectConflicts(requested []Binding, listeners []Listener) ([]Conflict, error) {
	conflicts := make([]Conflict, 0)
	for _, binding := range requested {
		protocol := strings.ToLower(binding.Protocol)
		if (protocol != "tcp" && protocol != "udp") || binding.Port == 0 {
			return nil, fmt.Errorf("requested binding has an invalid protocol or port")
		}
		if !validBindAddress(binding.Address) {
			return nil, fmt.Errorf("requested binding has an invalid address")
		}
		binding.Protocol = protocol
		for _, listener := range listeners {
			if strings.ToLower(listener.Protocol) != protocol || listener.Port != binding.Port || !addressesOverlap(binding.Address, listener.Address) {
				continue
			}
			conflicts = append(conflicts, Conflict{Requested: binding, Existing: listener, Reason: "address and port are already occupied"})
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Requested.Port != conflicts[j].Requested.Port {
			return conflicts[i].Requested.Port < conflicts[j].Requested.Port
		}
		return conflicts[i].Requested.Protocol < conflicts[j].Requested.Protocol
	})
	return conflicts, nil
}

func validBindAddress(address string) bool {
	if address == "*" {
		return true
	}
	_, err := netip.ParseAddr(address)
	return err == nil
}

func addressesOverlap(first, second string) bool {
	if first == "*" || second == "*" || first == "0.0.0.0" || second == "0.0.0.0" || first == "::" || second == "::" {
		return true
	}
	firstAddress, firstErr := netip.ParseAddr(first)
	secondAddress, secondErr := netip.ParseAddr(second)
	return firstErr == nil && secondErr == nil && firstAddress.Unmap() == secondAddress.Unmap()
}
