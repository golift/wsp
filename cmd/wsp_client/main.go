package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"golift.io/mulery/client"
)

func main() {
	ctx := context.Background()

	configFile := flag.String("config", "wsp_client.cfg", "config file path")
	flag.Parse()

	// Load configuration
	config, err := client.LoadConfiguration(*configFile)
	if err != nil {
		log.Fatalf("Unable to load configuration : %s", err)
	}

	proxy := client.NewClient(config)
	proxy.Start(ctx)

	// Wait signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	// When receives the signal, shutdown
	proxy.Shutdown()
}
