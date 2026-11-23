package tunnel

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	valid := []string{"office-vpn", "home_vpn", "x.y+z=1", strings.Repeat("a", 32)}
	for _, n := range valid {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", n, err)
		}
	}
	invalid := []string{"", `..\evil`, "a/b", `C:\x.conf`, "has space", strings.Repeat("a", 33)}
	for _, n := range invalid {
		if err := ValidateName(n); !errors.Is(err, ErrInvalidName) {
			t.Errorf("ValidateName(%q) = %v, want ErrInvalidName", n, err)
		}
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		arg, def, want string
		wantErr        error
	}{
		{"work", "home", "work", nil},
		{"", "home", "home", nil},
		{"", "", "", ErrNoDefault},
		{"a/b", "", "", ErrInvalidName},
		{"", "a/b", "", ErrInvalidName},
	}
	for _, tt := range tests {
		got, err := Resolve(tt.arg, tt.def)
		if !errors.Is(err, tt.wantErr) || got != tt.want {
			t.Errorf("Resolve(%q,%q) = %q, %v; want %q, %v", tt.arg, tt.def, got, err, tt.want, tt.wantErr)
		}
	}
}
