package cli

import (
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

type layeredRequest struct {
	Kind                  string
	Name                  generationName
	Fields                []sliceField
	Depends               []string
	Method, Path, Handler string
	Topic                 string
	Register              bool
	Options               writeOptions
	JSON                  bool
}

func parseLayeredRequest(args []string) (layeredRequest, error) {
	r := layeredRequest{Register: true}
	if len(args) < 2 {
		return r, fmt.Errorf("usage: spine g <domain|dto|repository|service|controller|resource|route> <[group/]name> [options]")
	}
	r.Kind = args[0]
	_, roleOK := roleFor(r.Kind)
	if !roleOK && r.Kind != "resource" && r.Kind != "route" && r.Kind != "consumer" && r.Kind != "websocket" {
		return r, fmt.Errorf("generator %q is unavailable in %s; use a supported role from spine schema --json", r.Kind, layeredLayout)
	}
	var err error
	r.Name, err = parseGenerationName(args[1])
	if err != nil {
		return r, err
	}
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			r.Options.DryRun = true
		case "--force":
			r.Options.Force = true
		case "--json":
			r.JSON = true
		case "--no-register":
			r.Register = false
		case "--register":
			r.Register = true
		case "--if-match", "--field", "--depends", "--method", "--path", "--handler", "--topic":
			option := args[i]
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return r, fmt.Errorf("%s requires a value", option)
			}
			i++
			value := args[i]
			switch option {
			case "--if-match":
				r.Options.IfMatch = value
			case "--field":
				if r.Kind != "domain" && r.Kind != "dto" && r.Kind != "consumer" && r.Kind != "websocket" {
					return r, fmt.Errorf("--field requires domain, dto, consumer or websocket")
				}
				field, err := parseRoleField(value)
				if err != nil {
					return r, err
				}
				r.Fields = append(r.Fields, field)
			case "--depends":
				if r.Kind != "repository" && r.Kind != "service" && r.Kind != "controller" {
					return r, fmt.Errorf("--depends requires a constructor role")
				}
				r.Depends = append(r.Depends, value)
			case "--method":
				r.Method = strings.ToUpper(value)
			case "--path":
				r.Path = value
			case "--handler":
				r.Handler = value
			case "--topic":
				r.Topic = value
			}
		default:
			return r, fmt.Errorf("unknown option: %s", args[i])
		}
	}
	if r.Kind != "route" && (r.Method != "" || (r.Path != "" && r.Kind != "websocket") || r.Handler != "") {
		return r, fmt.Errorf("--method, --path and --handler require the route generator")
	}
	if r.Kind == "route" {
		if !r.Register {
			return r, fmt.Errorf("route generation requires registration")
		}
		switch r.Method {
		case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		default:
			return r, fmt.Errorf("route requires a supported --method")
		}
		if !strings.HasPrefix(r.Path, "/") || strings.ContainsAny(r.Path, " ?#\t\n") {
			return r, fmt.Errorf("route requires an absolute --path without query or fragment")
		}
		if r.Handler == "" {
			return r, fmt.Errorf("route requires --handler package/path:Type.Method")
		}
	}
	if r.Kind == "consumer" && (r.Topic == "" || len(r.Fields) == 0) {
		return r, fmt.Errorf("consumer requires --topic and at least one --field")
	}
	if r.Kind == "websocket" && (!strings.HasPrefix(r.Path, "/") || len(r.Fields) == 0) {
		return r, fmt.Errorf("websocket requires --path and at least one --field")
	}
	if r.Kind != "consumer" && r.Topic != "" {
		return r, fmt.Errorf("--topic requires consumer")
	}
	seen := map[string]bool{}
	for _, f := range r.Fields {
		if seen[f.GoName] {
			return r, fmt.Errorf("duplicate field %s", f.GoName)
		}
		seen[f.GoName] = true
	}
	return r, nil
}

func parseRoleField(value string) (sliceField, error) {
	name, typ, ok := strings.Cut(value, ":")
	if !ok {
		return sliceField{}, fmt.Errorf("--field requires name:type")
	}
	n, err := parseResourceName(name)
	if err != nil {
		return sliceField{}, err
	}
	// ID는 자동 생성하거나 예약하지 않으며, 명시한 필드만 생성합니다.
	if strings.EqualFold(name, "id") {
		n.Type = "ID"
	}
	switch typ {
	case "string", "bool", "int", "int64", "float64":
	default:
		return sliceField{}, fmt.Errorf("unsupported field type %q", typ)
	}
	return sliceField{GoName: n.Type, JSONName: n.Package, GoType: typ}, nil
}

