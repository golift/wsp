package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"golift.io/mulery"
)

func main() {
	configFile := flag.String("config", "/config/mulery.conf", "config file path")
	flag.Parse()

	// Load configuration file.
	mulery, err := mulery.LoadConfigFile(*configFile)
	if err != nil {
		log.Fatalf("Config File Error: %s", err)
	}

	mulery.SetupLogs()
	mulery.PrintConfig()

	mulery.Start()
	defer mulery.Shutdown()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	// Wait here for a signal to shut down.
	<-sigCh
}
