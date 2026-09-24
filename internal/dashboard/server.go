package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// DefaultAddr is what `mate dashboard` binds when nobody says otherwise.
// Loopback, and a port nothing else in this toolchain uses.
const DefaultAddr = "127.0.0.1:7777"

// Deps are the two answers the database cannot give, handed in rather than
// imported: the text of `mate diff` for a crew, and whether a crew's
// branch still exists. Both live in the CLI and in `internal/gitx`, and one
// seam keeps the dashboard's own package free of git and of `cmd/mate`.
//
// A nil Deps field is not an error: the endpoint that needs it says so in
// its `reason` rather than failing, which is the same answer a reader gets
// for a branch that was deleted.
type Deps struct {
	// Diff is `mate diff <project> <crew>`, verbatim - cmd/mate wires
	// its own crewDiffText here so the page and the terminal can never
	// disagree about what a branch contains.
	Diff func(ctx context.Context, project, crew string) (string, error)
	// BranchExists reports whether branch is still in the project's repo.
	BranchExists func(ctx context.Context, project, branch string) (bool, error)
	// Now is the clock behind `generated_at`; tests freeze it.
	Now func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Options is everything Serve needs. The workspace and the database are
// both required: the workspace answers which projects exist and what mode
// each is in, the database answers everything else.
type Options struct {
	Workspace *store.Workspace
	// DB must be a read-only handle (db.OpenRead). The server never closes
	// it; whoever opened it owns it.
	DB *db.DB
	// Addr is the bind address, DefaultAddr when empty.
	Addr string
	// AllowRemote permits a non-loopback bind. Without it Listen refuses
	// one and says why.
	AllowRemote bool
	Deps        Deps
}

// Server is a bound-but-not-yet-serving dashboard.
type Server struct {
	ws    *store.Workspace
	db    *db.DB
	deps  Deps
	cache *cache
	mux   *http.ServeMux
}

// New builds the server and its routes. It reads nothing yet: a dashboard
// that refused to start because a project's files were mid-write would be
// worse than one whose first page says so.
func New(opts Options) (*Server, error) {
	if opts.Workspace == nil {
		return nil, errors.New("dashboard: a workspace is required")
	}
	if opts.DB == nil {
		return nil, errors.New("dashboard: a database handle is required")
	}
	if opts.DB.Writable() {
		// Said rather than tolerated: the dashboard's whole claim is that it
		// cannot change the workspace, and a handle holding the writer lock
		// would also keep the console out of its own timeline.
		return nil, errors.New("dashboard: the database handle must be read-only (db.OpenRead)")
	}
	s := &Server{ws: opts.Workspace, db: opts.DB, deps: opts.Deps, cache: newCache(), mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

// Handler is the whole site: the JSON API under /api/ and the embedded UI
// at /.
func (s *Server) Handler() http.Handler { return s.mux }

// Listen binds opts.Addr, refusing a non-loopback address unless
// AllowRemote was set. It is separate from Serve so a caller can print the
// URL a port of 0 actually resolved to.
func Listen(opts Options) (net.Listener, error) {
	addr := opts.Addr
	if strings.TrimSpace(addr) == "" {
		addr = DefaultAddr
	}
	if !opts.AllowRemote {
		if err := requireLoopback(addr); err != nil {
			return nil, err
		}
	}
	return net.Listen("tcp", addr)
}

// requireLoopback is the refusal of docs/mvp.md M6's "chỉ đọc, local": a
// workspace timeline quotes every line the captain typed and names every
// path a crew touched, so binding it to a routable address has to be an
// explicit decision and never a default or a typo.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("dashboard: %s is not a host:port address: %w", addr, err)
	}
	host = strings.TrimSpace(host)
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return fmt.Errorf("dashboard: %s binds every interface; pass --allow-remote if that is really what you want", addr)
	case "localhost":
		return nil
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return fmt.Errorf("dashboard: %s is not a loopback address; pass --allow-remote if that is really what you want", addr)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("dashboard: %s is not a loopback address; pass --allow-remote if that is really what you want", addr)
	}
	return nil
}

// Serve runs the site on ln until ctx is cancelled, then shuts down.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler: s.Handler(),
		// No read or write timeout: /api/events is a long poll and a
		// deadline here would cut it off mid-wait. The wait itself is
		// bounded (maxWaitSeconds), which is the bound that belongs to the
		// protocol rather than to the socket.
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		close(done)
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		<-done
		return nil
	}
	return err
}

// URL is the address a reader opens, for the line `mate dashboard` prints.
func URL(ln net.Listener) string {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return "http://" + ln.Addr().String() + "/"
	}
	host := addr.IP.String()
	if addr.IP == nil || addr.IP.IsUnspecified() {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%d/", host, addr.Port)
}
