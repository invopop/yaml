package yaml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type MarshalTest struct {
	A string
	B int64
	C float32
	D float64
}

func TestMarshal(t *testing.T) {
	f32String := strconv.FormatFloat(math.MaxFloat32, 'g', -1, 32)
	f64String := strconv.FormatFloat(math.MaxFloat64, 'g', -1, 64)
	s := MarshalTest{"a", math.MaxInt64, math.MaxFloat32, math.MaxFloat64}
	e := []byte(fmt.Sprintf("A: a\nB: %d\nC: %s\nD: %s\n", int64(math.MaxInt64), f32String, f64String))

	t.Run("Marshal", func(t *testing.T) {
		y, err := Marshal(s)
		if err != nil {
			t.Errorf("error marshaling YAML: %v", err)
		}

		if !reflect.DeepEqual(y, e) {
			t.Errorf("marshal YAML was unsuccessful, expected: %#v, got: %#v",
				string(e), string(y))
		}
	})
	t.Run("Encode", func(t *testing.T) {
		var buf bytes.Buffer
		if err := NewEncoder(&buf).Encode(s); err != nil {
			t.Errorf("error encoding YAML: %v", err)
		}

		if y := buf.Bytes(); !reflect.DeepEqual(y, e) {
			t.Errorf("encode YAML was unsuccessful, expected: %#v, got: %#v",
				string(e), string(y))
		}
	})

}

type UnmarshalString struct {
	A string
	B string
}

type UnmarshalStringMap struct {
	A map[string]string
}

type UnmarshalNestedString struct {
	A NestedString
}

type NestedString struct {
	A string
}

type UnmarshalSlice struct {
	A []NestedSlice
}

type NestedSlice struct {
	B string
	C *string
}

func TestUnmarshalStrict(t *testing.T) {
	y := []byte("a: 1")
	s1 := UnmarshalString{}
	e1 := UnmarshalString{A: "1"}
	unmarshalEqual(t, y, &s1, &e1, true)

	y = []byte(`a: "1"`)
	s1 = UnmarshalString{}
	e1 = UnmarshalString{A: "1"}
	unmarshalEqual(t, y, &s1, &e1, true)

	y = []byte("a: true")
	s1 = UnmarshalString{}
	e1 = UnmarshalString{A: "true"}
	unmarshalEqual(t, y, &s1, &e1, true)

	y = []byte("a: 1")
	s1 = UnmarshalString{}
	e1 = UnmarshalString{A: "1"}
	unmarshalEqual(t, y, &s1, &e1, true)

	y = []byte("a:\n  a: 1")
	s2 := UnmarshalNestedString{}
	e2 := UnmarshalNestedString{NestedString{"1"}}
	unmarshalEqual(t, y, &s2, &e2, true)

	y = []byte("a:\n  - b: abc\n    c: def\n  - b: 123\n    c: 456\n")
	s3 := UnmarshalSlice{}
	e3 := UnmarshalSlice{[]NestedSlice{{"abc", strPtr("def")}, {"123", strPtr("456")}}}
	unmarshalEqual(t, y, &s3, &e3, true)

	y = []byte("a:\n  b: 1")
	s4 := UnmarshalStringMap{}
	e4 := UnmarshalStringMap{map[string]string{"b": "1"}}
	unmarshalEqual(t, y, &s4, &e4, true)

	y = []byte(`
a:
  name: TestA
b:
  name: TestB
`)
	type NamedThing struct {
		Name string `json:"name"`
	}
	s5 := map[string]*NamedThing{}
	e5 := map[string]*NamedThing{
		"a": {Name: "TestA"},
		"b": {Name: "TestB"},
	}
	unmarshalEqual(t, y, &s5, &e5, true)
}

// TestUnmarshalNonStrict tests that we parse ambiguous YAML without error.
func TestUnmarshalNonStrict(t *testing.T) {
	for _, tc := range []struct {
		yaml []byte
		want UnmarshalString
	}{
		{
			yaml: []byte("a: 1"),
			want: UnmarshalString{A: "1"},
		},
		{
			// Order does not matter.
			yaml: []byte("b: 1\na: 2"),
			want: UnmarshalString{A: "2", B: "1"},
		},
		{
			// Unknown field get ignored.
			yaml: []byte("a: 1\nunknownField: 2"),
			want: UnmarshalString{A: "1"},
		},
		{
			// Unknown fields get ignored.
			yaml: []byte("unknownOne: 2\na: 1\nunknownTwo: 2"),
			want: UnmarshalString{A: "1"},
		},
		{
			// In YAML, `YES` is no longer Boolean true.
			yaml: []byte("a: YES"),
			want: UnmarshalString{A: "YES"},
		},
	} {
		s := UnmarshalString{}
		unmarshalEqual(t, tc.yaml, &s, &tc.want, false)
	}
}

