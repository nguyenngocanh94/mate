// Package runtime is the RuntimeAdapter port and the Herdr-facing contracts
// behind it: session/workspace/tab/agent handles, the name and collision
// rules, the Herdr error taxonomy, readiness waiting, key sending, terminal
// attach, the session stream, and a fake adapter for tests.
//
// Workspace identity stays separate from opaque Herdr handles. This package
// does not fork harness processes and does not persist state; it shells out
// to the herdr CLI through a process.Runner.
//
// Copied from v1 (github.com/nguyenngocanh94/mate), where the identity types
// lived in internal/domain and the runtime binding in internal/application.
// matev2 has neither: the few identity pieces the adapter needs are in
// identity.go, and binding state is the caller's business.
package runtime
