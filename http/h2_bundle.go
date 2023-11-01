package http

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"sync"
	"time"
)

type http2goroutineLock uint64

func (g http2goroutineLock) checkNotOn() {
	if http2curGoroutineID() == uint64(g) {
		panic("running on the wrong goroutine")
	}
}

var http2goroutineSpace = []byte("goroutine ")

func http2curGoroutineID() uint64 {
	bp := http2littleBuf.Get().(*[]byte)
	defer http2littleBuf.Put(bp)
	b := *bp
	b = b[:runtime.Stack(b, false)]
	// Parse the 4707 out of "goroutine 4707 ["
	b = bytes.TrimPrefix(b, http2goroutineSpace)
	i := bytes.IndexByte(b, ' ')
	if i < 0 {
		panic(fmt.Sprintf("No space found in %q", b))
	}
	b = b[:i]
	n, err := http2parseUintBytes(b, 10, 64)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse goroutine ID out of %q: %v", b, err))
	}
	return n
}

var http2littleBuf = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 64)
		return &buf
	},
}

// parseUintBytes is like strconv.ParseUint, but using a []byte.
func http2parseUintBytes(s []byte, base int, bitSize int) (n uint64, err error) {
	var cutoff, maxVal uint64

	if bitSize == 0 {
		bitSize = int(strconv.IntSize)
	}

	s0 := s
	switch {
	case len(s) < 1:
		err = strconv.ErrSyntax
		goto Error

	case 2 <= base && base <= 36:
		// valid base; nothing to do

	case base == 0:
		// Look for octal, hex prefix.
		switch {
		case s[0] == '0' && len(s) > 1 && (s[1] == 'x' || s[1] == 'X'):
			base = 16
			s = s[2:]
			if len(s) < 1 {
				err = strconv.ErrSyntax
				goto Error
			}
		case s[0] == '0':
			base = 8
		default:
			base = 10
		}

	default:
		err = errors.New("invalid base " + strconv.Itoa(base))
		goto Error
	}

	n = 0
	cutoff = http2cutoff64(base)
	maxVal = 1<<uint(bitSize) - 1

	for i := 0; i < len(s); i++ {
		var v byte
		d := s[i]
		switch {
		case '0' <= d && d <= '9':
			v = d - '0'
		case 'a' <= d && d <= 'z':
			v = d - 'a' + 10
		case 'A' <= d && d <= 'Z':
			v = d - 'A' + 10
		default:
			n = 0
			err = strconv.ErrSyntax
			goto Error
		}
		if int(v) >= base {
			n = 0
			err = strconv.ErrSyntax
			goto Error
		}

		if n >= cutoff {
			// n*base overflows
			n = 1<<64 - 1
			err = strconv.ErrRange
			goto Error
		}
		n *= uint64(base)

		n1 := n + uint64(v)
		if n1 < n || n1 > maxVal {
			// n+v overflows
			n = 1<<64 - 1
			err = strconv.ErrRange
			goto Error
		}
		n = n1
	}

	return n, nil

Error:
	return n, &strconv.NumError{Func: "ParseUint", Num: string(s0), Err: err}
}

// Return the first number n such that n*base >= 1<<64.
func http2cutoff64(base int) uint64 {
	if base < 2 {
		return 0
	}
	return (1<<64-1)/uint64(base) + 1
}

