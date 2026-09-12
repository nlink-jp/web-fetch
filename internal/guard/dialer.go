package guard

import (
	"context"
	"errors"
	"net"
	"net/netip"

	"github.com/nlink-jp/web-fetch/internal/toolerr"
)

// Resolver resolves a host name to addresses. *net.Resolver satisfies it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Dialer is the DialContext of the HTTP transport: resolve, judge every
// address, connect to a vetted one as an ip:port literal. It never dials by
// name.
type Dialer struct {
	// Resolver answers name lookups; nil means net.DefaultResolver. Tests
	// inject one so a fixture name resolves to a chosen address.
	Resolver Resolver
	// AllowPrivate lifts the table (ADR-0001 §6). Set only from a config file
	// on the default search path — the config package enforces that.
	AllowPrivate bool
	// Connect opens the TCP connection to a vetted "ip:port"; nil means
	// net.Dialer. Tests inject one to reach an httptest listener while
	// asserting the literal they were handed.
	Connect func(ctx context.Context, network, address string) (net.Conn, error)
}

func (d *Dialer) resolver() Resolver {
	if d.Resolver != nil {
		return d.Resolver
	}
	return net.DefaultResolver
}

func (d *Dialer) connect() func(ctx context.Context, network, address string) (net.Conn, error) {
	if d.Connect != nil {
		return d.Connect
	}
	return (&net.Dialer{}).DialContext
}

// Vet resolves host and returns the addresses a connection may go to, every
// one judged. An IP literal is judged as itself without a lookup. One denied
// address denies the whole answer; a lookup that fails or answers with nothing
// is dns_failed.
func (d *Dialer) Vet(ctx context.Context, host string) ([]netip.Addr, error) {
	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		// A zone ("fe80::1%en0") does not change the address; it is dropped
		// here and only here — a name containing '%' stays a name and goes
		// to the resolver whole (pre-release review, finding 4).
		addrs = []netip.Addr{ip.WithZone("")}
	} else {
		var lerr error
		addrs, lerr = d.resolver().LookupNetIP(ctx, "ip", host)
		if lerr != nil {
			if ctx.Err() != nil {
				return nil, toolerr.Newf(toolerr.CodeTimeout, "resolving %s exceeded the deadline", host).With("host", host)
			}
			return nil, toolerr.Newf(toolerr.CodeDNSFailed, "could not resolve %s: %v", host, lerr).With("host", host).With("cause", lerr.Error())
		}
		if len(addrs) == 0 {
			return nil, toolerr.Newf(toolerr.CodeDNSFailed, "%s resolved to no addresses", host).With("host", host)
		}
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		// Judge the address as answered (so a mapped form is reported as
		// such), then dial its unmapped form.
		if class, via := Classify(a); class != "" && !d.AllowPrivate {
			e := toolerr.Newf(toolerr.CodeAddressNotAllowed,
				"%s resolves to %s, a %s address; web-fetch does not reach such addresses (an operator may set [network] allow_private = true in the global config)",
				host, a, class).
				With("host", host).With("address", a.Unmap().String()).With("class", string(class))
			if via != "" {
				e = e.With("via", via)
			}
			return nil, e
		}
		out = append(out, a.Unmap())
	}
	return out, nil
}

// DialContext implements http.Transport.DialContext. address is "host:port".
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, toolerr.Newf(toolerr.CodeConnectFailed, "malformed dial address %q: %v", address, err).With("cause", err.Error())
	}
	addrs, err := d.Vet(ctx, host)
	if err != nil {
		return nil, err
	}
	var last error
	for _, a := range addrs {
		if ctx.Err() != nil {
			break
		}
		conn, cerr := d.connect()(ctx, network, net.JoinHostPort(a.String(), port))
		if cerr == nil {
			return conn, nil
		}
		last = cerr
	}
	if ctx.Err() != nil || (last != nil && errors.Is(last, context.DeadlineExceeded)) {
		return nil, toolerr.Newf(toolerr.CodeTimeout, "connecting to %s exceeded the deadline", host).With("host", host)
	}
	cause := "no address to try"
	if last != nil {
		cause = last.Error()
	}
	return nil, toolerr.Newf(toolerr.CodeConnectFailed, "could not connect to %s: %s", host, cause).With("host", host).With("cause", cause)
}
