// What a sandbox can't reach or take over (round-4 security audit, F1, F2, F4, F6, F7, F9), and the
// browser-facing defences of open risk 6 (F8). Sandboxes run untrusted code: a malicious repo or a
// prompt-injected agent. Probes run in the pods, as that code would.
import { LoginAction } from '../src/lib/gen/banda/v1/banda_pb.js';
import { API, expect, HOST, kexec, kube, kubeIn, NAMESPACE, refused, repo, sql, Status, test, userHash, type Thread, type User } from './fixtures.ts';

/** Whether a TCP connection from the pod (in the run's namespace, or ns) to host:port opens within 3 s. */
const reaches = async (pod: string, host: string, port: number, ns?: string) =>
	(await kexec(pod, `timeout 3 bash -c '</dev/tcp/${host}/${port}' 2>/dev/null && echo open || echo closed`, ns)).trim() === 'open';

const loginTerminal = (u: User, action: LoginAction) =>
	u.terminal((signal) => u.settings.openLoginTerminal({ harness: 'claude', action, cols: 80, rows: 24 }, { signal }));

const env = (pod: string, name: string) => kexec(pod, `printf %s "$${name}"`);

/** POST /mcp tools/list as thread id with token: the HTTP status. */
const mcpStatus = async (api: string, threadId: string, token: string) =>
	(
		await fetch(`${api}/mcp`, {
			method: 'POST',
			headers: { Authorization: `Bearer ${token}`, 'X-Banda-Thread': threadId, 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream' },
			body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/list' })
		})
	).status;

test.describe('network', () => {
	test('F1, F4: a sandbox reaches the API, the git server and the internet, and nothing else in the cluster or on the host', async ({ as }) => {
		const alice = as('alice');
		const [a, b] = [await alice.create(), await as('bob').create()];
		await a.waitForStatus(Status.AWAITING_INPUT);
		await b.waitForStatus(Status.AWAITING_INPUT);
		await b.turn(() => b.send('start the preview'));
		await expect.poll(() => kexec(b.pod, 'curl -s localhost:5173/'), { timeout: 30_000 }).toContain('e2e-preview-page');
		const bIP = await kube('get', 'pod', b.pod, '-o', 'jsonpath={.status.podIP}');
		const node = await kube('get', 'nodes', '-o', 'jsonpath={.items[0].status.addresses[0].address}');
		const kubeAPI = new URL(await kube('config', 'view', '--minify', '-o', 'jsonpath={.clusters[0].cluster.server}')).port;

		expect(await kexec(a.pod, 'curl -s -o /dev/null -w %{http_code} "$BANDA_API_URL/"')).toBe('200');
		expect(await reaches(a.pod, 'e2e-git', 9418)).toBe(true);
		expect(await reaches(a.pod, 'registry.npmjs.org', 443)).toBe(true);
		const closed = {
			'Postgres': ['postgres.banda.svc.cluster.local', 5432],
			'Postgres through its NodePort': [node, 30432],
			'Postgres through the host': [HOST, 5432],
			'the Kubernetes API': ['kubernetes.default.svc', 443],
			'the Kubernetes API through the host': [HOST, Number(kubeAPI)],
			'the kubelet': [node, 10250],
			'Dex': ['dex', 5556],
			'another thread’s dev server': [bIP, 5173]
		} as const;
		for (const [what, [host, port]] of Object.entries(closed)) expect(await reaches(a.pod, host, port), what).toBe(false);
	});

	test('F1: Postgres accepts connections only from the API, not from other pods', async () => {
		expect(await reaches('e2e-git', 'postgres.banda.svc.cluster.local', 5432)).toBe(false);
	});
});

// SANDBOX_EGRESS_ALLOW applies to a whole namespace, so these tests run their sandboxes in one of their own.
test.describe('egress allowlist', () => {
	const plain = 'file:///opt/e2e/plain.git';

	test('a sandbox reaches a private range SANDBOX_EGRESS_ALLOW covers, and no more once it is empty', async ({ as, startApi }) => {
		// The git server stands in for production: a pod, so on a private network.
		const git = await kube('get', 'pod', 'e2e-git', '-o', 'jsonpath={.status.podIP}');
		const api = await startApi({ SANDBOX_EGRESS_ALLOW: `${git}/32` }, { namespace: true });
		const t = await as('alice', api).create({ repoUrl: `git://${git}/plain.git` });
		await t.waitForStatus(Status.AWAITING_INPUT); // cloned from it
		expect(await reaches(t.pod, git, 9418, api.namespace)).toBe(true);

		await startApi({ SANDBOX_NAMESPACE: api.namespace }); // empty: it removes the policy
		await expect.poll(() => reaches(t.pod, git, 9418, api.namespace), { timeout: 15_000 }).toBe(false);
	});

	test('Postgres, Dex, the API’s other ports, other sandboxes and the Kubernetes API stay closed when an allowed range covers them', async ({ as, startApi }) => {
		const api = await startApi({ SANDBOX_EGRESS_ALLOW: '10.0.0.0/8' }, { namespace: true }); // kind's pods, services and node
		const ns = api.namespace;
		// The API in the cluster (the chart's banda-api) as the network sees it: the runner port and the preview's.
		await kubeIn(ns, 'run', 'banda-api', `--image=${process.env.SANDBOX_IMAGE}`, '--image-pull-policy=Never', '--labels=app=banda-api',
			'--command', '--', 'sh', '-c', 'python3 -m http.server 8788 & exec python3 -m http.server 8787');
		const alice = as('alice', api);
		const [a, b] = [await alice.create({ repoUrl: plain }), await alice.create({ repoUrl: plain })];
		await a.waitForStatus(Status.AWAITING_INPUT);
		await b.waitForStatus(Status.AWAITING_INPUT);
		await b.turn(() => b.send('start the preview'));
		await expect.poll(() => kexec(b.pod, 'curl -s localhost:5173/', ns), { timeout: 30_000 }).toContain('e2e-preview-page');
		await expect.poll(() => kexec('banda-api', 'curl -s -o /dev/null -o /dev/null -w %{http_code} localhost:8787 localhost:8788', ns).catch(() => ''), { timeout: 30_000 }).toBe('200200');
		const ip = (pod: string) => kubeIn(ns, 'get', 'pod', pod, '-o', 'jsonpath={.status.podIP}');
		const [apiIP, bIP] = [await ip('banda-api'), await ip(b.pod)];
		const node = await kube('get', 'nodes', '-o', 'jsonpath={.items[0].status.addresses[0].address}');

		// The range opens what the base policy refuses: the in-cluster API's pod, the Kubernetes API.
		expect(await reaches(a.pod, apiIP, 8787, ns)).toBe(true);
		expect(await reaches(a.pod, 'kubernetes.default.svc', 443, ns)).toBe(true);
		const closed = {
			'Postgres': ['postgres.banda.svc.cluster.local', 5432],
			'Postgres through its NodePort': [node, 30432],
			'Dex': [`dex.${NAMESPACE}.svc.cluster.local`, 5556],
			'the API’s preview port': [apiIP, 8788],
			'another thread’s dev server': [bIP, 5173]
		} as const;
		for (const [what, [host, port]] of Object.entries(closed)) expect(await reaches(a.pod, host, port, ns), what).toBe(false);
		// Sandboxes carry no token: the Kubernetes API and the kubelet answer, but refuse.
		const status = (url: string) => kexec(a.pod, `curl -sk -o /dev/null -w %{http_code} ${url}`, ns);
		expect(['401', '403']).toContain(await status(`https://kubernetes.default.svc/api/v1/namespaces/${ns}/secrets`));
		expect(['401', '403']).toContain(await status(`https://${node}:10250/pods`));
	});
});

test.describe('pods', () => {
	/** Fails if pod has no limits, mounts a service account token or carries the runner token as plain env. */
	async function checkPod(name: string) {
		const pod = JSON.parse(await kube('get', 'pod', name, '-o', 'json'));
		expect(pod.spec.automountServiceAccountToken).toBe(false);
		expect(pod.spec.volumes.filter((v: object) => 'projected' in v)).toEqual([]);
		const c = pod.spec.containers[0];
		expect(Object.keys(c.resources?.limits ?? {}).sort()).toEqual(['cpu', 'ephemeral-storage', 'memory']);
		expect(Object.keys(c.resources?.requests ?? {}).sort()).toEqual(['cpu', 'ephemeral-storage', 'memory']);
		expect(c.env.find((e: { name: string }) => e.name === 'BANDA_RUNNER_TOKEN')).toMatchObject({ valueFrom: { secretKeyRef: {} } });
		expect(await kexec(name, 'ls /var/run/secrets/kubernetes.io 2>&1 || true')).toContain('No such file');
		const token = await env(name, 'BANDA_RUNNER_TOKEN');
		expect(token.length).toBeGreaterThan(20);
		expect(await kube('get', 'sandbox', name, '-o', 'json')).not.toContain(token);
		return token;
	}

	test('F6, F9, F2: thread and login pods have resource limits, no service account token, and their runner token from a Secret', async ({ as }) => {
		const alice = as('alice');
		const t = await alice.create();
		await t.waitForStatus(Status.AWAITING_INPUT);
		await checkPod(t.pod);
		expect(await loginTerminal(alice, LoginAction.LOGIN)).toContain('Login successful');
		await checkPod(`login-${userHash(alice.id)}`);
	});
});

test.describe('tokens', () => {
	test('F2: nothing the database holds for a thread or its owner works as their runner token', async ({ as }) => {
		const alice = as('alice');
		const t = await alice.create();
		await t.waitForStatus(Status.AWAITING_INPUT);
		expect(await loginTerminal(alice, LoginAction.LOGIN)).toContain('Login successful');
		const token = await env(t.pod, 'BANDA_RUNNER_TOKEN');
		const loginToken = await env(`login-${userHash(alice.id)}`, 'BANDA_RUNNER_TOKEN');
		expect(await mcpStatus(API, t.id, token)).toBe(200);

		const values = async (table: string, id: string) =>
			(await sql(`select v from ${table} x, json_each_text(row_to_json(x)) j(k, v) where x.id = '${id}' and length(v) >= 16`)).split('\n').filter(Boolean);
		const stored = [...(await values('threads', t.id)), ...(await values('users', alice.id))];
		expect(stored.join('\n')).not.toContain(token);
		expect(stored.join('\n')).not.toContain(loginToken);
		for (const v of stored) expect(await mcpStatus(API, t.id, v), `the stored value ${v.slice(0, 40)}`).toBe(401);
	});

	/** Runs a second runner in the thread's pod, with its environment (and so its token), for up to 15 s. */
	const secondRunner = (t: Thread, home = '/tmp/second') => kexec(t.pod, `mkdir -p ${home}; HOME=${home} timeout 15 runner 2>&1; echo exit=$?`);

	test('F2: a second runner with the thread’s token can’t take over a live runner', async ({ as }) => {
		const t = await as('alice').create();
		await t.waitForStatus(Status.AWAITING_INPUT);
		const out = await secondRunner(t);
		expect(out).toContain('already_exists');
		expect(out).not.toContain('exit=124');
		const info = await t.turn(() => t.send('still mine e2e-echo'));
		expect(info.status).toBe(Status.AWAITING_INPUT);
		expect((await t.prompts()).at(-1)).toContain('still mine');
	});

	test('F2: a runner that stopped answering is replaced by the next one (a restart the API has not noticed)', async ({ as }) => {
		const t = await as('alice').create();
		await t.waitForStatus(Status.AWAITING_INPUT);
		await kexec(t.pod, 'kill -STOP 1');
		await kexec(t.pod, 'nohup runner >/tmp/second.log 2>&1 &');
		await t.turn(() => t.send('served by the second e2e-echo'));
		expect((await t.prompts()).at(-1)).toContain('served by the second');
	});
});

test.describe('repository URLs', () => {
	test('F7: only plain git URLs are accepted; transports, options and control characters are refused', async ({ as }) => {
		const alice = as('alice');
		for (const url of [
			'ext::sh -c touch% /tmp/pwned',
			'--upload-pack=touch /tmp/pwned',
			'-u',
			'fd::17',
			'https::example.com/a.git',
			'-oProxyCommand=x@host:path',
			'ftp://example.com/a.git',
			'https://example.com/a.git\n--upload-pack=x',
			'https://example.com/a\tb.git',
			'https:///a.git'
		])
			expect(await refused(alice.create({ repoUrl: url })), url).toContain('invalid_argument');
		for (const url of ['file:///opt/e2e/plain.git', repo()]) {
			const t = await alice.create({ repoUrl: url });
			await t.waitForStatus(Status.AWAITING_INPUT);
		}
	});
});

test.describe('browser', () => {
	// Regression guards for what open risk 6 relies on (PLAN.md): all pass since round 3.
	const list = `${API}/banda.v1.ThreadService/ListThreads`;

	test('F8: the Connect API refuses CORS-simple requests and answers no preflight', async () => {
		const simple = await fetch(list, { method: 'POST', headers: { 'Content-Type': 'text/plain', 'X-Banda-User': 'e2e-csrf' }, body: '{}' });
		expect(simple.status).toBe(415);
		const form = await fetch(list, { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-Banda-User': 'e2e-csrf' }, body: 'a=b' });
		expect(form.status).toBe(415);
		const preflight = await fetch(list, {
			method: 'OPTIONS',
			headers: { Origin: 'http://evil.example', 'Access-Control-Request-Method': 'POST', 'Access-Control-Request-Headers': 'content-type' }
		});
		expect(preflight.headers.get('access-control-allow-origin')).toBeNull();
		expect(preflight.headers.get('access-control-allow-credentials')).toBeNull();
	});

	test('F8: uploads from another site are refused; pages refuse frames', async ({ as }) => {
		const alice = as('alice');
		for (const site of ['cross-site', 'same-site']) {
			const r = await fetch(`${API}/files?name=a.txt`, { method: 'POST', headers: { 'Content-Type': 'text/plain', 'X-Banda-User': alice.id, 'Sec-Fetch-Site': site }, body: 'x' });
			expect(r.status, site).toBe(403);
		}
		expect((await alice.upload('a.txt', 'text/plain', 'x')).status).toBe(200);
		expect((await fetch(`${API}/`)).headers.get('content-security-policy')).toContain("frame-ancestors 'none'");
	});
});
