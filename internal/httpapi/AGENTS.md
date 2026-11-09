# HTTP boundary

- Root `AGENTS.md` also applies here. `docs/api.md` is the frontend-facing contract; update it with route changes.
- Register explicit method/path patterns with `http.ServeMux`. Business endpoints use `/api/v1`; operational endpoints remain outside that prefix.
- Successful business responses use `{"data": ...}` and meaningful HTTP status codes. Define and document a JSON error contract when the first business error is introduced; router-generated 404/405 currently use standard-library text responses.
- Validate parameters and JSON bodies before invoking business logic. Bound request bodies with `http.MaxBytesReader`, reject unknown fields where appropriate, and reject trailing JSON values.
- Pass `r.Context()` to downstream work. HTTP handlers translate inputs and outputs; feature packages own business rules.
- Test public behavior with `httptest`: success, invalid input, missing routes and unsupported methods. Avoid tests coupled to private helper implementation.
- Frontend calls use a same-origin `/api` reverse proxy. If cross-origin access becomes necessary, introduce an explicit origin allowlist with preflight tests; CORS is not authentication.
