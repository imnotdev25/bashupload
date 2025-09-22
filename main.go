package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
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
	db     *gorm.DB
	apiKey string
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

	// Create uploads and templates directories
	os.MkdirAll("./uploads", os.ModePerm)
	os.MkdirAll("./templates", os.ModePerm)
	os.MkdirAll("./static", os.ModePerm)

	// Initialize template engine
	engine := html.New("./templates", ".html")

	// Initialize Fiber app with optimized settings and template engine
	app := fiber.New(fiber.Config{
		Views:             engine,
		BodyLimit:         50 * 1024 * 1024 * 1024, // 50GB limit
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
		db.Where("expires_at < ? OR downloads >= 1", time.Now()).Find(&expiredFiles)

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
	db, err = gorm.Open(sqlite.Open("fileuploader.db"), &gorm.Config{})
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

	// Check file size (50GB limit)
	if fileSize > 50*1024*1024*1024 {
		return c.Status(413).SendString("File too large. Maximum size is 50GB")
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

	// Save to database with expiration (3 days)
	expiresAt := time.Now().Add(72 * time.Hour)
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

	// Return plain text response (bashupload static)
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

	// Check file size (50GB limit)
	if fileSize > 50*1024*1024*1024 {
		return c.Status(413).JSON(UploadResponse{
			Success: false,
			Message: "File too large. Maximum size is 50GB",
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

	// Save to database with expiration (3 days)
	expiresAt := time.Now().Add(72 * time.Hour)
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

	// Check if already downloaded once (bashupload static)
	if fileRecord.Downloads >= 1 {
		// Clean up file after first download
		os.Remove(fileRecord.FilePath)
		db.Delete(&fileRecord)
		return c.Status(410).SendString("File has already been downloaded and removed")
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

	// Template data
	data := fiber.Map{
		"RequiresAuth": requiresAuth,
		"AuthHeader":   authHeader,
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
