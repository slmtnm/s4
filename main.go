package main

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: s4 <bucket-name>")
		fmt.Println("\nS4 is a TUI (Terminal User Interface) for browsing S3 buckets.")
		fmt.Println("It reads configuration from .s3cfg file (compatible with s3cmd).")
		fmt.Println("\nExample: s4 my-bucket")
		os.Exit(1)
	}

	bucketName := os.Args[1]

	// Load S3 configuration
	config, err := LoadS3Config()
	if err != nil {
		fmt.Printf("No S3 configuration found: %s\n", err)
		fmt.Println()
		
		// Offer interactive setup
		config, err = InteractiveS3Setup()
		if err != nil {
			fmt.Printf("Setup cancelled or failed: %s\n", err)
			fmt.Println("\nPlease create a .s3cfg file manually in one of these locations:")
			fmt.Println("  - Current directory: .s3cfg")
			fmt.Println("  - Home directory: ~/.s3cfg")
			fmt.Println("  - System directory: /etc/s3cfg")
			fmt.Println("\nSee example.s3cfg for the required format.")
			os.Exit(1)
		}
	}

	// Create S3 client
	s3Client, err := NewS3Client(config)
	if err != nil {
		fmt.Printf("Error creating S3 client: %s\n", err)
		os.Exit(1)
	}

	// Test bucket access
	ctx := context.Background()
	if err := s3Client.HeadBucket(ctx, bucketName); err != nil {
		fmt.Printf("Error accessing bucket '%s': %s\n", bucketName, err)
		fmt.Println("\nPlease check:")
		fmt.Println("  - Bucket name is correct")
		fmt.Println("  - Your credentials have access to this bucket")
		fmt.Println("  - Your S3 endpoint configuration is correct")
		os.Exit(1)
	}

	// Initialize and run TUI
	model := NewModel(s3Client, bucketName)
	program := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		fmt.Printf("Error running TUI: %s\n", err)
		os.Exit(1)
	}
}