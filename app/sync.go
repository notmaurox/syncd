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

func fileExistsInS3(ctx context.Context, client *s3.Client, bucket, key string) (bool, error) {
	_, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		// If error is NoSuchKey, file doesn't exist
		return false, nil
	}
	return true, nil
}

func listFiles(dir string) (map[string]bool, error) {
	files := make(map[string]bool)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			relPath, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			// Normalize path separators
			relPath = strings.ReplaceAll(relPath, "\\", "/")
			files[relPath] = true
		}
		return nil
	})
	return files, err
}

func listS3Files(ctx context.Context, client *s3.Client, bucket, prefix string, markerFile string) (map[string]bool, error) {
	files := make(map[string]bool)
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	})

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, obj := range output.Contents {
			key := *obj.Key
			// Remove prefix to get relative path
			if prefix != "" {
				key = strings.TrimPrefix(key, prefix)
				key = strings.TrimPrefix(key, "/")
			}
			// Don't include sync marker files in comparison
			if !strings.HasSuffix(key, markerFile) {
				files[key] = true
			}
		}
	}

	return files, nil
}

func syncDirectoryToS3(ctx context.Context, client *s3.Client, cfg *SyncConfig) error {
	// Track files by subdirectory
	subdirFiles := make(map[string]map[string]bool)

	// First phase: Upload all new files and track them by subdirectory
	err := filepath.Walk(cfg.LocalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path and normalize separators
		relativePath, err := filepath.Rel(cfg.LocalDir, path)
		if err != nil {
			return err
		}
		relativePath = strings.ReplaceAll(relativePath, "\\", "/")

		// Get subdirectory
		subdir := filepath.Dir(relativePath)
		subdir = strings.ReplaceAll(subdir, "\\", "/")

		// Initialize subdir tracking if needed
		if _, exists := subdirFiles[subdir]; !exists {
			subdirFiles[subdir] = make(map[string]bool)
		}
		subdirFiles[subdir][relativePath] = true

		// Create the S3 key
		s3Key := filepath.Join(cfg.Prefix, relativePath)
		s3Key = strings.ReplaceAll(s3Key, "\\", "/")

		// Check if file already exists in S3
		exists, err := fileExistsInS3(ctx, client, cfg.BucketName, s3Key)
		if err != nil {
			return err
		}

		if !exists {
			// File doesn't exist in S3, upload it
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			_, err = client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: &cfg.BucketName,
				Key:    &s3Key,
				Body:   file,
			})

			if err != nil {
				log.Printf("Error uploading %s: %v", path, err)
				return err
			}

			log.Printf("Uploaded new file: %s -> s3://%s/%s", path, cfg.BucketName, s3Key)
		}

		return nil
	})

	if err != nil {
		return err
	}

	// Second phase: Verify all subdirectories
	allSubdirsComplete := true
	subdirStatus := make(map[string]bool)

	for subdir, localSubdirFiles := range subdirFiles {
		// Skip root directory
		if subdir == "." {
			continue
		}

		// Check if all files in this subdirectory exist in S3
		allFilesExist := true
		for file := range localSubdirFiles {
			s3Key := filepath.Join(cfg.Prefix, file)
			s3Key = strings.ReplaceAll(s3Key, "\\", "/")

			exists, err := fileExistsInS3(ctx, client, cfg.BucketName, s3Key)
			if err != nil || !exists {
				allFilesExist = false
				log.Printf("File missing in subdirectory %s: %s", subdir, file)
				break
			}
		}

		subdirStatus[subdir] = allFilesExist
		if !allFilesExist {
			allSubdirsComplete = false
			log.Printf("Subdirectory %s is not fully synced", subdir)
		}
	}

	// Third phase: Create marker files only if all subdirectories are synced
	if allSubdirsComplete {
		log.Println("All subdirectories are fully synced, creating marker files")

		for subdir := range subdirFiles {
			// Skip root directory
			if subdir == "." {
				continue
			}

			// Create sync marker file
			markerKey := filepath.Join(cfg.Prefix, subdir, cfg.SyncMarkerFile)
			markerKey = strings.ReplaceAll(markerKey, "\\", "/")

			markerContent := []byte(fmt.Sprintf("Synced at: %s\nAll subdirectories verified complete.",
				time.Now().Format(time.RFC3339)))

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

		log.Println("All marker files created successfully")
	} else {
		log.Println("Some subdirectories are not fully synced, skipping all marker files")
		// Log details about incomplete directories
		for subdir, isComplete := range subdirStatus {
			if !isComplete {
				log.Printf("Incomplete sync: %s", subdir)
			}
		}
	}

	return nil
}

func performFullSync(ctx context.Context, client *s3.Client, cfg *SyncConfig) error {
	log.Println("Starting full directory sync to S3")

	// Sync local files to S3
	err := syncDirectoryToS3(ctx, client, cfg)
	if err != nil {
		return fmt.Errorf("error syncing directory: %v", err)
	}

	log.Println("Full sync completed successfully")
	return nil
}
