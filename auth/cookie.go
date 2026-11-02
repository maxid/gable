package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/maxid/gable/web"
)

// CookieSession carries the tokens in HttpOnly cookies instead of response bodies.
//
// Browsers cannot attach an Authorization header to a page navigation, so an application
// that renders the frontend itself (rather than a client that owns the tokens) needs the
// session in a cookie. The token is the same one the bearer middleware accepts: there is
// no second session format to keep in sync, and a cookie is only a transport.
type CookieSession struct {
	// AccessName and RefreshName are the cookie names; defaults are gable_access and
	// gable_refresh.
	AccessName  string
	RefreshName string
	// Path defaults to "/".
	Path string
	// Domain is empty for a host-only cookie.
	Domain string
	// Secure must be set in production: the session is a bearer credential.
	Secure bool
	// SameSite defaults to Lax, which keeps the cookie off cross-site POSTs.
	SameSite http.SameSite
	// AccessTTL and RefreshTTL bound the cookie lifetimes; zero means "same as the token
	// configuration", which is the honest default.
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

// DefaultCookieSession returns the development shape of the cookie session.
func DefaultCookieSession() CookieSession {
	return CookieSession{
		AccessName:  "gable_access",
		RefreshName: "gable_refresh",
		Path:        "/",
		SameSite:    http.SameSiteLaxMode,
	}
}

func (c CookieSession) normalize(cfg Config) CookieSession {
	if c.AccessName == "" {
		c.AccessName = "gable_access"
	}
	if c.RefreshName == "" {
		c.RefreshName = "gable_refresh"
	}
	if c.Path == "" {
		c.Path = "/"
	}
	if c.AccessTTL <= 0 {
		c.AccessTTL = cfg.AccessTTL
	}
	if c.RefreshTTL <= 0 {
		c.RefreshTTL = cfg.RefreshTTL
	}
	return c
}

// sessionCookie is the normalized config the handlers use.
func (h *Handlers) sessionCookie() (CookieSession, bool) {
	if h.Cookies == nil {
		return CookieSession{}, false
	}
	return h.Cookies.normalize(h.Service.Config()), true
}

// writeSession stores a freshly issued pair in cookies.
func (h *Handlers) writeSession(ctx *web.Context, pair *TokenPair) {
	session, ok := h.sessionCookie()
	if !ok || pair == nil {
		return
	}
	// SameSite=None requires Secure, and a browser drops an insecure one; refusing to
	// set it is better than an auth failure that only shows up in the browser.
	sameSite := session.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	http.SetCookie(ctx.Writer(), &http.Cookie{
		Name:     session.AccessName,
		Value:    pair.AccessToken,
		Path:     session.Path,
		Domain:   session.Domain,
		MaxAge:   int(session.AccessTTL.Seconds()),
		Secure:   session.Secure,
		HttpOnly: true,
		SameSite: sameSite,
	})
	if pair.RefreshToken != "" {
		http.SetCookie(ctx.Writer(), &http.Cookie{
			Name:     session.RefreshName,
			Value:    pair.RefreshToken,
			Path:     session.Path,
			Domain:   session.Domain,
			MaxAge:   int(session.RefreshTTL.Seconds()),
			Secure:   session.Secure,
			HttpOnly: true,
			SameSite: sameSite,
		})
	}
}

// clearSession expires both cookies. It runs even when the token was already gone, so a
// logout never leaves a usable credential behind.
func (h *Handlers) clearSession(ctx *web.Context) {
	session, ok := h.sessionCookie()
	if !ok {
		return
	}
	for _, name := range []string{session.AccessName, session.RefreshName} {
		http.SetCookie(ctx.Writer(), &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     session.Path,
			Domain:   session.Domain,
			MaxAge:   -1,
			Secure:   session.Secure,
			HttpOnly: true,
			SameSite: session.SameSite,
		})
	}
}

// accessToken returns the credential for a request: the Authorization header first, then the
// session cookie. The header wins so a client can override a stale cookie.
func (h *Handlers) accessToken(request *http.Request) string {
	if token := bearerToken(request); token != "" {
		return token
	}
	session, ok := h.sessionCookie()
	if !ok {
		return ""
	}
	cookie, err := request.Cookie(session.AccessName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

// refreshToken returns the refresh credential, from the body first and the cookie second.
func (h *Handlers) refreshToken(request *http.Request, bodyValue string) string {
	if strings.TrimSpace(bodyValue) != "" {
		return strings.TrimSpace(bodyValue)
	}
	session, ok := h.sessionCookie()
	if !ok {
		return ""
	}
	cookie, err := request.Cookie(session.RefreshName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}
