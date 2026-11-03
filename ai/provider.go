package ai

import (
	"fmt"

	"github.com/maxid/gai"
	"github.com/maxid/gai/anthropic"
	"github.com/maxid/gai/openai"

	"github.com/maxid/gable/rpc"
)

// defaultProvider builds the configured backend.
//
// Only the OpenAI-compatible provider exists, and it is chosen explicitly: an unknown
// provider name must fail rather than quietly become "openai", because a misconfigured
// deployment would otherwise send traffic — and an API key — to an unintended host.
func defaultProvider(cfg Config) (gai.Provider, error) {
	name := cfg.Provider
	if name == "" {
		name = "openai"
	}

	switch name {
	case "openai":
		provider, err := openai.New(openai.Config{
			BaseURL:        cfg.BaseURL,
			APIKey:         cfg.APIKey,
			Model:          cfg.Model,
			RequestTimeout: cfg.RequestTimeout,
		})
		if err != nil {
			return nil, err
		}
		return provider, nil
	case "anthropic":
		provider, err := anthropic.New(anthropic.Config{
			BaseURL:        cfg.BaseURL,
			APIKey:         cfg.APIKey,
			Model:          cfg.Model,
			MaxTokens:      cfg.MaxTokens,
			RequestTimeout: cfg.RequestTimeout,
		})
		if err != nil {
			return nil, err
		}
		return provider, nil
	default:
		return nil, fmt.Errorf("%w: unknown ai provider %q", rpc.ErrPermanent, name)
	}
}
