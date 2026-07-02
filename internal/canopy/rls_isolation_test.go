package canopy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kusold/grove/db"
	"github.com/kusold/grove/migrate"
	"github.com/kusold/grove/tenancy"
)

// These tests prove tenant isolation is enforced by Postgres row-level security,
// not only by application-level filtering. They are the Phase 3 RLS isolation
// tests (grove issue #56) and exercise the real example_widgets table.
//
// The default testcontainers user is a superuser, and superusers bypass RLS even
// with FORCE ROW LEVEL SECURITY. To make RLS the actual isolation boundary, the
// tests connect through a non-superuser role (canopy_app) that is subject to the
// table's policy. startPostgres (defined in widgets_integration_test.go) skips in
// short mode and starts a fresh postgres:18 container per test, so each test
// begins from an empty database.

// Two distinct tenants so cross-tenant reads and writes can be attempted.
const (
	rlsTenantAID   = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	rlsTenantASlug = "acme"
	rlsTenantBID   = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	rlsTenantBSlug = "globex"

	// rlsAdminSeededID is a widget inserted directly by SystemTx (bypassing RLS
	// through the admin pool) to demonstrate the intentional escape hatch.
	rlsAdminSeededID   = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	rlsBlockedInsertID = "dddddddd-dddd-dddd-dddd-dddddddddddd"
)

