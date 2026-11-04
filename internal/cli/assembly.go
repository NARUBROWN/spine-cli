package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	assemblyRecipe           = "public-contracts-20261103"
	assemblySpineVersion     = "v0.6.1-0.20261103010611-6e60b34204d3"
	kafkaPlatformPath        = "github.com/NARUBROWN/spine-kafka-platform"
	kafkaPlatformVersion     = "v0.0.0-20261103001523-e4b87708f841"
	rabbitPlatformPath       = "github.com/NARUBROWN/spine-rabbitmq-platform"
	rabbitPlatformVersion    = "v0.0.0-20261103001525-b2be17ee8a11"
	validatorPlatformPath    = "github.com/NARUBROWN/spine-validator-playground"
	validatorPlatformVersion = "v0.0.0-20261103011253-5508c82a155e"
)

type eventPlatformSpec struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

var eventPlatformName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func parseEventPlatform(value string) (eventPlatformSpec, error) {
	name, provider, named := strings.Cut(value, ":")
	if !named {
		provider = name
	}
	spec := eventPlatformSpec{Name: name, Provider: provider}
	if !eventPlatformName.MatchString(name) {
		return spec, fmt.Errorf("event platform name %q must match [a-z][a-z0-9_]*", name)
	}
	if provider != "kafka" && provider != "rabbitmq" {
		return spec, fmt.Errorf("unsupported event platform: %s", provider)
	}
	return spec, nil
}

func validateAssemblySelection(http string, events []eventPlatformSpec, validator string) error {
	if validator != "none" && validator != "playground" {
		return fmt.Errorf("unsupported validator: %s", validator)
	}
	names := map[string]bool{}
	for _, spec := range events {
		if _, err := parseEventPlatform(spec.Name + ":" + spec.Provider); err != nil {
			return err
		}
		if names[spec.Name] {
			return fmt.Errorf("duplicate event platform name: %s", spec.Name)
		}
		names[spec.Name] = true
	}
	if http == "none" && len(events) == 0 {
		return fmt.Errorf("--platform none requires at least one --event-platform")
	}
	return nil
}

func assemblyGoVersion(validator string) string {
	if validator == "playground" {
		return "1.26.0"
	}
	return "1.25.5"
}

func assemblyModules(http string, events []eventPlatformSpec, validator string) []selectedModule {
	modules := []selectedModule{{Path: spineImportPath, Version: assemblySpineVersion}}
	if http == "echo" {
		modules = append(modules, selectedModule{Path: echoPlatformPath, Version: echoPlatformVersion}, selectedModule{Path: "github.com/labstack/echo/v5", Version: platformEchoVersion})
	}
	providers := map[string]bool{}
	for _, spec := range events {
		providers[spec.Provider] = true
	}
	if providers["kafka"] {
		modules = append(modules, selectedModule{Path: kafkaPlatformPath, Version: kafkaPlatformVersion})
	}
	if providers["rabbitmq"] {
		modules = append(modules, selectedModule{Path: rabbitPlatformPath, Version: rabbitPlatformVersion})
	}
	if validator == "playground" {
		modules = append(modules, selectedModule{Path: validatorPlatformPath, Version: validatorPlatformVersion})
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })
	return modules
}

func assemblyGoMod(options newProjectOptions) string {
	var source strings.Builder
	fmt.Fprintf(&source, "module %s\n\ngo %s\n\nrequire (\n", options.Module, assemblyGoVersion(options.Validator))
	for _, module := range currentAssemblyModules(options) {
		// Echo 자체는 플랫폼을 통해 선택되며 앱에서 직접 가져올 때 tidy가 직접 의존성으로 바꿉니다.
		if module.Path == "github.com/labstack/echo/v5" {
			continue
		}
		fmt.Fprintf(&source, " %s %s\n", module.Path, module.Version)
	}
	return source.String() + ")\n"
}

