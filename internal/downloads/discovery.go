package downloads

import (
	"context"
	"log/slog"
	"time"

	"github.com/anacrolix/torrent"
)

// Called with m.mu held. Keep recovery independent of playback readers and storage.
func (m *Manager) recoverPeersLocked(j *job, now time.Time) {
	if j.record.Paused || j.err != "" || (j.file != nil && j.file.BytesCompleted() == j.file.Length()) || len(j.t.PeerConns()) > 0 {
		j.peerlessSince = time.Time{}
		return
	}
	if j.peerlessSince.IsZero() {
		j.peerlessSince = now
	}
	if now.Sub(j.peerlessSince) >= 30*time.Second && now.Sub(j.lastDiscovery) >= time.Minute {
		m.discoverLocked(j, now)
	}
}

func (m *Manager) discoverLocked(j *job, now time.Time) {
	if m.closed || j.discovering || now.Sub(j.lastDiscovery) < 15*time.Second {
		return
	}
	servers := m.client.DhtServers()
	if len(servers) == 0 {
		return
	}
	j.discovering = true
	j.lastDiscovery = now
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() { m.mu.Lock(); j.discovering = false; m.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(m.ctx, 25*time.Second)
		defer cancel()
		for _, server := range servers {
			// Refresh the routing table after losing the previous network.
			if wrapped, ok := server.(torrent.AnacrolixDhtServerWrapper); ok {
				bootstrapCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				_, _ = wrapped.BootstrapContext(bootstrapCtx)
				stop()
			}
			if ctx.Err() != nil {
				return
			}
			_, stop, err := j.t.AnnounceToDht(server)
			if err != nil {
				slog.Warn("peer discovery retry", "hash", j.record.Hash, "error", err)
				continue
			}
			defer stop()
		}
		<-ctx.Done()
	}()
}
