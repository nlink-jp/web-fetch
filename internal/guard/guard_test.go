package guard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// Every row of the table, one representative address each, plus the exact
// addresses judged before the table. This is the finite domain of ADR-0001;
// a new row here is how a class is added.
func TestClassifyPinsEveryRow(t *testing.T) {
	cases := map[string]Class{
		"127.0.0.1":       ClassLoopback,
		"127.255.255.254": ClassLoopback,
		"::1":             ClassLoopback,
		"10.1.2.3":        ClassPrivate,
		"172.16.0.1":      ClassPrivate,
		"172.31.255.255":  ClassPrivate,
		"192.168.99.1":    ClassPrivate,
		"169.254.169.254": ClassLinkLocal, // cloud metadata
		"fe80::1":         ClassLinkLocal,
		"fec0::1":         ClassSiteLocal,
		"fc00::1":         ClassUniqueLocal,
		"fd12:3456::1":    ClassUniqueLocal,
		"0.0.0.0":         ClassUnspecified,
		"::":              ClassUnspecified,
		"255.255.255.255": ClassBroadcast,
		"224.0.0.1":       ClassMulticast,
		"239.255.255.250": ClassMulticast,
		"ff02::1":         ClassMulticast,
		"100.64.0.1":      ClassShared,
		"100.127.255.254": ClassShared,
		"0.1.2.3":         ClassReserved,
		"192.0.0.9":       ClassReserved,
		"198.18.0.1":      ClassReserved,
		"198.19.255.255":  ClassReserved,
		"240.0.0.1":       ClassReserved,
		"2001:2::1":       ClassReserved, // IPv6 benchmarking, the twin of 198.18/15
		// Public: the empty class.
		"8.8.8.8":         "",
		"1.1.1.1":         "",
		"93.184.216.34":   "",
		"172.32.0.1":      "", // just outside 172.16/12
		"100.128.0.1":     "", // just outside 100.64/10
		"2606:4700::1111": "",
		"2001:db8::1":     "", // documentation range: not in the table, deliberately
	}
	for in, want := range cases {
		got, _ := Classify(netip.MustParseAddr(in))
		if got != want {
			t.Errorf("Classify(%s) = %q, want %q", in, got, want)
		}
	}
	if len(Table()) != 16 {
		t.Errorf("the table has %d rows; ADR-0001 lists 16 — update the ADR and this test together", len(Table()))
	}
}

// Every embedding is unwrapped and the inner IPv4 judged: a private inner
// address is denied through the wrapper, a public one passes through it.
func TestClassifyUnwrapsEmbeddedIPv4(t *testing.T) {
	cases := []struct {
		in   string
		want Class
		via  string
	}{
		{"::ffff:127.0.0.1", ClassLoopback, "ipv4-mapped"},
		{"::ffff:8.8.8.8", "", "ipv4-mapped"},
		{"::7f00:1", ClassLoopback, "ipv4-compatible"},          // ::127.0.0.1
		{"::a00:1", ClassPrivate, "ipv4-compatible"},            // ::10.0.0.1
		{"64:ff9b::7f00:1", ClassLoopback, "nat64"},             // 64:ff9b::127.0.0.1
		{"64:ff9b::808:808", "", "nat64"},                       // 64:ff9b::8.8.8.8 — DNS64 to a public host
		{"64:ff9b:1::a9fe:a9fe", ClassLinkLocal, "nat64-local"}, // 169.254.169.254 behind local NAT64
		{"2002:7f00:1::1", ClassLoopback, "6to4"},               // 6to4 of 127.0.0.1
		{"2002:c0a8:101::1", ClassPrivate, "6to4"},              // 6to4 of 192.168.1.1
		{"2002:808:808::1", "", "6to4"},                         // 6to4 of 8.8.8.8
		{"2001:0:1:2:3:4:80ff:fffe", ClassLoopback, "teredo"},   // ~0x80fffffe = 127.0.0.1
		{"2001:0:1:2:3:4:f7f7:f7f7", "", "teredo"},              // ~0xf7f7f7f7 = 8.8.8.8
	}
	for _, c := range cases {
		got, via := Classify(netip.MustParseAddr(c.in))
		if got != c.want || via != c.via {
			t.Errorf("Classify(%s) = (%q, %q), want (%q, %q)", c.in, got, via, c.want, c.via)
		}
	}
	if len(Embeddings()) != 6 {
		t.Errorf("%d embeddings; ADR-0001 lists 6", len(Embeddings()))
	}
}

// fakeResolver answers from a table; a missing name is a lookup error.
type fakeResolver struct {
	answers map[string][]netip.Addr
	calls   []string
	err     error
}

func (f *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	f.calls = append(f.calls, host)
	if f.err != nil {
		return nil, f.err
	}
	a, ok := f.answers[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	return a, nil
}

func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(s))
	for _, x := range s {
		out = append(out, netip.MustParseAddr(x))
	}
	return out
}

