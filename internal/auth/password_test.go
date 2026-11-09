package auth

import (
	"strings"
	"testing"
)

func TestPasswordPolicy(t *testing.T) {
	for _, tc := range []struct {
		password string
		valid    bool
	}{
		{"Abcdef1!", true},
		{"Abcde1!", false},
		{"abcdef1!", false},
		{"ABCDEF1!", false},
		{"Abcdefg!", false},
		{"Abcdef12", false},
		{"Abcdef1 ", false},
		{"Abcdef1中", false},
		{"Aa1!" + strings.Repeat("a", 124), true},
		{"Aa1!" + strings.Repeat("a", 125), false},
	} {
		if got := validPassword(tc.password); got != tc.valid {
			t.Errorf("validPassword(%q) = %v; want %v", tc.password, got, tc.valid)
		}
	}
}
