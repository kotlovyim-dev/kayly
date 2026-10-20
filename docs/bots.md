# Telegram bots and Mini Apps

A service becomes a Telegram bot with one table in `kayly.toml`. Kayly checks its token, sets its webhook (or clears it for polling) and keeps its menu button pointed at your Mini App, so a deploy never leaves a bot talking to the wrong place.

## Webhook bot

```toml
[services.bot]
command = "node bot.js"
port = 8080

[services.bot.telegram]
token = "BOT_TOKEN"
mode = "webhook"
path = "/telegram"

[env]
BOT_TOKEN = { to = ["bot"] }
```

```sh
kayly env set BOT_TOKEN=123456:ABC…
kayly deploy
```

- Before anything live changes, Kayly calls `getMe` with the new release's token. A wrong or revoked token fails the release, and the live bot keeps running.
- Kayly generates `TELEGRAM_WEBHOOK_SECRET` once and delivers it to the bot. Check it against the `X-Telegram-Bot-Api-Secret-Token` header of every update.
- After the switch, Kayly sets the webhook to `https://<first custom domain, else the sslip.io address><path>` only if Telegram reports something else. The URL stays the same across releases, so an ordinary deploy does not touch Telegram, and updates during the switch reach whichever release the proxy routes to.

## Polling bot

```toml
[services.bot.telegram]
token = "BOT_TOKEN"
mode = "polling"
```

A polling bot needs no port. Two `getUpdates` clients with one token conflict, so the old container stops before the new one starts (a gap of a few seconds). Kayly deletes any webhook first, keeping pending updates, so a bot moved from webhook to polling works at once.

## Mini App

```toml
[services.web]
command = "node server.js"
port = 3000

[services.bot.telegram]
token = "BOT_TOKEN"
mode = "webhook"
path = "/telegram"
menu_button = { text = "Open", url = "${web.url}" }
```

After the switch Kayly sets the chat's menu button to open the `web` service. Validating `initData` is the app's job: every Telegram library ships it. Kayly ships no templates; the repository has two working examples: [a bot](../examples/telegram-bot) and [a Mini App](../examples/telegram-mini-app).

## When Telegram does not answer

A Telegram call after the switch that fails leaves the release live (the app is fine; only Telegram's view of it is stale), sends an alert, and is retried by the reconciler every 30 seconds. A rollback applies the old release's `telegram` table under the same rules.
