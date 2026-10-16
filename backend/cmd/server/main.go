package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func parsePort(value string) (int, error) {
	if value == "" {
		return 8080, nil
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, errors.New("invalid PORT: expected an integer from 1 to 65535")
		}
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("invalid PORT: expected an integer from 1 to 65535")
	}
	return port, nil
}

func run() error {
	port, err := parsePort(os.Getenv("PORT"))
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	handler, closeDatabase, err := application(ctx)
	if err != nil {
		return err
	}
	defer closeDatabase()
	server := &http.Server{
		Addr:              ":" + strconv.Itoa(port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8192,
	}
	return serve(ctx, server)
}

type httpServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
	Close() error
}

func serve(ctx context.Context, server httpServer) error {
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()
	select {
	case err := <-serverErrors:
		return fmt.Errorf("HTTP server stopped unexpectedly: %w", err)
	case <-ctx.Done():
	}

	// The signal context is canceled; draining requests needs a fresh deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("HTTP shutdown failed: %w", errors.Join(err, server.Close()))
	}
	if err := <-serverErrors; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP server failed during shutdown: %w", err)
	}
	return nil
}
