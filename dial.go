// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package net

import (
	"context"
	"internal/bytealg"
	"syscall"
	"time"
)

// A Dialer contains options for connecting to an address.
//
// The zero value for each field is equivalent to dialing
// without that option. Dialing with the zero value of Dialer
// is therefore equivalent to just calling the Dial function.
//
// It is safe to call Dialer's methods concurrently.
type Dialer struct {
	// Timeout is the maximum amount of time a dial will wait for
	// a connect to complete. If Deadline is also set, it may fail
	// earlier.
	//
	// The default is no timeout.
	//
	// When using TCP and dialing a host name with multiple IP
	// addresses, the timeout may be divided between them.
	//
	// With or without a timeout, the operating system may impose
	// its own earlier timeout. For instance, TCP timeouts are
	// often around 3 minutes.
	Timeout time.Duration

	// Deadline is the absolute point in time after which dials
	// will fail. If Timeout is set, it may fail earlier.
	// Zero means no deadline, or dependent on the operating system
	// as with the Timeout option.
	Deadline time.Time

	// LocalAddr is the local address to use when dialing an
	// address. The address must be of a compatible type for the
	// network being dialed.
	// If nil, a local address is automatically chosen.
	LocalAddr Addr

	// // DualStack previously enabled RFC 6555 Fast Fallback
	// // support, also known as "Happy Eyeballs", in which IPv4 is
	// // tried soon if IPv6 appears to be misconfigured and
	// // hanging.
	// //
	// // Deprecated: Fast Fallback is enabled by default. To
	// // disable, set FallbackDelay to a negative value.
	// DualStack bool

	// FallbackDelay specifies the length of time to wait before
	// spawning a RFC 6555 Fast Fallback connection. That is, this
	// is the amount of time to wait for IPv6 to succeed before
	// assuming that IPv6 is misconfigured and falling back to
	// IPv4.
	//
	// If zero, a default delay of 300ms is used.
	// A negative value disables Fast Fallback support.
	FallbackDelay time.Duration

	// // KeepAlive specifies the interval between keep-alive
	// // probes for an active network connection.
	// // If zero, keep-alive probes are sent with a default value
	// // (currently 15 seconds), if supported by the protocol and operating
	// // system. Network protocols or operating systems that do
	// // not support keep-alives ignore this field.
	// // If negative, keep-alive probes are disabled.
	// KeepAlive time.Duration

	// Resolver optionally specifies an alternate resolver to use.
	Resolver *Resolver

	// Cancel is an optional channel whose closure indicates that
	// the dial should be canceled. Not all types of dials support
	// cancellation.
	//
	// Deprecated: Use DialContext instead.
	Cancel <-chan struct{}

	// // If Control is not nil, it is called after creating the network
	// // connection but before actually dialing.
	// //
	// // Network and address parameters passed to Control function are not
	// // necessarily the ones passed to Dial. For example, passing "tcp" to Dial
	// // will cause the Control function to be called with "tcp4" or "tcp6".
	// //
	// // Control is ignored if ControlContext is not nil.
	// Control func(network, address string, c syscall.RawConn) error

	// // If ControlContext is not nil, it is called after creating the network
	// // connection but before actually dialing.
	// //
	// // Network and address parameters passed to ControlContext function are not
	// // necessarily the ones passed to Dial. For example, passing "tcp" to Dial
	// // will cause the ControlContext function to be called with "tcp4" or "tcp6".
	// //
	// // If ControlContext is not nil, Control is ignored.
	// ControlContext func(ctx context.Context, network, address string, c syscall.RawConn) error

	// // If mptcpStatus is set to a value allowing Multipath TCP (MPTCP) to be
	// // used, any call to Dial with "tcp(4|6)" as network will use MPTCP if
	// // supported by the operating system.
	// mptcpStatus mptcpStatus
}

func (d *Dialer) dualStack() bool { return d.FallbackDelay >= 0 }

func minNonzeroTime(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() || a.Before(b) {
		return a
	}
	return b
}

