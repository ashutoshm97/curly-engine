// Command styleengine extracts components.xml from PPTX/HTML decks and serves the API.
//
//	styleengine extract deck.pptx -o components.xml
//	styleengine generate -profile components.xml -prompt "Q3 review" -o deck.pptx
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
	"github.com/ashutoshm97/curly-engine/internal/gen"
	"github.com/ashutoshm97/curly-engine/internal/htmlx"
	"github.com/ashutoshm97/curly-engine/internal/ingest"
	"github.com/ashutoshm97/curly-engine/internal/jobs"
	"github.com/ashutoshm97/curly-engine/internal/llm"
	"github.com/ashutoshm97/curly-engine/internal/normalize"
	"github.com/ashutoshm97/curly-engine/internal/profile"
	"github.com/ashutoshm97/curly-engine/internal/render"
	"github.com/ashutoshm97/curly-engine/internal/scene"
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
	case "generate":
		err = generate(os.Args[2:])
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
	fmt.Fprintln(os.Stderr, "usage: styleengine extract <deck.pptx|deck.html> [-o components.xml]\n       styleengine generate -profile components.xml -prompt \"...\" [-slides 8] [-o deck.pptx] [-svg dir] [-data ./data]\n       styleengine serve [-addr :8080] [-data ./data]")
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
	signup := fs.Bool("signup", envOr("SIGNUP", "on") != "off", "allow new account signups (SIGNUP=off to disable)")
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

	writer := newWriter(st)
	gs := &gen.Service{Store: st, Writer: writer, Concurrency: 4, AssetURL: func(ref string) string { return "/api/assets/" + ref }}
	runner.Handle(gen.JobType, gs.Handle)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	wg := runner.Start(ctx)

	srv := &api.Server{Store: st, Blobs: blobs, Runner: runner, Ingest: ing, Gen: gs, Signup: *signup}
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

// newWriter returns the Gemini writer when a key is set, otherwise the offline writer. st may be nil.
func newWriter(st *store.Store) gen.Writer {
	key := firstEnv("GEMINI_API_KEY", "GOOGLE_API_KEY")
	if key == "" {
		slog.Warn("llm", "provider", "offline", "hint", "set GEMINI_API_KEY to use Gemini")
		return gen.Offline{}
	}
	slog.Info("llm", "provider", "gemini")
	return &gen.LLMWriter{
		Client:    llm.NewCached(llm.NewGemini(key), st, 1024, 7*24*time.Hour),
		PlanModel: envOr("GEMINI_PLAN_MODEL", "gemini-3.8-flash"),
		FillModel: envOr("GEMINI_FILL_MODEL", "gemini-3.8-flash"),
	}
}

func generate(args []string) error {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	profPath := fs.String("profile", "", "components.xml to generate in the style of")
	prompt := fs.String("prompt", "", "what the deck is about")
	slides := fs.Int("slides", 8, "number of slides (1-30)")
	out := fs.String("o", "deck.pptx", "output .pptx")
	svgDir := fs.String("svg", "", "also write one SVG per slide into this directory")
	dataDir := fs.String("data", "", "data dir whose blobs/ holds profile assets (optional)")
	fs.Parse(args)
	if *profPath == "" || strings.TrimSpace(*prompt) == "" {
		usage()
	}
	x, err := os.ReadFile(*profPath)
	if err != nil {
		return err
	}
	p, err := profile.Parse(x)
	if err != nil {
		return err
	}
	var blobs render.Blobs
	if *dataDir != "" {
		bs, err := blob.New(filepath.Join(*dataDir, "blobs"))
		if err != nil {
			return err
		}
		blobs = bs.Get
	}
	start := time.Now()
	sc, u, err := gen.Generate(context.Background(), p, newWriter(nil), *prompt, *slides, 4, gen.Hooks{
		Slide: func(s *scene.Slide, total int) { fmt.Fprintf(os.Stderr, "slide %d/%d: %s\n", s.Index, total, s.Title) },
	})
	if err != nil {
		return err
	}
	pptx, err := render.PPTX(sc, p, blobs)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, pptx, 0o644); err != nil {
		return err
	}
	if *svgDir != "" {
		if err := os.MkdirAll(*svgDir, 0o755); err != nil {
			return err
		}
		for _, s := range sc.Slides {
			name := filepath.Join(*svgDir, fmt.Sprintf("slide%02d.svg", s.Index))
			if err := os.WriteFile(name, []byte(render.SVG(s, sc.WidthPt, sc.HeightPt, func(ref string) string { return "" })), 0o644); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d slides in %s (tokens in %d / out %d)\n", *out, len(sc.Slides), time.Since(start).Round(time.Millisecond), u.InputTokens, u.OutputTokens)
	return nil
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
