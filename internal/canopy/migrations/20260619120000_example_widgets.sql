-- +goose Up
-- +goose StatementBegin
-- Tenant-scoped widgets table demonstrating Grove's RLS tenancy model.
--
-- Unqualified DDL lands in the public schema via Grove's default migration
-- connection search_path (public,grove); Grove helpers such as
-- current_tenant_id() stay reachable under the grove schema.
--
-- Row-Level Security is enabled and forced so the application database user
-- cannot bypass tenant isolation, even as the table owner. The policy defers to
-- grove.current_tenant_id(), which Grove's TenantTx sets per transaction. With
-- no tenant set the helper returns NULL and the policy matches zero rows
-- (fail-closed).
create table example_widgets (
    id uuid primary key,
    tenant_id uuid not null,
    name text not null,
    created_at timestamptz not null default now()
);

alter table example_widgets enable row level security;
alter table example_widgets force row level security;

create policy example_widgets_tenant_isolation on example_widgets
    using (tenant_id = grove.current_tenant_id())
    with check (tenant_id = grove.current_tenant_id());
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table if exists example_widgets;
-- +goose StatementEnd
