package decode

import (
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// MaxSourceBytes bounds encoded and decoded provider bodies. Keyboxes are a few KiB.
const MaxSourceBytes = 1024 * 1024

func readSource(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSourceBytes {
		return nil, fmt.Errorf("source exceeds %d bytes", MaxSourceBytes)
	}
	return data, nil
}

func writeSource(w io.Writer, data []byte) error {
	_, err := io.Copy(w, strings.NewReader(string(data)))
	return err
}

// Pipeline applies Duck/Tricky Addon's declarative provider decoder syntax.
// Steps are recognized as data; no command or shell fragment is executed.
// Hex decoding accepts whitespace but rejects all other non-hex characters.
func Pipeline(pipeline string, r io.Reader, w io.Writer) error {
	steps := strings.Split(pipeline, "|")
	if len(steps) > 32 {
		return fmt.Errorf("too many decoder steps")
	}
	kinds := make([]string, len(steps))
	for i, step := range steps {
		switch strings.Join(strings.Fields(step), " ") {
		case "", "cat":
			kinds[i] = "identity"
		case "base64 -d", "base64 --decode":
			kinds[i] = "base64"
		case "xxd -r -p", "xxd -p -r", "xxd -rp", "xxd -pr":
			kinds[i] = "hex"
		default:
			return fmt.Errorf("unsupported decoder step %d", i+1)
		}
	}
	data, err := readSource(r)
	if err != nil {
		return err
	}
	for i, kind := range kinds {
		switch kind {
		case "base64":
			data, err = b64(data)
		case "hex":
			data, err = hex.DecodeString(string(stripSpace(data)))
		}
		if err != nil {
			return fmt.Errorf("decoder step %d: %w", i+1, err)
		}
	}
	return writeSource(w, data)
}

// Addon decodes the default provider shipped by Duck ToolBox / Tricky Addon.
func Addon(r io.Reader, w io.Writer) error {
	return Pipeline("xxd -r -p | base64 -d", r, w)
}
