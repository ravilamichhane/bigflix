# Bigflix — implementation task list

Architecture baseline: ARCHITECTURE.md, finalized 2026-09-25. Tasks are planned unless checked. Catalog and initial download/playback milestones are implemented. Storage eviction, broader compatibility, and household deployment remain pending.

## Phase 0 — decisions recorded

- [x] Go application with server-rendered HTML, CSS, and minimal vanilla JavaScript.
- [x] No React, Node.js, or frontend build pipeline.
- [x] Local PC first, home-network access and NAS evaluation later.
- [x] YTS-compatible API selected; one list response verified.
- [x] Catalog/source discovery first; TMDB for trends and release metadata.
- [x] Sequential torrent downloading with playback and metadata priority exceptions.
- [x] One shared content store; Watch now and Keep downloaded retention modes.
- [x] SQLite, in-process workers, SSE, native browser video, optional later FFmpeg.

## Phase 1 — catalog and source discovery

- [x] Check repository instructions and available Go toolchain; initialize the module and reproducible build.
- [ ] Validate dependency versions/licenses and choose a pure-Go SQLite driver.
- [x] Scaffold net/http handlers, embedded templates/static files, config, logging, and migrations.
- [ ] Implement YTS adapter with configurable base URL, bounded responses, timeouts, throttling, and retries.
- [ ] Verify list/search, pagination, detail lookup, sorting, torrent fields, and upstream errors against actual responses.
- [x] Add fixture tests for normalization, missing fields, duplicate variants, malformed payloads, and provider outages.
- [ ] Store movies, IMDb/provider IDs, source variants, timestamps, and provider health.
- [ ] Configure a user-provided TMDB credential server-side and add required attribution.
- [ ] Implement TMDB day/week trends and a last-30-days movie release feed using documented date semantics.
- [x] Match titles via IMDb IDs; expose unresolved/ambiguous matches rather than guessing.
- [ ] Add feed caching, refresh jobs, freshness labels, and stale-data fallback.
- [x] Build search, movie details, quality selection, trending/release shelves, and Recently Added to YTS.
- [x] Show absent TMDB configuration explicitly while retaining YTS browsing.
- [ ] Verify layout, accessibility, pagination, and provider-error states in the browser.

Exit: real catalog browsing and source selection work locally. No fabricated availability/trends and no playback claim yet.

## Phase 2 — prove downloading and in-progress playback

- [ ] Validate anacrolix/torrent APIs and platform support in an isolated experiment.
- [ ] Generate or use a known test video and controlled local torrent/seeder.
- [x] Resolve torrent metadata, validate source hash, and select/map video files to pieces.
- [ ] Instrument ascending background piece priority, metadata fetches, and per-reader read-ahead.
- [x] Implement verified seekable reads with bounded waits, cancellation, and resource cleanup.
- [x] Implement media HTTP ranges including HEAD, suffix ranges, invalid ranges, and disconnects.
- [ ] Demonstrate playback while the file is incomplete in Chrome and Safari using H.264/AAC MP4.
- [ ] Test metadata at the file end and forward/backward seeks into uncached regions.
- [ ] Test two readers at different positions sharing one content store.
- [ ] Test slow/stalled peers, unavailable pieces, timeouts, and recovery.
- [x] Restore partial downloads after restart and verify cached data before playback.
- [ ] Record startup/seek/buffering measurements and piece-order evidence under controlled conditions.
- [ ] Confirm the engine supports the required policy; revise the architecture explicitly if it does not.

Exit: sequential scheduling, early playback, seeking, shared storage, and restart recovery are demonstrated before product integration.

## Phase 3 — integrate player and retained downloads

- [x] Connect YTS variant selection to deduplicated torrent jobs.
- [ ] Add Watch now, Keep downloaded, pause/resume, and explicit removal controls.
- [x] Persist desired job state and selected-file completion independently from torrent completion.
- [x] Add playback-session leases and SSE status updates.
- [x] Build player loading/buffering/error/retry behavior and unsupported-codec messaging.
- [ ] Save watch position and add Continue Watching, Downloads, and Kept Movies pages.
- [ ] Test changing cache retention to Keep without copying bytes or restarting the download.
- [x] Add graceful shutdown and recovery from incomplete state transitions.

