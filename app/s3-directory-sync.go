package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type SyncConfig struct {
	AWSAccessKey   string
	AWSSecretKey   string
	LocalDir       string
	BucketName     string
	Prefix         string
	SyncInterval   time.Duration
	SyncMarkerFile string
}

func readConfigFile(filepath string) (*SyncConfig, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, fmt.Errorf("error opening config file: %v", err)
	}
	defer file.Close()

	config := &SyncConfig{
		// Set default sync marker filename
		SyncMarkerFile: "syncd.txt",
	}
	scanner := bufio.NewScanner(file)
	configMap := make(map[string]string)

	// Read config file line by line
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue // Skip empty lines and comments
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid config line: %s", line)
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		configMap[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}

	// Validate and populate config
	requiredFields := []string{"aws_access_key", "aws_secret_key", "local_dir", "bucket_name"}
	for _, field := range requiredFields {
		if _, exists := configMap[field]; !exists {
			return nil, fmt.Errorf("missing required config field: %s", field)
		}
	}

	config.AWSAccessKey = configMap["aws_access_key"]
	config.AWSSecretKey = configMap["aws_secret_key"]
	config.LocalDir = configMap["local_dir"]
	config.BucketName = configMap["bucket_name"]
	config.Prefix = configMap["prefix"] // Optional

	// Optional: custom sync marker filename
	if markerFile, exists := configMap["sync_marker_file"]; exists {
		config.SyncMarkerFile = markerFile
	}

	// Parse sync interval
	if intervalStr, exists := configMap["sync_interval"]; exists {
		interval, err := time.ParseDuration(intervalStr)
		if err != nil {
			return nil, fmt.Errorf("invalid sync interval: %v", err)
		}
		config.SyncInterval = interval
	}

	return config, nil
}

func syncDirectoryToS3(ctx context.Context, client *s3.Client, cfg *SyncConfig) error {
	// Track synced subdirectories
	syncedSubdirs := make(map[string]bool)

	// Walk through the local directory
	err := filepath.Walk(cfg.LocalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Create the S3 key by removing the local directory path and adding the prefix
		relativePath, err := filepath.Rel(cfg.LocalDir, path)
		if err != nil {
			return err
		}
		s3Key := filepath.Join(cfg.Prefix, relativePath)
		s3Key = strings.ReplaceAll(s3Key, "\\", "/") // Normalize path for S3

		// Track the subdirectory
		subdir := filepath.Dir(relativePath)
		syncedSubdirs[subdir] = true

		// Open the file
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		// Upload to S3
		_, err = client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &cfg.BucketName,
			Key:    &s3Key,
			Body:   file,
		})

		if err != nil {
			log.Printf("Error uploading %s: %v", path, err)
			return err
		}

		log.Printf("Uploaded: %s -> s3://%s/%s", path, cfg.BucketName, s3Key)
		return nil
	})

	// Create sync marker file for each synced subdirectory
	for subdir := range syncedSubdirs {
		// Skip root directory
		if subdir == "." {
			continue
		}

		// Create S3 key for sync marker file
		markerKey := filepath.Join(cfg.Prefix, subdir, cfg.SyncMarkerFile)
		markerKey = strings.ReplaceAll(markerKey, "\\", "/")

		// Create a buffer with sync timestamp
		markerContent := []byte(fmt.Sprintf("Synced at: %s", time.Now().Format(time.RFC3339)))

		// Upload sync marker file to S3
		_, err = client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &cfg.BucketName,
			Key:    &markerKey,
			Body:   bytes.NewReader(markerContent),
		})

		if err != nil {
			log.Printf("Error creating %s for %s: %v", cfg.SyncMarkerFile, subdir, err)
			return err
		}

		log.Printf("Created %s for subdirectory: %s", cfg.SyncMarkerFile, subdir)
	}

	return err
}

func listS3Objects(ctx context.Context, client *s3.Client, bucketName, prefix string) ([]types.Object, error) {
	result, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &bucketName,
		Prefix: &prefix,
	})
	if err != nil {
		return nil, err
	}

	return result.Contents, nil
}

func deleteS3Objects(ctx context.Context, client *s3.Client, bucketName string, objects []types.Object) error {
	if len(objects) == 0 {
		return nil
	}

	// Prepare object identifiers for deletion
	objectsToDelete := make([]types.ObjectIdentifier, 0, len(objects))
	for _, obj := range objects {
		objectsToDelete = append(objectsToDelete, types.ObjectIdentifier{
			Key: obj.Key,
		})
	}

	// Batch delete objects
	_, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: &bucketName,
		Delete: &types.Delete{
			Objects: objectsToDelete,
		},
	})

	return err
}

func performFullSync(ctx context.Context, client *s3.Client, cfg *SyncConfig) error {
	log.Println("Starting full directory sync to S3")

	// Sync local files to S3
	err := syncDirectoryToS3(ctx, client, cfg)
	if err != nil {
		return fmt.Errorf("error syncing directory: %v", err)
	}

	// List existing S3 objects
	existingObjects, err := listS3Objects(ctx, client, cfg.BucketName, cfg.Prefix)
	if err != nil {
		return fmt.Errorf("error listing S3 objects: %v", err)
	}

	// Track files to potentially delete
	localFiles := make(map[string]bool)
	filepath.Walk(cfg.LocalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		relativePath, err := filepath.Rel(cfg.LocalDir, path)
		if err != nil {
			return err
		}
		s3Key := filepath.Join(cfg.Prefix, relativePath)
		s3Key = strings.ReplaceAll(s3Key, "\\", "/")
		localFiles[s3Key] = true
		return nil
	})

	// Identify and delete files in S3 that no longer exist locally
	objectsToDelete := make([]types.Object, 0)
	for _, obj := range existingObjects {
		if !localFiles[*obj.Key] {
			objectsToDelete = append(objectsToDelete, obj)
		}
	}

	// Delete objects no longer present in local directory
	if len(objectsToDelete) > 0 {
		err = deleteS3Objects(ctx, client, cfg.BucketName, objectsToDelete)
		if err != nil {
			return fmt.Errorf("error deleting S3 objects: %v", err)
		}
		log.Printf("Deleted %d objects from S3", len(objectsToDelete))
	}

	log.Println("Full sync completed successfully")
	return nil
}
