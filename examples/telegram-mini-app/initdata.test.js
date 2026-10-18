import assert from "node:assert/strict";
import { test } from "node:test";
import { sign, verify } from "./initdata.js";

const token = "123456:test-token";
const now = 1_800_000_000;

// initData as Telegram sends it: URL-encoded fields, signed over "key=value" lines in key order.
function initData(fields, botToken = token) {
	const check = Object.entries(fields)
		.map(([k, v]) => `${k}=${v}`)
		.sort()
		.join("\n");
	return new URLSearchParams({ ...fields, hash: sign(check, botToken) }).toString();
}

const user = { id: 42, first_name: "Ada" };
const fields = { auth_date: String(now - 60), query_id: "AAE", user: JSON.stringify(user) };

test("a fresh, signed initData yields its user", () => {
	assert.deepEqual(verify(initData(fields), token, 86400, now), user);
});

test("anything else yields null", () => {
	const tampered = initData(fields).replace("Ada", "Eve");
	assert.equal(verify(tampered, token, 86400, now), null);
	assert.equal(verify(initData(fields, "other:token"), token, 86400, now), null);
	assert.equal(verify(initData({ ...fields, auth_date: String(now - 90000) }), token, 86400, now), null);
	assert.equal(verify(initData({ user: fields.user }), token, 86400, now), null); // no auth_date
	assert.equal(verify("", token, 86400, now), null);
});
