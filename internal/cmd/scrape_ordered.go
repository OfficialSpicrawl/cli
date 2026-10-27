package cmd

import (
	"bytes"
	"encoding/json"
)

// ordObj is a JSON object that keeps its members in the order they arrived.
// Decoding into map[string]any loses that: encoding/json writes a map's keys
// sorted, so a human reading `--extract` output saw the fields shuffled.
type ordObj []ordMember

type ordMember struct {
	key string
	val any
}

// MarshalJSON writes the members in order, without HTML escaping (a person
// reads <, > and & as they are). Nested ordObj values come back through here.
func (o ordObj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		for j, v := range []any{m.key, m.val} {
			if j == 1 {
				b.WriteByte(':')
			}
			if err := enc.Encode(v); err != nil {
				return nil, err
			}
			b.Truncate(b.Len() - 1) // Encode ends with a newline
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// decodeOrdered reads the next JSON value from dec: objects as ordObj, arrays
// as []any, scalars as Token returns them (json.Number if dec.UseNumber).
func decodeOrdered(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	if d == '{' {
		obj := ordObj{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeOrdered(dec)
			if err != nil {
				return nil, err
			}
			obj = append(obj, ordMember{k.(string), v})
		}
		_, err = dec.Token() // }
		return obj, err
	}
	arr := []any{}
	for dec.More() {
		v, err := decodeOrdered(dec)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
	}
	_, err = dec.Token() // ]
	return arr, err
}