Exit: browse → select → download/buffer → play/seek → keep → restart works through the local UI.

## Phase 4 — cache, robustness, and local release

- [ ] Implement 50 GB configurable cache target and 10 GB configurable free-space reserve.
- [ ] Account for partial files, allocated space, retained torrent units, and filesystem limitations.
- [ ] Implement lease-aware eviction and safe pause/release of idle background jobs.
- [ ] Protect kept files, active viewers, and active writes; stop admission when no safe space is available.
- [ ] Implement visible bandwidth and seeding policy settings with no idle seeding by default.
- [ ] Validate remote URLs/redirects, torrent paths, symlinks, and source identities.
- [ ] Protect state-changing routes and enforce local-only initial access.
- [ ] Test disk full, restart during download, corrupted cache metadata, deletion during playback, and provider failure.
- [ ] Verify templates escape remote metadata and secrets do not enter logs/browser responses.
- [ ] Document setup, configuration, build/run, storage behavior, backup/restore, and codec limits.
- [ ] Run an end-to-end acceptance scenario and publish measured results.

Exit: a usable local movie player with downloads and cache, with documented limits.

## Phase 5 — home-network pilot

- [ ] Add accounts, login/logout, secure sessions, and per-user progress.
- [ ] Authorize API, video, event, and management requests.
- [ ] Restrict provider/storage configuration to administrators.
- [ ] Enable explicit LAN binding only after access-control tests pass.
- [ ] Load-test increasing viewer counts up to 10, separating cached playback from active torrent streaming.
- [ ] Measure CPU, RAM, disk, network, startup, seeking, and buffering; document actual capacity.

Exit: authenticated household use within measured limits.

## Later extensions — separate acceptance criteria

- [ ] Add ffprobe/FFmpeg repackaging/transcoding only when broad format support is needed; test resource limits and seeking.
- [ ] Add TV metadata, episode identity, and a suitable TV source provider.
- [ ] Add direct HTTP and supported unprotected HLS/DASH providers with transport-specific cache tests.
- [ ] Evaluate site-specific scrapers or a resolver helper only for a selected provider that needs them.
- [ ] Validate QNAP ARM64 build, Container Station setup, mounts, permissions, memory, and throughput.
- [ ] Decide PC/NAS workload placement from measurements; keep SQLite local to the application process.

Public hosting, native TV/mobile clients, distributed services, and guaranteed instant playback for every source are outside this baseline.

## Current verification notes

Go build/unit tests passed; live YTS listing and movie-details responses verified. TMDB routes exist but need a user-provided token and live validation. Metadata refresh is request-driven; periodic jobs, automatic retries, normalized variant tables, provider-health persistence, and a full dependency-license audit remain open. Current storage caches metadata only, not video.

## Playback milestone verification (2026-09-25)

- Integrated anacrolix/torrent v1.61.0 with verified readers and sequential background windows.
- Isolated TCP seeder test discovers metadata from the info hash, serves initial bytes below 4% completion, seeks to uncached bytes, checks ranges/HEAD, pauses, restarts, and reads verified cached bytes without reconnecting to a peer.
- Browser test with a generated H.264/AAC MP4 showed real playback at about 48% completion. Separate Chrome/Safari compatibility runs remain open.
- Race tests and vet pass. Form tests check CSRF, source ownership, and duplicate-start behavior.
- Watch now/Keep, pause/resume, Downloads, live SSE status, player progress saving, and file export are implemented. A Continue Watching shelf and deletion/eviction controls remain open.
- Space protection currently uses a total selected-video budget and free-space reserve, not full allocated-byte accounting/automatic eviction.
- End-of-file MP4 metadata, multi-viewer fairness/load tests, disk-failure tests, and broad codec support still need dedicated validation.
