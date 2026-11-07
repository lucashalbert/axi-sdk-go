package output_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/toon-format/toon-go"
	"go.yaml.in/yaml/v3"

	"github.com/lucashalbert/axi-sdk-go/output"
)

type item struct {
	SKU   string  `json:"sku"`
	Qty   int     `json:"qty"`
	Price float64 `json:"price"`
}

type user struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Tags        []string `json:"tags"`
	Active      bool     `json:"active"`
	Preferences []string `json:"preferences"`
}

func render(t *testing.T, v any, f output.Format) string {
	t.Helper()
	var buf bytes.Buffer
	if err := output.Render(&buf, v, f); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

// The expected strings below are the worked examples from the TOON
// specification (nested object, inline primitive array, tabular array).
func TestRenderTOONSpecExamples(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{
			name: "nested object with inline array and empty array (spec 4.4: key: [])",
			in: map[string]any{"user": user{
				ID: 123, Name: "Ada", Tags: []string{"reading", "gaming"},
				Active: true, Preferences: []string{},
			}},
			want: "user:\n  id: 123\n  name: Ada\n  tags[2]: reading,gaming\n  active: true\n  preferences: []\n",
		},
		{
			name: "tabular array of uniform objects",
			in:   map[string]any{"items": []item{{"A1", 2, 9.99}, {"B2", 1, 14.5}}},
			want: "items[2]{sku,qty,price}:\n  A1,2,9.99\n  B2,1,14.5\n",
		},
		{
			name: "primitive array",
			in:   map[string]any{"tags": []string{"admin", "ops", "dev"}},
			want: "tags[3]: admin,ops,dev\n",
		},
		{
			name: "mixed array uses list form",
			in:   map[string]any{"items": []any{1, map[string]any{"a": 1}, "text"}},
			want: "items[3]:\n  - 1\n  - a: 1\n  - text\n",
		},
		{
			name: "strings that look like other types are quoted",
			in:   map[string]any{"a": "true", "b": "05", "c": "x,y", "d": "", "e": "hello world"},
			want: "a: \"true\"\nb: \"05\"\nc: \"x,y\"\nd: \"\"\ne: hello world\n",
		},
		{
			name: "null and numbers",
			in:   map[string]any{"n": nil, "i": 42, "f": 1.5},
			want: "f: 1.5\ni: 42\nn: null\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := render(t, tt.in, output.TOON); got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestRenderTOONRoundTrip(t *testing.T) {
	in := map[string]any{"items": []item{{"A1", 2, 9.99}, {"B2", 1, 14.5}}}
	var got map[string]any
	if err := toon.UnmarshalString(render(t, in, output.TOON), &got); err != nil {
		t.Fatal(err)
	}
	items := got["items"].([]any)
	if len(items) != 2 || items[1].(map[string]any)["sku"] != "B2" {
		t.Errorf("round trip lost data: %#v", got)
	}
}

func TestRenderJSON(t *testing.T) {
	in := user{ID: 1, Name: "a<b>&", Tags: []string{"x"}}
	got := render(t, in, output.JSON)
	if want := `{"id":1,"name":"a<b>&","tags":["x"],"active":false,"preferences":null}` + "\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	var back user
	if err := json.Unmarshal([]byte(got), &back); err != nil || !reflect.DeepEqual(back, in) {
		t.Errorf("round trip: %v %#v", err, back)
	}
}

func TestRenderYAML(t *testing.T) {
	in := user{ID: 1, Name: "true", Tags: []string{"x", "y"}, Active: true, Preferences: []string{}}
	got := render(t, in, output.YAML)
	want := "id: 1\nname: \"true\"\ntags:\n  - x\n  - y\nactive: true\npreferences: []\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	var back map[string]any
	if err := yaml.Unmarshal([]byte(got), &back); err != nil {
		t.Fatal(err)
	}
	if back["name"] != "true" || back["id"] != 1 {
		t.Errorf("round trip: %#v", back)
	}
}

func TestRenderUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := output.Render(&buf, 1, output.Format(99)); err == nil {
		t.Error("expected error for unknown format")
	}
}

func TestParseFormat(t *testing.T) {
	tests := []struct {
		json, yaml bool
		want       output.Format
	}{{false, false, output.TOON}, {true, false, output.JSON}, {false, true, output.YAML}}
	for _, tt := range tests {
		got, err := output.ParseFormat(tt.json, tt.yaml)
		if err != nil || got != tt.want {
			t.Errorf("ParseFormat(%v,%v) = %v, %v; want %v", tt.json, tt.yaml, got, err, tt.want)
		}
	}
}

func TestParseFormatConflictIsUsageError(t *testing.T) {
	_, err := output.ParseFormat(true, true)
	if !errors.Is(err, output.ErrConflictingFormats) {
		t.Fatalf("err = %v, want ErrConflictingFormats", err)
	}
	var ec interface{ ExitCode() int }
	if !errors.As(err, &ec) || ec.ExitCode() != 2 {
		t.Errorf("error does not carry exit code 2: %v", err)
	}
}

func TestFormatString(t *testing.T) {
	for f, want := range map[output.Format]string{output.TOON: "toon", output.JSON: "json", output.YAML: "yaml"} {
		if f.String() != want {
			t.Errorf("%d.String() = %q, want %q", f, f.String(), want)
		}
	}
}
