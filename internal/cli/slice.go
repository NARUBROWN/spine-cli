package cli

import (
	"fmt"
	"path/filepath"
	"strings"
)

type sliceField struct {
	GoName   string
	JSONName string
	GoType   string
}

func parseSliceField(value string) (sliceField, error) {
	nameValue, typeValue, ok := strings.Cut(value, ":")
	if !ok || strings.TrimSpace(nameValue) == "" || strings.TrimSpace(typeValue) == "" {
		return sliceField{}, fmt.Errorf("--field must use name:type syntax")
	}
	name, err := parseResourceName(nameValue)
	if err != nil {
		return sliceField{}, fmt.Errorf("invalid field %q: %w", nameValue, err)
	}
	if strings.EqualFold(name.Type, "ID") || name.Package == "id" {
		return sliceField{}, fmt.Errorf("field name %q is reserved for the generated ID", nameValue)
	}
	goType := strings.TrimSpace(typeValue)
	switch goType {
	case "string", "bool", "int", "int64", "float64":
	default:
		return sliceField{}, fmt.Errorf("unsupported field type %q; use string, bool, int, int64, or float64", goType)
	}
	return sliceField{GoName: name.Type, JSONName: name.Package, GoType: goType}, nil
}

func validateSliceFields(fields []sliceField) error {
	if len(fields) == 0 {
		return fmt.Errorf("slice generation requires at least one --field name:type")
	}
	seen := map[string]bool{}
	for _, field := range fields {
		if seen[field.JSONName] {
			return fmt.Errorf("duplicate slice field %q", field.JSONName)
		}
		seen[field.JSONName] = true
	}
	return nil
}

func sliceFiles(name resourceName, fields []sliceField) map[string]string {
	base := filepath.Join("src", name.Package)
	return map[string]string{
		filepath.Join(base, "module.go"):                     sliceModuleTemplate(name),
		filepath.Join(base, name.Package+"_model.go"):        sliceModelTemplate(name, fields),
		filepath.Join(base, name.Package+"_repository.go"):   sliceRepositoryTemplate(name),
		filepath.Join(base, name.Package+"_service.go"):      sliceServiceTemplate(name, fields),
		filepath.Join(base, name.Package+"_controller.go"):   sliceControllerTemplate(name),
		filepath.Join(base, name.Package+"_service_test.go"): sliceServiceTestTemplate(name, fields),
	}
}

func sliceModuleTemplate(name resourceName) string {
	return fmt.Sprintf(`package %s

import "github.com/NARUBROWN/spine"

// Register는 이 vertical slice의 생성자와 HTTP 계약을 애플리케이션에 등록합니다.
func Register(app spine.App) error {
	app.Constructor(
		New%sRepository,
		New%sService,
		New%sController,
	)
	app.Route("POST", "/%s", (*%sController).Create)
	app.Route("GET", "/%s/:id", (*%sController).Get)
	return nil
}
`, name.Package, name.Type, name.Type, name.Type, name.Route, name.Type, name.Route, name.Type)
}

func sliceModelTemplate(name resourceName, fields []sliceField) string {
	var modelFields, requestFields strings.Builder
	for _, field := range fields {
		fmt.Fprintf(&modelFields, "\t%s %s `json:\"%s\"`\n", field.GoName, field.GoType, field.JSONName)
		fmt.Fprintf(&requestFields, "\t%s %s `json:\"%s\"`\n", field.GoName, field.GoType, field.JSONName)
	}
	return fmt.Sprintf(`package %s

type %s struct {
	ID int64 `+"`json:\"id\"`"+`
%s}

type Create%sRequest struct {
%s}
`, name.Package, name.Type, modelFields.String(), name.Type, requestFields.String())
}

