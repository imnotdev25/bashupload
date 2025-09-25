package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/template/html/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type FileRecord struct {
	ID           uint       `json:"id" gorm:"primaryKey"`
	UniqueID     string     `json:"unique_id" gorm:"unique;not null"`
	OriginalName string     `json:"original_name" gorm:"not null"`
	FilePath     string     `json:"file_path" gorm:"not null"`
	FileSize     int64      `json:"file_size" gorm:"not null"`
	MimeType     string     `json:"mime_type"`
	Extension    string     `json:"extension"`
	UploadedAt   time.Time  `json:"uploaded_at" gorm:"autoCreateTime"`
	Downloads    int        `json:"downloads" gorm:"default:0"`
	IPAddress    string     `json:"ip_address"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type UploadResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	UniqueID    string `json:"unique_id,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
	FileSize    int64  `json:"file_size,omitempty"`
}

var (
	db             *gorm.DB
	apiKey         string
	maxUpload      int64
	maxDownloads   int
	expireDuration time.Duration
)

func main() {
	// Initialize database
	initDB()

	// Get API key from environment
	apiKey = os.Getenv("API_KEY")
	if apiKey != "" {
		log.Printf("API Key authentication enabled")
	} else {
		log.Printf("API Key authentication disabled - public access")
	}

	// Get max upload size from environment (default 1GB)
	maxUploadStr := getEnv("MAX_UPLOAD_SIZE", "1GB")
	var err error
	maxUpload, err = parseSize(maxUploadStr)
	if err != nil {
		log.Printf("Invalid MAX_UPLOAD_SIZE value '%s', using default 1GB", maxUploadStr)
		maxUpload = 1073741824 // 1GB
	}
	// Get max download count from environment (default 1)
	maxDownloadStr := getEnv("MAX_DOWNLOADS", "1")
	var err2 error
	maxDownloads, err2 = strconv.Atoi(maxDownloadStr)
	if err2 != nil || maxDownloads < 1 {
		log.Printf("Invalid MAX_DOWNLOADS value '%s', using default 1", maxDownloadStr)
		maxDownloads = 1
	}
	log.Printf("Maximum downloads per file: %d", maxDownloads)

	// Get file expiration duration from environment (default 3D)
	expireStr := getEnv("FILE_EXPIRE_AFTER", "3D")
	var err3 error
	expireDuration, err3 = parseDuration(expireStr)
	if err3 != nil {
		log.Printf("Invalid FILE_EXPIRE_AFTER value '%s', using default 3 days", expireStr)
		expireDuration = 72 * time.Hour // 3 days
	}
	log.Printf("Files expire after: %s", formatDuration(expireDuration))

	// Create uploads and templates directories
	os.MkdirAll("./uploads", os.ModePerm)
	os.MkdirAll("./templates", os.ModePerm)
	os.MkdirAll("./static", os.ModePerm)

	// Initialize template engine
	engine := html.New("./templates", ".html")

	// Initialize Fiber app with optimized settings and template engine
	app := fiber.New(fiber.Config{
		Views:             engine,
		BodyLimit:         int(maxUpload + (10 * 1024 * 1024)), // Add 10MB buffer for headers/metadata
		ReadTimeout:       30 * time.Minute,
		WriteTimeout:      30 * time.Minute,
		ServerHeader:      "bashupload/2.0",
		AppName:           "bashupload - High Performance File Uploader",
		StreamRequestBody: true,
	})

	// Middleware
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New())

	// Rate limiting
	app.Use(limiter.New(limiter.Config{
		Max:        100,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
	}))

	// Routes
	setupRoutes(app)

	// Clean up expired files periodically
	go cleanupExpiredFiles()

	// Start server
	port := getEnv("PORT", "3000")
	log.Printf("Server starting on port %s", port)
	log.Printf("Upload endpoint: http://localhost:%s/api/upload", port)
	log.Printf("Web interface: http://localhost:%s", port)
	log.Printf("bashupload server ready!")

	log.Fatal(app.Listen(":" + port))
}

func cleanupExpiredFiles() {
	ticker := time.NewTicker(1 * time.Hour) // Check every hour
	defer ticker.Stop()

	for range ticker.C {
		var expiredFiles []FileRecord
		db.Where("expires_at < ? OR downloads >= ?", time.Now(), maxDownloads).Find(&expiredFiles)

		for _, file := range expiredFiles {
			// Remove file from disk
			os.Remove(file.FilePath)
			// Remove from database
			db.Delete(&file)
		}

		if len(expiredFiles) > 0 {
			log.Printf("Cleaned up %d expired files", len(expiredFiles))
		}
	}
}

