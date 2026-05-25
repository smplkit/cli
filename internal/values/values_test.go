package values

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseTyped(t *testing.T) {
	cases := []struct {
		raw  string
		t    ItemType
		want interface{}
	}{
		{"hello", ItemTypeString, "hello"},
		{"42", ItemTypeNumber, 42.0},
		{"3.14", ItemTypeNumber, 3.14},
		{"true", ItemTypeBoolean, true},
		{"false", ItemTypeBoolean, false},
		{"YES", ItemTypeBoolean, true},
		{`{"a":1}`, ItemTypeJSON, map[string]interface{}{"a": 1.0}},
	}
	for _, c := range cases {
		got, err := ParseTyped(c.raw, c.t)
		if err != nil {
			t.Errorf("ParseTyped(%q,%s) error: %v", c.raw, c.t, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseTyped(%q,%s) = %#v want %#v", c.raw, c.t, got, c.want)
		}
	}
}

func TestParseTyped_Errors(t *testing.T) {
	if _, err := ParseTyped("notnum", ItemTypeNumber); err == nil {
		t.Error("expected error for non-number")
	}
	if _, err := ParseTyped("maybe", ItemTypeBoolean); err == nil {
		t.Error("expected error for non-bool")
	}
	if _, err := ParseTyped("not json", ItemTypeJSON); err == nil {
		t.Error("expected error for invalid JSON")
	}
	if _, err := ParseTyped("v", "weird"); err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestParseFlagDefault(t *testing.T) {
	cases := []struct {
		raw, ft string
		want    interface{}
	}{
		{"true", "BOOLEAN", true},
		{"hello", "STRING", "hello"},
		{"7", "NUMERIC", 7.0},
		{`["a","b"]`, "JSON", []interface{}{"a", "b"}},
	}
	for _, c := range cases {
		got, err := ParseFlagDefault(c.raw, c.ft)
		if err != nil {
			t.Errorf("ParseFlagDefault(%q,%s) error: %v", c.raw, c.ft, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseFlagDefault(%q,%s) = %#v want %#v", c.raw, c.ft, got, c.want)
		}
	}
}

func TestSplitKeyValue(t *testing.T) {
	k, v, err := SplitKeyValue("foo=bar=baz")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if k != "foo" || v != "bar=baz" {
		t.Errorf("got %q=%q", k, v)
	}
	if _, _, err := SplitKeyValue("nokey"); err == nil {
		t.Error("expected error for missing =")
	}
	if _, _, err := SplitKeyValue("=v"); err == nil {
		t.Error("expected error for empty key")
	}
}

func TestAtFileOrLiteral(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.json")
	if err := os.WriteFile(path, []byte(`{"k":1}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := AtFileOrLiteral("@" + path)
	if err != nil {
		t.Fatalf("AtFileOrLiteral: %v", err)
	}
	if got != `{"k":1}` {
		t.Errorf("file: got %q", got)
	}
	if got, _ := AtFileOrLiteral("literal"); got != "literal" {
		t.Errorf("literal: got %q", got)
	}
}
