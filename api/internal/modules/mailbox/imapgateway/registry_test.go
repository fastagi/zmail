package imapgateway

import (
	"errors"
	"testing"
)

func TestRegistryPerIPCapCoversAnIPv6Prefix(t *testing.T) {
	registry := newSessionRegistry(10, 10, 2)
	open := func(id uint64, ip string) error { return registry.open(&session{id: id, ip: ip}) }
	if err := open(1, "2001:db8:1:2::1"); err != nil {
		t.Fatal(err)
	}
	if err := open(2, "2001:db8:1:2::2"); err != nil {
		t.Fatal(err)
	}
	if err := open(3, "2001:db8:1:2:ffff::3"); !errors.Is(err, errTooManyFromAddress) {
		t.Fatalf("a third connection from one /64 = %v; want errTooManyFromAddress", err)
	}
	if err := open(4, "2001:db8:1:3::1"); err != nil {
		t.Fatalf("a connection from another /64: %v", err)
	}
	registry.close(&session{id: 1, ip: "2001:db8:1:2::1"}, 0)
	if err := open(5, "2001:db8:1:2::5"); err != nil {
		t.Fatalf("a connection after one in the /64 closed: %v", err)
	}
}
