package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"golift.io/cnfgfile"
	"golift.io/mulery/server"
)

func main() {
	configFile := flag.String("config", "wsp_server.cfg", "config file path")
	flag.Parse()

	// Load configuration
	config, err := LoadConfiguration(*configFile)
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

// LoadConfiguration loads configuration from a YAML file.
func LoadConfiguration(path string) (*server.Config, error) {
	config := server.NewConfig()

	err := cnfgfile.Unmarshal(config, path)
	if err != nil {
		return nil, fmt.Errorf("failed to parse configuration: %w", err)
	}

	return config, nil
}
