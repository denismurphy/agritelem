// Package contract builds the Agritelem OpenAPI document from the vendored
// ICAR ADE bundle and the Agritelem extension document.
//
// The extension may only add to ICAR: maps merge recursively, arrays append
// elements not already present, and a differing scalar is an error. This keeps
// every ICAR constraint intact in the published contract.
package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	directiveKey   = "x-agritelem-contract"
	extPathPrefix  = "/agritelem/"
	extNamePrefix  = "agritelem"
	componentsRef  = "#/components/"
	securitySchema = "securitySchemes"
)

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

type directive struct {
	ICAROperations map[string][]string `json:"icarOperations"`
}

// Build returns the bundled OpenAPI document as indented JSON. icar is the
// vendored ICAR bundle (JSON); ext is the Agritelem extension (YAML).
func Build(icar, ext []byte) ([]byte, error) {
	base, err := decodeJSON(icar)
	if err != nil {
		return nil, fmt.Errorf("decode ICAR bundle: %w", err)
	}
	extDoc, err := decodeYAML(ext)
	if err != nil {
		return nil, fmt.Errorf("decode extension: %w", err)
	}

	var dir directive
	if err := remarshal(extDoc[directiveKey], &dir); err != nil {
		return nil, fmt.Errorf("decode %s: %w", directiveKey, err)
	}
	if len(dir.ICAROperations) == 0 {
		return nil, fmt.Errorf("%s.icarOperations is empty", directiveKey)
	}
	if extDoc["openapi"] != base["openapi"] {
		return nil, fmt.Errorf("extension targets openapi %v but the ICAR bundle is %v", extDoc["openapi"], base["openapi"])
	}

	out := map[string]any{"openapi": base["openapi"]}
	for _, key := range []string{"info", "servers", "security"} {
		if v, ok := extDoc[key]; ok {
			out[key] = v
		} else if v, ok := base[key]; ok {
			out[key] = v
		}
	}

	icarPaths, _ := base["paths"].(map[string]any)
	paths, err := selectOperations(icarPaths, dir.ICAROperations)
	if err != nil {
		return nil, err
	}
	extPaths, _ := extDoc["paths"].(map[string]any)
	for _, p := range sortedKeys(extPaths) {
		if _, selected := paths[p]; !selected {
			if _, isICAR := icarPaths[p]; isICAR {
				return nil, fmt.Errorf("extension extends ICAR path %s, which is not listed in %s.icarOperations", p, directiveKey)
			}
			if !strings.HasPrefix(p, extPathPrefix) {
				return nil, fmt.Errorf("new path %s must start with %s", p, extPathPrefix)
			}
		}
		merged, err := mergeAdditive(paths[p], extPaths[p], "/paths/"+escapePointer(p))
		if err != nil {
			return nil, err
		}
		paths[p] = merged
	}
	out["paths"] = paths

	components, _ := base["components"].(map[string]any)
	if components == nil {
		components = map[string]any{}
	}
	extComponents, _ := extDoc["components"].(map[string]any)
	for _, section := range sortedKeys(extComponents) {
		entries, _ := extComponents[section].(map[string]any)
		existing, _ := components[section].(map[string]any)
		if existing == nil {
			existing = map[string]any{}
			components[section] = existing
		}
		for _, name := range sortedKeys(entries) {
			if _, ok := existing[name]; !ok && section != securitySchema && !strings.HasPrefix(name, extNamePrefix) {
				return nil, fmt.Errorf("new component %s/%s must be named with prefix %q", section, name, extNamePrefix)
			}
			merged, err := mergeAdditive(existing[name], entries[name], "/components/"+section+"/"+escapePointer(name))
			if err != nil {
				return nil, err
			}
			existing[name] = merged
		}
	}

	schemas, _ := components["schemas"].(map[string]any)
	inlineNullableScalars(paths, schemas)
	inlineNullableScalars(components, schemas)

	pruned, err := referencedComponents(out, components)
	if err != nil {
		return nil, err
	}
	out["components"] = pruned

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("encode contract: %w", err)
	}
	return buf.Bytes(), nil
}

// selectOperations copies the listed operations, plus path-level fields such
// as shared parameters, out of the ICAR paths object.
func selectOperations(icarPaths map[string]any, ops map[string][]string) (map[string]any, error) {
	paths := make(map[string]any, len(ops))
	for _, p := range sortedKeys(ops) {
		item, ok := icarPaths[p].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("ICAR path %s not found in bundle", p)
		}
		selected := map[string]any{}
		for key, v := range item {
			if !slices.Contains(httpMethods, key) {
				selected[key] = v
			}
		}
		for _, method := range ops[p] {
			op, ok := item[method]
			if !ok {
				return nil, fmt.Errorf("ICAR operation %s %s not found in bundle", method, p)
			}
			selected[method] = op
		}
		paths[p] = selected
	}
	return paths, nil
}

