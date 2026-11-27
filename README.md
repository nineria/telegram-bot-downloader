# macdrop

A Telegram bot that saves every file you forward to it onto your computer, including files over 20 MB (up to 2 GB), with a queue, parallel downloads, retries and resume.

Forward files to the bot, and it replies with a live progress message per file (percent, speed, ETA) and saves them to `~/Downloads/telegram` (or `DOWNLOAD_DIR`) as `<message id>_<name>`.

## How it works
- The bot long-polls Telegram and turns each forwarded file into an [Inngest](https://www.inngest.com) event.
- An Inngest function downloads it: at most 2 at a time, up to 8 retries per file, and the queue survives restarts.
- Plain Telegram bots can only fetch 20 MB. A **local Bot API server** lifts that limit, so the bot asks it to download the file and then moves the result into place.
- If the Inngest connection stays down for over 2 minutes, the bot exits so a process supervisor (launchd, systemd, Docker restart policy) can restart it.
- A health check sends a ping event every minute. If none is handled for 5 minutes (the connection looks up but Inngest runs nothing), the bot exits for a restart too.

## Requirements
- Go (see `go.mod`)
- Docker (for the Bot API server and the Inngest dev server)
- A bot token from [@BotFather](https://t.me/BotFather) and an API ID/hash from <https://my.telegram.org>

## Setup
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
`LOCAL_API_ROOT` must be the host folder you mount at `/var/lib/telegram-bot-api` (the bot maps the server's file paths from that fixed location). `DOWNLOAD_DIR` can be on another disk.

**Windows (PowerShell):** set the variables with `$env:TELEGRAM_API_ID="..."`, use a Windows path such as `C:\tg-data` for `LOCAL_API_ROOT`, and write the volume as `-v "${env:LOCAL_API_ROOT}:/var/lib/telegram-bot-api"`. Build with `go build -o macdrop.exe .` and run `.\macdrop.exe`.

Export the variables from `.env` (for example `set -a; . ./.env; set +a`) before the `docker run` commands. The bot itself reads `.env` from the working directory.

## Configuration
All settings are environment variables, read from the process or from `.env` (see `.env.example`).

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `TELEGRAM_BOT_TOKEN` | yes | | Bot token from @BotFather |
| `TELEGRAM_OWNER_ID` | yes | | Only this user can use the bot |
| `TELEGRAM_API_URL` | yes | | Local Bot API server, e.g. `http://127.0.0.1:8081` |
| `LOCAL_API_ROOT` | yes | | Host folder mounted at `/var/lib/telegram-bot-api` |
| `INNGEST_DEV`, `INNGEST_BASE_URL` | yes | | Inngest dev server, e.g. `1` and `http://127.0.0.1:8288` |
| `DOWNLOAD_DIR` | no | `~/Downloads/telegram` | Where files are saved |
| `PARALLEL_DOWNLOADS` | no | `2` | Files downloaded at the same time |
| `MAX_RETRIES` | no | `8` | Retries per file before it is marked failed |

## Platforms
- **macOS, Windows, Linux:** the Go code builds and its tests run on all three (CI covers Linux, macOS and Windows). File names are cleaned of characters Windows forbids (`: ? * " < > | \`), so a name may differ slightly from the original.
- **Linux:** the Bot API container writes files as its own user, so the bot must be allowed to read and move them from `LOCAL_API_ROOT` (for example run the bot as root or add its user to the right group). This is untested.
- `status.sh` needs `sh` and `python3`, so it works on macOS and Linux only. On Windows read `macdrop.log`.

## Usage
- Forward any files (photos, videos, documents, audio) to the bot. Only `TELEGRAM_OWNER_ID` is served; everyone else is ignored.
- Reply `/retry` to a failed file's status message to retry it, or send `/retry` alone to retry all failed files.
- `./status.sh` prints a one-line summary: done today, downloading, waiting, failed, free disk.

## License
MIT, see [LICENSE](LICENSE).
