#!/bin/bash

# Test script for the new metadata API
echo "Testing the new metadata API..."

# Start the server in background
echo "Starting server..."
./ytmp3-api &
SERVER_PID=$!

# Wait for server to start
sleep 3

# Test the metadata endpoint
echo "Testing /metadata endpoint..."
curl -X POST http://localhost:8080/metadata \
  -H "Content-Type: application/json" \
  -d '{"url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ"}' \
  | jq '.'

echo -e "\n\nTesting status endpoint..."
# Get the session_id from the response and test status
SESSION_ID=$(curl -s -X POST http://localhost:8080/metadata \
  -H "Content-Type: application/json" \
  -d '{"url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ"}' \
  | jq -r '.session_id')

echo "Session ID: $SESSION_ID"

# Wait a bit for download to start
sleep 5

# Check status
curl -s http://localhost:8080/status/$SESSION_ID | jq '.'

# Clean up
echo "Stopping server..."
kill $SERVER_PID