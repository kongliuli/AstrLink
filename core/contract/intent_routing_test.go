package contract

import "testing"

func TestIntentRoutingValidate(t *testing.T) {
	t.Parallel()
	if err := (IntentRouting{Enabled: true, Targets: map[string]string{"coding": "gpt-x"}, Fallback: "gpt-x"}).Validate(); err != nil {
		t.Fatalf("valid routing rejected: %v", err)
	}
	if err := (IntentRouting{Enabled: true, Targets: map[string]string{}, Fallback: ""}).Validate(); err == nil {
		t.Fatal("enabled without fallback accepted")
	}
	if err := (IntentRouting{Enabled: true, Targets: map[string]string{"coding": AstrLinkAutoModelID}, Fallback: "gpt-x"}).Validate(); err == nil {
		t.Fatal("auto target accepted")
	}
	if err := (IntentRouting{Enabled: false, Targets: map[string]string{"nope": "gpt-x"}, Fallback: "gpt-x"}).Validate(); err == nil {
		t.Fatal("unknown category accepted")
	}
}

func TestIntentRoutingResolve(t *testing.T) {
	t.Parallel()
	routing := IntentRouting{
		Enabled:  true,
		Targets:  map[string]string{"coding": "code-model"},
		Fallback: "fallback-model",
	}
	if got := routing.ResolveIntentModel("coding"); got != "code-model" {
		t.Fatalf("got %q", got)
	}
	if got := routing.ResolveIntentModel("research"); got != "fallback-model" {
		t.Fatalf("got %q", got)
	}
}
