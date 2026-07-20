// Package waf provides request-shape validation at the edge: size caps,
// path-traversal rejection, and a small set of injection heuristics.
//
// This is defense-in-depth, not the defense. It exists to reject
// obviously malformed or hostile-shaped requests before they cost a
// connection to a backend, and to catch unparameterized-query-style
// payloads that shouldn't be reaching a backend at all. It is not a
// substitute for parameterized queries/prepared statements at the data
// layer, and the injection heuristics here WILL both miss real attacks
// (any WAF signature list is incomplete) and flag legitimate input
// (a payment note field containing "SELECT" as plain English text).
// Backend services must validate and parameterize independently of
// whatever this layer does.
package waf

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

type Config struct {
	MaxBodyBytes int64
	MaxURLLength int
	MaxHeaders   int
}

func DefaultConfig() Config {
	return Config{MaxBodyBytes: 1 << 20, MaxURLLength: 2048, MaxHeaders: 100}
}

// injectionPatterns are common SQL-injection and command-injection
// shapes. Deliberately conservative (word-boundary anchored SQL
// keywords, not bare substrings) to reduce false positives on
// legitimate free-text fields, but false positives are still expected —
// see the package doc.
var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bunion\b.{0,40}\bselect\b`),
	regexp.MustCompile(`(?i)\bor\b\s+['"]?1['"]?\s*=\s*['"]?1['"]?`),
	regexp.MustCompile(`(?i);\s*drop\s+table\b`),
	regexp.MustCompile(`(?i)xp_cmdshell`),
	regexp.MustCompile(`(?i)<script[\s>]`),
}

// Middleware rejects requests that fail shape validation before calling
// next, and wraps the body reader with a hard size cap for requests that
// pass.
func Middleware(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.String()) > cfg.MaxURLLength {
			http.Error(w, "request rejected", http.StatusRequestURITooLong)
			return
		}
		if len(r.Header) > cfg.MaxHeaders {
			http.Error(w, "request rejected", http.StatusBadRequest)
			return
		}
		if containsPathTraversal(r.URL.Path) {
			http.Error(w, "request rejected", http.StatusBadRequest)
			return
		}
		if matchesInjectionPattern(r.URL.Path) || queryContainsInjectionPattern(r.URL.Query()) {
			http.Error(w, "request rejected", http.StatusBadRequest)
			return
		}

		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// containsPathTraversal checks the already percent-decoded URL.Path (net/http
// decodes the raw request target before populating Path, so "/%2e%2e/x"
// arrives here as "/../x" and this check covers both forms).
func containsPathTraversal(path string) bool {
	return strings.Contains(path, "..")
}

func matchesInjectionPattern(s string) bool {
	if s == "" {
		return false
	}
	for _, re := range injectionPatterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// queryContainsInjectionPattern checks decoded query values (not the
// raw, still percent-encoded query string) so the check isn't defeated
// by encoding form (%20 vs "+" vs literal space) and isn't fooled into
// false negatives by encoding that r.URL.Query() would normalize anyway.
func queryContainsInjectionPattern(values url.Values) bool {
	for key, vs := range values {
		if matchesInjectionPattern(key) {
			return true
		}
		for _, v := range vs {
			if matchesInjectionPattern(v) {
				return true
			}
		}
	}
	return false
}