func assemblyApplication(options newProjectOptions) (string, error) {
	var source strings.Builder
	source.WriteString("package application\n\nimport (\n\"github.com/NARUBROWN/spine\"\n\"github.com/NARUBROWN/spine/pkg/boot\"\n")
	if options.Platform == "echo" {
		source.WriteString("\"os\"\nechoplatform \"" + echoPlatformPath + "\"\n")
		if options.Layout == layeredLayout {
			fmt.Fprintf(&source, "controller %q\nroute %q\n", options.Module+"/src/controller", options.Module+"/src/route")
		} else {
			source.WriteString("\"github.com/NARUBROWN/spine/pkg/httpx\"\n")
		}
	}
	if options.WebSocketPlatform == "gorilla" {
		fmt.Fprintf(&source, "gorillaplatform %q\n", gorillaPlatformPath)
	}
	providers := map[string]bool{}
	for _, spec := range options.Events {
		providers[spec.Provider] = true
	}
	for _, provider := range []string{"kafka", "rabbitmq"} {
		if providers[provider] {
			path := kafkaPlatformPath
			if provider == "rabbitmq" {
				path = rabbitPlatformPath
			}
			fmt.Fprintf(&source, "%splatform %q\n", provider, path)
		}
	}
	if options.Validator == "playground" {
		fmt.Fprintf(&source, "playground %q\n", validatorPlatformPath)
	}
	source.WriteString(")\n\n")
	if options.Layout == "legacy" && options.Platform == "echo" {
		source.WriteString(`type HealthController struct{}
func NewHealthController() *HealthController { return &HealthController{} }
func (*HealthController) Get() httpx.Response[map[string]string] {
 return httpx.Response[map[string]string]{Body: map[string]string{"status":"ok"}}
}
`)
	}
	source.WriteString("// BuildApplication은 플랫폼 설정을 검사하고 생성자와 수신 계약을 하나의 App에 연결합니다.\nfunc BuildApplication() (spine.App, boot.Options, error) {\n")
	for index, spec := range options.Events {
		fmt.Fprintf(&source, "config%d, err := load%sConfig(%q)\nif err != nil { return nil, boot.Options{}, err }\nfactory%d, err := %splatform.NewFactory(config%d)\nif err != nil { return nil, boot.Options{}, err }\n", index, strings.ToUpper(spec.Provider[:1])+spec.Provider[1:], eventEnvPrefix(spec.Name), index, spec.Provider, index)
	}
	if options.Validator == "playground" {
		source.WriteString("validator, err := playground.New()\nif err != nil { return nil, boot.Options{}, err }\n")
	}
	if options.Platform == "echo" && len(options.Events) == 0 && options.Validator != "playground" && options.WebSocketPlatform != "gorilla" {
		source.WriteString("app := spine.New(echoplatform.Option())\n")
	} else {
		source.WriteString("app := spine.New(\n")
		if options.Platform == "echo" {
			source.WriteString("echoplatform.Option(),\n")
		}
		if options.WebSocketPlatform == "gorilla" {
			source.WriteString("gorillaplatform.Option(),\n")
		}
		for index, spec := range options.Events {
			fmt.Fprintf(&source, "spine.WithEventAdapter(%q, factory%d),\n", spec.Name, index)
		}
		if options.Validator == "playground" {
			source.WriteString("spine.WithValidator(validator),\n")
		}
		source.WriteString(")\n")
	}
	if options.Platform == "echo" {
		if options.Layout == layeredLayout {
			source.WriteString("app.Constructor(controller.NewHealthController)\nif err := route.Register(app); err != nil { return nil, boot.Options{}, err }\n")
		} else {
			source.WriteString("app.Constructor(NewHealthController)\napp.Route(\"GET\",\"/health\",(*HealthController).Get)\n")
		}
		source.WriteString("address := \":8080\"\nif port := os.Getenv(\"SPINE_PORT\"); port != \"\" { address = \":\"+port }\nreturn app, boot.Options{Address:address, HTTP:&boot.HTTPOptions{}, EnableGracefulShutdown:true}, nil\n")
	} else {
		source.WriteString("return app, boot.Options{}, nil\n")
	}
	source.WriteString("}\n")
	return source.String(), nil
}

func eventEnvPrefix(name string) string { return "SPINE_" + strings.ToUpper(name) + "_" }

func assemblyConfig(options newProjectOptions) string {
	providers := map[string]bool{}
	for _, spec := range options.Events {
		providers[spec.Provider] = true
	}
	imports := "\"fmt\"\n\"os\"\n\"strconv\"\n\"strings\"\n"
	if providers["kafka"] {
		imports += "kafkaplatform \"" + kafkaPlatformPath + "\"\n"
	}
	if providers["rabbitmq"] {
		imports += "rabbitmqplatform \"" + rabbitPlatformPath + "\"\n"
	}
	source := "package application\nimport (\n" + imports + ")\n" + generatedConfigHelpers
	if providers["kafka"] {
		source += generatedKafkaConfig
	}
	if providers["rabbitmq"] {
		source += generatedRabbitConfig
	}
	return source
}

