package usecase_test

// These tests exercise Login and SwitchContext against a real PostgreSQL,
// because what isu #12 fase 4 asks to prove — which grants are usable, and
// whether switch-context's re-check catches a role or unit retired after the
// original token was issued — lives in the database. auth_token_test.go
// (package usecase, no DB) covers the pure token mechanics; these cover the
// business rules layered on top.

import (
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/config"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/repository"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/sirupsen/logrus"
)

const testPassword = "rahasia123"

// testLoginIP is a fixed stand-in for the caller's address in every test that
// does not care about throttling specifically — every such test uses a
// different username, so a shared IP never collides across them (the throttle
// key is the pair, isu #24 fase 4).
const testLoginIP = "127.0.0.1"

// loginAs is a small helper: create a user with the given grants, log in, and
// return the response.
func loginAs(t *testing.T, a *app, username string, grants []model.GrantRequest) *model.LoginResponse {
	t.Helper()

	if _, err := a.user.Create(ctx(), &model.CreateUserRequest{
		ActorID:  testActor(t),
		Username: username,
		Password: testPassword,
		Grants:   grants,
	}); err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}

	response, err := a.auth.Login(ctx(), testLoginIP, &model.LoginRequest{
		Username: username,
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}

	return response
}

// A user with no grants can still log in — reads are open to any authenticated
// caller regardless of role — and gets an empty grant list with no active
// context, never a nil slice.
func TestLoginWithNoGrantsHasNoActiveContext(t *testing.T) {
	a := newApp(t)

	response := loginAs(t, a, "belum_ada_grant", nil)

	if response.Aktif != nil {
		t.Errorf("Aktif = %v, want nil", response.Aktif)
	}

	if response.Grants == nil {
		t.Error("Grants is nil; it must be an empty slice")
	}

	if len(response.Grants) != 0 {
		t.Errorf("Grants = %v, want none", response.Grants)
	}
}

