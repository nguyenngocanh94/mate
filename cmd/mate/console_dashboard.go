package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// startConsoleDashboard serves the read-only dashboard (docs/mvp.md M6) for as
// long as the console is open, on the default address. It lives in the
// console's process like the observer does, so quitting the console stops it.
//
// The database is opened with db.OpenRead, which takes no lock, so it runs
// beside the observer that owns the writer. It returns the URL being served
// and a stop func that is safe to call more than once.
//
// A busy port is not an error worth stopping the console for: another
// console (or `mate dashboard`) is most likely already serving the same
// workspace there. The caller says so on the status line and carries on.
func startConsoleDashboard(ctx context.Context, ws *store.Workspace) (string, func(), error) {
	handle, err := db.OpenRead(ws)
	if err != nil {
		return "", nil, err
	}
	opts := dashboard.Options{Workspace: ws, DB: handle, Deps: dashboardDeps(ws)}
	server, err := dashboard.New(opts)
	if err != nil {
		_ = handle.Close()
		return "", nil, err
	}
	ln, err := dashboard.Listen(opts)
	if err != nil {
		_ = handle.Close()
		return "", nil, fmt.Errorf("dashboard not started: %w", err)
	}

	serveCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(serveCtx, ln)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			<-done
			_ = handle.Close()
		})
	}
	return dashboard.URL(ln), stop, nil
}
