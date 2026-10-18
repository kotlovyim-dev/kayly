# Telegram Mini App

One service that serves a Mini App, tells the page who the user is, and takes the bot's webhook. It has no dependencies.

```sh
cd examples/telegram-mini-app
kayly deploy                        # creates the project, then stops: BOT_TOKEN has no value
kayly env set BOT_TOKEN=<token from @BotFather>
kayly deploy
```

Open the bot in Telegram and tap **Open** next to the message field. The page greets you by name.

- Kayly sets the bot's menu button to `${web.url}`, the service's own address. It follows a custom domain once you add one with `kayly domains add <hostname> web`.
- Telegram passes signed launch data (`initData`) to the page. `initdata.js` checks the signature with the bot token, which only the server knows. Everything the page says about the user comes from that check, never from the page itself. `npm test` covers it.
- Webhook updates arrive at `/telegram` with the secret Kayly delivers as `TELEGRAM_WEBHOOK_SECRET`.
