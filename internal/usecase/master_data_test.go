package usecase_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// masterModule adapts one master-data module to the checks that apply to all of
// them. Writing these once is the point: the four modules share a contract, and a
// per-module copy of each check would drift.
type masterModule struct {
	name string
	// createNamed inserts a row whose display name is nama and returns its id.
	createNamed func(nama string) (int64, error)
	// list returns one page: the ids and names in order, the reported total, and
	// the response slice as it will be marshalled.
	list func(search string, page, size int) (ids []int64, names []string, total int64, data any, err error)
	get  func(id int64) error
	// patchAktif flips is_aktif and nothing else, returning the stored nama so a
	// caller can prove untouched fields stay untouched. nil for modules without
	// PATCH.
	patchAktif func(id int64, aktif bool) (nama string, err error)
	// namesAreUnique marks the tables where two rows cannot share a display name,
	// so a duplicate-name fixture is impossible rather than merely untested.
	namesAreUnique bool
	// table is used only to simulate a concurrent writer, which is what makes the
	// unstable-ordering bug observable.
	table string
}

func masterModules(t *testing.T, a *app) []masterModule {
	// isu #23 made Create/Update on every module below require a real ActorID.
	// A function rather than one id seeded up front: several callers of
	// masterModules re-run truncateMaster per subtest (wiping users along with
	// everything else) after this constructor already returned, so an id
	// captured once here would point at a row already gone by the time a
	// closure runs. Calling testActor(t) fresh on every use costs a throwaway
	// row per call, which nothing here minds.
	actor := func() int64 { return testActor(t) }

	return []masterModule{
		{
			name:           "satuan",
			table:          "satuan",
			namesAreUnique: true,
			createNamed: func(nama string) (int64, error) {
				response, err := a.satuan.Create(ctx(), &model.CreateSatuanRequest{ActorID: actor(), Nama: nama})
				if err != nil {
					return 0, err
				}

				return response.ID, nil
			},
			list: func(search string, page, size int) ([]int64, []string, int64, any, error) {
				responses, paging, err := a.satuan.Search(ctx(), &model.ListSatuanRequest{
					PageRequest: model.PageRequest{Page: page, Size: size},
					Search:      search,
				})
				if err != nil {
					return nil, nil, 0, nil, err
				}

				ids := make([]int64, len(responses))
				names := make([]string, len(responses))
				for i, response := range responses {
					ids[i], names[i] = response.ID, response.Nama
				}

				return ids, names, paging.TotalItem, responses, nil
			},
			get: func(id int64) error {
				_, err := a.satuan.Get(ctx(), &model.GetSatuanRequest{ID: id})

				return err
			},
			patchAktif: func(id int64, aktif bool) (string, error) {
				response, err := a.satuan.Update(ctx(), &model.UpdateSatuanRequest{
					ID:      id,
					ActorID: actor(),
					IsAktif: model.Optional[bool]{Present: true, Value: &aktif},
				})
				if err != nil {
					return "", err
				}

				return response.Nama, nil
			},
		},
		{
			name:           "ekspedisi",
			table:          "ekspedisi",
			namesAreUnique: true,
			createNamed: func(nama string) (int64, error) {
				response, err := a.ekspedisi.Create(ctx(), &model.CreateEkspedisiRequest{ActorID: actor(), Nama: nama})
				if err != nil {
					return 0, err
				}

				return response.ID, nil
			},
			list: func(search string, page, size int) ([]int64, []string, int64, any, error) {
				responses, paging, err := a.ekspedisi.Search(ctx(), &model.ListEkspedisiRequest{
					PageRequest: model.PageRequest{Page: page, Size: size},
					Search:      search,
				})
				if err != nil {
					return nil, nil, 0, nil, err
				}

				ids := make([]int64, len(responses))
				names := make([]string, len(responses))
				for i, response := range responses {
					ids[i], names[i] = response.ID, response.Nama
				}

				return ids, names, paging.TotalItem, responses, nil
			},
			get: func(id int64) error {
				_, err := a.ekspedisi.Get(ctx(), &model.GetEkspedisiRequest{ID: id})

				return err
			},
			patchAktif: func(id int64, aktif bool) (string, error) {
				response, err := a.ekspedisi.Update(ctx(), &model.UpdateEkspedisiRequest{
					ID:      id,
					ActorID: actor(),
					IsAktif: model.Optional[bool]{Present: true, Value: &aktif},
				})
				if err != nil {
					return "", err
				}

				return response.Nama, nil
			},
		},
		{
			name:  "supplier",
			table: "supplier",
			createNamed: func(nama string) (int64, error) {
				response, err := a.supplier.Create(ctx(), &model.CreateSupplierRequest{ActorID: actor(), Nama: nama})
				if err != nil {
					return 0, err
				}

				return response.ID, nil
			},
			list: func(search string, page, size int) ([]int64, []string, int64, any, error) {
				responses, paging, err := a.supplier.Search(ctx(), &model.ListSupplierRequest{
					PageRequest: model.PageRequest{Page: page, Size: size},
					Search:      search,
				})
				if err != nil {
					return nil, nil, 0, nil, err
				}

				ids := make([]int64, len(responses))
				names := make([]string, len(responses))
				for i, response := range responses {
					ids[i], names[i] = response.ID, response.Nama
				}

				return ids, names, paging.TotalItem, responses, nil
			},
			get: func(id int64) error {
				_, err := a.supplier.Get(ctx(), &model.GetSupplierRequest{ID: id})

				return err
			},
			patchAktif: func(id int64, aktif bool) (string, error) {
				response, err := a.supplier.Update(ctx(), &model.UpdateSupplierRequest{
					ID:      id,
					ActorID: actor(),
					IsAktif: model.Optional[bool]{Present: true, Value: &aktif},
				})
				if err != nil {
					return "", err
				}

				return response.Nama, nil
			},
		},
		{
			name:  "pelanggan",
			table: "pelanggan",
			createNamed: func(nama string) (int64, error) {
				response, err := a.pelanggan.Create(ctx(), &model.CreatePelangganRequest{ActorID: actor(), Nama: nama})
				if err != nil {
					return 0, err
				}

				return response.ID, nil
			},
			list: func(search string, page, size int) ([]int64, []string, int64, any, error) {
				responses, paging, err := a.pelanggan.Search(ctx(), &model.ListPelangganRequest{
					PageRequest: model.PageRequest{Page: page, Size: size},
					Search:      search,
				})
				if err != nil {
					return nil, nil, 0, nil, err
				}

				ids := make([]int64, len(responses))
				names := make([]string, len(responses))
				for i, response := range responses {
					ids[i], names[i] = response.ID, response.Nama
				}

				return ids, names, paging.TotalItem, responses, nil
			},
			get: func(id int64) error {
				_, err := a.pelanggan.Get(ctx(), &model.GetPelangganRequest{ID: id})

				return err
			},
			patchAktif: func(id int64, aktif bool) (string, error) {
				response, err := a.pelanggan.Update(ctx(), &model.UpdatePelangganRequest{
					ID:      id,
					ActorID: actor(),
					IsAktif: model.Optional[bool]{Present: true, Value: &aktif},
				})
				if err != nil {
					return "", err
				}

				return response.Nama, nil
			},
		},
		{
			name:           "unit_kerja",
			table:          "unit_kerja",
			namesAreUnique: false,
			createNamed: func(nama string) (int64, error) {
				response, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{ActorID: actor(), Nama: nama})
				if err != nil {
					return 0, err
				}

				return response.ID, nil
			},
			list: func(search string, page, size int) ([]int64, []string, int64, any, error) {
				responses, paging, err := a.unitKerja.Search(ctx(), &model.ListUnitKerjaRequest{
					PageRequest: model.PageRequest{Page: page, Size: size},
					Search:      search,
				})
				if err != nil {
					return nil, nil, 0, nil, err
				}

				ids := make([]int64, len(responses))
				names := make([]string, len(responses))
				for i, response := range responses {
					ids[i], names[i] = response.ID, response.Nama
				}

				return ids, names, paging.TotalItem, responses, nil
			},
			get: func(id int64) error {
				_, err := a.unitKerja.Get(ctx(), &model.GetUnitKerjaRequest{ID: id})

				return err
			},
			patchAktif: func(id int64, aktif bool) (string, error) {
				response, err := a.unitKerja.Update(ctx(), &model.UpdateUnitKerjaRequest{
					ID:      id,
					ActorID: actor(),
					IsAktif: model.Optional[bool]{Present: true, Value: &aktif},
				})
				if err != nil {
					return "", err
				}

				return response.Nama, nil
			},
		},
		{
			// ruang is included because the two bugs fixed in section 1 of isu #2
			// live in its Search. patchAktif is left nil even though isu #23 gave
			// ruang a PATCH: that endpoint's own retirement guards (stock, freeze)
			// need fixtures this generic harness has no way to build, and get their
			// own coverage in ruang_patch_test.go instead.
			//
			// Every ruang needs an id_unit_kerja since isu #12 fase 2, and
			// truncateMaster wipes unit_kerja along with ruang between subtests, so a
			// unit is created fresh on every call rather than reused across them.
			name:  "ruang",
			table: "ruang",
			createNamed: func(nama string) (int64, error) {
				unit, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{ActorID: actor(), Nama: "Unit " + nama})
				if err != nil {
					return 0, err
				}

				response, err := a.ruang.Create(ctx(), &model.CreateRuangRequest{
					ActorID:     actor(),
					NamaRuang:   nama,
					IDUnitKerja: unit.ID,
				})
				if err != nil {
					return 0, err
				}

				return response.ID, nil
			},
			list: func(search string, page, size int) ([]int64, []string, int64, any, error) {
				responses, paging, err := a.ruang.Search(ctx(), &model.ListRuangRequest{
					PageRequest: model.PageRequest{Page: page, Size: size},
					Search:      search,
				})
				if err != nil {
					return nil, nil, 0, nil, err
				}

				ids := make([]int64, len(responses))
				names := make([]string, len(responses))
				for i, response := range responses {
					ids[i], names[i] = response.ID, response.NamaRuang
				}

				return ids, names, paging.TotalItem, responses, nil
			},
			get: func(id int64) error {
				_, err := a.ruang.Get(ctx(), &model.GetRuangRequest{ID: id})

				return err
			},
		},
	}
}

