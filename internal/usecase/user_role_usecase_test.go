package usecase_test

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"

	"golang.org/x/crypto/bcrypt"
)

// seedRoles creates the three roles the application ships with and returns their
// ids by name. truncateMaster empties role, so every test that needs them calls
// this rather than relying on db/seeder_postgres/003_role.sql having run.
func seedRoles(t *testing.T, testApp *app) map[string]int64 {
	t.Helper()

	ids := make(map[string]int64, 3)

	for _, nama := range []string{"SUPERADMIN", "CASHIER", "INVENTARIS"} {
		role, err := testApp.role.Create(ctx(), &model.CreateRoleRequest{Nama: nama})
		if err != nil {
			t.Fatalf("create role %s: %v", nama, err)
		}

		ids[nama] = role.ID
	}

	return ids
}

// grants builds a []model.GrantRequest granting each role id globally (every
// unit_kerja) — the shape most of these tests want, since grant scoping to one
// unit (isu #12 fase 3) is exercised by its own tests further down.
func grants(roleIDs ...int64) []model.GrantRequest {
	list := make([]model.GrantRequest, len(roleIDs))
	for i, id := range roleIDs {
		list[i] = model.GrantRequest{IDRole: id}
	}

	return list
}

// roleNames flattens a user's roles for comparison.
func roleNames(user *model.UserResponse) []string {
	names := make([]string, len(user.Roles))
	for i, role := range user.Roles {
		names[i] = role.Nama
	}

	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// The requirement in one test: one user, several roles at once.
func TestUserHoldsManyRoles(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "kasir_gudang",
		Password: "rahasia123",
		Grants:   grants(roles["CASHIER"], roles["INVENTARIS"]),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// FindRolesByUserIDs orders by role nama, so the order is deterministic.
	if want := []string{"CASHIER", "INVENTARIS"}; !equalStrings(roleNames(user), want) {
		t.Fatalf("roles = %v, want %v", roleNames(user), want)
	}

	// And they survive a re-read rather than only existing in the create response.
	fetched, err := testApp.user.Get(ctx(), &model.GetUserRequest{ID: user.ID})
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	if want := []string{"CASHIER", "INVENTARIS"}; !equalStrings(roleNames(fetched), want) {
		t.Fatalf("roles after get = %v, want %v", roleNames(fetched), want)
	}
}

// This is the regression test for the bug migration 000010 fixes.
//
// Migration 000002 declared UNIQUE (role_active) on users, which is unique across
// the whole table rather than per user — so the system could hold exactly one
// cashier, and the second was rejected by the database. Nothing about the Go code
// would have hinted at it, which is why the check lives here and not in a comment.
func TestManyUsersShareTheSameRole(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	for _, username := range []string{"kasir_satu", "kasir_dua", "kasir_tiga"} {
		user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
			Username: username,
			Password: "rahasia123",
			Grants:   grants(roles["CASHIER"]),
		})
		if err != nil {
			t.Fatalf("create %s: %v", username, err)
		}

		if want := []string{"CASHIER"}; !equalStrings(roleNames(user), want) {
			t.Fatalf("%s roles = %v, want %v", username, roleNames(user), want)
		}
	}

	// All three come back under the role filter.
	list, paging, err := testApp.user.Search(ctx(), &model.ListUserRequest{
		RoleID: ptr(roles["CASHIER"]),
	})
	if err != nil {
		t.Fatalf("search by role: %v", err)
	}

	if paging.TotalItem != 3 {
		t.Fatalf("total_item = %d, want 3", paging.TotalItem)
	}

	if len(list) != 3 {
		t.Fatalf("len(list) = %d, want 3", len(list))
	}
}

