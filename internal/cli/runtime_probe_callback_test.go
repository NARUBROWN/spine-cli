package cli

import "testing"

func TestRuntimeProbeSeparatesUnobservedCallbacksFromMissingRegistrations(t *testing.T) {
	static := applicationContract{Complete: true, WebSockets: []namedRegistration{
		{Name: "/callback", Handler: "(*Chat).RegisterRoutes[WS /callback]", controllerCallback: true},
		{Name: "/legacy", Handler: "(*Chat).Handle"},
	}}
	observed := runtimeProbePayload{WebSockets: []runtimeProbeRegistration{{Name: "/legacy", Handler: "(*Chat).Handle"}}}
	report := compareRuntimeProbe(static, observed)
	if !report.MatchesStatic || report.RegistrationParityComplete || len(report.UnobservedWebSockets) != 1 || len(report.WebSockets) != 1 {
		t.Fatalf("incorrect evidence boundary: %+v", report)
	}
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Code != "WEBSOCKET_RUNTIME_REGISTRATION_UNOBSERVABLE" || report.Diagnostics[0].Severity != "warning" {
		t.Fatalf("callback limitation was not disclosed: %+v", report.Diagnostics)
	}
	missing := compareRuntimeProbe(static, runtimeProbePayload{})
	if missing.MatchesStatic {
		t.Fatal("unobserved callback hid a missing method registration")
	}
	wrong := compareRuntimeProbe(static, runtimeProbePayload{WebSockets: []runtimeProbeRegistration{{Name: "/legacy", Handler: "(*Other).Handle"}}})
	if wrong.MatchesStatic {
		t.Fatal("unobserved callback hid a changed runtime handler")
	}
	complete := compareRuntimeProbe(applicationContract{Complete: true}, runtimeProbePayload{})
	if !complete.RegistrationParityComplete || complete.UnobservedWebSockets == nil {
		t.Fatalf("empty observable registries should have complete parity: %+v", complete)
	}
}
