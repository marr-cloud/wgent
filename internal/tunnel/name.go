// Package tunnel contains pure helpers for WireGuard tunnel names.
package tunnel

import (
	"errors"
	"fmt"
	"regexp"
)

var (
	// ErrInvalidName is returned when a tunnel name does not match the allowed pattern.
	ErrInvalidName = errors.New("invalid tunnel name")
	// ErrNoDefault is returned when no tunnel is given and none is configured as default.
	ErrNoDefault = errors.New("no tunnel given and no default_tunnel configured")
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_=+.-]{1,32}$`)

// ValidateName reports whether name is a valid tunnel name.
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

// Resolve picks the tunnel from arg, falling back to defaultTunnel, and validates it.
func Resolve(arg, defaultTunnel string) (string, error) {
	name := arg
	if name == "" {
		name = defaultTunnel
	}
	if name == "" {
		return "", ErrNoDefault
	}
	if err := ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}