const generatedConfigHelpers = `
// 오류에는 설정값과 인증 정보를 넣지 않고 설정 키만 표시합니다.
func requiredPlatformEnv(key string) (string, error) {
 value := strings.TrimSpace(os.Getenv(key))
 if value == "" { return "", fmt.Errorf("%s is required", key) }
 return value,nil
}
func platformInsecure(prefix string) (bool,error) {
 value := os.Getenv(prefix+"ALLOW_INSECURE")
 if value == "" { return false,nil }
 enabled,err := strconv.ParseBool(value)
 if err != nil { return false,fmt.Errorf("%sALLOW_INSECURE must be a boolean",prefix) }
 return enabled,nil
}
`
const generatedKafkaConfig = `
func loadKafkaConfig(prefix string) (kafkaplatform.Config,error) {
 config := kafkaplatform.Config{Consume:true,Publish:true}
 brokers,err := requiredPlatformEnv(prefix+"BROKERS"); if err != nil {return config,err}
 for _,broker := range strings.Split(brokers,",") {
  broker = strings.TrimSpace(broker)
  if broker == "" { return config,fmt.Errorf("%sBROKERS contains an empty address",prefix) }
  config.Brokers = append(config.Brokers,broker)
 }
 config.GroupID,err = requiredPlatformEnv(prefix+"GROUP_ID"); if err != nil {return config,err}
 config.TopicPrefix = os.Getenv(prefix+"TOPIC_PREFIX")
 config.AllowInsecureTransport,err = platformInsecure(prefix)
 return config,err
}
`
const generatedRabbitConfig = `
func loadRabbitmqConfig(prefix string) (rabbitmqplatform.Config,error) {
 config := rabbitmqplatform.Config{Consume:true,Publish:true,FailurePolicy:rabbitmqplatform.FailureReject}
 var err error
 config.URL,err = requiredPlatformEnv(prefix+"URL"); if err != nil {return config,err}
 config.Exchange,err = requiredPlatformEnv(prefix+"EXCHANGE"); if err != nil {return config,err}
 config.QueuePrefix = os.Getenv(prefix+"QUEUE_PREFIX")
 config.TopicPrefix = os.Getenv(prefix+"TOPIC_PREFIX")
 config.AllowInsecureTransport,err = platformInsecure(prefix)
 return config,err
}
`