// deadline returns the earliest of:
//   - now+Timeout
//   - d.Deadline
//   - the context's deadline
//
// Or zero, if none of Timeout, Deadline, or context's deadline is set.
func (d *Dialer) deadline(ctx context.Context, now time.Time) (earliest time.Time) {
	if d.Timeout != 0 { // including negative, for historical reasons
		earliest = now.Add(d.Timeout)
	}
	if d, ok := ctx.Deadline(); ok {
		earliest = minNonzeroTime(earliest, d)
	}
	return minNonzeroTime(earliest, d.Deadline)
}

func (d *Dialer) resolver() *Resolver {
	if d.Resolver != nil {
		return d.Resolver
	}
	return DefaultResolver
}

func parseNetwork(ctx context.Context, network string, needsProto bool) (afnet string, proto int, err error) {
	i := bytealg.LastIndexByteString(network, ':')
	if i < 0 { // no colon
		switch network {
		case "tcp", "tcp4", "tcp6":
		case "udp", "udp4", "udp6":
		case "ip", "ip4", "ip6":
			if needsProto {
				return "", 0, UnknownNetworkError(network)
			}
		case "unix", "unixgram", "unixpacket":
		default:
			return "", 0, UnknownNetworkError(network)
		}
		return network, 0, nil
	}
	afnet = network[:i]
	switch afnet {
	case "ip", "ip4", "ip6":
		protostr := network[i+1:]
		proto, i, ok := dtoi(protostr)
		if !ok || i != len(protostr) {
			proto, err = lookupProtocol(ctx, protostr)
			if err != nil {
				return "", 0, err
			}
		}
		return afnet, proto, nil
	}
	return "", 0, UnknownNetworkError(network)
}

// resolveAddrList resolves addr using hint and returns a list of
// addresses. The result contains at least one address when error is
// nil.
func (r *Resolver) resolveAddrList(ctx context.Context, op, network, addr string, hint Addr) (addrList, error) {
	afnet, _, err := parseNetwork(ctx, network, true)
	if err != nil {
		return nil, err
	}
	if op == "dial" && addr == "" {
		return nil, errMissingAddress
	}

	// TODO: Handle unix, unixgram, and unixpacket.

	addrs, err := r.internetAddrList(ctx, afnet, addr)
	if err != nil || op != "dial" || hint == nil {
		return addrs, err
	}

	return addrs, err
}

// sysDialer contains a Dial's parameters and configuration.
type sysDialer struct {
	Dialer
	network, address string
	// testHookDialTCP  func(ctx context.Context, net string, laddr, raddr *TCPAddr) (*TCPConn, error)
}

// DialContext connects to the address on the named network using
// the provided context.
//
// The provided Context must be non-nil. If the context expires before
// the connection is complete, an error is returned. Once successfully
// connected, any expiration of the context will not affect the
// connection.
//
// When using TCP, and the host in the address parameter resolves to multiple
// network addresses, any dial timeout (from d.Timeout or ctx) is spread
// over each consecutive dial, such that each is given an appropriate
// fraction of the time to connect.
// For example, if a host has 4 IP addresses and the timeout is 1 minute,
// the connect to each single address will be given 15 seconds to complete
// before trying the next one.
//
// See func Dial for a description of the network and address
// parameters.
func (d *Dialer) DialContext(ctx context.Context, network, address string) (Conn, error) {
	if ctx == nil {
		panic("nil context")
	}
	deadline := d.deadline(ctx, time.Now())
	if !deadline.IsZero() {
		// testHoolStepTime()
		if d, ok := ctx.Deadline(); !ok || deadline.Before(d) {
			subCtx, cancel := context.WithDeadline(ctx, deadline)
			defer cancel()
			ctx = subCtx
		}
	}
	if oldCancel := d.Cancel; oldCancel != nil {
		subCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-oldCancel:
				cancel()
			case <-subCtx.Done():
			}
		}()
		ctx = subCtx
	}

	// // Shadow the nettrace (if any) during resolve so Connect events don't fire for DNS lookups.
	resolveCtx := ctx
	// if trace, _ := ctx.Value(nettrace.TraceKey{}).(*nettrace.Trace); trace != nil {
	// 	shadow := *trace
	// 	shadow.ConnectStart = nil
	// 	shadow.ConnectDone = nil
	// 	resolveCtx = context.WithValue(resolveCtx, nettrace.TraceKey{}, &shadow)
	// }

	addrs, err := d.resolver().resolveAddrList(resolveCtx, "dial", network, address, d.LocalAddr)
	if err != nil {
		return nil, &OpError{Op: "dial", Net: network, Source: nil, Addr: nil, Err: err}
	}

	sd := &sysDialer{
		Dialer:  *d,
		network: network,
		address: address,
	}

	var primaries, fallbacks addrList
	if d.dualStack() && network == "tcp" {
		primaries, fallbacks = addrs.partition(isIPv4)
	} else {
		primaries = addrs
	}

	return sd.dialParallel(ctx, primaries, fallbacks)
}

