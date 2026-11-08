package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type frontendOverlay struct {
	path    string
	cleanup func()
}

func frontendSettingsArgument(frontend []*frontendSettings) *frontendSettings {
	if len(frontend) == 0 {
		return nil
	}
	return frontend[0]
}

func applicationBuildArgs(config projectConfig, output string, overlay *frontendOverlay) []string {
	args := []string{"build"}
	if overlay != nil {
		args = append(args, "-overlay", overlay.path)
	}
	if output != "" {
		args = append(args, "-o", output)
	}
	return append(args, config.Entrypoint)
}

func createFrontendOverlay(projectRoot string, config projectConfig, settings *frontendSettings) (*frontendOverlay, error) {
	if settings == nil {
		return nil, nil
	}
	composition := filepath.Join(projectRoot, filepath.FromSlash(config.Composition))
	entries, err := os.ReadDir(composition)
	if err != nil {
		return nil, fmt.Errorf("could not inspect HTTP composition %q for frontend serving: %w", config.Composition, err)
	}
	fset := token.NewFileSet()
	var sourcePath string
	var sourceFile *ast.File
	var optionKind, optionAlias string
	for _, entry := range entries {
		if entry.IsDir() || !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(composition, entry.Name())
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return nil, fmt.Errorf("could not inspect HTTP composition file %q: %w", path, parseErr)
		}
		kind, alias, found := findFrontendPlatformOption(file)
		if !found {
			continue
		}
		sourcePath, sourceFile, optionKind, optionAlias = path, file, kind, alias
		break
	}
	if sourceFile == nil {
		return nil, fmt.Errorf("frontend serving requires an Echo or net/http Option or NewFactory call in the composition package")
	}
	if err := replaceFrontendPlatformCall(sourceFile, optionAlias, optionKind); err != nil {
		return nil, err
	}
	if !frontendPlatformAliasUsed(sourceFile, optionAlias) {
		removeFrontendPlatformImport(sourceFile, optionAlias, optionKind)
	}
	defaultStaticRelative, err := filepath.Rel(filepath.Join(projectRoot, "bin"), settings.output)
	if err != nil {
		return nil, fmt.Errorf("could not resolve the frontend output path for application builds: %w", err)
	}
	var modified strings.Builder
	if err := format.Node(&modified, fset, sourceFile); err != nil {
		return nil, fmt.Errorf("could not format frontend composition overlay: %w", err)
	}
	helper, err := frontendOverlaySource(optionKind, sourceFile.Name.Name, defaultStaticRelative, settings.apiPrefix)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "spine-frontend-overlay-")
	if err != nil {
		return nil, fmt.Errorf("could not create frontend build overlay: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	modifiedPath := filepath.Join(dir, "composition.go")
	helperPath := filepath.Join(dir, "frontend.go")
	virtualHelperPath := filepath.Join(composition, "spine_frontend_overlay_generated.go")
	if _, err := os.Lstat(virtualHelperPath); err == nil {
		cleanup()
		return nil, fmt.Errorf("composition package already contains reserved file %q", filepath.Base(virtualHelperPath))
	} else if !os.IsNotExist(err) {
		cleanup()
		return nil, fmt.Errorf("could not inspect reserved composition file %q: %w", virtualHelperPath, err)
	}
	if err := os.WriteFile(modifiedPath, []byte(modified.String()), 0o600); err != nil {
		cleanup()
		return nil, err
	}
	if err := os.WriteFile(helperPath, helper, 0o600); err != nil {
		cleanup()
		return nil, err
	}
	replacements := map[string]string{sourcePath: modifiedPath, virtualHelperPath: helperPath}
	overlayBytes, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: replacements})
	if err != nil {
		cleanup()
		return nil, err
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlayBytes, 0o600); err != nil {
		cleanup()
		return nil, err
	}
	return &frontendOverlay{path: overlayPath, cleanup: cleanup}, nil
}

func supportsFrontendServingPlatform(platform string) bool {
	return platform == "echo" || platform == "nethttp"
}

func projectFrontendServingAdapter(projectRoot string, config projectConfig) (string, string, bool) {
	if config.Platform != "" && !supportsFrontendServingPlatform(config.Platform) {
		return "", "", false
	}
	composition := filepath.Join(projectRoot, filepath.FromSlash(config.Composition))
	entries, err := os.ReadDir(composition)
	if err != nil {
		return "", "", false
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(composition, entry.Name()), nil, 0)
		if err != nil {
			return "", "", false
		}
		kind, _, found := findFrontendPlatformOption(file)
		if found && (config.Platform == "" || config.Platform == kind) {
			return kind, file.Name.Name, true
		}
	}
	return "", "", false
}

func projectHasFrontendServingAdapter(projectRoot string, config projectConfig) bool {
	_, _, ok := projectFrontendServingAdapter(projectRoot, config)
	return ok
}