func assemblyReadme(options newProjectOptions) string {
	var source strings.Builder
	fmt.Fprintf(&source, "# Spine application\n\n모듈: %s\n\nGo %s 이상이 필요합니다. 레시피 `%s`는 코어 `%s`와 플랫폼 `%s`의 공개 Preview 태그를 사용합니다.\n\n", options.Module, assemblyGoVersion(options.Validator), currentAssemblyRecipe, currentAssemblySpineVersion, previewPlatformVersion)
	source.WriteString("기본 생성은 의존성 설치, 정적 계약 검사, 빌드를 수행합니다. `--skip-install`은 파일만 생성하므로 먼저 `go mod tidy`를 실행하세요. 생성 과정에서 앱과 factory는 실행하지 않습니다.\n\n")
	if len(options.Events) > 0 {
		source.WriteString("## 이벤트 설정\n\n실행과 `go test` 전에 다음 환경 변수를 설정하세요. 주소와 자격 증명은 소스에 저장하지 않습니다. 기본 전송은 TLS이며 로컬 평문 브로커에만 `ALLOW_INSECURE=true`를 명시합니다.\n\n")
		for _, spec := range options.Events {
			prefix := eventEnvPrefix(spec.Name)
			fmt.Fprintf(&source, "- `%s` (%s): ", spec.Name, spec.Provider)
			if spec.Provider == "kafka" {
				fmt.Fprintf(&source, "`%sBROKERS`, `%sGROUP_ID`; 선택 `%sTOPIC_PREFIX`, `%sALLOW_INSECURE`.\n", prefix, prefix, prefix, prefix)
			} else {
				fmt.Fprintf(&source, "`%sURL`, `%sEXCHANGE`; 선택 `%sQUEUE_PREFIX`, `%sTOPIC_PREFIX`, `%sALLOW_INSECURE`. 실패 정책은 reject입니다.\n", prefix, prefix, prefix, prefix, prefix)
			}
		}
		source.WriteString("\n`BuildApplication`은 설정과 factory 생성 오류를 반환합니다. consumer는 `app.Consumers().Register`로 등록합니다. 등록된 이벤트 factory는 같은 consumer registry를 사용하므로 복수 플랫폼에서는 각 플랫폼의 topic, prefix와 중복 처리 정책을 함께 설계하세요. 생성만으로 consumer나 topic, queue를 만들지는 않습니다.\n")
	}
	if options.WebSocketPlatform == "gorilla" {
		source.WriteString("\nWebSocket 구현은 gorillaplatform.Option()으로 선택했습니다. app.WebSocket().Register로 경로를 등록하고 boot.HTTPOptions.WebSocket으로 정책을 설정합니다.\n")
	}
	if options.Validator == "playground" {
		source.WriteString("\n검증기는 `playground.New()`와 `spine.WithValidator`로 연결됩니다. DTO에 `validate` 태그를 추가하세요. 검증기는 핸들러 전 HTTP, WS, consumer 입력을 검사합니다.\n")
	} else {
		source.WriteString("\n검증기를 생략했습니다. DTO의 `validate` 태그는 자동 검사되지 않습니다.\n")
	}
	source.WriteString("\n## 실행과 검사\n\n```sh\nspine contract check\nspine verify\nspine dev\n```\n")
	if options.Platform == "echo" {
		source.WriteString("\n`GET /health`를 제공합니다. `SPINE_PORT` 또는 CLI의 `spine.toml` port로 포트를 정합니다. Echo native 설정은 `echoplatform.Option(echoplatform.WithEcho(func(*echo.Echo) { ... }))`에 추가합니다.\n")
	}
	source.WriteString("\n`spine update --dry-run --json`으로 의존성 변경을 검토하고 `--if-match`로 적용합니다. update는 앱 소스를 바꾸지 않습니다. 정적 검사와 build/test는 실제 브로커 연결, ACK/NACK, 배포 검증을 뜻하지 않습니다.\n")
	if options.Layout == layeredLayout && options.Platform == "echo" {
		source.WriteString(strings.ReplaceAll(generatedFirstAPIGuide(), "{{MODULE}}", options.Module))
	}
	return source.String()
}

func assemblyConfigTest(options newProjectOptions) string {
	setup := ""
	for _, spec := range options.Events {
		prefix := eventEnvPrefix(spec.Name)
		if spec.Provider == "kafka" {
			setup += fmt.Sprintf("t.Setenv(%q, %q)\nt.Setenv(%q, %q)\n", prefix+"BROKERS", "127.0.0.1:9092", prefix+"GROUP_ID", "generated-test")
		} else {
			setup += fmt.Sprintf("t.Setenv(%q, %q)\nt.Setenv(%q, %q)\n", prefix+"URL", "amqp://guest:guest@127.0.0.1:5672/", prefix+"EXCHANGE", "generated-test")
		}
		setup += fmt.Sprintf("t.Setenv(%q, %q)\n", prefix+"ALLOW_INSECURE", "true")
	}
	source := "package application\nimport \"testing\"\nfunc TestPlatformConfiguration(t *testing.T) {\n" + setup
	source += "app, options, err := BuildApplication()\nif err != nil {t.Fatal(err)}\nif err := app.Validate(options); err != nil {t.Fatal(err)}\n"
	for _, spec := range options.Events {
		prefix := eventEnvPrefix(spec.Name)
		key, value := prefix+"BROKERS", "127.0.0.1:9092"
		if spec.Provider == "rabbitmq" {
			key, value = prefix+"URL", "amqp://guest:guest@127.0.0.1:5672/"
		}
		for _, invalid := range []struct{ key, value, restore string }{{key, "", value}, {prefix + "ALLOW_INSECURE", "invalid", "true"}} {
			source += fmt.Sprintf("t.Setenv(%q,%q)\nif app, _, err := BuildApplication(); err == nil || app != nil {t.Fatal(\"invalid platform configuration was accepted\")}\nt.Setenv(%q,%q)\n", invalid.key, invalid.value, invalid.key, invalid.restore)
		}
		if spec.Provider == "rabbitmq" {
			source += fmt.Sprintf("t.Setenv(%q,\"invalid-url\")\nif app, _, err := BuildApplication(); err == nil || app != nil {t.Fatal(\"factory error was not returned\")}\nt.Setenv(%q,%q)\n", key, key, value)
		}
	}
	return source + "}\n"
}
