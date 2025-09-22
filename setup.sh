#!/bin/bash

# bashupload Setup Script
echo "🚀 Setting up bashupload project structure..."

# Create directories
mkdir -p templates static cmd/cli uploads

# Create template file if it doesn't exist
if [ ! -f "templates/index.html" ]; then
    echo "📝 Creating HTML template..."
    echo "<!-- Copy the HTML template content from the artifacts to this file -->" > templates/index.html
    echo "✅ HTML template placeholder created!"
fi

# Create CSS file if it doesn't exist
if [ ! -f "static/style.css" ]; then
    echo "🎨 Creating CSS styles..."
    echo "/* Copy the CSS content from the artifacts to this file */" > static/static.css
    echo "✅ CSS file placeholder created!"
fi

# Create go.mod if it doesn't exist
if [ ! -f "go.mod" ]; then
    echo "📦 Initializing Go module..."
    go mod init bashupload
    echo "✅ Go module initialized!"
fi

# Create main.go if it doesn't exist
if [ ! -f "main.go" ]; then
    echo "🔧 Creating main.go placeholder..."
    cat > main.go << 'EOF'
package main

import (
    "log"
)

func main() {
    log.Println("bashupload server - copy the main.go content from artifacts")
}
EOF
    echo "✅ main.go placeholder created!"
fi

# Create CLI main.go if it doesn't exist
if [ ! -f "cmd/cli/main.go" ]; then
    echo "💻 Creating CLI main.go placeholder..."
    mkdir -p cmd/cli
    cat > cmd/cli/main.go << 'EOF'
package main

import (
    "log"
)

func main() {
    log.Println("bashupload CLI - copy the CLI main.go content from artifacts")
}
EOF
    echo "✅ CLI main.go placeholder created!"
fi

echo ""
echo "✅ bashupload setup complete!"
echo ""
echo "📋 Next steps:"
echo "   1. Copy the full main.go content from artifacts"
echo "   2. Copy the CLI content to cmd/cli/main.go from artifacts"
echo "   3. Copy the HTML template to templates/index.html"
echo "   4. Copy the CSS styles to static/style.css"
echo "   5. Run: go mod tidy"
echo "   6. Run: make build"
echo "   7. Run: make run"
echo ""
echo "🐳 Or use Docker:"
echo "   docker-compose up -d"
echo ""
echo "🔧 For private instance, set API_KEY environment variable:"
echo "   export API_KEY=your_secret_key"
echo ""
echo "🌐 Access bashupload at: http://localhost:3000"
echo ""
echo "📚 Features:"
echo "   • 50GB file upload limit"
echo "   • 3-day file expiration"
echo "   • Single download policy"
echo "   • Terminal-style web interface"
echo "   • cURL upload support"
echo "   • API key authentication (optional)"
echo "   • Cross-platform CLI tool"
if [ ! -f "templates/index.html" ]; then
    echo "📝 Creating HTML template..."
    cat > templates/index.html << 'EOF'
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>bashupload</title>
    <link rel="stylesheet" href="/static/style.css">
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@300;400;500;700&display=swap" rel="stylesheet">
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
            <span class="command">curl bashupload.com -T your_file.txt{{.AuthHeader}}</span>
        </div>

        {{if .RequiresAuth}}
        <div class="auth-section">
            <div style="margin-bottom: 15px; color: #ff6600;">🔐 API Key Required</div>
            <input type="password" id="apiKeyInput" class="auth-input" placeholder="Enter your API key..." value="">
            <button class="btn" onclick="setApiKey()">💾 SAVE KEY</button>
        </div>
        {{end}}

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
        const requiresAuth = {{.RequiresAuth}};
        let apiKey = '';

        if (requiresAuth) {
            const savedKey = localStorage.getItem('api_key');
            if (savedKey) {
                apiKey = savedKey;
            }
        }

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
                        progressFill.style.width = percentComplete + '%';
                    }
                });

                xhr.onload = function() {
                    if (xhr.status === 200) {
                        const response = JSON.parse(xhr.responseText);
                        if (response.success) {
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
            progressFill.style.width = '0%';
        }

        function showCurlExample() {
            const authHeader = requiresAuth ? ' -H "X-API-Key: YOUR_API_KEY"' : '';
            alert(\`cURL Examples:

Upload: curl\${authHeader} \${location.origin} -T filename.ext

Or use form: curl\${authHeader} -F "file=@filename.ext" \${location.origin}/api/upload\`);
        }

        if (requiresAuth && !apiKey) {
            setTimeout(() => {
                showResult('🔐 This instance requires an API key', 'error');
            }, 1000);
        }
    </script>
</body>
</html>
EOF
    echo "✅ HTML template created!"
fi

# Create CSS file if it doesn't exist
if [ ! -f "static/style.css" ]; then
    echo "🎨 Creating CSS styles..."
    echo "/* CSS content would be here - see the artifacts for the full CSS */" > static/static.css
    echo "✅ CSS file created! (You'll need to copy the full CSS content from the artifacts)"
fi

echo ""
echo "✅ Setup complete!"
echo ""
echo "📋 Next steps:"
echo "   1. Copy the full CSS content to static/style.css"
echo "   2. Run: go mod tidy"
echo "   3. Run: make build"
echo "   4. Run: make run"
echo ""
echo "🐳 Or use Docker:"
echo "   docker-compose up -d"
echo ""
echo "🔧 For private instance, set API_KEY environment variable:"
echo "   export API_KEY=your_secret_key"