// Server is an HTTP/2 server.
type http2Server struct {
	// MaxHandlers limits the number of http.Handler ServeHTTP goroutines
	// which may run at a time over all connections.
	// Negative or zero no limit.
	// TODO: implement
	MaxHandlers int

	// MaxConcurrentStreams optionally specifies the number of
	// concurrent streams that each client may have open at a
	// time. This is unrelated to the number of http.Handler goroutines
	// which may be active globally, which is MaxHandlers.
	// If zero, MaxConcurrentStreams defaults to at least 100, per
	// the HTTP/2 spec's recommendations.
	MaxConcurrentStreams uint32

	// MaxDecoderHeaderTableSize optionally specifies the http2
	// SETTINGS_HEADER_TABLE_SIZE to send in the initial settings frame. It
	// informs the remote endpoint of the maximum size of the header compression
	// table used to decode header blocks, in octets. If zero, the default value
	// of 4096 is used.
	MaxDecoderHeaderTableSize uint32

	// MaxEncoderHeaderTableSize optionally specifies an upper limit for the
	// header compression table used for encoding request headers. Received
	// SETTINGS_HEADER_TABLE_SIZE settings are capped at this limit. If zero,
	// the default value of 4096 is used.
	MaxEncoderHeaderTableSize uint32

	// MaxReadFrameSize optionally specifies the largest frame
	// this server is willing to read. A valid value is between
	// 16k and 16M, inclusive. If zero or otherwise invalid, a
	// default value is used.
	MaxReadFrameSize uint32

	// PermitProhibitedCipherSuites, if true, permits the use of
	// cipher suites prohibited by the HTTP/2 spec.
	PermitProhibitedCipherSuites bool

	// IdleTimeout specifies how long until idle clients should be
	// closed with a GOAWAY frame. PING frames are not considered
	// activity for the purposes of IdleTimeout.
	IdleTimeout time.Duration

	// MaxUploadBufferPerConnection is the size of the initial flow
	// control window for each connections. The HTTP/2 spec does not
	// allow this to be smaller than 65535 or larger than 2^32-1.
	// If the value is outside this range, a default value will be
	// used instead.
	MaxUploadBufferPerConnection int32

	// MaxUploadBufferPerStream is the size of the initial flow control
	// window for each stream. The HTTP/2 spec does not allow this to
	// be larger than 2^32-1. If the value is zero or larger than the
	// maximum, a default value will be used instead.
	MaxUploadBufferPerStream int32

	// NewWriteScheduler constructs a write scheduler for a connection.
	// If nil, a default scheduler is chosen.
	NewWriteScheduler func() http2WriteScheduler

	// CountError, if non-nil, is called on HTTP/2 server errors.
	// It's intended to increment a metric for monitoring, such
	// as an expvar or Prometheus metric.
	// The errType consists of only ASCII word characters.
	CountError func(errType string)

	// Internal state. This is a pointer (rather than embedded directly)
	// so that we don't embed a Mutex in this struct, which will make the
	// struct non-copyable, which might break some callers.
	state *http2serverInternalState
}

type http2serverInternalState struct {
	mu          sync.Mutex
	activeConns map[*http2serverConn]struct{}
}

func (s *http2serverInternalState) startGracefulShutdown() {
	if s == nil {
		return // if the Server was used without calling ConfigureServer
	}
	s.mu.Lock()
	for sc := range s.activeConns {
		sc.startGracefulShutdown()
	}
	s.mu.Unlock()
}

