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
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type FileRecord struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	UniqueID    string    `json:"unique_id" gorm:"unique;not null"`
	OriginalName string   `json:"original_name" gorm:"not null"`
	FilePath    string    `json:"file_path" gorm:"not null"`
	FileSize    int64     `json:"file_size" gorm:"not null"`
	MimeType    string    `json:"mime_type"`
	Extension   string    `json:"extension"`
	UploadedAt  time.Time `json:"uploaded_at" gorm:"autoCreateTime"`
	Downloads   int       `json:"downloads" gorm:"default:0"`
	IPAddress   string    `json:"ip_address"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type UploadResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	UniqueID   string `json:"unique_id,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
	FileSize   int64  `json:"file_size,omitempty"`
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

	// Create uploads directory
	os.MkdirAll("./uploads", os.ModePerm)

	// Initialize Fiber app with optimized settings
	app := fiber.New(fiber.Config{
		BodyLimit:    10 * 1024 * 1024 * 1024, // 10GB limit
		ReadTimeout:  30 * time.Minute,
		WriteTimeout: 30 * time.Minute,
		ServerHeader: "FileUploader/1.0",
		AppName:      "High Performance File Uploader",
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
		UniqueID:    uniqueID,
		OriginalName: filename,
		FilePath:    filePath,
		FileSize:    actualSize,
		MimeType:    c.Get("Content-Type"),
		Extension:   ext,
		IPAddress:   clientIP,
		ExpiresAt:   &expiresAt,
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

	// Check file size (10GB limit)
	if file.Size > 10*1024*1024*1024 {
		return c.Status(413).JSON(UploadResponse{
			Success: false,
			Message: "File too large. Maximum size is 10GB",
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
		UniqueID:    uniqueID,
		OriginalName: file.Filename,
		FilePath:    filePath,
		FileSize:    file.Size,
		MimeType:    file.Header.Get("Content-Type"),
		Extension:   ext,
		IPAddress:   clientIP,
		ExpiresAt:   &expiresAt,
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
	var uniqueID, ext string
	if lastDot := strings.LastIndex(filename, "."); lastDot != -1 {
		uniqueID = filename[:lastDot]
		ext = filename[lastDot:]
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

	// Check if already downloaded once (bashupload style)
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
		"success":     true,
		"total_files": totalFiles,
		"total_size":  totalSize,
		"total_size_formatted": formatBytes(totalSize),
	})
}

func serveWebInterface(c *fiber.Ctx) error {
	requiresAuth := apiKey != ""

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>bashupload</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@300;400;500;700&display=swap');
        
        * { margin: 0; padding: 0; box-sizing: border-box; }
        
        body {
            font-family: 'JetBrains Mono', 'Courier New', monospace;
            background: #0a0a0a;
            color: #00ff41;
            min-height: 100vh;
            padding: 20px;
            line-height: 1.6;
            font-size: 14px;
        }
        
        .container {
            max-width: 800px;
            margin: 0 auto;
            background: #111;
            border: 2px solid #00ff41;
            border-radius: 8px;
            padding: 30px;
            box-shadow: 0 0 20px rgba(0, 255, 65, 0.3);
        }
        
        h1 {
            font-size: 2.5em;
            margin-bottom: 20px;
            text-shadow: 0 0 10px #00ff41;
            font-weight: 700;
        }
        
        .description {
            margin-bottom: 30px;
            color: #888;
            font-size: 16px;
        }
        
        .terminal-box {
            background: #000;
            border: 1px solid #333;
            border-radius: 4px;
            padding: 20px;
            margin: 20px 0;
            font-family: inherit;
            position: relative;
            overflow-x: auto;
        }
        
        .terminal-box::before {
            content: "$ ";
            color: #00ff41;
            font-weight: bold;
        }
        
        .command {
            color: #00ff41;
            font-weight: 500;
        }
        
        .upload-area {
            border: 2px dashed #333;
            border-radius: 8px;
            padding: 40px 20px;
            text-align: center;
            margin: 30px 0;
            cursor: pointer;
            transition: all 0.3s ease;
            background: #111;
        }
        
        .upload-area:hover, .upload-area.dragover {
            border-color: #00ff41;
            background: #0f1f0f;
            box-shadow: 0 0 15px rgba(0, 255, 65, 0.2);
        }
        
        .upload-area.dragover {
            animation: pulse 1s infinite;
        }
        
        @keyframes pulse {
            0%% { box-shadow: 0 0 15px rgba(0, 255, 65, 0.2); }
            50%% { box-shadow: 0 0 25px rgba(0, 255, 65, 0.4); }
            100%% { box-shadow: 0 0 15px rgba(0, 255, 65, 0.2); }
        }
        
        .file-input { display: none; }
        
        .btn {
            background: #000;
            color: #00ff41;
            border: 2px solid #00ff41;
            padding: 12px 24px;
            font-family: inherit;
            font-size: 14px;
            cursor: pointer;
            border-radius: 4px;
            transition: all 0.3s ease;
            margin: 10px;
        }
        
        .btn:hover {
            background: #00ff41;
            color: #000;
            box-shadow: 0 0 15px rgba(0, 255, 65, 0.5);
        }
        
        .btn:disabled {
            opacity: 0.5;
            cursor: not-allowed;
        }
        
        .progress {
            width: 100%%;
            height: 20px;
            background: #222;
            border: 1px solid #333;
            border-radius: 4px;
            margin: 20px 0;
            overflow: hidden;
            display: none;
        }
        
        .progress-bar {
            height: 100%%;
            background: linear-gradient(90deg, #00ff41, #00cc33);
            width: 0%%;
            transition: width 0.3s ease;
            animation: matrix-flow 2s linear infinite;
        }
        
        @keyframes matrix-flow {
            0%% { background-position: 0 0; }
            100%% { background-position: 20px 0; }
        }
        
        .result {
            margin: 20px 0;
            padding: 20px;
            border-radius: 4px;
            display: none;
            font-family: inherit;
        }
        
        .success {
            background: #001100;
            border: 1px solid #00ff41;
            color: #00ff41;
        }
        
        .error {
            background: #110000;
            border: 1px solid #ff4444;
            color: #ff4444;
        }
        
        .download-link {
            background: #000;
            color: #00ff41;
            border: 1px solid #00ff41;
            padding: 10px 15px;
            text-decoration: none;
            border-radius: 4px;
            display: inline-block;
            margin: 10px 5px;
            font-family: inherit;
            font-size: 12px;
            transition: all 0.3s ease;
        }
        
        .download-link:hover {
            background: #00ff41;
            color: #000;
        }
        
        .file-info {
            color: #666;
            font-size: 12px;
            margin: 10px 0;
        }
        
        .auth-section {
            margin: 20px 0;
            padding: 20px;
            border: 1px solid #333;
            border-radius: 4px;
            background: #0a0a0a;
        }
        
        .auth-input {
            background: #000;
            border: 1px solid #333;
            color: #00ff41;
            padding: 10px;
            font-family: inherit;
            font-size: 14px;
            border-radius: 4px;
            width: 100%%;
            margin: 10px 0;
        }
        
        .auth-input:focus {
            outline: none;
            border-color: #00ff41;
            box-shadow: 0 0 10px rgba(0, 255, 65, 0.3);
        }
        
        .curl-example {
            background: #000;
            border: 1px solid #333;
            border-radius: 4px;
            padding: 15px;
            margin: 20px 0;
            overflow-x: auto;
            font-size: 12px;
        }
        
        .alternative {
            margin: 20px 0;
            color: #888;
            text-align: center;
        }
        
        .alternative a {
            color: #00ff41;
            text-decoration: underline;
        }
        
        .alternative a:hover {
            text-shadow: 0 0 5px #00ff41;
        }
        
        .stats {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
            gap: 20px;
            margin: 30px 0;
        }
        
        .stat-box {
            background: #000;
            border: 1px solid #333;
            border-radius: 4px;
            padding: 15px;
            text-align: center;
        }
        
        .stat-value {
            font-size: 1.5em;
            color: #00ff41;
            font-weight: bold;
        }
        
        .stat-label {
            color: #666;
            font-size: 0.9em;
        }
        
        @media (max-width: 600px) {
            .container { 
                margin: 10px; 
                padding: 20px; 
            }
            h1 { font-size: 2em; }
            .terminal-box { padding: 15px; }
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>bashupload</h1>
        
        <div class="description">
            Upload files from command line to easily share between servers,<br>
            desktops and mobiles, 50G max. Files are stored for 3 days and can be<br>
            downloaded only once.
        </div>
        
        <div class="terminal-box">
            <span class="command">curl bashupload.com -T your_file.txt%s</span>
        </div>
        
        %s
        
        <div class="upload-area" onclick="document.getElementById('fileInput').click()">
            <p>📁 alternatively <strong>choose file(s)</strong> to upload</p>
            <p class="file-info">Maximum file size: 50GB • Files expire in 3 days • Single download only</p>
        </div>
        
        <input type="file" id="fileInput" class="file-input">
        
        <div class="progress">
            <div class="progress-bar"></div>
        </div>
        
        <button class="btn" onclick="uploadFile()">► UPLOAD FILE</button>
        
        <div id="result" class="result"></div>
        
        <div class="alternative">
            alternatively <a href="#" onclick="showCurlExample()">read more docs</a>
        </div>
    </div>

    <script>
        let selectedFile = null;
        const uploadArea = document.querySelector('.upload-area');
        const fileInput = document.getElementById('fileInput');
        const progressBar = document.querySelector('.progress');
        const progressFill = document.querySelector('.progress-bar');
        const result = document.getElementById('result');
        const requiresAuth = %t;
        let apiKey = '';

        // Get API key if required
        if (requiresAuth) {
            const savedKey = localStorage.getItem('api_key');
            if (savedKey) {
                apiKey = savedKey;
            }
        }

        // Drag and drop handlers
        uploadArea.addEventListener('dragover', (e) => {
            e.preventDefault();
            uploadArea.classList.add('dragover');
        });

        uploadArea.addEventListener('dragleave', () => {
            uploadArea.classList.remove('dragover');
        });

        uploadArea.addEventListener('drop', (e) => {
            e.preventDefault();
            uploadArea.classList.remove('dragover');
            const files = e.dataTransfer.files;
            if (files.length > 0) {
                selectedFile = files[0];
                updateUploadArea();
            }
        });

        fileInput.addEventListener('change', (e) => {
            if (e.target.files.length > 0) {
                selectedFile = e.target.files[0];
                updateUploadArea();
            }
        });

        function updateUploadArea() {
            if (selectedFile) {
                uploadArea.innerHTML = \`
	<p>📄 \${selectedFile.name}</p>
	<p class="file-info">Size: \${formatBytes(selectedFile.size)} • Ready to upload</p>
	\`;
            }
        }

        function setApiKey() {
            const key = document.getElementById('apiKeyInput').value;
            if (key) {
                apiKey = key;
                localStorage.setItem('api_key', key);
                document.querySelector('.auth-section').style.display = 'none';
                showResult('✅ API key saved', 'success');
            }
        }

        function formatBytes(bytes) {
            if (bytes === 0) return '0 Bytes';
            const k = 1024;
            const sizes = ['Bytes', 'KB', 'MB', 'GB'];
            const i = Math.floor(Math.log(bytes) / Math.log(k));
            return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
        }

        async function uploadFile() {
            if (!selectedFile) {
                showResult('❌ Please select a file first', 'error');
                return;
            }

            if (requiresAuth && !apiKey) {
                showResult('❌ API key required. Please enter your API key.', 'error');
                return;
            }

            const formData = new FormData();
            formData.append('file', selectedFile);
            
            if (requiresAuth && apiKey) {
                formData.append('api_key', apiKey);
            }

            const uploadBtn = document.querySelector('.btn');
            uploadBtn.disabled = true;
            uploadBtn.textContent = '⚡ UPLOADING...';
            progressBar.style.display = 'block';
            result.style.display = 'none';

            try {
                const xhr = new XMLHttpRequest();
                
                xhr.upload.addEventListener('progress', (e) => {
                    if (e.lengthComputable) {
                        const percentComplete = (e.loaded / e.total) * 100;
                        progressFill.style.width = percentComplete + '%%';
                    }
                });

                xhr.onload = function() {
                    if (xhr.status === 200) {
                        const response = JSON.parse(xhr.responseText);
                        if (response.success) {
                            const curlCmd = requiresAuth ? 
                                \`curl -H "X-API-Key: YOUR_API_KEY" \${location.origin} -T your_file.txt\` :
                                \`curl \${location.origin} -T your_file.txt\`;
                            
                            showResult(\`
	<div style="margin-bottom: 15px;">
	<div style="color: #00ff41; font-size: 1.2em; margin-bottom: 10px;">✅ UPLOAD SUCCESSFUL</div>
	<div>File: \${selectedFile.name}</div>
	<div>Size: \${formatBytes(response.file_size)}</div>
	<div>Expires: 3 days (single download)</div>
	</div>
	<div class="terminal-box" style="margin: 15px 0; word-break: break-all;">
	<span style="color: #00ff41;">\${response.download_url}</span>
	</div>
	<div>
	<a href="\${response.download_url}" class="download-link" target="_blank">⬇ DOWNLOAD</a>
	<button class="btn" onclick="copyToClipboard('\${response.download_url}')">📋 COPY LINK</button>
	</div>
	\`, 'success');
                        } else {
                            showResult('❌ ' + response.message, 'error');
                        }
                    } else {
                        const errorText = xhr.responseText ? JSON.parse(xhr.responseText).message : 'Upload failed';
                        showResult('❌ ' + errorText, 'error');
                    }
                    resetUpload();
                };

                xhr.onerror = function() {
                    showResult('❌ Network error. Check your connection.', 'error');
                    resetUpload();
                };

                xhr.open('POST', '/api/upload');
                xhr.send(formData);

            } catch (error) {
                showResult('❌ Upload failed: ' + error.message, 'error');
                resetUpload();
            }
        }

        function copyToClipboard(text) {
            navigator.clipboard.writeText(text).then(() => {
                showResult('📋 Link copied to clipboard!', 'success');
            });
        }

        function showResult(message, type) {
            result.innerHTML = message;
            result.className = 'result ' + type;
            result.style.display = 'block';
        }

        function resetUpload() {
            const uploadBtn = document.querySelector('.btn');
            uploadBtn.disabled = false;
            uploadBtn.textContent = '► UPLOAD FILE';
            progressBar.style.display = 'none';
            progressFill.style.width = '0%%';
        }

        function showCurlExample() {
            const authHeader = requiresAuth ? ' -H "X-API-Key: YOUR_API_KEY"' : '';
            alert(\`cURL Examples:

Upload: curl\${authHeader} \${location.origin} -T filename.ext

	Or use form: curl\${authHeader} -F "file=@filename.ext" \${location.origin}/api/upload\`);
        }

        // Check if auth section should be shown
        if (requiresAuth && !apiKey) {
            setTimeout(() => {
                showResult('🔐 This instance requires an API key', 'error');
            }, 1000);
        }
    </script>
</body>
</html>`,
		func() string {
			if requiresAuth {
				return ` -H "X-API-Key: YOUR_API_KEY"`
			}
			return ""
		}(),
		func() string {
			if requiresAuth {
				return `<div class="auth-section">
            <div style="margin-bottom: 15px; color: #ff6600;">🔐 API Key Required</div>
            <input type="password" id="apiKeyInput" class="auth-input" placeholder="Enter your API key..." value="">
            <button class="btn" onclick="setApiKey()">💾 SAVE KEY</button>
        </div>`
			}
			return ""
		}(),
		requiresAuth)

	c.Type("html")
	return c.SendString(html)
}cursor: pointer;
transition: all 0.3s ease;
background: #fafafa;
}
.upload-area:hover { border-color: #667eea; background: #f0f0ff; }
.upload-area.dragover { border-color: #667eea; background: #e6f3ff; }
.file-input { display: none; }
.upload-btn {
background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
color: white;
border: none;
padding: 12px 30px;
border-radius: 25px;
cursor: pointer;
font-size: 16px;
font-weight: 600;
transition: transform 0.2s;
margin: 20px 10px;
}
.upload-btn:hover { transform: translateY(-2px); }
.upload-btn:disabled { opacity: 0.6; cursor: not-allowed; transform: none; }
.progress {
width: 100%;
height: 20px;
background: #f0f0f0;
border-radius: 10px;
margin: 20px 0;
overflow: hidden;
display: none;
}
.progress-bar {
height: 100%;
background: linear-gradient(90deg, #667eea, #764ba2);
width: 0%;
transition: width 0.3s;
}
.result {
margin: 20px 0;
padding: 15px;
border-radius: 10px;
display: none;
}
.success { background: #d4edda; color: #155724; border: 1px solid #c3e6cb; }
.error { background: #f8d7da; color: #721c24; border: 1px solid #f5c6cb; }
.download-link {
background: #28a745;
color: white;
padding: 10px 20px;
text-decoration: none;
border-radius: 5px;
display: inline-block;
margin: 10px;
font-weight: 500;
}
.file-info { font-size: 14px; color: #666; margin: 10px 0; }
@media (max-width: 600px) {
.container { margin: 10px; padding: 30px 20px; }
h1 { font-size: 2em; }
}
</style>
</head>
<body>
<div class="container">
<h1>📁 File Uploader</h1>
<p class="subtitle">Upload files up to 10GB with secure links</p>

<div class="upload-area" onclick="document.getElementById('fileInput').click()">
<p>🗂️ Click here or drag & drop files</p>
<p class="file-info">Maximum file size: 10GB</p>
</div>

<input type="file" id="fileInput" class="file-input">

<div class="progress">
<div class="progress-bar"></div>
</div>

<button class="upload-btn" onclick="uploadFile()">Upload File</button>

<div id="result" class="result"></div>
</div>

<script>
let selectedFile = null;
const uploadArea = document.querySelector('.upload-area');
const fileInput = document.getElementById('fileInput');
const progressBar = document.querySelector('.progress');
const progressFill = document.querySelector('.progress-bar');
const result = document.getElementById('result');

// Drag and drop handlers
uploadArea.addEventListener('dragover', (e) => {
e.preventDefault();
uploadArea.classList.add('dragover');
});

uploadArea.addEventListener('dragleave', () => {
uploadArea.classList.remove('dragover');
});

uploadArea.addEventListener('drop', (e) => {
e.preventDefault();
uploadArea.classList.remove('dragover');
const files = e.dataTransfer.files;
if (files.length > 0) {
selectedFile = files[0];
updateUploadArea();
}
});

fileInput.addEventListener('change', (e) => {
if (e.target.files.length > 0) {
selectedFile = e.target.files[0];
updateUploadArea();
}
});

function updateUploadArea() {
if (selectedFile) {
uploadArea.innerHTML = \`
                    <p>📄 \${selectedFile.name}</p>
                    <p class="file-info">Size: \${formatBytes(selectedFile.size)}</p>
                \`;
}
}

function formatBytes(bytes) {
if (bytes === 0) return '0 Bytes';
const k = 1024;
const sizes = ['Bytes', 'KB', 'MB', 'GB'];
const i = Math.floor(Math.log(bytes) / Math.log(k));
return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

async function uploadFile() {
if (!selectedFile) {
showResult('Please select a file first', 'error');
return;
}

const formData = new FormData();
formData.append('file', selectedFile);

const uploadBtn = document.querySelector('.upload-btn');
uploadBtn.disabled = true;
uploadBtn.textContent = 'Uploading...';
progressBar.style.display = 'block';
result.style.display = 'none';

try {
const xhr = new XMLHttpRequest();

xhr.upload.addEventListener('progress', (e) => {
if (e.lengthComputable) {
const percentComplete = (e.loaded / e.total) * 100;
progressFill.style.width = percentComplete + '%';
}
});

xhr.onload = function() {
if (xhr.status === 200) {
const response = JSON.parse(xhr.responseText);
if (response.success) {
showResult(\`
                                <p>✅ Upload successful!</p>
                                <p>File: \${selectedFile.name}</p>
                                <p>Size: \${formatBytes(response.file_size)}</p>
                                <a href="\${response.download_url}" class="download-link" target="_blank">📥 Download Link</a>
                                <p style="font-size: 12px; margin-top: 10px; word-break: break-all;">
                                    Share this link: <strong>\${response.download_url}</strong>
                                </p>
                            \`, 'success');
} else {
showResult('❌ ' + response.message, 'error');
}
} else {
showResult('❌ Upload failed. Please try again.', 'error');
}
resetUpload();
};

xhr.onerror = function() {
showResult('❌ Network error. Please try again.', 'error');
resetUpload();
};

xhr.open('POST', '/api/upload');
xhr.send(formData);

} catch (error) {
showResult('❌ Upload failed: ' + error.message, 'error');
resetUpload();
}
}

function showResult(message, type) {
result.innerHTML = message;
result.className = 'result ' + type;
result.style.display = 'block';
}

function resetUpload() {
const uploadBtn = document.querySelector('.upload-btn');
uploadBtn.disabled = false;
uploadBtn.textContent = 'Upload File';
progressBar.style.display = 'none';
progressFill.style.width = '0%';
}
</script>
</body>
</html>`

	c.Type("html")
	return c.SendString(html)
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