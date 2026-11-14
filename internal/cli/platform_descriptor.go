package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	goversion "go/version"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const platformDescriptorFile = "spine-platform.json"
const platformDescriptorSchema = "spine.platform/v1"

var providerNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$`)
var envSuffixPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

type platformDescriptor struct {
	SchemaVersion       string                `json:"schema_version"`
	Kind                string                `json:"kind"`
	Provider            string                `json:"provider"`
	ContractVersion     string                `json:"contract_version"`
	Package             string                `json:"package"`
	Factory             string                `json:"factory"`
	ConfigType          string                `json:"config_type,omitempty"`
	MinimumGo           string                `json:"minimum_go"`
	Fields              []platformConfigField `json:"fields,omitempty"`
	PublishDestinations bool                  `json:"-"`
	SHA256              string                `json:"-"`
}

type platformConfigField struct {
	Field    string          `json:"field"`
	Type     string          `json:"type"`
	Env      string          `json:"env,omitempty"`
	Required bool            `json:"required,omitempty"`
	Default  json.RawMessage `json:"default,omitempty"`
	Example  json.RawMessage `json:"example,omitempty"`
}

// 테스트는 외부 저장소를 바꾸지 않고 조회 실패와 메타데이터 변화를 재현합니다.
var platformDescriptorResolver = resolvePlatformDescriptor

func validProviderName(value string) bool { return providerNamePattern.MatchString(value) }

func providerModule(provider string) string {
	if !validProviderName(provider) {
		return ""
	}
	return "github.com/NARUBROWN/spine-" + provider + "-platform"
}

func validatorModule(provider string) string {
	if !validProviderName(provider) || provider == "none" {
		return ""
	}
	return "github.com/NARUBROWN/spine-validator-" + provider
}

func platformModuleIdentity(path string) (kind, provider string) {
	const prefix = "github.com/NARUBROWN/spine-"
	if !strings.HasPrefix(path, prefix) {
		return "", ""
	}
	name := strings.TrimPrefix(path, prefix)
	if strings.HasPrefix(name, "frontend-") {
		provider = strings.TrimPrefix(name, "frontend-")
		if validProviderName(provider) {
			return "frontend", provider
		}
	}
	if strings.HasPrefix(name, "validator-") {
		provider = strings.TrimPrefix(name, "validator-")
		if validProviderName(provider) {
			return "validator", provider
		}
	}
	if strings.HasSuffix(name, "-platform") {
		provider = strings.TrimSuffix(name, "-platform")
		if validProviderName(provider) {
			return "event", provider
		}
	}
	return "", ""
}

func providerIdentifier(provider string) string { return strings.ReplaceAll(provider, "-", "_") }

func providerGoIdentifier(provider string) string {
	// '-'와 '_'를 구분해 서로 다른 저장소 이름의 Go 식별자가 충돌하지 않게 합니다.
	return strings.NewReplacer("_", "_underscore_", "-", "_dash_").Replace(provider)
}

func providerTitle(provider string) string {
	identifier := providerGoIdentifier(provider)
	return strings.ToUpper(identifier[:1]) + identifier[1:]
}

func descriptorAlias(d *platformDescriptor) string {
	if d.Kind == "validator" {
		if d.Provider == "playground" {
			return "playground"
		}
		return providerGoIdentifier(d.Provider) + "validator"
	}
	return providerGoIdentifier(d.Provider) + "platform"
}

func descriptorLoader(d *platformDescriptor) string {
	if d.Kind == "validator" {
		return "load" + providerTitle(d.Provider) + "ValidatorConfig"
	}
	return "load" + providerTitle(d.Provider) + "Config"
}

func readPlatformDescriptor(root, kind, provider string) (*platformDescriptor, error) {
	path := filepath.Join(root, platformDescriptorFile)
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return nil, err
	}
	if len(data) > 65536 {
		return nil, fmt.Errorf("%s exceeds 64 KiB", platformDescriptorFile)
	}
	var descriptor platformDescriptor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", platformDescriptorFile, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("%s must contain one JSON object", platformDescriptorFile)
	}
	if err := validatePlatformDescriptor(&descriptor, kind, provider); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	descriptor.SHA256 = hex.EncodeToString(hash[:])
	return &descriptor, nil
}

func exportedIdentifier(value string) bool {
	return token.IsIdentifier(value) && value[0] >= 'A' && value[0] <= 'Z'
}

func validatePlatformDescriptor(d *platformDescriptor, kind, provider string) error {
	invalid := func(reason string) error { return fmt.Errorf("invalid %s: %s", platformDescriptorFile, reason) }
	contract := map[string]string{"event": "spine.event-adapter/v1", "validator": "spine.validation/v1", "http": "spine.http-adapter/v3", "websocket": "spine.websocket-adapter/v1"}[kind]
	if d.SchemaVersion != platformDescriptorSchema || d.Kind != kind || d.Provider != provider || contract == "" || d.ContractVersion != contract {
		return invalid("schema, kind, provider or contract version does not match the selected module")
	}
	if !token.IsIdentifier(d.Package) || !exportedIdentifier(d.Factory) || (d.ConfigType != "" && !exportedIdentifier(d.ConfigType)) {
		return invalid("package, factory and config type must be Go identifiers")
	}
	if (kind == "http" || kind == "websocket") && (d.ConfigType != "" || len(d.Fields) != 0) {
		return invalid("HTTP and WebSocket options do not accept metadata configuration")
	}
	if !goversion.IsValid("go"+d.MinimumGo) || !regexp.MustCompile(`^1\.[0-9]+\.[0-9]+$`).MatchString(d.MinimumGo) {
		return invalid("minimum_go must be a full Go version")
	}
	if (kind == "event" && d.ConfigType == "") || (d.ConfigType == "" && len(d.Fields) != 0) || len(d.Fields) > 64 {
		return invalid("config type and field declarations are inconsistent")
	}
	fields, env := map[string]bool{}, map[string]bool{}
	for _, field := range d.Fields {
		if !exportedIdentifier(field.Field) || fields[field.Field] || (field.Env != "" && (!envSuffixPattern.MatchString(field.Env) || env[field.Env])) {
			return invalid("config fields and environment suffixes must be valid and unique")
		}
		fields[field.Field], env[field.Env] = true, true
		if field.Required && (field.Env == "" || len(field.Example) == 0) {
			return invalid("required environment fields need an example")
		}
		if field.Env == "" && len(field.Default) == 0 {
			return invalid("fixed fields need a default")
		}
		if _, err := configLiteral(field.Type, field.Default); err != nil {
			return invalid(field.Field + ": " + err.Error())
		}
		if _, err := configLiteral(field.Type, field.Example); err != nil {
			return invalid(field.Field + ": invalid example")
		}
		if field.Required && (bytes.Equal(field.Example, []byte(`""`)) || bytes.Equal(field.Example, []byte(`[]`))) {
			return invalid(field.Field + ": required example is empty")
		}
	}
	return nil
}

// 메타데이터는 정해진 자료형의 리터럴만 선언하며 실행 코드나 템플릿을 넣지 않습니다.
func configLiteral(kind string, data json.RawMessage) (string, error) {
	if kind != "string" && kind != "strings" && kind != "bool" && kind != "int" && kind != "duration" {
		return "", fmt.Errorf("unsupported config type %q", kind)
	}
	if len(data) == 0 {
		return "", nil
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return "", fmt.Errorf("null is not a config literal")
	}
	switch kind {
	case "string", "duration":
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return "", err
		}
		if kind == "duration" {
			duration, err := time.ParseDuration(value)
			return fmt.Sprint(int64(duration)), err
		}
		return fmt.Sprintf("%q", value), nil
	case "strings":
		var values []string
		if err := json.Unmarshal(data, &values); err != nil {
			return "", err
		}
		literals := make([]string, len(values))
		for index, value := range values {
			literals[index] = fmt.Sprintf("%q", value)
		}
		return "[]string{" + strings.Join(literals, ",") + "}", nil
	case "bool":
		var value bool
		err := json.Unmarshal(data, &value)
		return fmt.Sprint(value), err
	default:
		var value int64
		err := json.Unmarshal(data, &value)
		return fmt.Sprint(value), err
	}
}

func resolvePlatformDescriptor(ctx context.Context, kind, provider string, module selectedModule, offline bool) (*platformDescriptor, error) {
	root := module.Dir
	if root == "" {
		temporary, err := os.MkdirTemp("", "spine-platform-descriptor-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(temporary)
		output, err := platformGo(ctx, temporary, offline, "mod", "download", "-json", module.Path+"@"+module.Version)
		if err != nil {
			return nil, fmt.Errorf("read platform module %s@%s: %w", module.Path, module.Version, err)
		}
		var downloaded struct{ Dir string }
		if err := json.Unmarshal(output, &downloaded); err != nil || downloaded.Dir == "" {
			return nil, fmt.Errorf("module directory was not returned for %s@%s", module.Path, module.Version)
		}
		root = downloaded.Dir
	}
	descriptor, err := readPlatformDescriptor(root, kind, provider)
	if os.IsNotExist(err) {
		if legacy := legacyPlatformDescriptor(kind, provider); legacy != nil {
			descriptor, err = legacy, nil
		} else {
			return nil, fmt.Errorf("%s@%s does not provide %s for %s", module.Path, module.Version, platformDescriptorFile, kind)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s@%s: %w", module.Path, module.Version, err)
	}
	if kind == "event" {
		descriptor.PublishDestinations = hasPublishDestinations(root, descriptor.ConfigType)
	}
	return descriptor, nil
}

// 기존 어댑터와 새 계약을 쓰는 어댑터가 함께 설치될 수 있어 선택한 모듈의 설정 타입을 확인합니다.
func hasPublishDestinations(root, configType string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, entry.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			group, ok := decl.(*ast.GenDecl)
			if !ok || group.Tok != token.TYPE {
				continue
			}
			for _, spec := range group.Specs {
				typed := spec.(*ast.TypeSpec)
				if typed.Name.Name != configType {
					continue
				}
				structure, ok := typed.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range structure.Fields.List {
					for _, name := range field.Names {
						if name.Name == "PublishDestinations" {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func resolveProjectDescriptors(ctx context.Context, options *newProjectOptions) error {
	resolve := func(kind, provider, path string) (*platformDescriptor, error) {
		module := selectedModule{Path: path, Version: selectedModuleVersion(options.Modules, path)}
		if module.Version == "" {
			return nil, fmt.Errorf("selected platform module is missing: %s", path)
		}
		return platformDescriptorResolver(ctx, kind, provider, module, false)
	}
	if options.Platform != "" && options.Platform != "none" {
		d, err := resolve("http", options.Platform, providerModule(options.Platform))
		if err != nil {
			return err
		}
		options.HTTPDescriptor = d
	}
	if options.WebSocketPlatform != "" && options.WebSocketPlatform != "none" {
		d, err := resolve("websocket", options.WebSocketPlatform, providerModule(options.WebSocketPlatform))
		if err != nil {
			return err
		}
		options.WebSocketDescriptor = d
	}
	for index := range options.Events {
		spec := &options.Events[index]
		descriptor, err := resolve("event", spec.Provider, providerModule(spec.Provider))
		if err != nil {
			return err
		}
		spec.Descriptor = descriptor
	}
	if options.Validator != "" && options.Validator != "none" {
		descriptor, err := resolve("validator", options.Validator, validatorModule(options.Validator))
		if err != nil {
			return err
		}
		options.ValidatorDescriptor = descriptor
	}
	return nil
}

func eventDescriptor(spec eventPlatformSpec) *platformDescriptor {
	if spec.Descriptor != nil {
		return spec.Descriptor
	}
	return legacyPlatformDescriptor("event", spec.Provider)
}

func selectedValidatorDescriptor(options newProjectOptions) *platformDescriptor {
	if options.ValidatorDescriptor != nil {
		return options.ValidatorDescriptor
	}
	return legacyPlatformDescriptor("validator", options.Validator)
}

func platformOption(descriptor *platformDescriptor) string {
	if descriptor != nil {
		return descriptor.Factory
	}
	return "Option"
}

func projectGoVersion(options newProjectOptions) string {
	version := assemblyGoVersion(options.Validator, options.Events...)
	if descriptor := selectedValidatorDescriptor(options); descriptor != nil && goversion.Compare("go"+descriptor.MinimumGo, "go"+version) > 0 {
		version = descriptor.MinimumGo
	}
	for _, d := range []*platformDescriptor{options.HTTPDescriptor, options.WebSocketDescriptor} {
		if d != nil && goversion.Compare("go"+d.MinimumGo, "go"+version) > 0 {
			version = d.MinimumGo
		}
	}
	return version
}

// 기존 공개 버전의 호환 메타데이터입니다. 새 provider의 허용 목록으로 사용하지 않습니다.
func legacyPlatformDescriptor(kind, provider string) *platformDescriptor {
	d := &platformDescriptor{SchemaVersion: platformDescriptorSchema, Kind: kind, Provider: provider, Package: "platform", Factory: "NewFactory", ConfigType: "Config", MinimumGo: "1.25.5", ContractVersion: "spine.event-adapter/v1"}
	field := func(name, kind, env string, required bool, def, example string) platformConfigField {
		return platformConfigField{Field: name, Type: kind, Env: env, Required: required, Default: json.RawMessage(def), Example: json.RawMessage(example)}
	}
	if (kind == "http" && (provider == "echo" || provider == "nethttp")) || (kind == "websocket" && provider == "gorilla") {
		d.ConfigType, d.Factory = "", "Option"
		if kind == "http" {
			d.ContractVersion = "spine.http-adapter/v3"
		} else {
			d.ContractVersion = "spine.websocket-adapter/v1"
		}
	} else if kind == "validator" && provider == "playground" {
		d.Package, d.Factory, d.ConfigType, d.MinimumGo, d.ContractVersion = "playground", "New", "", "1.26.0", "spine.validation/v1"
	} else if kind == "event" && provider == "kafka" {
		d.Fields = []platformConfigField{field("Brokers", "strings", "BROKERS", true, "", `["127.0.0.1:9092"]`), field("GroupID", "string", "GROUP_ID", true, "", `"generated-test"`)}
	} else if kind == "event" && provider == "rabbitmq" {
		d.Fields = []platformConfigField{field("URL", "string", "URL", true, "", `"amqp://guest:guest@127.0.0.1:5672/"`), field("Exchange", "string", "EXCHANGE", true, "", `"generated-test"`), field("QueuePrefix", "string", "QUEUE_PREFIX", false, "", ""), field("FailurePolicy", "string", "", false, `"reject"`, "")}
	} else {
		return nil
	}
	if kind == "event" {
		d.Fields = append(d.Fields, field("TopicPrefix", "string", "TOPIC_PREFIX", false, "", ""), field("AllowInsecureTransport", "bool", "ALLOW_INSECURE", false, "false", "true"), field("Consume", "bool", "", false, "true", ""), field("Publish", "bool", "", false, "true", ""))
	}
	data, _ := json.Marshal(d)
	hash := sha256.Sum256(data)
	d.SHA256 = "legacy-" + hex.EncodeToString(hash[:])
	return d
}