// spyConnect records the literal it was asked to dial and returns a closed
// pipe end, or the error configured for that literal.
type spyConnect struct {
	dialed []string
	fail   map[string]error
}

func (s *spyConnect) dial(_ context.Context, _, address string) (net.Conn, error) {
	s.dialed = append(s.dialed, address)
	if err, ok := s.fail[address]; ok {
		return nil, err
	}
	c1, c2 := net.Pipe()
	_ = c2.Close()
	return c1, nil
}

func TestDialConnectsToTheVettedLiteralNotTheName(t *testing.T) {
	res := &fakeResolver{answers: map[string][]netip.Addr{"example.test": addrs("93.184.216.34")}}
	spy := &spyConnect{}
	d := &Dialer{Resolver: res, Connect: spy.dial}
	conn, err := d.DialContext(context.Background(), "tcp", "example.test:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if len(spy.dialed) != 1 || spy.dialed[0] != "93.184.216.34:443" {
		t.Errorf("dialed %v; the connection must go to the judged literal", spy.dialed)
	}
	if len(res.calls) != 1 || res.calls[0] != "example.test" {
		t.Errorf("resolver calls: %v", res.calls)
	}
}

// Rebinding: whatever the name answers later, the connection goes only to what
// was judged in this very call, because judge and connect share one lookup.
func TestDialJudgesAndConnectsInOneOperation(t *testing.T) {
	res := &fakeResolver{answers: map[string][]netip.Addr{"flip.test": addrs("93.184.216.34")}}
	spy := &spyConnect{}
	d := &Dialer{Resolver: res, Connect: spy.dial}
	// A second answer for the same name arriving "between" check and connect
	// cannot exist: there is no second lookup. Prove it by changing the table
	// after the call and seeing the dial unaffected, and by counting lookups.
	conn, err := d.DialContext(context.Background(), "tcp", "flip.test:80")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	res.answers["flip.test"] = addrs("127.0.0.1")
	if len(res.calls) != 1 || spy.dialed[0] != "93.184.216.34:80" {
		t.Errorf("lookups=%d dialed=%v", len(res.calls), spy.dialed)
	}
	if _, err := d.DialContext(context.Background(), "tcp", "flip.test:80"); toolerr.Code(err) != toolerr.CodeAddressNotAllowed {
		t.Errorf("the rebound answer must be refused on its own lookup: %v", err)
	}
}

func TestOneDeniedAddressDeniesTheWholeAnswer(t *testing.T) {
	res := &fakeResolver{answers: map[string][]netip.Addr{"mixed.test": addrs("93.184.216.34", "10.0.0.5")}}
	spy := &spyConnect{}
	d := &Dialer{Resolver: res, Connect: spy.dial}
	_, err := d.DialContext(context.Background(), "tcp", "mixed.test:80")
	if toolerr.Code(err) != toolerr.CodeAddressNotAllowed {
		t.Fatalf("want address_not_allowed, got %v", err)
	}
	if toolerr.Detail(err, "class") != "private" || toolerr.Detail(err, "address") != "10.0.0.5" || toolerr.Detail(err, "host") != "mixed.test" {
		t.Errorf("details: %v", toolerr.Detail(err, "class"))
	}
	if len(spy.dialed) != 0 {
		t.Errorf("nothing may be dialed when any address is denied: %v", spy.dialed)
	}
}

func TestIPLiteralsAreJudgedWithoutALookup(t *testing.T) {
	res := &fakeResolver{}
	d := &Dialer{Resolver: res, Connect: (&spyConnect{}).dial}
	for _, addr := range []string{"127.0.0.1:80", "[::1]:80", "[::ffff:10.0.0.1]:80", "[fe80::1%25en0]:80", "169.254.169.254:80"} {
		if _, err := d.DialContext(context.Background(), "tcp", addr); toolerr.Code(err) != toolerr.CodeAddressNotAllowed {
			t.Errorf("%s: want address_not_allowed, got %v", addr, err)
		}
	}
	if len(res.calls) != 0 {
		t.Errorf("a literal must not be looked up: %v", res.calls)
	}
	if via := toolerr.Detail(mustErr(d.DialContext(context.Background(), "tcp", "[::ffff:10.0.0.1]:80")), "via"); via != "ipv4-mapped" {
		t.Errorf("via = %v", via)
	}
}

func mustErr(_ net.Conn, err error) error { return err }