// TestListEmptyIsJSONArray guards the response shape: a nil slice marshals to
// null, which forces every client to special-case "no data".
func TestListEmptyIsJSONArray(t *testing.T) {
	for _, module := range masterModules(t, newApp(t)) {
		t.Run(module.name, func(t *testing.T) {
			_, _, total, data, err := module.list("", 1, 20)
			if err != nil {
				t.Fatalf("list: %v", err)
			}

			if total != 0 {
				t.Errorf("total_item = %d, want 0", total)
			}

			encoded, err := json.Marshal(data)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			if string(encoded) != "[]" {
				t.Errorf("data = %s, want []", encoded)
			}
		})
	}
}

// TestSearchTreatsWildcardsAsText covers bug 2: unescaped % and _ from the user
// turn the filter into "match anything" and make a name containing a literal %
// unfindable.
func TestSearchTreatsWildcardsAsText(t *testing.T) {
	for _, module := range masterModules(t, newApp(t)) {
		t.Run(module.name, func(t *testing.T) {
			truncateMaster(t)

			for _, nama := range []string{"Diskon 100%", "abc", "a_c"} {
				if _, err := module.createNamed(nama); err != nil {
					t.Fatalf("create %q: %v", nama, err)
				}
			}

			cases := []struct {
				search string
				want   []string
			}{
				// A bare % must not behave as "everything".
				{search: "%", want: []string{"Diskon 100%"}},
				// _ must match only a literal underscore, not any character.
				{search: "a_c", want: []string{"a_c"}},
				// An empty search still means "no filter".
				{search: "", want: []string{"Diskon 100%", "a_c", "abc"}},
			}

			for _, testCase := range cases {
				_, names, total, _, err := module.list(testCase.search, 1, 20)
				if err != nil {
					t.Fatalf("list %q: %v", testCase.search, err)
				}

				// Compared as a set: which rows match is the point here, and the
				// order of "a_c" against "abc" depends on the database collation.
				got := slices.Clone(names)
				want := slices.Clone(testCase.want)
				slices.Sort(got)
				slices.Sort(want)

				if !slices.Equal(got, want) {
					t.Errorf("search %q returned %v, want %v", testCase.search, got, want)
				}

				if total != int64(len(testCase.want)) {
					t.Errorf("search %q total_item = %d, want %d", testCase.search, total, len(testCase.want))
				}
			}
		})
	}
}

