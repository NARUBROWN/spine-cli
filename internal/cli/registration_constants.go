package cli

import (
	"bytes"
	"go/constant"
	"go/format"
	"go/types"
	"os"
	"path/filepath"

	"golang.org/x/tools/go/packages"
)

// 등록 이름에는 가져온 패키지의 문자열 상수만 사용합니다.
// 타입 정보는 Go 도구로 읽고 패키지 초기화나 함수 호출은 실행하지 않습니다.
func (i *contractInspector) registrationConstant(importPath, name string) (string, bool) {
	if i.registrationPackages == nil {
		i.registrationPackages = make(map[string]*types.Package)
	}
	pkg, loaded := i.registrationPackages[importPath]
	if !loaded {
		pkg = i.loadRegistrationPackage(importPath)
		i.registrationPackages[importPath] = pkg
	}
	if pkg == nil {
		return "", false
	}
	object, ok := pkg.Scope().Lookup(name).(*types.Const)
	if !ok || object.Val().Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(object.Val()), true
}

func (i *contractInspector) loadRegistrationPackage(importPath string) *types.Package {
	overlay := make(map[string][]byte)
	for _, source := range i.sources {
		var content bytes.Buffer
		if err := format.Node(&content, source.Fset, source.File); err != nil {
			return nil
		}
		path, err := filepath.Abs(source.Path)
		if err != nil {
			return nil
		}
		overlay[path] = content.Bytes()
	}
	config := &packages.Config{
		Mode:       packages.NeedName | packages.NeedTypes,
		Overlay:    overlay,
		Env:        os.Environ(),
		BuildFlags: []string{"-mod=readonly"},
	}
	loaded, err := packages.Load(config, importPath)
	if err != nil || len(loaded) != 1 || len(loaded[0].Errors) != 0 {
		return nil
	}
	return loaded[0].Types
}
