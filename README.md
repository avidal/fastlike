![Fastlike logo](.media/logo.png)

# Fastlike

Fastlike is a local development server for Fastly Compute (previously known as Compute@Edge) applications. It allows you to run and test your Fastly Compute WebAssembly programs locally without deploying to Fastly, making development faster and more efficient.

## What is Fastlike?

Fastlike is a complete Go implementation of the Fastly Compute XQD ABI that runs locally using WebAssembly. It emulates Fastly's runtime environment, allowing you to:

- Test Fastly Compute programs locally before deployment
- Debug issues without the deployment cycle
- Develop with hot-reloading capabilities
- Simulate backend services and configurations

## Features

- Full ABI Compatibility: Implements 100% of the Fastly Compute XQD ABI
- Local Development: Run Fastly Compute programs without deploying
- Multiple Backend Support: Configure multiple named backends
- Hot Reloading: Automatically reload WASM modules on file changes
- Dictionaries & Config Stores: Support for key-value lookups
- Caching: Full HTTP caching with surrogate keys
- ACL & Rate Limiting: Access control and rate limiting support
- Secure Secret Handling: Secure credential management
- KV Store Support: Object storage with async operations
- Image Optimization Hooks: Transform images on-the-fly

## Installation

### Prerequisites

- Go 1.23 or later

### Install via Go

```bash
go install fastlike.dev/cmd/fastlike@latest
```

### Build from Source

```bash
# Clone the repository (if you have the source code)
# If you just want to install, use: go install fastlike.dev/cmd/fastlike@latest
git clone https://github.com/fastlike/fastlike.git
cd fastlike

# Build and install
make build
# Binary will be available at ./bin/fastlike

# Or install to GOPATH/bin
make install
```

## Getting Started

### 1. Get a Fastly Compute WASM Program

You'll need a Fastly Compute compatible WebAssembly program. The easiest way is using [Fastly CLI](https://github.com/fastly/cli):

```bash
# Create a new project
fastly compute init my-project
cd my-project

# Build the WASM binary
fastly compute build
# WASM file will be at bin/main.wasm
```

### 2. Run with a Backend

Start Fastlike with your WASM program and a backend service:

```bash
# Run with a local backend on port 8000
fastlike -wasm bin/main.wasm -backend localhost:8000

# Or with a named backend
fastlike -wasm bin/main.wasm -backend api=localhost:8000 -bind localhost:5000
```

### 3. Test Your Application

Once running, make requests to your local Fastlike server:

```bash
curl http://localhost:5000/
```

## Configuration Options

### Basic Options

```bash
# Specify custom bind address
fastlike -wasm my-program.wasm -backend localhost:8000 -bind localhost:8080

# Enable verbose logging (0, 1, or 2 levels)
fastlike -wasm my-program.wasm -backend localhost:8000 -v 2

# Enable hot reloading on SIGHUP
fastlike -wasm my-program.wasm -backend localhost:8000 -reload
```

### Multiple Backends

Configure multiple named backends for complex routing:

```bash
fastlike -wasm my-program.wasm \
  -backend api=localhost:8000 \
  -backend static=localhost:8080 \
  -backend images=localhost:9000
```

### Dictionaries and Config Stores

Use JSON files for key-value lookups:

```bash
# Create a config file
echo '{"api_key": "secret123", "version": "1.0.0"}' > config.json

# Use as a dictionary
fastlike -wasm my-program.wasm -backend localhost:8000 -dictionary config=config.json

# Or use as a config store
fastlike -wasm my-program.wasm -backend localhost:8000 -config-store config=config.json
```

The file should contain a JSON object with string keys and values:

```json
{
  "key1": "value1",
  "key2": "value2",
  "api_endpoint": "https://api.example.com"
}
```

### KV Stores

Load KV store data from JSON files or create empty stores:

```bash
# Create a KV store with initial data
fastlike -wasm my-program.wasm -backend localhost:8000 -kv mystore=data.json

# Create an empty KV store
fastlike -wasm my-program.wasm -backend localhost:8000 -kv mystore
```

KV store JSON supports simple string values or objects with body and metadata:

```json
{
  "simple_key": "simple value",
  "key_with_metadata": {
    "body": "the value content",
    "metadata": "optional metadata"
  }
}
```

### Secret Stores

Store sensitive credentials in JSON files:

```bash
fastlike -wasm my-program.wasm -backend localhost:8000 -secret-store secrets=secrets.json
```

Secret store JSON format (string key-value pairs):

```json
{
  "api_key": "sk-live-xxxxx",
  "database_password": "supersecret"
}
```

