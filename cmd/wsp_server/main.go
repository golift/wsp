package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"golift.io/mulery/server"
)

func main() {
	configFile := flag.String("config", "wsp_server.cfg", "config file path")
	flag.Parse()

	// Load configuration
	config, err := server.LoadConfiguration(*configFile)
	if err != nil {
		log.Fatalf("Unable to load configuration : %s", err)
	}

	server := server.NewServer(config)
	server.Start()

	// Wait signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	// When receives the signal, shutdown
	server.Shutdown()
}
