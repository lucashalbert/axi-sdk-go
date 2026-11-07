// Package output renders values for agents and humans.
//
// TOON is the default because it is the most compact. All three formats
// honour `json` struct tags, so a type renders the same field names
// everywhere; a `toon` tag takes precedence for TOON only.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/toon-format/toon-go"
	"go.yaml.in/yaml/v3"
)

// Format selects an output encoding. The zero value is TOON.
type Format int

// Supported output formats.
const (
	TOON Format = iota
	JSON
	YAML
)

func (f Format) String() string {
	switch f {
	case TOON:
		return "toon"
	case JSON:
		return "json"
	case YAML:
		return "yaml"
	}
	return fmt.Sprintf("Format(%d)", int(f))
}

// usageError is a flag-combination error. It reports exit code 2, which
// axierr.ExitCode honours through the ExitCode method.
type usageError string

func (e usageError) Error() string { return string(e) }

// ExitCode is the usage-error exit code.
func (usageError) ExitCode() int { return 2 }

// ErrConflictingFormats is returned by ParseFormat when more than one
// format flag is set. It is a usage error (exit code 2).
var ErrConflictingFormats error = usageError("--json and --yaml are mutually exclusive")

// ParseFormat maps the --json and --yaml flags to a Format, defaulting to TOON.
func ParseFormat(json, yaml bool) (Format, error) {
	switch {
	case json && yaml:
		return TOON, ErrConflictingFormats
	case json:
		return JSON, nil
	case yaml:
		return YAML, nil
	}
	return TOON, nil
}

// Render writes v to w in format f, terminated by a newline.
func Render(w io.Writer, v any, f Format) error {
	var (
		b   []byte
		err error
	)
	switch f {
	case TOON:
		b, err = toon.Marshal(v)
	case JSON:
		b, err = marshalJSON(v)
	case YAML:
		b, err = marshalYAML(v)
	default:
		return fmt.Errorf("output: unknown format %d", int(f))
	}
	if err != nil {
		return fmt.Errorf("output: render %s: %w", f, err)
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	_, err = w.Write(b)
	return err
}

// marshalJSON is compact and leaves <, > and & unescaped, which is what a
// reader of terminal output expects.
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// marshalYAML goes through JSON so that json tags and field order apply,
// then re-encodes the parsed tree in block style.
func marshalYAML(v any) ([]byte, error) {
	j, err := marshalJSON(v)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(j, &doc); err != nil {
		return nil, err
	}
	clearStyle(&doc)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func clearStyle(n *yaml.Node) {
	n.Style = 0
	for _, c := range n.Content {
		clearStyle(c)
	}
}
