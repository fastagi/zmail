package config

import "testing"

// TestExternalMailTenantAssignmentLimit: unset takes 1000; a set value must be a whole number from 1
// to 10000, and one that does not parse stops startup.
func TestExternalMailTenantAssignmentLimit(t *testing.T) {
	if limit := (ExternalMailConfig{}).TenantAssignmentLimit(); limit != DefaultExternalMailMaxTenantAssignments {
		t.Fatalf("default = %d", limit)
	}
	if limit := (ExternalMailConfig{MaxTenantAssignments: 250}).TenantAssignmentLimit(); limit != 250 {
		t.Fatalf("set = %d", limit)
	}
	for _, value := range []int{-1, MaxExternalMailMaxTenantAssignments + 1} {
		if err := validateExternalMailConfig(ExternalMailConfig{MaxTenantAssignments: value}, "", false); err == nil {
			t.Errorf("%d accepted", value)
		}
	}
	for _, value := range []int{0, 1, MaxExternalMailMaxTenantAssignments} {
		if err := validateExternalMailConfig(ExternalMailConfig{MaxTenantAssignments: value}, "", false); err != nil {
			t.Errorf("%d refused: %v", value, err)
		}
	}
	if parsed := parseExternalMailMaxSharedConnections("ten"); parsed != -1 {
		t.Fatalf("unparsable = %d", parsed)
	}
}
