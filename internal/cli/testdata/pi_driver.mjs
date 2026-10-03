// pi_driver.mjs — drives the generated pi extension (.pi/extensions/satelle.ts)
// against a stub of pi's ExtensionAPI (sty_b3c7b37d). It is a test harness, not
// part of the product: `go test` runs it under node, and fails when node is
// missing rather than skipping.
//
// usage: node --experimental-strip-types pi_driver.mjs <extension.ts> <steps.json>
//
// steps.json is an array of
//   { "event": "<pi event>", "arg": {...}, "ctx": { "cwd": "...", "idle": true, "hasUI": true } }
//   { "wait_messages": N, "timeout_ms": T }   (see below)
// and the driver prints one JSON document:
//   { "registered": [<pi event names in registration order>],
//     "results":    [<what each step's handlers returned>],
//     "messages":   [{ "text": "...", "opts": {...} }],
//     "notices":    [{ "msg": "...", "level": "..." }] }
import { copyFileSync, mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [extPath, stepsPath] = process.argv.slice(2);

// Node decides a .ts file's module type from its package scope; the repo under
// test has none. Load a private .mts copy so the format is never ambiguous.
const dir = mkdtempSync(join(tmpdir(), "pi-driver-"));
const copy = join(dir, "satelle.mts");
copyFileSync(extPath, copy);

const handlers = new Map();
const registered = [];
const messages = [];
const notices = [];

const pi = {
	on(name, fn) {
		registered.push(name);
		if (!handlers.has(name)) handlers.set(name, []);
		handlers.get(name).push(fn);
	},
	sendUserMessage(text, opts) {
		messages.push({ text, opts: opts ?? null });
	},
};

const mod = await import(pathToFileURL(copy).href);
mod.default(pi);

const steps = JSON.parse(readFileSync(stepsPath, "utf8")) ?? [];
const results = [];
for (const step of steps) {
	// { "wait_messages": N, "timeout_ms": T } waits — for work the extension left
	// running without awaiting, such as a gate waiter — until N messages have been
	// sent, or T ms pass.
	if (step.wait_messages !== undefined) {
		const deadline = Date.now() + (step.timeout_ms ?? 10000);
		while (messages.length < step.wait_messages && Date.now() < deadline) {
			await new Promise((r) => setTimeout(r, 50));
		}
		results.push({ event: "wait_messages", result: null, threw: null });
		continue;
	}
	const c = step.ctx ?? {};
	const ctx = {
		cwd: c.cwd,
		hasUI: c.hasUI ?? true,
		isIdle: () => c.idle ?? true,
		ui: { notify: (msg, level) => notices.push({ msg, level }) },
		sessionManager: { getSessionId: () => c.sessionId ?? "pi-session-1" },
	};
	let result = null;
	let threw = null;
	for (const fn of handlers.get(step.event) ?? []) {
		try {
			const r = await fn(step.arg ?? {}, ctx);
			if (r !== undefined) result = r;
		} catch (e) {
			threw = String(e);
		}
	}
	results.push({ event: step.event, result, threw });
}

process.stdout.write(JSON.stringify({ registered, results, messages, notices }));
