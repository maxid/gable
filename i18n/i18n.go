// Package i18n loads message bundles and resolves a message for a language.
//
// It exists because validation, errors and business messages all need the same three
// behaviours and each module implementing them is how a product ends up with three
// fallback rules: a lookup with a default language, a region fallback ("zh-CN" answers from
// "zh"), and placeholder substitution.
package i18n

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Bundle is a set of messages per language.
//
// The zero value is usable and starts empty, which keeps tests short; production code adds
// languages with Load.
type Bundle struct {
	mu       sync.RWMutex
	messages map[string]map[string]string
	fallback string
}

// New returns a bundle whose fallback language is fallback.
func New(fallback string) *Bundle {
	bundle := &Bundle{messages: map[string]map[string]string{}}
	bundle.SetFallback(fallback)
	return bundle
}

// SetFallback sets the language used when a key is missing everywhere else.
func (b *Bundle) SetFallback(language string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fallback = normalize(language)
}

// Fallback returns the fallback language.
func (b *Bundle) Fallback() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.fallback
}

// Load parses a flat YAML mapping of key to message for one language:
//
//	zh-CN:
//	  required: 必填
//
// A nested document is accepted too (language at the top), so one file can hold every
// language and still be loaded per language.
func (b *Bundle) Load(language string, data []byte) error {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("i18n: parse %s: %w", language, err)
	}
	normalized := normalize(language)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.messages == nil {
		b.messages = map[string]map[string]string{}
	}
	if nested, ok := raw[normalized].(map[string]any); ok {
		raw = nested
	}
	messages := b.messages[normalized]
	if messages == nil {
		messages = map[string]string{}
		b.messages[normalized] = messages
	}
	for key, value := range raw {
		text, ok := value.(string)
		if !ok {
			continue
		}
		messages[key] = text
	}
	return nil
}

// Set adds or replaces one message.
func (b *Bundle) Set(language, key, message string) {
	normalized := normalize(language)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.messages == nil {
		b.messages = map[string]map[string]string{}
	}
	if b.messages[normalized] == nil {
		b.messages[normalized] = map[string]string{}
	}
	b.messages[normalized][key] = message
}

// Languages lists the loaded languages, sorted.
func (b *Bundle) Languages() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	languages := make([]string, 0, len(b.messages))
	for language := range b.messages {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}

// Has reports whether a key resolves in a language, ignoring the fallback.
func (b *Bundle) Has(language, key string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.lookup(language, key)
	return ok
}

// Translate resolves key for language, substituting {name} placeholders.
//
// Resolution order: the language itself, its base language ("zh-CN" → "zh"), the fallback,
// then the key itself — a missing message shows the key, so a gap is visible instead of
// rendering an empty string.
func (b *Bundle) Translate(language, key string, params map[string]string) string {
	b.mu.RLock()
	message, ok := b.lookup(language, key)
	b.mu.RUnlock()
	if !ok {
		return key
	}
	return substitute(message, params)
}

// lookup walks the fallback chain. The caller holds the lock.
func (b *Bundle) lookup(language, key string) (string, bool) {
	candidates := []string{normalize(language)}
	if base := baseLanguage(language); base != candidates[0] {
		candidates = append(candidates, base)
	}
	if b.fallback != "" {
		candidates = append(candidates, b.fallback)
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if message, ok := b.messages[candidate][key]; ok {
			return message, true
		}
	}
	return "", false
}

// substitute replaces {name} with its value. An unknown placeholder is left alone: a
// half-rendered message that still shows {name} is easier to debug than one with a blank.
func substitute(message string, params map[string]string) string {
	if len(params) == 0 || !strings.Contains(message, "{") {
		return message
	}
	var builder strings.Builder
	remaining := message
	for {
		open := strings.Index(remaining, "{")
		if open < 0 {
			builder.WriteString(remaining)
			break
		}
		close := strings.Index(remaining[open:], "}")
		if close < 0 {
			builder.WriteString(remaining)
			break
		}
		builder.WriteString(remaining[:open])
		name := remaining[open+1 : open+close]
		if value, ok := params[name]; ok {
			builder.WriteString(value)
		} else {
			builder.WriteString(remaining[open : open+close+1])
		}
		remaining = remaining[open+close+1:]
	}
	return builder.String()
}

// normalize compares languages case-insensitively and treats "_" like "-", because both
// spellings arrive from HTTP headers and configuration files.
func normalize(language string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(language), "_", "-"))
}

// baseLanguage drops the region: "zh-cn" becomes "zh".
func baseLanguage(language string) string {
	normalized := normalize(language)
	if dash := strings.Index(normalized, "-"); dash > 0 {
		return normalized[:dash]
	}
	return normalized
}