func (sd *sysDialer) dialParallel(ctx context.Context, primaries, fallbacks addrList) (Conn, error) {
	// TODO: Implement logic.
	return nil, nil
}

// ListenConfig contains options for listening to an address.
type ListenConfig struct {
	// If Control is not nil, it is called after creating the network
	// connection but before binding it to the operating system.
	//
	// Network and address parameters passed to Control method are not
	// necessarily the ones passed to Listen. For example, passing "tcp" to
	// Listen will cause the Control function to be called with "tcp4" or "tcp6".
	Control func(network, address string, c syscall.RawConn) error

	// KeepAlive specifies the keep-alive period for network
	// connections accepted by this listener.
	// If zero, keep-alives are enabled if supported by the protocol
	// and operating system. Network protocols or operating systems
	// that do not support keep-alives ignore this field.
	// If negative, keep-alives are disabled.
	KeepAlive time.Duration
}

// Listen announces on the local network address.
//
// See func Listen for a description of the network and address
// parameters.
func (lc *ListenConfig) Listen(ctx context.Context, network, address string) (Listener, error) {
	addrs, err := DefaultResolver.resolveAddrList(ctx, "listen", network, address, nil)
	if err != nil {
		return nil, &OpError{Op: "listen", Net: network, Source: nil, Addr: nil, Err: err}
	}
	sl := &sysListener{
		ListenConfig: *lc,
		network:      network,
		address:      address,
	}
	var l Listener
	la := addrs.first(isIPv4)
	switch la := la.(type) {
	case *TCPAddr:
		// TODO: Support multipath TCP.
		l, err = sl.listenTCP(ctx, la)
	default:
		return nil, &OpError{Op: "listen", Net: sl.network, Source: nil, Addr: la, Err: &AddrError{Err: "unexpected address type", Addr: address}}
	}
	if err != nil {
		return nil, &OpError{Op: "listen", Net: sl.network, Source: nil, Addr: la, Err: err} // l is non-nil interface containing nil pointer
	}
	return l, nil
}

// sysListener contains a Listen's parameters and configuration.
type sysListener struct {
	ListenConfig
	network, address string
}

// Listen announces on the local network address.
//
// The network must be "tcp", "tcp4", "tcp6", "unix" or "unixpacket".
//
// For TCP networks, if the host in the address parameter is empty or
// a literal unspecified IP address, Listen listens on all available
// unicast and anycast IP addresses of the local system.
// To only use IPv4, use network "tcp4".
// The address can use a host name, but this is not recommended,
// because it will create a listener for at most one of the host's IP
// addresses.
// If the port in the address parameter is empty or "0", as in
// "127.0.0.1:" or "[::1]:0", a port number is automatically chosen.
// The Addr method of Listener can be used to discover the chosen
// port.
//
// See func Dial for a description of the network and address
// parameters.
//
// Listen uses context.Background internally; to specify the context, use
// ListenConfig.Listen.
func Listen(network, address string) (Listener, error) {
	var lc ListenConfig
	return lc.Listen(context.Background(), network, address)
}
