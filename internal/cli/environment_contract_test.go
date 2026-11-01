package cli

import (
	"os"
	"strings"
	"testing"
)

const environmentContractSource = `package application
import (
	environment "os"
	"github.com/NARUBROWN/spine"
)
func BuildApplication() spine.App {
	app := spine.New()
	_ = app.Consumers().Register(environment.Getenv("SPINE_TEST_TOPIC_PREFIX")+"order.created", (*Controller).Consume)
	if environment.Getenv("SPINE_TEST_SCENARIOS") == "1" {
		app.Route("GET", "/scenario", (*Controller).Get)
	} else {
		app.Route("GET", "/normal", (*Controller).Get)
	}
	return app
}
type Controller struct{}
func (*Controller) Get() {}
func (*Controller) Consume() error { return nil }
`

func writeEnvironmentContract(t *testing.T, source string) {
	if err := prepareTestModuleCache(); err != nil {
		t.Fatal(err)
	}
	t.Helper()
	for path, contents := range map[string]string{
		"go.mod":         "module example.com/environment\n\ngo 1.25.5\n\nrequire github.com/NARUBROWN/spine v0.5.2\n",
		"spine.toml":     "module = \"example.com/environment\"\nentrypoint = \".\"\ncomposition = \".\"\nport = 8080\n",
		"application.go": source,
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnvironmentRegistrationsFollowExplicitInputs(t *testing.T) {
	inTempDir(t)
	writeEnvironmentContract(t, environmentContractSource)
	for _, scenario := range []struct{ prefix, enabled, path string }{
		{"", "", "/normal"},
		{"test.", "1", "/scenario"},
	} {
		t.Setenv("SPINE_TEST_TOPIC_PREFIX", scenario.prefix)
		t.Setenv("SPINE_TEST_SCENARIOS", scenario.enabled)
		contract, err := inspectApplication()
		if err != nil {
			t.Fatal(err)
		}
		if !contract.Complete || contractHasErrors(contract) || len(contract.HTTP) != 1 || contract.HTTP[0].Path != scenario.path || len(contract.Consumers) != 1 || contract.Consumers[0].Name != scenario.prefix+"order.created" {
			t.Fatalf("environment contract mismatch: %#v", contract)
		}
	}
}

func TestEnvironmentConditionsDoNotHideUnknownOuterBranch(t *testing.T) {
	inTempDir(t)
	t.Setenv("SPINE_TEST_SCENARIOS", "1")
	source := strings.Replace(environmentContractSource, `if environment.Getenv("SPINE_TEST_SCENARIOS") == "1" {`, `if unknownCondition() { if environment.Getenv("SPINE_TEST_SCENARIOS") == "1" {`, 1)
	source = strings.Replace(source, "\treturn app", "\t}\n\treturn app", 1)
	writeEnvironmentContract(t, source)
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range contract.Diagnostics {
		if diagnostic.Code == "REGISTRATION_CONDITIONAL" && !contract.Complete {
			return
		}
	}
	t.Fatalf("unknown outer branch was accepted: %#v", contract)
}

func TestEnvironmentRegistrationRejectsShadowedPackageAndArbitraryCall(t *testing.T) {
	for _, scenario := range []struct{ name, source string }{
		{"shadowed package", strings.Replace(environmentContractSource, "app := spine.New()", "app := spine.New(); environment := customEnvironment{}", 1)},
		{"arbitrary function", strings.Replace(environmentContractSource, `environment.Getenv("SPINE_TEST_TOPIC_PREFIX")`, `arbitraryFunction()`, 1)},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			inTempDir(t)
			writeEnvironmentContract(t, scenario.source)
			contract, err := inspectApplication()
			if err != nil {
				t.Fatal(err)
			}
			for _, diagnostic := range contract.Diagnostics {
				if diagnostic.Code == "CONSUMER_REGISTRATION_DYNAMIC" && !contract.Complete {
					return
				}
			}
			t.Fatalf("dynamic registration was accepted: %#v", contract)
		})
	}
}

func TestEnvironmentMutationFailsCompleteness(t *testing.T) {
	inTempDir(t)
	source := strings.Replace(environmentContractSource, "app := spine.New()", `app := spine.New(); environment.Setenv("SPINE_TEST_SCENARIOS", "1")`, 1)
	writeEnvironmentContract(t, source)
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range contract.Diagnostics {
		if diagnostic.Code == "ENVIRONMENT_MUTATION_UNRESOLVED" && !contract.Complete {
			return
		}
	}
	t.Fatalf("environment mutation was accepted: %#v", contract)
}

func TestNamedRegistryHandlersResolveAcrossPackages(t *testing.T) {
	inTempDir(t)
	source := `package application
import (
	"github.com/NARUBROWN/spine"
	handlers "example.com/environment/handlers"
)
func BuildApplication() spine.App {
	app := spine.New()
	_ = app.Consumers().Register("order.created", (*handlers.Controller).Consume)
	_ = app.WebSocket().Register("/ws/chat", (*handlers.Controller).Get)
	return app
}
`
	writeEnvironmentContract(t, source)
	if err := os.Mkdir("handlers", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("handlers/controller.go", []byte("package handlers\ntype Controller struct{}\nfunc (*Controller) Consume() error { return nil }\nfunc (*Controller) Get() error { return nil }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	contract, err := inspectApplication()
	if err != nil {
		t.Fatal(err)
	}
	if !contract.Complete || contractHasErrors(contract) || contract.Consumers[0].Handler != "(*Controller).Consume" || contract.WebSockets[0].Handler != "(*Controller).Get" {
		t.Fatalf("imported handlers were not resolved: %#v", contract)
	}
}