// A password must never be stored in the clear, and the stored hash must never come
// back out. UserResponse has no password field at all, so the second half is
// enforced by the type — what needs checking is the column.
func TestUserPasswordIsHashedAndNeverReturned(t *testing.T) {
	testApp := newApp(t)

	const plaintext = "rahasia123"

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "budi",
		Password: plaintext,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	var stored string
	if err := testDB.QueryRow("SELECT password FROM users WHERE id = $1", user.ID).Scan(&stored); err != nil {
		t.Fatalf("read password column: %v", err)
	}

	if stored == plaintext {
		t.Fatal("password was stored in plaintext")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(plaintext)); err != nil {
		t.Fatalf("stored value is not a bcrypt hash of the password: %v", err)
	}

	// A patched password is hashed on the same path.
	if _, err := testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID:       user.ID,
		Password: model.Optional[string]{Present: true, Value: ptr("password_baru")},
	}); err != nil {
		t.Fatalf("patch password: %v", err)
	}

	if err := testDB.QueryRow("SELECT password FROM users WHERE id = $1", user.ID).Scan(&stored); err != nil {
		t.Fatalf("re-read password column: %v", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte("password_baru")); err != nil {
		t.Fatalf("patched password is not a bcrypt hash of the new password: %v", err)
	}
}

// grants replaces the set rather than adding to it, and the three Optional states
// have to stay distinguishable: absent, [], and a list.
func TestUserPatchReplacesRoleSet(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "siti",
		Password: "rahasia123",
		Grants:   grants(roles["CASHIER"]),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Absent grants: a patch touching another field leaves the grants alone.
	patched, err := testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID:          user.ID,
		NamaLengkap: model.Optional[string]{Present: true, Value: ptr("Siti Aminah")},
	})
	if err != nil {
		t.Fatalf("patch nama_lengkap: %v", err)
	}

	if want := []string{"CASHIER"}; !equalStrings(roleNames(patched), want) {
		t.Fatalf("roles after unrelated patch = %v, want %v", roleNames(patched), want)
	}

	// A list replaces the whole set: CASHIER goes away, the two named arrive.
	replaced := grants(roles["SUPERADMIN"], roles["INVENTARIS"])

	patched, err = testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID:     user.ID,
		Grants: model.Optional[[]model.GrantRequest]{Present: true, Value: &replaced},
	})
	if err != nil {
		t.Fatalf("replace roles: %v", err)
	}

	if want := []string{"INVENTARIS", "SUPERADMIN"}; !equalStrings(roleNames(patched), want) {
		t.Fatalf("roles after replace = %v, want %v", roleNames(patched), want)
	}

	// An empty array revokes everything, and serialises as [] rather than null.
	empty := []model.GrantRequest{}

	patched, err = testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID:     user.ID,
		Grants: model.Optional[[]model.GrantRequest]{Present: true, Value: &empty},
	})
	if err != nil {
		t.Fatalf("revoke roles: %v", err)
	}

	if len(patched.Roles) != 0 {
		t.Fatalf("roles after revoke = %v, want none", roleNames(patched))
	}

	if patched.Roles == nil {
		t.Error("Roles is nil; it must be an empty slice so it serialises as [] not null")
	}
}

// An explicit null is not a third way to say "no grants" — [] already says it.
func TestUserPatchRejectsNullGrants(t *testing.T) {
	testApp := newApp(t)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "joko",
		Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	_, err = testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID:     user.ID,
		Grants: model.Optional[[]model.GrantRequest]{Present: true}, // present, Value nil = null
	})

	assertKind(t, err, model.KindInvalid)
}

// A patch carrying only grants changes no column of users, but it is still a
// change to the user: updated_at has to move, and an unknown id still has to 404.
func TestUserRolesOnlyPatchBumpsUpdatedAt(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "dewi",
		Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	granted := grants(roles["INVENTARIS"])

	patched, err := testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID:     user.ID,
		Grants: model.Optional[[]model.GrantRequest]{Present: true, Value: &granted},
	})
	if err != nil {
		t.Fatalf("roles-only patch: %v", err)
	}

	if !patched.UpdatedAt.After(user.UpdatedAt) {
		t.Errorf("updated_at did not move: was %v, now %v", user.UpdatedAt, patched.UpdatedAt)
	}

	if want := []string{"INVENTARIS"}; !equalStrings(roleNames(patched), want) {
		t.Fatalf("roles = %v, want %v", roleNames(patched), want)
	}

	// Same shape of patch against an id that does not exist.
	_, err = testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID:     user.ID + 10_000,
		Grants: model.Optional[[]model.GrantRequest]{Present: true, Value: &granted},
	})

	assertKind(t, err, model.KindNotFound)
}