// Numeric spellings are not IP literals to the parser; they reach the resolver,
// whose answer is then judged like any other. Whether the system resolver
// normalises "2130706433" to 127.0.0.1 or fails is its business — both end in
// a refusal.
func TestNumericSpellingsGoThroughTheResolver(t *testing.T) {
	res := &fakeResolver{answers: map[string][]netip.Addr{"2130706433": addrs("127.0.0.1"), "0x7f.0.0.1": addrs("127.0.0.1")}}
	d := &Dialer{Resolver: res, Connect: (&spyConnect{}).dial}
	for _, host := range []string{"2130706433", "0x7f.0.0.1", "127.1"} {
		_, err := d.DialContext(context.Background(), "tcp", host+":80")
		if c := toolerr.Code(err); c != toolerr.CodeAddressNotAllowed && c != toolerr.CodeDNSFailed {
			t.Errorf("%s: %v", host, err)
		}
	}
	if len(res.calls) != 3 {
		t.Errorf("all three must be resolved, not parsed: %v", res.calls)
	}
}

func TestAllowPrivateLiftsTheTable(t *testing.T) {
	spy := &spyConnect{}
	d := &Dialer{Resolver: &fakeResolver{answers: map[string][]netip.Addr{"lan.test": addrs("192.168.1.10")}}, AllowPrivate: true, Connect: spy.dial}
	conn, err := d.DialContext(context.Background(), "tcp", "lan.test:8080")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if spy.dialed[0] != "192.168.1.10:8080" {
		t.Errorf("dialed %v", spy.dialed)
	}
}

func TestLookupFailuresAreDNSFailed(t *testing.T) {
	d := &Dialer{Resolver: &fakeResolver{answers: map[string][]netip.Addr{"empty.test": {}}}, Connect: (&spyConnect{}).dial}
	if _, err := d.DialContext(context.Background(), "tcp", "empty.test:80"); toolerr.Code(err) != toolerr.CodeDNSFailed {
		t.Errorf("zero addresses: %v", err)
	}
	if _, err := d.DialContext(context.Background(), "tcp", "unknown.test:80"); toolerr.Code(err) != toolerr.CodeDNSFailed || toolerr.Detail(err, "cause") == nil {
		t.Errorf("lookup error: %v", err)
	}
}

func TestAddressesAreTriedInOrderUntilOneConnects(t *testing.T) {
	spy := &spyConnect{fail: map[string]error{"[2606:4700::1111]:443": errors.New("no route")}}
	res := &fakeResolver{answers: map[string][]netip.Addr{"dual.test": addrs("2606:4700::1111", "1.1.1.1")}}
	d := &Dialer{Resolver: res, Connect: spy.dial}
	conn, err := d.DialContext(context.Background(), "tcp", "dual.test:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if len(spy.dialed) != 2 || spy.dialed[1] != "1.1.1.1:443" {
		t.Errorf("dialed %v", spy.dialed)
	}
	// Every address failing is connect_failed with the last cause.
	spy2 := &spyConnect{fail: map[string]error{"[2606:4700::1111]:443": errors.New("no route"), "1.1.1.1:443": errors.New("refused")}}
	d2 := &Dialer{Resolver: res, Connect: spy2.dial}
	_, err = d2.DialContext(context.Background(), "tcp", "dual.test:443")
	if toolerr.Code(err) != toolerr.CodeConnectFailed || toolerr.Detail(err, "cause") != "refused" {
		t.Errorf("all failed: %v", err)
	}
}

func TestAnExpiredContextIsATimeout(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	slow := &fakeResolver{err: context.DeadlineExceeded}
	d := &Dialer{Resolver: slow, Connect: (&spyConnect{}).dial}
	if _, err := d.DialContext(ctx, "tcp", "slow.test:80"); toolerr.Code(err) != toolerr.CodeTimeout {
		t.Errorf("resolver deadline: %v", err)
	}
	res := &fakeResolver{answers: map[string][]netip.Addr{"ok.test": addrs("93.184.216.34")}}
	spy := &spyConnect{fail: map[string]error{"93.184.216.34:80": context.DeadlineExceeded}}
	d2 := &Dialer{Resolver: res, Connect: spy.dial}
	if _, err := d2.DialContext(context.Background(), "tcp", "ok.test:80"); toolerr.Code(err) != toolerr.CodeTimeout {
		t.Errorf("connect deadline: %v", err)
	}
}

func TestMalformedDialAddress(t *testing.T) {
	d := &Dialer{Resolver: &fakeResolver{}, Connect: (&spyConnect{}).dial}
	if _, err := d.DialContext(context.Background(), "tcp", "no-port"); toolerr.Code(err) != toolerr.CodeConnectFailed {
		t.Errorf("%v", err)
	}
}

// The production defaults are wired: with no injected resolver or connect the
// dialer uses the system resolver and net.Dialer. Only the refusal path is
// exercised, so no network is touched.
func TestDefaultsRefuseLoopbackWithoutTouchingTheNetwork(t *testing.T) {
	d := &Dialer{}
	if _, err := d.DialContext(context.Background(), "tcp", "127.0.0.1:1"); toolerr.Code(err) != toolerr.CodeAddressNotAllowed {
		t.Errorf("%v", err)
	}
}
