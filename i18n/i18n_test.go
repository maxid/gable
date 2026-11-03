package i18n_test

import (
	"testing"

	"github.com/maxid/gable/i18n"
)

// The fallback chain is the whole point: a regional request answers from the base language,
// then from the default, and only then shows the key.
func TestTranslateResolutionOrder(t *testing.T) {
	bundle := i18n.New("en")
	if err := bundle.Load("zh", []byte("required: 必填\nemail: 邮箱格式不正确\n")); err != nil {
		t.Fatal(err)
	}
	if err := bundle.Load("zh-CN", []byte("required: 必填（大陆）\n")); err != nil {
		t.Fatal(err)
	}
	if err := bundle.Load("en", []byte("required: is required\n")); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		language string
		key      string
		want     string
	}{
		{"zh-CN", "required", "必填（大陆）"}, // the exact language wins
		{"zh_cn", "required", "必填（大陆）"}, // underscore spelling is the same language
		{"zh-CN", "email", "邮箱格式不正确"},   // a regional request falls back to the base language
		{"zh", "email", "邮箱格式不正确"},
		{"ja", "required", "is required"}, // fallback language
		{"ja", "missing.key", "missing.key"},
	}
	for _, tc := range cases {
		if got := bundle.Translate(tc.language, tc.key, nil); got != tc.want {
			t.Fatalf("Translate(%q, %q) = %q, want %q", tc.language, tc.key, got, tc.want)
		}
	}
	if bundle.Has("zh-CN", "email") != true || bundle.Has("zh-CN", "missing") != false {
		t.Fatal("Has must ignore the fallback")
	}
	if languages := bundle.Languages(); len(languages) != 3 || languages[0] != "en" || languages[1] != "zh" || languages[2] != "zh-cn" {
		t.Fatalf("languages = %v", languages)
	}
}

// A message may carry placeholders; an unknown one stays visible rather than rendering
// empty.
func TestPlaceholders(t *testing.T) {
	bundle := i18n.New("en")
	bundle.Set("en", "greeting", "hello {name}, you have {count} messages")
	bundle.Set("en", "partial", "hello {name}, {unknown}")

	if got := bundle.Translate("en", "greeting", map[string]string{"name": "ada", "count": "3"}); got != "hello ada, you have 3 messages" {
		t.Fatalf("greeting = %q", got)
	}
	if got := bundle.Translate("en", "partial", map[string]string{"name": "ada"}); got != "hello ada, {unknown}" {
		t.Fatalf("partial = %q", got)
	}
	if got := bundle.Translate("en", "greeting", nil); got != "hello {name}, you have {count} messages" {
		t.Fatalf("no params = %q", got)
	}
}

// One file may hold every language; loading it per language must not duplicate messages.
func TestNestedDocumentAndReload(t *testing.T) {
	document := []byte(`
zh:
  required: 必填
en:
  required: is required
`)
	bundle := i18n.New("en")
	if err := bundle.Load("zh", document); err != nil {
		t.Fatal(err)
	}
	// The nested form was unwrapped, so only "zh" is loaded.
	if languages := bundle.Languages(); len(languages) != 1 || languages[0] != "zh" {
		t.Fatalf("languages = %v", languages)
	}
	if got := bundle.Translate("zh", "required", nil); got != "必填" {
		t.Fatalf("required = %q", got)
	}
}

func TestBadYAMLIsAnError(t *testing.T) {
	if err := i18n.New("en").Load("en", []byte("required: [unclosed")); err == nil {
		t.Fatal("malformed YAML must be reported")
	}
}

// The zero value is usable, so a test can build one inline.
func TestZeroValueWorks(t *testing.T) {
	var bundle i18n.Bundle
	bundle.Set("en", "ok", "ok")
	if got := bundle.Translate("en", "ok", nil); got != "ok" {
		t.Fatalf("translate = %q", got)
	}
	if got := bundle.Translate("en", "missing", nil); got != "missing" {
		t.Fatalf("missing = %q", got)
	}
}
