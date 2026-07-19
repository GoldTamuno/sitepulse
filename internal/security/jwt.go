package security

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/yourname/sitepulse/internal/domain"
)

// AccessTokenTTL is intentionally short. Short-lived access tokens plus a
// long-lived, revocable refresh token is the standard pattern for a
// reason: an access token can't practically be revoked before it expires
// (it's self-verifying — no DB lookup), so we bound the blast radius of a
// stolen access token by expiring it fast. If it leaks, an attacker gets a
// 15-minute window, not indefinite access.
const AccessTokenTTL = 15 * time.Minute

// RefreshTokenTTL governs how long a refresh token chain stays valid
// without the user re-authenticating from scratch.
const RefreshTokenTTL = 7 * 24 * time.Hour

// Claims embeds the standard registered claims (exp, iat, sub) plus the
// role — putting role in the token lets middleware do a coarse check
// without a DB round-trip, but see the note on domain.Role: this is a
// convenience, not the authorization boundary. Sensitive operations must
// still verify against the DB-backed user record.
type Claims struct {
	UserID int64       `json:"uid"`
	Role   domain.Role `json:"role"`
	jwt.RegisteredClaims
}

// TokenIssuer signs and verifies access tokens. It's a small struct wrapping
// the secret rather than free functions taking a secret param everywhere —
// this is dependency injection again: handlers/services take a
// *TokenIssuer, constructed once in main.go from config.JWTSecret, instead
// of each call site reaching into config globally.
type TokenIssuer struct {
	secret []byte
}

func NewTokenIssuer(secret string) *TokenIssuer {
	return &TokenIssuer{secret: []byte(secret)}
}

func (t *TokenIssuer) GenerateAccessToken(userID int64, role domain.Role) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
			Subject:   fmt.Sprintf("%d", userID),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(t.secret)
	if err != nil {
		return "", fmt.Errorf("security: signing token: %w", err)
	}
	return signed, nil
}

var ErrInvalidToken = errors.New("security: invalid or expired token")

func (t *TokenIssuer) VerifyAccessToken(tokenString string) (*Claims, error) {
	claims := &Claims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		// Explicitly reject anything other than HMAC — without this check,
		// a token crafted with "alg: none" or an asymmetric algorithm the
		// server never intended to accept could bypass verification
		// entirely. This is CVE territory in JWT libraries historically;
		// pinning the expected algorithm here closes that door.
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return t.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}