func http2ConfigureServer(s *Server, conf *http2Server) error {
	if s == nil {
		panic("nil *http.Server")
	}
	if conf == nil {
		conf = new(http2Server)
	}
	conf.state = &http2serverInternalState{activeConns: make(map[*http2serverConn]struct{})}
	// if h1, h2 := s, conf; h2.IdleTimeout == 0 {
	// 	if h1.IdleTimeout != 0 {
	// 		h2.IdleTimeout = h1.IdleTimeout
	// 	} else {
	// 		h2.IdleTimeout = h1.ReadTimeout
	// 	}
	// }
	s.RegisterOnShutdown(conf.state.startGracefulShutdown)

	// if s.TLSConfig == nil {
	// 	s.TLSConfig = new(tls.Config)
	// } else if s.TLSConfig.CipherSuites != nil && s.TLSConfig.MinVersion < tls.VersionTLS13 {
	// 	// If they already provided a TLS 1.0–1.2 CipherSuite list, return an
	// 	// error if it is missing ECDHE_RSA_WITH_AES_128_GCM_SHA256 or
	// 	// ECDHE_ECDSA_WITH_AES_128_GCM_SHA256.
	// 	haveRequired := false
	// 	for _, cs := range s.TLSConfig.CipherSuites {
	// 		switch cs {
	// 		case tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	// 			// Alternative MTI cipher to not discourage ECDSA-only servers.
	// 			// See http://golang.org/cl/30721 for further information.
	// 			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256:
	// 			haveRequired = true
	// 		}
	// 	}
	// 	if !haveRequired {
	// 		return fmt.Errorf("http2: TLSConfig.CipherSuites is missing an HTTP/2-required AES_128_GCM_SHA256 cipher (need at least one of TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256 or TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256)")
	// 	}
	// }

	// // Note: not setting MinVersion to tls.VersionTLS12,
	// // as we don't want to interfere with HTTP/1.1 traffic
	// // on the user's server. We enforce TLS 1.2 later once
	// // we accept a connection. Ideally this should be done
	// // during next-proto selection, but using TLS <1.2 with
	// // HTTP/2 is still the client's bug.

	// s.TLSConfig.PreferServerCipherSuites = true

	// if !http2strSliceContains(s.TLSConfig.NextProtos, http2NextProtoTLS) {
	// 	s.TLSConfig.NextProtos = append(s.TLSConfig.NextProtos, http2NextProtoTLS)
	// }
	// if !http2strSliceContains(s.TLSConfig.NextProtos, "http/1.1") {
	// 	s.TLSConfig.NextProtos = append(s.TLSConfig.NextProtos, "http/1.1")
	// }

	// if s.TLSNextProto == nil {
	// 	s.TLSNextProto = map[string]func(*Server, *tls.Conn, Handler){}
	// }
	// protoHandler := func(hs *Server, c *tls.Conn, h Handler) {
	// 	if http2testHookOnConn != nil {
	// 		http2testHookOnConn()
	// 	}
	// 	// The TLSNextProto interface predates contexts, so
	// 	// the net/http package passes down its per-connection
	// 	// base context via an exported but unadvertised
	// 	// method on the Handler. This is for internal
	// 	// net/http<=>http2 use only.
	// 	var ctx context.Context
	// 	type baseContexter interface {
	// 		BaseContext() context.Context
	// 	}
	// 	if bc, ok := h.(baseContexter); ok {
	// 		ctx = bc.BaseContext()
	// 	}
	// 	conf.ServeConn(c, &http2ServeConnOpts{
	// 		Context:    ctx,
	// 		Handler:    h,
	// 		BaseConfig: hs,
	// 	})
	// }
	// s.TLSNextProto[http2NextProtoTLS] = protoHandler
	return nil
}

