package discover

import (
	"net"
	"testing"

	"github.com/brutella/dnssd"
)

func TestEntryToLight_PicksIPv4(t *testing.T) {
	e := dnssd.BrowseEntry{
		Name: "Elgato Key Light AE8A",
		Port: 9123,
		IPs:  []net.IP{net.ParseIP("fe80::1"), net.ParseIP("192.168.1.31")},
	}
	l, ok := entryToLight(e)
	if !ok {
		t.Fatal("expected ok")
	}
	if l.Name != "Elgato Key Light AE8A" {
		t.Fatalf("name: %q", l.Name)
	}
	if l.HostPort != "192.168.1.31:9123" {
		t.Fatalf("hostport: %q", l.HostPort)
	}
}

func TestEntryToLight_NoUsableIP(t *testing.T) {
	e := dnssd.BrowseEntry{Name: "x", Port: 9123, IPs: []net.IP{net.ParseIP("fe80::1")}}
	if _, ok := entryToLight(e); ok {
		t.Fatal("expected not ok when only link-local IPv6 present")
	}
}
