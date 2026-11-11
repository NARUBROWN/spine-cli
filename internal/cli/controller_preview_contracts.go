package cli

import (
	"bytes"
	"fmt"
	"go/format"
	"go/types"
	"os"
	"path/filepath"

	"golang.org/x/mod/semver"
	"golang.org/x/tools/go/packages"
)

func previewStableHTTPContracts(coreVersion []string) bool {
	return len(coreVersion) > 0 && semver.IsValid(coreVersion[0]) && semver.Compare(coreVersion[0], "v0.7.5-preview.1") >= 0
}

// checkPreviewStableControllerMethod는 새 계약을 적용하는 코어에서만 생성할
// 콜백의 실제 메서드 시그니처를 검사합니다. 사용자 초기화와 생성자는 실행하지 않습니다.
func checkPreviewStableControllerMethod(directory, receiver, method string, sources layoutSources, coreVersion string) error {
	if !previewStableHTTPContracts([]string{coreVersion}) {
		return nil
	}
	overlay := map[string][]byte{}
	for _, source := range sources.sortedFiles() {
		var contents bytes.Buffer
		if err := format.Node(&contents, source.Fset, source.File); err != nil {
			return err
		}
		path, err := filepath.Abs(source.Path)
		if err != nil {
			return err
		}
		overlay[path] = contents.Bytes()
	}
	pattern := "."
	if directory != "" && directory != "." {
		pattern = "./" + filepath.ToSlash(directory)
	}
	config := &packages.Config{Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps | packages.NeedTypes,
		Overlay: overlay, Env: os.Environ(), BuildFlags: []string{"-mod=readonly"}}
	loaded, err := packages.Load(config, pattern)
	if err != nil {
		return fmt.Errorf("controller method typecheck failed: %w", err)
	}
	for _, pkg := range loaded {
		if len(pkg.Errors) > 0 {
			return fmt.Errorf("controller method typecheck failed: %s", pkg.Errors[0].Msg)
		}
		if pkg.Types == nil {
			continue
		}
		object := pkg.Types.Scope().Lookup(receiver)
		if object == nil {
			continue
		}
		member, _, _ := types.LookupFieldOrMethod(types.NewPointer(object.Type()), false, nil, method)
		if member == nil {
			continue
		}
		signature, ok := member.Type().(*types.Signature)
		if !ok {
			continue
		}
		if err := checkControllerInputs(signature, coreVersion); err != nil {
			return fmt.Errorf("HTTP_INPUT_CONTRACT_INVALID: %s.%s: %w", receiver, method, err)
		}
		if err := checkControllerResponseCount(signature, coreVersion); err != nil {
			return fmt.Errorf("HTTP_RETURN_UNSUPPORTED: %s.%s: %w", receiver, method, err)
		}
		return nil
	}
	return fmt.Errorf("controller method %s.%s could not be resolved", receiver, method)
}

func checkControllerResponseCount(signature *types.Signature, coreVersion ...string) error {
	if !previewStableHTTPContracts(coreVersion) {
		return nil
	}
	first := -1
	for i := 0; i < signature.Results().Len(); i++ {
		t := types.Unalias(signature.Results().At(i).Type())
		if pointer, ok := t.(*types.Pointer); ok {
			t = types.Unalias(pointer.Elem())
		}
		response, ok := t.(*types.Named)
		if !ok || response.Obj().Pkg() == nil || response.Obj().Pkg().Path() != spineImportPath+"/pkg/httpx" ||
			(response.Obj().Name() != "Response" && response.Obj().Name() != "Binary" && response.Obj().Name() != "Redirect") {
			continue
		}
		if first >= 0 {
			return fmt.Errorf("HTTP response contract: handler declares responses at return indexes %d and %d; return at most one HTTP response, optionally accompanied by an error", first, i)
		}
		first = i
	}
	return nil
}
