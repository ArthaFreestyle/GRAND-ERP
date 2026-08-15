// Internal test package: it calls the unexported issue so a token can be minted
// without a database. Login needs PostgreSQL; token handling does not, and these are
// the parts worth testing without one.
package usecase

import (
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func newAuth(t *testing.T, ttl time.Duration) *AuthUseCase {
	t.Helper()

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	// nil DB: Authenticate and issue never touch it.
	return NewAuthUseCase(nil, log, nil, nil, testSecret, ttl, "grand-erp")
}

func testUser() *entity.User {
	return &entity.User{
		ID:       7,
		Username: "kasir_pagi",
		Roles: []entity.RoleGrant{
			{ID: 20, Role: entity.Role{ID: 2, Nama: "CASHIER", IsAktif: true}},
			{ID: 30, Role: entity.Role{ID: 3, Nama: "INVENTARIS", IsAktif: true}},
		},
	}
}

// issue itself never chooses an active context — that decision belongs to
// Login (auto-select when exactly one grant) and SwitchContext (the caller's
// choice, re-checked against the database). Here the token is minted with
// CASHIER explicitly active, the way Login would for a single-grant user.
func TestTokenRoundTripCarriesUserAndActiveRole(t *testing.T) {
	auth := newAuth(t, time.Hour)
	user := testUser()
	aktif := toActiveContext(&user.Roles[0]) // CASHIER

	token, expiresAt, err := auth.issue(user, aktif)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if !expiresAt.After(time.Now()) {
		t.Errorf("expiresAt = %v, want a future time", expiresAt)
	}

	session, err := auth.Authenticate(token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if session.UserID != 7 {
		t.Errorf("UserID = %d, want 7", session.UserID)
	}

	if session.Username != "kasir_pagi" {
		t.Errorf("Username = %q, want kasir_pagi", session.Username)
	}

	// HasRole looks at the active grant alone, not the full grant list: holding
	// INVENTARIS too does not make it authorize as INVENTARIS right now.
	if !session.HasRole("CASHIER") {
		t.Errorf("active = %v, want CASHIER", session.Aktif)
	}

	if session.HasRole("INVENTARIS") {
		t.Error("session authorizes as INVENTARIS despite CASHIER being the active grant")
	}

	if session.HasRole("SUPERADMIN") {
		t.Error("session claims SUPERADMIN, which was never granted")
	}

	// Both grants still ride along for the switcher menu, even though only one
	// is active.
	if len(session.Grants) != 2 {
		t.Fatalf("Grants = %v, want both CASHIER and INVENTARIS", session.Grants)
	}
}

// A token minted with no active context (Login's answer when a caller holds
// more than one grant) authorizes nothing at all — HasRole and HasAnyRole must
// both answer false regardless of what the grant list contains.
func TestNoActiveContextAuthorizesNothing(t *testing.T) {
	auth := newAuth(t, time.Hour)

	token, _, err := auth.issue(testUser(), nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	session, err := auth.Authenticate(token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if session.Aktif != nil {
		t.Fatalf("Aktif = %v, want nil", session.Aktif)
	}

	if session.HasRole("CASHIER") || session.HasRole("INVENTARIS") {
		t.Error("a session with no active context authorized a role")
	}

	if session.HasAnyRole("CASHIER", "INVENTARIS", "SUPERADMIN") {
		t.Error("HasAnyRole answered true for a session with no active context")
	}

	if len(session.Grants) != 2 {
		t.Fatalf("Grants = %v, want both held grants to still be listed", session.Grants)
	}
}

// An expired token has to be refused, and say so — it is the one failure a client can
// act on by logging in again.
func TestExpiredTokenIsRejected(t *testing.T) {
	auth := newAuth(t, -time.Minute) // already past

	token, _, err := auth.issue(testUser(), nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	_, err = auth.Authenticate(token)
	if err == nil {
		t.Fatal("expired token was accepted")
	}

	assertUnauthorized(t, err)

	if got := err.(*model.Error).Message; got != "token expired" {
		t.Errorf("message = %q, want %q", got, "token expired")
	}
}

// The signature is the only thing standing between a caller and any identity they
// care to claim.
func TestTokenSignedWithAnotherSecretIsRejected(t *testing.T) {
	forger := NewAuthUseCase(nil, logrus.New(), nil, nil,
		"ffffffffffffffffffffffffffffffff", time.Hour, "grand-erp")

	token, _, err := forger.issue(&entity.User{
		ID:       1,
		Username: "penyusup",
		Roles:    []entity.RoleGrant{{ID: 1, Role: entity.Role{Nama: "SUPERADMIN", IsAktif: true}}},
	}, nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := newAuth(t, time.Hour).Authenticate(token); err == nil {
		t.Fatal("a token signed with a different secret was accepted")
	} else {
		assertUnauthorized(t, err)
	}
}

// The classic JWT attack: strip the signature and declare alg=none. Rejecting it is
// why Authenticate pins the accepted methods instead of trusting the token header.
func TestAlgNoneTokenIsRejected(t *testing.T) {
	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, &claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			Issuer:    "grand-erp",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Username: "penyusup",
		Aktif:    &model.ActiveContext{Role: "SUPERADMIN"},
	})

	token, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("build alg=none token: %v", err)
	}

	if _, err := newAuth(t, time.Hour).Authenticate(token); err == nil {
		t.Fatal("alg=none token was accepted; the signing method is not pinned")
	} else {
		assertUnauthorized(t, err)
	}
}

// A token minted for another service must not open this one, even with the same key.
func TestTokenFromAnotherIssuerIsRejected(t *testing.T) {
	other := NewAuthUseCase(nil, logrus.New(), nil, nil, testSecret, time.Hour, "layanan-lain")

	token, _, err := other.issue(testUser(), nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := newAuth(t, time.Hour).Authenticate(token); err == nil {
		t.Fatal("a token from another issuer was accepted")
	} else {
		assertUnauthorized(t, err)
	}
}

func TestGarbageAndEmptyTokensAreRejected(t *testing.T) {
	auth := newAuth(t, time.Hour)

	for _, token := range []string{"", "not-a-token", "a.b.c", "Bearer x"} {
		if _, err := auth.Authenticate(token); err == nil {
			t.Errorf("token %q was accepted", token)
		} else {
			assertUnauthorized(t, err)
		}
	}
}

// A user with no grants must produce an empty slice, not nil: GET /auth/me
// serialises it and HasAnyRole is called on it.
func TestSessionGrantsAreNeverNil(t *testing.T) {
	auth := newAuth(t, time.Hour)

	token, _, err := auth.issue(&entity.User{ID: 9, Username: "belum_punya_role"}, nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	session, err := auth.Authenticate(token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if session.Grants == nil {
		t.Error("Grants is nil; it must be an empty slice")
	}
}

// Every rejection has to be a 401, not a bare error that the handler would turn into
// a 500 with a generic message.
func assertUnauthorized(t *testing.T, err error) {
	t.Helper()

	domainErr, ok := err.(*model.Error)
	if !ok {
		t.Fatalf("error is %T, want *model.Error", err)
	}

	if domainErr.Kind != model.KindUnauthorized {
		t.Errorf("kind = %v, want KindUnauthorized", domainErr.Kind)
	}
}
