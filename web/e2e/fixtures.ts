// The e2e harness: `mise run e2e` starts a world of its own (see e2e() in deploy/dev.sh) and passes
// it in E2E_* variables. Every test gets its own users, so tests run in parallel and never see each
// other's threads, settings or volumes.
import { test as base, expect, type Browser, type BrowserContext, type BrowserContextOptions, type Page } from '@playwright/test';
import { create, toJsonString, type MessageInitShape } from '@bufbuild/protobuf';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import { createClient, type Interceptor } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { execFile, spawn, type ChildProcess } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { openSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import {
	CreateThreadRequestSchema,
	EventSchema,
	MessageSchema,
	SettingsService,
	Status,
	ThreadService,
	UserService,
	type Event,
	type Thread as ThreadInfo
} from '../src/lib/gen/banda/v1/banda_pb.js';

export { expect };
export { Status };
export const API = env('E2E_API');
export const OIDC_API = env('E2E_OIDC_API');
export const NAMESPACE = env('E2E_NAMESPACE');
export const DB = env('E2E_DB');
export const API_LOG = env('E2E_LOG');
const DIR = env('E2E_DIR');
/** The host as sandboxes see it, where the run's APIs listen (host_ip in deploy/dev.sh). */
export const HOST = env('E2E_HOST');

function env(name: string): string {
	const v = process.env[name];
	if (!v) throw new Error(`${name} is not set: run the suite with \`mise run e2e\``);
	return v;
}

// The run's git server (see deploy/dev.sh) serves the fixture repos from deploy/sandbox.Dockerfile:
// plain, setup-fails, setup-bg, setup-hold, profiles (it overrides the spec profile's text) and readonly
// (plain, refusing pushes).
export const repo = (name = 'plain') => `git://e2e-git/${name}.git`;

const run = (cmd: string, args: string[], input?: string) =>
	new Promise<string>((resolve, reject) => {
		const p = execFile(cmd, args, { maxBuffer: 64 << 20 }, (err, stdout, stderr) =>
			err ? reject(new Error(`${cmd} ${args.join(' ')}: ${stderr || err.message}`)) : resolve(stdout)
		);
		if (input !== undefined) p.stdin!.end(input);
	});

/** kubectl in the namespace. Only for facts no API exposes. */
export const kubeIn = (ns: string, ...args: string[]) => run('kubectl', ['--context', 'kind-banda', '-n', ns, ...args]);
/** kubectl in the run's namespace. */
export const kube = (...args: string[]) => kubeIn(NAMESPACE, ...args);
/** Runs a shell command in a pod of the run's namespace, or of ns. */
export const kexec = (pod: string, command: string, ns = NAMESPACE) => kubeIn(ns, 'exec', pod, '--', 'sh', '-c', command);
/** Whether the object exists in the run's namespace. */
export const exists = (kind: string, name: string) =>
	kube('get', kind, name, '--ignore-not-found', '-o', 'name').then((out) => out.trim() !== '');
/** A query on the run's database, rows as lines of |-separated values. Only for facts no API exposes. */
export const sql = (query: string, db = DB) =>
	run('kubectl', ['--context', 'kind-banda', '-n', 'banda', 'exec', 'postgres-0', '--', 'psql', '-U', 'banda', '-d', db, '-tAc', query]).then((s) => s.trim());

/** The name suffix of the sandbox, volume and secrets of a user's own objects (see internal/sandbox). */
export const userHash = (id: string) => createHash('sha256').update(id).digest('hex').slice(0, 16);

/** Polls fn until it returns something truthy; fails with what() if it doesn't within timeout. */
export async function until<T>(fn: () => Promise<T | undefined>, what: () => string | Promise<string>, timeout = 60_000): Promise<T> {
	const end = Date.now() + timeout;
	for (;;) {
		const v = await fn().catch(() => undefined);
		if (v) return v;
		if (Date.now() > end) throw new Error(`timed out after ${timeout / 1000}s: ${await what()}`);
		await new Promise((r) => setTimeout(r, 500));
	}
}

const json = (v: unknown) => JSON.stringify(v, (_, x) => (typeof x === 'bigint' ? x.toString() : x))?.slice(0, 1000);

/** Reads a server stream in the background: got holds what it sent so far. */
export function listen<T>(open: (signal: AbortSignal) => AsyncIterable<T>) {
	const ac = new AbortController();
	const got: T[] = [];
	let error: unknown;
	void (async () => {
		try {
			for await (const m of open(ac.signal)) got.push(m);
		} catch (e) {
			if (!ac.signal.aborted) error = e;
		}
	})();
	return {
		got,
		stop: () => ac.abort(),
		/** The first message so far or to come that matches ok. */
		until: (ok: (m: T) => unknown, what: string, timeout = 30_000) =>
			until(
				async () => got.find(ok),
				() => `${what}: ${got.length} messages, the last ${json(got.at(-1))}${error ? `, then ${error}` : ''}`,
				timeout
			)
	};
}

/** Everything a server stream sends within ms, or its error. */
export async function collect<T>(open: (signal: AbortSignal) => AsyncIterable<T>, ms = 4000): Promise<T[]> {
	const got: T[] = [];
	const signal = AbortSignal.timeout(ms);
	try {
		for await (const m of open(signal)) got.push(m);
	} catch (e) {
		if (!signal.aborted) throw e;
	}
	return got;
}

/** One line per event, for failure messages. */
export const describe = (events: Event[]) =>
	events.map((e) => toJsonString(EventSchema, e).slice(0, 300)).join('\n');

export type User = ReturnType<typeof newUser>;

function newUser(id: string, browser: () => Promise<Browser>, api = API) {
	const as: Interceptor = (next) => async (req) => {
		req.header.set('X-Banda-User', id);
		try {
			return await next(req);
		} catch (e) {
			// Say which call failed, and why underneath (a bare "[unknown]" is a network error).
			if (e instanceof Error) e.message += ` (${req.method.name} as ${id}${e.cause ? `: ${e.cause}` : ''})`;
			throw e;
		}
	};
	const transport = createConnectTransport({ baseUrl: api, interceptors: [as] });
	let context: Promise<BrowserContext> | undefined;
	const user = {
		id,
		api,
		threads: createClient(ThreadService, transport),
		settings: createClient(SettingsService, transport),
		users: createClient(UserService, transport),
		/** Starts a thread on the plain repo unless told otherwise. */
		async create(req: MessageInitShape<typeof CreateThreadRequestSchema> = {}) {
			const r = await user.threads.createThread({ repoUrl: repo(), baseBranch: 'main', prompt: 'e2e', ...req });
			return user.thread(r.thread!.id);
		},
		/** A thread as this user sees it. */
		thread: (threadId: string) => new Thread(user, threadId),
		/** POST /files: the response, its JSON is the attachment. */
		upload: (name: string, type: string, body: Uint8Array | string) =>
			fetch(`${api}/files?name=${encodeURIComponent(name)}`, {
				method: 'POST',
				// No type: none at all, so the API goes by the name.
				headers: { ...(type && { 'Content-Type': type }), 'X-Banda-User': id },
				body: typeof body === 'string' ? new TextEncoder().encode(body) : new Uint8Array(body)
			}),
		/** Uploads and returns the attachment id. */
		async attach(name: string, type: string, body: Uint8Array | string) {
			const r = await user.upload(name, type, body);
			expect(r.status, await r.clone().text()).toBe(200);
			return ((await r.json()) as { id: string }).id;
		},
		/**
		 * A browser tab signed in as this user (dev auth). Tabs share one context per user (its
		 * page.context().setOffline() cuts them all off); options, e.g. devices['iPhone 13'], get a context of their own.
		 */
		async page(options?: BrowserContextOptions): Promise<Page> {
			const open = () => browser().then((b) => b.newContext({ ...options, baseURL: api, extraHTTPHeaders: { 'X-Banda-User': id } }));
			if (options) return (await open()).newPage();
			context ??= open();
			return (await context).newPage();
		},
		/** Collects a terminal's output until it exits (with a command) or timeout. */
		async terminal(open: (signal: AbortSignal) => AsyncIterable<{ data: Uint8Array; exited: boolean }>, timeout = 60_000) {
			const signal = AbortSignal.timeout(timeout);
			let out = '';
			try {
				for await (const r of open(signal)) {
					out += Buffer.from(r.data).toString();
					if (r.exited) break;
				}
			} catch (e) {
				if (!signal.aborted) throw e;
			}
			return out;
		}
	};
	return user;
}

/** A thread as one user sees it. */
export class Thread {
	/** Everything WatchThread sent since watch(). */
	readonly seen: Event[] = [];
	private watching?: AbortController;
	private woken: (() => void)[] = [];

	constructor(
		readonly user: User,
		readonly id: string
	) {}

	/** The same thread as another user sees it. */
	as(user: User) {
		return user.thread(this.id);
	}

	async get(): Promise<ThreadInfo> {
		return (await this.user.threads.getThread({ threadId: this.id })).thread!;
	}

	/** Polls GetThread until ok; the failure says what the thread was doing. */
	async waitFor(what: string, ok: (t: ThreadInfo) => boolean, timeout = 90_000): Promise<ThreadInfo> {
		let last: ThreadInfo | undefined;
		return until(
			async () => {
				last = await this.get();
				return ok(last) ? last : undefined;
			},
			async () => `thread ${this.id} ${what}; it is ${Status[last?.status ?? 0]}, its last events:\n${describe((await this.events()).slice(-15))}`,
			timeout
		);
	}

	waitForStatus(status: Status, timeout?: number) {
		return this.waitFor(`to be ${Status[status]}`, (t) => t.status === status, timeout);
	}

	/** Waits until the thread's agent finished a turn that started after this call. */
	async turn(send: () => Promise<unknown>, timeout?: number) {
		const before = await this.turns();
		await send();
		await until(
			async () => (await this.turns()) > before,
			async () => `no turn ended in thread ${this.id}:\n${describe((await this.events()).slice(-15))}`,
			timeout ?? 90_000
		);
		return this.waitForStatus(Status.AWAITING_INPUT);
	}

	private async turns() {
		return (await this.events()).filter((e) => e.kind.case === 'turnEnded').length;
	}

	/** Subscribes to WatchThread (stored events first, then live ones, ephemeral ones included) into seen. */
	watch() {
		if (this.watching) return;
		this.watching = new AbortController();
		const signal = this.watching.signal;
		void (async () => {
			try {
				for await (const r of this.user.threads.watchThread({ threadId: this.id }, { signal })) {
					if (!r.event) continue; // a keepalive
					this.seen.push(r.event);
					this.woken.splice(0).forEach((w) => w());
				}
			} catch {
				// aborted, or the API went away: waitForEvent times out with what it saw
			}
		})();
	}

	unwatch() {
		this.watching?.abort();
		this.watching = undefined;
	}

	/** Waits for a watched event (stored or ephemeral) matching ok; starts watching if needed. */
	async waitForEvent(ok: (e: Event) => boolean, what = 'the event', timeout = 60_000): Promise<Event> {
		this.watch();
		const end = Date.now() + timeout;
		for (;;) {
			const e = this.seen.find(ok);
			if (e) return e;
			if (Date.now() > end)
				throw new Error(`thread ${this.id}: no ${what} within ${timeout / 1000}s; saw:\n${describe(this.seen.slice(-15))}`);
			await new Promise<void>((r) => {
				this.woken.push(r);
				setTimeout(r, 500);
			});
		}
	}

	/** All stored events, in full (History). */
	async events(): Promise<Event[]> {
		const all: Event[] = [];
		let before = BigInt(Number.MAX_SAFE_INTEGER);
		for (;;) {
			const { events } = await this.user.threads.history({ threadId: this.id, beforeSeq: before, limit: 1000, full: true });
			all.unshift(...events);
			if (events.length < 1000) return all;
			before = events[0].seq;
		}
	}

	/** The stored events of one kind, e.g. of('toolCall'). */
	async of<K extends Event['kind']['case']>(kind: K) {
		return (await this.events()).filter((e) => e.kind.case === kind).map((e) => e.kind.value as Extract<Event['kind'], { case: K }>['value']);
	}

	/** The agent's latest text (the fake names its model and effort in it). */
	async reply() {
		return (await this.of('agentText')).at(-1)?.text ?? '';
	}

	send(text: string, message: MessageInitShape<typeof MessageSchema> = {}) {
		return this.user.threads.sendMessage({ threadId: this.id, message: create(MessageSchema, { text, ...message }) });
	}

	/** Snoozes the thread for good, which suspends its environment. */
	snooze() {
		return this.user.threads.snooze({ threadId: this.id, until: timestampFromDate(new Date('2099-01-01')) });
	}

	/** Waits until the thread's pod is gone (suspended or deleted). */
	podGone(timeout = 90_000) {
		return until(async () => !(await exists('pod', this.pod)), () => `pod ${this.pod} is still there`, timeout);
	}

	/** Runs command in the thread's terminal and returns its output. */
	terminal(command: string, timeout?: number) {
		return this.user.terminal((signal) => this.user.threads.openTerminal({ threadId: this.id, cols: 80, rows: 24, command }, { signal }), timeout);
	}

	/** Ends an e2e-hold:<name> of the fake agent (see deploy/fake-claude). */
	release(name: string) {
		return kexec(this.pod, `mkdir -p ~/e2e-release && touch ~/e2e-release/${name}`);
	}

	get pod() {
		return `t-${this.id}`;
	}

	/** What reached the agent with e2e-echo (see deploy/fake-claude), oldest first. */
	async prompts() {
		return (await this.of('agentText')).filter((a) => a.text.startsWith('Received: ')).map((a) => a.text.slice(10));
	}

	/** Kills the runner (the container's PID 1): the container restarts and reconnects. */
	killRunner() {
		return kexec(this.pod, 'kill 1');
	}

	/** Deletes the pod: the controller recreates it on the same volumes. */
	deletePod() {
		return kube('delete', 'pod', this.pod, '--wait=false');
	}

	/** Holds pushes of the thread's branch at the git server until releasePush (deploy/e2e-pre-receive). */
	async holdPush() {
		await kexec('e2e-git', `mkdir -p /tmp/git-hold && touch /tmp/git-hold/${await this.holdName()}`);
	}

	async releasePush() {
		await kexec('e2e-git', `rm -f /tmp/git-hold/${await this.holdName()}`);
	}

	private async holdName() {
		return (await this.get()).branch.replaceAll('/', '_');
	}

	/** Runs git on origin, the git server's copy of the repo (default plain). */
	origin(args: string, repoName = 'plain') {
		return kexec('e2e-git', `git --git-dir=/tmp/git/${repoName}.git ${args}`);
	}

	/** Calls a tool of the Banda MCP server from the sandbox, with the thread's runner token, as its agent does. */
	mcp(tool: string, args: object) {
		const body = JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: tool, arguments: args } });
		return this.mcpRaw(body);
	}

	mcpRaw(body: string) {
		return kexec(
			this.pod,
			`curl -sf -X POST "$BANDA_API_URL/mcp" -H "Authorization: Bearer $BANDA_RUNNER_TOKEN" -H "X-Banda-Thread: $BANDA_THREAD_ID" ` +
				`-H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d '${body.replaceAll("'", "'\\''")}'`
		);
	}
}

