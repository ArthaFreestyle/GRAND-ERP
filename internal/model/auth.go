package model

import (
	"strings"
	"time"
)

// LoginRequest carries credentials. Username matches case-insensitively, mirroring
// users_username_lower_uidx — "Budi" and "budi" are the same account, so refusing
// the wrong casing would be a lie.
type LoginRequest struct {
	Username string `json:"username" validate:"required,max=64"`
	Password string `json:"password" validate:"required,max=72"`
}

// LoginResponse hands back a bearer token and nothing secret.
//
// ExpiresAt is absolute rather than a duration so a client does not have to guess
// when it received the response.
type LoginResponse struct {
	Token     string        `json:"token"`
	TokenType string        `json:"token_type"`
	ExpiresAt time.Time     `json:"expires_at"`
	User      *UserResponse `json:"user"`
}

// SessionResponse is what GET /api/v1/auth/me returns: the caller exactly as the
// token describes them, which is what the server authorizes with. Roles is always an
// array, never null.
type SessionResponse struct {
	UserID   int64    `json:"user_id"`
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
}

// Session is the authenticated caller, decoded from a token. It crosses from the
// usecase layer back into delivery, which is why it is a model and not an entity.
//
// Roles is the union of every role the user held **when the token was issued**.
// With stateless JWT there is nothing to consult at request time, so a role granted
// or revoked afterwards does not appear here until the user logs in again. That is
// the accepted trade of the stateless design, bounded by jwt.ttl_minutes.
type Session struct {
	UserID   int64
	Username string
	Roles    []string
}

// HasRole reports whether the session carries the named role. Comparison is
// case-insensitive because role.nama is unique case-insensitively, so "CASHIER" and
// "cashier" are the same role and must not authorize differently.
func (s *Session) HasRole(name string) bool {
	for _, role := range s.Roles {
		if strings.EqualFold(role, name) {
			return true
		}
	}

	return false
}

// HasAnyRole reports whether the session carries at least one of the named roles.
// No names means no requirement, so it answers true — a route guarded by an empty
// list is authenticated-only, not impossible to reach.
func (s *Session) HasAnyRole(names ...string) bool {
	if len(names) == 0 {
		return true
	}

	for _, name := range names {
		if s.HasRole(name) {
			return true
		}
	}

	return false
}
