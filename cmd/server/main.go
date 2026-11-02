package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/center/CenterLINECRM/internal/crm"
	"github.com/center/CenterLINECRM/internal/transport"
)

func main() {
	database := flag.String("database", "./data/centerlinecrm.db", "SQLite database path")
	address := flag.String("address", "127.0.0.1:8000", "HTTP listen address")
	flag.Parse()
	abs, err := filepath.Abs(*database)
	if err != nil {
		log.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		log.Fatal(err)
	}
	s, err := crm.Open(abs)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	srv := &http.Server{Addr: *address, Handler: transport.Handler(s), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()
	log.Printf("CenterLINECRM listening on %s (MCP /mcp, registration /public/register, public API /api/v1)", *address)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
