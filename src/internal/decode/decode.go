// Package decode implements the keybox source encodings.
package decode

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

const (
	spectreShuffled = "1dgWnocayqxU3r6vA5lCIPYfHmkV08b4tz+KMsp2NQ9LRXihODwSj7BEFJ/ZuGTe"
	spectreStd      = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
)

func stripSpace(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
		default:
			out = append(out, c)
		}
	}
	return out
}

func b64(data []byte) ([]byte, error) {
	return base64.StdEncoding.DecodeString(string(stripSpace(data)))
}

func rot13(data []byte) []byte {
	out := make([]byte, len(data))
	for i, c := range data {
		switch {
		case c >= 'a' && c <= 'z':
			out[i] = 'a' + (c-'a'+13)%26
		case c >= 'A' && c <= 'Z':
			out[i] = 'A' + (c-'A'+13)%26
		default:
			out[i] = c
		}
	}
	return out
}

// Meow decodes the MeowDump "Megatron" pipeline: base64 x10 -> hex -> ROT13.
func Meow(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	for i := 0; i < 10; i++ {
		if data, err = b64(data); err != nil {
			return fmt.Errorf("base64 step %d: %w", i+1, err)
		}
	}
	raw, err := hex.DecodeString(string(stripSpace(data)))
	if err != nil {
		return fmt.Errorf("hex: %w", err)
	}
	_, err = w.Write(rot13(raw))
	return err
}

// Spectre decodes the dpejoh payload: shuffled base64 alphabet -> base64.
func Spectre(r io.Reader, w io.Writer) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	table := make(map[byte]byte, len(spectreShuffled))
	for i := 0; i < len(spectreShuffled); i++ {
		table[spectreShuffled[i]] = spectreStd[i]
	}
	for i, c := range data {
		if m, ok := table[c]; ok {
			data[i] = m
		}
	}
	raw, err := b64(data)
	if err != nil {
		return fmt.Errorf("base64: %w", err)
	}
	_, err = w.Write(raw)
	return err
}
