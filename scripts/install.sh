#!/usr/bin/env bash
set -e

# BabelSub Quick Installation & Environment Bootstrap Script

echo "========================================================"
echo "   BabelSub (巴别字幕) - Environment Setup Script"
echo "========================================================"

# 1. Check / Install ffmpeg
if ! command -v ffmpeg &> /dev/null; then
    echo "[!] ffmpeg not found in PATH."
    if [[ "$OSTYPE" == "darwin"* ]]; then
        echo "[*] Detected macOS. Installing ffmpeg via Homebrew..."
        brew install ffmpeg
    elif [[ -f /etc/debian_version ]]; then
        echo "[*] Detected Debian/Ubuntu. Installing ffmpeg..."
        sudo apt-get update && sudo apt-get install -y ffmpeg
    else
        echo "[-] Please install ffmpeg manually for your OS."
        exit 1
    fi
else
    echo "[+] ffmpeg is installed: $(ffmpeg -version | head -n 1)"
fi

# 2. Check / Setup Python virtualenv for ffsubsync
VENV_DIR="$HOME/.config/babelsub/venv"
if [ ! -f "$VENV_DIR/bin/ffsubsync" ]; then
    echo "[*] Setting up isolated Python environment for ffsubsync at: $VENV_DIR..."
    mkdir -p "$(dirname "$VENV_DIR")"
    python3 -m venv "$VENV_DIR"
    "$VENV_DIR/bin/pip" install --upgrade pip
    "$VENV_DIR/bin/pip" install ffsubsync
    echo "[+] ffsubsync installed successfully into $VENV_DIR!"
else
    echo "[+] ffsubsync already available in $VENV_DIR"
fi

# 3. Compile Go binary
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
echo "[*] Building BabelSub binary..."
cd "$PROJECT_DIR"
mkdir -p bin
go build -o bin/babelsub ./cmd/babelsub

echo ""
echo "========================================================"
echo "   Build & Environment Setup Complete!"
echo "========================================================"
echo "Executable location: $PROJECT_DIR/bin/babelsub"
echo ""
echo "Next Steps:"
echo "1. Run Web GUI:      ./bin/babelsub"
echo "2. Connect Emby:     ./bin/babelsub config --type emby --host <URL> --token <KEY>"
echo "3. Scan library:     ./bin/babelsub scan --lang ja"
echo "4. Fetch subtitles:  ./bin/babelsub fetch --query \"Movie Name\""
echo "========================================================"
