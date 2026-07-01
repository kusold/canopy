package canopy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/kusold/grove"
)

// fakeWidgetStore is a test double for widgetStore. Each method delegates to a
// configurable function so individual handler branches can be exercised without
// a database.
type fakeWidgetStore struct {
	createFn func(ctx context.Context, name string) (widget, error)
	listFn   func(ctx context.Context) ([]widget, error)
	getFn    func(ctx context.Context, id string) (widget, error)

	createCalledName string
	listCalled       bool
	getCalledID      string
}

func (f *fakeWidgetStore) Create(ctx context.Context, name string) (widget, error) {
	f.createCalledName = name
	if f.createFn != nil {
		return f.createFn(ctx, name)
	}
	return widget{ID: "fake-id", Name: name}, nil
}

func (f *fakeWidgetStore) List(ctx context.Context) ([]widget, error) {
	f.listCalled = true
	if f.listFn != nil {
		return f.listFn(ctx)
	}
	return nil, nil
}

func (f *fakeWidgetStore) Get(ctx context.Context, id string) (widget, error) {
	f.getCalledID = id
	if f.getFn != nil {
		return f.getFn(ctx, id)
	}
	return widget{}, errWidgetNotFound
}

// widgetTestRouter mounts the widget handlers on a chi router at /widgets for
// handler-level testing without the full Grove app.
func widgetTestRouter(api *widgetAPI) http.Handler {
	r := chi.NewRouter()
	r.Post("/widgets", api.create)
	r.Get("/widgets", api.list)
	r.Get("/widgets/{id}", api.get)
	return r
}

func TestWidgetCreateHandler(t *testing.T) {
	t.Run("creates a widget and returns 201", func(t *testing.T) {
		store := &fakeWidgetStore{
			createFn: func(_ context.Context, name string) (widget, error) {
				return widget{ID: "abc-123", Name: name}, nil
			},
		}
		router := widgetTestRouter(&widgetAPI{store: store})

		req := httptest.NewRequest(http.MethodPost, "/widgets", bytes.NewBufferString(`{"name":"demo widget"}`))
		req.Header.Set("X-Tenant-ID", "00000000-0000-0000-0000-000000000001")
		req.Header.Set("X-Tenant-Slug", "acme")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}
		if got := rec.Header().Get("Location"); got != "/example/widgets/abc-123" {
			t.Errorf("Location = %q, want %q", got, "/example/widgets/abc-123")
		}

		var body widget
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body.ID != "abc-123" {
			t.Errorf("id = %q, want %q", body.ID, "abc-123")
		}
		if body.Name != "demo widget" {
			t.Errorf("name = %q, want %q", body.Name, "demo widget")
		}
		if store.createCalledName != "demo widget" {
			t.Errorf("store called with name = %q, want %q", store.createCalledName, "demo widget")
		}
	})

	t.Run("trims whitespace before validating and storing", func(t *testing.T) {
		store := &fakeWidgetStore{
			createFn: func(_ context.Context, name string) (widget, error) {
				return widget{ID: "abc", Name: name}, nil
			},
		}
		router := widgetTestRouter(&widgetAPI{store: store})

		req := httptest.NewRequest(http.MethodPost, "/widgets", bytes.NewBufferString(`{"name":"  demo  "}`))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
		}
		if store.createCalledName != "demo" {
			t.Errorf("store called with name = %q, want trimmed %q", store.createCalledName, "demo")
		}
	})

	t.Run("returns 400 for invalid JSON", func(t *testing.T) {
		store := &fakeWidgetStore{}
		router := widgetTestRouter(&widgetAPI{store: store})

		req := httptest.NewRequest(http.MethodPost, "/widgets", bytes.NewBufferString(`{not json`))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
		var body apperrResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if body.Error.Code != "invalid_json" {
			t.Errorf("code = %q, want %q", body.Error.Code, "invalid_json")
		}
	})

	t.Run("returns 422 when name is missing", func(t *testing.T) {
		store := &fakeWidgetStore{}
		router := widgetTestRouter(&widgetAPI{store: store})

		req := httptest.NewRequest(http.MethodPost, "/widgets", bytes.NewBufferString(`{}`))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
		}
		var body apperrResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if body.Error.Code != "invalid_argument" {
			t.Errorf("code = %q, want %q", body.Error.Code, "invalid_argument")
		}
		if store.createCalledName != "" {
			t.Errorf("store should not be called when name is missing; got %q", store.createCalledName)
		}
	})

	t.Run("returns 422 when name is whitespace only", func(t *testing.T) {
		store := &fakeWidgetStore{}
		router := widgetTestRouter(&widgetAPI{store: store})

		req := httptest.NewRequest(http.MethodPost, "/widgets", bytes.NewBufferString(`{"name":"   "}`))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
		}
	})

	t.Run("returns 500 when the store fails", func(t *testing.T) {
		store := &fakeWidgetStore{
			createFn: func(_ context.Context, _ string) (widget, error) {
				return widget{}, errors.New("boom")
			},
		}
		router := widgetTestRouter(&widgetAPI{store: store})

		req := httptest.NewRequest(http.MethodPost, "/widgets", bytes.NewBufferString(`{"name":"demo"}`))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
		var body apperrResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if body.Error.Code != "internal_error" {
			t.Errorf("code = %q, want %q", body.Error.Code, "internal_error")
		}
	})
}