// Replacing a set that keeps a role must not re-grant it: user_role.created_at is
// the record of when the grant actually started.
func TestUserRoleGrantTimestampSurvivesReplace(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "agus",
		Password: "rahasia123",
		Grants:   grants(roles["CASHIER"]),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	grantedAt := func() string {
		t.Helper()

		var at string
		if err := testDB.QueryRow(
			"SELECT created_at::TEXT FROM user_role WHERE user_id = $1 AND role_id = $2",
			user.ID, roles["CASHIER"],
		).Scan(&at); err != nil {
			t.Fatalf("read user_role created_at: %v", err)
		}

		return at
	}

	before := grantedAt()

	// CASHIER stays, INVENTARIS is added.
	replaced := grants(roles["CASHIER"], roles["INVENTARIS"])

	if _, err := testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID:     user.ID,
		Grants: model.Optional[[]model.GrantRequest]{Present: true, Value: &replaced},
	}); err != nil {
		t.Fatalf("replace roles: %v", err)
	}

	if after := grantedAt(); after != before {
		t.Errorf("CASHIER was revoked and re-granted: created_at moved from %s to %s", before, after)
	}
}

// A body repeating a pair plainly means "grant that once". It is not a conflict,
// and it must not trip the count check that validates the ids.
func TestUserDuplicateGrantsAreCollapsed(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "rina",
		Password: "rahasia123",
		Grants:   grants(roles["CASHIER"], roles["CASHIER"], roles["CASHIER"]),
	})
	if err != nil {
		t.Fatalf("create user with repeated role: %v", err)
	}

	if want := []string{"CASHIER"}; !equalStrings(roleNames(user), want) {
		t.Fatalf("roles = %v, want %v", roleNames(user), want)
	}
}

func TestUserUnknownRoleIDIsInvalid(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	_, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "hendra",
		Password: "rahasia123",
		Grants:   grants(roles["CASHIER"], roles["CASHIER"]+10_000),
	})

	assertKind(t, err, model.KindInvalid)

	// The whole request rolled back — no half-created user.
	var count int64
	if err := testDB.QueryRow(
		"SELECT COUNT(*) FROM users WHERE lower(username) = 'hendra'",
	).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}

	if count != 0 {
		t.Errorf("user was created despite the bad role id: count = %d", count)
	}
}

// A retired role cannot be granted. The foreign key cannot catch this — the row
// exists, it is just inactive — so it is the usecase's check that has to.
func TestUserCannotBeGrantedRetiredRole(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	if _, err := testApp.role.Update(ctx(), &model.UpdateRoleRequest{
		ID:      roles["INVENTARIS"],
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("retire role: %v", err)
	}

	_, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "wawan",
		Password: "rahasia123",
		Grants:   grants(roles["INVENTARIS"]),
	})

	assertKind(t, err, model.KindInvalid)
}

// Retiring a role after it was granted does not revoke it. The assignment is still
// real and still needs to be visible so an operator can remove it — and IsAktif is
// what stops it from looking live.
func TestRetiringRoleKeepsExistingGrantVisible(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "tono",
		Password: "rahasia123",
		Grants:   grants(roles["CASHIER"]),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := testApp.role.Update(ctx(), &model.UpdateRoleRequest{
		ID:      roles["CASHIER"],
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("retire role: %v", err)
	}

	fetched, err := testApp.user.Get(ctx(), &model.GetUserRequest{ID: user.ID})
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	if len(fetched.Roles) != 1 {
		t.Fatalf("roles = %v, want the retired CASHIER still listed", roleNames(fetched))
	}

	if fetched.Roles[0].IsAktif {
		t.Error("retired role is reported as active; an operator cannot tell it apart from a live grant")
	}
}

func TestUserDuplicateUsernameIgnoresCase(t *testing.T) {
	testApp := newApp(t)

	if _, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "Bambang",
		Password: "rahasia123",
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	_, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "bambang",
		Password: "rahasia123",
	})

	assertKind(t, err, model.KindConflict)
}

