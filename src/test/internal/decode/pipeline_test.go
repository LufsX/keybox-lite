package decode

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestProviderDecoders(t *testing.T) {
	xml := []byte("<AndroidAttestation>synthetic fixture</AndroidAttestation>")
	addon := hex.EncodeToString([]byte(base64.StdEncoding.EncodeToString(xml)))
	wrapped := addon[:10] + "\n\t" + addon[10:]
	for _, pipeline := range []string{
		"xxd -r -p | base64 -d", "xxd -p -r | base64 --decode",
		"xxd -rp | base64 -d", "cat | xxd -pr | base64 -d",
	} {
		var out bytes.Buffer
		if err := Pipeline(pipeline, strings.NewReader(wrapped), &out); err != nil || !bytes.Equal(out.Bytes(), xml) {
			t.Fatalf("%s: got %q, %v", pipeline, out.String(), err)
		}
	}
	var out bytes.Buffer
	if err := Addon(strings.NewReader(wrapped), &out); err != nil || !bytes.Equal(out.Bytes(), xml) {
		t.Fatalf("Addon: %q, %v", out.String(), err)
	}
}

func TestExistingSourceRoundTrips(t *testing.T) {
	xml := []byte("<AndroidAttestation>synthetic fixture</AndroidAttestation>")
	meow := []byte(hex.EncodeToString(rot13(xml)))
	for i := 0; i < 10; i++ {
		meow = []byte(base64.StdEncoding.EncodeToString(meow))
	}
	spectre := []byte(base64.StdEncoding.EncodeToString(xml))
	for i, c := range spectre {
		if pos := strings.IndexByte(spectreStd, c); pos >= 0 {
			spectre[i] = spectreShuffled[pos]
		}
	}
	for _, tc := range []struct {
		name string
		data []byte
		fn   func(io.Reader, io.Writer) error
	}{{"meow", meow, Meow}, {"spectre", spectre, Spectre}} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := tc.fn(bytes.NewReader(tc.data), &out); err != nil || !bytes.Equal(out.Bytes(), xml) {
				t.Fatalf("round trip: %q, %v", out.String(), err)
			}
		})
	}
}

func TestPipelineFailsWithoutPublishing(t *testing.T) {
	for _, tc := range []struct{ pipeline, input string }{
		{"xxd -r -p | base64 -d", "not hex"},
		{"xxd -r -p | base64 -d", "6"},
		{"base64 -d", "eA==garbage"},
		{"base64 -d | sh", "eA=="},
		{"cat; touch /tmp/unsafe", "x"},
		{"$(id)", "x"},
		{"xxd -r -p", "61#62"},
		{strings.Repeat("cat|", 33), "x"},
	} {
		var out bytes.Buffer
		if err := Pipeline(tc.pipeline, strings.NewReader(tc.input), &out); err == nil || out.Len() != 0 {
			t.Fatalf("accepted %q / %q, output %q", tc.pipeline, tc.input, out.String())
		}
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestDecoderLimitsAndWriteErrors(t *testing.T) {
	tooLarge := strings.Repeat("A", MaxSourceBytes+1)
	for _, fn := range []func(io.Reader, io.Writer) error{Addon, Meow, Spectre} {
		var out bytes.Buffer
		if err := fn(strings.NewReader(tooLarge), &out); err == nil || out.Len() != 0 {
			t.Fatal("oversized source accepted or partially published")
		}
	}
	if err := Pipeline("cat", strings.NewReader("synthetic"), shortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer error = %v", err)
	}
	var out bytes.Buffer
	if err := Pipeline("", strings.NewReader("synthetic"), &out); err != nil || out.String() != "synthetic" {
		t.Fatalf("identity = %q, %v", out.String(), err)
	}
}
