package platform

// DBPlatform is the db-platform version the library's integration tests
// (-tags integration) last ran against — the version whose api.* signatures
// (api.authorize / authorize_local, api.log_request, api.parse_message,
// api.sql jsonb conventions) this code calls. The two halves of one contract
// live in two repositories; this constant is the link, and the integration run
// checks it against GO_TEST_DB_PLATFORM_VERSION (the submodule's VERSION file).
// Bump it in the commit that re-runs the integration tests, never alone.
const DBPlatform = "1.2.21"