// email is nullable and a unique index does not constrain NULLs, so any number of
// users may have none — the same property the nullable master kode relies on.
func TestManyUsersWithoutEmailAreAllowed(t *testing.T) {
	testApp := newApp(t)

	for _, username := range []string{"a_user", "b_user", "c_user"} {
		if _, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
			Username: username,
			Password: "rahasia123",
		}); err != nil {
			t.Fatalf("create %s without email: %v", username, err)
		}
	}

	// And a duplicate email, when supplied, still collides case-insensitively.
	if _, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "d_user",
		Email:    ptr("Kantor@Example.com"),
		Password: "rahasia123",
	}); err != nil {
		t.Fatalf("create with email: %v", err)
	}

	_, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "e_user",
		Email:    ptr("kantor@example.com"),
		Password: "rahasia123",
	})

	assertKind(t, err, model.KindConflict)
}

// An email typed by mistake has to be clearable, which is the whole reason
// Optional exists rather than a plain pointer.
func TestUserEmailCanBeCleared(t *testing.T) {
	testApp := newApp(t)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "eko",
		Email:    ptr("salah@example.com"),
		Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	patched, err := testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID:    user.ID,
		Email: model.Optional[string]{Present: true}, // explicit null
	})
	if err != nil {
		t.Fatalf("clear email: %v", err)
	}

	if patched.Email != nil {
		t.Errorf("email = %q, want nil", *patched.Email)
	}
}

// The role filter is an EXISTS, not a join. A join would emit one row per matching
// role and quietly return the same user several times on one page.
func TestUserListDoesNotDuplicateUsersWithManyRoles(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	if _, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "serba_bisa",
		Password: "rahasia123",
		Grants:   grants(roles["SUPERADMIN"], roles["CASHIER"], roles["INVENTARIS"]),
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	list, paging, err := testApp.user.Search(ctx(), &model.ListUserRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if paging.TotalItem != 1 {
		t.Errorf("total_item = %d, want 1", paging.TotalItem)
	}

	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1 — the user was multiplied by its roles", len(list))
	}

	if len(list[0].Roles) != 3 {
		t.Errorf("roles = %v, want all three", roleNames(&list[0]))
	}
}

// A user with no roles serialises with [] rather than null, so a client can read
// roles.length on every row without a nil check.
func TestUserWithoutRolesHasEmptyArray(t *testing.T) {
	testApp := newApp(t)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "belum_punya_role",
		Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if user.Roles == nil {
		t.Error("Roles is nil on create; it must be an empty slice")
	}

	list, _, err := testApp.user.Search(ctx(), &model.ListUserRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}

	if list[0].Roles == nil {
		t.Error("Roles is nil in a list row; it must be an empty slice")
	}
}

func TestUserPatchWithNoFieldsIsInvalid(t *testing.T) {
	testApp := newApp(t)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "kosong",
		Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	_, err = testApp.user.Update(ctx(), &model.UpdateUserRequest{ID: user.ID})

	assertKind(t, err, model.KindInvalid)
}

func TestUserPatchRejectsNullOnNotNullColumns(t *testing.T) {
	testApp := newApp(t)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "wajib_isi",
		Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	cases := []struct {
		name    string
		request *model.UpdateUserRequest
	}{
		{
			name: "username",
			request: &model.UpdateUserRequest{
				ID:       user.ID,
				Username: model.Optional[string]{Present: true},
			},
		},
		{
			name: "password",
			request: &model.UpdateUserRequest{
				ID:       user.ID,
				Password: model.Optional[string]{Present: true},
			},
		},
		{
			name: "is_aktif",
			request: &model.UpdateUserRequest{
				ID:      user.ID,
				IsAktif: model.Optional[bool]{Present: true},
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := testApp.user.Update(ctx(), testCase.request)

			assertKind(t, err, model.KindInvalid)
		})
	}
}

