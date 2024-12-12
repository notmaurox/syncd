# S3 Directory Sync Service

A Golang microservice that synchronizes a local directory with an Amazon S3 bucket. The service intelligently syncs files by only uploading new files and verifies directory contents before marking directories as synced.

## Features

- Smart file synchronization (only uploads new files)
- One-time or periodic synchronization
- Subdirectory tracking with sync markers
- Parent directories recieve sync marker only if all subdirs are syncd
- Verification of directory contents before marking as synced
- AWS credentials configuration
- Configurable sync marker files
- Prevents overlapping sync operations
- Non-destructive (never deletes files from S3)

## Prerequisites

- Go 1.16 or later
- AWS account with S3 access
- AWS credentials (access key and secret key)
- AWS bucket configured for provided credentials

## Installation

1. Clone the repository
```bash
git clone https://github.com/notmaurox/syncd
cd syncd
```

2. Install dependencies
```bash
go mod init syncd
go get github.com/aws/aws-sdk-go-v2
go get github.com/aws/aws-sdk-go-v2/config
go get github.com/aws/aws-sdk-go-v2/credentials
go get github.com/aws/aws-sdk-go-v2/service/s3
```

3. Build executable
```bash
make
```

## Configuration

Create a configuration file (e.g., `config.txt`) with the following format:

```ini
aws_access_key=YOUR_AWS_ACCESS_KEY
aws_secret_key=YOUR_AWS_SECRET_KEY
local_dir=/path/to/local/directory
bucket_name=your-s3-bucket-name
prefix=optional/path/prefix/
sync_interval=5m
sync_marker_file=syncd.txt
```

### Configuration Options

| Option | Required | Description | Default | Example |
|--------|----------|-------------|---------|---------|
| aws_access_key | Yes | AWS Access Key ID | - | AKIAIOSFODNN7EXAMPLE |
| aws_secret_key | Yes | AWS Secret Access Key | - | wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY |
| local_dir | Yes | Local directory to sync | - | /home/user/documents |
| bucket_name | Yes | S3 bucket name | - | my-backup-bucket |
| prefix | No | S3 key prefix | "" | backups/ |
| sync_interval | No | Sync interval duration | 0 (one-time sync) | 5m, 1h, 24h |
| sync_marker_file | No | Name of sync marker file | syncd.txt | .sync_complete |

### Sync Interval Format
Duration strings are specified using numbers and unit suffixes:
- "s" - seconds
- "m" - minutes
- "h" - hours

Examples: "30s", "5m", "2h"

## Usage

- Start a one-time sync:
```bash
go run app/main.go app/sync.go /path/to/config.txt
```

- Start periodic sync (when sync_interval is specified in config):
```bash
go run app/main.go app/sync.go /path/to/config.txt
```

- Build executable and run
```bash
make
./syncd path/to/config.txt
```

## Sync Behavior

### File Synchronization
- Only uploads files that don't exist in S3
- Preserves existing files in S3
- Never deletes files from S3
- Maintains directory structure in S3

### Sync Markers
- Creates a marker file (default: syncd.txt) in each subdirectory
- Marker file is only created when:
  - All files in the subdirectory exist in S3
  - All subdirectories have been synced and verified
  - Directory verification is complete
- Contains timestamp of successful sync
- Skips marker creation for partially synced directories

### Periodic Sync
- If sync_interval is specified, runs continuously
- Skips sync if previous sync is still running
- Only uploads new files on each run
- Re-verifies directory contents on each run

## Error Handling

- Logs failed uploads but continues with remaining files
- Reports directory sync status for each subdirectory
- Validates configuration file before starting
- Prevents overlapping sync operations
- Provides detailed logging of sync operations

## Limitations

- Does not update existing files in S3
- Does not delete files from S3
- No support for file versioning
- No comparison of file modification times
- No partial file uploads
- No multi-part uploads for large files
- No encryption configuration
- No support for S3-compatible services

## Contributing

1. Fork the repository
2. Create a feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details.