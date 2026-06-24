package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

type Authenticator struct {
	expectedToken string
}

func New(password string) Authenticator {
	sum := sha256.Sum256([]byte(password))
	return Authenticator{expectedToken: hex.EncodeToString(sum[:])}
}

func (a Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.ValidBearer(r.Header.Get("Authorization")) {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a Authenticator) ValidBearer(header string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return a.ValidToken(token)
}

func (a Authenticator) ValidToken(token string) bool {
	if len(token) != len(a.expectedToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(a.expectedToken)) == 1
}
