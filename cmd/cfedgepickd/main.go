package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/kayphoon/cfedgepickd/internal/config"
	"github.com/kayphoon/cfedgepickd/internal/daemon"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		run(os.Args[2:])
	case "once":
		once(os.Args[2:])
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
}

func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "/etc/cfedgepickd/config.json", "config file")
	dryRun := fs.Bool("dry-run", false, "force dry-run")
	apply := fs.Bool("apply", false, "force apply mode")
	_ = fs.Parse(args)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if *dryRun {
		cfg.Runtime.DryRun = true
	}
	if *apply {
		cfg.Runtime.DryRun = false
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}
	if err := daemon.Run(context.Background(), cfg); err != nil {
		log.Fatalf("daemon stopped: %v", err)
	}
}

func once(args []string) {
	fs := flag.NewFlagSet("once", flag.ExitOnError)
	configPath := fs.String("config", "/etc/cfedgepickd/config.json", "config file")
	apply := fs.Bool("apply", false, "apply if eligible")
	_ = fs.Parse(args)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if !*apply {
		cfg.Runtime.DryRun = true
	}
	decision, sw, err := daemon.Once(context.Background(), cfg, *apply)
	if err != nil {
		log.Fatalf("once failed: %v", err)
	}
	daemon.PrintDecision(decision, sw)
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: cfedgepickd run|once|version [flags]\n")
}
