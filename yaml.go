// Package yaml provides a wrapper around go-yaml designed to enable a better
// way of handling YAML when marshaling to and from structs.
//
// In short, this package first converts YAML to JSON using go-yaml and then
// uses json.Marshal and json.Unmarshal to convert to or from the struct. This
// means that it effectively reuses the JSON struct tags as well as the custom
// JSON methods MarshalJSON and UnmarshalJSON unlike go-yaml.
package yaml // import "github.com/invopop/yaml"

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Marshal the object into JSON then converts JSON to YAML and returns the
// YAML.
func Marshal(o interface{}) ([]byte, error) {
	j, err := json.Marshal(o)
	if err != nil {
		return nil, fmt.Errorf("error marshaling into JSON: %v", err)
	}

	y, err := JSONToYAML(j)
	if err != nil {
		return nil, fmt.Errorf("error converting JSON to YAML: %v", err)
	}

	return y, nil
}

// JSONOpt is a decoding option for decoding from JSON format.
type JSONOpt func(*json.Decoder) *json.Decoder

// Unmarshal converts YAML to JSON then uses JSON to unmarshal into an object,
// optionally configuring the behavior of the JSON unmarshal.
func Unmarshal(y []byte, o interface{}, opts ...JSONOpt) error {
	dec := yaml.NewDecoder(bytes.NewReader(y))
	return unmarshal(dec, o, opts)
}

func unmarshal(dec *yaml.Decoder, o interface{}, opts []JSONOpt) error {
	vo := reflect.ValueOf(o)
	j, err := yamlToJSON(dec, &vo)
	if err != nil {
		return fmt.Errorf("error converting YAML to JSON: %v", err)
	}

	err = jsonUnmarshal(bytes.NewReader(j), o, opts...)
	if err != nil {
		return fmt.Errorf("error unmarshaling JSON: %v", err)
	}

	return nil
}

// jsonUnmarshal unmarshals the JSON byte stream from the given reader into the
// object, optionally applying decoder options prior to decoding.  We are not
// using json.Unmarshal directly as we want the chance to pass in non-default
// options.
func jsonUnmarshal(r io.Reader, o interface{}, opts ...JSONOpt) error {
	d := json.NewDecoder(r)
	for _, opt := range opts {
		d = opt(d)
	}
	if err := d.Decode(&o); err != nil {
		return fmt.Errorf("while decoding JSON: %v", err)
	}
	return nil
}

// JSONToYAML converts JSON to YAML.
func JSONToYAML(j []byte) ([]byte, error) {
	var n yaml.Node
	// We are using yaml.Unmarshal here (instead of json.Unmarshal) because the
	// Go JSON library doesn't try to pick the right number type (int, float,
	// etc.) when unmarshalling to interface{}, it just picks float64
	// universally. go-yaml does go through the effort of picking the right
	// number type, so we can preserve number type throughout this process.
	err := yaml.Unmarshal(j, &n)
	if err != nil {
		return nil, err
	}

	// Force yaml.Node to be marshaled as formatted YAML.
	enforceNodeStyle(&n)

	// Marshal this object into YAML.
	return yaml.Marshal(&n)
}

func enforceNodeStyle(n *yaml.Node) {
	if n == nil {
		return
	}

	switch n.Kind {
	case yaml.SequenceNode, yaml.MappingNode:
		n.Style = yaml.LiteralStyle
	case yaml.ScalarNode:
		// Special case: if node is a string, then there are special styling
		// rules that we must abide by to conform to yaml.v3. Some of the logic
		// has been copied out, because the other way would've been to re-encode
		// the string which causes a ~2x performance hit!
		//
		// Ideally, we wouldn't need to copy this at all!
		// https://github.com/go-yaml/yaml/pull/574 implements a fix for this
		// issue that included code for the internal node() marshaling function,
		// except https://github.com/go-yaml/yaml/pull/583 was merged instead,
		// and it completely left out the fix for this issue!
		//
		// Instead of trying to make a pull request to a repository which hasn't
		// received any commit in over 2 years and has 125+ open pull requests,
		// I've decided to just copy the code here.
		//
		// There is one case that has been omitted from this code, though: the
		// code makes no attempt at checking for isBase64Float(). The README of
		// the YAML package says that it doesn't support this either (but it is
		// in the code).
		if n.ShortTag() == "!!str" {
			switch {
			case strings.Contains(n.Value, "\n"):
				n.Style = yaml.LiteralStyle
			case isOldBool(n.Value):
				n.Style = yaml.DoubleQuotedStyle
			default:
				n.Style = yaml.FlowStyle
			}
		}
	}

	for _, c := range n.Content {
		enforceNodeStyle(c)
	}
}

