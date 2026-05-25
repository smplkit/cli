// Package values converts CLI flag strings into the typed Go values
// the SDK models expect for flag defaults and config items.
//
// `--value` / `--default` arrive as strings; the SDK wants `bool`,
// `float64`, `map[string]interface{}`, etc. This package handles the
// JSON-or-scalar parsing and the per-type coercions.
package values

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ItemType is the typed-input mode for `--item` and flag defaults.
type ItemType string

// Supported item types.
const (
	ItemTypeString  ItemType = "string"
	ItemTypeNumber  ItemType = "number"
	ItemTypeBoolean ItemType = "bool"
	ItemTypeJSON    ItemType = "json"
)

// ParseTyped converts a raw CLI string into a Go value of the requested
// item type. Bool accepts true/false/1/0/yes/no (case-insensitive),
// number accepts any Go float parse, string passes through, JSON is
// json.Unmarshalled.
func ParseTyped(raw string, t ItemType) (interface{}, error) {
	switch t {
	case ItemTypeString:
		return raw, nil
	case ItemTypeNumber:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q: %w", raw, err)
		}
		return f, nil
	case ItemTypeBoolean:
		switch strings.ToLower(raw) {
		case "true", "1", "yes":
			return true, nil
		case "false", "0", "no":
			return false, nil
		}
		return nil, fmt.Errorf("invalid bool %q (use true/false/1/0/yes/no)", raw)
	case ItemTypeJSON:
		return ParseJSON(raw)
	}
	return nil, fmt.Errorf("unknown item type %q", t)
}

// ParseJSON parses a raw JSON literal, accepting either a string of
// JSON or `@path` to a file containing JSON.
func ParseJSON(raw string) (interface{}, error) {
	body, err := AtFileOrLiteral(raw)
	if err != nil {
		return nil, err
	}
	var v interface{}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return v, nil
}

// ParseJSONObject is ParseJSON but requires the top-level value be an
// object (so callers can use it for flag rules / filter expressions).
func ParseJSONObject(raw string) (map[string]interface{}, error) {
	v, err := ParseJSON(raw)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("expected JSON object, got %T", v)
	}
	return m, nil
}

// ParseFlagDefault converts a default-value string for a typed flag
// (BOOLEAN, STRING, NUMERIC, JSON) into the matching Go value.
func ParseFlagDefault(raw, flagType string) (interface{}, error) {
	switch strings.ToUpper(flagType) {
	case "BOOLEAN", "BOOL":
		switch strings.ToLower(raw) {
		case "true", "1", "yes":
			return true, nil
		case "false", "0", "no":
			return false, nil
		}
		return nil, fmt.Errorf("invalid bool %q for BOOLEAN flag", raw)
	case "STRING":
		return raw, nil
	case "NUMERIC", "NUMBER":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q for NUMERIC flag: %w", raw, err)
		}
		return f, nil
	case "JSON":
		return ParseJSON(raw)
	}
	return nil, fmt.Errorf("unsupported flag type %q (expected bool|string|number|json)", flagType)
}

// SplitKeyValue splits a "k=v" string on the first "=", returning
// (key, value, true). Empty key is an error.
func SplitKeyValue(raw string) (string, string, error) {
	eq := strings.Index(raw, "=")
	if eq == -1 {
		return "", "", fmt.Errorf("expected key=value, got %q", raw)
	}
	key := strings.TrimSpace(raw[:eq])
	value := raw[eq+1:]
	if key == "" {
		return "", "", fmt.Errorf("empty key in %q", raw)
	}
	return key, value, nil
}