func runLayeredGenerate(args []string, config projectConfig, out, errOut io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(errOut, err); return 1 }
	r, err := parseLayeredRequest(args)
	if err != nil {
		return fail(err)
	}
	module, err := modulePath()
	if err != nil {
		return fail(err)
	}
	lock, err := acquireProjectLock(generationLockPath)
	if err != nil {
		return fail(err)
	}
	defer lock.release()
	sources, inputHash, err := readLayoutSources()
	if err != nil {
		return fail(err)
	}
	p, err := planLayeredGeneration(r, config, module, sources)
	if err != nil {
		return fail(err)
	}
	changes := []fileChange{}
	for _, change := range p.Changes {
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	if err = applyFileChanges(changes, true); err != nil {
		if strings.Contains(err.Error(), "refusing to overwrite") {
			if diff, diffErr := renderFileChanges(changes); diffErr == nil {
				if r.JSON {
					_ = encodeIndentedJSON(map[string]any{"layout": layeredLayout, "kind": r.Kind, "group": r.Name.Group, "diff": diff}, out, errOut)
				} else {
					fmt.Fprint(out, diff)
				}
			}
		}
		return fail(err)
	}
	_, currentHash, err := readLayoutSources()
	if err != nil {
		return fail(err)
	}
	if inputHash != currentHash {
		return fail(fmt.Errorf("generation inputs changed during planning; create a new dry-run"))
	}
	baseHash, err := layeredPlanHash(changes, inputHash)
	if err != nil {
		return fail(err)
	}
	if r.Options.IfMatch != "" && r.Options.IfMatch != baseHash {
		return fail(fmt.Errorf("generation inputs or targets changed since dry-run: expected base hash %s, current %s", r.Options.IfMatch, baseHash))
	}
	plan, err := planFileChanges(changes)
	if err != nil {
		return fail(err)
	}
	diff, err := renderFileChanges(changes)
	if err != nil {
		return fail(err)
	}
	if err = applyFileChanges(changes, r.Options.DryRun); err != nil {
		return fail(err)
	}
	files := []string{}
	for _, c := range changes {
		files = append(files, c.Path)
	}
	registered := p.Register
	details := p.details(r)
	if r.JSON {
		return encodeIndentedJSON(map[string]any{"layout": layeredLayout, "kind": r.Kind, "name": r.Name.Name.Type, "group": r.Name.Group, "roles": generationRoles(), "files": files, "registered": registered, "dry_run": r.Options.DryRun, "base_hash": baseHash, "inputs_hash": inputHash, "changes": plan, "diff": diff, "types": details.Types, "registrations": details.Registrations, "routes": details.Routes}, out, errOut)
	}
	if r.Options.DryRun {
		fmt.Fprintf(out, "Would generate %s %s (%s)\n%s", r.Kind, r.Name.Name.Type, layeredLayout, diff)
		printDryRunHash(out, baseHash)
	} else {
		fmt.Fprintf(out, "Generated %s %s (%s)\n", r.Kind, r.Name.Name.Type, layeredLayout)
		printGeneratedFiles(out, plan)
		printGenerationDetails(out, details)
	}
	return 0
}

