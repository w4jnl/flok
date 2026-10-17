package relay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/w4jnl/flok/internal/link/wire"
)

// A tenant is the set of floks and phones that see each other on one relay. Membership is held
// by keys the relay issues: a flok's own key (instances.json, from flok link join), a phone's own
// key (phones.json, from flok phone pair). The relay's own tenant, DefaultTenant, is where the
// static tokens of its config link and sign in, and where every record from before tenants
// belongs; it needs no file.

// DefaultTenant is the id and name of the relay's own tenant.
const DefaultTenant = "default"

// tenantRec is one tenant as tenants.json keeps it.
type tenantRec struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	By      string    `json:"by,omitempty"` // who made it: the admin flok's name
}

// instanceRec is a flok that joined a tenant by code: its key, kept as a hash, and what it
// said about itself. A flok linking with a token from the relay's config has no record.
type instanceRec struct {
	ID         string    `json:"id"`
	Tenant     string    `json:"tenant"`
	Name       string    `json:"name"`
	KeySHA256  string    `json:"key_sha256"`
	Hostname   string    `json:"hostname,omitempty"`
	Version    string    `json:"version,omitempty"`
	Joined     time.Time `json:"joined"`
	LastSeen   time.Time `json:"last_seen,omitzero"`
	ApprovedBy string    `json:"approved_by,omitempty"` // "phone Jaro's iPhone", "flok home", "admin home"
}

func (r instanceRec) info() wire.InstanceInfo {
	return wire.InstanceInfo{ID: r.ID, Name: r.Name, Hostname: r.Hostname, Version: r.Version, Joined: r.Joined, LastSeen: r.LastSeen, ApprovedBy: r.ApprovedBy}
}

// tenantNameShape is what a tenant may be called: a word a shell and a URL take as it is.
var tenantNameShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidTenantName says whether a name may name a tenant.
func ValidTenantName(s string) bool { return tenantNameShape.MatchString(s) }

// tenantStore keeps the tenants in tenants.json under the data dir (in memory without one). The
// default tenant is never in the file; it is always there.
type tenantStore struct {
	path string
	mu   sync.Mutex
	byID map[string]tenantRec
}

func loadTenants(dir string) (*tenantStore, error) {
	ts := &tenantStore{byID: map[string]tenantRec{}}
	if dir == "" {
		return ts, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ts.path = filepath.Join(dir, "tenants.json")
	var list []tenantRec
	if err := loadList(ts.path, &list); err != nil {
		return nil, err
	}
	for _, t := range list {
		if t.ID != DefaultTenant {
			ts.byID[t.ID] = t
		}
	}
	return ts, nil
}

// get is the tenant with this id; the default tenant always exists.
func (ts *tenantStore) get(id string) (tenantRec, bool) {
	if id == DefaultTenant {
		return tenantRec{ID: DefaultTenant, Name: DefaultTenant}, true
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t, ok := ts.byID[id]
	return t, ok
}

// find is the tenant with this id, else the one with this name (case does not matter).
func (ts *tenantStore) find(nameOrID string) (tenantRec, bool) {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return tenantRec{}, false
	}
	if strings.EqualFold(nameOrID, DefaultTenant) {
		return tenantRec{ID: DefaultTenant, Name: DefaultTenant}, true
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if t, ok := ts.byID[nameOrID]; ok {
		return t, true
	}
	for _, t := range ts.byID {
		if strings.EqualFold(t.Name, nameOrID) {
			return t, true
		}
	}
	return tenantRec{}, false
}

// list is every tenant, the default first, then by creation.
func (ts *tenantStore) list() []tenantRec {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	out := make([]tenantRec, 0, len(ts.byID)+1)
	out = append(out, tenantRec{ID: DefaultTenant, Name: DefaultTenant})
	rest := make([]tenantRec, 0, len(ts.byID))
	for _, t := range ts.byID {
		rest = append(rest, t)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Created.Before(rest[j].Created) })
	return append(out, rest...)
}

func (ts *tenantStore) count() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return len(ts.byID) + 1
}

func (ts *tenantStore) add(t tenantRec) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.byID[t.ID] = t
	if err := ts.saveLocked(); err != nil {
		delete(ts.byID, t.ID)
		return err
	}
	return nil
}

func (ts *tenantStore) remove(id string) (tenantRec, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	t, ok := ts.byID[id]
	if !ok {
		return tenantRec{}, false
	}
	delete(ts.byID, id)
	_ = ts.saveLocked()
	return t, true
}

func (ts *tenantStore) saveLocked() error {
	list := make([]tenantRec, 0, len(ts.byID))
	for _, t := range ts.byID {
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Created.Before(list[j].Created) })
	return saveList(ts.path, list)
}

// instanceStore keeps the floks that joined by code in instances.json under the data dir (in
// memory without one).
type instanceStore struct {
	path   string
	mu     sync.Mutex
	byID   map[string]instanceRec
	byHash map[string]string    // key sha256 → id
	seen   map[string]time.Time // id → LastSeen as the file has it
}