/** A directive for the fake agent that calls a Banda MCP tool (see deploy/fake-claude). */
export const mcpDirective = (tool: string, args: object) => `e2e-mcp:${tool}:${Buffer.from(JSON.stringify(args)).toString('base64')}`;

/** Expects the call to fail (a refused RPC); returns the error message. */
export async function refused(call: Promise<unknown>): Promise<string> {
	try {
		await call;
	} catch (e) {
		return String(e);
	}
	throw new Error('the call succeeded but should have been refused');
}

/** Bytes of a 1x1 PNG. */
export const png = Buffer.from(
	'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==',
	'base64'
);

export const freePort = () =>
	new Promise<number>((resolve) => {
		const s = createServer().listen(0, () => {
			const { port } = s.address() as { port: number };
			s.close(() => resolve(port));
		});
	});

/**
 * An API process of a test's own (see startApi), on the run's namespace, with extra env, e.g. { IDLE_SUSPEND_AFTER: '5s' }.
 * Stop and start it mid-test; the run's shared API serves the other tests and is never restarted.
 */
export class Api {
	url = '';
	log = '';
	private proc?: ChildProcess;

	constructor(
		private env: Record<string, string>,
		readonly db = DB
	) {}

	async start() {
		const port = Number(new URL(this.url || 'http://x').port) || (await freePort());
		await run('../deploy/dev.sh', ['host-api', this.namespace, String(port)]); // its sandboxes may reach it (the chart's policies)
		this.url = `http://localhost:${port}`;
		this.log = `${DIR}/api-${port}.log`;
		const out = openSync(this.log, 'a');
		this.proc = spawn(`${DIR}/api`, {
			cwd: '..', // WEB_DIR is relative to the repo
			env: {
				...process.env,
				DEV_AUTH: '1',
				ADDR: `:${port}`,
				PREVIEW_ADDR: `:${await freePort()}`,
				RUNNER_API_URL: `http://${HOST}:${port}`,
				...this.env
			},
			stdio: ['ignore', out, out]
		});
		const exited = new Promise((r) => this.proc!.once('exit', r));
		await until(
			() => Promise.race([fetch(this.url).then(() => true), exited.then(() => Promise.reject(new Error('exited')))]),
			async () => `the API on ${this.url} did not start:\n${(await readFile(this.log, 'utf8')).slice(-2000)}`,
			30_000
		);
	}

