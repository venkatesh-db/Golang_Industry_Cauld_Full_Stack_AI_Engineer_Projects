package webhook

import (
	"errors"
	"io"
	"net/http"

	"subscriptioncore/provider"
)

// SignatureHeader is where the provider signature is expected. Stripe uses
// "Stripe-Signature"; kept configurable-by-constant for the reference build.
const SignatureHeader = "X-Signature"

// Ingress is the HTTP entry point for provider webhooks. It does the minimum
// synchronously — verify + dedupe + apply via the Processor — and returns fast.
// In production the apply step would be an outbox enqueue; here it is inline.
type Ingress struct {
	proc *Processor
}

// NewIngress wraps a Processor as an http.Handler.
func NewIngress(proc *Processor) *Ingress { return &Ingress{proc: proc} }

// ServeHTTP handles POST /webhooks/<provider>.
//
// Status contract:
//   - 200: processed, duplicate, stale, or illegal-but-safely-handled — all are
//     terminal outcomes the provider should NOT retry.
//   - 400: unverifiable signature — a real bad request.
//   - 405: wrong method.
//   - 500: transient store failure — the provider SHOULD retry.
func (in *Ingress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // cap at 1 MiB
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	sig := r.Header.Get(SignatureHeader)

	res, err := in.proc.Handle(r.Context(), body, sig)
	if err != nil {
		// Distinguish "won't ever succeed" (bad signature) from "try again".
		if errors.Is(err, provider.ErrInvalidSignature) {
			http.Error(w, "invalid signature", http.StatusBadRequest)
			return
		}
		http.Error(w, "processing failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, string(res))
}
