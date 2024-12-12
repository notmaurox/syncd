package main

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	// Check if config file path is provided
	if len(os.Args) < 2 {
		log.Fatal("Please provide path to config file")
	}

	configFilePath := os.Args[1]

	// Read configuration from file
	config, err := readConfigFile(configFilePath)
	if err != nil {
		log.Fatalf("Error reading config: %v", err)
	}

	// Load AWS configuration with credentials
	awsConfig, err := loadAWSConfig(config)
	if err != nil {
		log.Fatalf("Unable to load AWS config: %v", err)
	}

	// Create S3 client
	client := s3.NewFromConfig(awsConfig)

	// Create a context that we can cancel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a WaitGroup to track running syncs
	var wg sync.WaitGroup

	// Create a channel to signal when a sync is in progress
	inProgress := make(chan struct{}, 1)

	// Perform initial sync
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := performFullSync(ctx, client, config); err != nil {
			log.Printf("Initial sync failed: %v", err)
		}
	}()

	// If sync interval is specified, start periodic syncing
	if config.SyncInterval > 0 {
		ticker := time.NewTicker(config.SyncInterval)
		defer ticker.Stop()

		log.Printf("Starting periodic sync every %v", config.SyncInterval)

		for {
			select {
			case <-ticker.C:
				// Try to acquire the inProgress channel
				select {
				case inProgress <- struct{}{}:
					// Successfully acquired the channel, start sync
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer func() { <-inProgress }() // Release the inProgress channel when done

						log.Printf("Starting scheduled sync")
						if err := performFullSync(ctx, client, config); err != nil {
							log.Printf("Periodic sync failed: %v", err)
						}
					}()
				default:
					// A sync is already in progress
					log.Printf("Previous sync still in progress, skipping this interval")
				}
			case <-ctx.Done():
				// Wait for any running syncs to complete
				wg.Wait()
				return
			}
		}
	}

	// Wait for the initial sync to complete if no interval was specified
	wg.Wait()
}

// Separate function to load AWS config with provided credentials
func loadAWSConfig(cfg *SyncConfig) (aws.Config, error) {
	// Create static credentials
	staticCredProvider := credentials.NewStaticCredentialsProvider(
		cfg.AWSAccessKey,
		cfg.AWSSecretKey,
		"",
	)

	// Load default config and override with static credentials
	return config.LoadDefaultConfig(context.TODO(),
		config.WithCredentialsProvider(staticCredProvider),
	)
}
