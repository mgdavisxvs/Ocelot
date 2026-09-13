#!/bin/bash
# Ocelot Tracker - Complete Launch Script for macOS XAMPP
# Double-click this file to launch everything

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo "🐆 Ocelot Tracker - Complete Launch"
echo "===================================="
echo ""

# Set XAMPP path
OCELOT_DIR="/Applications/XAMPP/xamppfiles/htdocs/ocelot"

# Check if directory exists
if [ ! -d "$OCELOT_DIR" ]; then
    echo -e "${RED}❌ Ocelot not found at $OCELOT_DIR${NC}"
    echo ""
    echo "Please install Ocelot first using INSTALL_XAMPP.sh"
    echo ""
    read -p "Press Enter to exit..."
    exit 1
fi

echo -e "${GREEN}✓ Found Ocelot installation${NC}"
cd "$OCELOT_DIR"

# Detect architecture
ARCH=$(uname -m)
if [ "$ARCH" = "arm64" ]; then
    BINARY="./ocelot-tracker-arm64"
    echo -e "${GREEN}✓ Detected: Apple Silicon (M1/M2/M3/M4)${NC}"
elif [ "$ARCH" = "x86_64" ]; then
    BINARY="./ocelot-tracker-intel"
    echo -e "${GREEN}✓ Detected: Intel Mac${NC}"
else
    echo -e "${RED}❌ Unknown architecture: $ARCH${NC}"
    read -p "Press Enter to exit..."
    exit 1
fi

# Check if binary exists
if [ ! -f "$BINARY" ]; then
    echo -e "${RED}❌ Tracker binary not found: $BINARY${NC}"
    echo ""
    echo "Please run UPDATE.sh to get the latest binaries"
    read -p "Press Enter to exit..."
    exit 1
fi

# Make binary executable
chmod +x "$BINARY" 2>/dev/null

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Check if tracker is already running
if lsof -Pi :34000 -sTCP:LISTEN -t >/dev/null 2>&1; then
    echo -e "${YELLOW}⚠️  Tracker already running on port 34000${NC}"
    echo ""
    read -p "Stop and restart? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        echo "Stopping tracker..."
        lsof -ti:34000 | xargs kill -9 2>/dev/null
        sleep 1
    else
        echo "Keeping existing tracker running"
        TRACKER_RUNNING=1
    fi
fi

# Start tracker if not already running
if [ -z "$TRACKER_RUNNING" ]; then
    echo -e "${BLUE}🚀 Starting Tracker...${NC}"
    echo ""

    # Create data directory
    mkdir -p data/db

    # Start tracker in background
    nohup $BINARY > tracker.log 2>&1 &
    TRACKER_PID=$!

    # Wait for tracker to start
    sleep 2

    # Check if tracker started successfully
    if lsof -Pi :34000 -sTCP:LISTEN -t >/dev/null 2>&1; then
        echo -e "${GREEN}✓ Tracker started successfully (PID: $TRACKER_PID)${NC}"
        echo -e "${GREEN}✓ Listening on port 34000${NC}"
    else
        echo -e "${RED}❌ Failed to start tracker${NC}"
        echo "Check tracker.log for errors"
        read -p "Press Enter to exit..."
        exit 1
    fi
fi

echo ""

# Check XAMPP Apache
echo -e "${BLUE}Checking XAMPP Apache...${NC}"
if curl -s http://localhost/ocelot/admin/login.php > /dev/null 2>&1; then
    echo -e "${GREEN}✓ Apache is running${NC}"
    echo -e "${GREEN}✓ Admin panel accessible${NC}"
else
    echo -e "${YELLOW}⚠️  Apache may not be running${NC}"
    echo ""
    echo "Starting XAMPP Control Panel..."
    open /Applications/XAMPP/manager-osx.app
    echo ""
    echo "Please click 'Start' next to Apache in the XAMPP panel"
    echo ""
    read -p "Press Enter after starting Apache..."
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo -e "${GREEN}✅ Ocelot Tracker is Ready!${NC}"
echo ""
echo "📊 Access Points:"
echo ""
echo "   Admin Panel:"
echo -e "   ${BLUE}http://localhost/ocelot/admin/login.php${NC}"
echo ""
echo "   Login:"
echo "   • Username: admin"
echo "   • Password: changeme"
echo ""
echo "   Tracker:"
echo -e "   ${BLUE}http://localhost:34000${NC}"
echo ""
echo "📝 Logs:"
echo "   • Tracker: $OCELOT_DIR/tracker.log"
echo ""
echo "🛑 To Stop:"
echo "   lsof -ti:34000 | xargs kill -9"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Open admin panel in default browser
echo "Opening admin panel in browser..."
sleep 1
open "http://localhost/ocelot/admin/login.php"

echo ""
echo -e "${GREEN}✓ Admin panel opened in browser${NC}"
echo ""
echo "Press Ctrl+C to stop watching logs, or close this window."
echo ""
echo "Tracker logs:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Follow tracker logs
tail -f tracker.log
