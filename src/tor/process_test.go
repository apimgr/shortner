package tor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestControlPortReady(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control_port")
	for _, test := range []struct {
		name string
		data string
		want bool
	}{
		{name: "missing newline", data: "PORT=127.0.0.1:9051"},
		{name: "partial", data: "PORT=127.0.0.1:"},
		{name: "wrong address", data: "PORT=0.0.0.0:9051\n"},
		{name: "invalid port", data: "PORT=127.0.0.1:70000\n"},
		{name: "valid", data: "PORT=127.0.0.1:9051\n", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			if got := controlPortReady(path); got != test.want {
				t.Errorf("controlPortReady() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestTorProcessCreatorRemovesControlPortFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-port")
	creator := &torProcessCreator{binary: "tor"}
	if _, err := creator.New(context.Background(), "--ControlPortWriteToFile", path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("PORT=127.0.0.1:9051\n"), 0600); err != nil {
		t.Fatal(err)
	}
	creator.removeControlPortFile()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("control-port file still exists: %v", err)
	}
}

func TestRemoveStaleControlPortFiles(t *testing.T) {
	dataDir := t.TempDir()
	stale := filepath.Join(dataDir, "control-port-old")
	keep := filepath.Join(dataDir, "hostname")
	if err := os.WriteFile(stale, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleControlPortFiles(dataDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale control-port file still exists: %v", err)
	}
	if contents, err := os.ReadFile(keep); err != nil || string(contents) != "preserve" {
		t.Errorf("unrelated data file changed: contents=%q err=%v", contents, err)
	}
}