func unmarshalEqual(t *testing.T, y []byte, s, e interface{}, knowFields bool) {
	t.Run("Unmarshal", func(t *testing.T) {
		if err := Unmarshal(y, s); err != nil {
			t.Errorf("Unmarshal(%#q, s) = %v", string(y), err)
			return
		}

		if !reflect.DeepEqual(s, e) {
			t.Errorf("Unmarshal(%#q, s) = %+#v; want %+#v", string(y), s, e)
		}
	})
	t.Run("Decode", func(t *testing.T) {
		d := NewDecoder(bytes.NewReader(y))
		if knowFields {
			d.KnownFields()
		}
		if err := d.Decode(s); err != nil {
			t.Errorf("Decode(%#q, s) = %v", string(y), err)
			return
		}

		if !reflect.DeepEqual(s, e) {
			t.Errorf("Decode(%#q, s) = %+#v; want %+#v", string(y), s, e)
		}
	})
}

// TestUnmarshalErrors tests that we return an error on ambiguous YAML.
func TestUnmarshalErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		yaml       []byte
		knowFields bool
		wantErr    string
	}{
		{
			name:    "Declaring `a` twice produces an error",
			yaml:    []byte("a: 1\na: 2"),
			wantErr: `key "a" already defined`,
		},
		{
			name:    "Not ignoring first declaration of A with wrong type",
			yaml:    []byte("a: [1,2,3]\na: value-of-a"),
			wantErr: `key "a" already defined`,
		},
		{
			name:    "Declaring field `true` twice",
			yaml:    []byte("true: string-value-of-yes\ntrue: 1"),
			wantErr: `key "true" already defined`,
		},
		{
			name:       "Declaring unknown C field",
			yaml:       []byte("C: 1"),
			knowFields: true,
			wantErr:    `json: unknown field "C"`,
		},
	} {
		t.Run(tc.name+": unmarshal", func(t *testing.T) {
			if tc.knowFields {
				t.Skip("decoder only tc")
				return
			}
			s := UnmarshalString{}
			err := Unmarshal(tc.yaml, &s)
			if tc.wantErr != "" && err == nil {
				t.Errorf("Unmarshal(%#q, &s) = nil; want error", string(tc.yaml))
				return
			}
			if tc.wantErr == "" && err != nil {
				t.Errorf("Unmarshal(%#q, &s) = %v; want no error", string(tc.yaml), err)
				return
			}
			// We only expect errors during unmarshalling YAML.
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Unmarshal(%#q, &s) = %v; want err contains %#q", string(tc.yaml), err, tc.wantErr)
			}
		})
		t.Run(tc.name+": decode", func(t *testing.T) {
			s := UnmarshalString{}
			d := NewDecoder(bytes.NewReader(tc.yaml))
			if tc.knowFields {
				d.KnownFields()
			}
			err := d.Decode(&s)
			if tc.wantErr != "" && err == nil {
				t.Errorf("Unmarshal(%#q, &s) = nil; want error", string(tc.yaml))
				return
			}
			if tc.wantErr == "" && err != nil {
				t.Errorf("Unmarshal(%#q, &s) = %v; want no error", string(tc.yaml), err)
				return
			}
			// We only expect errors during unmarshalling YAML.
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Unmarshal(%#q, &s) = %v; want err contains %#q", string(tc.yaml), err, tc.wantErr)
			}
		})
	}
}

type Case struct {
	input  string
	output string
	// By default we test that reversing the output == input. But if there is a
	// difference in the reversed output, you can optionally specify it here.
	reverse *string
}

type RunType int

const (
	RunTypeJSONToYAML RunType = iota
	RunTypeYAMLToJSON
)

func TestJSONToYAML(t *testing.T) {
	cases := []Case{
		{
			`{"t":"a"}`,
			"t: a\n",
			nil,
		}, {
			`{"t":null}`,
			"t: null\n",
			nil,
		},
	}

	runCases(t, RunTypeJSONToYAML, cases)
}

