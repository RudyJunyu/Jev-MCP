package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jev/mcphub/internal/hub"
	"github.com/jev/mcphub/internal/install"
	"github.com/jev/mcphub/internal/typesafe"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "jev:", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run(args []string) error {
	command := "serve"
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}
	switch command {
	case "version", "--version":
		fmt.Println(hub.Version)
		return nil
	case "help", "--help", "-h":
		fmt.Println("Jev MCP Hub\n  serve [--addr 0.0.0.0:8080] [--transport http|stdio]\n  init [--url http://localhost:8080/mcp] [--config jev-client.json]\n  install --client codex|claude|all [--config jev-client.json]\n  healthcheck [--url http://127.0.0.1:8080/healthz]\n  version")
		return nil
	case "init":
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		endpoint := fs.String("url", "http://localhost:8080/mcp", "MCP endpoint distributed by the operator")
		path := fs.String("config", "jev-client.json", "client configuration file")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if err := install.Init(*path, *endpoint); err != nil {
			return err
		}
		fmt.Println("Created", *path, "— replace token with your TypeSafe API key, then run install.")
		return nil
	case "install":
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		client := fs.String("client", "all", "codex, claude or all")
		path := fs.String("config", "jev-client.json", "client JSON file")
		homeDir := fs.String("home", "", "override home for an isolated installation")
		if err := fs.Parse(args); err != nil {
			return err
		}
		cfg, err := install.Load(*path)
		if err != nil {
			return err
		}
		results, err := install.Install(*client, cfg, *homeDir)
		fmt.Print(install.Summary(results))
		if err == nil {
			fmt.Println("Restart Codex / Claude Code to load Jev. Tokens are stored in your local client config.")
		}
		return err
	case "healthcheck":
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		endpoint := fs.String("url", "http://127.0.0.1:8080/healthz", "health URL")
		if err := fs.Parse(args); err != nil {
			return err
		}
		client := &http.Client{Timeout: 3 * time.Second}
		res, err := client.Get(*endpoint)
		if err != nil {
			return errors.New("health check failed")
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			return errors.New("health check failed")
		}
		return nil
	case "serve":
		return serve(args)
	default:
		return fmt.Errorf("unknown command %q; use help", command)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", env("JEV_ADDR", "0.0.0.0:8080"), "HTTP listen address")
	transport := fs.String("transport", "http", "http or stdio")
	upstream := fs.String("upstream", env("TYPESAFE_BASE_URL", typesafe.DefaultURL), "trusted TypeSafe API base URL")
	timeout := fs.Duration("timeout", 45*time.Second, "total tool timeout including retries")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *transport != "http" && *transport != "stdio" {
		return errors.New("transport must be http or stdio")
	}
	client, err := typesafe.New(*upstream, *timeout)
	if err != nil {
		return err
	}
	token := ""
	if *transport == "stdio" {
		var ok bool
		token, ok = hub.Token("Bearer " + os.Getenv("TYPESAFE_API_KEY"))
		if !ok {
			return errors.New("stdio requires TYPESAFE_API_KEY")
		}
	}
	s := hub.New(client, token, *timeout)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *transport == "stdio" {
		return s.Run(ctx, &mcp.StdioTransport{})
	}
	srv := &http.Server{Addr: *addr, Handler: hub.Handler(s), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: *timeout + 10*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	fmt.Fprintln(os.Stderr, "Jev MCP listening on", *addr, "(/mcp); tokens are provided by each client")
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			srv.Close()
			return err
		}
		return nil
	}
}