// A user is retired with is_aktif, never deleted: users is referenced by every
// created_by column in the schema.
func TestUserCanBeRetiredAndFiltered(t *testing.T) {
	testApp := newApp(t)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "mantan_pegawai",
		Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID:      user.ID,
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("retire user: %v", err)
	}

	active, _, err := testApp.user.Search(ctx(), &model.ListUserRequest{IsAktif: ptr(true)})
	if err != nil {
		t.Fatalf("search active: %v", err)
	}

	if len(active) != 0 {
		t.Errorf("retired user still appears under is_aktif=true: %d rows", len(active))
	}

	retired, _, err := testApp.user.Search(ctx(), &model.ListUserRequest{IsAktif: ptr(false)})
	if err != nil {
		t.Fatalf("search retired: %v", err)
	}

	if len(retired) != 1 {
		t.Errorf("retired user not found under is_aktif=false: %d rows", len(retired))
	}
}

// Search covers username, nama_lengkap, and email, and every term goes through
// EscapeLike — a user searching for "100%" must not match everything.
func TestUserSearchMatchesNamaLengkapAndEscapesWildcards(t *testing.T) {
	testApp := newApp(t)

	if _, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username:    "operator1",
		NamaLengkap: ptr("Budi Santoso"),
		Password:    "rahasia123",
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username:    "operator2",
		NamaLengkap: ptr("Diskon 100% Tunai"),
		Password:    "rahasia123",
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	found, _, err := testApp.user.Search(ctx(), &model.ListUserRequest{Search: "Santoso"})
	if err != nil {
		t.Fatalf("search nama_lengkap: %v", err)
	}

	if len(found) != 1 || found[0].Username != "operator1" {
		t.Fatalf("search on nama_lengkap returned %d rows, want just operator1", len(found))
	}

	// Unescaped, the % would match both rows.
	found, _, err = testApp.user.Search(ctx(), &model.ListUserRequest{Search: "100%"})
	if err != nil {
		t.Fatalf("search literal percent: %v", err)
	}

	if len(found) != 1 || found[0].Username != "operator2" {
		t.Fatalf("search for \"100%%\" returned %d rows, want just operator2", len(found))
	}
}

func TestRoleDuplicateNamaIgnoresCase(t *testing.T) {
	testApp := newApp(t)

	if _, err := testApp.role.Create(ctx(), &model.CreateRoleRequest{Nama: "CASHIER"}); err != nil {
		t.Fatalf("create role: %v", err)
	}

	_, err := testApp.role.Create(ctx(), &model.CreateRoleRequest{Nama: "cashier"})

	assertKind(t, err, model.KindConflict)
}

func TestRoleGetUnknownIDIsNotFound(t *testing.T) {
	testApp := newApp(t)

	_, err := testApp.role.Get(ctx(), &model.GetRoleRequest{ID: 999_999})

	assertKind(t, err, model.KindNotFound)
}

func TestRolePatchWithNoFieldsIsInvalid(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	_, err := testApp.role.Update(ctx(), &model.UpdateRoleRequest{ID: roles["CASHIER"]})

	assertKind(t, err, model.KindInvalid)
}

// The seeder's three roles list in a stable order, and the page ends on a unique
// column so no role can appear twice.
func TestRoleListIsOrderedAndPaged(t *testing.T) {
	testApp := newApp(t)
	seedRoles(t, testApp)

	first, paging, err := testApp.role.Search(ctx(), &model.ListRoleRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 2},
	})
	if err != nil {
		t.Fatalf("search page 1: %v", err)
	}

	if paging.TotalItem != 3 || paging.TotalPage != 2 {
		t.Fatalf("paging = %+v, want total_item 3 over 2 pages", paging)
	}

	second, _, err := testApp.role.Search(ctx(), &model.ListRoleRequest{
		PageRequest: model.PageRequest{Page: 2, Size: 2},
	})
	if err != nil {
		t.Fatalf("search page 2: %v", err)
	}

	seen := make(map[int64]bool, 3)
	for _, role := range append(append([]model.RoleResponse{}, first...), second...) {
		if seen[role.ID] {
			t.Errorf("role %d (%s) appeared on both pages", role.ID, role.Nama)
		}

		seen[role.ID] = true
	}

	if len(seen) != 3 {
		t.Errorf("saw %d distinct roles across both pages, want 3", len(seen))
	}
}