	/** Where its sandboxes run. */
	get namespace() {
		return this.env.SANDBOX_NAMESPACE ?? NAMESPACE;
	}

	async stop() {
		const p = this.proc;
		if (!p || p.exitCode !== null) return;
		const exited = new Promise((r) => p.once('exit', r));
		p.kill();
		await exited;
	}

	async restart() {
		await this.stop();
		await this.start();
	}
}

type Fixtures = {
	/** as('alice') is alice-<random> in this test only: tests never share users. Pass an Api to call that one. */
	as: (name: string, api?: Api) => User;
	/**
	 * Starts an API of this test's own (see Api) on a fresh database, so neither its schema nor its sweeps touch
	 * other tests' threads; sharedDatabase puts it on the run's. query is added to its DATABASE_URL, e.g. pool_max_conns=1.
	 * namespace gives it a fresh namespace with the sandbox policies, for settings that apply to a whole namespace
	 * (SANDBOX_EGRESS_ALLOW); its threads clone file:///opt/e2e/<repo>.git there, as e2e-git is out of reach.
	 */
	startApi: (env?: Record<string, string>, opts?: { sharedDatabase?: boolean; query?: string; namespace?: boolean }) => Promise<Api>;
};

export const test = base.extend<Fixtures>({
	startApi: async ({}, use) => {
		const apis: Api[] = [];
		const dbs: string[] = [];
		const namespaces: string[] = [];
		await use(async (env = {}, opts = {}) => {
			let db = DB;
			if (!opts.sharedDatabase) {
				db = `${DB}_${randomBytes(3).toString('hex')}`;
				await sql(`CREATE DATABASE ${db}`);
				dbs.push(db);
			}
			const url = `${process.env.DATABASE_URL!.replace(/\/[^/?]*(\?.*)?$/, `/${db}`)}${opts.query ? `?${opts.query}` : ''}`;
			env = { DATABASE_URL: url, ...env };
			if (opts.namespace) {
				const ns = `${NAMESPACE}-${randomBytes(3).toString('hex')}`;
				namespaces.push(ns);
				await kubeIn(ns, 'create', 'namespace', ns);
				await kubeIn(ns, 'label', 'namespace', ns, `banda.dev/e2e-run=${NAMESPACE}`); // deploy/dev.sh deletes leftovers
				await run('../deploy/dev.sh', ['policies', ns]);
				env = { SANDBOX_NAMESPACE: ns, ...env };
			}
			const api = new Api(env, db);
			apis.push(api);
			await api.start();
			return api;
		});
		await Promise.all(apis.map((a) => a.stop()));
		for (const db of dbs) await sql(`DROP DATABASE ${db} WITH (FORCE)`);
		for (const ns of namespaces) await kubeIn(ns, 'delete', 'namespace', ns, '--wait=false');
	},
	// Depends on startApi, so it tears down first, while the test's own APIs still run.
	as: async ({ browser, startApi: _ }, use, testInfo) => {
		const suffix = randomBytes(3).toString('hex');
		const users = new Map<string, { user: User; api?: Api }>();
		await use((name, api) => {
			const key = `${name} ${api?.url ?? API}`;
			if (!users.has(key)) users.set(key, { user: newUser(`${name}-${suffix}`, async () => browser, api?.url), api });
			return users.get(key)!.user;
		});
		const ids = [...new Set([...users.values()].map((u) => u.user.id))];
		const owners = ids.map((i) => `'${i}'`).join(',');
		const sandboxes = ids.map((id) => `login-${userHash(id)}`);
		// Per API (a test's own may have its own database): its threads, and their last events on failure.
		for (const { user, api } of new Map([...users.values()].map((u) => [u.api?.url ?? API, u])).values()) {
			const threads = (await sql(`select id from threads where owner_id in (${owners})`, api?.db).catch(() => '')).split('\n').filter(Boolean);
			sandboxes.push(...threads.map((id) => `t-${id}`));
			if (testInfo.status === testInfo.expectedStatus) continue;
			const logFile = api?.log ?? API_LOG;
			const log = await readFile(logFile, 'utf8').catch(() => '');
			for (const id of threads) {
				const t = user.thread(id);
				const info = await t.get().catch(() => undefined);
				const events = await t.events().catch(() => []);
				await testInfo.attach(`thread ${id}`, {
					body: `${Status[info?.status ?? 0]} (owner ${info?.ownerId})\n${describe(events.slice(-40))}\n\nAPI log (${logFile}):\n` +
						log.split('\n').filter((l) => l.includes(id)).slice(-40).join('\n')
				});
			}
		}
		// Bound the pods on the shared kind node: the run's namespace goes at the end anyway.
		if (sandboxes.length) await kube('delete', 'sandbox', ...sandboxes, '--ignore-not-found', '--wait=false');
	}
});