func (p *layeredPlanner) addComponent(kind string, name generationName, fields []sliceField, refs []string) error {
	role, _ := roleFor(kind)
	dir, pkg, err := roleTarget(role, name)
	if err != nil {
		return err
	}
	typeName := name.Name.Type + role.Suffix
	fileName := name.Name.Package
	if role.Constructor {
		fileName += "_" + kind
	}
	path := filepath.Join(dir, fileName+".go")
	// 같은 패키지의 다른 파일에 있는 선언과 충돌하지 않도록 확인합니다.
	reserved := map[string]bool{typeName: true}
	if role.Constructor {
		reserved["New"+typeName] = true
	}
	for _, s := range p.Sources {
		if s.Package != filepath.ToSlash(dir) || s.Path == filepath.ToSlash(path) {
			continue
		}
		for _, decl := range s.File.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && reserved[fn.Name.Name] {
				return fmt.Errorf("declaration %s already exists in %s", fn.Name.Name, s.Path)
			}
			if g, ok := decl.(*ast.GenDecl); ok {
				for _, spec := range g.Specs {
					names := []*ast.Ident{}
					switch value := spec.(type) {
					case *ast.TypeSpec:
						names = append(names, value.Name)
					case *ast.ValueSpec:
						names = value.Names
					}
					for _, name := range names {
						if reserved[name.Name] {
							return fmt.Errorf("declaration %s already exists in %s", name.Name, s.Path)
						}
					}
				}
			}
		}
	}
	if !role.Constructor {
		var b strings.Builder
		fmt.Fprintf(&b, "package %s\n\ntype %s struct {\n", pkg, typeName)
		for _, f := range fields {
			tag := ""
			if kind == "dto" {
				tag = " `json:" + fmt.Sprintf("%q", f.JSONName) + "`"
			}
			fmt.Fprintf(&b, "%s %s%s\n", f.GoName, f.GoType, tag)
		}
		b.WriteString("}\n")
		return p.put(path, b.String(), false)
	}
	imports := map[string]string{}
	deps := []layeredDependency{}
	seen := map[string]bool{}
	fieldsUsed := map[string]bool{}
	for _, ref := range refs {
		dep, err := p.resolveDependency(ref)
		if err != nil {
			return err
		}
		if seen[dep.Key] {
			return fmt.Errorf("duplicate dependency %s", ref)
		}
		seen[dep.Key] = true
		alias := ""
		if dep.Package != filepath.ToSlash(dir) {
			alias = chooseAlias(imports, filepath.Base(dep.Package), p.Module+"/"+dep.Package)
			imports[alias] = p.Module + "/" + dep.Package
		}
		dep.Expression = dep.Pointer + dep.Type
		if alias != "" {
			dep.Expression = dep.Pointer + alias + "." + dep.Type
		}
		dep.Field = lowerFirst(dep.Type)
		for suffix := 2; fieldsUsed[dep.Field]; suffix++ {
			dep.Field = fmt.Sprintf("%s%d", lowerFirst(dep.Type), suffix)
		}
		fieldsUsed[dep.Field] = true
		deps = append(deps, dep)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	writeImports(&b, imports)
	fmt.Fprintf(&b, "type %s struct {\n", typeName)
	for _, dep := range deps {
		fmt.Fprintf(&b, "%s %s\n", dep.Field, dep.Expression)
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "func New%s(", typeName)
	for i, dep := range deps {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s %s", dep.Field, dep.Expression)
	}
	fmt.Fprintf(&b, ") *%s {\nreturn &%s{", typeName, typeName)
	for _, dep := range deps {
		fmt.Fprintf(&b, "%s: %s,", dep.Field, dep.Field)
	}
	b.WriteString("}\n}\n")
	if err = p.put(path, b.String(), false); err != nil {
		return err
	}
	if p.Register {
		for _, dep := range deps {
			if err = p.registerConstructor(dep.Package, dep.Factory); err != nil {
				return err
			}
		}
		return p.registerConstructor(filepath.ToSlash(dir), "New"+typeName)
	}
	return nil
}

func chooseAlias(imports map[string]string, preferred, path string) string {
	aliases := make([]string, 0, len(imports))
	for alias := range imports {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		if imports[alias] == path && alias != "_" && alias != "." {
			return alias
		}
	}

	if preferred == "" || preferred == "_" || token.Lookup(preferred).IsKeyword() {
		preferred = "component"
	}
	for index := 0; ; index++ {
		alias := preferred
		if index > 0 {
			alias = fmt.Sprintf("%s%d", preferred, index)
		}
		if _, exists := imports[alias]; !exists {
			return alias
		}
	}
}

func writeImports(b *strings.Builder, imports map[string]string) {
	if len(imports) == 0 {
		return
	}
	b.WriteString("import (\n")
	keys := []string{}
	for key := range imports {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(b, "%s %q\n", key, imports[key])
	}
	b.WriteString(")\n\n")
}

func (p *layeredPlanner) addMessageModule(r layeredRequest) error {
	registration := r.Topic
	if r.Kind == "websocket" {
		registration = r.Path
	}
	files := messageModuleFiles(r.Kind, r.Name.Name, registration, r.Fields)
	directory := filepath.ToSlash(filepath.Join("src", r.Kind, r.Name.Group, r.Name.Name.Package))
	if _, _, err := roleTarget(generationRole{Root: "src/" + r.Kind}, generationName{Group: filepath.ToSlash(filepath.Join(r.Name.Group, r.Name.Name.Package))}); err != nil {
		return err
	}
	for path, source := range files {
		if err := p.put(filepath.Join(directory, filepath.Base(path)), source, false); err != nil {
			return err
		}
	}
	if p.Register {
		return p.registerModule(directory)
	}
	return nil
}
