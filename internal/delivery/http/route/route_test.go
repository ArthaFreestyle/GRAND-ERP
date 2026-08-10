// Internal test package, not route_test: it calls setupGuestRoute directly rather
// than Setup. Setup also registers the module routes, which would dereference the
// nil controllers this test does not supply — and exporting a method purely to let
// a test in would be the wrong trade.
package route

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"Arthafreestyle/ERP/docs"
	deliveryhttp "Arthafreestyle/ERP/internal/delivery/http"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// newTestApp registers only the guest routes. The module routes need controllers
// backed by a database, and none of them are under test here.
func newTestApp(t *testing.T, withDocs bool) *fiber.App {
	t.Helper()

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	app := fiber.New()

	config := RouteConfig{App: app}
	if withDocs {
		config.DocsController = deliveryhttp.NewDocsController(log)
	}

	config.setupGuestRoute()

	return app
}

func get(t *testing.T, app *fiber.App, target string) (*http.Response, []byte) {
	t.Helper()

	response, err := app.Test(httptest.NewRequest(http.MethodGet, target, nil))
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body of %s: %v", target, err)
	}

	_ = response.Body.Close()

	return response, body
}

// The docs page is only useful if it points at a spec that is actually served, and
// the two live in different files. A typo in either path yields a page that loads
// and then reports "Failed to load API definition", which is easy to miss.
func TestDocsPageAndSpecAreServedTogether(t *testing.T) {
	app := newTestApp(t, true)

	response, body := get(t, app, "/")

	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", response.StatusCode)
	}

	if contentType := response.Header.Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Errorf("GET / Content-Type = %q, want HTML", contentType)
	}

	if !bytes.Contains(body, []byte(`dom_id: "#swagger-ui"`)) {
		t.Error("GET / did not return the Swagger UI bootstrap")
	}

	// The URL the page will fetch has to be a route that exists.
	if !bytes.Contains(body, []byte(`url: "/openapi.yaml"`)) {
		t.Fatal("GET / does not point at /openapi.yaml")
	}

	response, spec := get(t, app, "/openapi.yaml")

	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /openapi.yaml status = %d, want 200", response.StatusCode)
	}

	if !bytes.Equal(spec, docs.OpenAPI) {
		t.Error("GET /openapi.yaml did not return the embedded contract verbatim")
	}
}

// go:embed fails at compile time for a missing file, but an empty or truncated
// asset would still compile. This checks the contract actually arrived.
func TestEmbeddedSpecIsTheRealContract(t *testing.T) {
	if len(docs.OpenAPI) == 0 {
		t.Fatal("embedded openapi.yaml is empty")
	}

	for _, want := range []string{"openapi:", "GRAND-ERP API", "/api/v1/user", "/api/v1/role"} {
		if !bytes.Contains(docs.OpenAPI, []byte(want)) {
			t.Errorf("embedded contract is missing %q", want)
		}
	}
}

// web.swagger = false has to remove the routes, not just stop linking to them. A
// half-disabled toggle that still serves /openapi.yaml would publish the whole API
// surface of a service that has no authentication yet.
func TestDocsRoutesAbsentWhenDisabled(t *testing.T) {
	app := newTestApp(t, false)

	for _, target := range []string{"/", "/openapi.yaml"} {
		response, _ := get(t, app, target)

		if response.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s status = %d with docs disabled, want 404", target, response.StatusCode)
		}
	}
}

// Health is not part of the docs toggle: it has to answer either way, or a
// container with docs turned off never passes its healthcheck.
func TestHealthAnswersRegardlessOfDocsToggle(t *testing.T) {
	for _, withDocs := range []bool{true, false} {
		app := newTestApp(t, withDocs)

		response, body := get(t, app, "/health")

		if response.StatusCode != http.StatusOK {
			t.Errorf("GET /health status = %d (docs=%v), want 200", response.StatusCode, withDocs)
		}

		if !bytes.Contains(body, []byte(`"status":"ok"`)) {
			t.Errorf("GET /health body = %s (docs=%v)", body, withDocs)
		}
	}
}
