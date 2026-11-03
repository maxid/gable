package validate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/maxid/gable/validate"
)

type signup struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
	Role     string `json:"role" validate:"required,oneof=admin member"`
	Age      int    `json:"age" validate:"gte=18,max=120"`
}

// Messages name the field the client sent (`json`), not the Go field, and every failing
// field is reported instead of only the first.
func TestStructReportsEveryFailingField(t *testing.T) {
	validator := validate.New()
	issues := validator.Struct(signup{Email: "not-an-email", Role: "root", Age: 12})

	if len(issues) != 4 {
		t.Fatalf("issues = %+v", issues)
	}
	byField := map[string]validate.Issue{}
	for _, issue := range issues {
		byField[issue.Field] = issue
	}
	if byField["email"].Rule != "email" {
		t.Fatalf("email = %+v", byField["email"])
	}
	if byField["role"].Rule != "oneof" || byField["role"].Param != "admin member" {
		t.Fatalf("role = %+v", byField["role"])
	}
	if byField["age"].Rule != "gte" {
		t.Fatalf("age = %+v", byField["age"])
	}
	if byField["password"].Rule != "required" {
		t.Fatalf("password = %+v", byField["password"])
	}
	if byField["role"].Message != "must be one of admin member" {
		t.Fatalf("role message = %q", byField["role"].Message)
	}

	// The error text is a single line a log can carry.
	if text := issues.Error(); !strings.Contains(text, "email: must be a valid email address") {
		t.Fatalf("error = %q", text)
	}
	// The envelope data is a list, not a map: order is preserved and a field can appear
	// once per rule.
	fields := issues.Fields()
	if len(fields) != 4 || fields[0]["field"] != "email" {
		t.Fatalf("fields = %+v", fields)
	}
}

func TestStructAcceptsValidInputAndPointers(t *testing.T) {
	validator := validate.New()
	entity := &signup{Email: "a@b.co", Password: "12345678", Role: "member", Age: 30}
	if issues := validator.Struct(entity); issues != nil {
		t.Fatalf("issues = %+v", issues)
	}
	if issues := validator.Struct(nil); len(issues) != 1 || issues[0].Rule != "required" {
		t.Fatalf("nil = %+v", issues)
	}
	// A non-struct cannot be validated; that is a programming error, and reporting it as
	// a field issue beats a panic in a handler.
	if issues := validator.Struct("not a struct"); len(issues) != 1 || issues[0].Field != "body" {
		t.Fatalf("scalar = %+v", issues)
	}
}

// A custom rule is registered with its message once and then used like a built-in.
func TestRegisterCustomRule(t *testing.T) {
	validator := validate.New()
	if err := validator.Register("slug", "must be a lowercase slug", func(value any) bool {
		text, ok := value.(string)
		if !ok || text == "" {
			return false
		}
		for _, r := range text {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return false
			}
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	entity := struct {
		Name string `json:"name" validate:"slug"`
	}{Name: "Not A Slug"}
	issues := validator.Struct(entity)
	if len(issues) != 1 || issues[0].Message != "must be a lowercase slug" {
		t.Fatalf("issues = %+v", issues)
	}
}

// A translator sees the rule and the parameter, so a bundle can phrase the message.
func TestTranslatorLocalisesMessages(t *testing.T) {
	validator := validate.New().WithTranslator(translator{})
	// Password is present but too short, so the min rule (not required) fires.
	issues := validator.StructContext(context.Background(), signup{Email: "a@b.co", Password: "short", Role: "member", Age: 30})
	seen := map[string]string{}
	for _, issue := range issues {
		seen[issue.Field] = issue.Message
	}
	if seen["password"] != "zh:min:8" {
		t.Fatalf("messages = %v", seen)
	}
	if len(seen) != 1 {
		t.Fatalf("messages = %v", seen)
	}
}

type translator struct{}

func (translator) Translate(_ context.Context, key string, params map[string]string) string {
	return "zh:" + key + ":" + params["param"]
}

// Overriding a message keeps the parameter formatting.
func TestSetMessageOverride(t *testing.T) {
	validator := validate.New().SetMessage("min", "至少 %s 个字符")
	issues := validator.Struct(signup{Email: "a@b.co", Password: "short", Role: "member", Age: 30})
	if len(issues) != 1 || issues[0].Message != "至少 8 个字符" {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestDescribeFallsBackForUnknownRule(t *testing.T) {
	if got := validate.Describe("nonexistent", ""); got != "is invalid" {
		t.Fatalf("describe = %q", got)
	}
	if got := validate.Describe("max", "10"); got != "must be at most 10" {
		t.Fatalf("describe = %q", got)
	}
}