### Access Control Lists (ACLs)

Configure IP-based access control:

```bash
fastlike -wasm my-program.wasm -backend localhost:8000 -acl blocklist=acl.json
```

ACL JSON format with CIDR prefixes and actions:

```json
{
  "entries": [
    {"prefix": "192.168.0.0/16", "action": "ALLOW"},
    {"prefix": "10.0.0.0/8", "action": "ALLOW"},
    {"prefix": "172.16.0.0/12", "action": "BLOCK"}
  ]
}
```

### Geolocation

Provide custom IP-to-location mappings:

```bash
fastlike -wasm my-program.wasm -backend localhost:8000 -geo geo.json
```

Geo JSON maps IP addresses or CIDRs to location data:

```json
{
  "192.168.1.0/24": {
    "city": "San Francisco",
    "country_code": "US",
    "region": "CA",
    "latitude": 37.7749,
    "longitude": -122.4194
  },
  "10.0.0.1": {
    "city": "New York",
    "country_code": "US"
  }
}
```

### Log Endpoints

Configure named log endpoints for your application:

```bash
# Log to stdout
fastlike -wasm my-program.wasm -backend localhost:8000 -logger access

# Log to a file
fastlike -wasm my-program.wasm -backend localhost:8000 -logger access=/var/log/access.log
```

### Compliance Region

Set the compliance region for data locality requirements:

```bash
fastlike -wasm my-program.wasm -backend localhost:8000 -compliance-region us-eu
```

### Hot Reloading

Enable hot reloading to automatically reload your WASM module when you make changes:

```bash
# Start with reload enabled
fastlike -wasm my-program.wasm -backend localhost:8000 -reload

# In another terminal, send SIGHUP to reload
kill -SIGHUP $(pgrep fastlike)
# Or if you know the process ID: kill -SIGHUP <pid>
```

## Testing and Development

### Make Commands

The project includes a Makefile with helpful commands:

```bash
# Build the binary
make build

# Run all tests
make test

# Build test WASM programs (for development)
make build-test-wasm

# Run with Make (requires WASM and BACKEND variables)
make run WASM=my-program.wasm BACKEND=localhost:8000

# Run with arguments
make run WASM=my-program.wasm BACKEND=localhost:8000 ARGS='-v 2'
```

### Verbose Mode per Request

For debugging specific requests, add the `fastlike-verbose` header:

```bash
curl -H "fastlike-verbose: 1" http://localhost:5000/
```

This enables verbose logging for just that request.

## Example Usage

### Simple Setup

```bash
# Start a simple backend server
python3 -m http.server 8000 &

# Run Fastlike with your WASM program
fastlike -wasm my-program.wasm -backend localhost:8000 -bind localhost:5000

# Test it
curl http://localhost:5000/
```

### Complex Multi-Backend Setup

```bash
# Start multiple backend services
python3 -m http.server 8000 &  # Static files
node app.js 8080 &             # API server
npx serve public/ 9000 &       # Another service

# Run Fastlike with multiple backends and stores
fastlike -wasm my-program.wasm \
  -backend static=localhost:8000 \
  -backend api=localhost:8080 \
  -backend images=localhost:9000 \
  -config-store config=config.json \
  -secret-store secrets=secrets.json \
  -kv cache=cache.json \
  -acl blocklist=acl.json \
  -geo geo.json \
  -logger access \
  -bind localhost:5000

# Now your WASM program can use all configured backends and stores
```

### Development Workflow with Hot Reloading

```bash
# Terminal 1: Start Fastlike with hot reload
fastlike -wasm my-program.wasm -backend localhost:8000 -reload

# Terminal 2: Build and reload when changes occur
while inotifywait -e modify my-project/src/*; do
  cd my-project && fastly compute build
  kill -SIGHUP $(pgrep fastlike)
done

# Terminal 3: Test continuously
watch -n 1 curl -s http://localhost:5000/
```

## Advanced Features

### Custom Logging

Enable verbose logging to see all ABI calls:

```bash
# Persistent verbose logging
fastlike -wasm my-program.wasm -backend localhost:8000 -v 2

# Per-request verbose logging
curl -H "fastlike-verbose: 1" http://localhost:5000/
```

### Backend Configuration

Support for named backend configurations. Complex backend configurations with timeouts, SSL, and more are available through the Go API but not through the CLI.

### Cache Support

Full HTTP caching support with request collapsing and surrogate key management - all working locally.

### Request Loops Prevention

Fastlike adds "Fastlike" to the `cdn-loop` header to prevent infinite request loops. If a loop is detected, it returns a 508 Loop Detected error.
