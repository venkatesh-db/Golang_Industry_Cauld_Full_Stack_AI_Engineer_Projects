-- records is a tenant-owned table. The composite primary key gives each tenant
-- its own id namespace, and Row-Level Security enforces isolation at the engine
-- level as a backstop behind the application's WHERE tenant_id predicate.
CREATE TABLE IF NOT EXISTS records (
    tenant_id text NOT NULL,
    id        text NOT NULL,
    data      text NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

-- Defense in depth: even a query that forgets its WHERE clause (or a compromised
-- code path) cannot cross tenants. FORCE makes the policy apply to the table
-- owner too, not just ordinary roles.
ALTER TABLE records ENABLE ROW LEVEL SECURITY;
ALTER TABLE records FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation ON records;
CREATE POLICY tenant_isolation ON records
    USING (tenant_id = current_setting('app.current_tenant', true))
    WITH CHECK (tenant_id = current_setting('app.current_tenant', true));
