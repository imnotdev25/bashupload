package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

type UploadResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	UniqueID    string `json:"unique_id,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
	FileSize    int64  `json:"file_size,omitempty"`
}

type FileInfo struct {
	Success bool `json:"success"`
	Data    struct {
		ID           uint      `json:"id"`
		UniqueID     string    `json:"unique_id"`
		OriginalName string    `json:"original_name"`
		FileSize     int64     `json:"file_size"`
		MimeType     string    `json:"mime_type"`
		Extension    string    `json:"extension"`
		UploadedAt   time.Time `json:"uploaded_at"`
		Downloads    int       `json:"downloads"`
	} `json:"data"`
}

var (
	serverURL string
	verbose   bool
	apiKey    string
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "uploader",
		Short: "High-performance file uploader CLI",
		Long:  `A CLI tool to upload files up to 10GB and generate secure download links`,
	}

	var uploadCmd = &cobra.Command{
		Use:   "upload [file]",
		Short: "Upload a file",
		Long:  `Upload a file to the server and get a download link`,
		Args:  cobra.ExactArgs(1),
		Run:   uploadFile,
	}

	var infoCmd = &cobra.Command{
		Use:   "info [file-id]",
		Short: "Get file information",
		Long:  `Get information about an uploaded file using its unique ID`,
		Args:  cobra.ExactArgs(1),
		Run:   getFileInfo,
	}

	var downloadCmd = &cobra.Command{
		Use:   "download [file-id] [output-path]",
		Short: "Download a file",
		Long:  `Download a file using its unique ID`,
		Args:  cobra.RangeArgs(1, 2),
		Run:   downloadFile,
	}

	// Add flags
	rootCmd.PersistentFlags().StringVarP(&serverURL, "server", "s", "http://localhost:3000", "Server URL")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	rootCmd.PersistentFlags().StringVarP(&apiKey, "api-key", "k", "", "API key for authentication")

	// Add commands
	rootCmd.AddCommand(uploadCmd)
	rootCmd.AddCommand(infoCmd)
	rootCmd.AddCommand(downloadCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func uploadFile(cmd *cobra.Command, args []string) {
	filePath := args[0]

	// Check if file exists
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: File not found - %v\n", err)
		os.Exit(1)
	}

	if fileInfo.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: Path is a directory, not a file\n")
		os.Exit(1)
	}

	// Check file size (10GB limit)
	if fileInfo.Size() > 10*1024*1024*1024 {
		fmt.Fprintf(os.Stderr, "Error: File too large. Maximum size is 10GB\n")
		os.Exit(1)
	}

	file, err := os.Open(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	fmt.Printf("📁 Uploading: %s (%s)\n", filepath.Base(filePath), formatBytes(fileInfo.Size()))

	// Create progress bar
	bar := progressbar.NewOptions64(fileInfo.Size(),
		progressbar.OptionSetDescription("Uploading..."),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(50),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetRenderBlankState(true),
	)

	// Create multipart form
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Create progress reader
	progressReader := &ProgressReader{
		Reader: file,
		bar:    bar,
	}

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating form file: %v\n", err)
		os.Exit(1)
	}

	_, err = io.Copy(part, progressReader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error copying file: %v\n", err)
		os.Exit(1)
	}

	err = writer.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error closing writer: %v\n", err)
		os.Exit(1)
	}

	bar.Finish()
	fmt.Println("\n🚀 Uploading to server...")

	// Create HTTP request
	uploadURL := strings.TrimRight(serverURL, "/") + "/api/upload"
	req, err := http.NewRequest("POST", uploadURL, &requestBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		os.Exit(1)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Add API key if provided
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	// Send request
	client := &http.Client{
		Timeout: 30 * time.Minute,
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error uploading file: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		os.Exit(1)
	}

	var uploadResp UploadResponse
	err = json.Unmarshal(respBody, &uploadResp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		if verbose {
			fmt.Fprintf(os.Stderr, "Raw response: %s\n", string(respBody))
		}
		os.Exit(1)
	}

	if !uploadResp.Success {
		fmt.Fprintf(os.Stderr, "Upload failed: %s\n", uploadResp.Message)
		os.Exit(1)
	}

	// Display success message
	fmt.Println("\n✅ Upload successful!")
	fmt.Printf("📄 File: %s\n", filepath.Base(filePath))
	fmt.Printf("📏 Size: %s\n", formatBytes(uploadResp.FileSize))
	fmt.Printf("🆔 ID: %s\n", uploadResp.UniqueID)
	fmt.Printf("🔗 Download URL: %s\n", uploadResp.DownloadURL)
	fmt.Println("\n📋 Share this link to allow others to download your file:")
	fmt.Printf("   %s\n", uploadResp.DownloadURL)
}

func getFileInfo(cmd *cobra.Command, args []string) {
	fileID := args[0]

	infoURL := strings.TrimRight(serverURL, "/") + "/api/files/" + fileID

	if verbose {
		fmt.Printf("Fetching info from: %s\n", infoURL)
	}

	req, err := http.NewRequest("GET", infoURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		os.Exit(1)
	}

	// Add API key if provided
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching file info: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		fmt.Fprintf(os.Stderr, "Authentication required. Use --api-key flag.\n")
		os.Exit(1)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		os.Exit(1)
	}

	var fileInfo FileInfo
	err = json.Unmarshal(respBody, &fileInfo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	if !fileInfo.Success {
		fmt.Fprintf(os.Stderr, "File not found\n")
		os.Exit(1)
	}

	// Display file information
	fmt.Println("📄 File Information")
	fmt.Println("==================")
	fmt.Printf("🆔 ID: %s\n", fileInfo.Data.UniqueID)
	fmt.Printf("📁 Original Name: %s\n", fileInfo.Data.OriginalName)
	fmt.Printf("📏 Size: %s\n", formatBytes(fileInfo.Data.FileSize))
	fmt.Printf("📝 MIME Type: %s\n", fileInfo.Data.MimeType)
	fmt.Printf("📎 Extension: %s\n", fileInfo.Data.Extension)
	fmt.Printf("📅 Uploaded: %s\n", fileInfo.Data.UploadedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("📊 Downloads: %d\n", fileInfo.Data.Downloads)
	fmt.Printf("🔗 Download URL: %s/d/%s%s\n", strings.TrimRight(serverURL, "/"), fileInfo.Data.UniqueID, fileInfo.Data.Extension)
}

func downloadFile(cmd *cobra.Command, args []string) {
	filename := args[0] // This should now include the extension

	var outputPath string
	if len(args) > 1 {
		outputPath = args[1]
	}

	downloadURL := strings.TrimRight(serverURL, "/") + "/d/" + filename

	if verbose {
		fmt.Printf("Downloading from: %s\n", downloadURL)
	}

	fmt.Printf("📥 Starting download...\n")

	// Create HTTP request
	resp, err := http.Get(downloadURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading file: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Download failed: HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}

	// Get filename from Content-Disposition header or use provided filename
	defaultFilename := filename
	if contentDisposition := resp.Header.Get("Content-Disposition"); contentDisposition != "" {
		if idx := strings.Index(contentDisposition, `filename="`); idx != -1 {
			start := idx + 10
			if end := strings.Index(contentDisposition[start:], `"`); end != -1 {
				defaultFilename = contentDisposition[start : start+end]
			}
		}
	}

	// Determine output path
	if outputPath == "" {
		outputPath = defaultFilename
	} else if info, err := os.Stat(outputPath); err == nil && info.IsDir() {
		outputPath = filepath.Join(outputPath, defaultFilename)
	}

	// Check if file already exists
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Printf("File %s already exists. Overwrite? (y/N): ", outputPath)
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			fmt.Println("Download cancelled.")
			return
		}
	}

	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
		os.Exit(1)
	}
	defer outFile.Close()

	// Get file size for progress bar
	fileSize, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)

	// Create progress bar
	var bar *progressbar.ProgressBar
	if fileSize > 0 {
		bar = progressbar.NewOptions64(fileSize,
			progressbar.OptionSetDescription("Downloading..."),
			progressbar.OptionSetWriter(os.Stderr),
			progressbar.OptionShowBytes(true),
			progressbar.OptionSetWidth(50),
			progressbar.OptionThrottle(100*time.Millisecond),
			progressbar.OptionShowCount(),
			progressbar.OptionSpinnerType(14),
			progressbar.OptionFullWidth(),
			progressbar.OptionSetRenderBlankState(true),
		)
	} else {
		bar = progressbar.NewOptions(-1,
			progressbar.OptionSetDescription("Downloading..."),
			progressbar.OptionSetWriter(os.Stderr),
			progressbar.OptionSpinnerType(14),
		)
	}

	// Copy with progress
	_, err = io.Copy(io.MultiWriter(outFile, bar), resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading file: %v\n", err)
		os.Exit(1)
	}

	bar.Finish()
	fmt.Printf("\n✅ Download complete: %s\n", outputPath)
}

// ProgressReader wraps an io.Reader and updates a progress bar
type ProgressReader struct {
	Reader io.Reader
	bar    *progressbar.ProgressBar
}

func (pr *ProgressReader) Read(p []byte) (n int, err error) {
	n, err = pr.Reader.Read(p)
	if n > 0 {
		pr.bar.Add(n)
	}
	return
}

func formatBytes(bytes int64) string {
	if bytes == 0 {
		return "0 Bytes"
	}

	k := int64(1024)
	sizes := []string{"Bytes", "KB", "MB", "GB", "TB"}

	i := 0
	for bytes >= k && i < len(sizes)-1 {
		bytes /= k
		i++
	}

	return fmt.Sprintf("%.2f %s", float64(bytes), sizes[i])
}
