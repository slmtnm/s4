# S4 - terminal browser for S3 storages

S4 is a fast, feature-rich Terminal User Interface (TUI) for browsing S3-compatible storage services. Built with Go.

## Features

- **Directory Navigation**: Browse S3 buckets like a file system
- **File Preview**: View text files with adaptive width and vim-like navigation
- **File Download**: Download files from S3 to your local directory
- **File Upload**: Upload files with full local filesystem navigation

## Installation

```bash
go install github.com/slmtnm/s4@latest
```

## Configuration

S4 reads configuration from `.s3cfg` files (compatible with s3cmd). If no configuration file is found, S4 will offer an interactive setup to create one for you.

Configuration file locations:
- Current directory: `.s3cfg`
- Home directory: `~/.s3cfg`
- System directory: `/etc/s3cfg`

### Example .s3cfg

```ini
[default]
access_key = your-access-key
secret_key = your-secret-key
host_base = s3.amazonaws.com
host_bucket = %(bucket)s.s3.amazonaws.com
use_https = True
signature_v2 = False
bucket_location = us-east-1
```

## Usage

```bash
s4 <bucket-name>
```

### Keyboard Shortcuts

#### Navigation
- `↑/k` - Move cursor up
- `↓/j` - Move cursor down
- `←/h` - Go back to parent directory
- `→/l/Enter` - Enter directory or preview file
- `r` - Refresh current directory

#### Actions
- `d` - Download selected file to current directory
- `u` - Upload file from current directory to S3
- `x` - Delete selected file from S3
- `?` - Show help
- `q/Ctrl+C` - Quit application
- `Esc` - Go back (from preview, upload, or help)

## Development Setup

### Prerequisites

- Go 1.21 or later
- Docker Compose

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

MIT License - see LICENSE file for details.
