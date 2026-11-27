<h1 align="center">macdrop</h1>

<p align="center">
  <b>Forward files to a Telegram bot. They land on your computer.</b><br/>
  Downloads up to 2 GB per file, with a queue, parallel downloads, retries and resume.
</p>

<p align="center">
  <a href="https://github.com/nineria/telegram-bot-downloader/actions/workflows/ci.yml"><img src="https://github.com/nineria/telegram-bot-downloader/actions/workflows/ci.yml/badge.svg?branch=develop" alt="CI"/></a>
  <a href="https://github.com/nineria/telegram-bot-downloader/releases"><img src="https://img.shields.io/github/v/release/nineria/telegram-bot-downloader" alt="Release"/></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/nineria/telegram-bot-downloader" alt="License: MIT"/></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/nineria/telegram-bot-downloader" alt="Go version"/></a>
</p>

<p align="center">
  <a href="#get-started">Get started</a>&nbsp;&middot;&nbsp;<a href="#configuration">Configuration</a>&nbsp;&middot;&nbsp;<a href="https://github.com/nineria/telegram-bot-downloader/releases">Releases</a>&nbsp;&middot;&nbsp;<a href="CONTRIBUTING.md">Contributing</a>
</p>

---

Send photos, videos, documents and audio to your own bot from any device, and macdrop saves them to a folder on the machine it runs on. A plain Telegram bot can only fetch 20 MB. macdrop uses a local Bot API server, so large videos work too.

- **Large files.** Up to 2 GB per file, not 20 MB.
- **A real queue.** Downloads run 2 at a time by default, retry on network errors, resume partial files and survive restarts.
- **Live progress.** Every file gets a status message with percent, speed and ETA, edited in place.
- **Only yours.** The bot answers one Telegram user and ignores everyone else.

> [!NOTE]
> macdrop runs on your own machine. Nothing goes through a third-party server except Telegram itself.

## Get started