// ---------------------------------------------------------------------------
// isu #12 fase 3: wewenang bertempat — a grant is now (role, unit_kerja), not
// just role.
// ---------------------------------------------------------------------------

// createUnit is a small helper so each of the tests below can build its own
// unit_kerja without repeating the boilerplate.
func createUnit(t *testing.T, testApp *app, nama string) int64 {
	t.Helper()

	unit, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{Nama: nama})
	if err != nil {
		t.Fatalf("create unit kerja %s: %v", nama, err)
	}

	return unit.ID
}

// The design's whole point: the same role, held at two different units, is two
// grants — not a duplicate to collapse and not a conflict to reject.
func TestUserHoldsSameRoleInTwoUnits(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)
	outletA := createUnit(t, testApp, "Outlet A")
	outletB := createUnit(t, testApp, "Outlet B")

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "budi_dua_outlet",
		Password: "rahasia123",
		Grants: []model.GrantRequest{
			{IDRole: roles["INVENTARIS"], IDUnitKerja: &outletA},
			{IDRole: roles["INVENTARIS"], IDUnitKerja: &outletB},
		},
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if len(user.Roles) != 2 {
		t.Fatalf("roles = %v, want two distinct grants of INVENTARIS", user.Roles)
	}

	seenUnits := make(map[int64]bool, 2)
	for _, role := range user.Roles {
		if role.Nama != "INVENTARIS" {
			t.Errorf("role = %s, want INVENTARIS on both grants", role.Nama)
		}
		if role.IDUnitKerja == nil {
			t.Fatal("id_unit_kerja is nil on a scoped grant")
		}
		seenUnits[*role.IDUnitKerja] = true
	}

	if !seenUnits[outletA] || !seenUnits[outletB] {
		t.Errorf("units seen = %v, want both %d and %d", seenUnits, outletA, outletB)
	}
}

// A nil id_unit_kerja grants the role everywhere — the shape the seeded
// SUPERADMIN grant takes. The response must carry that back as a nil
// id_unit_kerja, not as some sentinel value.
func TestUserGlobalGrantHasNilUnitKerja(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "admin_kedua",
		Password: "rahasia123",
		Grants:   []model.GrantRequest{{IDRole: roles["SUPERADMIN"]}},
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if len(user.Roles) != 1 {
		t.Fatalf("roles = %v, want exactly one grant", user.Roles)
	}

	if user.Roles[0].IDUnitKerja != nil {
		t.Errorf("id_unit_kerja = %d, want nil for a global grant", *user.Roles[0].IDUnitKerja)
	}
}

// A grant naming a retired unit_kerja must be refused, the same as a grant
// naming a retired role — the foreign key alone cannot tell a retired unit from
// a live one.
func TestUserCannotBeGrantedRetiredUnitKerja(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)
	unit := createUnit(t, testApp, "Unit Pensiun")

	if _, err := testApp.unitKerja.Update(ctx(), &model.UpdateUnitKerjaRequest{
		ID:      unit,
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("retire unit: %v", err)
	}

	_, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "grant_unit_mati",
		Password: "rahasia123",
		Grants:   []model.GrantRequest{{IDRole: roles["INVENTARIS"], IDUnitKerja: &unit}},
	})

	assertKind(t, err, model.KindInvalid)
}

// An unknown unit_kerja id is rejected the same way an unknown role id is.
func TestUserUnknownUnitKerjaIDIsInvalid(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	unknown := int64(999_999)

	_, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "unit_tidak_ada",
		Password: "rahasia123",
		Grants:   []model.GrantRequest{{IDRole: roles["INVENTARIS"], IDUnitKerja: &unknown}},
	})

	assertKind(t, err, model.KindInvalid)
}

