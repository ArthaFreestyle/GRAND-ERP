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

// Grant is one role a user holds, and where — the shape a session's switcher
// menu is built from (isu #12 fase 4). It mirrors entity.RoleGrant, but this
// copy crosses into the JWT claims and back out into a response, which is why
// it lives here rather than there.
//
// A nil IDUnitKerja means the grant applies everywhere.
type Grant struct {
	IDUserRole    int64   `json:"id_user_role"`
	IDRole        int64   `json:"id_role"`
	Role          string  `json:"role"`
	IDUnitKerja   *int64  `json:"id_unit_kerja"`
	NamaUnitKerja *string `json:"nama_unit_kerja"`
}

// ActiveContext is the one grant a session is currently acting as — isu #12
// fase 4. Nil means the caller has not chosen: login with more than one usable
// grant issues a token with no active context, reachable only through
// switch-context and auth/me, because with grants now bound to a place there
// is no default that would not risk someone acting under an authority they did
// not realize they had picked.
type ActiveContext struct {
	IDUserRole  int64  `json:"id_user_role"`
	Role        string `json:"role"`
	IDUnitKerja *int64 `json:"id_unit_kerja"`
}

// SwitchContextRequest names the grant to switch to by the user_role row's own
// id — the same id RoleRef.id_user_role and Grant.id_user_role carry.
type SwitchContextRequest struct {
	IDUserRole int64 `json:"id_user_role" validate:"required,gt=0"`
}

// LoginResponse hands back a bearer token and nothing secret.
//
// ExpiresAt is absolute rather than a duration so a client does not have to guess
// when it received the response.
//
// Grants and Aktif let a client decide immediately whether it must call
// switch-context before doing anything else — Aktif is nil exactly when it
// must. SwitchContext returns the same shape, since it is really "issue a
// token" answered a second time with a chosen context.
//
// RefreshToken is populated by Login and Refresh (isu #24 fase 2) but never by
// SwitchContext, which mints an access token only, same as before that issue —
// omitempty keeps SwitchContext's response byte-identical to what it always
// returned.
type LoginResponse struct {
	Token        string         `json:"token"`
	TokenType    string         `json:"token_type"`
	ExpiresAt    time.Time      `json:"expires_at"`
	RefreshToken string         `json:"refresh_token,omitempty"`
	User         *UserResponse  `json:"user"`
	Grants       []Grant        `json:"grants"`
	Aktif        *ActiveContext `json:"aktif"`
}

// ChangePasswordRequest is POST /api/v1/auth/me/password's body — isu #24
// fase 1. There is no ActorID field: the target is always the caller's own
// session (session.UserID, passed as AuthUseCase.ChangePassword's own
// parameter the same way SwitchContext takes userID), never something the
// body could redirect elsewhere.
//
// PasswordLama is required even though the caller is already authenticated —
// a stolen access token must not be able to lock the real owner out of their
// own account by changing the password out from under them.
type ChangePasswordRequest struct {
	PasswordLama string `json:"password_lama" validate:"required,max=72"`
	PasswordBaru string `json:"password_baru" validate:"required,min=8,max=72"`
}

// RefreshTokenRequest is POST /api/v1/auth/refresh's body — isu #24 fase 2.
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// LogoutRequest is POST /api/v1/auth/logout's body. Same shape as
// RefreshTokenRequest but kept as its own type: the two actions diverge (one
// mints a new pair, the other only deletes), and a shared type would make
// that divergence a coincidence rather than a fact the compiler enforces.
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// LogoutResponse is deliberately not empty — every action endpoint in this
// API returns something, so a client that logs the response body of every
// call never has to special-case this one.
type LogoutResponse struct {
	Message string `json:"message"`
}

// SessionResponse is what GET /api/v1/auth/me returns: the caller exactly as the
// token describes them, which is what the server authorizes with. Grants is
// always an array, never null.
type SessionResponse struct {
	UserID   int64          `json:"user_id"`
	Username string         `json:"username"`
	Grants   []Grant        `json:"grants"`
	Aktif    *ActiveContext `json:"aktif"`
}

// Session is the authenticated caller, decoded from a token. It crosses from the
// usecase layer back into delivery, which is why it is a model and not an entity.
//
// Grants is the union of every usable grant the user held **when the token was
// issued** — role active and, if scoped, unit active too. With stateless JWT
// there is nothing to consult at request time, so a grant made or revoked
// afterwards does not appear here until the user logs in again (or switches
// context, which mints a fresh token). That is the accepted trade of the
// stateless design, bounded by jwt.ttl_minutes.
//
// Aktif is the one grant this session currently authorizes as. A session with a
// nil Aktif authorizes nothing at all — HasRole always answers false — which is
// what confines a not-yet-chosen session to auth/me and switch-context without
// either of those routes needing a role guard of their own: RequireRole and the
// route table are unmodified by this, on purpose.
type Session struct {
	UserID   int64
	Username string
	Grants   []Grant
	Aktif    *ActiveContext
}

// HasRole reports whether the session's active grant is the named role.
// Comparison is case-insensitive because role.nama is unique case-insensitively,
// so "CASHIER" and "cashier" are the same role and must not authorize
// differently.
//
// It looks at Aktif alone, never at the full Grants list: holding a role
// somewhere is not the same as acting as it right now, and letting any held
// grant authorize would make "wewenang bertempat" (isu #12 fase 3) pointless —
// the whole point of an active context is that only the chosen one counts.
func (s *Session) HasRole(name string) bool {
	return s.Aktif != nil && strings.EqualFold(s.Aktif.Role, name)
}

// HasAnyRole reports whether the session's active grant is one of the named
// roles. No names means no requirement, so it answers true — a route guarded by
// an empty list is authenticated-only, not impossible to reach.
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