// isOldBool is copied from yaml.v3.
func isOldBool(s string) (result bool) {
	switch s {
	case "y", "Y", "yes", "Yes", "YES", "on", "On", "ON",
		"n", "N", "no", "No", "NO", "off", "Off", "OFF":
		return true
	default:
		return false
	}
}

// YAMLToJSON converts YAML to JSON. Since JSON is a subset of YAML,
// passing JSON through this method should be a no-op.
//
// Things YAML can do that are not supported by JSON:
//   - In YAML you can have binary and null keys in your maps. These are invalid
//     in JSON. (int and float keys are converted to strings.)
//   - Binary data in YAML with the !!binary tag is not supported. If you want to
//     use binary data with this library, encode the data as base64 as usual but do
//     not use the !!binary tag in your YAML. This will ensure the original base64
//     encoded data makes it all the way through to the JSON.
func YAMLToJSON(y []byte) ([]byte, error) { //nolint:revive
	dec := yaml.NewDecoder(bytes.NewReader(y))
	return yamlToJSON(dec, nil)
}

func yamlToJSON(dec *yaml.Decoder, jsonTarget *reflect.Value) ([]byte, error) {
	var n yaml.Node
	if err := dec.Decode(&n); err != nil {
		// Functionality changed in v3 which means we need to ignore EOF error.
		// See https://github.com/go-yaml/yaml/issues/639
		if !errors.Is(err, io.EOF) {
			return nil, err
		}
	}

	// YAML objects are not completely compatible with JSON objects (e.g. you
	// can have non-string keys in YAML). So, convert the YAML-compatible object
	// to a JSON-compatible object, failing with an error if irrecoverable
	// incompatibilities happen along the way.
	jsonObj, err := convertToJSONableObject(&n, jsonTarget)
	if err != nil {
		return nil, err
	}

	// Convert this object to JSON and return the data.
	return json.Marshal(jsonObj)
}

