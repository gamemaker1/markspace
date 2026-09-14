// Package module is the engine behind Markspace modules and plugins, as
// docs/plugins.md specifies. It holds the registry of contributions, the in-process
// event bus, the durable job engine with its scheduler, the contribution and
// declarative UI types with their validation, and the pure logic of suggested
// edits.
//
// The package has no HTTP routes of its own and no SQLite code. The wiring
// step mounts registry routes with Registry.Mount, adapts core events to
// Event, and supplies a JobStore backed by the index database. That store
// must pass the conformance suite in internal/module/conformance.
//
// # Decisions where docs/plugins.md is open
//
// Registry:
//   - Registry.Add stages every registration of one module and commits them
//     only when all of them are valid. A module that fails leaves no trace.
//   - API and Web routes are for built-in modules only. The plugin host
//     proxies plugin routes through its own built-in routes, so plugins never
//     add ServeMux patterns. Unregister therefore refuses a module with routes.
//   - Job kinds, command ids and panel ids are checked against the id pattern
//     but not against the module id prefix, because docs/plugins.md uses both
//     "plugin.bookmark-wiki.ingest" and "bookmark-wiki.wiki" as examples.
//   - Duplicate importer ids and AI tool names fail like duplicate commands.
//     A module has at most one settings schema and may schedule only its own
//     job kinds, once per kind.
//   - AI tool names use the stricter pattern ^[a-z0-9][a-z0-9_-]{0,63}$,
//     because provider tool APIs reject dots.
//   - Event patterns follow the plugin protocol: a star matches any run of
//     characters, dots included. "note.*" matches "note.created" but not
//     "note".
//   - Panel, Importer and AITool carry a MinAccess field like Command, so the
//     contribution filter treats every kind the same way.
//
// Event bus:
//   - EventHandler returns an error. Errors and panics are logged and counted,
//     and the handler keeps receiving later events.
//   - Publish holds one lock while it enqueues, so every handler and
//     subscriber sees events in the same order.
//   - Streaming subscribers also drop their oldest event when full.
//
// Jobs:
//   - Claim counts the attempt. The attempt number is the lease fence: every
//     worker call passes it, and the store rejects a call whose attempt no
//     longer matches a running job with ErrLeaseLost. No lease owner column is
//     needed.
//   - A pause requested while a job runs is stored in Job.PauseRequested, so
//     the SQLite table needs a pause_requested column beside the columns in
//     docs/plugins.md.
//   - The per-module cap of 10 000 counts queued, running and paused jobs.
//     Retries and lease expiry move jobs among those states, so the count can
//     never exceed the cap.
//   - A lease that expires on the last allowed attempt fails the job.
//   - Cancel also cancels every unfinished descendant of the job.
//   - The engine cancels a handler's context when its lease expires, so a job
//     whose handler stops reporting progress does not run twice at once.
//   - Retention deletes a finished job tree only when every job in the tree is
//     finished and older than the cutoff, so parent progress never changes
//     after the fact and no child loses its parent.
//   - A parent with total 0 and at least one child reports the finished
//     children as progress and the child count as total in DisplayProgress.
//   - Engine.Enqueue requires a registered kind and takes the module from the
//     registration, so a caller cannot charge another module's cap.
//
// Scheduler:
//   - A schedule that is due while its previous job is still queued, running
//     or paused skips that run and records it as the last run.
//   - Jitter is a deterministic offset in [0, jitter) derived from the job kind
//     and the last run, so a restart computes the same next run.
//   - A schedule that has never run is due once its jitter offset has passed
//     since the scheduler first saw it.
//
// Declarative UI:
//   - uitree.go lists the limits, the check order and the error codes. The
//     rules follow test/vectors/protocol.json, shared with the SDKs.
//   - docs/plugins.md counts every JSON object as a node. Columns, options, list
//     item badges, actions, link targets and payload objects count too.
//   - A link URL with user information or a backslash is refused.
//   - Numbers in UI trees and action payloads decode as float64.
//
// Suggestions:
//   - An insertion edit (empty Exact, Start == End) resolves where Prefix
//     followed by Suffix occurs in full, nearest to Start. With no context it
//     resolves at Start when Start lies inside the body.
//   - Two insertions at the same point overlap, and ApplyEdits rejects them.
//   - An orphaned edit that resolves again after a later change becomes open.
package module
