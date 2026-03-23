package engine

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/fgrehm/crib/internal/config"
	"github.com/fgrehm/crib/internal/driver"
	"github.com/fgrehm/crib/internal/workspace"
)

// suspendDriver wraps mockDriver to return a specific container from
// FindContainer and track StopContainer calls.
type suspendDriver struct {
	mockDriver
	container   *driver.ContainerDetails
	stopCalled  bool
	stopWSID    string
	stopCtrID   string
}

func (d *suspendDriver) FindContainer(_ context.Context, _ string) (*driver.ContainerDetails, error) {
	return d.container, nil
}

func (d *suspendDriver) StopContainer(_ context.Context, workspaceID, containerID string) error {
	d.stopCalled = true
	d.stopWSID = workspaceID
	d.stopCtrID = containerID
	return nil
}

func TestSuspend_CallsStopContainer(t *testing.T) {
	store := workspace.NewStoreAt(t.TempDir())

	ws := &workspace.Workspace{
		ID:               "test-suspend",
		Source:           t.TempDir(),
		DevContainerPath: ".devcontainer/devcontainer.json",
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	drv := &suspendDriver{
		container: &driver.ContainerDetails{
			ID:    "ctr-123",
			State: driver.ContainerState{Status: "running"},
		},
	}

	e := &Engine{
		driver: drv,
		store:  store,
		logger: slog.Default(),
		stdout: io.Discard,
		stderr: io.Discard,
	}

	if err := e.Suspend(context.Background(), ws); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	if !drv.stopCalled {
		t.Fatal("expected StopContainer to be called")
	}
	if drv.stopWSID != ws.ID {
		t.Errorf("expected workspace ID %q, got %q", ws.ID, drv.stopWSID)
	}
	if drv.stopCtrID != "ctr-123" {
		t.Errorf("expected container ID %q, got %q", "ctr-123", drv.stopCtrID)
	}
}

func TestSuspend_DoesNotClearHookMarkers(t *testing.T) {
	store := workspace.NewStoreAt(t.TempDir())

	ws := &workspace.Workspace{
		ID:               "test-suspend-markers",
		Source:           t.TempDir(),
		DevContainerPath: ".devcontainer/devcontainer.json",
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Create hook markers.
	hooks := []string{"onCreateCommand", "updateContentCommand", "postCreateCommand"}
	for _, hook := range hooks {
		if err := store.MarkHookDone(ws.ID, hook); err != nil {
			t.Fatal(err)
		}
	}

	drv := &suspendDriver{
		container: &driver.ContainerDetails{
			ID:    "ctr-123",
			State: driver.ContainerState{Status: "running"},
		},
	}

	e := &Engine{
		driver: drv,
		store:  store,
		logger: slog.Default(),
		stdout: io.Discard,
		stderr: io.Discard,
	}

	if err := e.Suspend(context.Background(), ws); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	// Verify markers are preserved (not cleared).
	for _, hook := range hooks {
		if !store.IsHookDone(ws.ID, hook) {
			t.Errorf("expected marker for %s to be preserved after Suspend", hook)
		}
	}
}

func TestSuspend_AlreadyStopped(t *testing.T) {
	store := workspace.NewStoreAt(t.TempDir())

	ws := &workspace.Workspace{
		ID:               "test-suspend-stopped",
		Source:           t.TempDir(),
		DevContainerPath: ".devcontainer/devcontainer.json",
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	drv := &suspendDriver{
		container: &driver.ContainerDetails{
			ID:    "ctr-123",
			State: driver.ContainerState{Status: "exited"},
		},
	}

	e := &Engine{
		driver: drv,
		store:  store,
		logger: slog.Default(),
		stdout: io.Discard,
		stderr: io.Discard,
	}

	err := e.Suspend(context.Background(), ws)
	if err == nil {
		t.Fatal("expected error for already stopped container")
	}
	if !strings.Contains(err.Error(), "already stopped") {
		t.Errorf("expected 'already stopped' in error, got: %v", err)
	}
}

func TestSuspend_NoContainer(t *testing.T) {
	store := workspace.NewStoreAt(t.TempDir())

	ws := &workspace.Workspace{
		ID:               "test-suspend-none",
		Source:           t.TempDir(),
		DevContainerPath: ".devcontainer/devcontainer.json",
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	// Default mockDriver.FindContainer returns nil.
	e := &Engine{
		driver: &mockDriver{},
		store:  store,
		logger: slog.Default(),
		stdout: io.Discard,
		stderr: io.Discard,
	}

	err := e.Suspend(context.Background(), ws)
	if err == nil {
		t.Fatal("expected error for nonexistent container")
	}
	if !strings.Contains(err.Error(), "no container found") {
		t.Errorf("expected 'no container found' in error, got: %v", err)
	}
}

func TestRestartSimple_WorksOnSuspendedContainer(t *testing.T) {
	store := workspace.NewStoreAt(t.TempDir())
	ws := &workspace.Workspace{ID: "ws-suspended", Source: "/home/user/project"}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	cfg := &config.DevContainerConfig{}
	cfg.Image = "ubuntu:22.04"
	cfg.RemoteUser = "vscode"

	mergedCfg, _ := json.Marshal(cfg)
	initialResult := &workspace.Result{
		ContainerID:  "c-1",
		ImageName:    "ubuntu:22.04",
		RemoteUser:   "vscode",
		MergedConfig: mergedCfg,
	}
	if err := store.SaveResult(ws.ID, initialResult); err != nil {
		t.Fatal(err)
	}

	// Create hook markers (as if container was previously set up and then suspended).
	hooks := []string{"onCreateCommand", "updateContentCommand", "postCreateCommand"}
	for _, hook := range hooks {
		if err := store.MarkHookDone(ws.ID, hook); err != nil {
			t.Fatal(err)
		}
	}

	// Container is stopped (suspended state).
	drv := &fixedFindContainerDriver{
		container: &driver.ContainerDetails{
			ID:    "c-1",
			State: driver.ContainerState{Status: "exited"},
		},
	}

	eng := &Engine{
		driver:      drv,
		store:       store,
		runtimeName: "docker",
		logger:      slog.Default(),
		stdout:      io.Discard,
		stderr:      io.Discard,
		progress:    func(string) {},
	}

	b := eng.newBackend(ws, cfg, "/workspaces/project")
	result, err := eng.restartSimple(context.Background(), ws, cfg, "/workspaces/project", b, initialResult)
	if err != nil {
		t.Fatalf("restartSimple on suspended container: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Hook markers should still be preserved (restartSimple does not clear them).
	for _, hook := range hooks {
		if !store.IsHookDone(ws.ID, hook) {
			t.Errorf("expected marker for %s to be preserved after restart of suspended container", hook)
		}
	}
}
