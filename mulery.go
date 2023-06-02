package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"golift.io/mulery/mulery"
)

func main() {
	configFile := flag.String("config", "mulery.conf", "config file path")
	flag.Parse()

	// Load configuration file.
	config, err := mulery.LoadConfigFile(*configFile)
	if err != nil {
		log.Fatalf("Unable to load configuration: %s", err)
	}

	sigCh := make(chan os.Signal, 1)

	config.Start()
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	// Wait here for a signal to shut down.
	<-sigCh
	config.Shutdown()
}
