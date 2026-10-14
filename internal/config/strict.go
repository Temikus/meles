package config

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

var nodeType = reflect.TypeFor[yaml.Node]()

// DecodeStrict decodes n into v and rejects mapping keys that v does not declare.
// yaml.v3 only enforces known fields on a Decoder; Node.Decode and custom
// unmarshalers silently drop unknown keys, so this walks the node itself.
func DecodeStrict(n *yaml.Node, v any) error {
	if err := checkKeys(n, reflect.TypeOf(v)); err != nil {
		return err
	}
	if err := n.Decode(v); err != nil {
		return posFromYAML(err)
	}
	return nil
}

func checkKeys(n *yaml.Node, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if t == nodeType {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		if n.Kind != yaml.MappingNode {
			return nil // type mismatch is reported by Decode
		}
		fields := yamlFields(t)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			f, ok := fields[k.Value]
			if !ok {
				return &posError{line: k.Line, msg: fmt.Sprintf("unknown field %q", k.Value)}
			}
			if err := checkKeys(n.Content[i+1], f); err != nil {
				return err
			}
		}
	case reflect.Map:
		if n.Kind == yaml.MappingNode {
			for i := 1; i < len(n.Content); i += 2 {
				if err := checkKeys(n.Content[i], t.Elem()); err != nil {
					return err
				}
			}
		}
	case reflect.Slice:
		if n.Kind == yaml.SequenceNode {
			for _, c := range n.Content {
				if err := checkKeys(c, t.Elem()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func yamlFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		out[name] = f.Type
	}
	return out
}

type posError struct {
	line int
	msg  string
}

func (e *posError) Error() string { return fmt.Sprintf("line %d: %s", e.line, e.msg) }

var yamlLineRe = regexp.MustCompile(`line (\d+): (.*)`)

// posFromYAML lifts the first "line N: msg" out of a yaml.TypeError.
func posFromYAML(err error) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) || len(te.Errors) == 0 {
		return err
	}
	m := yamlLineRe.FindStringSubmatch(te.Errors[0])
	if m == nil {
		return err
	}
	line, _ := strconv.Atoi(m[1])
	return &posError{line: line, msg: m[2]}
}
