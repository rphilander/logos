package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	logos "github.com/rphilander/logos/core"
)

func main() {
	sockPath := os.Getenv("LOGOS_SOCK")
	if sockPath == "" {
		sockPath = "/tmp/logos.sock"
	}

	modSockPath := os.Getenv("LOGOS_MOD_SOCK")
	if modSockPath == "" {
		modSockPath = "/tmp/logos-mod.sock"
	}

	cbSockPath := os.Getenv("LOGOS_CB_SOCK")
	if cbSockPath == "" {
		cbSockPath = "/tmp/logos-cb.sock"
	}

	dir := os.Getenv("LOGOS_DIR")
	if dir == "" {
		dir = "."
	}

	core, err := logos.NewCore(dir, sockPath, modSockPath, cbSockPath)
	if err != nil {
		log.Fatalf("failed to start core: %v", err)
	}

	// Handle shutdown signals
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Println("shutting down...")
		core.Shutdown()
		os.Exit(0)
	}()

	log.Printf("logos core listening (cli: %s, modules: %s, callbacks: %s, log dir: %s)", sockPath, modSockPath, cbSockPath, dir)
	core.Run()
}
