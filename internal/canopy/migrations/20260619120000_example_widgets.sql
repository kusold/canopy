-- +goose Up
-- +goose StatementBegin
-- Tenant-scoped widgets table demonstrating Grove's RLS tenancy model.
--
-- The table is schema-qualified to public so it always lands in the same schema
-- regardless of the connecting database user's name or search_path. Grove's RLS
-- prelude creates a "grove" schema; without qualification, an app user named
-- "grove" would otherwise resolve unqualified tables into that schema.
--
-- Row-Level Security is enabled and forced so the application database user
-- cannot bypass tenant isolation, even as the table owner. The policy defers to
-- grove.current_tenant_id(), which Grove's TenantTx sets per transaction. With
-- no tenant set the helper returns NULL and the policy matches zero rows
-- (fail-closed).
create table public.example_widgets (
    id uuid primary key,
    tenant_id uuid not null,
    name text not null,
    created_at timestamptz not null default now()
);

alter table public.example_widgets enable row level security;
alter table public.example_widgets force row level security;

create policy example_widgets_tenant_isolation on public.example_widgets
    using (tenant_id = grove.current_tenant_id())
    with check (tenant_id = grove.current_tenant_id());
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table if exists public.example_widgets;
-- +goose StatementEnd
