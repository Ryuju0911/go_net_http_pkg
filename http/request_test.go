// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package http_test

import (
	"bytes"
	"fmt"
	"io"
	. "net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestQuery(t *testing.T) {
	req := &Request{Method: "GET"}
	req.URL, _ = url.Parse("http://www.google.com/search?q=foo&q=bar")
	if q := req.FormValue("q"); q != "foo" {
		t.Errorf(`req.FormValue("q") = %q, want "foo"`, q)
	}
}

// Issue #25192: Test that ParseForm fails but still parses the form when a URL
// containing a semicolon is provided.
func TestParseFormSemicolonSeparator(t *testing.T) {
	for _, method := range []string{"POST", "PATCH", "PUT", "GET"} {
		req, _ := NewRequest(method, "http://www.google.com/search?q=foo;q=bar&a=1",
			strings.NewReader("q"))
		err := req.ParseForm()
		if err == nil {
			t.Fatalf(`for method %s, ParseForm expected an error, got success`, method)
		}
		wantForm := url.Values{"a": []string{"1"}}
		if !reflect.DeepEqual(req.Form, wantForm) {
			t.Fatalf("for method %s, ParseForm expected req.Form = %v, want %v", method, req.Form, wantForm)
		}
	}
}

func TestParseFormQuery(t *testing.T) {
	req, _ := NewRequest("POST", "http://www.google.com/search?q=foo&q=bar&both=x&prio=1&orphan=nope&empty=not",
		strings.NewReader("z=post&both=y&prio=2&=nokey&orphan&empty=&"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; param=value")

	if q := req.FormValue("q"); q != "foo" {
		t.Errorf(`req.FormValue("q") = %q, want "foo"`, q)
	}
	if z := req.FormValue("z"); z != "post" {
		t.Errorf(`req.FormValue("z") = %q, want "post"`, z)
	}
	if bq, found := req.PostForm["q"]; found {
		t.Errorf(`req.PostForm["q"] = %q, want no entry in map`, bq)
	}
	if bz := req.PostFormValue("z"); bz != "post" {
		t.Errorf(`req.PostFormValue("z") = %q, want "post"`, bz)
	}
	if qs := req.Form["q"]; !reflect.DeepEqual(qs, []string{"foo", "bar"}) {
		t.Errorf(`req.Form["q"] = %q, want ["foo", "bar"]`, qs)
	}
	if both := req.Form["both"]; !reflect.DeepEqual(both, []string{"y", "x"}) {
		t.Errorf(`req.Form["both"] = %q, want ["y", "x"]`, both)
	}
	if prio := req.FormValue("prio"); prio != "2" {
		t.Errorf(`req.FormValue("prio") = %q, want "2" (from body)`, prio)
	}
	if orphan := req.Form["orphan"]; !reflect.DeepEqual(orphan, []string{"", "nope"}) {
		t.Errorf(`req.FormValue("orphan") = %q, want "" (from body)`, orphan)
	}
	if empty := req.Form["empty"]; !reflect.DeepEqual(empty, []string{"", "not"}) {
		t.Errorf(`req.FormValue("empty") = %q, want "" (from body)`, empty)
	}
	if nokey := req.Form[""]; !reflect.DeepEqual(nokey, []string{"nokey"}) {
		t.Errorf(`req.FormValue("nokey") = %q, want "nokey" (from body)`, nokey)
	}
}

// Tests that we only parse the form automatically for certain methods.
func TestParseFormQueryMethods(t *testing.T) {
	for _, method := range []string{"POST", "PATCH", "PUT", "FOO"} {
		req, _ := NewRequest(method, "http://www.google.com/search",
			strings.NewReader("foo=bar"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; param=value")
		want := "bar"
		if method == "FOO" {
			want = ""
		}
		if got := req.FormValue("foo"); got != want {
			t.Errorf(`for method %s, FormValue("foo") = %q; want %q`, method, got, want)
		}
	}
}

func TestParseFormUnknownContentType(t *testing.T) {
	for _, test := range []struct {
		name        string
		wantErr     string
		contentType Header
	}{
		{"text", "", Header{"Content-Type": {"text/plain"}}},
		// Empty content type is legal - may be treated as
		// application/octet-stream (RFC 7231, section 3.1.1.5)
		{"empty", "", Header{}},
		{"boundary", "mime: invalid media parameter", Header{"Content-Type": {"text/plain; boundary="}}},
		{"unknown", "", Header{"Content-Type": {"application/unknown"}}},
	} {
		t.Run(test.name,
			func(t *testing.T) {
				req := &Request{
					Method: "POST",
					Header: test.contentType,
					Body:   io.NopCloser(strings.NewReader("body")),
				}
				err := req.ParseForm()
				switch {
				case err == nil && test.wantErr != "":
					t.Errorf("unexpected success; want error %q", test.wantErr)
				case err != nil && test.wantErr == "":
					t.Errorf("want success, got error: %v", err)
				case test.wantErr != "" && test.wantErr != fmt.Sprint(err):
					t.Errorf("got error %q; want %q", err, test.wantErr)
				}
			},
		)
	}
}

func TestParseFormInitializeOnError(t *testing.T) {
	nilBody, _ := NewRequest("POST", "http://www.google.com/search?q=foo", nil)
	tests := []*Request{
		nilBody,
		{Method: "GET", URL: nil},
	}
	for i, req := range tests {
		err := req.ParseForm()
		if req.Form == nil {
			t.Errorf("%d. Form not initialized, error %v", i, err)
		}
		if req.PostForm == nil {
			t.Errorf("%d. PostForm not initialized, error %v", i, err)
		}
	}
}

func TestMultiPartReader(t *testing.T) {
	tests := []struct {
		shouldError bool
		contentType string
	}{
		{false, `multipart/form-data; boundary="foo123"`},
		{false, `multipart/mixed; boundary="foo123"`},
		{true, `text/plain`},
	}

	for i, test := range tests {
		req := &Request{
			Method: "POST",
			Header: Header{"Content-Type": {test.contentType}},
			Body:   io.NopCloser(new(bytes.Buffer)),
		}
		multipart, err := req.MultipartReader()
		if test.shouldError {
			if err == nil || multipart != nil {
				t.Errorf("test %d: unexpectedly got nil-error (%v) or non-nil-multipart (%v)", i, err, multipart)
			}
			continue
		}
		if err != nil || multipart == nil {
			t.Errorf("test %d: unexpectedly got error (%v) or nil-multipart (%v)", i, err, multipart)
		}
	}
}