func loadInstances(dir string) (*instanceStore, error) {
	is := &instanceStore{byID: map[string]instanceRec{}, byHash: map[string]string{}, seen: map[string]time.Time{}}
	if dir == "" {
		return is, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	is.path = filepath.Join(dir, "instances.json")
	var list []instanceRec
	if err := loadList(is.path, &list); err != nil {
		return nil, err
	}
	for _, r := range list {
		if r.Tenant == "" {
			r.Tenant = DefaultTenant
		}
		is.byID[r.ID] = r
		is.byHash[r.KeySHA256] = r.ID
		is.seen[r.ID] = r.LastSeen
	}
	return is, nil
}

// ListInstances reads the floks a relay's data dir holds, for flok-relay instances.
func ListInstances(dir string) ([]instanceRec, error) {
	is, err := loadInstances(dir)
	if err != nil {
		return nil, err
	}
	return is.list(""), nil
}

// ListTenants reads the tenants a relay's data dir holds, the default first, for flok-relay tenants.
func ListTenants(dir string) ([]wire.TenantInfo, error) {
	ts, err := loadTenants(dir)
	if err != nil {
		return nil, err
	}
	is, err := loadInstances(dir)
	if err != nil {
		return nil, err
	}
	ps, err := loadPhones(dir)
	if err != nil {
		return nil, err
	}
	var out []wire.TenantInfo
	for _, t := range ts.list() {
		info := wire.TenantInfo{ID: t.ID, Name: t.Name, Created: t.Created, Static: t.ID == DefaultTenant}
		info.Instances = len(is.list(t.ID))
		info.Phones = len(ps.listTenant(t.ID))
		out = append(out, info)
	}
	return out, nil
}

// list is the floks of a tenant ("" = all) in the order they joined.
func (is *instanceStore) list(tenant string) []instanceRec {
	is.mu.Lock()
	defer is.mu.Unlock()
	out := make([]instanceRec, 0, len(is.byID))
	for _, r := range is.byID {
		if tenant == "" || r.Tenant == tenant {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Joined.Before(out[j].Joined) })
	return out
}

func (is *instanceStore) count() int {
	is.mu.Lock()
	defer is.mu.Unlock()
	return len(is.byID)
}

func (is *instanceStore) add(r instanceRec) error {
	is.mu.Lock()
	defer is.mu.Unlock()
	is.byID[r.ID] = r
	is.byHash[r.KeySHA256] = r.ID
	is.seen[r.ID] = r.LastSeen
	if err := is.saveLocked(); err != nil { // a flok the file does not hold has not joined
		delete(is.byID, r.ID)
		delete(is.byHash, r.KeySHA256)
		delete(is.seen, r.ID)
		return err
	}
	return nil
}

// byKey is the flok a bearer key belongs to.
func (is *instanceStore) byKey(tok string) (instanceRec, bool) {
	if tok == "" {
		return instanceRec{}, false
	}
	is.mu.Lock()
	defer is.mu.Unlock()
	id, ok := is.byHash[hashToken(tok)]
	if !ok {
		return instanceRec{}, false
	}
	r, ok := is.byID[id]
	return r, ok
}

func (is *instanceStore) get(id string) (instanceRec, bool) {
	is.mu.Lock()
	defer is.mu.Unlock()
	r, ok := is.byID[id]
	return r, ok
}

// find is a tenant's flok with this id, else the one with this name (case does not matter).
func (is *instanceStore) find(tenant, nameOrID string) (instanceRec, bool) {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return instanceRec{}, false
	}
	is.mu.Lock()
	defer is.mu.Unlock()
	if r, ok := is.byID[nameOrID]; ok && r.Tenant == tenant {
		return r, true
	}
	for _, r := range is.byID {
		if r.Tenant == tenant && strings.EqualFold(r.Name, nameOrID) {
			return r, true
		}
	}
	return instanceRec{}, false
}

func (is *instanceStore) remove(id string) (instanceRec, bool) {
	is.mu.Lock()
	defer is.mu.Unlock()
	r, ok := is.byID[id]
	if !ok {
		return instanceRec{}, false
	}
	delete(is.byID, id)
	delete(is.byHash, r.KeySHA256)
	delete(is.seen, id)
	_ = is.saveLocked()
	return r, true
}

// linked notes a flok's hello: its name, machine and version as they are now, and that it was
// seen; the file is rewritten at most every few minutes for a mere seen, at once for a change.
func (is *instanceStore) linked(id, name, hostname, version string, now time.Time) {
	is.mu.Lock()
	defer is.mu.Unlock()
	r, ok := is.byID[id]
	if !ok {
		return
	}
	changed := r.Name != name || r.Hostname != hostname || r.Version != version
	r.Name, r.Hostname, r.Version, r.LastSeen = name, hostname, version, now
	is.byID[id] = r
	if !changed && now.Sub(is.seen[id]) < 5*time.Minute {
		return
	}
	if is.saveLocked() == nil {
		is.seen[id] = now
	}
}

func (is *instanceStore) saveLocked() error {
	list := make([]instanceRec, 0, len(is.byID))
	for _, r := range is.byID {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Joined.Before(list[j].Joined) })
	return saveList(is.path, list)
}

// loadList reads a JSON list file into v; a missing file is an empty list.
func loadList(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return errors.New(path + ": " + err.Error())
	}
	return nil
}

// saveList writes a JSON list file (temp + rename, 0600); no path means in memory only.
func saveList(path string, v any) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newTenantID() string { return newPhoneID() }
