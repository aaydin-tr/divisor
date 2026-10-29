package reload

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

const TriggerConfigFileChange Trigger = "config file change"

// ConfigFileQuietPeriod is how long the config file must stay untouched
// before a burst of changes to it Reloads, once.
const ConfigFileQuietPeriod = time.Second

// ConfigFileWatch watches the directory, not the file: an atomic save or a
// ConfigMap update replaces the file, which a watch on it would not survive.
type ConfigFileWatch struct {
	configPath         string
	resolvedConfigPath string
	quietPeriod        time.Duration
	watcher            *fsnotify.Watcher
	reloadLoopOnce     sync.Once
	reloadingStopped   chan struct{}
}

// WatchConfigFile queues changes until StartReloading, so a change made
// while divisor starts is applied, not lost.
func WatchConfigFile(configPath string, quietPeriod time.Duration) (*ConfigFileWatch, error) {
	absoluteConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("watching config file %s: %w", configPath, err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watching config file %s: %w", configPath, err)
	}
	if err := watcher.Add(filepath.Dir(absoluteConfigPath)); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("watching config file %s: %w", configPath, err)
	}
	return &ConfigFileWatch{
		configPath:         absoluteConfigPath,
		resolvedConfigPath: resolveSymlinks(absoluteConfigPath),
		quietPeriod:        quietPeriod,
		watcher:            watcher,
		reloadingStopped:   make(chan struct{}),
	}, nil
}

func (w *ConfigFileWatch) StartReloading(reloader *Reloader) {
	w.reloadLoopOnce.Do(func() { go w.reloadAfterEachBurstOfChanges(reloader) })
}

// Close stops the watch and waits for a Reload it started.
func (w *ConfigFileWatch) Close() error {
	err := w.watcher.Close()
	w.reloadLoopOnce.Do(func() { close(w.reloadingStopped) })
	<-w.reloadingStopped
	return err
}

func (w *ConfigFileWatch) reloadAfterEachBurstOfChanges(reloader *Reloader) {
	defer close(w.reloadingStopped)
	quietPeriodTimer := time.NewTimer(w.quietPeriod)
	quietPeriodTimer.Stop()
	defer quietPeriodTimer.Stop()
	for {
		select {
		case event, open := <-w.watcher.Events:
			if !open {
				return
			}
			configPathResolvesElsewhere := w.updateResolvedConfigPath()
			if configPathResolvesElsewhere || w.namesConfigFile(event) {
				quietPeriodTimer.Reset(w.quietPeriod)
			}
		case err, open := <-w.watcher.Errors:
			if !open {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				quietPeriodTimer.Reset(w.quietPeriod)
				continue
			}
			zap.S().Errorw("Watching the config file failed", "error", err.Error())
		case <-quietPeriodTimer.C:
			reloader.Reload(TriggerConfigFileChange) //nolint:errcheck
		}
	}
}

// updateResolvedConfigPath reports whether the config path resolves elsewhere
// now: a ConfigMap update only renames the ..data symlink, never the file.
func (w *ConfigFileWatch) updateResolvedConfigPath() (resolvesElsewhere bool) {
	resolvedConfigPath := resolveSymlinks(w.configPath)
	resolvesElsewhere = resolvedConfigPath != w.resolvedConfigPath
	w.resolvedConfigPath = resolvedConfigPath
	return resolvesElsewhere
}

func (w *ConfigFileWatch) namesConfigFile(event fsnotify.Event) bool {
	return filepath.Clean(event.Name) == w.configPath && event.Op != fsnotify.Chmod
}

// resolveSymlinks answers "" for a path that does not resolve (mid-swap or
// removed), which is itself a change from any resolved path.
func resolveSymlinks(path string) string {
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return resolvedPath
}
