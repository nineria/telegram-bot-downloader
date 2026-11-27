# macdrop

A Telegram bot that saves every file you forward to it onto your computer, including files over 20 MB (up to 2 GB), with a queue, parallel downloads, retries and resume.

Forward files to the bot, and it replies with a live progress message per file (percent, speed, ETA) and saves them to `~/Downloads/telegram` (or `DOWNLOAD_DIR`) as `<message id>_<name>`.

## How it works
- The bot long-polls Telegram and turns each forwarded file into an [Inngest](https://www.inngest.com) event.
- An Inngest function downloads it: at most 2 at a time, up to 8 retries per file, and the queue survives restarts.
- Plain Telegram bots can only fetch 20 MB. A **local Bot API server** lifts that limit, so the bot asks it to download the file and then moves the result into place.
- If the Inngest connection stays down for over 2 minutes, the bot exits so a process supervisor (launchd, systemd, Docker restart policy) can restart it.

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
Export the variables from `.env` (for example `set -a; . ./.env; set +a`) before the `docker run` commands. The bot itself reads `.env` from the working directory.

## Usage
- Forward any files (photos, videos, documents, audio) to the bot. Only `TELEGRAM_OWNER_ID` is served; everyone else is ignored.
- Reply `/retry` to a failed file's status message to retry it, or send `/retry` alone to retry all failed files.
- `./status.sh` prints a one-line summary: done today, downloading, waiting, failed, free disk.
