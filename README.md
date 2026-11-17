# Bigflix

Bigflix is a self-hosted movie catalog and torrent streaming server. It combines a single Go binary with an embedded React + TypeScript frontend, ConnectRPC APIs, SQLite persistence, and on-the-fly FFmpeg remuxing/transcoding.

## Features

- **Browse & Search:** Explore the YTS catalog by search query, IMDb ID, genre, or sort order, plus TMDB trending and new releases with automatic IMDb source matching.
- **Stream While Downloading:** Start watching immediately after torrent metadata loads. Sequential background fetching and active reader piece prioritization keep playback and seeking responsive.
- **Browser & Desktop Playback:**
  - Plays MP4/WebM directly, remuxes compatible containers (such as HEVC in MKV for Safari/supported browsers) with zero video re-encoding, or transcodes on the fly to H.264 for browsers without HEVC support (using VideoToolbox hardware acceleration on macOS when available).
  - Copy an authenticated stream URL directly from the player's **VLC** control bar button to play the untouched source in VLC, IINA, or mpv while background downloading continues.
- **Subtitles:** Search YIFY Subtitles, extract bundled `.srt`/`.vtt` tracks from torrents, or upload your own `.srt`/`.vtt` files (up to 2 MB) with adjustable timing offset.
- **Manual Storage Cleanup:** Delete downloads directly from the **Downloads** page or the **Watch** player page to cancel active readers, drop the torrent, clear saved metadata/subtitles, and remove downloaded files from disk.
- **Token Lock Security:** Every ConnectRPC API endpoint and media stream requires the access token. Lock the UI anytime from the header or rotate the token in **Settings**.
- **Network Interface Binding:** Bind torrent TCP, uTP, and DHT listeners to specific network interfaces and fixed/dynamic ports from **Settings**.

---

## Quick Start with Docker

Build the image (bundles the React frontend, Go server, and `ffmpeg`):

```sh
docker build -t bigflix .
```

Run with persistent host directories for `/data` (SQLite database) and `/media` (downloaded torrents):

```sh
docker run -d --name bigflix \
  -p 3000:3000 \
  -p 51413:51413/tcp \
  -p 51413:51413/udp \
  -e AUTH_TOKEN=your-secret-token \
  -e TMDB_READ_TOKEN=your-tmdb-read-token \
  -v /your/custom/data:/data \
  -v /your/custom/media:/media \
  bigflix
```

### Custom Container Paths

To use custom paths inside the container instead of `/data` and `/media/torrents`, override `DATA_DIR` and `MEDIA_DIR` alongside matching volume mounts:

```sh
docker run -d --name bigflix \
  -p 3000:3000 \
  -p 51413:51413/tcp \
  -p 51413:51413/udp \
  -e AUTH_TOKEN=your-secret-token \
  -e DATA_DIR=/custom/data \
  -e MEDIA_DIR=/custom/media \
  -v /your/custom/data:/custom/data \
  -v /your/custom/media:/custom/media \
  bigflix
```

Open `http://localhost:3000` (or `http://YOUR-SERVER-IP:3000`) and enter your `AUTH_TOKEN` (defaults to `bigflix` if not set).

---

## Running Locally from Source

### Prerequisites

- **Go 1.26+**
- **Node.js 22+** and **npm** (to build `frontend/` into `internal/web/dist`)
- **FFmpeg** (`ffmpeg` and `ffprobe` in `PATH`, required for remuxing/transcoding non-MP4 containers in the browser)

### Build & Run

```sh
# 1. Install frontend dependencies and build embedded SPA assets
npm --prefix frontend install
npm --prefix frontend run build

# 2. Run the server (or build a single standalone binary)
AUTH_TOKEN=your-secret-token go run ./cmd/bigflix
```

Or build a standalone binary with all React assets embedded:

```sh
npm --prefix frontend run build
go build -o bin/bigflix ./cmd/bigflix
AUTH_TOKEN=your-secret-token ./bin/bigflix
```

For hot-reloading development, run Vite and point the Go backend to the Vite dev server via `FRONTEND_URL` (or run `task dev`):