func convertToJSONableObject(n *yaml.Node, jsonTarget *reflect.Value) (json.RawMessage, error) { //nolint:gocyclo
	var err error

	// Resolve jsonTarget to a concrete value (i.e. not a pointer or an
	// interface). We pass decodingNull as false because we're not actually
	// decoding into the value, we're just checking if the ultimate target is a
	// string.
	if jsonTarget != nil {
		ju, tu, pv := indirect(*jsonTarget, false)
		// We have a JSON or Text Umarshaler at this level, so we can't be trying
		// to decode into a string.
		if ju != nil || tu != nil {
			jsonTarget = nil
		} else {
			jsonTarget = &pv
		}
	}

	switch n.Kind {
	case yaml.DocumentNode:
		return convertToJSONableObject(n.Content[0], jsonTarget)

	case yaml.MappingNode:
		jsonMap := make(orderedMap, 0, len(n.Content)/2)
		keyNodes := make(map[string]*yaml.Node, len(n.Content)/2)
		for i := 0; i < len(n.Content); i += 2 {
			kNode := n.Content[i]
			vNode := n.Content[i+1]

			var anyKey interface{}
			if err := kNode.Decode(&anyKey); err != nil {
				return nil, fmt.Errorf("error decoding yaml map key %s: %v", kNode.Tag, err)
			}

			// Resolve the key to a string first.
			var key string
			switch typedKey := anyKey.(type) {
			case string:
				key = typedKey
			case int:
				key = strconv.Itoa(typedKey)
			case int64:
				// go-yaml will only return an int64 as a key if the system
				// architecture is 32-bit and the key's value is between 32-bit
				// and 64-bit. Otherwise the key type will simply be int.
				key = strconv.FormatInt(typedKey, 10)
			case float64:
				// Float64 is now supported in keys
				key = strconv.FormatFloat(typedKey, 'g', -1, 64)
			case bool:
				if typedKey {
					key = "true"
				} else {
					key = "false"
				}
			default:
				return nil, fmt.Errorf("unsupported map key of type: %s, key: %+#v, value: %+#v",
					reflect.TypeOf(kNode), kNode, vNode)
			}

			if otherNode, ok := keyNodes[key]; ok {
				return nil, fmt.Errorf("mapping key %q already defined at line %d", key, otherNode.Line)
			}
			keyNodes[key] = kNode

			// jsonTarget should be a struct or a map. If it's a struct, find
			// the field it's going to map to and pass its reflect.Value. If
			// it's a map, find the element type of the map and pass the
			// reflect.Value created from that type. If it's neither, just pass
			// nil - JSON conversion will error for us if it's a real issue.
			if jsonTarget != nil {
				t := *jsonTarget
				if t.Kind() == reflect.Struct {
					keyBytes := []byte(key)
					// Find the field that the JSON library would use.
					var f *field
					fields := cachedTypeFields(t.Type())
					for i := range fields {
						ff := &fields[i]
						if bytes.Equal(ff.nameBytes, keyBytes) {
							f = ff
							break
						}
						// Do case-insensitive comparison.
						if f == nil && ff.equalFold(ff.nameBytes, keyBytes) {
							f = ff
						}
					}
					if f != nil {
						// Find the reflect.Value of the most preferential
						// struct field.
						jtf := t.Field(f.index[0])
						if err := jsonMap.AppendYAML(f.name, vNode, &jtf); err != nil {
							return nil, err
						}
						continue
					}
				} else if t.Kind() == reflect.Map {
					// Create a zero value of the map's element type to use as
					// the JSON target.
					jtv := reflect.Zero(t.Type().Elem())
					if err := jsonMap.AppendYAML(key, vNode, &jtv); err != nil {
						return nil, err
					}
					continue
				}
			}

			if err := jsonMap.AppendYAML(key, vNode, nil); err != nil {
				return nil, err
			}
		}

		return jsonMap.MarshalJSON()

	case yaml.SequenceNode:
		// We need to recurse into arrays in case there are any
		// map[interface{}]interface{}'s inside and to convert any
		// numbers to strings.

		// If jsonTarget is a slice (which it really should be), find the
		// thing it's going to map to. If it's not a slice, just pass nil
		// - JSON conversion will error for us if it's a real issue.
		var jsonSliceElemValue *reflect.Value
		if jsonTarget != nil {
			t := *jsonTarget
			if t.Kind() == reflect.Slice {
				// By default slices point to nil, but we need a reflect.Value
				// pointing to a value of the slice type, so we create one here.
				ev := reflect.Indirect(reflect.New(t.Type().Elem()))
				jsonSliceElemValue = &ev
			}
		}

		// Make and use a new array.
		arr := make([]json.RawMessage, len(n.Content))
		for i, v := range n.Content {
			arr[i], err = convertToJSONableObject(v, jsonSliceElemValue)
			if err != nil {
				return nil, err
			}
		}
		return json.Marshal(arr)

	default:
		var rawObject interface{}
		if err := n.Decode(&rawObject); err != nil {
			return nil, fmt.Errorf("error decoding yaml object %s: %v", n.Tag, err)
		}

		// If the target type is a string and the YAML type is a number,
		// convert the YAML type to a string.
		if jsonTarget != nil && (*jsonTarget).Kind() == reflect.String {
			// Based on my reading of go-yaml, it may return int, int64,
			// float64, or uint64.
			var s string
			switch typedVal := rawObject.(type) {
			case int:
				s = strconv.FormatInt(int64(typedVal), 10)
			case int64:
				s = strconv.FormatInt(typedVal, 10)
			case float64:
				s = strconv.FormatFloat(typedVal, 'g', -1, 64)
			case uint64:
				s = strconv.FormatUint(typedVal, 10)
			case bool:
				if typedVal {
					s = "true"
				} else {
					s = "false"
				}
			}
			if len(s) > 0 {
				rawObject = s
			}
		}

		return json.Marshal(rawObject)
	}
}

type orderedMap []orderedPair

type orderedPair struct {
	K string
	V interface{}
}

func (m *orderedMap) AppendYAML(k string, v *yaml.Node, jsonTarget *reflect.Value) error {
	r, err := convertToJSONableObject(v, jsonTarget)
	if err != nil {
		return fmt.Errorf("%q: %w", k, err)
	}
	*m = append(*m, orderedPair{K: k, V: r})
	return nil
}

func (m orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, p := range m {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := json.Marshal(p.K)
		if err != nil {
			return nil, fmt.Errorf("key %q error: %w", p.K, err)
		}
		buf.Write(k)
		buf.WriteByte(':')
		b, err := json.Marshal(p.V)
		if err != nil {
			return nil, fmt.Errorf("value %q error: %w", p.K, err)
		}
		buf.Write(b)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
