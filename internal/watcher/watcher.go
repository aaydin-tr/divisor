package watcher

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// ConfigWatcher watches config file for changes and triggers reloads
type ConfigWatcher struct {
	configPath     string
	watcher        *fsnotify.Watcher
	reloadChan     chan string
	stopChan       chan struct{}
	mu             sync.Mutex
	debounceTimer  *time.Timer
	debouncePeriod time.Duration
}

// NewConfigWatcher creates a new config file watcher
func NewConfigWatcher(configPath string) (*ConfigWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	absPath, err := filepath.Abs(configPath)
	if err != nil {
		watcher.Close()
		return nil, err
	}

	if err := watcher.Add(absPath); err != nil {
		watcher.Close()
		return nil, err
	}

	return &ConfigWatcher{
		configPath:     absPath,
		watcher:        watcher,
		reloadChan:     make(chan string, 1),
		stopChan:       make(chan struct{}),
		debouncePeriod: 1 * time.Second, // 1s debounce for safer multi-write handling
	}, nil
}

// Start begins watching the config file for changes
func (cw *ConfigWatcher) Start(ctx context.Context) error {
	go func() {
		for {
			select {
			case event, ok := <-cw.watcher.Events:
				if !ok {
					return
				}

				cw.handleEvent(event)

			case err, ok := <-cw.watcher.Errors:
				if !ok {
					return
				}
				zap.S().Errorf("File watcher error: %s", err)

			case <-ctx.Done():
				cw.Stop()
				return

			case <-cw.stopChan:
				return
			}
		}
	}()

	return nil
}

// handleEvent processes file system events with debouncing
func (cw *ConfigWatcher) handleEvent(event fsnotify.Event) {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	switch {
	case event.Has(fsnotify.Write):
		// Reset debounce timer on write
		if cw.debounceTimer != nil {
			cw.debounceTimer.Stop()
		}

		cw.debounceTimer = time.AfterFunc(cw.debouncePeriod, func() {
			cw.triggerReload()
		})

	case event.Has(fsnotify.Remove):
		zap.S().Warnf("Config file removed: %s (continuing with current config)", event.Name)
		cw.triggerReload()

	case event.Has(fsnotify.Rename):
		zap.S().Warnf("Config file renamed: %s (attempting to re-add watch)", event.Name)

		// Try to re-add watch after a short delay (file might be recreated)
		time.AfterFunc(100*time.Millisecond, func() {
			if err := cw.watcher.Add(cw.configPath); err != nil {
				zap.S().Errorf("Failed to re-add watch after rename: %s", err)
			}
		})

		cw.triggerReload()
	}
}

// triggerReload sends a reload signal (non-blocking)
func (cw *ConfigWatcher) triggerReload() {
	select {
	case cw.reloadChan <- cw.configPath:
		// Signal sent
	default:
		// Channel full, reload already pending
	}
}

// ReloadChan returns the channel for reload signals
func (cw *ConfigWatcher) ReloadChan() <-chan string {
	return cw.reloadChan
}

// Stop stops the file watcher
func (cw *ConfigWatcher) Stop() error {
	close(cw.stopChan)

	if cw.debounceTimer != nil {
		cw.debounceTimer.Stop()
	}

	return cw.watcher.Close()
}
