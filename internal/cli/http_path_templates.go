package cli

import (
	"fmt"
	"strings"
)

// 프로젝트의 코어 버전에 맞는 입력 인자를 컨트롤러에 생성합니다.
func httpControllerInputTemplate(source, name string) string {
	explicitInputs := requiresExplicitHTTPInputs(declaredSpineVersion())
	if !explicitInputs {
		config, err := loadProjectConfig()
		if err != nil || !isPreviewAssemblyRecipe(config.Recipe) {
			return source
		}
	}

	pathType := "*" + name + "Path"
	inputImport := ""
	if explicitInputs {
		pathType = "input.Path[" + name + "Path]"
		inputImport = `"github.com/NARUBROWN/spine/pkg/input"`
		requestType := "Create" + name + "Request"
		source = strings.ReplaceAll(source, "request *"+requestType, "request input.Body["+requestType+"]")
	}

	source = strings.ReplaceAll(source, `"github.com/NARUBROWN/spine/pkg/path"`, inputImport)
	declaration := fmt.Sprintf("type %sPath struct { ID int64 `path:\"id\"` }\n\n", name)
	source = strings.Replace(source, "type "+name+"Controller struct", declaration+"type "+name+"Controller struct", 1)
	source = strings.ReplaceAll(source, "id path.Int", "params "+pathType)
	return strings.ReplaceAll(source, "id.Value", "params.ID")
}
