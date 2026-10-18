// A Mini App for Kayly, with no dependencies. One service serves the page, tells it who the
// user is once Telegram's signature checks out, and takes the bot's webhook. Kayly points the
// bot's menu button at this service, so the page opens from the chat.
import { timingSafeEqual } from "node:crypto";
import { readFileSync } from "node:fs";
import { createServer } from "node:http";
import { verify } from "./initdata.js";

const token = process.env.BOT_TOKEN;
const secret = Buffer.from(process.env.TELEGRAM_WEBHOOK_SECRET ?? "");
const page = readFileSync(new URL("index.html", import.meta.url));

async function readBody(req) {
	let body = "";
	for await (const chunk of req) body += chunk;
	return body;
}

async function handle(update) {
	const msg = update.message;
	if (!msg) return;
	const res = await fetch(`https://api.telegram.org/bot${token}/sendMessage`, {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify({ chat_id: msg.chat.id, text: "Tap Open below to start the app." }),
	});
	const r = await res.json();
	if (!r.ok) throw new Error(`sendMessage: ${r.description}`);
}

createServer(async (req, res) => {
	if (req.method === "GET" && req.url === "/health") return res.end("ok");
	if (req.method === "GET" && req.url === "/") {
		res.setHeader("content-type", "text/html; charset=utf-8");
		return res.end(page);
	}
	if (req.method === "POST" && req.url === "/api/me") {
		const user = verify(await readBody(req), token);
		res.statusCode = user ? 200 : 401;
		res.setHeader("content-type", "application/json");
		return res.end(JSON.stringify(user ?? { error: "not opened from Telegram" }));
	}
	if (req.method === "POST" && req.url === "/telegram") {
		const got = Buffer.from(req.headers["x-telegram-bot-api-secret-token"] ?? "");
		if (secret.length === 0 || got.length !== secret.length || !timingSafeEqual(got, secret)) {
			res.statusCode = 401;
			return res.end();
		}
		const update = JSON.parse(await readBody(req));
		res.end(); // answer at once: Telegram resends updates a webhook is slow to take
		return handle(update).catch(console.error);
	}
	res.statusCode = 404;
	res.end();
}).listen(3000);
