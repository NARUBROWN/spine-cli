package cli

import (
	"fmt"
	"strings"
)

// Preview로 생성한 프로젝트에서는 새 path DTO를 사용하고 기존 프로젝트의 템플릿은 보존합니다.
func previewHTTPPathTemplate(source, name string) string {
	config, err := loadProjectConfig()
	if err != nil || !isPreviewAssemblyRecipe(config.Recipe) {
		return source
	}
	source = strings.ReplaceAll(source, `"github.com/NARUBROWN/spine/pkg/path"`, "")
	declaration := fmt.Sprintf("type %sPath struct { ID int64 `path:\"id\"` }\n\n", name)
	source = strings.Replace(source, "type "+name+"Controller struct", declaration+"type "+name+"Controller struct", 1)
	source = strings.ReplaceAll(source, "id path.Int", "params *"+name+"Path")
	return strings.ReplaceAll(source, "id.Value", "params.ID")
}
