package mcpsetup

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/tailscale/hujson"
)

// jsonDoc is a JSON (or JSONC: comments and trailing commas) configuration file
// edited in place. Edits change only the member they target: every other byte,
// comments and formatting included, is kept, and new members follow the file's
// indentation.
type jsonDoc struct {
	root hujson.Value
	// unit is one level of the file's indentation.
	unit string
}

var errNotObject = errors.New("the top level is not a JSON object")

// parseJSON parses src; an empty file is an empty object.
func parseJSON(src []byte) (*jsonDoc, error) {
	if len(bytes.TrimSpace(src)) == 0 {
		src = []byte("{}\n")
	}
	root, err := hujson.Parse(src)
	if err != nil {
		return nil, err
	}
	obj, ok := root.Value.(*hujson.Object)
	if !ok {
		return nil, errNotObject
	}
	unit := "  "
	if indent, ok := memberIndent(obj); ok && indent != "" {
		unit = indent
	}
	return &jsonDoc{root: root, unit: unit}, nil
}

// data returns the document as generic values (objects are map[string]any).
func (d *jsonDoc) data() (map[string]any, error) {
	clone := d.root.Clone()
	clone.Standardize()
	var out map[string]any
	if err := sonic.Unmarshal(clone.Pack(), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func (d *jsonDoc) bytes() []byte {
	return d.root.Pack()
}

// set makes the member at path value, creating missing parent objects.
func (d *jsonDoc) set(path []string, value any) error {
	cur := &d.root
	indent := ""
	for i, key := range path {
		obj, ok := cur.Value.(*hujson.Object)
		if !ok {
			return fmt.Errorf("%s is not a JSON object", strings.Join(path[:i], "."))
		}
		// Members of an object written on one line are added on that line too.
		unit := d.unit
		inner, ok := memberIndent(obj)
		switch {
		case !ok && len(obj.Members) > 0:
			inner, unit = "", ""
		case !ok:
			inner = indent + d.unit
		}
		idx, err := findMember(obj, key)
		if err != nil {
			return err
		}
		if idx < 0 {
			for j := len(path) - 1; j > i; j-- {
				value = object{{path[j], value}}
			}
			parsed, err := renderValue(value, inner, unit)
			if err != nil {
				return err
			}
			appendMember(obj, key, parsed, inner, indent, unit == "")
			return nil
		}
		member := &obj.Members[idx]
		if i == len(path)-1 {
			parsed, err := renderValue(value, inner, unit)
			if err != nil {
				return err
			}
			member.Value.Value = parsed.Value
			return nil
		}
		cur, indent = &member.Value, inner
	}
	return nil
}

func renderValue(value any, indent, unit string) (hujson.Value, error) {
	text, err := render(value, indent, unit)
	if err != nil {
		return hujson.Value{}, err
	}
	return hujson.Parse(text)
}

// remove deletes the member at path and reports whether it existed.
func (d *jsonDoc) remove(path []string) (bool, error) {
	cur := &d.root
	for i, key := range path {
		obj, ok := cur.Value.(*hujson.Object)
		if !ok {
			return false, nil
		}
		idx, err := findMember(obj, key)
		if err != nil || idx < 0 {
			return false, err
		}
		if i == len(path)-1 {
			removeMember(obj, idx)
			return true, nil
		}
		cur = &obj.Members[idx].Value
	}
	return false, nil
}

// findMember returns the index of the member named key, or -1. A name that
// appears twice is ambiguous: harnesses disagree about which one wins.
func findMember(obj *hujson.Object, key string) (int, error) {
	found := -1
	for i := range obj.Members {
		lit, ok := obj.Members[i].Name.Value.(hujson.Literal)
		if !ok || lit.String() != key {
			continue
		}
		if found >= 0 {
			return -1, fmt.Errorf("%q is defined twice", key)
		}
		found = i
	}
	return found, nil
}

// memberIndent is the indentation of obj's members when they start on lines of
// their own.
func memberIndent(obj *hujson.Object) (string, bool) {
	for _, member := range obj.Members {
		before := member.Name.BeforeExtra
		if i := bytes.LastIndexByte(before, '\n'); i >= 0 && isBlank(before[i+1:]) {
			return string(before[i+1:]), true
		}
	}
	return "", false
}

// appendMember adds key: value as the last member of obj, on a line of its own at
// indent (on the same line in a compact object); closing is the indentation of
// obj's closing brace.
func appendMember(
	obj *hujson.Object,
	key string,
	value hujson.Value,
	indent, closing string,
	compact bool,
) {
	member := hujson.ObjectMember{
		Name:  hujson.Value{Value: hujson.String(key)},
		Value: hujson.Value{Value: value.Value},
	}
	if n := len(obj.Members); n > 0 && obj.Members[n-1].Value.AfterExtra != nil {
		// Keep the file's trailing comma.
		member.Value.AfterExtra = hujson.Extra{}
	}
	if compact {
		obj.Members = append(obj.Members, member)
		return
	}
	member.Value.BeforeExtra = hujson.Extra(" ")
	after := obj.AfterExtra
	var lead []byte
	// A comment on the line of the last member stays on that line.
	if i := bytes.IndexByte(after, '\n'); i >= 0 {
		if !isBlank(after[:i]) {
			lead, after = after[:i], after[i:]
		}
	} else if !isBlank(after) {
		lead, after = bytes.TrimRight(after, " \t\r"), nil
	}
	if !bytes.Contains(after, []byte{'\n'}) {
		after = []byte("\n" + closing)
	}
	member.Name.BeforeExtra = concat(lead, []byte("\n"+indent))
	obj.Members = append(obj.Members, member)
	obj.AfterExtra = after
}

// removeMember deletes obj.Members[idx] with the comments on its line; comments
// on lines above it stay.
func removeMember(obj *hujson.Object, idx int) {
	cur := obj.Members[idx]
	before := cur.Name.BeforeExtra
	var keep []byte
	if i := bytes.LastIndexByte(before, '\n'); i >= 0 {
		keep = before[:i]
	}
	last := idx == len(obj.Members)-1
	obj.Members = append(obj.Members[:idx], obj.Members[idx+1:]...)
	if !last {
		next := &obj.Members[idx]
		rest := next.Name.BeforeExtra
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			rest = rest[i:]
		}
		next.Name.BeforeExtra = concat(keep, rest)
		return
	}
	rest := obj.AfterExtra
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i:]
	} else if !isBlank(rest) {
		rest = nil
	}
	if n := len(obj.Members); n > 0 {
		prev := &obj.Members[n-1].Value
		if cur.Value.AfterExtra == nil {
			// No trailing comma before: the new last member gets none either.
			keep = concat(prev.AfterExtra, keep)
			prev.AfterExtra = nil
		} else if prev.AfterExtra == nil {
			prev.AfterExtra = hujson.Extra{}
		}
	}
	obj.AfterExtra = concat(keep, rest)
	if len(obj.Members) == 0 && isBlank(obj.AfterExtra) {
		obj.AfterExtra = nil
	}
}