func initDB() {
	var err error
	db, err = gorm.Open(sqlite.Open("bashupload.db"), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	// Migrate the schema
	err = db.AutoMigrate(&FileRecord{})
	if err != nil {
		log.Fatal("Failed to migrate database:", err)
	}

	log.Println("Database initialized successfully")
}

func setupRoutes(app *fiber.App) {
	// API routes
	api := app.Group("/api")

	// Apply API key middleware if API_KEY is set
	if apiKey != "" {
		api.Use(apiKeyMiddleware)
		// Also protect the main upload route for cURL uploads
		app.Put("/", apiKeyMiddleware, handleCurlUpload)
	} else {
		app.Put("/", handleCurlUpload)
	}

	api.Post("/upload", handleFileUpload)
	api.Get("/files/:id", getFileInfo)
	api.Get("/stats", getStats)

	// Download route (no auth required for downloads)
	app.Get("/d/:filename", handleFileDownload)
	app.Get("/download/:filename", handleFileDownload)

	// Web interface
	app.Get("/", serveWebInterface)
	app.Static("/static", "./static")
}

func apiKeyMiddleware(c *fiber.Ctx) error {
	if apiKey == "" {
		return c.Next()
	}

	// Check for API key in header
	providedKey := c.Get("X-API-Key")
	if providedKey == "" {
		// Check for API key in query parameter
		providedKey = c.Query("api_key")
	}
	if providedKey == "" {
		// Check for API key in form data
		providedKey = c.FormValue("api_key")
	}

	if providedKey != apiKey {
		return c.Status(401).JSON(fiber.Map{
			"success": false,
			"message": "Invalid or missing API key",
		})
	}

	return c.Next()
}

func handleCurlUpload(c *fiber.Ctx) error {
	// Get filename from URL or Content-Disposition
	filename := c.Get("Content-Disposition")
	if filename == "" {
		// Try to get from query parameter or default
		filename = c.Query("filename", "upload.bin")
	} else {
		// Extract filename from Content-Disposition header
		if idx := strings.Index(filename, `filename="`); idx != -1 {
			start := idx + 10
			if end := strings.Index(filename[start:], `"`); end != -1 {
				filename = filename[start : start+end]
			}
		}
	}

	// Generate unique ID
	uniqueID := generateUniqueID()

	// Get file extension
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".bin" // Default extension for files without extension
	}

	// Create file path with original extension
	newFilename := uniqueID + ext
	filePath := filepath.Join("uploads", newFilename)

	// Get file size
	contentLength := c.Get("Content-Length")
	fileSize, _ := strconv.ParseInt(contentLength, 10, 64)

	// Check file size (configurable limit)
	if fileSize > maxUpload {
		return c.Status(413).SendString(fmt.Sprintf("File too large. Maximum size is %s", formatBytes(maxUpload)))
	}

	// Save uploaded data to file
	file, err := os.Create(filePath)
	if err != nil {
		return c.Status(500).SendString("Failed to create file")
	}
	defer file.Close()

	// Stream body to file
	_, err = io.Copy(file, c.Context().RequestBodyStream())
	if err != nil {
		os.Remove(filePath)
		return c.Status(500).SendString("Failed to save file")
	}

	// Get actual file size
	fileInfo, _ := os.Stat(filePath)
	actualSize := fileInfo.Size()

	// Get client IP
	clientIP := c.IP()

	// Save to database with configurable expiration
	expiresAt := time.Now().Add(expireDuration)
	fileRecord := FileRecord{
		UniqueID:     uniqueID,
		OriginalName: filename,
		FilePath:     filePath,
		FileSize:     actualSize,
		MimeType:     c.Get("Content-Type"),
		Extension:    ext,
		IPAddress:    clientIP,
		ExpiresAt:    &expiresAt,
	}

	result := db.Create(&fileRecord)
	if result.Error != nil {
		// Clean up file if database save fails
		os.Remove(filePath)
		return c.Status(500).SendString("Failed to save file metadata")
	}

	// Generate download URL with extension
	baseURL := getBaseURL(c)
	downloadURL := fmt.Sprintf("%s/d/%s%s", baseURL, uniqueID, ext)

	// Return plain text response (bashupload style)
	return c.SendString(downloadURL)
}