func TestWidgetListHandler(t *testing.T) {
	t.Run("returns widgets as JSON", func(t *testing.T) {
		store := &fakeWidgetStore{
			listFn: func(_ context.Context) ([]widget, error) {
				return []widget{
					{ID: "1", Name: "first"},
					{ID: "2", Name: "second"},
				}, nil
			},
		}
		router := widgetTestRouter(&widgetAPI{store: store})

		req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		var body struct {
			Widgets []widget `json:"widgets"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(body.Widgets) != 2 {
			t.Fatalf("widgets = %d, want 2", len(body.Widgets))
		}
		if body.Widgets[0].ID != "1" || body.Widgets[1].Name != "second" {
			t.Errorf("widgets = %+v, want ordered first/second", body.Widgets)
		}
	})

	t.Run("returns an empty array when there are no widgets", func(t *testing.T) {
		store := &fakeWidgetStore{
			listFn: func(_ context.Context) ([]widget, error) {
				return nil, nil
			},
		}
		router := widgetTestRouter(&widgetAPI{store: store})

		req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if raw := rec.Body.String(); raw == `{"widgets":null}` || raw == `{"widgets":null}`+"\n" {
			t.Fatalf("list should emit an empty array, got %q", raw)
		}
		var body struct {
			Widgets []widget `json:"widgets"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body.Widgets == nil {
			t.Error("widgets should be a non-nil empty array, got nil")
		}
	})

	t.Run("returns 500 when the store fails", func(t *testing.T) {
		store := &fakeWidgetStore{
			listFn: func(_ context.Context) ([]widget, error) {
				return nil, errors.New("boom")
			},
		}
		router := widgetTestRouter(&widgetAPI{store: store})

		req := httptest.NewRequest(http.MethodGet, "/widgets", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
	})
}

func TestWidgetGetHandler(t *testing.T) {
	t.Run("returns the widget when found", func(t *testing.T) {
		store := &fakeWidgetStore{
			getFn: func(_ context.Context, id string) (widget, error) {
				return widget{ID: id, Name: "demo widget"}, nil
			},
		}
		router := widgetTestRouter(&widgetAPI{store: store})

		req := httptest.NewRequest(http.MethodGet, "/widgets/abc-123", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if store.getCalledID != "abc-123" {
			t.Errorf("store called with id = %q, want %q", store.getCalledID, "abc-123")
		}
		var body widget
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body.ID != "abc-123" || body.Name != "demo widget" {
			t.Errorf("body = %+v, want id abc-123 name demo widget", body)
		}
	})

	t.Run("returns 404 when the widget is not found", func(t *testing.T) {
		store := &fakeWidgetStore{
			getFn: func(_ context.Context, _ string) (widget, error) {
				return widget{}, errWidgetNotFound
			},
		}
		router := widgetTestRouter(&widgetAPI{store: store})

		req := httptest.NewRequest(http.MethodGet, "/widgets/missing", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
		var body apperrResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if body.Error.Code != "not_found" {
			t.Errorf("code = %q, want %q", body.Error.Code, "not_found")
		}
	})

	t.Run("returns 500 when the store fails with another error", func(t *testing.T) {
		store := &fakeWidgetStore{
			getFn: func(_ context.Context, _ string) (widget, error) {
				return widget{}, errors.New("boom")
			},
		}
		router := widgetTestRouter(&widgetAPI{store: store})

		req := httptest.NewRequest(http.MethodGet, "/widgets/abc", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
	})
}

// apperrResponse mirrors grove's apperr error envelope so tests can decode
// error responses without importing the framework type.
type apperrResponse struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

// widgetTestApp builds a Grove app with the canopy module registered so route
// wiring and tenant gating can be exercised without a connected database.
// RequireMiddleware rejects missing-tenant requests before any store call, so
// the database never needs to be reachable for these tests.
func widgetTestApp(t *testing.T, opts ...grove.Option) *grove.App {
	t.Helper()

	allOpts := []grove.Option{grove.WithHTTP(), grove.WithTenancy()}
	allOpts = append(allOpts, opts...)

	app, err := grove.NewApp("canopy", allOpts...)
	if err != nil {
		t.Fatalf("NewApp() error: %v", err)
	}

	if err := (Module{}).Register(context.Background(), app); err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	return app
}

func TestWidgetRouteWiring(t *testing.T) {
	tenantHeaders := func(req *http.Request) {
		req.Header.Set("X-Tenant-ID", "00000000-0000-0000-0000-000000000001")
		req.Header.Set("X-Tenant-Slug", "acme")
	}

	t.Run("widget routes are registered when Postgres is enabled", func(t *testing.T) {
		app := widgetTestApp(t, grove.WithPostgres())

		// A missing tenant is rejected by RequireMiddleware before the store is
		// touched, proving the route group exists and is tenant-gated.
		req := httptest.NewRequest(http.MethodPost, "/example/widgets", bytes.NewBufferString(`{"name":"demo"}`))
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("POST without tenant status = %d, want %d (tenant_required)", rec.Code, http.StatusUnprocessableEntity)
		}

		// GET by id is likewise tenant-gated.
		req = httptest.NewRequest(http.MethodGet, "/example/widgets/some-id", nil)
		rec = httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("GET by id without tenant status = %d, want %d (tenant_required)", rec.Code, http.StatusUnprocessableEntity)
		}
	})

	t.Run("widget routes require a tenant header", func(t *testing.T) {
		app := widgetTestApp(t, grove.WithPostgres())

		// Even though no database is connected, RequireMiddleware runs first.
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req := httptest.NewRequest(method, "/example/widgets", nil)
			rec := httptest.NewRecorder()
			app.HTTP().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s /example/widgets without tenant status = %d, want %d", method, rec.Code, http.StatusUnprocessableEntity)
			}
		}
	})

	t.Run("widget routes are absent when Postgres is not enabled", func(t *testing.T) {
		app := widgetTestApp(t) // HTTP + Tenancy only, no Postgres

		req := httptest.NewRequest(http.MethodPost, "/example/widgets", bytes.NewBufferString(`{"name":"demo"}`))
		tenantHeaders(req)
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d (route should not be registered without Postgres)", rec.Code, http.StatusNotFound)
		}
	})

	t.Run("non-widget example routes still work without Postgres", func(t *testing.T) {
		app := widgetTestApp(t)

		req := httptest.NewRequest(http.MethodGet, "/example/hello", nil)
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("/example/hello status = %d, want %d", rec.Code, http.StatusOK)
		}
	})
}

// TestWidgetStoreContract documents the widgetStore interface contract that the
// fake satisfies, guarding against accidental interface drift.
func TestWidgetStoreContract(t *testing.T) {
	var _ widgetStore = (*fakeWidgetStore)(nil)
	var _ widgetStore = (*pgxWidgetStore)(nil)
}
