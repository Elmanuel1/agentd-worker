// Command agentd-worker runs agentd's sandboxes off Kubernetes.
//
//	agentd-worker scale    the controller: polls the queue, launches and hands over, releases, reaps
//	agentd-worker fetch    a sandbox's fetch container: collects its work item
//	agentd-worker sidecar  a sandbox's sidecar: stops the item when the worker exits
//	agentd-worker reap     the reaper alone, for an environment served by Anthropic's chart
//	agentd-worker resolver a DNS relay the Docker adapter runs for sandboxes under runsc
//
// Every setting is an environment variable; docs/configuration.md lists them.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level()}))
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: agentd-worker scale|fetch|sidecar|reap|resolver")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	commands := map[string]func(context.Context, *slog.Logger) error{
		"scale": runScale, "fetch": runFetch, "sidecar": runSidecar, "reap": runReap, "resolver": runResolver,
	}
	run, ok := commands[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
	if err := run(ctx, log); err != nil {
		if !errors.Is(err, errLogged) {
			log.Error(os.Args[1]+" failed", "error", err)
		}
		stop()
		os.Exit(1)
	}
}

// errLogged marks a failure its command already logged at its own level, as one nobody must act on.
var errLogged = errors.New("logged")

func level() slog.Level {
	var l slog.Level
	if l.UnmarshalText([]byte(os.Getenv("AGENTD_LOG_LEVEL"))) != nil {
		return slog.LevelInfo
	}
	return l
}
