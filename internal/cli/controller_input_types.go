package cli

import (
	"fmt"
	"go/types"
	"reflect"
)

// checkControllerInputs는 선택한 실제 Go 타입으로 새 입력의 구조와 필드 지원을 확인합니다.
func checkControllerInputs(signature *types.Signature) error {
	if signature.Variadic() {
		return fmt.Errorf("variadic controller callbacks are unsupported")
	}
	for index := 0; index < signature.Params().Len(); index++ {
		named, ok := types.Unalias(signature.Params().At(index).Type()).(*types.Named)
		if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != spineImportPath+"/pkg/input" {
			continue
		}
		source := named.Obj().Name()
		if source != "Path" && source != "Query" && source != "Body" {
			continue
		}
		pointer, ok := named.Underlying().(*types.Pointer)
		if !ok {
			return fmt.Errorf("input.%s must be a named pointer", source)
		}
		if source == "Path" && controllerBasicPathType(pointer.Elem()) {
			continue
		}
		structure, ok := pointer.Elem().Underlying().(*types.Struct)
		if !ok {
			return fmt.Errorf("input.%s requires a struct payload", source)
		}
		if source == "Body" {
			continue
		}
		if err := checkControllerFields(structure, source, map[*types.Struct]bool{}); err != nil {
			return err
		}
	}
	return nil
}

func checkControllerFields(structure *types.Struct, source string, visiting map[*types.Struct]bool) error {
	if visiting[structure] {
		return fmt.Errorf("recursive anonymous %s input", source)
	}
	visiting[structure] = true
	defer delete(visiting, structure)
	tag := "query"
	if source == "Path" {
		tag = "path"
	}
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if !field.Exported() {
			continue
		}
		name := reflect.StructTag(structure.Tag(index)).Get(tag)
		if name == "-" {
			continue
		}
		base := types.Unalias(field.Type())
		if field.Embedded() && name == "" {
			if _, pointer := base.Underlying().(*types.Pointer); pointer {
				return fmt.Errorf("anonymous pointer fields are not supported in %s DTOs", tag)
			}
			if embedded, ok := base.Underlying().(*types.Struct); ok {
				if err := checkControllerFields(embedded, source, visiting); err != nil {
					return err
				}
				continue
			}
		}
		if source == "Path" {
			if pointer, ok := base.Underlying().(*types.Pointer); ok {
				base = pointer.Elem()
			}
			if !controllerScalarType(base) {
				return fmt.Errorf("unsupported path field %s", field.Name())
			}
			continue
		}
		if controllerTextType(base) {
			continue
		}
		switch value := base.Underlying().(type) {
		case *types.Pointer:
			base = value.Elem()
		case *types.Slice:
			base = value.Elem()
		}
		if !controllerScalarType(base) {
			return fmt.Errorf("unsupported query field %s", field.Name())
		}
	}
	return nil
}

func controllerScalarType(t types.Type) bool {
	if controllerTextType(t) {
		return true
	}
	if named, ok := types.Unalias(t).(*types.Named); ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "time" && named.Obj().Name() == "Time" {
		return true
	}
	basic, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	info := basic.Info()
	return info&(types.IsBoolean|types.IsString|types.IsInteger|types.IsFloat) != 0
}

func controllerTextType(t types.Type) bool {
	method, _, _ := types.LookupFieldOrMethod(types.NewPointer(t), true, nil, "UnmarshalText")
	if method == nil {
		return false
	}
	signature, ok := method.Type().(*types.Signature)
	if !ok || signature.Params().Len() != 1 || signature.Results().Len() != 1 {
		return false
	}
	return types.Identical(signature.Params().At(0).Type(), types.NewSlice(types.Typ[types.Byte])) && types.Identical(signature.Results().At(0).Type(), types.Universe.Lookup("error").Type())
}

// controllerBasicPathType는 코어의 기본 스칼라만 허용하며 별도 정의한 타입을 제외합니다.
func controllerBasicPathType(t types.Type) bool {
	basic, ok := types.Unalias(t).(*types.Basic)
	if !ok {
		return false
	}
	switch basic.Kind() {
	case types.Bool, types.String, types.Int, types.Int8, types.Int16, types.Int32, types.Int64, types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr, types.Float32, types.Float64:
		return true
	}
	return false
}
