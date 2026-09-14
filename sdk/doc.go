// Package sdk is the Go SDK for Markspace plugins. docs/plugins.md defines
// the protocol.
//
// A plugin is an HTTP service. New takes the plugin manifest. On, Job,
// Command, Panel, Action and Importer register handlers. Handler returns the
// http.Handler that serves the routes Markspace calls. Host is a client for
// the host API under /api/plugin/v1.
//
// The Markspace server imports this package too. It signs deliveries with
// Sign or SignRequest and validates plugin output with ValidateCommandResponse
// and ui.Validate. test/vectors/protocol.json holds the conformance
// vectors that this package and sdk/python/markspace both pass.
package sdk
