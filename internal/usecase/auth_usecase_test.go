package usecase_test

// These tests exercise Login and SwitchContext against a real PostgreSQL,
// because what isu #12 fase 4 asks to prove — which grants are usable, and
// whether switch-context's re-check catches a role or unit retired after the
// original token was issued — lives in the database. auth_token_test.go
// (package usecase, no DB) covers the pure token mechanics; these cover the
// business rules layered on top.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

const testPassword = "rahasia123"

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

	response, err := a.auth.Login(ctx(), &model.LoginRequest{
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
		ActorID: actor,
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

	response, err := a.auth.Login(ctx(), &model.LoginRequest{Username: "role_pensiun", Password: testPassword})
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
		ActorID: actor,
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

	response, err := a.auth.Login(ctx(), &model.LoginRequest{Username: "unit_pensiun_login", Password: testPassword})
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
		ActorID: actor,
		Username: "bukan_pemilik",
		Password: testPassword,
	}); err != nil {
		t.Fatalf("create second user: %v", err)
	}

	intruder, err := a.auth.Login(ctx(), &model.LoginRequest{Username: "bukan_pemilik", Password: testPassword})
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
