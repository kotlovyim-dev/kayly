// An echo bot for Kayly, with no dependencies. Kayly delivers TELEGRAM_WEBHOOK_SECRET only
// to webhook bots, so its presence picks the mode: answer Telegram's POSTs, or poll.
import { timingSafeEqual } from "node:crypto";
import { createServer } from "node:http";
import { setTimeout as sleep } from "node:timers/promises";

const token = process.env.BOT_TOKEN;
const secret = process.env.TELEGRAM_WEBHOOK_SECRET;

async function api(method, body) {
	const res = await fetch(`https://api.telegram.org/bot${token}/${method}`, {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify(body),
	});
	const r = await res.json();
	if (!r.ok) throw new Error(`${method}: ${r.description}`);
	return r.result;
}

async function handle(update) {
	const msg = update.message;
	if (msg?.text) await api("sendMessage", { chat_id: msg.chat.id, text: msg.text });
}

function fromTelegram(req) {
	const got = Buffer.from(req.headers["x-telegram-bot-api-secret-token"] ?? "");
	const want = Buffer.from(secret);
	return got.length === want.length && timingSafeEqual(got, want);
}

// Both modes serve /health, which Kayly checks before it switches to a new release.
createServer(async (req, res) => {
	if (req.method === "GET" && req.url === "/health") return res.end("ok");
	if (secret && req.method === "POST" && req.url === "/telegram") {
		if (!fromTelegram(req)) {
			res.statusCode = 401;
			return res.end();
		}
		let body = "";
		for await (const chunk of req) body += chunk;
		res.end(); // answer at once: Telegram resends updates a webhook is slow to take
		return handle(JSON.parse(body)).catch(console.error);
	}
	res.statusCode = 404;
	res.end();
}).listen(8080);

if (!secret) {
	// Kayly removed the webhook before this container started, so getUpdates works at once.
	let offset = 0;
	for (;;) {
		try {
			for (const update of await api("getUpdates", { offset, timeout: 50 })) {
				offset = update.update_id + 1;
				await handle(update).catch(console.error);
			}
		} catch (err) {
			console.error(err.message);
			await sleep(5000);
		}
	}
}
