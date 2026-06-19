-- +goose Up
-- +goose StatementBegin
-- Canopy service initialization.
--
-- This migration establishes the canopy migration source so that future
-- service-owned migrations are discovered and applied by Grove's migration
-- runner. It deliberately avoids creating tenant-scoped tables; those land in
-- later Phase 3 issues. The version function gives operators and tests a
-- lightweight way to confirm canopy migrations have been applied.
create or replace function public.canopy_schema_version() returns text
    language sql
    stable
    as $$
        select '0.1.0'
    $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop function if exists public.canopy_schema_version();
-- +goose StatementEnd
