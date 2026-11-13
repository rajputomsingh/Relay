## Context

The Node.js SDK supports getEndpointHealth(id), but the Go SDK does not currently expose an equivalent operation.

## Scope

Add a Go SDK method:

```go
func (c *Client) GetEndpointHealth(ctx context.Context, endpointID string) (*EndpointHealth, error)
```

Add an EndpointHealth type that mirrors the actual API response. Use pointer fields for nullable timestamps where appropriate.

## Acceptance Criteria

- [ ] Method is added to the Go SDK package.
- [ ] EndpointHealth mirrors the OpenAPI response.
- [ ] httptest.Server tests cover success, 404, and 401.
- [ ] Method has a Go documentation comment.
- [ ] sdk-go/README.md includes an example.
- [ ] Existing Go SDK tests continue to pass.

## Contributor Notes

Follow existing Go SDK patterns for requests and non-2xx responses. Use the OpenAPI document as the source of truth for response fields.

**Size:** S (~2 hours)
