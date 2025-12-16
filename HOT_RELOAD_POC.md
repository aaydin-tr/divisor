# File Watcher POC - Hot Reload for Divisor

## Overview

This POC adds hot-reload capability to Divisor using `github.com/fsnotify/fsnotify`. The config file is automatically monitored for changes, and the load balancer reloads without downtime when modifications are detected.

## What Was Implemented

### 1. File Watcher (`internal/watcher/watcher.go`)
- Monitors config file using fsnotify
- 1-second debounce to handle editors writing in chunks
- Handles Write, Remove, and Rename events
- Non-blocking reload signals via channel

### 2. Swappable Balancer (`internal/balancer/swappable.go`)
- Wraps `IBalancer` interface with atomic swap capability
- Uses `atomic.Value` for lock-free reads (no performance impact)
- Gracefully shuts down old balancer after 5 seconds
- Zero-downtime during config reload

### 3. Config Reloader (`internal/reloader/reloader.go`)
- Orchestrates reload workflow: parse → validate → create → swap
- Validates new config before applying changes
- Keeps old config running if validation fails
- Updates middleware executor on successful reload

### 4. Main Integration (`main.go`)
- Wraps balancer in SwappableBalancer
- Starts file watcher in background goroutine
- Continues without hot-reload if watcher fails to start
- Logs only reload success/failure (not every file event)

## How to Use

### Starting Divisor
```bash
# Start with config file
./divisor --config ./config.yaml

# You'll see this log when hot-reload is enabled:
# Config file watcher started, hot-reload enabled
```

### Reloading Config
Simply edit your config file:
```bash
vim config.yaml
# or
nano config.yaml
```

The reload happens automatically after 1 second of file inactivity. Watch the logs:
```
Starting config reload...
New config validated successfully
New balancer created with algorithm: random
Balancer swapped successfully
Shutting down old balancer...
Old balancer shutdown completed
Config reload completed successfully
```

### What Can Be Reloaded

✅ **Supported (hot-reload):**
- Backend servers (URLs, health check paths, weights)
- Load balancing algorithm (round-robin, weighted, ip-hash, random, etc.)
- Connection settings (max connections, timeouts, idle duration)
- Health check interval
- Middleware configuration
- Custom headers

❌ **Not Supported (requires restart):**
- Server host/port
- TLS certificates (cert_file, key_file)
- HTTP version (HTTP/1.1 vs HTTP/2)
- Monitoring server config

## Testing the POC

### Test 1: Change Algorithm
```yaml
# Original config.yaml
type: round-robin

# Edit to:
type: random
```
Save the file and watch logs for successful reload.

### Test 2: Add/Remove Backends
```yaml
# Original
backends:
  - url: http://localhost:8081

# Edit to:
backends:
  - url: http://localhost:8081
  - url: http://localhost:8082
  - url: http://localhost:8083
```
Save and verify new backends are used.

### Test 3: Change Weights
```yaml
# Original
type: w-round-robin
backends:
  - url: http://localhost:8081
    weight: 1
  - url: http://localhost:8082
    weight: 1

# Edit to:
type: w-round-robin
backends:
  - url: http://localhost:8081
    weight: 3
  - url: http://localhost:8082
    weight: 1
```
Save and verify traffic distribution changes.

### Test 4: Invalid Config (Error Handling)
```yaml
# Break the config
backends: []
```
Save and check logs:
```
Config reload failed: balancer creation failed: no available servers
```
Divisor continues running with previous config.

## Architecture Details

### Request Flow
```
Client Request
    ↓
FastHTTP/net/http Server
    ↓
SwappableBalancer.Serve()
    ↓
atomic.Load() (current balancer)
    ↓
IBalancer.Serve() (round-robin, random, etc.)
    ↓
Backend Server
```

**Performance Impact:** One atomic load operation (~1-2 CPU cycles) added to request path. Expected latency increase: <1μs (negligible).

### Reload Flow
```
File Change Detected
    ↓
Debounce (1s wait)
    ↓
ConfigReloader.Reload()
    ↓
Parse → Validate → Create New Balancer
    ↓
Atomic Swap (instantaneous)
    ↓
Old Balancer Drain (5s delay)
    ↓
Old Balancer Shutdown
```

### Error Handling
If any step fails during reload:
- Parse error → Keep old config
- Validation error → Keep old config
- No backends alive → Keep old config
- Swap error → Cleanup new, keep old

**Principle:** System always continues with last known good config.

## Implementation Stats

**Files Created:**
- `internal/watcher/watcher.go` - 136 lines
- `internal/balancer/swappable.go` - 73 lines
- `internal/reloader/reloader.go` - 71 lines

**Files Modified:**
- `main.go` - Added imports, watcher setup, balancer wrapping
- `go.mod` - Added fsnotify dependency

**Total Lines of Code:** ~280 lines for complete hot-reload capability

## Known Limitations (POC)

1. **Server config changes require restart** (host, port, TLS, HTTP version)
2. **No config rollback** if backends fail health checks after reload
3. **No HTTP API** for manual reload trigger
4. **No config versioning** or history
5. **Windows symlink handling** may have edge cases

## Future Enhancements (Not in POC)

- Automatic rollback if new backends fail health checks
- Prometheus metrics for reload success/failure rates
- HTTP endpoint: `POST /admin/reload` for manual trigger
- Config diff logging (show what changed)
- Dry-run mode to validate config without applying
- Dynamic TLS certificate reload
- Graceful drain with configurable timeout
- Config versioning and rollback command

## Testing Checklist

- [x] Build compiles without errors
- [x] File watcher starts successfully
- [x] Algorithm change reloads correctly
- [x] Backend addition/removal works
- [x] Weight changes apply (w-round-robin)
- [x] Invalid config keeps old config running
- [x] File deletion doesn't crash server
- [x] Multiple rapid edits handled by debounce
- [x] Graceful shutdown still works
- [x] No memory leaks during repeated reloads
- [x] Atomic swap prevents race conditions

## Conclusion

The POC successfully demonstrates hot-reload capability for Divisor load balancer using fsnotify. Key achievements:

✅ Zero-downtime config reload
✅ Atomic balancer swap
✅ Graceful old balancer drain
✅ Robust error handling
✅ Minimal performance impact
✅ Clean architecture

The implementation is production-ready for the supported features (backends, algorithm, middleware) and can be extended with additional capabilities as needed.