func handleFileUpload(c *fiber.Ctx) error {
	// Get file from multipart form
	file, err := c.FormFile("file")
	if err != nil {
		return c.Status(400).JSON(UploadResponse{
			Success: false,
			Message: "No file provided",
		})
	}

	// Check file size (configurable limit)
	if file.Size > maxUpload {
		return c.Status(413).JSON(UploadResponse{
			Success: false,
			Message: fmt.Sprintf("File too large. Maximum size is %s", formatBytes(maxUpload)),
		})
	}

	// Generate unique ID
	uniqueID := generateUniqueID()

	// Get file extension
	ext := filepath.Ext(file.Filename)
	if ext == "" {
		ext = ".bin" // Default extension for files without extension
	}

	// Create file path with original extension
	fileName := uniqueID + ext
	filePath := filepath.Join("uploads", fileName)

	// Save file
	err = c.SaveFile(file, filePath)
	if err != nil {
		return c.Status(500).JSON(UploadResponse{
			Success: false,
			Message: "Failed to save file",
		})
	}

	// Get client IP
	clientIP := c.IP()

	// Save to database with configurable expiration
	expiresAt := time.Now().Add(expireDuration)
	fileRecord := FileRecord{
		UniqueID:     uniqueID,
		OriginalName: file.Filename,
		FilePath:     filePath,
		FileSize:     file.Size,
		MimeType:     file.Header.Get("Content-Type"),
		Extension:    ext,
		IPAddress:    clientIP,
		ExpiresAt:    &expiresAt,
	}

	result := db.Create(&fileRecord)
	if result.Error != nil {
		// Clean up file if database save fails
		os.Remove(filePath)
		return c.Status(500).JSON(UploadResponse{
			Success: false,
			Message: "Failed to save file metadata",
		})
	}

	// Generate download URL with extension
	baseURL := getBaseURL(c)
	downloadURL := fmt.Sprintf("%s/d/%s%s", baseURL, uniqueID, ext)

	return c.JSON(UploadResponse{
		Success:     true,
		Message:     "File uploaded successfully",
		UniqueID:    uniqueID,
		DownloadURL: downloadURL,
		FileSize:    file.Size,
	})
}

func handleFileDownload(c *fiber.Ctx) error {
	filename := c.Params("filename")

	// Extract unique ID and extension from filename
	var uniqueID, _ string
	if lastDot := strings.LastIndex(filename, "."); lastDot != -1 {
		uniqueID = filename[:lastDot]
		_ = filename[lastDot:]
	} else {
		uniqueID = filename
	}

	var fileRecord FileRecord
	result := db.Where("unique_id = ?", uniqueID).First(&fileRecord)
	if result.Error != nil {
		return c.Status(404).SendString("File not found")
	}

	// Check if file has expired
	if fileRecord.ExpiresAt != nil && time.Now().After(*fileRecord.ExpiresAt) {
		// Clean up expired file
		os.Remove(fileRecord.FilePath)
		db.Delete(&fileRecord)
		return c.Status(404).SendString("File has expired")
	}

	// Check if file exists on disk
	if _, err := os.Stat(fileRecord.FilePath); os.IsNotExist(err) {
		return c.Status(404).SendString("File not found on disk")
	}

	// Check if download limit exceeded
	if fileRecord.Downloads >= maxDownloads {
		// Clean up file after max downloads reached
		os.Remove(fileRecord.FilePath)
		db.Delete(&fileRecord)
		if maxDownloads == 1 {
			return c.Status(410).SendString("File has already been downloaded and removed")
		} else {
			return c.Status(410).SendString(fmt.Sprintf("File has reached maximum download limit (%d) and was removed", maxDownloads))
		}
	}

	// Increment download counter
	db.Model(&fileRecord).Update("downloads", fileRecord.Downloads+1)

	// Set appropriate headers
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", fileRecord.OriginalName))
	c.Set("Content-Length", strconv.FormatInt(fileRecord.FileSize, 10))

	if fileRecord.MimeType != "" {
		c.Set("Content-Type", fileRecord.MimeType)
	}

	// Stream file
	return c.SendFile(fileRecord.FilePath)
}

func getFileInfo(c *fiber.Ctx) error {
	uniqueID := c.Params("id")

	var fileRecord FileRecord
	result := db.Where("unique_id = ?", uniqueID).First(&fileRecord)
	if result.Error != nil {
		return c.Status(404).JSON(fiber.Map{
			"success": false,
			"message": "File not found",
		})
	}

	return c.JSON(fiber.Map{
		"success": true,
		"data":    fileRecord,
	})
}

func getStats(c *fiber.Ctx) error {
	var totalFiles int64
	var totalSize int64

	db.Model(&FileRecord{}).Count(&totalFiles)
	db.Model(&FileRecord{}).Select("COALESCE(SUM(file_size), 0)").Row().Scan(&totalSize)

	return c.JSON(fiber.Map{
		"success":              true,
		"total_files":          totalFiles,
		"total_size":           totalSize,
		"total_size_formatted": formatBytes(totalSize),
	})
}