```sh
# Terminal 1
npm --prefix frontend run dev

# Terminal 2
FRONTEND_URL=http://127.0.0.1:5173 go run ./cmd/bigflix
```

Open `http://localhost:3000` — the Go server serves ConnectRPC and `/media/*` directly and proxies frontend routes and HMR to Vite.

If you use [Task](https://taskfile.dev/), `task run` builds the frontend and starts the server using `.env.example`, and `task dev` starts both Vite and the Go proxy together.

---

## Environment Variables

Go reads configuration from environment variables (it does not load `.env` files automatically; see [.env.example](.env.example)).

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `3000` | HTTP port for the web UI, ConnectRPC APIs, and media streams |
| `BIND_ADDR` | `127.0.0.1` (`0.0.0.0` in Docker) | Listen IP address (`0.0.0.0` for LAN or remote Docker access) |
| `AUTH_TOKEN` | `bigflix` | Initial access token for ConnectRPC and media streams (rotating in Settings persists to SQLite and takes precedence) |
| `TORRENT_LISTEN_PORT` | `51413` | Startup BitTorrent TCP/UDP port (`0` for dynamic; saved Settings override) |
| `DATA_DIR` | `./data` (`/data` in Docker) | Directory for `bigflix.db` SQLite database |
| `MEDIA_DIR` | `./media/torrents` (`/media/torrents` in Docker) | Directory for downloaded torrent media files |
| `MEDIA_BUDGET_GB` | `50` | Maximum total selected-video size budget across active downloads (decimal GB) |
| `MIN_FREE_GB` | `10` | Minimum free disk space reserve; downloading pauses below this threshold |
| `YTS_API_URL` | `https://movies-api.accel.li/api/v2/` | YTS API base URL |
| `TMDB_READ_TOKEN` | unset | Optional TMDB API Read Access Token (Bearer token) for Trending and New Releases tabs |
| `FRONTEND_URL` | unset | Optional dev frontend server URL (e.g. `http://127.0.0.1:5173`); when set, Go proxies frontend requests there instead of serving embedded `dist` |

---

## How to Use

1. **Unlock:** Open Bigflix in your browser and enter your access token (`bigflix` by default unless `AUTH_TOKEN` is set or rotated). Click **Lock** in the top navigation bar at any time to clear the browser session.
2. **Find a Movie:** Browse **Discover**, **Trending**, or **New releases** (requires `TMDB_READ_TOKEN`), or search by title / IMDb ID (`tt...`).
3. **Start Streaming:** Open a movie, pick a quality/source, and click **▶ Watch now** (or **↓ Keep** to mark it as retained).
4. **Watch & Control Playback:**
   - Playback begins as soon as initial pieces and headers are ready.
   - **Keyboard shortcuts:** `Space`/`K` play/pause, `←`/`→` seek 5s, `J`/`L` seek 10s, `↑`/`↓` volume, `M` mute, `F` fullscreen, `C` subtitles.
   - **VLC button:** Click **VLC** in the player control bar to copy an authenticated `/media/{hash}?token=...` stream URL for external players like VLC, IINA, or mpv.
   - **CC+ button:** Search YIFY Subtitles, load bundled torrent subtitle files, upload `.srt`/`.vtt` files, and adjust subtitle timing offset.
   - **↻ button:** Reinitialize/retry playback if peers reconnect or a stream stalls.
5. **Manage Downloads & Free Space:**
   - Visit **Downloads** or use the panel below the player to pause/resume, toggle **Keep**, trigger peer discovery (**Find peers again**), save a copy of the video file, or click **Delete** / **Delete download** to remove the torrent and free disk space.
6. **Settings:**
   - **Access lock:** Rotate your access token (saved in SQLite) or lock the current session.
   - **Network interfaces:** Select specific network interfaces and fixed or dynamic TCP/UDP ports for torrent traffic, then click **Save and restart client**.

---

## Development & Verification

```sh
# Regenerate Go and TypeScript ConnectRPC code after editing proto/bigflix/v1/bigflix.proto
task proto

# Build frontend TypeScript & Vite bundle
npm --prefix frontend run build

# Run Go test suite
go test -count=1 ./...
go vet ./...
```
