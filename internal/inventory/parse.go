package inventory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type ipAddressDocument struct {
	Name      string `json:"ifname"`
	State     string `json:"operstate"`
	MTU       int    `json:"mtu"`
	Addresses []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
		Scope     string `json:"scope"`
	} `json:"addr_info"`
}

func parseInterfaces(data []byte) ([]Interface, error) {
	var documents []ipAddressDocument
	if err := json.Unmarshal(data, &documents); err != nil {
		return nil, fmt.Errorf("decode interface inventory: %w", err)
	}
	if len(documents) > maximumInterfaces {
		return nil, fmt.Errorf("interface inventory exceeds %d records", maximumInterfaces)
	}
	interfaces := make([]Interface, 0, len(documents))
	addressCount := 0
	for _, document := range documents {
		if document.Name == "" || document.MTU < 0 {
			return nil, fmt.Errorf("interface inventory contains invalid metadata")
		}
		item := Interface{Name: document.Name, State: strings.ToLower(document.State), MTU: document.MTU, Addresses: []Address{}}
		for _, address := range document.Addresses {
			parsed, err := netip.ParseAddr(address.Local)
			if err != nil || address.PrefixLen < 0 || address.PrefixLen > parsed.BitLen() {
				continue
			}
			addressCount++
			if addressCount > maximumAddresses {
				return nil, fmt.Errorf("address inventory exceeds %d records", maximumAddresses)
			}
			prefix := netip.PrefixFrom(parsed.Unmap(), address.PrefixLen)
			item.Addresses = append(item.Addresses, Address{Family: address.Family, CIDR: prefix.String(), Scope: address.Scope})
		}
		interfaces = append(interfaces, item)
	}
	sort.Slice(interfaces, func(i, j int) bool { return interfaces[i].Name < interfaces[j].Name })
	return interfaces, nil
}

type ipRouteDocument struct {
	Destination string          `json:"dst"`
	Gateway     string          `json:"gateway"`
	Device      string          `json:"dev"`
	Protocol    string          `json:"protocol"`
	Table       json.RawMessage `json:"table"`
	Metric      int             `json:"metric"`
}

func parseRoutes(data []byte) ([]Route, error) {
	var documents []ipRouteDocument
	if err := json.Unmarshal(data, &documents); err != nil {
		return nil, fmt.Errorf("decode route inventory: %w", err)
	}
	if len(documents) > maximumRoutes {
		return nil, fmt.Errorf("route inventory exceeds %d records", maximumRoutes)
	}
	routes := make([]Route, 0, len(documents))
	for _, document := range documents {
		destination := document.Destination
		isDefault := destination == "default" || destination == ""
		if destination == "" {
			destination = "default"
		}
		routes = append(routes, Route{
			Destination: destination,
			Gateway:     document.Gateway,
			Interface:   document.Device,
			Protocol:    document.Protocol,
			Table:       rawScalar(document.Table),
			Metric:      document.Metric,
			Default:     isDefault,
		})
	}
	return routes, nil
}

func rawScalar(value json.RawMessage) string {
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return ""
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text
	}
	var number json.Number
	if json.Unmarshal(value, &number) == nil {
		return number.String()
	}
	return ""
}

var processPattern = regexp.MustCompile(`users:\(\(\"([^\"]+)\",pid=([0-9]+)`)

func parseListeners(data []byte) ([]Listener, error) {
	listeners := make([]Listener, 0)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}
		protocol := strings.ToLower(fields[0])
		if protocol != "tcp" && protocol != "udp" {
			continue
		}
		address, port, ok := parseEndpoint(fields[4])
		if !ok {
			continue
		}
		listener := Listener{Protocol: protocol, Address: address, Port: port}
		match := processPattern.FindStringSubmatch(scanner.Text())
		if len(match) == 3 {
			listener.Process = match[1]
			listener.PID, _ = strconv.Atoi(match[2])
		}
		listeners = append(listeners, listener)
		if len(listeners) > maximumListeners {
			return nil, fmt.Errorf("listener inventory exceeds %d records", maximumListeners)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan listener inventory: %w", err)
	}
	return listeners, nil
}

func parseEndpoint(value string) (string, uint16, bool) {
	separator := strings.LastIndexByte(value, ':')
	if separator < 0 || separator == len(value)-1 {
		return "", 0, false
	}
	portValue, err := strconv.ParseUint(value[separator+1:], 10, 16)
	if err != nil || portValue == 0 {
		return "", 0, false
	}
	address := strings.Trim(value[:separator], "[]")
	if address == "*" {
		address = "0.0.0.0"
	}
	return address, uint16(portValue), true
}

func parseDNS(data []byte) DNSState {
	state := DNSState{Source: "/etc/resolv.conf", Servers: []string{}, SearchDomains: []string{}}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			if address, err := netip.ParseAddr(fields[1]); err == nil {
				state.Servers = appendUnique(state.Servers, address.Unmap().String())
			}
		case "search", "domain":
			for _, domain := range fields[1:] {
				if validDNSName(domain) {
					state.SearchDomains = appendUnique(state.SearchDomains, strings.ToLower(strings.TrimSuffix(domain, ".")))
				}
			}
		}
	}
	return state
}

func validDNSName(value string) bool {
	value = strings.TrimSuffix(value, ".")
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, character := range label {
			if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-') {
				return false
			}
		}
	}
	return true
}

func appendUnique(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}
