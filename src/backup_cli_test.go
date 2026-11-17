package main

import (
	"errors"
	"testing"

	"github.com/apimgr/shortner/src/service"
)

type fakeRestoreService struct {
	status   service.Status
	stopErr  error
	startErr error
	stops    int
	starts   int
}

func (f *fakeRestoreService) Status() service.Status { return f.status }
func (f *fakeRestoreService) Stop() error {
	f.stops++
	return f.stopErr
}
func (f *fakeRestoreService) Start() error {
	f.starts++
	return f.startErr
}

func TestRestoreServiceLifecycle(t *testing.T) {
	t.Run("stops and restarts an active service", func(t *testing.T) {
		manager := &fakeRestoreService{status: service.Status{Installed: true, State: "running"}}
		wasRunning, err := stopServiceForRestore(manager)
		if err != nil || !wasRunning {
			t.Fatalf("stopServiceForRestore() = (%v, %v), want running service", wasRunning, err)
		}
		if err := restartServiceAfterRestore(manager, wasRunning); err != nil {
			t.Fatal(err)
		}
		if manager.stops != 1 || manager.starts != 1 {
			t.Fatalf("service calls = stop %d, start %d; want one each", manager.stops, manager.starts)
		}
	})

	t.Run("leaves a stopped service stopped", func(t *testing.T) {
		manager := &fakeRestoreService{status: service.Status{Installed: true, State: "stopped"}}
		wasRunning, err := stopServiceForRestore(manager)
		if err != nil || wasRunning {
			t.Fatalf("stopServiceForRestore() = (%v, %v), want stopped service", wasRunning, err)
		}
		if err := restartServiceAfterRestore(manager, wasRunning); err != nil {
			t.Fatal(err)
		}
		if manager.stops != 0 || manager.starts != 0 {
			t.Fatalf("service calls = stop %d, start %d; want none", manager.stops, manager.starts)
		}
	})

	t.Run("does not restart if stopping failed", func(t *testing.T) {
		manager := &fakeRestoreService{status: service.Status{Installed: true, State: "running"}, stopErr: errors.New("stop error")}
		wasRunning, err := stopServiceForRestore(manager)
		if err == nil || wasRunning {
			t.Fatalf("stopServiceForRestore() = (%v, %v), want stop error", wasRunning, err)
		}
		if manager.stops != 1 || manager.starts != 0 {
			t.Fatalf("service calls = stop %d, start %d; want stop only", manager.stops, manager.starts)
		}
	})
}