func frontendOverlayFingerprint(projectRoot string, config projectConfig, settings *frontendSettings) string {
	const overlayCacheVersion = "frontend-overlay-v2"
	kind, packageName, ok := projectFrontendServingAdapter(projectRoot, config)
	if !ok || settings == nil {
		return overlayCacheVersion + "\n" + installedVersion() + "\nunavailable"
	}
	defaultStaticRelative, err := filepath.Rel(filepath.Join(projectRoot, "bin"), settings.output)
	if err != nil {
		return overlayCacheVersion + "\n" + installedVersion() + "\nerror:" + err.Error()
	}
	source, err := frontendOverlaySource(kind, packageName, defaultStaticRelative, settings.apiPrefix)
	if err != nil {
		return overlayCacheVersion + "\n" + installedVersion() + "\nerror:" + err.Error()
	}
	digest := sha256.Sum256(source)
	return fmt.Sprintf("%s\n%s\n%x", overlayCacheVersion, installedVersion(), digest)
}

func frontendPlatformAliasUsed(file *ast.File, alias string) bool {
	used := false
	ast.Inspect(file, func(node ast.Node) bool {
		if declaration, ok := node.(*ast.GenDecl); ok && declaration.Tok == token.IMPORT {
			return false
		}
		if selector, ok := node.(*ast.SelectorExpr); ok {
			if identifier, ok := selector.X.(*ast.Ident); ok && identifier.Name == alias {
				used = true
				return false
			}
		}
		return !used
	})
	return used
}

func removeFrontendPlatformImport(file *ast.File, alias, kind string) {
	importPath := echoPlatformPath
	if kind == "nethttp" {
		importPath = nethttpPlatformPath
	}
	decls := file.Decls[:0]
	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.IMPORT {
			decls = append(decls, declaration)
			continue
		}
		specs := group.Specs[:0]
		for _, spec := range group.Specs {
			importSpec, ok := spec.(*ast.ImportSpec)
			if !ok {
				specs = append(specs, spec)
				continue
			}
			path, err := strconv.Unquote(importSpec.Path.Value)
			specAlias := "platform"
			if importSpec.Name != nil {
				specAlias = importSpec.Name.Name
			}
			if err == nil && path == importPath && specAlias == alias {
				continue
			}
			specs = append(specs, spec)
		}
		if len(specs) > 0 {
			group.Specs = specs
			decls = append(decls, group)
		}
	}
	file.Decls = decls
	imports := file.Imports[:0]
	for _, importSpec := range file.Imports {
		path, err := strconv.Unquote(importSpec.Path.Value)
		specAlias := "platform"
		if importSpec.Name != nil {
			specAlias = importSpec.Name.Name
		}
		if err == nil && path == importPath && specAlias == alias {
			continue
		}
		imports = append(imports, importSpec)
	}
	file.Imports = imports
}

func findFrontendPlatformOption(file *ast.File) (string, string, bool) {
	aliases := map[string]string{}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		var kind string
		switch importPath {
		case echoPlatformPath:
			kind = "echo"
		case nethttpPlatformPath:
			kind = "nethttp"
		default:
			continue
		}
		alias := "platform"
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		aliases[alias] = kind
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "BuildApplication" || function.Body == nil {
			continue
		}
		var kind, alias string
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || kind != "" {
				return kind == ""
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if !ok || (selector.Sel.Name != "Option" && selector.Sel.Name != "NewFactory") {
				return true
			}
			if supported := aliases[identifier.Name]; supported != "" {
				kind, alias = supported, identifier.Name
				return false
			}
			return true
		})
		if kind != "" {
			return kind, alias, true
		}
	}
	return "", "", false
}

func replaceFrontendPlatformCall(file *ast.File, alias, kind string) error {
	replacement := "spineCLIFrontend" + strings.ToUpper(kind[:1]) + kind[1:]
	found := false
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "BuildApplication" || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || found {
				return !found
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Option" && selector.Sel.Name != "NewFactory" {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if !ok || identifier.Name != alias {
				return true
			}
			if selector.Sel.Name == "Option" {
				replacement += "Option"
			} else {
				replacement += "Factory"
			}
			call.Fun = ast.NewIdent(replacement)
			found = true
			return false
		})
	}
	if !found {
		return fmt.Errorf("could not locate the selected frontend platform option in BuildApplication")
	}
	return nil
}

