package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cmrigney/sbx-projects/internal/core"
	"github.com/cmrigney/sbx-projects/internal/sbx"
	"github.com/cmrigney/sbx-projects/internal/store"
)

func init() {
	listEvery = 50 * time.Millisecond
	recheckEvery = 50 * time.Millisecond
}

type fixture struct {
	srv   *httptest.Server
	devs  *Devices
	token string
	p     store.Project
}

func newFixture(t *testing.T, seeds []sbx.MockSeed) *fixture {
	t.Helper()
	return newFixtureWith(t, seeds, func(m *sbx.Mock) sbx.Backend { return m })
}

// newFixtureWith serves the seeds through a backend that wraps the mock.
func newFixtureWith(t *testing.T, seeds []sbx.MockSeed, wrap func(*sbx.Mock) sbx.Backend) *fixture {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, _ := st.AddProject(store.Project{Name: "repo", Path: "/src/repo"})
	for _, s := range seeds {
		_ = st.UpsertSandbox(store.Sandbox{Name: s.Name, ProjectID: p.ID, Title: "Title of " + s.Name, Agent: s.Agent, Clone: s.Clone, Source: "branch"})
	}
	mon, err := NewMonitor(&core.Service{Backend: wrap(sbx.NewMock(seeds)), Store: st, Demo: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go mon.Run(ctx)

	devs, _ := LoadDevices(filepath.Join(t.TempDir(), "devices.json"))
	srv := httptest.NewTLSServer((&Server{Monitor: mon, Devices: devs, ID: "abc", Name: "mac"}).Handler())
	t.Cleanup(srv.Close)
	f := &fixture{srv: srv, devs: devs, p: p}
	code := devs.OpenPairing(time.Minute, nil)
	var res struct{ Token string }
	if st := f.do(t, "POST", "/v1/pair", `{"code":"`+code[:4]+"-"+code[4:]+`","device":"Phone"}`, &res); st != 200 || res.Token == "" {
		t.Fatalf("pairing: %d %+v", st, res)
	}
	f.token = res.Token
	return f
}

func (f *fixture) do(t *testing.T, method, path, body string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	if f.token != "" {
		req.Header.Set("Authorization", "Bearer "+f.token)
	}
	resp, err := f.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// waitFor polls the sandbox list until ok accepts it.
func (f *fixture) waitFor(t *testing.T, ok func(map[string]SandboxView) bool) map[string]SandboxView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var list []SandboxView
		f.do(t, "GET", "/v1/sandboxes", "", &list)
		byName := map[string]SandboxView{}
		for _, v := range list {
			byName[v.Name] = v
		}
		if ok(byName) {
			return byName
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out; last list: %+v", byName)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPairing(t *testing.T) {
	f := newFixture(t, nil)
	if st := f.do(t, "POST", "/v1/pair", `{"code":"whatever"}`, nil); st != http.StatusForbidden {
		t.Fatalf("a used code should close pairing: %d", st)
	}
	f.devs.OpenPairing(time.Minute, nil)
	for i := 0; i < maxPairTries; i++ {
		if st := f.do(t, "POST", "/v1/pair", `{"code":"WRONG"}`, nil); st != http.StatusUnauthorized {
			t.Fatalf("try %d: %d", i, st)
		}
	}
	if f.devs.Pairing() {
		t.Fatal("too many wrong codes should close pairing")
	}

	token := f.token
	f.token = "nope"
	if st := f.do(t, "GET", "/v1/projects", "", nil); st != http.StatusUnauthorized {
		t.Fatalf("a bad token got %d", st)
	}
	f.token = token
	if st := f.do(t, "GET", "/v1/projects", "", nil); st != 200 {
		t.Fatalf("the paired token got %d", st)
	}
}

func TestRemovedDeviceIsSignedOutWithoutRestart(t *testing.T) {
	f := newFixture(t, nil)
	other, _ := LoadDevices(f.devs.path)
	// File times can be coarse; make sure the change is visible.
	time.Sleep(10 * time.Millisecond)
	if _, err := other.Remove("Phone"); err != nil {
		t.Fatal(err)
	}
	f.devs.mu.Lock()
	f.devs.mtime = time.Time{}
	f.devs.mu.Unlock()
	if st := f.do(t, "GET", "/v1/projects", "", nil); st != http.StatusUnauthorized {
		t.Fatalf("a removed device got %d", st)
	}
}

// stream opens /v1/events. next returns the next event, and false once the
// server ends the stream.
func (f *fixture) stream(t *testing.T) (next func() (Event, bool)) {
	t.Helper()
	req, _ := http.NewRequest("GET", f.srv.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	resp, err := f.srv.Client().Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 1<<20)
	return func() (Event, bool) {
		for sc.Scan() {
			if b, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var ev Event
				if err := json.Unmarshal([]byte(b), &ev); err != nil {
					t.Fatal(err)
				}
				return ev, true
			}
		}
		if ctx.Err() != nil {
			t.Fatal("timed out reading the stream")
		}
		return Event{}, false
	}
}

func TestEventsStartWithASnapshotThenChanges(t *testing.T) {
	f := newFixture(t, []sbx.MockSeed{{Name: "claude-a", Agent: "claude", Workspace: "/src/repo", Running: true, AgentUp: true, Clone: true}})
	stream := f.stream(t)
	next := func() Event {
		ev, ok := stream()
		if !ok {
			t.Fatal("stream ended")
		}
		return ev
	}
	if ev := next(); ev.Type != "snapshot" || len(ev.Sandboxes) != 1 || len(ev.Projects) != 1 {
		t.Fatalf("first event = %+v", ev)
	}
	f.do(t, "POST", "/v1/sandboxes/claude-a/stop", "", nil)
	for {
		ev := next()
		if ev.Type == "action" {
			if !ev.OK || ev.Name != "claude-a" {
				t.Fatalf("action = %+v", ev)
			}
			return
		}
		if ev.Type != "sandbox" || ev.Sandbox.Name != "claude-a" {
			t.Fatalf("unexpected event %+v", ev)
		}
	}
}

func TestCreateRunsThroughToTheAgent(t *testing.T) {
	f := newFixture(t, nil)
	var errBody map[string]string
	if st := f.do(t, "POST", "/v1/sandboxes", `{"projectId":99,"agent":"claude","source":"branch","value":"x"}`, &errBody); st != 404 {
		t.Fatalf("unknown project: %d %v", st, errBody)
	}
	if st := f.do(t, "POST", "/v1/sandboxes", `{"projectId":1,"agent":"claude","source":"pr","value":"nope"}`, nil); st != 400 {
		t.Fatalf("a PR needs a number: %d", st)
	}
	var v SandboxView
	st := f.do(t, "POST", "/v1/sandboxes", `{"projectId":1,"agent":"claude","source":"pr","value":"#12","model":"opus","mcps":["notion"]}`, &v)
	if st != http.StatusAccepted || v.Name != "claude-pr-12" || !v.Busy || !v.Clone || v.ModelLabel != "Opus" {
		t.Fatalf("create: %d %+v", st, v)
	}
	got := f.waitFor(t, func(m map[string]SandboxView) bool { return m["claude-pr-12"].Running && !m["claude-pr-12"].Busy })
	if got["claude-pr-12"].Branch != "pr/12" || strings.Join(got["claude-pr-12"].MCPs, ",") != "notion" {
		t.Fatalf("created = %+v", got["claude-pr-12"])
	}
	var opts OptionsView
	f.do(t, "GET", "/v1/options?project=1", "", &opts)
	if opts.Defaults.Models["claude"] != "opus" || strings.Join(opts.Defaults.MCPs, ",") != "notion" {
		t.Fatalf("the project should remember the picks: %+v", opts.Defaults)
	}
}

func TestActionsCheckTheSandboxsState(t *testing.T) {
	f := newFixture(t, []sbx.MockSeed{{Name: "claude-off", Agent: "claude", Workspace: "/src/repo", Clone: true}})
	f.waitFor(t, func(m map[string]SandboxView) bool { return m["claude-off"].State == "stopped" })
	for _, path := range []string{"/stop", "/pr", "/checkout"} {
		if st := f.do(t, "POST", "/v1/sandboxes/claude-off"+path, "", nil); st != http.StatusConflict {
			t.Errorf("%s on a stopped sandbox: %d", path, st)
		}
	}
	if st := f.do(t, "GET", "/v1/sandboxes/claude-off/diff", "", nil); st != 200 {
		t.Errorf("a sandbox with no changes has an empty diff: %d", st)
	}
	if st := f.do(t, "POST", "/v1/sandboxes/claude-off/resume", "", nil); st != http.StatusAccepted {
		t.Fatalf("resume: %d", st)
	}
	f.waitFor(t, func(m map[string]SandboxView) bool { return m["claude-off"].Running && !m["claude-off"].Busy })
}

func TestSigningOutEndsAnOpenStream(t *testing.T) {
	f := newFixture(t, nil)
	next := f.stream(t)
	if ev, _ := next(); ev.Type != "snapshot" {
		t.Fatalf("first event = %+v", ev)
	}
	other, _ := LoadDevices(f.devs.path)
	if _, err := other.Remove("Phone"); err != nil {
		t.Fatal(err)
	}
	f.devs.mu.Lock()
	f.devs.mtime = time.Time{}
	f.devs.mu.Unlock()
	for {
		ev, ok := next()
		if !ok {
			break
		}
		if ev.Type == "sandbox" || ev.Type == "snapshot" {
			t.Fatalf("a signed-out device was sent %+v", ev)
		}
	}
	if st := f.do(t, "GET", "/v1/events", "", nil); st != http.StatusUnauthorized {
		t.Fatalf("reconnecting got %d", st)
	}
}

// slowProbes holds every exec until it is cancelled, as an sbx exec still
// on its way to the sandbox would be, and notes a stop that lands while
// one is in flight.
type slowProbes struct {
	*sbx.Mock
	started  chan struct{}
	inFlight atomic.Int32
	raced    atomic.Bool
}

func (b *slowProbes) Exec(ctx context.Context, name, script string) (string, error) {
	b.inFlight.Add(1)
	defer b.inFlight.Add(-1)
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return "", ctx.Err()
}

func (b *slowProbes) Stop(ctx context.Context, name string) error {
	if b.inFlight.Load() > 0 {
		b.raced.Store(true)
	}
	return b.Mock.Stop(ctx, name)
}

func TestStopWaitsOutAProbeInFlight(t *testing.T) {
	be := &slowProbes{started: make(chan struct{}, 1)}
	f := newFixtureWith(t, []sbx.MockSeed{{Name: "claude-a", Agent: "claude", Workspace: "/src/repo", Running: true, AgentUp: true, Clone: true}},
		func(m *sbx.Mock) sbx.Backend { be.Mock = m; return be })
	select {
	case <-be.started:
	case <-time.After(5 * time.Second):
		t.Fatal("no probe started")
	}
	if st := f.do(t, "POST", "/v1/sandboxes/claude-a/stop", "", nil); st != http.StatusAccepted {
		t.Fatalf("stop: %d", st)
	}
	f.waitFor(t, func(m map[string]SandboxView) bool { return m["claude-a"].State == "stopped" && !m["claude-a"].Busy })
	if be.raced.Load() {
		t.Fatal("the sandbox was stopped while a probe's exec could still reach it")
	}
}

// flakyList fails to list while failing is set.
type flakyList struct {
	*sbx.Mock
	failing atomic.Bool
}

func (b *flakyList) List(ctx context.Context) ([]sbx.Sandbox, error) {
	if b.failing.Load() {
		return nil, errors.New("sandboxd isn't running")
	}
	return b.Mock.List(ctx)
}

func TestListErrorsReachOpenStreams(t *testing.T) {
	be := &flakyList{}
	f := newFixtureWith(t, nil, func(m *sbx.Mock) sbx.Backend { be.Mock = m; return be })
	next := f.stream(t)
	status := func() Event {
		for {
			ev, ok := next()
			if !ok {
				t.Fatal("stream ended")
			}
			if ev.Type == "status" {
				return ev
			}
		}
	}
	if ev, _ := next(); ev.Type != "snapshot" || ev.ListError != "" {
		t.Fatalf("first event = %+v", ev)
	}
	be.failing.Store(true)
	if ev := status(); ev.ListError != "sandboxd isn't running" {
		t.Fatalf("failing: %+v", ev)
	}
	be.failing.Store(false)
	if ev := status(); ev.ListError != "" {
		t.Fatalf("recovered: %+v", ev)
	}
}
