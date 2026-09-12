// Package guard is the SSRF defence of web-fetch (ADR-0001): a finite table of
// denied address classes, and a dialer that resolves a host, judges every
// address it got, and connects to a vetted one as an ip:port literal — check
// and connect as one operation, so a DNS answer that changes in between has
// nothing to exploit.
//
// The table is the whole rule. A hostname or URL-pattern rule is never added
// here; a class is a table row plus a test row.
package guard

import (
	"encoding/binary"
	"net/netip"
)

// Class names a denied address class. The empty Class means publicly routable.
type Class string

// The denied classes (RFP §3, ADR-0001 §2).
const (
	ClassLoopback    Class = "loopback"
	ClassPrivate     Class = "private"
	ClassLinkLocal   Class = "link-local"
	ClassSiteLocal   Class = "site-local"
	ClassUniqueLocal Class = "unique-local"
	ClassUnspecified Class = "unspecified"
	ClassBroadcast   Class = "broadcast"
	ClassMulticast   Class = "multicast"
	ClassShared      Class = "shared-address-space"
	ClassReserved    Class = "reserved"
)

type row struct {
	prefix netip.Prefix
	class  Class
}

// table is the finite denied domain. Order matters only where prefixes nest
// (none do here); exact addresses (unspecified, broadcast, ::1) are handled
// before the table because they sit inside broader rows.
var table = []row{
	// IPv4
	{mustPrefix("0.0.0.0/8"), ClassReserved},
	{mustPrefix("10.0.0.0/8"), ClassPrivate},
	{mustPrefix("100.64.0.0/10"), ClassShared},
	{mustPrefix("127.0.0.0/8"), ClassLoopback},
	{mustPrefix("169.254.0.0/16"), ClassLinkLocal},
	{mustPrefix("172.16.0.0/12"), ClassPrivate},
	{mustPrefix("192.0.0.0/24"), ClassReserved},
	{mustPrefix("192.168.0.0/16"), ClassPrivate},
	{mustPrefix("198.18.0.0/15"), ClassReserved},
	{mustPrefix("224.0.0.0/4"), ClassMulticast},
	{mustPrefix("240.0.0.0/4"), ClassReserved},
	// IPv6
	{mustPrefix("fe80::/10"), ClassLinkLocal},
	{mustPrefix("fec0::/10"), ClassSiteLocal},
	{mustPrefix("fc00::/7"), ClassUniqueLocal},
	{mustPrefix("ff00::/8"), ClassMulticast},
	{mustPrefix("2001:2::/48"), ClassReserved}, // benchmarking, the IPv6 twin of 198.18/15
}

// embed is an IPv6 prefix that carries an IPv4 address; the embedded address
// is judged in place of the outer one.
type embed struct {
	prefix  netip.Prefix
	name    string
	extract func(netip.Addr) netip.Addr
}

var embeds = []embed{
	{mustPrefix("64:ff9b::/96"), "nat64", last4},
	{mustPrefix("64:ff9b:1::/48"), "nat64-local", last4},
	{mustPrefix("2002::/16"), "6to4", bytes2to5},
	{mustPrefix("2001::/32"), "teredo", last4Inverted},
	// ::/96 (IPv4-compatible) last: :: and ::1 live inside it and are judged
	// exactly before the embeds run.
	{mustPrefix("::/96"), "ipv4-compatible", last4},
}

var (
	unspecified4 = netip.MustParseAddr("0.0.0.0")
	broadcast4   = netip.MustParseAddr("255.255.255.255")
)

// Classify judges one address. It returns the denied class, or "" when the
// address is publicly routable, and the name of the IPv6 embedding that was
// unwrapped to reach the judgment ("" when none). IPv4-mapped IPv6 is
// unwrapped first, like every other embedding.
func Classify(addr netip.Addr) (Class, string) {
	if addr.Is4In6() {
		c, _ := Classify(addr.Unmap())
		return c, "ipv4-mapped"
	}
	if addr.Is6() {
		if addr.IsUnspecified() {
			return ClassUnspecified, ""
		}
		if addr.IsLoopback() {
			return ClassLoopback, ""
		}
		for _, e := range embeds {
			if e.prefix.Contains(addr) {
				c, _ := Classify(e.extract(addr))
				return c, e.name
			}
		}
		return lookup(addr), ""
	}
	if addr == broadcast4 {
		return ClassBroadcast, ""
	}
	if addr == unspecified4 {
		return ClassUnspecified, ""
	}
	return lookup(addr), ""
}

func lookup(addr netip.Addr) Class {
	for _, r := range table {
		if r.prefix.Contains(addr) {
			return r.class
		}
	}
	return ""
}

// last4 returns the IPv4 address in the low 32 bits.
func last4(a netip.Addr) netip.Addr {
	b := a.As16()
	return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
}

// bytes2to5 returns the IPv4 address in bits 16–47 (6to4: 2002:AABB:CCDD::).
func bytes2to5(a netip.Addr) netip.Addr {
	b := a.As16()
	return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})
}

// last4Inverted returns the IPv4 address Teredo stores in the low 32 bits with
// every bit flipped (RFC 4380 §4).
func last4Inverted(a netip.Addr) netip.Addr {
	b := a.As16()
	v := ^binary.BigEndian.Uint32(b[12:16])
	var out [4]byte
	binary.BigEndian.PutUint32(out[:], v)
	return netip.AddrFrom4(out)
}

func mustPrefix(s string) netip.Prefix { return netip.MustParsePrefix(s) }

// Table returns the denied prefixes and their classes, in table order, for
// documentation and tests.
func Table() []struct {
	Prefix netip.Prefix
	Class  Class
} {
	out := make([]struct {
		Prefix netip.Prefix
		Class  Class
	}, 0, len(table))
	for _, r := range table {
		out = append(out, struct {
			Prefix netip.Prefix
			Class  Class
		}{r.prefix, r.class})
	}
	return out
}

// Embeddings returns the IPv6 embedding prefixes that are unwrapped, in the
// order they are tried, for documentation and tests.
func Embeddings() []struct {
	Prefix netip.Prefix
	Name   string
} {
	out := make([]struct {
		Prefix netip.Prefix
		Name   string
	}, 0, len(embeds)+1)
	out = append(out, struct {
		Prefix netip.Prefix
		Name   string
	}{mustPrefix("::ffff:0:0/96"), "ipv4-mapped"})
	for _, e := range embeds {
		out = append(out, struct {
			Prefix netip.Prefix
			Name   string
		}{e.prefix, e.name})
	}
	return out
}
