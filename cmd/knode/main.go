package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kexue-aihao/Knode/internal/config"
	"github.com/kexue-aihao/Knode/internal/node"
	"kray/pkg/kless"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	if err := run(os.Args[1:], logger); err != nil {
		logger.Print(err)
		os.Exit(1)
	}
}

func run(args []string, logger *log.Logger) error {
	node.SetBuildInfo(version, commit, date)

	if len(args) == 0 {
		return openManagerMenu()
	}

	command := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}

	switch command {
	case "run", "serve", "start":
		return runServer(args, logger)
	case "check", "validate":
		return checkConfig(args)
	case "check-upstreams":
		return checkUpstreams(args, logger)
	case "init", "init-config":
		return initConfig(args)
	case "gen-keys", "keys":
		return generateKeys()
	case "menu", "manage":
		return openManagerMenu()
	case "version":
		fmt.Printf("knode %s commit=%s date=%s\n", version, commit, date)
		return nil
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func openManagerMenu() error {
	paths := []string{
		os.Getenv("KNODE_MANAGER_PATH"),
		"/usr/local/bin/knode-manager",
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		cmd := exec.Command(path, "menu")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = os.Environ()
		return cmd.Run()
	}
	return fmt.Errorf("knode-manager not found; install or update it with: curl -fsSL https://raw.githubusercontent.com/kexue-aihao/Knode/master/install.sh | sudo bash -s -- update-script")
}

func runServer(args []string, logger *log.Logger) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", "knode.json", "path to config file")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Printf("knode %s commit=%s date=%s\n", version, commit, date)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	svc, err := node.New(cfg, logger)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return svc.Run(ctx)
}

func checkConfig(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	configPath := fs.String("config", "knode.json", "path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	fmt.Println("config: ok")
	return nil
}

func checkUpstreams(args []string, logger *log.Logger) error {
	fs := flag.NewFlagSet("check-upstreams", flag.ExitOnError)
	configPath := fs.String("config", "knode.json", "path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	return node.CheckUpstreams(context.Background(), cfg, logger)
}

func initConfig(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", "knode.json", "path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(*configPath); err == nil {
		return fmt.Errorf("%s already exists", *configPath)
	}
	if err := config.WriteSample(*configPath); err != nil {
		return err
	}
	fmt.Printf("created %s\n", *configPath)
	return nil
}

func generateKeys() error {
	serverPublic, serverPrivate, err := kless.GenerateServerIdentity()
	if err != nil {
		return err
	}
	clientSecret, err := kless.GenerateClientSecret()
	if err != nil {
		return err
	}
	fmt.Printf("server_signing_public=%s\n", kless.EncodeKey(serverPublic))
	fmt.Printf("server_signing_private=%s\n", kless.EncodeKey(serverPrivate))
	fmt.Printf("client_secret=%s\n", kless.EncodeKey(clientSecret))
	return nil
}

func printUsage() {
	fmt.Print(`Knode node backend

Usage:
  knode
  knode menu
  knode run -config knode.json
  knode check -config knode.json
  knode check-upstreams -config knode.json
  knode init -config knode.json
  knode gen-keys
  knode version
`)
}
