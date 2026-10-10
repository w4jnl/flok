package relay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// device is a phone the app registered for pushes.
type device struct {
	Token   string    `json:"token"`
	Name    string    `json:"name,omitempty"`
	Sandbox bool      `json:"sandbox,omitempty"`
	Added   time.Time `json:"added"`
}

// deviceStore keeps the devices in devices.json under the data dir (in memory without one).
type deviceStore struct {
	path string
	mu   sync.Mutex
	byTk map[string]device
}

func loadDevices(dir string) (*deviceStore, error) {
	ds := &deviceStore{byTk: map[string]device{}}
	if dir == "" {
		return ds, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ds.path = filepath.Join(dir, "devices.json")
	data, err := os.ReadFile(ds.path)
	if errors.Is(err, os.ErrNotExist) {
		return ds, nil
	}
	if err != nil {
		return nil, err
	}
	var list []device
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, errors.New(ds.path + ": " + err.Error())
	}
	for _, d := range list {
		ds.byTk[d.Token] = d
	}
	return ds, nil
}

func (ds *deviceStore) list() []device {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	out := make([]device, 0, len(ds.byTk))
	for _, d := range ds.byTk {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Added.Before(out[j].Added) })
	return out
}

func (ds *deviceStore) add(d device) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	old, had := ds.byTk[d.Token]
	if had {
		d.Added = old.Added
	}
	ds.byTk[d.Token] = d
	if err := ds.saveLocked(); err != nil { // a device the file does not hold is not registered
		if had {
			ds.byTk[d.Token] = old
		} else {
			delete(ds.byTk, d.Token)
		}
		return err
	}
	return nil
}

func (ds *deviceStore) remove(tok string) bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if _, ok := ds.byTk[tok]; !ok {
		return false
	}
	delete(ds.byTk, tok)
	_ = ds.saveLocked()
	return true
}

func (ds *deviceStore) saveLocked() error {
	if ds.path == "" {
		return nil
	}
	list := make([]device, 0, len(ds.byTk))
	for _, d := range ds.byTk {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Added.Before(list[j].Added) })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := ds.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, ds.path)
}
