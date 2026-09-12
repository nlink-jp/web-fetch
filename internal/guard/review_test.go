package guard

import (
	"context"
	"net/netip"
	"testing"

	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// Findings of the pre-release review (2026-09-12), pinned.

// A resolver may answer with IPv4-mapped IPv6. The mapped form is judged as
// what it embeds and the unmapped literal is dialed.
func TestResolverAnsweringMappedAddresses(t *testing.T) {
	res := &fakeResolver{answers: map[string][]netip.Addr{
		"mapped-private.test": addrs("::ffff:10.0.0.1"),
		"mapped-public.test":  addrs("::ffff:93.184.216.34"),
	}}
	spy := &spyConnect{}
	d := &Dialer{Resolver: res, Connect: spy.dial}
	_, err := d.DialContext(context.Background(), "tcp", "mapped-private.test:80")
	if toolerr.Code(err) != toolerr.CodeAddressNotAllowed || toolerr.Detail(err, "via") != "ipv4-mapped" || toolerr.Detail(err, "address") != "10.0.0.1" {
		t.Errorf("%v (%v / %v)", err, toolerr.Detail(err, "via"), toolerr.Detail(err, "address"))
	}
	conn, err := d.DialContext(context.Background(), "tcp", "mapped-public.test:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if spy.dialed[len(spy.dialed)-1] != "93.184.216.34:443" {
		t.Errorf("a public mapped answer dials the unmapped literal: %v", spy.dialed)
	}
}

// Finding 4: the zone strip applied to every host, so a name containing '%'
// was truncated before resolution and dialed as a different host. Only an IP
// literal has a zone; a name goes to the resolver whole.
func TestZoneIsStrippedFromLiteralsOnly(t *testing.T) {
	res := &fakeResolver{answers: map[string][]netip.Addr{"odd%name.test": addrs("93.184.216.34")}}
	spy := &spyConnect{}
	d := &Dialer{Resolver: res, Connect: spy.dial}
	conn, err := d.DialContext(context.Background(), "tcp", "odd%name.test:80")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if len(res.calls) != 1 || res.calls[0] != "odd%name.test" {
		t.Errorf("the name must reach the resolver whole: %v", res.calls)
	}
	// A zoned literal is still judged as its address.
	if _, err := d.DialContext(context.Background(), "tcp", "[fe80::1%25en0]:80"); toolerr.Code(err) != toolerr.CodeAddressNotAllowed {
		t.Errorf("zoned link-local: %v", err)
	}
	if a, _ := d.Vet(context.Background(), "2606:4700::1111%en0"); len(a) != 1 || a[0].Zone() != "" {
		t.Errorf("the zone must be dropped from the vetted literal: %v", a)
	}
}
