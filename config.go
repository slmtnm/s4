package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/ini.v1"
)

// S3Config holds the S3 configuration parsed from .s3cfg
type S3Config struct {
	AccessKey       string
	SecretKey       string
	HostBase        string
	HostBucket      string
	UseHTTPS        bool
	SignatureV2     bool
	Region          string
}

// LoadS3Config loads configuration from .s3cfg file
func LoadS3Config() (*S3Config, error) {
	// Try to find .s3cfg in common locations
	configPaths := []string{
		".s3cfg",
		filepath.Join(os.Getenv("HOME"), ".s3cfg"),
		"/etc/s3cfg",
	}

	var configPath string
	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			configPath = path
			break
		}
	}

	if configPath == "" {
		return nil, fmt.Errorf(".s3cfg file not found in any of the standard locations")
	}

	cfg, err := ini.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load .s3cfg: %w", err)
	}

	section := cfg.Section("default")
	
	config := &S3Config{
		AccessKey:   section.Key("access_key").String(),
		SecretKey:   section.Key("secret_key").String(),
		HostBase:    section.Key("host_base").MustString("s3.amazonaws.com"),
		HostBucket:  section.Key("host_bucket").MustString("%(bucket)s.s3.amazonaws.com"),
		UseHTTPS:    section.Key("use_https").MustBool(true),
		SignatureV2: section.Key("signature_v2").MustBool(false),
		Region:      section.Key("bucket_location").MustString("us-east-1"),
	}

	if config.AccessKey == "" || config.SecretKey == "" {
		return nil, fmt.Errorf("access_key and secret_key must be specified in .s3cfg")
	}

	return config, nil
}

// GetEndpointURL returns the endpoint URL for the S3 service
func (c *S3Config) GetEndpointURL() string {
	protocol := "https"
	if !c.UseHTTPS {
		protocol = "http"
	}
	return fmt.Sprintf("%s://%s", protocol, c.HostBase)
}

// InteractiveS3Setup provides an interactive setup for S3 configuration
func InteractiveS3Setup() (*S3Config, error) {
	scanner := bufio.NewScanner(os.Stdin)
	
	fmt.Println("🔧 S4 Interactive Setup")
	fmt.Println("========================")
	fmt.Println()
	fmt.Println("No .s3cfg configuration file found.")
	fmt.Println("Would you like to create one interactively? (y/N)")
	
	fmt.Print("> ")
	if !scanner.Scan() {
		return nil, fmt.Errorf("failed to read input")
	}
	
	response := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if response != "y" && response != "yes" {
		return nil, fmt.Errorf("setup declined by user")
	}
	
	fmt.Println()
	fmt.Println("Great! Let's set up your S3 configuration.")
	fmt.Println()
	fmt.Println("Common configurations:")
	fmt.Println("  • AWS S3: Use your AWS credentials and s3.amazonaws.com")
	fmt.Println("  • MinIO local: Use minioadmin/minioadmin123 and localhost:9000")
	fmt.Println("  • Other S3-compatible: Use your service's endpoint and credentials")
	fmt.Println()
	
	config := &S3Config{}
	
	// Get Access Key
	fmt.Print("Access Key ID: ")
	if !scanner.Scan() {
		return nil, fmt.Errorf("failed to read access key")
	}
	config.AccessKey = strings.TrimSpace(scanner.Text())
	if config.AccessKey == "" {
		return nil, fmt.Errorf("access key cannot be empty")
	}
	
	// Get Secret Key
	fmt.Print("Secret Access Key: ")
	if !scanner.Scan() {
		return nil, fmt.Errorf("failed to read secret key")
	}
	config.SecretKey = strings.TrimSpace(scanner.Text())
	if config.SecretKey == "" {
		return nil, fmt.Errorf("secret key cannot be empty")
	}
	
	// Get Host Base
	fmt.Print("S3 Endpoint (default: s3.amazonaws.com): ")
	if !scanner.Scan() {
		return nil, fmt.Errorf("failed to read endpoint")
	}
	hostBase := strings.TrimSpace(scanner.Text())
	if hostBase == "" {
		config.HostBase = "s3.amazonaws.com"
	} else {
		config.HostBase = hostBase
	}
	
	// Set host bucket based on endpoint
	if config.HostBase == "s3.amazonaws.com" {
		config.HostBucket = "%(bucket)s.s3.amazonaws.com"
	} else {
		config.HostBucket = config.HostBase + "/%(bucket)s"
	}
	
	// Get Region
	fmt.Print("Region (default: us-east-1): ")
	if !scanner.Scan() {
		return nil, fmt.Errorf("failed to read region")
	}
	region := strings.TrimSpace(scanner.Text())
	if region == "" {
		config.Region = "us-east-1"
	} else {
		config.Region = region
	}
	
	// Determine HTTPS usage
	config.UseHTTPS = !strings.Contains(config.HostBase, "localhost") && !strings.Contains(config.HostBase, "127.0.0.1")
	config.SignatureV2 = false
	
	fmt.Println()
	fmt.Printf("Configuration summary:\n")
	fmt.Printf("  Endpoint: %s\n", config.GetEndpointURL())
	fmt.Printf("  Region: %s\n", config.Region)
	fmt.Printf("  HTTPS: %t\n", config.UseHTTPS)
	fmt.Println()
	
	// Ask where to save
	fmt.Println("Where would you like to save this configuration?")
	fmt.Println("1. Current directory (.s3cfg)")
	fmt.Println("2. Home directory (~/.s3cfg)")
	fmt.Print("Choice (1-2, default: 2): ")
	
	if !scanner.Scan() {
		return nil, fmt.Errorf("failed to read save location")
	}
	
	choice := strings.TrimSpace(scanner.Text())
	var configPath string
	
	switch choice {
	case "1":
		configPath = ".s3cfg"
	case "", "2":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		configPath = filepath.Join(homeDir, ".s3cfg")
	default:
		return nil, fmt.Errorf("invalid choice")
	}
	
	// Save configuration
	err := saveS3Config(config, configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to save configuration: %w", err)
	}
	
	fmt.Printf("\n✅ Configuration saved to: %s\n", configPath)
	fmt.Println("You can now use S4 to browse your S3 buckets!")
	fmt.Println()
	
	return config, nil
}

// saveS3Config saves the configuration to a file
func saveS3Config(config *S3Config, path string) error {
	cfg := ini.Empty()
	section := cfg.Section("default")
	
	section.Key("access_key").SetValue(config.AccessKey)
	section.Key("secret_key").SetValue(config.SecretKey)
	section.Key("host_base").SetValue(config.HostBase)
	section.Key("host_bucket").SetValue(config.HostBucket)
	
	if config.UseHTTPS {
		section.Key("use_https").SetValue("True")
	} else {
		section.Key("use_https").SetValue("False")
	}
	
	if config.SignatureV2 {
		section.Key("signature_v2").SetValue("True")
	} else {
		section.Key("signature_v2").SetValue("False")
	}
	
	section.Key("bucket_location").SetValue(config.Region)
	
	return cfg.SaveTo(path)
}