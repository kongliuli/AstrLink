package ingress

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestResolveIntentModelClassifies(t *testing.T) {
	t.Parallel()
	handler := NewWithDependencies(Dependencies{
		Classifier: ClassifierFunc(func(context.Context, string) autoclassifier.Outcome {
			return autoclassifier.Outcome{Category: "coding"}
		}),
	})
	session := &recordSession{}
	classified := Request{
		Model:        contract.AstrLinkAutoModelID,
		Protocol:     contract.ProtocolOpenAIChat,
		lastUserText: "fix this unit test",
	}
	settings := contract.RoutingSettings{
		IntentRouting: &contract.IntentRouting{
			Enabled:  true,
			Targets:  map[string]string{"coding": "code-model"},
			Fallback: "fallback-model",
		},
	}
	resolved, err := handler.resolveIntentModel(context.Background(), session, classified, settings)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.RedirectedModel != "code-model" {
		t.Fatalf("got %q", resolved.RedirectedModel)
	}
	if session.modelRedirect == nil || session.modelRedirect.To != "code-model" {
		t.Fatalf("redirect not recorded: %+v", session.modelRedirect)
	}
	if len(session.events) == 0 || !strings.Contains(session.events[len(session.events)-1].Summary, "coding") {
		t.Fatalf("summary missing category: %+v", session.events)
	}
}

func TestResolveIntentModelFallbackWhenClassifierMissing(t *testing.T) {
	t.Parallel()
	handler := NewWithDependencies(Dependencies{})
	session := &recordSession{}
	classified := Request{Model: contract.AstrLinkAutoModelID, lastUserText: "hello"}
	settings := contract.RoutingSettings{
		IntentRouting: &contract.IntentRouting{Enabled: true, Fallback: "fallback-model"},
	}
	resolved, err := handler.resolveIntentModel(context.Background(), session, classified, settings)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.RedirectedModel != "fallback-model" {
		t.Fatalf("got %q", resolved.RedirectedModel)
	}
}

func TestResolveIntentModelDisabled(t *testing.T) {
	t.Parallel()
	handler := NewWithDependencies(Dependencies{})
	_, err := handler.resolveIntentModel(
		context.Background(),
		&recordSession{},
		Request{Model: contract.AstrLinkAutoModelID},
		contract.RoutingSettings{},
	)
	if err != errIntentRoutingDisabled {
		t.Fatalf("got %v", err)
	}
}

func TestResolveIntentModelStickyAffinity(t *testing.T) {
	t.Parallel()
	handler := NewWithDependencies(Dependencies{
		Classifier: ClassifierFunc(func(context.Context, string) autoclassifier.Outcome {
			t.Fatal("classifier should not run when sticky")
			return autoclassifier.Outcome{}
		}),
	})
	handler.affinities.entries = map[affinityKey]affinityEntry{
		{principal: "token_a", responseID: "resp_1"}: {
			binding: storage.ResponseAffinity{UpstreamModel: "sticky-model"},
		},
	}
	ctx := context.WithValue(context.Background(), accessTokenIDContextKey{}, contract.AccessTokenID("token_a"))
	session := &recordSession{}
	classified := Request{
		Model:              contract.AstrLinkAutoModelID,
		Protocol:           contract.ProtocolOpenAIResponses,
		PreviousResponseID: "resp_1",
		lastUserText:       "now write a poem",
	}
	settings := contract.RoutingSettings{
		IntentRouting: &contract.IntentRouting{Enabled: true, Fallback: "fallback-model"},
	}
	resolved, err := handler.resolveIntentModel(ctx, session, classified, settings)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.RedirectedModel != "sticky-model" {
		t.Fatalf("got %q", resolved.RedirectedModel)
	}
}

func TestIntentRoutingDiscoveryListsAuto(t *testing.T) {
	t.Parallel()
	entries, err := synthesizeDiscoveryEntries(contract.ProtocolOpenAIModels, []string{contract.AstrLinkAutoModelID})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].id != contract.AstrLinkAutoModelID {
		t.Fatalf("got %+v", entries)
	}
	var document map[string]any
	if err := json.Unmarshal(entries[0].raw, &document); err != nil {
		t.Fatal(err)
	}
	if document["id"] != contract.AstrLinkAutoModelID {
		t.Fatalf("got %#v", document)
	}
}
