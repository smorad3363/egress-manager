// Package domain contains the transport-independent Egress Manager model.
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	idPattern         = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	linuxNamePattern  = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)
	configNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
)

// ID is a stable, API-safe object identifier.
type ID string

func (id ID) Validate(field string) error {
	if !idPattern.MatchString(string(id)) {
		return fmt.Errorf("%s must match %s", field, idPattern.String())
	}
	return nil
}

func validateDisplayName(field, value string) error {
	if value != strings.TrimSpace(value) || value == "" || len(value) > 96 {
		return fmt.Errorf("%s must be 1 to 96 trimmed bytes", field)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains control characters", field)
		}
	}
	return nil
}

func validateLinuxName(field, value string) error {
	if !linuxNamePattern.MatchString(value) {
		return fmt.Errorf("%s is not a safe Linux interface name", field)
	}
	return nil
}

func validateConfigName(field, value string) error {
	if !configNamePattern.MatchString(value) {
		return fmt.Errorf("%s is not a safe configuration identifier", field)
	}
	return nil
}

func joinErrors(errs ...error) error {
	var present []error
	for _, err := range errs {
		if err != nil {
			present = append(present, err)
		}
	}
	return errors.Join(present...)
}
