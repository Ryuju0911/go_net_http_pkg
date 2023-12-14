// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package http

import (
	"log"
	"os"
	"strings"
	"testing"
)

func TestCookieSanitizeValue(t *testing.T) {
	defer log.SetOutput(os.Stderr)
	var logbuf strings.Builder
	log.SetOutput(&logbuf)

	tests := []struct {
		in, want string
	}{
		{"foo", "foo"},
		{"foo;bar", "foobar"},
		{"foo\\bar", "foobar"},
		{"foo\"bar", "foobar"},
		{"\x00\x7e\x7f\x80", "\x7e"},
		{`"withquotes"`, "withquotes"},
		{"a z", `"a z"`},
		{" z", `" z"`},
		{"a ", `"a "`},
		{"a,z", `"a,z"`},
		{",z", `",z"`},
		{"a,", `"a,"`},
	}
	for _, tt := range tests {
		if got := sanitizeCookieValue(tt.in); got != tt.want {
			t.Errorf("sanitizeCookieValue(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}

	if got, sub := logbuf.String(), "dropping invalid bytes"; !strings.Contains(got, sub) {
		t.Errorf("Expected substring %q in log output. Got:\n%s", sub, got)
	}
}
