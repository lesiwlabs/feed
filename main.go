package main

//go:generate go run lesiw.io/plain/cmd/plaingen@latest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "golang.org/x/crypto/x509roots/fallback"
	"labs.lesiw.io/ctr"
	"lesiw.io/defers"
	"lesiw.io/plain"
)

var db *pgxpool.Pool
var ctx = context.Background()

type cache struct {
	f   func() ([]byte, error)
	t   time.Time
	buf []byte
	err error
	sync.RWMutex
}

func (c *cache) Handler(w http.ResponseWriter, r *http.Request) {
	if c.t.IsZero() || time.Since(c.t) > 5*time.Minute {
		func() {
			c.Lock()
			defer c.Unlock()
			c.buf, c.err = c.f()
			c.t = time.Now()
		}()
	}
	c.RLock()
	defer c.RUnlock()
	if c.err != nil {
		http.Error(w, c.err.Error(), 500)
	} else {
		_, _ = io.Copy(w, bytes.NewBuffer(c.buf))
	}
}

func main() {
	defer defers.Run()
	if err := run(); err != nil {
		if err.Error() != "" {
			fmt.Fprintln(os.Stderr, err)
		}
		defers.Exit(1)
	}
}

func run() error {
	if err := ez(os.Getenv("PGHOST"), ctr.Postgres); err != nil {
		return fmt.Errorf("failed to set up postgres: %w", err)
	}
	db = plain.ConnectPgx(ctx)
	defers.Add(db.Close)

	mux := new(http.ServeMux)
	goAnnounce := &cache{
		f: func() ([]byte, error) { return groupFeed("golang-announce") },
	}
	mux.HandleFunc("/go/announce.xml", goAnnounce.Handler)

	l, err := net.Listen("tcp4", ":8080")
	if err != nil {
		return fmt.Errorf("could not listen on port 8080: %w", err)
	}
	return http.Serve(l, mux)
}

// ez execs f if v is zero.
func ez[T comparable](v T, f func() error) error {
	var zero T
	if v != zero {
		return nil
	}
	return f()
}