// Holding exactly one usable grant has no ambiguity, so Login selects it
// automatically rather than forcing an extra switch-context round trip.
func TestLoginWithOneGrantSelectsItAutomatically(t *testing.T) {
	a := newApp(t)
	roles := seedRoles(t, a)

	response := loginAs(t, a, "satu_grant", []model.GrantRequest{{IDRole: roles["CASHIER"]}})

	if response.Aktif == nil {
		t.Fatal("Aktif is nil, want the sole grant auto-selected")
	}

	if response.Aktif.Role != "CASHIER" {
		t.Errorf("Aktif.Role = %q, want CASHIER", response.Aktif.Role)
	}

	if response.Aktif.IDUserRole != response.Grants[0].IDUserRole {
		t.Errorf("Aktif.IDUserRole = %d, want %d (the grant's own id)",
			response.Aktif.IDUserRole, response.Grants[0].IDUserRole)
	}

	session, err := a.auth.Authenticate(response.Token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	if !session.HasRole("CASHIER") {
		t.Error("session does not authorize as CASHIER despite it being the sole, auto-selected grant")
	}
}

// With grants now bound to a place, there is no default that would not risk
// someone acting under an authority they did not realize they had picked — so
// holding more than one usable grant leaves the active context unset, and
// HasRole must then answer false for every role.
func TestLoginWithManyGrantsHasNoActiveContext(t *testing.T) {
	a := newApp(t)
	roles := seedRoles(t, a)
	outletA := createUnit(t, a, "Login Outlet A")
	outletB := createUnit(t, a, "Login Outlet B")

	response := loginAs(t, a, "dua_grant", []model.GrantRequest{
		{IDRole: roles["INVENTARIS"], IDUnitKerja: &outletA},
		{IDRole: roles["INVENTARIS"], IDUnitKerja: &outletB},
	})

	if response.Aktif != nil {
		t.Errorf("Aktif = %v, want nil — the caller must choose via switch-context", response.Aktif)
	}

	if len(response.Grants) != 2 {
		t.Fatalf("Grants = %v, want both", response.Grants)
	}

	session, err := a.auth.Authenticate(response.Token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	if session.HasRole("INVENTARIS") {
		t.Error("a session with no active context must not authorize any role, even one it holds twice")
	}
}

// A grant whose role was retired after being granted must not be counted
// toward "exactly one usable grant", must not appear in the token's grant
// list, and must not become the active context.
func TestLoginExcludesGrantWithRetiredRole(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)
	roles := seedRoles(t, a)

	// The grant is created while INVENTARIS is still active — Create's own
	// requireActiveGrants check (isu #12 fase 3) would otherwise refuse it
	// outright — and only retired afterwards, so what Login excludes is a
	// grant that was valid when it was made.
	if _, err := a.user.Create(ctx(), &model.CreateUserRequest{
		ActorID:  actor,
		Username: "role_pensiun",
		Password: testPassword,
		Grants: []model.GrantRequest{
			{IDRole: roles["CASHIER"]},
			{IDRole: roles["INVENTARIS"]},
		},
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := a.role.Update(ctx(), &model.UpdateRoleRequest{
		ActorID: actor,
		ID:      roles["INVENTARIS"],
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("retire role: %v", err)
	}

	response, err := a.auth.Login(ctx(), testLoginIP, &model.LoginRequest{Username: "role_pensiun", Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if len(response.Grants) != 1 || response.Grants[0].Role != "CASHIER" {
		t.Fatalf("Grants = %v, want only the still-active CASHIER grant", response.Grants)
	}

	if response.Aktif == nil || response.Aktif.Role != "CASHIER" {
		t.Errorf("Aktif = %v, want CASHIER auto-selected as the only usable grant", response.Aktif)
	}
}

// The same rule, for a retired unit_kerja rather than a retired role: a grant
// scoped to a unit that is no longer active is not usable either.
func TestLoginExcludesGrantWithRetiredUnitKerja(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)
	roles := seedRoles(t, a)
	unit := createUnit(t, a, "Unit Pensiun Login")

	// Same ordering constraint as the retired-role test: the grant is created
	// while the unit is still active, then retired afterwards.
	if _, err := a.user.Create(ctx(), &model.CreateUserRequest{
		ActorID:  actor,
		Username: "unit_pensiun_login",
		Password: testPassword,
		Grants: []model.GrantRequest{
			{IDRole: roles["CASHIER"]}, // global, unaffected
			{IDRole: roles["INVENTARIS"], IDUnitKerja: &unit},
		},
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := a.unitKerja.Update(ctx(), &model.UpdateUnitKerjaRequest{
		ActorID: actor,
		ID:      unit,
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("retire unit: %v", err)
	}

	response, err := a.auth.Login(ctx(), testLoginIP, &model.LoginRequest{Username: "unit_pensiun_login", Password: testPassword})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if len(response.Grants) != 1 || response.Grants[0].Role != "CASHIER" {
		t.Fatalf("Grants = %v, want only the global CASHIER grant", response.Grants)
	}

	if response.Aktif == nil || response.Aktif.Role != "CASHIER" {
		t.Errorf("Aktif = %v, want CASHIER auto-selected", response.Aktif)
	}
}

// SwitchContext's whole point: pick one of several held grants and get a token
// that authorizes as exactly that one.
func TestSwitchContextSelectsTheNamedGrant(t *testing.T) {
	a := newApp(t)
	roles := seedRoles(t, a)
	outletA := createUnit(t, a, "Switch Outlet A")
	outletB := createUnit(t, a, "Switch Outlet B")

	login := loginAs(t, a, "pilih_konteks", []model.GrantRequest{
		{IDRole: roles["INVENTARIS"], IDUnitKerja: &outletA},
		{IDRole: roles["INVENTARIS"], IDUnitKerja: &outletB},
	})

	if login.Aktif != nil {
		t.Fatal("precondition failed: login should have no active context with two grants")
	}

	session, err := a.auth.Authenticate(login.Token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	// Find the grant naming outletB.
	var target int64
	for _, grant := range login.Grants {
		if grant.IDUnitKerja != nil && *grant.IDUnitKerja == outletB {
			target = grant.IDUserRole
		}
	}
	if target == 0 {
		t.Fatal("could not find the outlet-B grant among login.Grants")
	}

	switched, err := a.auth.SwitchContext(ctx(), session.UserID, &model.SwitchContextRequest{IDUserRole: target})
	if err != nil {
		t.Fatalf("switch context: %v", err)
	}

	if switched.Aktif == nil || switched.Aktif.IDUserRole != target {
		t.Fatalf("Aktif = %v, want id_user_role %d active", switched.Aktif, target)
	}

	if switched.Aktif.IDUnitKerja == nil || *switched.Aktif.IDUnitKerja != outletB {
		t.Errorf("Aktif.IDUnitKerja = %v, want %d", switched.Aktif.IDUnitKerja, outletB)
	}

	newSession, err := a.auth.Authenticate(switched.Token)
	if err != nil {
		t.Fatalf("authenticate new token: %v", err)
	}

	if !newSession.HasRole("INVENTARIS") {
		t.Error("the switched session does not authorize as INVENTARIS")
	}

	// The original token is not revoked — switching context mints a new one,
	// it does not invalidate the old. This is the caveat CLAUDE.md states
	// plainly: an old token stays valid until it expires.
	if _, err := a.auth.Authenticate(login.Token); err != nil {
		t.Errorf("the pre-switch token was rejected, but switching context must not revoke it: %v", err)
	}
}

// A grant id that belongs to another user must be refused — switch-context is
// not a way to borrow someone else's authority by guessing an id.
func TestSwitchContextRejectsGrantOfAnotherUser(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)
	roles := seedRoles(t, a)

	owner := loginAs(t, a, "pemilik_grant", []model.GrantRequest{
		{IDRole: roles["CASHIER"]},
	})

	if _, err := a.user.Create(ctx(), &model.CreateUserRequest{
		ActorID:  actor,
		Username: "bukan_pemilik",
		Password: testPassword,
	}); err != nil {
		t.Fatalf("create second user: %v", err)
	}

	intruder, err := a.auth.Login(ctx(), testLoginIP, &model.LoginRequest{Username: "bukan_pemilik", Password: testPassword})
	if err != nil {
		t.Fatalf("login second user: %v", err)
	}

	intruderSession, err := a.auth.Authenticate(intruder.Token)
	if err != nil {
		t.Fatalf("authenticate second user: %v", err)
	}

	_, err = a.auth.SwitchContext(ctx(), intruderSession.UserID, &model.SwitchContextRequest{
		IDUserRole: owner.Grants[0].IDUserRole,
	})

	assertKind(t, err, model.KindForbidden)
}

// A grant whose role was retired after the token was issued must be refused
// by switch-context even though it was usable at login time — this is the one
// place a stale token's claims are re-checked against the database rather than
// trusted.
func TestSwitchContextRejectsGrantWithRoleRetiredSinceLogin(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)
	roles := seedRoles(t, a)

	login := loginAs(t, a, "role_dicabut_setelah_login", []model.GrantRequest{
		{IDRole: roles["CASHIER"]},
		{IDRole: roles["INVENTARIS"]},
	})

	session, err := a.auth.Authenticate(login.Token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	var inventarisGrant int64
	for _, grant := range login.Grants {
		if grant.Role == "INVENTARIS" {
			inventarisGrant = grant.IDUserRole
		}
	}
	if inventarisGrant == 0 {
		t.Fatal("could not find the INVENTARIS grant")
	}

	if _, err := a.role.Update(ctx(), &model.UpdateRoleRequest{
		ActorID: actor,
		ID:      roles["INVENTARIS"],
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("retire role: %v", err)
	}

	_, err = a.auth.SwitchContext(ctx(), session.UserID, &model.SwitchContextRequest{IDUserRole: inventarisGrant})

	assertKind(t, err, model.KindForbidden)
}

// The same re-check, for a unit_kerja retired after login rather than a role.
func TestSwitchContextRejectsGrantWithUnitRetiredSinceLogin(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)
	roles := seedRoles(t, a)
	unit := createUnit(t, a, "Unit Dicabut Setelah Login")

	login := loginAs(t, a, "unit_dicabut_setelah_login", []model.GrantRequest{
		{IDRole: roles["CASHIER"]},
		{IDRole: roles["INVENTARIS"], IDUnitKerja: &unit},
	})

	session, err := a.auth.Authenticate(login.Token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	var scopedGrant int64
	for _, grant := range login.Grants {
		if grant.IDUnitKerja != nil {
			scopedGrant = grant.IDUserRole
		}
	}
	if scopedGrant == 0 {
		t.Fatal("could not find the unit-scoped grant")
	}

	if _, err := a.unitKerja.Update(ctx(), &model.UpdateUnitKerjaRequest{
		ActorID: actor,
		ID:      unit,
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("retire unit: %v", err)
	}

	_, err = a.auth.SwitchContext(ctx(), session.UserID, &model.SwitchContextRequest{IDUserRole: scopedGrant})

	assertKind(t, err, model.KindForbidden)
}

// An id_user_role that does not exist at all must answer the same 403 as an
// id that exists but is not usable — distinguishing them would let a caller
// probe which grant ids exist for other users.
func TestSwitchContextRejectsUnknownGrant(t *testing.T) {
	a := newApp(t)
	roles := seedRoles(t, a)

	login := loginAs(t, a, "grant_tidak_ada", []model.GrantRequest{{IDRole: roles["CASHIER"]}})

	session, err := a.auth.Authenticate(login.Token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	_, err = a.auth.SwitchContext(ctx(), session.UserID, &model.SwitchContextRequest{IDUserRole: 999_999})

	assertKind(t, err, model.KindForbidden)
}

// ---------------------------------------------------------------------------
// isu #24 fase 1: self password change.
// ---------------------------------------------------------------------------

// Changing to the new password with the correct old one succeeds, and the
// next login must use the new password — the old one no longer works.
func TestChangePasswordWithCorrectOldPasswordSucceeds(t *testing.T) {
	a := newApp(t)
	login := loginAs(t, a, "ganti_password_benar", nil)

	const newPassword = "rahasia_baru_123"

	if _, err := a.auth.ChangePassword(ctx(), login.User.ID, &model.ChangePasswordRequest{
		PasswordLama: testPassword,
		PasswordBaru: newPassword,
	}); err != nil {
		t.Fatalf("change password: %v", err)
	}

	if _, err := a.auth.Login(ctx(), testLoginIP, &model.LoginRequest{
		Username: "ganti_password_benar", Password: testPassword,
	}); err == nil {
		t.Error("the old password still logs in after it was changed")
	}

	if _, err := a.auth.Login(ctx(), testLoginIP, &model.LoginRequest{
		Username: "ganti_password_benar", Password: newPassword,
	}); err != nil {
		t.Errorf("the new password does not log in: %v", err)
	}
}

// A wrong password_lama is refused, and nothing about the account changes —
// a stolen access token must not be able to lock the real owner out.
func TestChangePasswordWithWrongOldPasswordIsRejected(t *testing.T) {
	a := newApp(t)
	login := loginAs(t, a, "ganti_password_salah", nil)

	_, err := a.auth.ChangePassword(ctx(), login.User.ID, &model.ChangePasswordRequest{
		PasswordLama: "bukan-password-lama-yang-benar",
		PasswordBaru: "rahasia_baru_123",
	})

	assertKind(t, err, model.KindUnauthorized)

	if _, err := a.auth.Login(ctx(), testLoginIP, &model.LoginRequest{
		Username: "ganti_password_salah", Password: testPassword,
	}); err != nil {
		t.Errorf("the original password stopped working despite the rejected change: %v", err)
	}
}

// A session with no active context (two usable grants, isu #12 fase 4) can
// still secure its own account — the endpoint carries no role guard.
func TestChangePasswordWorksWithNoActiveContext(t *testing.T) {
	a := newApp(t)
	roles := seedRoles(t, a)
	outletA := createUnit(t, a, "Ganti Password Outlet A")
	outletB := createUnit(t, a, "Ganti Password Outlet B")

	login := loginAs(t, a, "ganti_password_tanpa_konteks", []model.GrantRequest{
		{IDRole: roles["INVENTARIS"], IDUnitKerja: &outletA},
		{IDRole: roles["INVENTARIS"], IDUnitKerja: &outletB},
	})

	if login.Aktif != nil {
		t.Fatal("precondition failed: login should have no active context with two grants")
	}

	if _, err := a.auth.ChangePassword(ctx(), login.User.ID, &model.ChangePasswordRequest{
		PasswordLama: testPassword,
		PasswordBaru: "rahasia_baru_123",
	}); err != nil {
		t.Errorf("change password with no active context: %v", err)
	}
}

// The target is always the caller's own session id, a usecase parameter
// rather than anything the request body carries — so there is no field to
// smuggle another user's id through in the first place.
func TestChangePasswordOnlyAffectsTheTargetUser(t *testing.T) {
	a := newApp(t)

	loginA := loginAs(t, a, "ganti_password_a", nil)
	_ = loginAs(t, a, "ganti_password_b", nil)

	if _, err := a.auth.ChangePassword(ctx(), loginA.User.ID, &model.ChangePasswordRequest{
		PasswordLama: testPassword,
		PasswordBaru: "rahasia_baru_123",
	}); err != nil {
		t.Fatalf("change user A's password: %v", err)
	}

	if _, err := a.auth.Login(ctx(), testLoginIP, &model.LoginRequest{
		Username: "ganti_password_b", Password: testPassword,
	}); err != nil {
		t.Errorf("changing user A's password affected user B: %v", err)
	}
}

// ---------------------------------------------------------------------------
// isu #24 fase 2: refresh tokens and logout.
// ---------------------------------------------------------------------------

// Refresh mints a brand new access/refresh pair and burns the old refresh
// token — a second attempt with the same value is the reuse case and must be
// refused.
func TestRefreshRotatesTokenAndRejectsReuse(t *testing.T) {
	a := newApp(t)
	login := loginAs(t, a, "refresh_rotasi", nil)

	if login.RefreshToken == "" {
		t.Fatal("login did not return a refresh token")
	}

	refreshed, err := a.auth.Refresh(ctx(), &model.RefreshTokenRequest{RefreshToken: login.RefreshToken})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if refreshed.RefreshToken == "" || refreshed.RefreshToken == login.RefreshToken {
		t.Error("refresh did not return a new, different refresh token")
	}

	// Not asserting refreshed.Token != login.Token: JWT timestamps are
	// second-precision, so an access token minted in the same wall-clock
	// second as the one it replaces can be byte-identical — same claims, same
	// IssuedAt/ExpiresAt. That is harmless (both authorize identically); what
	// matters is that the new one authenticates and the spent refresh token
	// does not work a second time, checked below.
	if _, err := a.auth.Authenticate(refreshed.Token); err != nil {
		t.Errorf("the new access token does not authenticate: %v", err)
	}

	_, err = a.auth.Refresh(ctx(), &model.RefreshTokenRequest{RefreshToken: login.RefreshToken})
	if err == nil {
		t.Error("the old (already rotated) refresh token still works")
	} else {
		assertKind(t, err, model.KindUnauthorized)
	}
}

// A refresh token must not double as a bearer access token — Authenticate
// only ever tries to parse a bearer token as a JWT, and an opaque refresh
// token simply fails to parse, with no special-casing needed anywhere.
func TestRefreshTokenCannotAuthenticateAsBearer(t *testing.T) {
	a := newApp(t)
	login := loginAs(t, a, "refresh_bukan_bearer", nil)

	if _, err := a.auth.Authenticate(login.RefreshToken); err == nil {
		t.Error("a refresh token was accepted as a bearer access token")
	}
}

// Logout revokes the named refresh token; a subsequent refresh with it fails.
// Logging out an already-revoked token again is not an error — it is already
// the state logout wants.
func TestLogoutRevokesRefreshTokenAndIsIdempotent(t *testing.T) {
	a := newApp(t)
	login := loginAs(t, a, "logout_mencabut", nil)

	if err := a.auth.Logout(ctx(), &model.LogoutRequest{RefreshToken: login.RefreshToken}); err != nil {
		t.Fatalf("logout: %v", err)
	}

	_, err := a.auth.Refresh(ctx(), &model.RefreshTokenRequest{RefreshToken: login.RefreshToken})
	if err == nil {
		t.Error("the refresh token still works after logout")
	} else {
		assertKind(t, err, model.KindUnauthorized)
	}

	if err := a.auth.Logout(ctx(), &model.LogoutRequest{RefreshToken: login.RefreshToken}); err != nil {
		t.Errorf("logging out an already-revoked token returned an error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// isu #24 fase 3: revocation triggers.
// ---------------------------------------------------------------------------

// A password change revokes every refresh token issued before it.
func TestChangePasswordRevokesExistingRefreshTokens(t *testing.T) {
	a := newApp(t)
	login := loginAs(t, a, "ganti_password_cabut_refresh", nil)

	if _, err := a.auth.ChangePassword(ctx(), login.User.ID, &model.ChangePasswordRequest{
		PasswordLama: testPassword,
		PasswordBaru: "rahasia_baru_123",
	}); err != nil {
		t.Fatalf("change password: %v", err)
	}

	if _, err := a.auth.Refresh(ctx(), &model.RefreshTokenRequest{RefreshToken: login.RefreshToken}); err == nil {
		t.Error("a refresh token issued before the password change still works")
	}
}

// PATCH /user/{id} setting is_aktif: false revokes every refresh token that
// user held — the same trigger as a self-service password change.
func TestDeactivatingUserRevokesRefreshTokens(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)
	login := loginAs(t, a, "nonaktif_cabut_refresh", nil)

	if _, err := a.user.Update(ctx(), &model.UpdateUserRequest{
		ActorID: actor,
		ID:      login.User.ID,
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}

	if _, err := a.auth.Refresh(ctx(), &model.RefreshTokenRequest{RefreshToken: login.RefreshToken}); err == nil {
		t.Error("a refresh token still works after the user was deactivated")
	}
}

// Replacing a user's grants with [] — revoking every grant — revokes their
// refresh tokens too, the third trigger fase 3 names.
func TestRevokingEveryGrantRevokesRefreshTokens(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)
	roles := seedRoles(t, a)

	login := loginAs(t, a, "grant_dicabut_refresh", []model.GrantRequest{{IDRole: roles["CASHIER"]}})

	empty := []model.GrantRequest{}
	if _, err := a.user.Update(ctx(), &model.UpdateUserRequest{
		ActorID: actor,
		ID:      login.User.ID,
		Grants:  model.Optional[[]model.GrantRequest]{Present: true, Value: &empty},
	}); err != nil {
		t.Fatalf("revoke every grant: %v", err)
	}

	if _, err := a.auth.Refresh(ctx(), &model.RefreshTokenRequest{RefreshToken: login.RefreshToken}); err == nil {
		t.Error("a refresh token still works after every grant was revoked")
	}
}

// None of the three revocation triggers may reach a different user's tokens.
func TestRevocationDoesNotTouchAnotherUsersRefreshToken(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	loginA := loginAs(t, a, "revoke_a", nil)
	loginB := loginAs(t, a, "revoke_b", nil)

	if _, err := a.user.Update(ctx(), &model.UpdateUserRequest{
		ActorID: actor,
		ID:      loginA.User.ID,
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("deactivate user A: %v", err)
	}

	if _, err := a.auth.Refresh(ctx(), &model.RefreshTokenRequest{RefreshToken: loginB.RefreshToken}); err != nil {
		t.Errorf("an action on user A revoked user B's refresh token too: %v", err)
	}
}

// ---------------------------------------------------------------------------
// isu #24 fase 4: rate limiting instead of captcha.
// ---------------------------------------------------------------------------

// throttledAuth builds an AuthUseCase sharing the real test database and
// Redis but with its own tight attempt ceiling — newApp's own a.auth uses a
// generous limit (testMaxLoginAttempts) precisely so ordinary test traffic
// never trips it, so throttling itself needs a dedicated instance rather than
// thousands of login attempts in a loop.
func throttledAuth(t *testing.T, maxAttempts int) *usecase.AuthUseCase {
	t.Helper()

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	return usecase.NewAuthUseCase(
		testDB, log, config.NewValidator(), repository.NewUserRepository(),
		repository.NewRefreshTokenRepository(testRedis), repository.NewLoginThrottleRepository(testRedis),
		testAuthSecret, testAuthTTL, testAuthRefreshTTL, testAuthIssuer,
		maxAttempts, time.Minute,
	)
}

// Enough failures from one (ip, username) pair throttles further attempts on
// that exact pair — even one with the correct password — with the identical
// message and kind a wrong password gets, never a distinct one. A different
// ip for the same username is unaffected, proving the limit is per pair, not
// per username: one attacker spamming one victim's username from their own
// ip cannot lock that victim out from the victim's own ip.
func TestLoginThrottlesOnePairWithoutAffectingAnother(t *testing.T) {
	a := newApp(t)
	auth := throttledAuth(t, 3)

	if _, err := a.user.Create(ctx(), &model.CreateUserRequest{
		ActorID:  testActor(t),
		Username: "throttle_target",
		Password: testPassword,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	const attackerIP = "203.0.113.10"

	for range 3 {
		_, err := auth.Login(ctx(), attackerIP, &model.LoginRequest{
			Username: "throttle_target", Password: "salah-terus",
		})
		assertKind(t, err, model.KindUnauthorized)
	}

	// The pair is now throttled: even the correct password is refused, with
	// the same message a wrong password gets.
	_, err := auth.Login(ctx(), attackerIP, &model.LoginRequest{
		Username: "throttle_target", Password: testPassword,
	})
	if err == nil {
		t.Fatal("a throttled pair accepted a login with the correct password")
	}
	assertKind(t, err, model.KindUnauthorized)
	if got := err.(*model.Error).Message; got != "username or password is wrong" {
		t.Errorf("throttled message = %q, want the same message a wrong password gets", got)
	}

	// A different ip, same username: unaffected.
	if _, err := auth.Login(ctx(), "203.0.113.99", &model.LoginRequest{
		Username: "throttle_target", Password: testPassword,
	}); err != nil {
		t.Errorf("a different ip for the same username was throttled too: %v", err)
	}
}

// A successful login resets the counter, so a caller who eventually gets it
// right does not go on paying for earlier mistakes.
func TestSuccessfulLoginResetsThrottleCounter(t *testing.T) {
	a := newApp(t)
	auth := throttledAuth(t, 2)

	if _, err := a.user.Create(ctx(), &model.CreateUserRequest{
		ActorID:  testActor(t),
		Username: "throttle_reset",
		Password: testPassword,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	const ip = "203.0.113.20"

	if _, err := auth.Login(ctx(), ip, &model.LoginRequest{Username: "throttle_reset", Password: "salah"}); err == nil {
		t.Fatal("precondition failed: the wrong password should have been rejected")
	}

	if _, err := auth.Login(ctx(), ip, &model.LoginRequest{Username: "throttle_reset", Password: testPassword}); err != nil {
		t.Fatalf("login with the correct password: %v", err)
	}

	// One more failure after the reset must not immediately exhaust a
	// 2-attempt ceiling — the earlier success brought the counter back to
	// zero, so the pair is not yet throttled.
	if _, err := auth.Login(ctx(), ip, &model.LoginRequest{Username: "throttle_reset", Password: "salah lagi"}); err == nil {
		t.Fatal("precondition failed: the wrong password should have been rejected")
	}

	if _, err := auth.Login(ctx(), ip, &model.LoginRequest{Username: "throttle_reset", Password: testPassword}); err != nil {
		t.Errorf("login was throttled even though the earlier success should have reset the counter: %v", err)
	}
}
