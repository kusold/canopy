package canopy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/kusold/grove/apperr"
	"github.com/kusold/grove/db"
)

// widget is the tenant-scoped example resource exposed by Canopy. Only the ID
// and name are part of the public API surface; tenant ownership is enforced by
// Postgres row-level security and is never exposed to clients.
type widget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// createWidgetRequest is the JSON body for POST /example/widgets.
type createWidgetRequest struct {
	Name string `json:"name"`
}

// errWidgetNotFound is returned by the widget store when no widget matches the
// request for the current tenant. The HTTP layer maps it to 404. It deliberately
// covers malformed IDs, unknown IDs, and IDs owned by another tenant so the
// response does not leak which is which.
var errWidgetNotFound = errors.New("widget not found")

// widgetStore abstracts tenant-scoped widget persistence so handlers can be
// unit-tested without a database. The production implementation,
// pgxWidgetStore, backs every operation with db.TenantTx so row-level security
// enforces tenant isolation at the database boundary rather than in SQL.
type widgetStore interface {
	Create(ctx context.Context, name string) (widget, error)
	List(ctx context.Context) ([]widget, error)
	Get(ctx context.Context, id string) (widget, error)
}

// pgxWidgetStore implements widgetStore against Postgres using db.TenantTx.
// Every method runs inside a tenant transaction, which sets app.tenant_id as a
// transaction-local session variable. RLS policies on example_widgets filter
// rows by that variable, so each query is scoped to the calling tenant by the
// database. The tenant is taken from the request context; there is no
// application-side tenant filtering.
type pgxWidgetStore struct {
	db *db.Database
}

// Create inserts a widget for the current tenant and returns it. The inserted
// tenant_id is set to grove.current_tenant_id() so the row belongs to the
// calling tenant and satisfies the table's RLS WITH CHECK policy.
func (s *pgxWidgetStore) Create(ctx context.Context, name string) (widget, error) {
	id := uuid.NewString()
	err := s.db.TenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`insert into public.example_widgets (id, tenant_id, name)
			 values ($1, grove.current_tenant_id(), $2)`,
			id, name)
		return err
	})
	if err != nil {
		return widget{}, err
	}
	return widget{ID: id, Name: name}, nil
}

// List returns every widget owned by the current tenant, oldest first. RLS
// restricts the result set to the calling tenant.
func (s *pgxWidgetStore) List(ctx context.Context) ([]widget, error) {
	var widgets []widget
	err := s.db.TenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`select id::text, name from public.example_widgets
			 order by created_at, id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var w widget
			if err := rows.Scan(&w.ID, &w.Name); err != nil {
				return err
			}
			widgets = append(widgets, w)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return widgets, nil
}

// Get returns the widget with the given ID if it belongs to the current tenant.
// A malformed or unknown ID yields errWidgetNotFound so callers can map it to
// 404 without distinguishing an invalid ID from one owned by another tenant.
func (s *pgxWidgetStore) Get(ctx context.Context, id string) (widget, error) {
	if _, err := uuid.Parse(id); err != nil {
		return widget{}, errWidgetNotFound
	}
	var w widget
	err := s.db.TenantTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`select id::text, name from public.example_widgets
			 where id = $1`, id).Scan(&w.ID, &w.Name)
		if errors.Is(err, pgx.ErrNoRows) {
			return errWidgetNotFound
		}
		return err
	})
	if err != nil {
		return widget{}, err
	}
	return w, nil
}

// widgetAPI wires tenant-scoped widget HTTP handlers to a widgetStore. The store
// is the database boundary; handlers focus on request decoding, validation, and
// response encoding.
type widgetAPI struct {
	store widgetStore
}

// create handles POST /example/widgets. It requires a tenant (enforced by
// tenant middleware on the route group) and creates a widget for that tenant.
func (a *widgetAPI) create(w http.ResponseWriter, r *http.Request) {
	var req createWidgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeWidgetError(w, r, "invalid_json", "request body is not valid JSON", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeWidgetError(w, r, "invalid_argument", "name is required", http.StatusUnprocessableEntity)
		return
	}

	created, err := a.store.Create(r.Context(), name)
	if err != nil {
		writeWidgetError(w, r, "internal_error", "could not create widget", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Location", "/example/widgets/"+created.ID)
	writeJSON(w, http.StatusCreated, created)
}

// list handles GET /example/widgets. It returns only the widgets owned by the
// current tenant.
func (a *widgetAPI) list(w http.ResponseWriter, r *http.Request) {
	widgets, err := a.store.List(r.Context())
	if err != nil {
		writeWidgetError(w, r, "internal_error", "could not list widgets", http.StatusInternalServerError)
		return
	}
	if widgets == nil {
		widgets = []widget{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"widgets": widgets})
}

// get handles GET /example/widgets/{id}. It returns the widget only if it
// belongs to the current tenant; otherwise it responds 404.
func (a *widgetAPI) get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	got, err := a.store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, errWidgetNotFound) {
			writeWidgetError(w, r, "not_found", "widget not found", http.StatusNotFound)
			return
		}
		writeWidgetError(w, r, "internal_error", "could not get widget", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// writeJSON encodes v as a JSON response with the given status. It mirrors how
// the framework writes JSON responses.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeWidgetError writes a consistent JSON error response using grove's apperr
// package so widget errors share the framework's error envelope and include the
// request ID when present. The underlying cause is logged by the caller and is
// never exposed in the response.
func writeWidgetError(w http.ResponseWriter, r *http.Request, code, message string, status int) {
	apperr.WriteError(w, r, &apperr.Error{
		Code:       code,
		Message:    message,
		StatusCode: status,
	})
}