func sliceRepositoryTemplate(name resourceName) string {
	return fmt.Sprintf(`package %[1]s

import "sync"

type %[2]sRepository interface {
	Save(value %[2]s) %[2]s
	FindByID(id int64) (%[2]s, bool)
}

// memory%[2]sRepository는 명시적으로 선택한 개발용 메모리 저장소입니다.
// 프로세스가 종료되면 데이터가 사라지므로 운영 영속성 구현으로 교체해야 합니다.
type memory%[2]sRepository struct {
	mu     sync.RWMutex
	nextID int64
	items  map[int64]%[2]s
}

func New%[2]sRepository() %[2]sRepository {
	return &memory%[2]sRepository{nextID: 1, items: make(map[int64]%[2]s)}
}

func (r *memory%[2]sRepository) Save(value %[2]s) %[2]s {
	r.mu.Lock()
	defer r.mu.Unlock()
	if value.ID == 0 {
		value.ID = r.nextID
		r.nextID++
	}
	r.items[value.ID] = value
	return value
}

func (r *memory%[2]sRepository) FindByID(id int64) (%[2]s, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.items[id]
	return value, ok
}
`, name.Package, name.Type)
}

func sliceServiceTemplate(name resourceName, fields []sliceField) string {
	var assignments strings.Builder
	for _, field := range fields {
		fmt.Fprintf(&assignments, "\t\t%s: request.%s,\n", field.GoName, field.GoName)
	}
	return fmt.Sprintf(`package %s

type %sService struct {
	repository %sRepository
}

func New%sService(repository %sRepository) *%sService {
	return &%sService{repository: repository}
}

func (s *%sService) Create(request Create%sRequest) %s {
	return s.repository.Save(%s{
%s	})
}

func (s *%sService) Get(id int64) (%s, bool) {
	return s.repository.FindByID(id)
}
`, name.Package, name.Type, name.Type, name.Type, name.Type, name.Type, name.Type, name.Type, name.Type, name.Type, name.Type, assignments.String(), name.Type, name.Type)
}

func sliceControllerTemplate(name resourceName) string {
	return fmt.Sprintf(`package %[1]s

import (
	"net/http"

	"github.com/NARUBROWN/spine/pkg/httperr"
	"github.com/NARUBROWN/spine/pkg/httpx"
	"github.com/NARUBROWN/spine/pkg/path"
)

type %[2]sController struct {
	service *%[2]sService
}

func New%[2]sController(service *%[2]sService) *%[2]sController {
	return &%[2]sController{service: service}
}

func (c *%[2]sController) Create(request *Create%[2]sRequest) (httpx.Response[%[2]s], error) {
	if request == nil {
		return httpx.Response[%[2]s]{}, httperr.BadRequest("request body is required")
	}
	created := c.service.Create(*request)
	return httpx.Response[%[2]s]{
		Body:    created,
		Options: httpx.ResponseOptions{Status: http.StatusCreated},
	}, nil
}

func (c *%[2]sController) Get(id path.Int) (httpx.Response[%[2]s], error) {
	value, ok := c.service.Get(id.Value)
	if !ok {
		return httpx.Response[%[2]s]{}, httperr.NotFound("resource not found")
	}
	return httpx.Response[%[2]s]{Body: value}, nil
}
`, name.Package, name.Type)
}

func sliceServiceTestTemplate(name resourceName, fields []sliceField) string {
	values := sampleFieldValues(fields)
	var requestValues, assertions strings.Builder
	for _, field := range fields {
		value := values[field.JSONName]
		fmt.Fprintf(&requestValues, "\t\t%s: %s,\n", field.GoName, value)
		fmt.Fprintf(&assertions, "\tif found.%s != created.%s {\n\t\tt.Fatalf(\"%s mismatch: %%v != %%v\", found.%s, created.%s)\n\t}\n", field.GoName, field.GoName, field.JSONName, field.GoName, field.GoName)
	}
	return fmt.Sprintf(`package %s

import "testing"

func Test%sServiceCreateAndGet(t *testing.T) {
	service := New%sService(New%sRepository())
	created := service.Create(Create%sRequest{
%s	})
	if created.ID == 0 {
		t.Fatal("created value must receive an ID")
	}
	found, ok := service.Get(created.ID)
	if !ok {
		t.Fatal("created value was not found")
	}
%s}
`, name.Package, name.Type, name.Type, name.Type, name.Type, requestValues.String(), assertions.String())
}

func sampleFieldValues(fields []sliceField) map[string]string {
	result := map[string]string{}
	for _, field := range fields {
		switch field.GoType {
		case "string":
			result[field.JSONName] = `"sample"`
		case "bool":
			result[field.JSONName] = "true"
		case "float64":
			result[field.JSONName] = "1.5"
		default:
			result[field.JSONName] = "1"
		}
	}
	return result
}
