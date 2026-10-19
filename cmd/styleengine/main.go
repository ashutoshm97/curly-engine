// Command styleengine extracts components.xml from PPTX/HTML decks and serves the API.
//
//	styleengine extract deck.pptx -o components.xml
//	styleengine serve -addr :8080 -data ./data
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ashutoshm97/curly-engine/internal/api"
	"github.com/ashutoshm97/curly-engine/internal/blob"
	"github.com/ashutoshm97/curly-engine/internal/htmlx"
	"github.com/ashutoshm97/curly-engine/internal/ingest"
	"github.com/ashutoshm97/curly-engine/internal/jobs"
	"github.com/ashutoshm97/curly-engine/internal/normalize"
	"github.com/ashutoshm97/curly-engine/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "extract":
		err = extract(os.Args[2:])
	case "serve":
		err = serve(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: styleengine extract <deck.pptx|deck.html> [-o components.xml]\n       styleengine serve [-addr :8080] [-data ./data]")
	os.Exit(2)
}

func extract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	out := fs.String("o", "", "output file (default stdout)")
	name := fs.String("name", "", "profile name")
	var file string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		file, args = args[0], args[1:]
	}
	fs.Parse(args)
	if file == "" {
		file = fs.Arg(0)
	}
	if file == "" {
		usage()
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	kind, err := ingest.DetectKind(file, data)
	if err != nil {
		return err
	}
	svc := &ingest.Service{}
	if kind == "html" {
		svc.HTML = htmlx.New(1)
		defer svc.HTML.Close()
	}
	start := time.Now()
	deck, err := svc.Extract(context.Background(), kind, data)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	}
	p, err := normalize.Build(deck, normalize.Options{ID: blob.Hash(data)[:12], Name: *name})
	if err != nil {
		return err
	}
	x, err := p.MarshalIndent()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "extracted %d slides -> %d layouts, %d components in %s\n", len(deck.Slides), len(p.Layouts), len(p.Components), time.Since(start).Round(time.Millisecond))
	if *out == "" {
		_, err = os.Stdout.Write(x)
		return err
	}
	return os.WriteFile(*out, x, 0o644)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", envOr("ADDR", ":8080"), "listen address")
	dataDir := fs.String("data", envOr("DATA_DIR", "./data"), "data directory (SQLite + blobs)")
	workers := fs.Int("workers", 4, "job workers")
	fs.Parse(args)

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(*dataDir, "styleengine.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	blobs, err := blob.New(filepath.Join(*dataDir, "blobs"))
	if err != nil {
		return err
	}
	html := htmlx.New(*workers)
	defer html.Close()

	runner := jobs.NewRunner(st, *workers)
	ing := &ingest.Service{Store: st, Blobs: blobs, HTML: html}
	runner.Handle(ingest.JobType, ing.Handle)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	wg := runner.Start(ctx)

	srv := &api.Server{Store: st, Blobs: blobs, Runner: runner, Ingest: ing}
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	slog.Info("listening", "addr", *addr, "data", *dataDir)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	wg.Wait()
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
