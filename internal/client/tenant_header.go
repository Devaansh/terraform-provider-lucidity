package client

import "context"

// headerTenant carries the Lucidity tenant a call acts on. Service accounts
// hold roles per tenant, and Lucidity's docs say to call APIs with the
// Bearer token "and X-tenant". The value is a Lucidity tenant ID (as in
// GET /v1/tenants), e.g. "lucidity-e2e".
const headerTenant = "X-tenant"

type tenantKey struct{}

// WithTenant returns a context whose calls send this tenant ID as X-tenant,
// overriding the client's default. An empty tenantID leaves ctx unchanged.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	if tenantID == "" {
		return ctx
	}
	return context.WithValue(ctx, tenantKey{}, tenantID)
}

// WithDefaultTenant sets the X-tenant value for calls whose context names
// no tenant (the provider's tenant_id setting).
func WithDefaultTenant(tenantID string) Option {
	return func(c *Client) { c.defaultTenant = tenantID }
}

// tenantFor picks the X-tenant value for a call: the context's, else the
// client default, else "" (header not sent).
func (c *Client) tenantFor(ctx context.Context) string {
	if t, ok := ctx.Value(tenantKey{}).(string); ok && t != "" {
		return t
	}
	return c.defaultTenant
}
