package downloads

import (
	"bigflix/internal/database"
	"context"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent"
)

type testDHT struct {
	torrent.DhtServer
	calls atomic.Int32
}

func (*testDHT) Addr() net.Addr        { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }
func (*testDHT) ID() [20]byte          { return [20]byte{} }
func (*testDHT) Stats() interface{}    { return nil }
func (*testDHT) WriteStatus(io.Writer) {}
func (d *testDHT) Announce([20]byte, int, bool) (torrent.DhtAnnounce, error) {
	d.calls.Add(1)
	return &testAnnounce{peers: make(chan dht.PeersValues)}, nil
}

type testAnnounce struct {
	peers chan dht.PeersValues
	once  sync.Once
}

func (a *testAnnounce) Peers() <-chan dht.PeersValues { return a.peers }
func (a *testAnnounce) Close()                        { a.once.Do(func() { close(a.peers) }) }

func TestPeerRecoveryPreservesJobAndStopsOnShutdown(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := Open(db, Options{MediaDir: filepath.Join(root, "media"), ClientConfig: isolatedConfig()})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	// Register before adding a torrent, as required by the client configuration API.
	server := &testDHT{}
	m.mu.Lock()
	m.client.AddDhtServer(server)
	m.mu.Unlock()
	hash := strings.Repeat("c", 40)
	if _, err = m.Add(hash, "Recovery fixture", "", 1, true); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return server.calls.Load() > 0 })
	baseline := server.calls.Load()
	if err = m.Update(hash, "progress", 0, 42); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	j := m.jobs[hash]
	j.peerlessSince = time.Now().Add(-31 * time.Second)
	m.recoverPeersLocked(j, time.Now())
	m.mu.Unlock()
	eventually(t, func() bool { return server.calls.Load() > baseline })
	calls := server.calls.Load()
	if err = m.Update(hash, "reconnect", 0, 0); err != nil {
		t.Fatal(err)
	}
	if server.calls.Load() != calls {
		t.Fatal("overlapping retry was started")
	}
	state, _ := m.Get(hash)
	if !state.Discovering || !state.Keep || state.Position != 42 {
		t.Fatalf("job state changed: %+v", state)
	}
	if err = m.Update(hash, "pause", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err = m.Update(hash, "reconnect", 0, 0); err != ErrPaused {
		t.Fatalf("paused retry: %v", err)
	}
	done := make(chan struct{})
	go func() { m.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("discovery blocked shutdown")
	}
	records, err := db.Downloads(context.Background())
	if err != nil || len(records) != 1 {
		t.Fatalf("lost stored job: %v", err)
	}
}
