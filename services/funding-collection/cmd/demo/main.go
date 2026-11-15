// Command demo prepares fixtures, serves the provider double, or runs the authenticated DEMO journey.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Print("funding demo command failed: " + err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: demo prepare|serve-provider|run")
	}
	switch args[0] {
	case "prepare":
		return runPrepare(args[1:])
	case "serve-provider":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runServeProvider(ctx, args[1:])
	case "run":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runJourney(ctx, args[1:])
	default:
		return errors.New("unknown funding demo subcommand")
	}
}