// TestPaginationIsStableWithDuplicateNames covers bug 1: ordering by a non-unique
// column lets a row appear on two pages while another is never returned.
func TestPaginationIsStableWithDuplicateNames(t *testing.T) {
	for _, module := range masterModules(t, newApp(t)) {
		t.Run(module.name, func(t *testing.T) {
			truncateMaster(t)

			// A large tie group, not just two rows: with only a handful of rows the
			// planner happens to return them in a consistent order and the defect
			// stays hidden, so a small fixture would pass even against the bug.
			//
			// On satuan and ekspedisi the name is unique, so a tie is impossible
			// there and only the "every row is reachable" half of the bug applies.
			const tied = 20

			names := make([]string, 0, tied+2)
			for i := range tied {
				nama := "Nama Kembar"
				if module.namesAreUnique {
					nama = fmt.Sprintf("Nama Kembar %02d", i)
				}

				names = append(names, nama)
			}

			names = append(names, "Aaa", "Zzz")

			created := make(map[int64]bool)

			for _, nama := range names {
				id, err := module.createNamed(nama)
				if err != nil {
					t.Fatalf("create %q: %v", nama, err)
				}

				created[id] = true
			}

			const size = 5

			seen := make(map[int64]bool)

			for page := 1; page*size <= len(created)+size-1; page++ {
				ids, _, total, _, err := module.list("", page, size)
				if err != nil {
					t.Fatalf("list page %d: %v", page, err)
				}

				if total != int64(len(created)) {
					t.Errorf("page %d total_item = %d, want %d", page, total, len(created))
				}

				for _, id := range ids {
					if seen[id] {
						t.Errorf("id %d appeared on more than one page", id)
					}

					seen[id] = true
				}

				// Simulate the concurrent writer that makes this bug bite in
				// production: rewriting a row moves it within the heap, so the
				// unspecified order of the tied rows actually changes between two
				// page requests. Without this the pages come back in the same
				// accidental order and nothing is proven.
				rewriteEveryOtherRow(t, module.table)
			}

			// Every row must be reachable by walking the pages — the flip side of
			// the same bug.
			if len(seen) != len(created) {
				t.Errorf("walking every page yielded %d rows, want %d", len(seen), len(created))
			}

			for id := range created {
				if !seen[id] {
					t.Errorf("id %d was never returned by any page", id)
				}
			}
		})
	}
}

