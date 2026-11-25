// Package browserauth owns the public Router's browser transport boundary. It
// parses cookies and CSRF proofs locally and never depends on Admin modules.
package browserauth

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	SessionCookieName = "zubrium_admin_session"
	CSRFCookieName    = "zubrium_admin_csrf"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrAmbiguous    = errors.New("ambiguous authentication")
)

type Credential struct {
	Value   string
	Session bool
}

type Transport struct {
	origins map[string]struct{}
}

func New(allowedOrigins []string) (*Transport, error) {
	if len(allowedOrigins) == 0 || len(allowedOrigins) > 32 {
		return nil, errors.New("Admin allowed origins must contain 1..32 values")
	}
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, value := range allowedOrigins {
		parsed, err := url.Parse(value)
		if err != nil || value == "" || len(value) > 512 || value != strings.TrimSpace(value) ||
			(parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
			parsed.Host != strings.ToLower(parsed.Host) || strings.ContainsAny(parsed.Host, "*%,") || strings.HasSuffix(parsed.Host, ":") ||
			parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			parsed.RawFragment != "" || parsed.Opaque != "" || value != parsed.Scheme+"://"+parsed.Host {
			return nil, errors.New("Admin allowed origin is invalid")
		}
		if port := parsed.Port(); port != "" {
			portNumber, parseErr := strconv.Atoi(port)
			if parseErr != nil || portNumber < 1 || portNumber > 65535 ||
				(parsed.Scheme == "http" && portNumber == 80) || (parsed.Scheme == "https" && portNumber == 443) {
				return nil, errors.New("Admin allowed origin is invalid")
			}
		}
		if _, duplicate := origins[value]; duplicate {
			return nil, errors.New("Admin allowed origin is duplicated")
		}
		origins[value] = struct{}{}
	}
	return &Transport{origins: origins}, nil
}

// Resolve accepts exactly one authentication mechanism. Browser cookies and
// service Bearer credentials cannot be combined in one request.
func Resolve(r *http.Request) (Credential, error) {
	if r == nil {
		return Credential{}, ErrUnauthorized
	}
	token, present, valid := SessionToken(r)
	if present {
		if !valid {
			return Credential{}, ErrUnauthorized
		}
		if len(r.Header.Values("Authorization")) != 0 {
			return Credential{}, ErrAmbiguous
		}
		return Credential{Value: token, Session: true}, nil
	}
	headerValues := r.Header.Values("Authorization")
	if len(headerValues) > 1 {
		return Credential{}, ErrAmbiguous
	}
	if len(headerValues) != 1 {
		return Credential{}, ErrUnauthorized
	}
	parts := strings.Fields(headerValues[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" || strings.ContainsAny(parts[1], "\r\n") {
		return Credential{}, ErrUnauthorized
	}
	return Credential{Value: parts[1]}, nil
}

func SessionToken(r *http.Request) (token string, present bool, valid bool) {
	if r == nil {
		return "", false, false
	}
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == SessionCookieName {
			token = cookie.Value
			count++
		}
	}
	if count == 0 {
		return "", false, false
	}
	return token, true, count == 1 && ValidSessionToken(token)
}

func (transport *Transport) ValidateSessionRequest(r *http.Request) error {
	if transport == nil || r == nil {
		return ErrForbidden
	}
	if !isStateChanging(r.Method) {
		return nil
	}
	if !transport.TrustedOrigin(r) || !ValidCSRFProof(r) {
		return ErrForbidden
	}
	return nil
}

func (transport *Transport) TrustedOrigin(r *http.Request) bool {
	if transport == nil || r == nil {
		return false
	}
	values := r.Header.Values("Origin")
	if len(values) != 1 {
		return false
	}
	_, allowed := transport.origins[values[0]]
	return allowed
}

// TrustedReadOrigin guards read-only browser streams such as EventSource.
// Those APIs send no Origin header on same-origin GETs, so we require an
// explicit same-origin fetch metadata header and an allowed Referer instead.
// State-changing requests must still use TrustedOrigin with a CSRF proof.
func (transport *Transport) TrustedReadOrigin(r *http.Request) bool {
	if transport == nil || r == nil {
		return false
	}
	if transport.TrustedOrigin(r) {
		return true
	}
	if len(r.Header.Values("Origin")) != 0 {
		return false
	}
	fetchSite := r.Header.Values("Sec-Fetch-Site")
	if len(fetchSite) != 1 || fetchSite[0] != "same-origin" {
		return false
	}
	referrers := r.Header.Values("Referer")
	if len(referrers) != 1 {
		return false
	}
	parsed, err := url.Parse(referrers[0])
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return false
	}
	_, allowed := transport.origins[parsed.Scheme+"://"+strings.ToLower(parsed.Host)]
	return allowed
}

func ValidCSRFProof(r *http.Request) bool {
	if r == nil {
		return false
	}
	var cookieValue string
	cookieCount := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == CSRFCookieName {
			cookieValue = cookie.Value
			cookieCount++
		}
	}
	headerValues := r.Header.Values("X-CSRF-Token")
	return cookieCount == 1 && len(headerValues) == 1 && ValidCSRFToken(cookieValue) && ValidCSRFToken(headerValues[0]) &&
		subtle.ConstantTimeCompare([]byte(cookieValue), []byte(headerValues[0])) == 1
}

func ValidSessionToken(value string) bool {
	if !strings.HasPrefix(value, "ss_") || len(value) != 46 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "ss_"))
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == strings.TrimPrefix(value, "ss_")
}

func ValidCSRFToken(value string) bool {
	if len(value) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func isStateChanging(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}