func concat(a, b []byte) []byte {
	out := make([]byte, 0, len(a)+len(b))
	return append(append(out, a...), b...)
}

func isBlank(b []byte) bool {
	return len(bytes.TrimLeft(b, " \t\r\n")) == 0
}

// object is a JSON object whose members keep their order.
type object []field

type field struct {
	key   string
	value any
}

var jsonScalar = sonic.Config{EscapeHTML: false}.Froze()

// render formats value (object, []string, string or bool) as JSON whose nested
// lines start at indent plus unit per level; an empty unit renders it on one line.
func render(value any, indent, unit string) ([]byte, error) {
	var b bytes.Buffer
	err := renderTo(&b, value, indent, unit)
	return b.Bytes(), err
}

func renderTo(b *bytes.Buffer, value any, indent, unit string) error {
	newline, colon := "\n"+indent+unit, ": "
	if unit == "" {
		newline, colon = "", ":"
	}
	closing := strings.TrimSuffix(newline, unit)
	switch v := value.(type) {
	case object:
		if len(v) == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteString("{")
		for i, f := range v {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(newline)
			if err := renderTo(b, f.key, "", ""); err != nil {
				return err
			}
			b.WriteString(colon)
			if err := renderTo(b, f.value, indent+unit, unit); err != nil {
				return err
			}
		}
		b.WriteString(closing + "}")
	case []string:
		if len(v) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[")
		for i, s := range v {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(newline)
			if err := renderTo(b, s, "", ""); err != nil {
				return err
			}
		}
		b.WriteString(closing + "]")
	default:
		out, err := jsonScalar.Marshal(v)
		if err != nil {
			return err
		}
		b.Write(out)
	}
	return nil
}

// generic converts an object to the values sonic decodes JSON into, for
// comparison with parsed files.
func generic(value any) any {
	switch v := value.(type) {
	case object:
		out := make(map[string]any, len(v))
		for _, f := range v {
			out[f.key] = generic(f.value)
		}
		return out
	case []string:
		out := make([]any, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out
	default:
		return v
	}
}