// mergeAdditive merges src into dst. ptr is the JSON pointer of dst, used in
// error messages.
func mergeAdditive(dst, src any, ptr string) (any, error) {
	if dst == nil {
		return src, nil
	}
	switch d := dst.(type) {
	case map[string]any:
		s, ok := src.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: extension replaces an object with %T", ptr, src)
		}
		for _, k := range sortedKeys(s) {
			merged, err := mergeAdditive(d[k], s[k], ptr+"/"+escapePointer(k))
			if err != nil {
				return nil, err
			}
			d[k] = merged
		}
		return d, nil
	case []any:
		s, ok := src.([]any)
		if !ok {
			return nil, fmt.Errorf("%s: extension replaces an array with %T", ptr, src)
		}
		for _, item := range s {
			if !slices.ContainsFunc(d, func(existing any) bool { return reflect.DeepEqual(existing, item) }) {
				d = append(d, item)
			}
		}
		return d, nil
	default:
		if !reflect.DeepEqual(dst, src) {
			return nil, fmt.Errorf("%s: extension changes ICAR value %v to %v", ptr, dst, src)
		}
		return dst, nil
	}
}

// inlineNullableScalars rewrites `anyOf: [{type: null}, {$ref: S}]`, where S
// is a plain scalar schema, into `type: [T, "null"]`. The two forms validate
// the same instances, but swift-openapi-generator drops the anyOf form.
func inlineNullableScalars(node any, schemas map[string]any) {
	switch n := node.(type) {
	case map[string]any:
		if typ, format, ok := nullableScalar(n["anyOf"], schemas); ok {
			delete(n, "anyOf")
			n["type"] = []any{typ, "null"}
			if format != nil {
				n["format"] = format
			}
		}
		for _, v := range n {
			inlineNullableScalars(v, schemas)
		}
	case []any:
		for _, v := range n {
			inlineNullableScalars(v, schemas)
		}
	}
}

func nullableScalar(anyOf any, schemas map[string]any) (typ string, format any, ok bool) {
	branches, isList := anyOf.([]any)
	if !isList || len(branches) != 2 {
		return "", nil, false
	}
	null := map[string]any{"type": "null"}
	var other map[string]any
	switch {
	case reflect.DeepEqual(branches[0], null):
		other, _ = branches[1].(map[string]any)
	case reflect.DeepEqual(branches[1], null):
		other, _ = branches[0].(map[string]any)
	default:
		return "", nil, false
	}
	ref, _ := other["$ref"].(string)
	name, found := strings.CutPrefix(ref, componentsRef+"schemas/")
	if !found || len(other) != 1 {
		return "", nil, false
	}
	target, _ := schemas[name].(map[string]any)
	for key := range target {
		if key != "type" && key != "format" && key != "description" {
			return "", nil, false
		}
	}
	typ, _ = target["type"].(string)
	if !slices.Contains([]string{"string", "number", "integer", "boolean"}, typ) {
		return "", nil, false
	}
	return typ, target["format"], true
}

// referencedComponents returns the components reachable from paths and
// top-level security, failing on any reference that does not resolve.
func referencedComponents(doc map[string]any, components map[string]any) (map[string]any, error) {
	kept := map[string]any{}
	keep := func(section, name string) (any, bool, error) {
		entries, _ := components[section].(map[string]any)
		v, ok := entries[name]
		if !ok {
			return nil, false, fmt.Errorf("dangling reference %s%s/%s", componentsRef, section, name)
		}
		dst, _ := kept[section].(map[string]any)
		if dst == nil {
			dst = map[string]any{}
			kept[section] = dst
		}
		if _, seen := dst[name]; seen {
			return nil, false, nil
		}
		dst[name] = v
		return v, true, nil
	}

	var queue []any
	queue = append(queue, doc["paths"])
	security, _ := doc["security"].([]any)
	for _, req := range security {
		reqMap, ok := req.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("security requirement %v is not an object", req)
		}
		for _, name := range sortedKeys(reqMap) {
			v, fresh, err := keep(securitySchema, name)
			if err != nil {
				return nil, err
			}
			if fresh {
				queue = append(queue, v)
			}
		}
	}

	var walkErr error
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if ref, ok := n["$ref"].(string); ok {
				rest, local := strings.CutPrefix(ref, componentsRef)
				section, name, split := strings.Cut(rest, "/")
				if !local || !split {
					walkErr = errors.Join(walkErr, fmt.Errorf("unsupported reference %s", ref))
				} else if v, fresh, err := keep(section, unescapePointer(name)); err != nil {
					walkErr = errors.Join(walkErr, err)
				} else if fresh {
					queue = append(queue, v)
				}
			}
			for _, v := range n {
				walk(v)
			}
		case []any:
			for _, v := range n {
				walk(v)
			}
		}
	}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		walk(next)
	}
	if walkErr != nil {
		return nil, walkErr
	}
	return kept, nil
}

func decodeJSON(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// decodeYAML decodes YAML and normalises it through JSON so that values have
// the same Go types as decodeJSON output and compare equal with DeepEqual.
func decodeYAML(data []byte) (map[string]any, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	asJSON, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	return decodeJSON(asJSON)
}

func remarshal(in, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func escapePointer(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
}

func unescapePointer(s string) string {
	return strings.NewReplacer("~1", "/", "~0", "~").Replace(s)
}
