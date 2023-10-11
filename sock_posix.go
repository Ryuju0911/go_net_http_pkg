package go_net

import (
	"context"
	"syscall"
)

func socket(
	ctx context.Context,
	net string,
	family,
	sotype,
	proto int,
	ipv6only bool,
	laddr,
	raddr sockaddr,
	ctrlCtxFn func(context.Context, string, string, syscall.RawConn) error,
) (fd *netFD, err error) {
	s, err := sysSocket(family, sotype, proto)
	if err != nil {
		return nil, err
	}
	if fd, err = newFD(s, family, sotype, net); err != nil {
		// poll.CloseFunc(s)
		return nil, err
	}

	return fd, nil
}
