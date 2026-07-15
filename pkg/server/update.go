package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/anh-chu/termyard/pkg/commands/update"
)

var updateState struct {
	mu     sync.RWMutex
	status update.Status
}

func handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	updateState.mu.RLock()
	status := updateState.status
	updateState.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func handleUpdateApply(w http.ResponseWriter, r *http.Request, opts *Options) {
	newVersion, binPath, err := update.Apply(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}

	managed := update.ServiceManaged()
	if !managed {
		updateState.mu.Lock()
		status := updateState.status
		status.LatestVersion = newVersion
		status.UpdateAvailable = false
		status.PendingRestart = true
		updateState.status = status
		updateState.mu.Unlock()
		if opts != nil && opts.Hub != nil {
			opts.Hub.BroadcastJSON(map[string]any{
				"type":             "update-status",
				"current_version":  status.CurrentVersion,
				"latest_version":   status.LatestVersion,
				"update_available": status.UpdateAvailable,
				"pending_restart":  status.PendingRestart,
				"channel":          status.Channel,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "new_version": newVersion, "restarting": managed})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if managed {
		go func() {
			if err := update.RestartManaged(binPath); err != nil {
				logrus.WithError(err).Warn("update restart failed")
			}
		}()
	}
}

func handleUpdateCheck(w http.ResponseWriter, r *http.Request, opts *Options) {
	status, err := update.CheckLatest(r.Context())
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	setUpdateStatus(status, opts)
	_ = json.NewEncoder(w).Encode(status)
}

func setUpdateStatus(status update.Status, opts *Options) {
	updateState.mu.Lock()
	prev := updateState.status
	updateState.status = status
	updateState.mu.Unlock()
	if opts != nil && opts.Hub != nil && prev != status {
		opts.Hub.BroadcastJSON(map[string]any{
			"type":             "update-status",
			"current_version":  status.CurrentVersion,
			"latest_version":   status.LatestVersion,
			"update_available": status.UpdateAvailable,
			"pending_restart":  status.PendingRestart,
			"channel":          status.Channel,
		})
	}
}

func runUpdateChecker(opts *Options) {
	if opts == nil {
		return
	}
	check := func() {
		updateState.mu.RLock()
		pending := updateState.status.PendingRestart
		updateState.mu.RUnlock()
		if pending {
			return
		}
		status, err := update.CheckLatest(context.Background())
		if err != nil {
			logrus.WithError(err).Warn("update check failed")
			return
		}
		setUpdateStatus(status, opts)
	}

	check()
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		check()
	}
}
