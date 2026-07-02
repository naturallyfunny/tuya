package firestore

import (
	"strings"
	"testing"
)

func TestValidateOwner(t *testing.T) {
	valid := []string{
		"user-123",
		"someone@example.com",
		"a",
		strings.Repeat("x", 1500),
		"__x", "x__", "___", // too short to match the reserved __*__ pattern
		".hidden", "..dots",
	}
	for _, owner := range valid {
		if err := validateOwner(owner); err != nil {
			t.Errorf("validateOwner(%q): unexpected error: %v", owner, err)
		}
	}

	invalid := []string{
		"",
		".", "..",
		"tenants/alice",
		strings.Repeat("x", 1501),
		"__reserved__", "____",
	}
	for _, owner := range invalid {
		if err := validateOwner(owner); err == nil {
			t.Errorf("validateOwner(%q): want error, got nil", owner)
		}
	}
}
