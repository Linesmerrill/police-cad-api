package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/form3tech-oss/jwt-go"
)

type key int

const (
	keyPrincipalID key = iota
)

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		authHeader := strings.Split(r.Header.Get("Authorization"), "Bearer ")
		// we dont really care about the error here, if it fails then oh well :shrug:
		dump, _ := httputil.DumpRequest(r, true)
		zap.S().Debugw("incoming request",
			"request body", string(dump))
		if len(authHeader) != 2 {
			zap.S().Errorw("malformed token",
				"url", r.URL,
				"auth header", r.Header.Get("Authorization"),
			)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error:" "malformed token"}`))
			return
		}
		jwtToken := authHeader[1]
		token, err := jwt.Parse(jwtToken, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf(`{"error": "unexpected signing method, %v"}`, token.Header["alg"])
			}
			return []byte(os.Getenv("SECRET_KEY")), nil
		})
		if err != nil {
			zap.S().With("error", err).Errorw("failed to parse token",
				"url", r.URL,
				"token", jwtToken,
			)
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf(`{"error": "failed to parse token, %v"}`, err)))
			return
		}

		if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
			ctx := context.WithValue(r.Context(), keyPrincipalID, claims)
			// Access context values in handlers like this
			// props, _ := r.Context().Value("props").(jwt.MapClaims)
			next.ServeHTTP(w, r.WithContext(ctx))
		} else {
			zap.S().Errorw("unauthorized",
				"url", r.URL,
				"token", jwtToken,
			)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error:" "unauthorized"}"`))
			return
		}
	})
}
