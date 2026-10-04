package contract

import (
	"fmt"
	"strings"
)

// IntentTaxonomyLabels is the frozen astrlink-text-v1 order. Keep aligned with
// core/internal/autotaxonomy.Labels and the classifier worker.
var IntentTaxonomyLabels = []string{"general", "research", "coding", "architect"}

// IntentRouting maps astrlink-text-v1 categories to concrete models when the
// client requests AstrLinkAutoModelID. Disabled (or nil) keeps astrlink/auto
// retired at the inference boundary.
type IntentRouting struct {
	Enabled  bool              `json:"enabled"`
	Targets  map[string]string `json:"targets"`
	Fallback string            `json:"fallback"`
}

func (routing *IntentRouting) UnmarshalJSON(data []byte) error {
	type document IntentRouting
	var value document
	if err := decodePolicy(data, &value, []string{"enabled", "targets", "fallback"}, nil); err != nil {
		return err
	}
	if err := IntentRouting(value).Validate(); err != nil {
		return err
	}
	*routing = IntentRouting(value)
	return nil
}

func (routing IntentRouting) Validate() error {
	if routing.Targets == nil {
		routing.Targets = map[string]string{}
	}
	for category, model := range routing.Targets {
		if !IsIntentCategory(category) {
			return fmt.Errorf("intent_routing targets key %q is not an astrlink-text-v1 label", category)
		}
		if err := validateRedirectModel("intent_routing target", model); err != nil {
			return err
		}
		if model == AstrLinkAutoModelID {
			return fmt.Errorf("intent_routing target for %q must not be %s", category, AstrLinkAutoModelID)
		}
	}
	if routing.Enabled {
		if strings.TrimSpace(routing.Fallback) == "" {
			return fmt.Errorf("intent_routing fallback is required when enabled")
		}
	}
	if routing.Fallback != "" {
		if err := validateRedirectModel("intent_routing fallback", routing.Fallback); err != nil {
			return err
		}
		if routing.Fallback == AstrLinkAutoModelID {
			return fmt.Errorf("intent_routing fallback must not be %s", AstrLinkAutoModelID)
		}
	}
	return nil
}

// ResolveIntentModel picks the model for a classified category. Missing or
// blank targets fall through to Fallback.
func (routing IntentRouting) ResolveIntentModel(category string) string {
	if model := strings.TrimSpace(routing.Targets[category]); model != "" {
		return model
	}
	return strings.TrimSpace(routing.Fallback)
}

func IsIntentCategory(category string) bool {
	for _, label := range IntentTaxonomyLabels {
		if category == label {
			return true
		}
	}
	return false
}