You need Docker, a bot token from [@BotFather](https://t.me/BotFather), your numeric Telegram user ID, and an API ID and hash from <https://my.telegram.org>.

```sh
git clone https://github.com/nineria/telegram-bot-downloader.git
cd telegram-bot-downloader
cp .env.example .env        # fill in the Telegram values
docker compose up -d --build
```

Then forward any file to your bot. Files are saved to `./downloads` (set `DOWNLOAD_DIR` in `.env` to use another folder). The Inngest dashboard is at <http://127.0.0.1:8288>, and logs are one command away: `docker compose logs -f macdrop`.

## See it in action

Each file you forward gets one message that the bot keeps editing:

```text
queued
  ⏳ 1752664677449.mp4
  59.5 MB · queued

downloading
  ⬇️ 1752664677449.mp4
  🟩🟩🟩🟩⬜⬜⬜⬜⬜⬜⬜⬜ 33%
  📦 19.6 MB / 59.5 MB
  🚀 13.1 MB/s  ⏳ ETA 3s  ⏱ 2s

done
  ✅ 1752664677449.mp4
  🟩🟩🟩🟩🟩🟩🟩🟩🟩🟩🟩🟩 100%
  📦 59.5 MB
  📁 /Users/you/Downloads/telegram/208_1752664677449.mp4
```

Send `/status` for the whole queue:

```text
📊 Status
⬇️ downloading: 2
  • 1752664677449.mp4
  • 1752664652812.mp4
⏳ waiting: 1
  • report.mp4
❌ failed: 0 (reply /retry)
✅ done since start: 12
```

## What it does

| Capability | What you get |
|---|---|
| **Large files** | A local Bot API server lifts Telegram's 20 MB bot limit, up to 2 GB per file |
| **Queue and retries** | [Inngest](https://www.inngest.com) holds the queue; each file retries up to 8 times and resumes the partial download |
| **Albums** | Every file in a forwarded album is queued; reply `/retry` to one failed file to retry its whole album |
| **Duplicates** | A file you already downloaded is skipped with a note, until you delete the saved copy |
| **Photos** | Saved with the right extension (`.jpg`) even though Telegram sends them without a name |
| **Self-healing** | Exits for a restart if the Inngest connection drops or Inngest stops running its functions |
| **Cross-platform** | Linux, macOS and Windows; the download folder can be on another disk |

## When to reach for it

| Situation | Why macdrop |
|---|---|
| Moving a phone video to your computer without cables or cloud storage | Forward it to the bot; it is on disk when the progress message turns green |
| A big batch from a group chat | Forward them all at once; they queue and download two at a time |
| A home server or NAS that should collect files | Run it with Docker Compose and let it restart itself |

## Commands

| Command | What it does |
|---|---|
| `/status` | Shows what is downloading, waiting and failed (counts cover this run of the bot) |
| `/retry` | Retries every failed file |
| `/retry` as a reply | Retries that file, and its whole album if it has one |

`./status.sh` prints a one-line summary for terminals and dashboards (done today, downloading, waiting, failed, free disk). It needs `sh` and `python3`, so it works on macOS and Linux only.

## Prerequisites

| Requirement | Minimum | Check | Install |
|---|---|---|---|
| Docker (Compose path) | with Compose v2 | `docker compose version` | <https://docs.docker.com/get-docker/> |
| Bot token | any bot | message [@BotFather](https://t.me/BotFather) | `/newbot` |
| Your Telegram user ID | numeric | message [@userinfobot](https://t.me/userinfobot) | |
| API ID and hash | from my.telegram.org | <https://my.telegram.org> | API development tools |
| Go (manual path only) | see `go.mod` | `go version` | <https://go.dev/dl/> |

## Install

Pick one.

**Docker Compose (recommended).** See [Get started](#get-started).

**Prebuilt binary.** Download the archive for your platform from [Releases](https://github.com/nineria/telegram-bot-downloader/releases) (Linux, macOS and Windows; `checksums.txt` lists SHA-256 sums). You still need the Bot API server and Inngest running, as in the manual setup below.

<details>
<summary>Manual setup (without Compose)</summary>

```sh
cp .env.example .env   # fill it in

# Local Telegram Bot API server (files over 20 MB)
docker run -d --rm --name tgapi -p 127.0.0.1:8081:8081 \
  -e TELEGRAM_API_ID -e TELEGRAM_API_HASH -e TELEGRAM_LOCAL=1 \
  -v "$LOCAL_API_ROOT:/var/lib/telegram-bot-api" \
  aiogram/telegram-bot-api:latest

# Inngest dev server
docker run -d --rm --name inngest -p 127.0.0.1:8288:8288 -p 127.0.0.1:8289:8289 \
  -v "$HOME/.inngest-dev-data:/.inngest" \
  inngest/inngest:latest inngest dev --persist --no-discovery --no-poll

go build -o macdrop . && ./macdrop
```

`LOCAL_API_ROOT` must be the host folder you mount at `/var/lib/telegram-bot-api` (the bot maps the server's file paths from that fixed location). Export the variables from `.env` (for example `set -a; . ./.env; set +a`) before the `docker run` commands. The bot itself reads `.env` from the working directory.

**Windows (PowerShell):** set the variables with `$env:TELEGRAM_API_ID="..."`, use a Windows path such as `C:\tg-data` for `LOCAL_API_ROOT`, and write the volume as `-v "${env:LOCAL_API_ROOT}:/var/lib/telegram-bot-api"`. Build with `go build -o macdrop.exe .` and run `.\macdrop.exe`.

</details>

## Configuration

All settings are environment variables, read from the process or from `.env` (see `.env.example`).

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `TELEGRAM_BOT_TOKEN` | yes | | Bot token from @BotFather |
| `TELEGRAM_OWNER_ID` | yes | | Only this user can use the bot |
| `TELEGRAM_API_URL` | yes | | Local Bot API server, e.g. `http://127.0.0.1:8081` |
| `LOCAL_API_ROOT` | yes | | Host folder mounted at `/var/lib/telegram-bot-api` |
| `INNGEST_DEV`, `INNGEST_BASE_URL` | yes | | Inngest dev server, e.g. `1` and `http://127.0.0.1:8288` |
| `INNGEST_GATEWAY_HOST` | no | | Inngest connect gateway as `host:port`, only when it is not on localhost (set by Compose) |
| `DOWNLOAD_DIR` | no | `~/Downloads/telegram` | Where files are saved (with Compose: the host folder, default `./downloads`) |
| `PARALLEL_DOWNLOADS` | no | `2` | Files downloaded at the same time |
| `MAX_RETRIES` | no | `8` | Retries per file before it is marked failed |
| `INNGEST_PORT` | no | `8288` | Compose only: host port for the Inngest dashboard |

Saved files are named `<message id>_<name>`. Characters Windows forbids (`: ? * " < > | \`) are replaced with `_`, so a name can differ slightly from the original.

## How it works

```text
Telegram  <->  macdrop  --event-->  Inngest (queue, retries)  --run-->  macdrop
                  |                                                       |
                  +--- getFile --->  local Bot API server  --- file ---> DOWNLOAD_DIR
```

- The bot long-polls Telegram and turns each forwarded file into an Inngest event.
- An Inngest function downloads it: at most `PARALLEL_DOWNLOADS` at a time, up to `MAX_RETRIES` retries, and the queue survives restarts.
- The bot asks the local Bot API server to fetch the file, then moves the result into place (a copy-and-delete when the folders are on different disks).
- If the Inngest connection stays down for over 2 minutes, the bot exits so a process supervisor (launchd, systemd, the Docker restart policy) restarts it.
- A health check sends a ping event every minute. If none is handled for 5 minutes (the connection looks up but Inngest runs nothing), the bot exits for a restart too.

<details>
<summary>Platform notes</summary>

- **macOS, Windows, Linux:** the Go code builds and its tests run on all three; CI covers each.
- **Linux without Compose:** the Bot API container writes files as its own user, so the bot must be allowed to read and move them from `LOCAL_API_ROOT` (for example run the bot as root or add its user to the right group). This is untested. The Compose setup runs the bot as root and avoids it.
- **Progress:** parallel downloads share one percentage, because the Bot API server's temp files cannot be matched to a single file.

</details>

## Troubleshooting

| Symptom | What to check |
|---|---|
| The bot never answers | `TELEGRAM_OWNER_ID` must be your numeric ID; check the log for `poll:` errors (an `Unauthorized` means a wrong token) |
| Files over 20 MB fail | `TELEGRAM_API_URL` must point at the local Bot API server, not `api.telegram.org` |
| Files stay "queued" | Open the Inngest dashboard; if it shows no function runs, restart the bot (it also restarts itself within about 5 minutes) |
| A failed download | Reply `/retry` to its status message |
| Saved file is not owned by you (Linux) | See the platform notes above |

## Security and privacy

- The bot token lives in `.env`, which is git-ignored. Never commit it. If it leaks, revoke it with @BotFather (`/revoke`).
- Only `TELEGRAM_OWNER_ID` is served; messages from anyone else are ignored.
- The Bot API server and Inngest listen on `127.0.0.1` only. The Inngest dev server has no login, so do not expose its port.
- To report a vulnerability, use the **Report a vulnerability** button under the Security tab (see [SECURITY.md](SECURITY.md)).

## Contributing

Open pull requests against `develop`; CI runs `gofmt`, `go vet`, `go build` and `go test` on Linux, macOS and Windows. A passing PR with no conflict merges itself. See [CONTRIBUTING.md](CONTRIBUTING.md). Releases are cut by tagging `main` with a version such as `v0.2.0`.

## License

MIT, see [LICENSE](LICENSE).