// TestGetUnknownIDIsNotFound pins the 404, not a 500 and not an empty 200.
func TestGetUnknownIDIsNotFound(t *testing.T) {
	for _, module := range masterModules(t, newApp(t)) {
		t.Run(module.name, func(t *testing.T) {
			// Nothing was created, so any id is absent.
			assertKind(t, module.get(999_999), model.KindNotFound)
		})
	}
}

// TestPatchLeavesAbsentFieldsAlone is the core PATCH promise: a field missing
// from the body is not touched.
func TestPatchLeavesAbsentFieldsAlone(t *testing.T) {
	for _, module := range masterModules(t, newApp(t)) {
		if module.patchAktif == nil {
			continue
		}

		t.Run(module.name, func(t *testing.T) {
			truncateMaster(t)

			const nama = "Nama Asli"

			id, err := module.createNamed(nama)
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			after, err := module.patchAktif(id, false)
			if err != nil {
				t.Fatalf("patch: %v", err)
			}

			if after != nama {
				t.Errorf("nama = %q after patching only is_aktif, want %q", after, nama)
			}
		})
	}
}

// rewriteEveryOtherRow stands in for another user editing the same table between
// two page requests. It writes through SQL rather than a usecase because that is
// the point: the ordering must hold against any writer, including one this process
// does not control.
func rewriteEveryOtherRow(t *testing.T, table string) {
	t.Helper()

	if _, err := testDB.Exec(
		"UPDATE " + table + " SET is_aktif = is_aktif WHERE id % 2 = 0",
	); err != nil {
		t.Fatalf("rewrite %s: %v", table, err)
	}
}

// assertKind asserts err is a domain error of the given kind, which is what
// decides the HTTP status in config.statusForKind.
func assertKind(t *testing.T, err error, want model.ErrorKind) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error of kind %d, got nil", want)
	}

	var domainErr *model.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected *model.Error, got %T: %v", err, err)
	}

	if domainErr.Kind != want {
		t.Errorf("kind = %d, want %d (message: %s)", domainErr.Kind, want, domainErr.Message)
	}
}
