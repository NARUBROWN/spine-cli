package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Generates actual preview adapter configuration without opening broker connections.
func TestPreviewEventDestinationConfiguration(t *testing.T) {
	binary := buildUpdateCLI(t)
	for _, provider := range []string{"kafka", "rabbitmq", "nats"} {
		t.Run(provider, func(t *testing.T) {
			workspace := t.TempDir()
			applyAssemblyPlan(t, binary, workspace, "new", "sample", "--module", "example.com/sample", "--platform", "none", "--event-platform", "events:"+provider, "--channel", "preview")
			root := filepath.Join(workspace, "sample")
			contents, err := os.ReadFile(filepath.Join(root, "internal/application/platform_config.go"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(contents), "PUBLISH_DESTINATIONS") {
				t.Fatal("preview destinations were not generated")
			}
			fixture := fmt.Sprintf(`package application
import("reflect";"testing")
func TestPreviewDestinations(t *testing.T){
 %s
 t.Setenv("SPINE_EVENTS_PUBLISH_DESTINATIONS","")
 config,err:=load%sConfig("SPINE_EVENTS_");if err!=nil{t.Fatal(err)}
 if config.Publish{t.Fatal("consumer-only configuration opened a publisher")}
 t.Setenv("SPINE_EVENTS_PUBLISH_DESTINATIONS"," order.created,order.updated ")
 config,err=load%sConfig("SPINE_EVENTS_");if err!=nil{t.Fatal(err)}
 if !config.Publish||!reflect.DeepEqual(config.PublishDestinations.PublishDestinations(),[]string{"order.created","order.updated"}){t.Fatal(config)}
 for _,invalid:=range []string{"order.created,",",order.created","order.created,,order.updated"}{
  t.Setenv("SPINE_EVENTS_PUBLISH_DESTINATIONS",invalid)
  if _,err:=load%sConfig("SPINE_EVENTS_");err==nil{t.Fatalf("empty destination accepted: %%q",invalid)}
 }
}
`, destinationTestEnvironment(provider), strings.ToUpper(provider[:1])+provider[1:], strings.ToUpper(provider[:1])+provider[1:], strings.ToUpper(provider[:1])+provider[1:])
			addGeneratedTest(t, root, "preview_destinations_test.go", fixture)
			generatedGo(t, root, "fmt", "./...")
			assertE2ESuccess(t, runE2E(binary, root, nil, "verify", "--warnings-as-errors", "--json"), "preview destination verify")
			t.Log(generatedGo(t, root, "test", "-race", "./...", "-count=1"))
		})
	}
}

func destinationTestEnvironment(provider string) string {
	switch provider {
	case "kafka":
		return `t.Setenv("SPINE_EVENTS_BROKERS","localhost:9092");t.Setenv("SPINE_EVENTS_GROUP_ID","test");t.Setenv("SPINE_EVENTS_ALLOW_INSECURE","true")`
	case "rabbitmq":
		return `t.Setenv("SPINE_EVENTS_EXCHANGE","test");t.Setenv("SPINE_EVENTS_URL","amqp://guest:guest@localhost:5672/");t.Setenv("SPINE_EVENTS_ALLOW_INSECURE","true")`
	default:
		return `t.Setenv("SPINE_EVENTS_URLS","nats://localhost:4222");t.Setenv("SPINE_EVENTS_STREAM","TEST");t.Setenv("SPINE_EVENTS_CONSUMER_PREFIX","test");t.Setenv("SPINE_EVENTS_ALLOW_INSECURE","true")`
	}
}
