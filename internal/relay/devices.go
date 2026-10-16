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
	Prefs   *Prefs    `json:"prefs,omitempty"` // what the phone wants pushed; nil = everything
	Phone   string    `json:"phone,omitempty"` // the paired phone that registered it (phones.json id); "" = a static device token did
	// Live Activities (Dynamic Island, Lock Screen): the token that starts one by push, and
	// the ones running with their own update tokens; the app reports both (PUT …/activity).
	ActivityStart string     `json:"activity_start,omitempty"`
	Activities    []Activity `json:"activities,omitempty"`
}

// hasOverview reports whether the phone runs the overview activity, as it last reported.
func (d device) hasOverview() bool {
	for _, a := range d.Activities {
		if a.Kind == liveOverview {
			return true
		}
	}
	return false
}

// Activity is one Live Activity on a phone.
type Activity struct {
	ID       string `json:"id"`
	Token    string `json:"token"`              // its update token
	Kind     string `json:"kind"`               // overview | agent
	Instance string `json:"instance,omitempty"` // agent: which one it follows
	Pane     string `json:"pane,omitempty"`
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
		if d.Prefs == nil { // a registration without settings (curl) keeps the phone's
			d.Prefs = old.Prefs
		}
		if d.Phone == "" { // a registration with a static token keeps the pairing it had
			d.Phone = old.Phone
		}
		d.ActivityStart, d.Activities = old.ActivityStart, old.Activities
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

// get is the device registered under a push token.
func (ds *deviceStore) get(tok string) (device, bool) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	d, ok := ds.byTk[tok]
	return d, ok
}

// anyFor reports whether a paired phone still has a device registered.
func (ds *deviceStore) anyFor(phone string) bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	for _, d := range ds.byTk {
		if d.Phone == phone {
			return true
		}
	}
	return false
}

// removeByPhone drops every device a paired phone registered and returns them.
func (ds *deviceStore) removeByPhone(phone string) []device {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	var gone []device
	for tok, d := range ds.byTk {
		if d.Phone == phone {
			gone = append(gone, d)
			delete(ds.byTk, tok)
		}
	}
	if len(gone) > 0 {
		_ = ds.saveLocked()
	}
	return gone
}

// setPrefs replaces a device's settings; false when the token is not registered.
func (ds *deviceStore) setPrefs(tok string, p *Prefs) (bool, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	old, ok := ds.byTk[tok]
	if !ok {
		return false, nil
	}
	d := old
	d.Prefs = p
	ds.byTk[tok] = d
	if err := ds.saveLocked(); err != nil {
		ds.byTk[tok] = old
		return true, err
	}
	return true, nil
}

// setActivities replaces a device's Live Activity tokens; false when it is not registered.
func (ds *deviceStore) setActivities(tok, start string, list []Activity) (bool, error) {
	return ds.update(tok, func(d *device) { d.ActivityStart, d.Activities = start, list })
}

// dropActivity forgets one activity (ended, or its token refused); start = "" drops none of
// the start token, start = the token clears it.
func (ds *deviceStore) dropActivity(tok, activityToken, start string) {
	_, _ = ds.update(tok, func(d *device) {
		kept := d.Activities[:0:0]
		for _, a := range d.Activities {
			if a.Token != activityToken {
				kept = append(kept, a)
			}
		}
		d.Activities = kept
		if start != "" && d.ActivityStart == start {
			d.ActivityStart = ""
		}
	})
}

func (ds *deviceStore) update(tok string, change func(*device)) (bool, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	old, ok := ds.byTk[tok]
	if !ok {
		return false, nil
	}
	d := old
	d.Activities = append([]Activity(nil), old.Activities...)
	change(&d)
	ds.byTk[tok] = d
	if err := ds.saveLocked(); err != nil {
		ds.byTk[tok] = old
		return true, err
	}
	return true, nil
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