// This is the regression test for the trap the issue calls out explicitly:
// `role_id <> ALL($2)` against a NULL array deletes nothing, because any
// comparison with NULL is NULL rather than true. ReplaceRoles' anti-join has to
// use IS NOT DISTINCT FROM so a cross-unit (nil id_unit_kerja) grant is really
// revoked when it is left out of a replacement set — not silently kept because
// nothing could prove it should go.
func TestUserRevokingGlobalGrantActuallyDeletesIt(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "cabut_global",
		Password: "rahasia123",
		Grants:   []model.GrantRequest{{IDRole: roles["SUPERADMIN"]}},
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	empty := []model.GrantRequest{}

	patched, err := testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID:     user.ID,
		Grants: model.Optional[[]model.GrantRequest]{Present: true, Value: &empty},
	})
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if len(patched.Roles) != 0 {
		t.Fatalf("roles after revoke = %v, want none", patched.Roles)
	}

	var count int64
	if err := testDB.QueryRow(
		"SELECT COUNT(*) FROM user_role WHERE user_id = $1", user.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count user_role: %v", err)
	}

	if count != 0 {
		t.Errorf("user_role still has %d row(s) after revoking the only (global) grant", count)
	}
}

// Replacing a set that keeps one scoped grant and drops another, in the
// presence of a global grant too, has to sort out all three kinds of row
// (kept, deleted, inserted) correctly under the NULL-safe diff.
func TestUserReplaceGrantsMixesGlobalAndScoped(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)
	outletA := createUnit(t, testApp, "Mixed Outlet A")
	outletB := createUnit(t, testApp, "Mixed Outlet B")

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "campuran",
		Password: "rahasia123",
		Grants: []model.GrantRequest{
			{IDRole: roles["SUPERADMIN"]},                        // global, should be kept
			{IDRole: roles["INVENTARIS"], IDUnitKerja: &outletA}, // should be dropped
		},
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	replaced := []model.GrantRequest{
		{IDRole: roles["SUPERADMIN"]},                        // kept
		{IDRole: roles["INVENTARIS"], IDUnitKerja: &outletB}, // new
	}

	patched, err := testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID:     user.ID,
		Grants: model.Optional[[]model.GrantRequest]{Present: true, Value: &replaced},
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}

	if len(patched.Roles) != 2 {
		t.Fatalf("roles = %v, want exactly 2 grants", patched.Roles)
	}

	var sawGlobalSuperadmin, sawOutletB, sawOutletA bool
	for _, role := range patched.Roles {
		switch {
		case role.Nama == "SUPERADMIN" && role.IDUnitKerja == nil:
			sawGlobalSuperadmin = true
		case role.Nama == "INVENTARIS" && role.IDUnitKerja != nil && *role.IDUnitKerja == outletB:
			sawOutletB = true
		case role.Nama == "INVENTARIS" && role.IDUnitKerja != nil && *role.IDUnitKerja == outletA:
			sawOutletA = true
		}
	}

	if !sawGlobalSuperadmin {
		t.Error("the kept global SUPERADMIN grant is missing")
	}
	if !sawOutletB {
		t.Error("the new INVENTARIS-at-outlet-B grant is missing")
	}
	if sawOutletA {
		t.Error("the dropped INVENTARIS-at-outlet-A grant is still present")
	}
}

// Retiring the unit a grant points at does not revoke the grant, the same rule
// that already applies to a retired role: the assignment is still real and
// still needs to be visible so an operator can remove it.
func TestRetiringUnitKerjaKeepsExistingGrantVisible(t *testing.T) {
	testApp := newApp(t)
	roles := seedRoles(t, testApp)
	unit := createUnit(t, testApp, "Unit Akan Pensiun")

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "grant_unit_pensiun",
		Password: "rahasia123",
		Grants:   []model.GrantRequest{{IDRole: roles["INVENTARIS"], IDUnitKerja: &unit}},
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := testApp.unitKerja.Update(ctx(), &model.UpdateUnitKerjaRequest{
		ID:      unit,
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("retire unit: %v", err)
	}

	fetched, err := testApp.user.Get(ctx(), &model.GetUserRequest{ID: user.ID})
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

	if len(fetched.Roles) != 1 {
		t.Fatalf("roles = %v, want the grant at the retired unit still listed", fetched.Roles)
	}

	if fetched.Roles[0].IDUnitKerja == nil || *fetched.Roles[0].IDUnitKerja != unit {
		t.Errorf("id_unit_kerja = %v, want %d", fetched.Roles[0].IDUnitKerja, unit)
	}
}
