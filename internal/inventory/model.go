// Package inventory inspects Linux network state without mutating it.
package inventory

import "time"

const (
	maximumInterfaces   = 512
	maximumAddresses    = 2048
	maximumRoutes       = 4096
	maximumPolicyRules  = 4096
	maximumListeners    = 4096
	maximumCapabilities = 32
)

type Inventory struct {
	GeneratedAt  time.Time    `json:"generated_at"`
	Interfaces   []Interface  `json:"interfaces"`
	Routes       []Route      `json:"routes"`
	PolicyRules  []PolicyRule `json:"policy_rules"`
	Listeners    []Listener   `json:"listeners"`
	DNS          DNSState     `json:"dns"`
	Capabilities []Capability `json:"capabilities"`
	Warnings     []string     `json:"warnings"`
}

type Interface struct {
	Name      string    `json:"name"`
	State     string    `json:"state"`
	MTU       int       `json:"mtu"`
	Addresses []Address `json:"addresses"`
}

type Address struct {
	Family string `json:"family"`
	CIDR   string `json:"cidr"`
	Scope  string `json:"scope"`
}

type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway,omitempty"`
	Interface   string `json:"interface,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	Table       string `json:"table,omitempty"`
	Metric      int    `json:"metric,omitempty"`
	Default     bool   `json:"default"`
}

type PolicyRule struct {
	Priority    int    `json:"priority"`
	Protocol    string `json:"protocol,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination,omitempty"`
	Table       string `json:"table"`
	FWMark      string `json:"fwmark,omitempty"`
	Input       string `json:"input_interface,omitempty"`
	Output      string `json:"output_interface,omitempty"`
}

type Listener struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     uint16 `json:"port"`
	Process  string `json:"process,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

type DNSState struct {
	Source        string   `json:"source"`
	Servers       []string `json:"servers"`
	SearchDomains []string `json:"search_domains"`
}

type Capability struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Running   bool   `json:"running"`
	Version   string `json:"version,omitempty"`
	Backend   string `json:"backend,omitempty"`
}
