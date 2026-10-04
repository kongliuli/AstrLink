package ingress

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/autotext"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

var errIntentRoutingDisabled = fmt.Errorf("intent routing is disabled")

// resolveIntentModel replaces astrlink/auto with a concrete model. Stickiness
// wins over a fresh classification: WebSocket pins and previous_response_id
// affinity reuse the first chosen upstream model so continuations stay coherent.
func (handler *Handler) resolveIntentModel(
	ctx context.Context,
	session *recordSession,
	classified Request,
	settings contract.RoutingSettings,
) (Request, error) {
	if classified.routingModel() != contract.AstrLinkAutoModelID {
		return classified, nil
	}
	routing := settings.IntentRouting
	if routing == nil || !routing.Enabled {
		return classified, errIntentRoutingDisabled
	}
	if sticky := handler.stickyIntentModel(ctx, classified); sticky != "" {
		session.noteModelRedirect(ctx, contract.AstrLinkAutoModelID, sticky)
		if n := len(session.events); n > 0 && session.events[n-1].Kind == contract.RequestEventModelRedirect {
			session.events[n-1].Summary = truncateRunes("intent sticky → "+sticky, contract.MaxEventSummaryRunes)
		}
		classified.RedirectedModel = sticky
		return classified, nil
	}

	category := ""
	fallbackReason := ""
	started := time.Now()
	switch {
	case handler.classifier == nil:
		fallbackReason = autoclassifier.FallbackUnavailable
	case autotext.IsBlank(classified.lastUserText):
		fallbackReason = autoclassifier.FallbackEmptyText
	default:
		outcome := handler.classifier.Classify(ctx, classified.lastUserText)
		category = outcome.Category
		fallbackReason = outcome.FallbackReason
	}
	target := routing.ResolveIntentModel(category)
	if target == "" {
		return classified, fmt.Errorf("intent_routing fallback is empty")
	}
	summary := fmt.Sprintf(
		"intent %s → %s · %dms",
		intentSummaryCategory(category, fallbackReason),
		target,
		time.Since(started).Milliseconds(),
	)
	if fallbackReason != "" {
		summary += " · " + fallbackReason
	}
	session.noteModelRedirect(ctx, contract.AstrLinkAutoModelID, target)
	if n := len(session.events); n > 0 && session.events[n-1].Kind == contract.RequestEventModelRedirect {
		session.events[n-1].Summary = truncateRunes(summary, contract.MaxEventSummaryRunes)
	}
	classified.RedirectedModel = target
	return classified, nil
}

func intentSummaryCategory(category, fallbackReason string) string {
	if category != "" {
		return category
	}
	if fallbackReason != "" {
		return "fallback"
	}
	return "unknown"
}

func (handler *Handler) stickyIntentModel(ctx context.Context, classified Request) string {
	if turn := responsesWSTurnFromContext(ctx); turn != nil && turn.session != nil {
		if model := turn.session.routingModel; model != "" && model != contract.AstrLinkAutoModelID {
			return model
		}
	}
	if classified.PreviousResponseID == "" {
		return ""
	}
	if classified.Protocol != contract.ProtocolOpenAIResponses &&
		classified.Protocol != contract.ProtocolOpenAIResponsesCompact {
		return ""
	}
	principal, _ := AccessTokenIDFromContext(ctx)
	key := affinityKey{string(principal), classified.PreviousResponseID}
	handler.affinities.mu.Lock()
	entry, found := handler.affinities.entries[key]
	handler.affinities.mu.Unlock()
	if found && entry.binding.UpstreamModel != "" && entry.binding.UpstreamModel != contract.AstrLinkAutoModelID {
		return entry.binding.UpstreamModel
	}
	store, ok := handler.requestRecords.(storage.ResponseAffinityStore)
	if !ok {
		return ""
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	binding, ok, err := store.GetResponseAffinity(lookupCtx, key.principal, key.responseID)
	if err == nil && ok && binding.UpstreamModel != "" && binding.UpstreamModel != contract.AstrLinkAutoModelID {
		return binding.UpstreamModel
	}
	return ""
}

func intentRoutingEnabled(settings contract.RoutingSettings) bool {
	return settings.IntentRouting != nil && settings.IntentRouting.Enabled
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
