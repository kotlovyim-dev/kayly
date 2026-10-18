import { createHmac, timingSafeEqual } from "node:crypto";

// verify returns the user in a Mini App's initData if the bot's token signed it within maxAge
// seconds, else null. Only a user this returns is one Telegram vouches for.
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
export function verify(initData, botToken, maxAge = 86400, now = Date.now() / 1000) {
	const params = new URLSearchParams(initData);
	const hash = Buffer.from(params.get("hash") ?? "");
	params.delete("hash");
	const check = [...params].map(([k, v]) => `${k}=${v}`).sort().join("\n");
	const want = Buffer.from(sign(check, botToken));
	if (hash.length !== want.length || !timingSafeEqual(hash, want)) return null;
	if (!(now - Number(params.get("auth_date")) < maxAge)) return null;
	return JSON.parse(params.get("user") ?? "null");
}

export function sign(dataCheckString, botToken) {
	const key = createHmac("sha256", "WebAppData").update(botToken).digest();
	return createHmac("sha256", key).update(dataCheckString).digest("hex");
}
