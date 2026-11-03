package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

// newTestRouter wires the handlers the way an application does, including the protected
// route the cookie test calls.
func newTestRouter(handlers *Handlers) http.Handler {
	server := web.New(web.WithTitle("auth-test"))
	server.Register(handlers)
	server.Use(handlers.Middleware())
	server.Router().GET("/protected", func(ctx *web.Context) error {
		principal, ok := PrincipalFromContext(ctx.Request().Context())
		if !ok {
			return ErrMissingToken
		}
		return ctx.JSON(http.StatusOK, map[string]string{"username": principal.Username})
	}).Doc("protected")
	return server.Handler()
}

// envelope unwraps the six-field response body the framework returns.
type envelope[T any] struct {
	Data T `json:"data"`
}

// RFC 6238's SHA-1 test vector, with the secret from appendix B truncated the way an
// authenticator app would use it.
func TestTOTPMatchesRFC6238(t *testing.T) {
	// "12345678901234567890" in base32.
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cfg := DefaultTOTPConfig("gable")
	at := time.Unix(59, 0)

	code, err := TOTPCode(secret, at, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if code != "287082" {
		t.Fatalf("code = %q, want 287082", code)
	}

	if !VerifyTOTP(secret, "287082", at, cfg) {
		t.Fatal("the code for its own step must verify")
	}
	if !VerifyTOTP(secret, "287082", at.Add(20*time.Second), cfg) {
		t.Fatal("one period of drift is inside the default skew")
	}
	if VerifyTOTP(secret, "287082", at.Add(2*time.Minute), cfg) {
		t.Fatal("four periods of drift must not verify")
	}
	if VerifyTOTP(secret, "000000", at, cfg) {
		t.Fatal("a wrong code must not verify")
	}
	if VerifyTOTP(secret, "2870820", at, cfg) {
		t.Fatal("a code with the wrong length must not verify")
	}
}

func TestOTPAuthURLRendersAScannableSecret(t *testing.T) {
	url := OTPAuthURL("gable", "admin", "ABC234", DefaultTOTPConfig("gable"))
	for _, want := range []string{"otpauth://totp/gable:admin", "secret=ABC234", "algorithm=SHA1", "digits=6", "period=30"} {
		if !strings.Contains(url, want) {
			t.Fatalf("url = %q, missing %q", url, want)
		}
	}
}

// Enrollment hands out the secret once; activation needs a code from it, and only then
// does login start asking for a second factor.
func TestMFAEnrollActivateVerify(t *testing.T) {
	service, err := NewMFAService(DefaultTOTPConfig("gable"), nil)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	ctx := context.Background()

	if active, err := service.Active(ctx, 1, "admin"); err != nil || active {
		t.Fatalf("active before enrollment = %v, %v", active, err)
	}

	enrollment, err := service.Enroll(ctx, 1, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if enrollment.Secret == "" || len(enrollment.RecoveryCodes) != RecoveryCodeCount {
		t.Fatalf("enrollment = %+v", enrollment)
	}
	if strings.Contains(enrollment.URL, "ABC") && !strings.Contains(enrollment.URL, enrollment.Secret) {
		t.Fatalf("url = %q, want the issued secret", enrollment.URL)
	}

	// The account is not protected until a code proves the secret is installed.
	if active, _ := service.Active(ctx, 1, "admin"); active {
		t.Fatal("enrollment alone must not activate the factor")
	}
	code, err := TOTPCode(enrollment.Secret, service.now(), service.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(ctx, 1, "admin", "111111"); err != ErrMFAInvalidCode {
		t.Fatalf("activate with a wrong code = %v", err)
	}
	if err := service.Activate(ctx, 1, "admin", code); err != nil {
		t.Fatal(err)
	}
	if active, _ := service.Active(ctx, 1, "admin"); !active {
		t.Fatal("activate must turn the factor on")
	}

	status, err := service.Status(ctx, 1, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Active || status.RecoveryCodes != RecoveryCodeCount {
		t.Fatalf("status = %+v", status)
	}
}

// A recovery code works once and is gone afterwards: a second use must fail, otherwise a
// leaked list is a permanent bypass.
func TestMFARecoveryCodesAreSingleUse(t *testing.T) {
	service, err := NewMFAService(DefaultTOTPConfig("gable"), nil)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	ctx := context.Background()

	enrollment, err := service.Enroll(ctx, 1, "admin")
	if err != nil {
		t.Fatal(err)
	}
	code, err := TOTPCode(enrollment.Secret, service.now(), service.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(ctx, 1, "admin", code); err != nil {
		t.Fatal(err)
	}

	recovery := enrollment.RecoveryCodes[0]
	if err := service.Verify(ctx, 1, "admin", strings.ToUpper(recovery)); err != nil {
		t.Fatalf("recovery code: %v", err)
	}
	if err := service.Verify(ctx, 1, "admin", recovery); err != ErrMFAInvalidCode {
		t.Fatalf("second use = %v, want ErrMFAInvalidCode", err)
	}
	status, _ := service.Status(ctx, 1, "admin")
	if status.RecoveryCodes != RecoveryCodeCount-1 {
		t.Fatalf("remaining codes = %d, want %d", status.RecoveryCodes, RecoveryCodeCount-1)
	}

	// Disabling needs a working code: a stolen session alone must not remove the factor.
	if err := service.Disable(ctx, 1, "admin", "999999"); err != ErrMFAInvalidCode {
		t.Fatalf("disable with a wrong code = %v", err)
	}
	fresh, err := TOTPCode(enrollment.Secret, service.now(), service.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Disable(ctx, 1, "admin", fresh); err != nil {
		t.Fatal(err)
	}
	if active, _ := service.Active(ctx, 1, "admin"); active {
		t.Fatal("disable must turn the factor off")
	}
}

// testMFAHandlers builds the same wiring the example uses: a token service, MFA, cookies.
func testMFAHandlers(t *testing.T) (*Handlers, *MFAService, func() time.Time) {
	t.Helper()
	service := testService(t, AccessJWT)
	mfa, err := NewMFAService(DefaultTOTPConfig("gable"), NewMemoryMFAStore())
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1_800_000_000, 0)
	mfa.now = func() time.Time { return clock }
	handlers := NewHandlers(service, tenant.NewSingleLoginResolver(tenant.DefaultConfig()))
	handlers.MFA = mfa
	return handlers, mfa, func() time.Time { return clock }
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var payload *strings.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = strings.NewReader(string(raw))
	} else {
		payload = strings.NewReader("")
	}
	request := httptest.NewRequest(method, path, payload)
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// The login endpoint stops at a ticket when the account has an active factor, and the
// ticket plus a code is the only way to tokens.
func TestMFALoginFlowOverHTTP(t *testing.T) {
	handlers, mfa, now := testMFAHandlers(t)
	router := newTestRouter(handlers)

	// Enroll and activate for the seeded admin (subject "1").
	enrollment, err := mfa.Enroll(context.Background(), 1, "admin")
	if err != nil {
		t.Fatal(err)
	}
	code, err := TOTPCode(enrollment.Secret, now(), mfa.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := mfa.Activate(context.Background(), 1, "admin", code); err != nil {
		t.Fatal(err)
	}

	login := map[string]any{
		"username": "admin", "password": "secret",
		"clientId": "console", "clientSecret": "client-secret",
	}
	response := doJSON(t, router, http.MethodPost, "/passport/login", login)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", response.Code, response.Body.String())
	}
	var challenge envelope[MFAChallengeVO]
	if err := json.Unmarshal(response.Body.Bytes(), &challenge); err != nil {
		t.Fatal(err)
	}
	if !challenge.Data.MFARequired || challenge.Data.Ticket == "" {
		t.Fatalf("login body = %s, want a challenge", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "accessToken") {
		t.Fatal("a challenge response must not contain tokens")
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("a challenge response must not set a session cookie")
	}

	// The ticket is single-use, so a wrong code burns it and the next attempt needs a
	// new login.
	wrong := doJSON(t, router, http.MethodPost, "/passport/mfa/verify", map[string]any{
		"ticket": challenge.Data.Ticket, "code": "000000",
	})
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code status = %d", wrong.Code)
	}
	replay := doJSON(t, router, http.MethodPost, "/passport/mfa/verify", map[string]any{
		"ticket": challenge.Data.Ticket, "code": code,
	})
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replayed ticket status = %d, want it consumed", replay.Code)
	}

	// A fresh login plus a valid code issues tokens.
	response = doJSON(t, router, http.MethodPost, "/passport/login", login)
	if err := json.Unmarshal(response.Body.Bytes(), &challenge); err != nil {
		t.Fatal(err)
	}
	verified := doJSON(t, router, http.MethodPost, "/passport/mfa/verify", map[string]any{
		"ticket": challenge.Data.Ticket, "code": code,
	})
	if verified.Code != http.StatusOK {
		t.Fatalf("verify status = %d: %s", verified.Code, verified.Body.String())
	}
	var pair envelope[TokenVO]
	if err := json.Unmarshal(verified.Body.Bytes(), &pair); err != nil {
		t.Fatal(err)
	}
	if pair.Data.AccessToken == "" {
		t.Fatalf("verify body = %s", verified.Body.String())
	}
}

// A cookie session is a transport, not a second session: the token in the cookie works
// on the protected route and logout clears it.
func TestCookieSessionCarriesTheSameToken(t *testing.T) {
	// Opaque tokens so logout's revocation is observable: a JWT is stateless and stays
	// valid until it expires, which is a property of the token format, not of the cookie.
	service := testService(t, AccessOpaque)
	handlers := NewHandlers(service, tenant.NewSingleLoginResolver(tenant.DefaultConfig()))
	cookies := DefaultCookieSession()
	handlers.Cookies = &cookies
	router := newTestRouter(handlers)

	login := map[string]any{
		"username": "admin", "password": "secret",
		"clientId": "console", "clientSecret": "client-secret",
	}
	response := doJSON(t, router, http.MethodPost, "/passport/login", login)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", response.Code, response.Body.String())
	}
	var pair envelope[TokenVO]
	if err := json.Unmarshal(response.Body.Bytes(), &pair); err != nil {
		t.Fatal(err)
	}
	session := findCookie(response.Result().Cookies(), cookies.AccessName)
	if session == nil {
		t.Fatalf("login set %v, want %q", response.Result().Cookies(), cookies.AccessName)
	}
	if !session.HttpOnly {
		t.Fatal("the session cookie must be HttpOnly")
	}
	if session.Value != pair.Data.AccessToken {
		t.Fatal("the cookie must carry the same token the body returned")
	}

	// The protected route accepts the cookie without an Authorization header.
	protected := doJSON(t, router, http.MethodGet, "/protected", nil, session)
	if protected.Code != http.StatusOK {
		t.Fatalf("protected status = %d: %s", protected.Code, protected.Body.String())
	}

	// Logout revokes and expires both cookies.
	logout := doJSON(t, router, http.MethodGet, "/passport/logout", nil, session)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout status = %d", logout.Code)
	}
	cleared := findCookie(logout.Result().Cookies(), cookies.AccessName)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("logout cookie = %+v, want it expired", cleared)
	}
	after := doJSON(t, router, http.MethodGet, "/protected", nil, session)
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("status after logout = %d, want 401", after.Code)
	}

	// Refresh also accepts the cookie when the body omits the token.
	response = doJSON(t, router, http.MethodPost, "/passport/login", login)
	refresh := findCookie(response.Result().Cookies(), cookies.RefreshName)
	if refresh == nil {
		t.Fatal("login must set the refresh cookie")
	}
	rotated := doJSON(t, router, http.MethodPost, "/passport/login/refresh", map[string]any{}, refresh)
	if rotated.Code != http.StatusOK {
		t.Fatalf("refresh status = %d: %s", rotated.Code, rotated.Body.String())
	}
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

// A TOTP code is valid for its step and no longer: replaying it in the same window must fail,
// even though the code itself is still inside the skew.
func TestTOTPCodeCannotBeReplayed(t *testing.T) {
	service, err := NewMFAService(DefaultTOTPConfig("gable"), nil)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1_800_000_000, 0)
	service.now = func() time.Time { return clock }
	ctx := context.Background()

	enrollment, err := service.Enroll(ctx, 1, "ada")
	if err != nil {
		t.Fatal(err)
	}
	code, err := TOTPCode(enrollment.Secret, clock, service.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(ctx, 1, "ada", code); err != nil {
		t.Fatal(err)
	}

	// Activate recorded nothing about the code; the first Verify consumes its step.
	if err := service.Verify(ctx, 1, "ada", code); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := service.Verify(ctx, 1, "ada", code); err != ErrMFAInvalidCode {
		t.Fatalf("replay = %v, want ErrMFAInvalidCode", err)
	}

	// The next step's code is accepted, and the old one stays dead even though the window has
	// moved on.
	clock = clock.Add(30 * time.Second)
	next, err := TOTPCode(enrollment.Secret, clock, service.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Verify(ctx, 1, "ada", next); err != nil {
		t.Fatalf("next step: %v", err)
	}
	if err := service.Verify(ctx, 1, "ada", code); err != ErrMFAInvalidCode {
		t.Fatalf("old step after a newer one = %v", err)
	}

	state, err := service.store.GetMFA(ctx, 1, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if state.LastStep != StepAt(clock, service.Config()) {
		t.Fatalf("LastStep = %d, want %d", state.LastStep, StepAt(clock, service.Config()))
	}
}

// Two concurrent verifications of the same code: exactly one wins inside a process.
func TestTOTPReplayIsSerialised(t *testing.T) {
	service, err := NewMFAService(DefaultTOTPConfig("gable"), nil)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1_800_000_000, 0)
	service.now = func() time.Time { return clock }
	ctx := context.Background()

	enrollment, err := service.Enroll(ctx, 1, "ada")
	if err != nil {
		t.Fatal(err)
	}
	code, err := TOTPCode(enrollment.Secret, clock, service.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Activate(ctx, 1, "ada", code); err != nil {
		t.Fatal(err)
	}

	var success, rejected int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := service.Verify(ctx, 1, "ada", code)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				success++
			case err == ErrMFAInvalidCode:
				rejected++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if success != 1 || rejected != 7 {
		t.Fatalf("success = %d, rejected = %d; want exactly one success", success, rejected)
	}
}
