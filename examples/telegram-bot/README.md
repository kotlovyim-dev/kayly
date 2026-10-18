# Telegram bot

An echo bot with no dependencies, deployed by Kayly as a webhook or a polling bot.

```sh
cd examples/telegram-bot
kayly deploy                        # creates the project, then stops: BOT_TOKEN has no value
kayly env set BOT_TOKEN=<token from @BotFather>
kayly deploy
```

Message the bot and it answers with your words.

What Kayly does for it ([ARCHITECTURE.md §6](../../ARCHITECTURE.md#telegram-bots-and-mini-apps)):

- Before a release starts, it checks the token with `getMe`. A wrong token fails the deploy, and the live bot keeps running.
- **Webhook** (`mode = "webhook"`): Kayly points the webhook at `https://bot.<project>.<server>.sslip.io/telegram`, or at the first custom domain (`kayly domains add <hostname> bot`), and delivers the secret as `TELEGRAM_WEBHOOK_SECRET`. The bot refuses any update that lacks it. Old and new containers overlap during a deploy, so no update is lost.
- **Polling** (`mode = "polling"`, no `path`): Kayly removes the webhook and stops the old container before the new one starts, because two `getUpdates` clients conflict. This is the only kind of deploy with a short gap.

The bot picks its mode by whether `TELEGRAM_WEBHOOK_SECRET` is set, so switching modes is a one-line change in `kayly.toml` and a deploy.
