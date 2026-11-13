package cli

import (
	"fmt"
	"sort"
	"strings"
)

func (p *layeredPlanner) validateGraphs() error {
	packages := map[string][]string{}
	for _, source := range p.Sources.sortedFiles() {
		key := modulePackage(p.Module, source.Package)
		for _, imported := range importPathsByAlias(source.File) {
			if imported == p.Module || strings.HasPrefix(imported, p.Module+"/") {
				packages[key] = append(packages[key], imported)
			}
		}
	}
	if err := acyclicGraph(packages, "package import"); err != nil {
		return err
	}
	if !p.Register {
		return nil
	}
	inspector, err := inspectLayeredRegistrations(p.Config, p.Module, p.Sources)
	if err != nil {
		return err
	}
	for _, diagnostic := range inspector.contract.Diagnostics {
		if diagnostic.Severity == "error" {
			return fmt.Errorf("%s: %s (%s)", diagnostic.Code, diagnostic.Message, diagnostic.Source)
		}
	}
	inspector.resolveHandlerSignatures()
	for _, diagnostic := range inspector.contract.Diagnostics {
		// 메시지 생성기는 미구현 골격을 허용하되 HTTP 입출력 계약은 적용 전에 확인합니다.
		if diagnostic.Severity == "error" && strings.HasPrefix(diagnostic.Code, "HTTP_") {
			return fmt.Errorf("%s: %s. %s", diagnostic.Code, diagnostic.Message, diagnostic.Hint)
		}
	}
	signatures := p.Sources.constructorSignatures(p.Module)
	graph := map[string][]string{}
	providers := map[string]string{}
	for _, constructor := range inspector.contract.Constructors {
		matches := signatures[constructor.Target]
		if len(matches) != 1 || matches[0].Output == "" {
			return fmt.Errorf("constructor %s must resolve to one non-variadic, non-generic function returning a value or a value and error", constructor.Target)
		}
		signature := matches[0]
		if old, exists := providers[signature.Output]; exists && old != constructor.Target {
			return fmt.Errorf("ambiguous constructors for %s: %s and %s", signature.Output, old, constructor.Target)
		}
		providers[signature.Output] = constructor.Target
		graph[signature.Output] = signature.Dependencies
	}
	for _, output := range sortedGraphKeys(graph) {
		for _, input := range graph[output] {
			if _, exists := providers[input]; !exists {
				return fmt.Errorf("constructor dependency %s has no registered provider", input)
			}
		}
	}
	return acyclicGraph(graph, "constructor dependency")
}

func sortedGraphKeys(graph map[string][]string) []string {
	keys := make([]string, 0, len(graph))
	for key := range graph {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func acyclicGraph(graph map[string][]string, kind string) error {
	state := map[string]int{}
	var visit func(string) error
	visit = func(node string) error {
		if state[node] == 1 {
			return fmt.Errorf("%s cycle at %s", kind, node)
		}
		if state[node] == 2 {
			return nil
		}
		state[node] = 1
		children := append([]string(nil), graph[node]...)
		sort.Strings(children)
		for _, next := range children {
			if err := visit(next); err != nil {
				return err
			}
		}
		state[node] = 2
		return nil
	}
	for _, key := range sortedGraphKeys(graph) {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}