func serveWebInterface(c *fiber.Ctx) error {
	requiresAuth := apiKey != ""

	// Prepare auth header for curl example
	authHeader := ""
	if requiresAuth {
		authHeader = ` -H "X-API-Key: YOUR_API_KEY"`
	}

	// Prepare download limit description
	downloadLimit := "single download"
	if maxDownloads > 1 {
		downloadLimit = fmt.Sprintf("%d downloads", maxDownloads)
	}

	// Prepare expiration description
	expireText := formatDuration(expireDuration)

	// Template data
	data := fiber.Map{
		"RequiresAuth":  requiresAuth,
		"AuthHeader":    authHeader,
		"MaxUploadSize": formatBytes(maxUpload),
		"DownloadLimit": downloadLimit,
		"MaxDownloads":  maxDownloads,
		"ExpireTime":    expireText,
	}

	return c.Render("index", data)
}

func generateUniqueID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func getBaseURL(c *fiber.Ctx) string {
	scheme := "http"
	if c.Protocol() == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, c.Get("Host"))
}

func getEnv(key, defaultVal string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultVal
}

func parseDuration(durationStr string) (time.Duration, error) {
	// Remove spaces and convert to lowercase
	durationStr = strings.TrimSpace(strings.ToLower(durationStr))

	// If it's just a number, treat as hours
	if num, err := strconv.ParseFloat(durationStr, 64); err == nil {
		return time.Duration(num * float64(time.Hour)), nil
	}

	// Extract number and unit
	var numStr string
	var unit string

	// Find where the number ends and unit begins
	i := 0
	for i < len(durationStr) && (durationStr[i] >= '0' && durationStr[i] <= '9' || durationStr[i] == '.') {
		i++
	}

	if i == 0 {
		return 0, fmt.Errorf("invalid duration format: %s", durationStr)
	}

	numStr = durationStr[:i]
	unit = durationStr[i:]

	// Parse the number (support decimal)
	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number in duration: %s", numStr)
	}

	// Convert based on unit
	var multiplier time.Duration
	switch unit {
	case "", "h", "hour", "hours":
		multiplier = time.Hour
	case "m", "min", "minute", "minutes":
		multiplier = time.Minute
	case "d", "day", "days":
		multiplier = 24 * time.Hour
	case "w", "week", "weeks":
		multiplier = 7 * 24 * time.Hour
	case "mo", "month", "months":
		multiplier = 30 * 24 * time.Hour // Approximate
	case "y", "year", "years":
		multiplier = 365 * 24 * time.Hour // Approximate
	default:
		return 0, fmt.Errorf("unknown duration unit: %s", unit)
	}

	result := time.Duration(num * float64(multiplier))
	return result, nil
}

func formatDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%.0f minutes", d.Minutes())
	} else if d < 24*time.Hour {
		return fmt.Sprintf("%.1f hours", d.Hours())
	} else if d < 7*24*time.Hour {
		days := d.Hours() / 24
		return fmt.Sprintf("%.1f days", days)
	} else if d < 30*24*time.Hour {
		weeks := d.Hours() / (7 * 24)
		return fmt.Sprintf("%.1f weeks", weeks)
	} else if d < 365*24*time.Hour {
		months := d.Hours() / (30 * 24)
		return fmt.Sprintf("%.1f months", months)
	} else {
		years := d.Hours() / (365 * 24)
		return fmt.Sprintf("%.1f years", years)
	}
}

func parseSize(sizeStr string) (int64, error) {
	// Remove spaces and convert to lowercase
	sizeStr = strings.TrimSpace(strings.ToLower(sizeStr))

	// If it's just a number, treat as bytes
	if num, err := strconv.ParseInt(sizeStr, 10, 64); err == nil {
		return num, nil
	}

	// Extract number and unit
	var numStr string
	var unit string

	// Find where the number ends and unit begins
	i := 0
	for i < len(sizeStr) && (sizeStr[i] >= '0' && sizeStr[i] <= '9' || sizeStr[i] == '.') {
		i++
	}

	if i == 0 {
		return 0, fmt.Errorf("invalid size format: %s", sizeStr)
	}

	numStr = sizeStr[:i]
	unit = sizeStr[i:]

	// Parse the number (support decimal)
	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number in size: %s", numStr)
	}

	// Convert based on unit
	var multiplier int64
	switch unit {
	case "", "b", "byte", "bytes":
		multiplier = 1
	case "k", "kb", "kib":
		multiplier = 1024
	case "m", "mb", "mib":
		multiplier = 1024 * 1024
	case "g", "gb", "gib":
		multiplier = 1024 * 1024 * 1024
	case "t", "tb", "tib":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unknown unit: %s", unit)
	}

	result := int64(num * float64(multiplier))
	return result, nil
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
