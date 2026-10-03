// Command davd is a throwaway WebDAV server used to validate qzrs-webdav-backup
// end-to-end on a real device.
//
// It is a development tool, not part of the shipped product: build.sh only
// compiles the root package, so this never lands in a release tarball.
//
// Usage on a router:
//
//	./davd -listen 127.0.0.1:8099 -root /tmp/davroot -user u -pass p
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/testdav"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8099", "listen address")
	root := flag.String("root", "/tmp/davroot", "directory to serve")
	user := flag.String("user", "davuser", "basic auth username (empty disables auth)")
	pass := flag.String("pass", "davpass", "basic auth password")
	strict := flag.Bool("require-length", true, "reject chunked PUTs with 411, emulating strict servers")
	prefix := flag.String("prefix", "", "URL prefix the DAV endpoint lives under (e.g. /dav), mirrored in PROPFIND hrefs")
	flag.Parse()

	if err := os.MkdirAll(*root, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "cannot create root %s: %v\n", *root, err)
		os.Exit(1)
	}

	srv := testdav.New(*root, *user, *pass)
	srv.RequireLength = *strict
	srv.PathPrefix = *prefix

	httpSrv := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("davd serving %s at http://%s%s/ (user=%q, require-length=%v)",
			*root, *listen, *prefix, *user, *strict)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	<-sigc

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	log.Print("davd stopped")
}