type http2serverConn struct {
	// // Immutable:
	// srv              *http2Server
	// hs               *Server
	// conn             net.Conn
	// bw               *http2bufferedWriter // writing to conn
	// handler          Handler
	// baseCtx          context.Context
	// framer           *http2Framer
	doneServing chan struct{} // closed when serverConn.serve ends
	// readFrameCh      chan http2readFrameResult   // written by serverConn.readFrames
	// wantWriteFrameCh chan http2FrameWriteRequest // from handlers -> serve
	// wroteFrameCh     chan http2frameWriteResult  // from writeFrameAsync -> serve, tickles more frame writes
	// bodyReadCh       chan http2bodyReadMsg       // from handlers -> serve
	serveMsgCh chan interface{} // misc messages & code to send to / run on the serve loop
	// flow             http2outflow                // conn-wide (not stream-specific) outbound flow control
	// inflow           http2inflow                 // conn-wide inbound flow control
	// tlsState         *tls.ConnectionState        // shared by all handlers, like net/http
	// remoteAddrStr    string
	// writeSched       http2WriteScheduler

	// Everything following is owned by the serve loop; use serveG.check():
	serveG http2goroutineLock // used to verify funcs are on serve()
	// pushEnabled                 bool
	// sawClientPreface            bool // preface has already been read, used in h2c upgrade
	// sawFirstSettings            bool // got the initial SETTINGS frame after the preface
	// needToSendSettingsAck       bool
	// unackedSettings             int    // how many SETTINGS have we sent without ACKs?
	// queuedControlFrames         int    // control frames in the writeSched queue
	// clientMaxStreams            uint32 // SETTINGS_MAX_CONCURRENT_STREAMS from client (our PUSH_PROMISE limit)
	// advMaxStreams               uint32 // our SETTINGS_MAX_CONCURRENT_STREAMS advertised the client
	// curClientStreams            uint32 // number of open streams initiated by the client
	// curPushedStreams            uint32 // number of open streams initiated by server push
	// curHandlers                 uint32 // number of running handler goroutines
	// maxClientStreamID           uint32 // max ever seen from client (odd), or 0 if there have been no client requests
	// maxPushPromiseID            uint32 // ID of the last push promise (even), or 0 if there have been no pushes
	// streams                     map[uint32]*http2stream
	// unstartedHandlers           []http2unstartedHandler
	// initialStreamSendWindowSize int32
	// maxFrameSize                int32
	// peerMaxHeaderListSize       uint32            // zero means unknown (default)
	// canonHeader                 map[string]string // http2-lower-case -> Go-Canonical-Case
	// canonHeaderKeysSize         int               // canonHeader keys size in bytes
	// writingFrame                bool              // started writing a frame (on serve goroutine or separate)
	// writingFrameAsync           bool              // started a frame on its own goroutine but haven't heard back on wroteFrameCh
	// needsFrameFlush             bool              // last frame write wasn't a flush
	// inGoAway                    bool              // we've started to or sent GOAWAY
	// inFrameScheduleLoop         bool              // whether we're in the scheduleFrameWrite loop
	// needToSendGoAway            bool              // we need to schedule a GOAWAY frame write
	// goAwayCode                  http2ErrCode
	// shutdownTimer               *time.Timer // nil until used
	// idleTimer                   *time.Timer // nil if unused

	// // Owned by the writeFrameAsync goroutine:
	// headerWriteBuf bytes.Buffer
	// hpackEncoder   *hpack.Encoder

	// Used by startGracefulShutdown.
	shutdownOnce sync.Once
}

type http2serverMessage int

// Message values sent to serveMsgCh.
var (
	http2gracefulShutdownMsg = new(http2serverMessage)
)

// startGracefulShutdown gracefully shuts down a connection. This
// sends GOAWAY with ErrCodeNo to tell the client we're gracefully
// shutting down. The connection isn't closed until all current
// streams are done.
//
// startGracefulShutdown returns immediately; it does not wait until
// the connection has shut down.
func (sc *http2serverConn) startGracefulShutdown() {
	sc.serveG.checkNotOn() // NOT
	sc.shutdownOnce.Do(func() { sc.sendServeMsg(http2gracefulShutdownMsg) })
}

func (sc *http2serverConn) sendServeMsg(msg interface{}) {
	sc.serveG.checkNotOn() // NOT
	select {
	case sc.serveMsgCh <- msg:
	case <-sc.doneServing:
	}
}

// WriteScheduler is the interface implemented by HTTP/2 write schdulers.
// Methods are never called concurrency.
type http2WriteScheduler interface {
}

// PriorityWriteSchedulerConfig configures a priorityWriteScheduler.
type http2PriorityWriteSchedulerConfig struct {
	// MaxClosedNodesInTree controls the maximum number of closed streams to
	// retain in the priority tree. Setting this to zero saves a small amount
	// of memory at the cost of performance.
	//
	// See RFC 7540, Section 5.3.4:
	//   "It is possible for a stream to become closed while prioritization
	//   information ... is in transit. ... This potentially creates suboptimal
	//   prioritization, since the stream could be given a priority that is
	//   different from what is intended. To avoid these problems, an endpoint
	//   SHOULD retain stream prioritization state for a period after streams
	//   become closed. The longer state is retained, the lower the chance that
	//   streams are assigned incorrect or default priority values."
	MaxClosedNodesInTree int

	// MaxIdleNodesInTree controls the maximum number of idle streams to
	// retain in the priority tree. Setting this to zero saves a small amount
	// of memory at the cost of performance.
	//
	// See RFC 7540, Section 5.3.4:
	//   Similarly, streams that are in the "idle" state can be assigned
	//   priority or become a parent of other streams. This allows for the
	//   creation of a grouping node in the dependency tree, which enables
	//   more flexible expressions of priority. Idle streams begin with a
	//   default priority (Section 5.3.5).
	MaxIdleNodesInTree int

	// ThrottleOutOfOrderWrites enables write throttling to help ensure that
	// data is delivered in priority order. This works around a race where
	// stream B depends on stream A and both streams are about to call Write
	// to queue DATA frames. If B wins the race, a naive scheduler would eagerly
	// write as much data from B as possible, but this is suboptimal because A
	// is a higher-priority stream. With throttling enabled, we write a small
	// amount of data from B to minimize the amount of bandwidth that B can
	// steal from A.
	ThrottleOutOfOrderWrites bool
}

