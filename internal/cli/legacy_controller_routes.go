package cli

import (
	"fmt"
	"strings"
)

func legacyControllerRoutes(source string, name resourceName, slice bool) string {
	controller := name.Type + "Controller"
	source = strings.Replace(source, "import (", "import (\n\"github.com/NARUBROWN/spine/pkg/api\"", 1)
	source = strings.Replace(source, "func New"+controller+"(", "func (*"+controller+") Constructor(", 1)
	methods := fmt.Sprintf("r.GET(%q, c.Get)\n", "/"+name.Route+"/:id")
	if slice {
		methods = fmt.Sprintf("r.POST(%q, c.Create)\n", "/"+name.Route) + methods
	}
	return source + fmt.Sprintf("\nfunc (c *%s) RegisterRoutes(r *api.Registry) {\n%s}\n", controller, methods)
}

func legacyControllerModule(source string, name resourceName) string {
	source = strings.Replace(source, "import \"github.com/NARUBROWN/spine\"", "import (\n\"github.com/NARUBROWN/spine\"\n\"github.com/NARUBROWN/spine/pkg/constructor\"\n)", 1)
	source = strings.Replace(source, "New"+name.Type+"Controller,", "constructor.Controller(&"+name.Type+"Controller{}),", 1)
	var lines []string
	for _, line := range strings.Split(source, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "app.Route(") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
