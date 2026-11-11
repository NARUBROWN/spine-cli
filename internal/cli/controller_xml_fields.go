package cli

import (
	"fmt"
	"go/types"
	"reflect"
	"slices"
	"strings"
)

type controllerXMLField struct {
	field     *types.Var
	tag       string
	index     []int
	path      []string
	namespace string
	mode      string
}

// checkControllerXMLBody는 타입 정보로 코어와 같은 XML 경로 충돌을 검사합니다.
// 사용자 JSON, XML과 텍스트 디코더는 실행하지 않습니다.
func checkControllerXMLBody(root types.Type) error {
	seen := map[types.Type]bool{}
	var walk func(types.Type) error
	walk = func(t types.Type) error {
		t = types.Unalias(t)
		if seen[t] {
			return nil
		}
		seen[t] = true
		if _, pointer := t.Underlying().(*types.Pointer); !pointer && controllerXMLDecoder(t) {
			return nil
		}
		switch value := t.Underlying().(type) {
		case *types.Pointer:
			return walk(value.Elem())
		case *types.Slice:
			return walk(value.Elem())
		case *types.Struct:
			fields, err := controllerXMLFields(t, map[types.Type]bool{})
			if err != nil {
				return err
			}
			for _, field := range fields {
				if field.mode == "element" || field.mode == "element,any" {
					if err := walk(field.field.Type()); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	return walk(root)
}

func controllerXMLDecoder(t types.Type) bool {
	if controllerTextType(t) {
		return true
	}
	method, _, _ := types.LookupFieldOrMethod(types.NewPointer(t), false, nil, "UnmarshalXML")
	if method == nil {
		return false
	}
	signature, ok := method.Type().(*types.Signature)
	if !ok || signature.Variadic() || signature.Params().Len() != 2 || signature.Results().Len() != 1 ||
		!types.Identical(signature.Results().At(0).Type(), types.Universe.Lookup("error").Type()) {
		return false
	}
	decoder, ok := types.Unalias(signature.Params().At(0).Type()).(*types.Pointer)
	return ok && controllerXMLNamed(decoder.Elem(), "Decoder") && controllerXMLNamed(signature.Params().At(1).Type(), "StartElement")
}

func controllerXMLNamed(t types.Type, name string) bool {
	named, ok := types.Unalias(t).(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "encoding/xml" && named.Obj().Name() == name
}

func controllerXMLFields(t types.Type, visiting map[types.Type]bool) ([]controllerXMLField, error) {
	t = types.Unalias(t)
	if visiting[t] || controllerXMLNamed(t, "Name") {
		return nil, nil
	}
	visiting[t] = true
	defer delete(visiting, t)
	structure := t.Underlying().(*types.Struct)
	var fields []controllerXMLField
	for i := 0; i < structure.NumFields(); i++ {
		field := structure.Field(i)
		tag := reflect.StructTag(structure.Tag(i)).Get("xml")
		if !field.Exported() && !field.Embedded() || tag == "-" || field.Name() == "XMLName" {
			continue
		}
		typ := types.Unalias(field.Type())
		if pointer, ok := typ.Underlying().(*types.Pointer); ok {
			typ = types.Unalias(pointer.Elem())
		}
		candidates := []controllerXMLField{controllerXMLFieldInfo(field, tag, i)}
		if _, embedded := typ.Underlying().(*types.Struct); field.Embedded() && embedded {
			inner, err := controllerXMLFields(typ, visiting)
			if err != nil {
				return nil, err
			}
			candidates = inner
			for j := range candidates {
				candidates[j].index = append([]int{i}, candidates[j].index...)
			}
		}
		for _, candidate := range candidates {
			var conflicts []int
			for j, existing := range fields {
				if controllerXMLFieldsConflict(existing, candidate) {
					conflicts = append(conflicts, j)
				}
			}
			if slices.ContainsFunc(conflicts, func(j int) bool { return len(fields[j].index) < len(candidate.index) }) {
				continue
			}
			for _, j := range conflicts {
				if len(fields[j].index) == len(candidate.index) {
					return nil, fmt.Errorf("body DTO %s has conflicting XML fields: %s (%q) and %s (%q)", t, fields[j].field.Name(), fields[j].tag, candidate.field.Name(), candidate.tag)
				}
			}
			for _, j := range slices.Backward(conflicts) {
				fields = slices.Delete(fields, j, j+1)
			}
			fields = append(fields, candidate)
		}
	}
	return fields, nil
}

func controllerXMLFieldInfo(field *types.Var, tag string, index int) controllerXMLField {
	info := controllerXMLField{field: field, tag: tag, index: []int{index}}
	if namespace, rest, ok := strings.Cut(tag, " "); ok {
		info.namespace, tag = namespace, rest
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	var modes []string
	for _, mode := range []string{"attr", "cdata", "chardata", "innerxml", "comment", "any"} {
		if slices.Contains(parts[1:], mode) {
			modes = append(modes, mode)
		}
	}
	if len(modes) == 0 || len(modes) == 1 && modes[0] == "any" {
		modes = append([]string{"element"}, modes...)
	}
	info.mode = strings.Join(modes, ",")
	if name == "" {
		name = field.Name()
		typ := types.Unalias(field.Type())
		seen := map[types.Type]bool{}
		for !seen[typ] {
			seen[typ] = true
			pointer, ok := typ.Underlying().(*types.Pointer)
			if !ok {
				break
			}
			typ = types.Unalias(pointer.Elem())
		}
		if structure, ok := typ.Underlying().(*types.Struct); ok {
			for i := 0; i < structure.NumFields(); i++ {
				if structure.Field(i).Name() != "XMLName" {
					continue
				}
				xmlName, _, _ := strings.Cut(reflect.StructTag(structure.Tag(i)).Get("xml"), ",")
				namespace, local, namespaced := strings.Cut(xmlName, " ")
				if local != "" && namespaced {
					info.namespace, name = namespace, local
				} else if xmlName != "" && xmlName != "-" && !namespaced {
					name = xmlName
				}
				break
			}
		}
		info.path = []string{name}
		return info
	}
	info.path = strings.Split(name, ">")
	if info.path[0] == "" {
		info.path[0] = field.Name()
	}
	return info
}

func controllerXMLFieldsConflict(a, b controllerXMLField) bool {
	if a.mode != b.mode || a.namespace != "" && b.namespace != "" && a.namespace != b.namespace {
		return false
	}
	length := min(len(a.path), len(b.path))
	return slices.Equal(a.path[:length], b.path[:length]) && (len(a.path) != len(b.path) || a.namespace == b.namespace)
}
