package wire

import (
	"crypto/subtle"
	"net/http"
)

// Guard strips the gateway-supplied identity headers unless the request carries
// key. An empty key disables the check (development). The key header itself is
// never passed on.
func Guard(key string, next http.Handler) http.Handler {
	if key == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(GatewayKeyHeader)
		r.Header.Del(GatewayKeyHeader)
		if subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
			r.Header.Del(AccountHeader)
			r.Header.Del(UsersHeader)
		}
		next.ServeHTTP(w, r)
	})
}
