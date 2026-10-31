// Package contracts decodes bounded JSON/YAML with duplicate/unknown key rejection.
package contracts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

func Decode(data []byte, target any) error {
	if len(data) > 524288 {
		return fmt.Errorf("contract exceeds 512 KiB")
	}
	var document yaml.Node
	d := yaml.NewDecoder(bytes.NewReader(data))
	if e := d.Decode(&document); e != nil {
		return e
	}
	var extra yaml.Node
	if e := d.Decode(&extra); e != io.EOF {
		return fmt.Errorf("exactly one contract document required")
	}
	var validate func(*yaml.Node) error
	validate = func(n *yaml.Node) error {
		if n.Kind == yaml.AliasNode {
			return fmt.Errorf("YAML aliases are unsupported")
		}
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				key := n.Content[i]
				if key.Tag != "!!str" || seen[key.Value] {
					return fmt.Errorf("non-string or duplicate key %q", key.Value)
				}
				seen[key.Value] = true
			}
		}
		if n.Kind == yaml.ScalarNode && n.Tag == "!!float" {
			return fmt.Errorf("contract numbers must be integers")
		}
		for _, child := range n.Content {
			if e := validate(child); e != nil {
				return e
			}
		}
		return nil
	}
	if e := validate(&document); e != nil {
		return e
	}
	var value any
	if e := document.Decode(&value); e != nil {
		return e
	}
	b, e := json.Marshal(value)
	if e != nil {
		return e
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// StrictJSON rejects duplicate keys before decoding into a typed RPC contract.
func StrictJSON(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var consume func() error
	consume = func() error {
		token, e := d.Token()
		if e != nil {
			return e
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return e
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate or invalid JSON key")
				}
				seen[name] = true
				if e = consume(); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return fmt.Errorf("invalid object")
			}
		case '[':
			for d.More() {
				if e = consume(); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return fmt.Errorf("invalid array")
			}
		default:
			return fmt.Errorf("unexpected delimiter")
		}
		return nil
	}
	if e := consume(); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("exactly one JSON value required")
	}
	typed := json.NewDecoder(bytes.NewReader(data))
	typed.DisallowUnknownFields()
	return typed.Decode(target)
}