func TestYAMLToJSON(t *testing.T) {
	cases := []Case{
		{
			"t: a\n",
			`{"t":"a"}`,
			nil,
		}, {
			"t: \n",
			`{"t":null}`,
			strPtr("t: null\n"),
		}, {
			"t: null\n",
			`{"t":null}`,
			nil,
		},
		{
			"true: yes\n",
			`{"true":"yes"}`,
			strPtr("\"true\": \"yes\"\n"),
		},
		{
			"false: yes\n",
			`{"false":"yes"}`,
			strPtr("\"false\": \"yes\"\n"),
		},
		{
			"1: a\n",
			`{"1":"a"}`,
			strPtr("\"1\": a\n"),
		}, {
			"1000000000000000000000000000000000000: a\n",
			`{"1e+36":"a"}`,
			strPtr("\"1e+36\": a\n"),
		}, {
			"1e+36: a\n",
			`{"1e+36":"a"}`,
			strPtr("\"1e+36\": a\n"),
		}, {
			"\"1e+36\": a\n",
			`{"1e+36":"a"}`,
			nil,
		}, {
			"\"1.2\": a\n",
			`{"1.2":"a"}`,
			nil,
		}, {
			"- t: a\n",
			`[{"t":"a"}]`,
			nil,
		}, {
			"- t: a\n" +
				"- t:\n" +
				"    b: 1\n" +
				"    c: 2\n",
			`[{"t":"a"},{"t":{"b":1,"c":2}}]`,
			nil,
		}, {
			`[{t: a}, {t: {b: 1, c: 2}}]`,
			`[{"t":"a"},{"t":{"b":1,"c":2}}]`,
			strPtr("- t: a\n" +
				"- t:\n" +
				"    b: 1\n" +
				"    c: 2\n"),
		}, {
			"- t: \n",
			`[{"t":null}]`,
			strPtr("- t: null\n"),
		}, {
			"- t: null\n",
			`[{"t":null}]`,
			nil,
		},
	}

	// Cases that should produce errors.
	_ = []Case{
		{
			"~: a",
			`{"null":"a"}`,
			nil,
		}, {
			"a: !!binary gIGC\n",
			"{\"a\":\"\x80\x81\x82\"}",
			nil,
		},
	}

	runCases(t, RunTypeYAMLToJSON, cases)
}

func runCases(t *testing.T, runType RunType, cases []Case) {
	var f func([]byte) ([]byte, error)
	var invF func([]byte) ([]byte, error)
	var msg string
	var invMsg string
	if runType == RunTypeJSONToYAML {
		f = JSONToYAML
		invF = YAMLToJSON
		msg = "JSON to YAML"
		invMsg = "YAML back to JSON"
	} else {
		f = YAMLToJSON
		invF = JSONToYAML
		msg = "YAML to JSON"
		invMsg = "JSON back to YAML"
	}

	for _, c := range cases {
		// Convert the string.
		t.Logf("converting %s\n", c.input)
		output, err := f([]byte(c.input))
		if err != nil {
			t.Errorf("Failed to convert %s, input: `%s`, err: %v", msg, c.input, err)
		}

		// Check it against the expected output.
		if string(output) != c.output {
			t.Errorf("Failed to convert %s, input: `%s`, expected `%s`, got `%s`",
				msg, c.input, c.output, string(output))
		}

		// Set the string that we will compare the reversed output to.
		reverse := c.input
		// If a special reverse string was specified, use that instead.
		if c.reverse != nil {
			reverse = *c.reverse
		}

		// Reverse the output.
		input, err := invF(output)
		if err != nil {
			t.Errorf("Failed to convert %s, input: `%s`, err: %v", invMsg, string(output), err)
		}

		// Check the reverse is equal to the input (or to *c.reverse).
		if string(input) != reverse {
			t.Errorf("Failed to convert %s, input: `%s`, expected `%s`, got `%s`",
				invMsg, string(output), reverse, string(input))
		}
	}

}

// To be able to easily fill in the *Case.reverse string above.
func strPtr(s string) *string {
	return &s
}

func TestYAMLToJSONDuplicateFields(t *testing.T) {
	data := []byte("foo: bar\nfoo: baz\n")
	if _, err := YAMLToJSON(data); err == nil {
		t.Error("expected YAMLtoJSON to fail on duplicate field names")
	}
}

type MultiDoc struct {
	Test int `json:"test"`
}

func TestMultiDocDecode(t *testing.T) {
	data := `---
test: 1
---
test: 2
---
test: 3
`
	decoder := NewDecoder(strings.NewReader(data))

	for i := 1; i < 4; i++ {
		var obj MultiDoc
		if err := decoder.Decode(&obj); err != nil {
			t.Errorf("decode #%d failed: %s", i, err)
		}
		if obj.Test != i {
			t.Errorf("decoded #%d has incorrect value %#v", i, obj)
		}
	}
	var obj MultiDoc
	if err := decoder.Decode(&obj); !errors.Is(err, io.EOF) {
		t.Errorf("decode should return EOF but got: %s", err)
	}
}

func TestMultiDocEncode(t *testing.T) {
	docs := []MultiDoc{
		{
			Test: 1,
		},
		{
			Test: 2,
		},
		{
			Test: 3,
		},
	}
	expected := `test: 1
---
test: 2
---
test: 3
`

	var buf bytes.Buffer
	encoder := NewEncoder(&buf)
	for _, obj := range docs {
		if err := encoder.Encode(obj); err != nil {
			t.Errorf("encode object %#v failed: %s", obj, err)
		}
	}
	if encoded := buf.String(); encoded != expected {
		t.Errorf("expected %s, but got %s", expected, encoded)

	}
}