func frontendOverlaySource(kind, packageName, defaultStaticRelative, apiPrefix string) ([]byte, error) {
	var aliases, constructors string
	switch kind {
	case "echo":
		aliases = `
	"errors"
	"github.com/NARUBROWN/spine"
	adapter "github.com/NARUBROWN/spine/contract/http"
	echoplatform "github.com/NARUBROWN/spine-echo-platform"
	echo "github.com/labstack/echo/v5"
`
		constructors = `
func spineCLIFrontendEchoOption(options ...echoplatform.EchoOption) spine.Option {
	options = append(options, echoplatform.WithEcho(func(server *echo.Echo) {
		server.Pre(frontendEchoStaticFallback(frontendStaticRoot(), frontendAPIPrefix()))
	}))
	return echoplatform.Option(options...)
}
func spineCLIFrontendEchoFactory(options ...echoplatform.EchoOption) adapter.Factory {
	options = append(options, echoplatform.WithEcho(func(server *echo.Echo) {
		server.Pre(frontendEchoStaticFallback(frontendStaticRoot(), frontendAPIPrefix()))
	}))
	return echoplatform.NewFactory(options...)
}
func frontendEchoStaticFallback(root, apiPrefix string) echo.MiddlewareFunc {
	if root == "" {
		return func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			request := c.Request()
			if request.Method != http.MethodGet && request.Method != http.MethodHead {
				return next(c)
			}
			if apiPrefix != "" && (request.URL.Path == apiPrefix || strings.HasPrefix(request.URL.Path, apiPrefix+"/")) {
				return next(c)
			}
			if request.Header.Get("Upgrade") != "" {
				return next(c)
			}
			wantsHTML := strings.Contains(request.Header.Get("Accept"), "text/html")
			if !wantsHTML && filepath.Ext(request.URL.Path) == "" {
				return next(c)
			}
			original := c.Response()
			capture := newFrontendResponseCapture(original)
			c.SetResponse(echo.NewResponse(capture, c.Echo().Logger))
			handlerErr := next(c)
			capturedResponse, unwrapErr := echo.UnwrapResponse(c.Response())
			c.SetResponse(original)
			if unwrapErr != nil {
				return unwrapErr
			}
			status := capturedResponse.Status
			if handlerErr != nil {
				if !frontendEchoErrorIsNotFound(handlerErr) || (status != 0 && status != http.StatusNotFound) {
					return handlerErr
				}
				status = http.StatusNotFound
			} else if status == 0 {
				status = http.StatusOK
			}
			if status != http.StatusNotFound {
				return nil
			}
			if name, ok := frontendRelativePath(request.URL.Path); ok && serveFrontendFile(original, request, root, name) {
				return nil
			}
			if wantsHTML && frontendAcceptsHTML(request) && filepath.Ext(request.URL.Path) == "" && serveFrontendFile(original, request, root, "index.html") {
				return nil
			}
			if handlerErr != nil {
				if capturedResponse.Committed {
					copyFrontendEchoResponse(original, capture, http.StatusNotFound, request.Method)
					return nil
				}
				return handlerErr
			}
			copyFrontendEchoResponse(original, capture, http.StatusNotFound, request.Method)
			return nil
		}
	}
}
func copyFrontendEchoResponse(w http.ResponseWriter, captured *frontendResponseCapture, status int, method string) {
	replaceFrontendHeaders(w, captured)
	w.WriteHeader(status)
	if method != http.MethodHead {
		_, _ = w.Write(captured.body.Bytes())
	}
}
func frontendEchoErrorIsNotFound(err error) bool {
	var httpError *echo.HTTPError
	return errors.As(err, &httpError) && httpError.Code == http.StatusNotFound
}
`
	case "nethttp":
		aliases = `
	"github.com/NARUBROWN/spine"
	adapter "github.com/NARUBROWN/spine/contract/http"
	nethttpplatform "github.com/NARUBROWN/spine-nethttp-platform"
`
		constructors = `
func spineCLIFrontendNethttpOption(options ...nethttpplatform.NetHTTPOption) spine.Option {
	options = append(options, nethttpplatform.WithMiddleware(frontendStaticFallback(frontendStaticRoot(), frontendAPIPrefix())))
	return nethttpplatform.Option(options...)
}
func spineCLIFrontendNethttpFactory(options ...nethttpplatform.NetHTTPOption) adapter.Factory {
	options = append(options, nethttpplatform.WithMiddleware(frontendStaticFallback(frontendStaticRoot(), frontendAPIPrefix())))
	return nethttpplatform.NewFactory(options...)
}
`
	default:
		return nil, fmt.Errorf("unsupported frontend HTTP platform %q", kind)
	}
	var source strings.Builder
	source.WriteString("package " + packageName + "\n\nimport (\n\"bufio\"\n\"bytes\"\n\"net\"\n\"net/http\"\n\"os\"\n\"path/filepath\"\n\"strings\"\n")
	source.WriteString(aliases)
	source.WriteString(")\n")
	serverHelpers := strings.Replace(frontendServerHelpers, `const frontendStaticOutput = "frontend"`, "const frontendStaticOutput = "+strconv.Quote(filepath.ToSlash(defaultStaticRelative)), 1)
	serverHelpers = strings.Replace(serverHelpers, `const frontendDefaultAPIPrefix = "/api"`, "const frontendDefaultAPIPrefix = "+strconv.Quote(apiPrefix), 1)
	source.WriteString(serverHelpers)
	source.WriteString(constructors)
	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return nil, fmt.Errorf("could not format frontend serving overlay: %w", err)
	}
	return formatted, nil
}
