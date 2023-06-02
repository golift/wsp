package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofrs/uuid/v5"
	"golift.io/cnfgfile"
	"golift.io/mulery/client"
)

func main() {
	ctx := context.Background()

	configFile := flag.String("config", "wsp_client.yml", "config file path")
	flag.Parse()

	// Load configuration
	config, err := LoadConfiguration(*configFile)
	if err != nil {
		log.Fatalf("Unable to load configuration: %s", err)
	}

	proxy := client.NewClient(config)
	proxy.Start(ctx) // go routines start.

	// Wait signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	// Wait here for a signal.
	<-sigCh
	proxy.Shutdown()
}

func LoadConfiguration(path string) (*client.Config, error) {
	config := client.NewConfig()

	err := cnfgfile.Unmarshal(config, path)
	if err != nil {
		return nil, fmt.Errorf("failed to parse configuration: %w", err)
	}

	if config.ID == "" {
		id, err := uuid.NewV4()
		if err != nil {
			return nil, fmt.Errorf("unable to get unique id: %w", err)
		}

		config.ID = id.String()
	}

	return config, nil
}