// NewPriorityWriteScheduler constructs a WriteScheduler that schedules
// frames by following HTTP/2 priorities as described in RFC 7540 Section 5.3.
// If cfg is nil, default options are used.
func http2NewPriorityWriteScheduler(cfg *http2PriorityWriteSchedulerConfig) http2WriteScheduler {
	if cfg == nil {
		// For justification of these defaults, see:
		// https://docs.google.com/document/d/1oLhNg1skaWD4_DtaoCxdSRN5erEXrH-KnLrMwEpOtFY
		cfg = &http2PriorityWriteSchedulerConfig{
			MaxClosedNodesInTree:     10,
			MaxIdleNodesInTree:       10,
			ThrottleOutOfOrderWrites: false,
		}
	}

	ws := &http2priorityWriteScheduler{
		nodes:                make(map[uint32]*http2priorityNode),
		maxClosedNodesInTree: cfg.MaxClosedNodesInTree,
		maxIdleNodesInTree:   cfg.MaxIdleNodesInTree,
		enableWriteThrottle:  cfg.ThrottleOutOfOrderWrites,
	}
	ws.nodes[0] = &ws.root
	if cfg.ThrottleOutOfOrderWrites {
		ws.writeThrottleLimit = 1024
	} else {
		ws.writeThrottleLimit = math.MaxInt32
	}
	return ws
}

// priorityNode is a node in an HTTP/2 priority tree.
// Each node is associated with a single stream ID.
// See RFC 7540, Section 5.3.
type http2priorityNode struct {
	// q            http2writeQueue        // queue of pending frames to write
	// id           uint32                 // id of the stream, or 0 for the root of the tree
	// weight       uint8                  // the actual weight is weight+1, so the value is in [1,256]
	// state        http2priorityNodeState // open | closed | idle
	// bytes        int64                  // number of bytes written by this node, or 0 if closed
	// subtreeBytes int64                  // sum(node.bytes) of all nodes in this subtree

	// // These links form the priority tree.
	// parent     *http2priorityNode
	// kids       *http2priorityNode // start of the kids list
	// prev, next *http2priorityNode // doubly-linked list of siblings
}

type http2priorityWriteScheduler struct {
	// root is the root of the priority tree, where root.id = 0.
	// The root queues control frames that are not associated with any stream.
	root http2priorityNode

	// nodes maps stream ids to priority tree nodes.
	nodes map[uint32]*http2priorityNode

	// // maxID is the maximum stream id in nodes.
	// maxID uint32

	// // lists of nodes that have been closed or are idle, but are kept in
	// // the tree for improved prioritization. When the lengths exceed either
	// // maxClosedNodesInTree or maxIdleNodesInTree, old nodes are discarded.
	// closedNodes, idleNodes []*http2priorityNode

	// From the config.
	maxClosedNodesInTree int
	maxIdleNodesInTree   int
	writeThrottleLimit   int32
	enableWriteThrottle  bool

	// // tmp is scratch space for priorityNode.walkReadyInOrder to reduce allocations.
	// tmp []*http2priorityNode

	// // pool of empty queues for reuse.
	// queuePool http2writeQueuePool
}