// setupRLSDB starts Postgres, creates a non-superuser role that is subject to
// RLS, applies canopy's migrations (which include Grove's RLS prelude and the
// example_widgets table with its tenant isolation policy), and returns a
// Database whose application pool connects as that role.
//
// The application pool connects as canopy_app, so every query it runs is
// filtered by RLS. The admin pool connects as the superuser, which SystemTx uses
// to intentionally bypass RLS for administrative work. This mirrors production:
// the app database user is subject to RLS, while SystemTx holds a privileged
// admin connection.
func setupRLSDB(t *testing.T) *db.Database {
	t.Helper()

	// startPostgres starts a fresh postgres:18 container as the canopy
	// superuser and skips in short mode.
	ownerURL := startPostgres(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect as the superuser to create the non-superuser role and own the
	// migrated schema objects.
	ownerDB, err := db.Open(ctx, db.Config{
		URL:            ownerURL,
		MaxConns:       4,
		MinConns:       0,
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("open owner database: %v", err)
	}
	t.Cleanup(ownerDB.Close)

	// Create the non-superuser app role. The container is fresh per test, so the
	// role never pre-exists; the guard mirrors Grove's own RLS test setup.
	if _, err := ownerDB.Pool().Exec(ctx, `
		do $$
		begin
			if not exists (select from pg_roles where rolname = 'canopy_app') then
				create role canopy_app login password 'canopy_app';
			end if;
		end
		$$;

		grant connect on database canopy_test to canopy_app;
	`); err != nil {
		t.Fatalf("create canopy_app role: %v", err)
	}

	// Apply migrations as the superuser (the table owner). NewRegistry seeds
	// Grove's RLS prelude (grove schema + current_tenant_id); canopy's source
	// adds the example_widgets table with enable+force RLS and its tenant policy.
	registry := migrate.NewRegistry()
	if err := registry.Register(canopyMigrations()); err != nil {
		t.Fatalf("register canopy migrations: %v", err)
	}
	if err := registry.Run(ctx, ownerDB.Pool()); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// Grant the app role access to the schemas and the widget table. The role is
	// neither the table owner nor a superuser, so RLS policies are enforced on
	// every statement it runs.
	if _, err := ownerDB.Pool().Exec(ctx, `
		grant usage on schema grove to canopy_app;
		grant usage on schema public to canopy_app;
		grant select, insert, update, delete on public.example_widgets to canopy_app;
	`); err != nil {
		t.Fatalf("grant privileges to canopy_app: %v", err)
	}

	// Build the app-role connection URL by swapping the superuser credentials.
	appURL := strings.Replace(ownerURL, "canopy:canopy@", "canopy_app:canopy_app@", 1)

	appDB, err := db.Open(ctx, db.Config{
		URL:            appURL,   // subject to RLS
		AdminURL:       ownerURL, // privileged; SystemTx bypasses RLS
		MaxConns:       4,
		MinConns:       0,
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("open app database: %v", err)
	}
	t.Cleanup(appDB.Close)

	return appDB
}

// TestWidgetRLSIsolation proves the example_widgets table enforces tenant
// isolation through Postgres row-level security. Each subtest maps to a scenario
// from grove issue #56. Scenario 5 (missing tenant cannot reach widget routes) is
// already covered by TestWidgetRoutesIntegration, which gates requests at the
// tenant middleware before any database access.
func TestWidgetRLSIsolation(t *testing.T) {
	appDB := setupRLSDB(t)
	store := &pgxWidgetStore{db: appDB}

	tenantA := tenancy.Tenant{ID: rlsTenantAID, Slug: rlsTenantASlug}
	tenantB := tenancy.Tenant{ID: rlsTenantBID, Slug: rlsTenantBSlug}
	ctxA := tenancy.WithTenant(context.Background(), tenantA)
	ctxB := tenancy.WithTenant(context.Background(), tenantB)

	// Seed one widget per tenant up front so the isolation subtests share stable
	// fixtures.
	widgetA, err := store.Create(ctxA, "tenant A widget")
	if err != nil {
		t.Fatalf("create tenant A widget: %v", err)
	}
	widgetB, err := store.Create(ctxB, "tenant B widget")
	if err != nil {
		t.Fatalf("create tenant B widget: %v", err)
	}

	// Scenario 1: a tenant can read its own widget.
	t.Run("tenant A can read its own widget", func(t *testing.T) {
		got, err := store.Get(ctxA, widgetA.ID)
		if err != nil {
			t.Fatalf("Get() tenant A error: %v", err)
		}
		if got != widgetA {
			t.Errorf("Get() tenant A = %+v, want %+v", got, widgetA)
		}
	})

	// Scenario 2: the other tenant can read its own widget.
	t.Run("tenant B can read its own widget", func(t *testing.T) {
		got, err := store.Get(ctxB, widgetB.ID)
		if err != nil {
			t.Fatalf("Get() tenant B error: %v", err)
		}
		if got != widgetB {
			t.Errorf("Get() tenant B = %+v, want %+v", got, widgetB)
		}
	})

	// Scenario 3: cross-tenant reads return empty results. The rows are simply
	// invisible under RLS, not 403 at the database boundary.
	t.Run("tenant B cannot see tenant A's widgets", func(t *testing.T) {
		// Direct lookup of tenant A's widget as tenant B is not found because RLS
		// hides the row.
		_, err := store.Get(ctxB, widgetA.ID)
		if !errors.Is(err, errWidgetNotFound) {
			t.Fatalf("Get() tenant B reading tenant A error = %v, want errWidgetNotFound", err)
		}

		// Listing as tenant B must include tenant B's widget and exclude tenant
		// A's widget.
		widgets, err := store.List(ctxB)
		if err != nil {
			t.Fatalf("List() tenant B error: %v", err)
		}
		for _, w := range widgets {
			if w.ID == widgetA.ID {
				t.Errorf("List() tenant B returned tenant A's widget %+v; RLS should hide it", w)
			}
		}
		var sawB bool
		for _, w := range widgets {
			if w.ID == widgetB.ID {
				sawB = true
			}
		}
		if !sawB {
			t.Errorf("List() tenant B did not include its own widget %q; got %+v", widgetB.ID, widgets)
		}
	})

	// Scenario 4a: tenant B cannot update tenant A's widget. RLS makes the row
	// invisible, so the update matches zero rows and tenant A's data is unchanged.
	t.Run("tenant B cannot update tenant A's widget", func(t *testing.T) {
		var affected int64
		err := appDB.TenantTx(ctxB, func(ctx context.Context, tx pgx.Tx) error {
			ct, err := tx.Exec(ctx,
				"update public.example_widgets set name = $1 where id = $2",
				"hijacked", widgetA.ID)
			if err != nil {
				return err
			}
			affected = ct.RowsAffected()
			return nil
		})
		if err != nil {
			t.Fatalf("TenantTx() tenant B update error: %v", err)
		}
		if affected != 0 {
			t.Fatalf("update as tenant B affected %d rows, want 0 (RLS should hide tenant A's row)", affected)
		}

		// Tenant A's widget must be unchanged.
		got, err := store.Get(ctxA, widgetA.ID)
		if err != nil {
			t.Fatalf("Get() tenant A after cross-tenant update error: %v", err)
		}
		if got.Name != "tenant A widget" {
			t.Errorf("tenant A widget name = %q, want %q (must be unchanged)", got.Name, "tenant A widget")
		}
	})

	// Scenario 4b: tenant B cannot delete tenant A's widget.
	t.Run("tenant B cannot delete tenant A's widget", func(t *testing.T) {
		var affected int64
		err := appDB.TenantTx(ctxB, func(ctx context.Context, tx pgx.Tx) error {
			ct, err := tx.Exec(ctx,
				"delete from public.example_widgets where id = $1",
				widgetA.ID)
			if err != nil {
				return err
			}
			affected = ct.RowsAffected()
			return nil
		})
		if err != nil {
			t.Fatalf("TenantTx() tenant B delete error: %v", err)
		}
		if affected != 0 {
			t.Fatalf("delete as tenant B affected %d rows, want 0 (RLS should hide tenant A's row)", affected)
		}

		// Tenant A's widget must still be readable.
		if _, err := store.Get(ctxA, widgetA.ID); err != nil {
			t.Errorf("Get() tenant A after cross-tenant delete error: %v (widget must still exist)", err)
		}
	})

	// RLS is the real isolation boundary: a write on the application pool with no
	// tenant set is rejected by the WITH CHECK policy, independent of TenantTx.
	t.Run("app role write without a tenant is rejected by RLS", func(t *testing.T) {
		_, err := appDB.Pool().Exec(context.Background(),
			`insert into public.example_widgets (id, tenant_id, name)
			 values ($1, $2, $3)`,
			rlsBlockedInsertID, rlsTenantAID, "should be blocked")
		if err == nil {
			t.Fatal("insert without tenant setting should fail under RLS, got nil error")
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Fatalf("insert without tenant setting error = %v, want RLS policy violation", err)
		}
	})

	// Scenario 6: TenantTx fails closed when no tenant is in context.
	t.Run("TenantTx fails when no tenant is in context", func(t *testing.T) {
		err := appDB.TenantTx(context.Background(), func(context.Context, pgx.Tx) error {
			return nil
		})
		if err == nil {
			t.Fatal("TenantTx() without tenant should fail")
		}
		if !strings.Contains(err.Error(), "tenant") {
			t.Errorf("TenantTx() error = %q, want a tenant-related error", err.Error())
		}
	})

	// Scenario 7: SystemTx requires a non-empty reason so tenant bypasses are
	// intentional and searchable.
	t.Run("SystemTx requires a non-empty reason", func(t *testing.T) {
		err := appDB.SystemTx(context.Background(), "", func(context.Context, pgx.Tx) error {
			return nil
		})
		if err == nil {
			t.Fatal("SystemTx() with empty reason should fail")
		}
		if !strings.Contains(err.Error(), "non-empty reason") {
			t.Errorf("SystemTx() error = %q, want a non-empty reason error", err.Error())
		}
	})

	// SystemTx is the intentional escape hatch: with a reason it uses the admin
	// pool (which bypasses RLS) to write cross-tenant data that the owning tenant
	// can then read. This validates the admin pool wiring and the bypass model.
	t.Run("SystemTx intentionally bypasses RLS with a reason", func(t *testing.T) {
		err := appDB.SystemTx(context.Background(), "seed tenant A widget via admin", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx,
				`insert into public.example_widgets (id, tenant_id, name)
				 values ($1, $2, $3)`,
				rlsAdminSeededID, rlsTenantAID, "admin seeded widget")
			return err
		})
		if err != nil {
			t.Fatalf("SystemTx() seed error: %v", err)
		}

		// The owning tenant can read the admin-seeded row.
		got, err := store.Get(ctxA, rlsAdminSeededID)
		if err != nil {
			t.Fatalf("Get() admin-seeded widget error: %v", err)
		}
		if got.Name != "admin seeded widget" {
			t.Errorf("admin-seeded widget name = %q, want %q", got.Name, "admin seeded widget")
		}

		// Tenant B still cannot see it.
		if _, err := store.Get(ctxB, rlsAdminSeededID); !errors.Is(err, errWidgetNotFound) {
			t.Errorf("Get() tenant B admin-seeded error = %v, want errWidgetNotFound", err)
		}
	})
}
