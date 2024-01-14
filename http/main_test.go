// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package http_test

import (
	"net/http"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func interestingGoroutines() (gs []string) {
	buf := make([]byte, 2<<20)
	buf = buf[:runtime.Stack(buf, true)]
	for _, g := range strings.Split(string(buf), "\n\n") {
		_, stack, _ := strings.Cut(g, "\n")
		stack = strings.TrimSpace(stack)
		if stack == "" ||
			strings.Contains(stack, "testing.(*M).before.func1") ||
			strings.Contains(stack, "os/signal.signal_recv") ||
			strings.Contains(stack, "created by net.startServer") ||
			strings.Contains(stack, "created by testing.RunTests") ||
			strings.Contains(stack, "closeWriteAndWait") ||
			strings.Contains(stack, "testing.Main(") ||
			// These only show up with GOTRACEBACK=2; Issue 5005 (comment 28)
			strings.Contains(stack, "runtime.goexit") ||
			strings.Contains(stack, "created by runtime.gc") ||
			strings.Contains(stack, "interestingGoroutines") ||
			strings.Contains(stack, "runtime.MHeap_Scavenger") {
			continue
		}
		gs = append(gs, stack)
	}
	sort.Strings(gs)
	return
}

// setParallel marks t as a parallel test if we're in short mode
// (all.bash), but as a serial test otherwise. Using t.Parallel isn't
// compatible with the afterTest func in non-short mode.
func setParallel(t *testing.T) {
	// if strings.Contains(t.Name(), "HTTP2") {
	// 	http.CondSkipHTTP2(t)
	// }
	if testing.Short() {
		t.Parallel()
	}
}

var leakReported bool

func afterTest(t testing.TB) {
	http.DefaultTransport.(*http.Transport).CloseIdleConnections()
	if testing.Short() {
		return
	}
	if leakReported {
		// To avoid confusion, only report the first leak of each test run.
		// After the first leak has been reported, we can't tell whether the leaked
		// goroutines are a new leak from a subsequent test or just the same
		// goroutines from the first leak still hanging around, and we may add a lot
		// of latency waiting for them to exit at the end of each test.
		return
	}

	// We shouldn't be running the leak check for parallel tests, because we might
	// report the goroutines from a test that is still running as a leak from a
	// completely separate test that has just finished. So we use non-atomic loads
	// and stores for the leakReported variable, and store every time we start a
	// leak check so that the race detector will flag concurrent leak checks as a
	// race even if we don't detect any leaks.
	leakReported = true

	var bad string
	badSubstring := map[string]string{
		").readLoop(":  "a Transport",
		").writeLoop(": "a Transport",
		"created by net/http/httptest.(*Server).Start": "an httptest.Server",
		"timeoutHandler":        "a TimeoutHandler",
		"net.(*netFD).connect(": "a timing out dial",
		").noteClientGone(":     "a closenotifier sender",
	}
	var stacks string
	for i := 0; i < 10; i++ {
		bad = ""
		stacks = strings.Join(interestingGoroutines(), "\n\n")
		for substr, what := range badSubstring {
			if strings.Contains(stacks, substr) {
				bad = what
			}
		}
		if bad == "" {
			leakReported = false
			return
		}
		// Bad stuff found, but goroutines might just still be
		// shutting down, so give it some time.
		time.Sleep(250 * time.Millisecond)
	}
	t.Errorf("Test appears to have leaked %s:\n%s", bad, stacks)
}
