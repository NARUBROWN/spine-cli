package cli

import (
	"fmt"
	"go/types"
	"maps"
	"reflect"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/mod/semver"
)

func previewStableHTTPContracts(coreVersion []string) bool {
	return len(coreVersion) > 0 && semver.IsValid(coreVersion[0]) && semver.Compare(coreVersion[0], "v0.7.5-preview.1") >= 0
}

func checkControllerReturns(signature *types.Signature, coreVersion ...string) error {
	firstResponse := -1
	for i := 0; i < signature.Results().Len(); i++ {
		t := types.Unalias(signature.Results().At(i).Type())
		if pointer, ok := t.(*types.Pointer); ok {
			t = types.Unalias(pointer.Elem())
		}
		response, ok := t.(*types.Named)
		if previewStableHTTPContracts(coreVersion) && ok && response.Obj().Pkg() != nil && response.Obj().Pkg().Path() == spineImportPath+"/pkg/httpx" &&
			(response.Obj().Name() == "Response" || response.Obj().Name() == "Binary" || response.Obj().Name() == "Redirect") {
			if firstResponse >= 0 {
				return fmt.Errorf("HTTP response contract: handler declares responses at return indexes %d and %d; return at most one HTTP response, optionally accompanied by an error", firstResponse, i)
			}
			firstResponse = i
		}
		if !ok || response.Obj().Pkg() == nil || response.Obj().Pkg().Path() != spineImportPath+"/pkg/httpx" ||
			response.Obj().Name() != "Response" || response.TypeArgs().Len() != 1 {
			continue
		}
		body := response.TypeArgs().At(0)
		if unsupported := unsupportedControllerJSON(body, false, map[jsonTypeUse]bool{}); unsupported != nil {
			return fmt.Errorf("httpx.Response body %s contains unsupported JSON type %s", body, unsupported)
		}
	}
	return nil
}

type jsonTypeUse struct {
	typeOf      types.Type
	addressable bool
}

func unsupportedControllerJSON(t types.Type, addressable bool, seen map[jsonTypeUse]bool) types.Type {
	t = types.Unalias(t)
	use := jsonTypeUse{t, addressable}
	if seen[use] {
		return nil
	}
	seen[use] = true
	if controllerMarshaler(t, "MarshalJSON") || controllerMarshaler(t, "MarshalText") ||
		addressable && (controllerMarshaler(types.NewPointer(t), "MarshalJSON") || controllerMarshaler(types.NewPointer(t), "MarshalText")) {
		return nil
	}
	switch value := t.Underlying().(type) {
	case *types.Chan, *types.Signature:
		return t
	case *types.Basic:
		if value.Info()&types.IsComplex != 0 || value.Kind() == types.UnsafePointer {
			return t
		}
	case *types.Pointer:
		return unsupportedControllerJSON(value.Elem(), true, seen)
	case *types.Slice:
		return unsupportedControllerJSON(value.Elem(), true, seen)
	case *types.Array:
		return unsupportedControllerJSON(value.Elem(), addressable, seen)
	case *types.Map:
		key, basic := value.Key().Underlying().(*types.Basic)
		if (!basic || key.Info()&(types.IsString|types.IsInteger) == 0) && !controllerMarshaler(value.Key(), "MarshalText") {
			return t
		}
		return unsupportedControllerJSON(value.Elem(), false, seen)
	case *types.Struct:
		fields := controllerJSONFields(value)
		for _, name := range slices.Sorted(maps.Keys(fields)) {
			field := fields[name]
			if unsupported := unsupportedControllerJSON(field.typeOf, addressable || field.indirect, seen); unsupported != nil {
				return unsupported
			}
		}
	}
	return nil
}

type controllerJSONField struct {
	typeOf                     types.Type
	depth                      int
	tagged, indirect, conflict bool
}

// controllerJSONFields는 JSON 인코더와 같은 필드 우선순위를 적용합니다.
func controllerJSONFields(root *types.Struct) map[string]controllerJSONField {
	fields := map[string]controllerJSONField{}
	var collect func(*types.Struct, []*types.Struct, bool)
	collect = func(structure *types.Struct, parents []*types.Struct, indirect bool) {
		for i := 0; i < structure.NumFields(); i++ {
			field := structure.Field(i)
			underlying := field.Type().Underlying()
			pointer, isPointer := underlying.(*types.Pointer)
			if isPointer {
				underlying = pointer.Elem().Underlying()
			}
			nested, isStruct := underlying.(*types.Struct)
			if !field.Exported() && (!field.Embedded() || !isStruct) {
				continue
			}
			tag := reflect.StructTag(structure.Tag(i)).Get("json")
			if tag == "-" {
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			if strings.ContainsFunc(name, func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", r)
			}) {
				name = ""
			}
			if field.Embedded() && name == "" && isStruct {
				if !slices.Contains(parents, nested) {
					collect(nested, append(slices.Clone(parents), nested), indirect || isPointer)
				}
				continue
			}
			candidate := controllerJSONField{typeOf: field.Type(), depth: len(parents), tagged: name != "", indirect: indirect}
			if name == "" {
				name = field.Name()
			}
			current, exists := fields[name]
			switch {
			case !exists || candidate.depth < current.depth || candidate.depth == current.depth && candidate.tagged && !current.tagged:
				fields[name] = candidate
			case candidate.depth == current.depth && candidate.tagged == current.tagged:
				current.conflict = true
				fields[name] = current
			}
		}
	}
	collect(root, []*types.Struct{root}, false)
	for name, field := range fields {
		if field.conflict {
			delete(fields, name)
		}
	}
	return fields
}

func controllerMarshaler(t types.Type, name string) bool {
	method, _, _ := types.LookupFieldOrMethod(t, false, nil, name)
	if method == nil {
		return false
	}
	signature, ok := method.Type().(*types.Signature)
	return ok && signature.Params().Len() == 0 && signature.Results().Len() == 2 &&
		types.Identical(signature.Results().At(0).Type(), types.NewSlice(types.Typ[types.Byte])) &&
		types.Identical(signature.Results().At(1).Type(), types.Universe.Lookup("error").Type())
}
