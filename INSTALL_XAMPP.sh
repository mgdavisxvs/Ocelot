#!/bin/bash
# Ocelot Tracker - XAMPP Installer for macOS
# This script installs Ocelot to your XAMPP htdocs directory

set -e

echo "🐆 Ocelot Tracker - XAMPP Installer"
echo "===================================="
echo ""

# Check if running on macOS
if [[ "$OSTYPE" != "darwin"* ]]; then
    echo "❌ This installer is for macOS only"
    exit 1
fi

# Check if XAMPP is installed
XAMPP_DIR="/Applications/XAMPP/xamppfiles/htdocs"
if [ ! -d "$XAMPP_DIR" ]; then
    echo "❌ XAMPP not found at $XAMPP_DIR"
    echo ""
    echo "Please install XAMPP first:"
    echo "  https://www.apachefriends.org/download.html"
    exit 1
fi

echo "✓ Found XAMPP installation"

# Check if package exists
if [ ! -f "ocelot-xampp-macos.tar.gz" ]; then
    echo "❌ Package not found: ocelot-xampp-macos.tar.gz"
    echo ""
    echo "Please download the package first"
    exit 1
fi

echo "✓ Found package: ocelot-xampp-macos.tar.gz"
echo ""

# Extract
echo "📦 Extracting package..."
tar -xzf ocelot-xampp-macos.tar.gz

# Check if ocelot directory already exists
INSTALL_DIR="$XAMPP_DIR/ocelot"
if [ -d "$INSTALL_DIR" ]; then
    echo ""
    echo "⚠️  Directory already exists: $INSTALL_DIR"
    read -p "Overwrite? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Installation cancelled"
        exit 1
    fi
    echo "Removing old installation..."
    sudo rm -rf "$INSTALL_DIR"
fi

# Copy to XAMPP
echo ""
echo "📂 Installing to $INSTALL_DIR..."
sudo cp -r ocelot-xampp "$INSTALL_DIR"

# Set ownership
echo "🔐 Setting permissions..."
sudo chown -R $(whoami):staff "$INSTALL_DIR"
chmod +x "$INSTALL_DIR/START.command"
chmod +x "$INSTALL_DIR/ocelot-tracker-"*

# Create data directory
mkdir -p "$INSTALL_DIR/data/db"

# Clean up
rm -rf ocelot-xampp

echo ""
echo "✅ Installation complete!"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "📍 Installed to: $INSTALL_DIR"
echo ""
echo "Next steps:"
echo ""
echo "1️⃣  Start XAMPP Apache:"
echo "    open /Applications/XAMPP/manager-osx.app"
echo ""
echo "2️⃣  Start Tracker (choose one):"
echo "    • Double-click: $INSTALL_DIR/START.command"
echo "    • Terminal: cd $INSTALL_DIR && ./START.command"
echo ""
echo "3️⃣  Access Admin Panel:"
echo "    http://localhost/ocelot/admin/login.php"
echo "    Username: admin"
echo "    Password: changeme"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "📖 Documentation:"
echo "    $INSTALL_DIR/XAMPP_SETUP.md"
echo ""
