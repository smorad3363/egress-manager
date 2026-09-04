package domain

import (
	"fmt"
	"strings"
)

type XrayBinding struct {
	ID          ID     `json:"id"`
	InboundTag  string `json:"inbound_tag"`
	OutboundTag string `json:"outbound_tag"`
	Enabled     bool   `json:"enabled"`
}

func (binding XrayBinding) Validate() error {
	return joinErrors(
		binding.ID.Validate("Xray binding id"),
		ValidateXrayTag(binding.InboundTag),
		ValidateXrayTag(binding.OutboundTag),
	)
}

func ValidateXrayTag(tag string) error {
	if tag == "" || tag != strings.TrimSpace(tag) || len(tag) > 128 {
		return fmt.Errorf("Xray tag must be 1 to 128 trimmed bytes")
	}
	for _, character := range tag {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("Xray tag contains control characters")
		}
	}
	return nil
}
