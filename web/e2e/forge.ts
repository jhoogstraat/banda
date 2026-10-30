// A fake forge for web/e2e/forge.spec.ts: the GitHub REST (and the GraphQL mutations Banda uses) and GitLab
// REST endpoints internal/forge calls, at /api/v3 and /api/v4, backed by one in-memory list of pull requests
// whose head branches must exist on the run's git server. An API of the test's own reaches it through
// TEST_FORGE_URL; the test changes its state (checks, reviews) as a forge's users would.
import { createHash, generateKeyPairSync, randomBytes, verify, type KeyObject } from 'node:crypto';
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { kexec } from './fixtures';

export type Check = { name: string; status: 'queued' | 'in_progress' | 'completed'; conclusion?: 'success' | 'failure' | 'skipped' };
/** thread: a GitLab discussion without a file, which is resolvable like one on a line (a plain note isn't). */
export type Comment = { id: number; login: string; body: string; at: string; path?: string; line?: number; inReplyTo?: number; review?: string; resolved?: boolean; thread?: boolean };

export type Pull = {
	number: number;
	title: string;
	body: string;
	draft: boolean;
	state: 'open' | 'closed' | 'merged';
	head: string;
	base: string;
	sha: string;
	author: string;
	reviewers: string[];
	labels: string[];
	checks: Check[];
	comments: Comment[];
	mergedBy?: string;
	mergeMethod?: string;
	/** GitLab: the detailed_merge_status that blocks merging, which then answers a bare 405. */
	blocked?: string;
};

type GitLabApp = { clientId: string; clientSecret: string; redirect: string; scopes: string[] };
type FakeApp = { id: number; slug: string; clientId: string; clientSecret: string; key: KeyObject; pem: string; manifest: Record<string, any>; owner: string; ownerType: 'User' | 'Organization' };

export class FakeForge {
	url = '';
	pulls: Pull[] = [];
	/** Tokens and whose they are: anything else is refused with a 401. */
	tokens: Record<string, string> = {};
	/** Who may review and the labels a repository has. */
	collaborators = ['alice', 'bob', 'carol', 'dave'];
	labels = ['e2e', 'bug', 'ui'];
	/** "METHOD path" of each request, for what tests can't see otherwise. */
	calls: string[] = [];

	// The GitHub App (internal/forge/app.go): made from the manifest Banda posts, installed on accounts, with
	// users connecting through its OAuth flow.
	app?: FakeApp;
	/** Every app made, the latest last: an old one's credentials still revoke its grants. */
	apps: FakeApp[] = [];
	/** What the next app made from a manifest differs in from it, as a forge (or someone posting their own manifest) could. */
	tamper: { public?: boolean; permissions?: Record<string, string>; events?: string[]; hookUrl?: string; owner?: string; ownerType?: 'User' | 'Organization' } = {};
	/** The OAuth error refreshing answers with instead of a token (e.g. incorrect_client_credentials), or an HTTP status. */
	refreshFails?: string | number;
	/** Accounts it is installed on (each with two repositories selected). */
	installedOn: string[] = [];
	/** Who is signed in to the fake GitHub in the browser, for its authorize page. */
	signedIn = '';
	/** What each GitHub login may do with the repositories, default push. */
	access: Record<string, 'push' | 'pull' | 'none'> = {};
	/** expires_in of the next user token issued by exchanging a code. */
	userTokenTTL = 28800;
	/** Installation tokens minted: what Banda asked for. */
	minted: { token: string; repositories: string[]; permissions: Record<string, string> }[] = [];
	/** Refresh tokens spent, and access tokens revoked by a disconnect. */
	refreshed: string[] = [];
	revoked: string[] = [];
	/** Users' current refresh tokens. */
	refreshTokens: Record<string, string> = {};
	private codes = new Map<string, { login: string; challenge: string; redirect: string }>();

	// GitLab's OAuth (internal/forge/gitlab_app.go): the application someone made by hand, and the grants users
	// gave it, each with its own access and refresh token and one scope, which the API endpoints enforce.
	/** Every application made, the latest last: GitLab keeps an old one, whose credentials still revoke its grants. */
	gitlabApps: GitLabApp[] = [];
	get gitlabApp(): GitLabApp | undefined {
		return this.gitlabApps.at(-1);
	}
	set gitlabApp(a: GitLabApp) {
		this.gitlabApps.push(a);
	}
	/** Grants, newest last; a refresh replaces a grant's tokens, a revocation ends it. */
	grants: { login: string; scope: string; access: string; refresh: string; clientId: string; revoked?: boolean; expired?: boolean }[] = [];
	/** Projects a user's api token lists, and finds by search. */
	gitlabProjects: string[] = [];
	/** Who approves an authorization of a scope instead of signedIn, as if the browser's GitLab session changed between the two. */
	gitlabSignedInFor: Record<string, string> = {};
	/** The OAuth error refreshing a GitLab token answers with (e.g. invalid_client, with a 401), or an HTTP status. */
	gitlabRefreshFails?: string | number;
	/** expires_in of the next GitLab token issued by exchanging a code. */
	gitlabTokenTTL = 7200;
	/** A scope whose authorization the user cancels on GitLab's consent screen. */
	gitlabDeny = '';
	/** Each authorization asked for, as its scope, and refresh tokens spent. */
	gitlabAuthorized: string[] = [];
	gitlabRefreshed: string[] = [];
	private gitlabCodes = new Map<string, { login: string; challenge: string; redirect: string; scope: string }>();

	/** A user's live GitLab grant for scope. */
	grant(login: string, scope: string) {
		return this.grants.find((g) => g.login === login && g.scope === scope && !g.revoked);
	}
	private nextId = 100;
	private users: string[] = []; // GitLab user ids are 1 + the index
	private idOf(name: string) {
		if (!this.users.includes(name)) this.users.push(name);
		return this.users.indexOf(name) + 1;
	}
	private server = createServer((req, res) => this.serve(req, res).catch((e) => send(res, 500, { message: String(e) })));

	async start() {
		await new Promise<void>((r) => this.server.listen(0, '127.0.0.1', r));
		this.url = `http://127.0.0.1:${(this.server.address() as { port: number }).port}`;
		return this;
	}

	stop() {
		this.server.closeAllConnections();
		return new Promise((r) => this.server.close(r));
	}

	pull(n = 1) {
		const p = this.pulls.find((p) => p.number === n);
		if (!p) throw new Error(`no pull request ${n}: ${this.pulls.map((p) => p.number)}`);
		return p;
	}

	/** A review or a review comment, as a forge user would leave it. */
	comment(n: number, c: Omit<Comment, 'id' | 'at'>) {
		this.pull(n).comments.push({ ...c, id: this.nextId++, at: new Date().toISOString() });
	}

	/** Opens a pull request directly on the forge, as someone outside Banda would. */
	async open(head: string, title: string, repo = 'plain') {
		const p = this.newPull(head, 'main', title, '', false, 'carol', await sha(repo, head));
		return p.number;
	}

	private newPull(head: string, base: string, title: string, body: string, draft: boolean, author: string, sha: string) {
		const p: Pull = { number: this.pulls.length + 1, title, body, draft, state: 'open', head, base, sha, author, reviewers: [], labels: ['e2e'], checks: [], comments: [] };
		this.pulls.push(p);
		return p;
	}

	private async serve(req: IncomingMessage, res: ServerResponse) {
		const url = new URL(req.url!, 'http://x');
		const path = decodeURIComponent(url.pathname);
		this.calls.push(`${req.method} ${path}`);
		const raw = await new Promise<string>((r) => {
			let s = '';
			req.on('data', (d) => (s += d)).on('end', () => r(s));
		});
		const form = /urlencoded/.test(req.headers['content-type'] ?? '') || path.endsWith('/settings/apps/new');
		const body = !raw ? {} : form ? Object.fromEntries(new URLSearchParams(raw)) : JSON.parse(raw);
		const done = await this.githubApp(req.method!, path, url, body, req, res);
		if (done !== false) return;
		if (path.startsWith('/oauth/')) return this.gitlabOAuth(req.method!, path, url, body, req, res);
		const token = (req.headers.authorization ?? '').replace(/^Bearer /, '') || String(req.headers['private-token'] ?? '');
		const login = this.tokens[token];
		if (!login) return send(res, 401, { message: path.startsWith('/api/v4/') ? '401 Unauthorized' : 'Bad credentials' });
		// GitLab: an OAuth token reaches the API only with the api scope; write_repository is for git over https.
		const g = this.grants.find((g) => g.access === token);
		if (g?.expired) return send(res, 401, { error: 'invalid_token', error_description: 'Token is expired. You can either do re-authorization or token refresh.' });
		if (g && path.startsWith('/api/v4/') && g.scope !== 'api') return send(res, 403, { error: 'insufficient_scope', scope: 'api' });
		// A user's app token reaches only repositories the app is installed for.
		if (token.startsWith('ghu_') && /^\/api\/v3\/repos\/e2e\//.test(path) && !this.installedOn.includes('e2e')) return send(res, 404, { message: 'Not Found' });
		if (path.startsWith('/api/v3/') || path === '/api/graphql') return this.github(req.method!, path, url, body, login, res);
		if (path.startsWith('/api/v4/')) return this.gitlab(req.method!, path, url, body, login, res);
		send(res, 404, { message: 'Not Found' });
	}

	private async github(method: string, path: string, url: URL, body: Record<string, any>, login: string, res: ServerResponse) {
		const web = `${this.url}/e2e/plain/pull`;
		const json = (p: Pull) => ({
			number: p.number, node_id: `PR_${p.number}`, title: p.title, body: p.body, draft: p.draft, state: p.state === 'open' ? 'open' : 'closed',
			merged: p.state === 'merged', merged_at: p.state === 'merged' ? new Date().toISOString() : null, merged_by: p.mergedBy ? { login: p.mergedBy } : null,
			html_url: `${web}/${p.number}`, user: { login: p.author }, head: { ref: p.head, sha: p.sha }, base: { ref: p.base },
			mergeable_state: p.draft ? 'draft' : 'clean', additions: 1, deletions: 0, changed_files: 1,
			labels: p.labels.map((name) => ({ name })), requested_reviewers: p.reviewers.map((login) => ({ login }))
		});
		if (path === '/api/graphql') {
			const query = String(body.query);
			const roots = (p: Pull) => p.comments.filter((c) => c.path && !c.inReplyTo);
			if (query.includes('reviewThreads')) {
				const p = this.pull(body.variables.number);
				const nodes = roots(p).map((c) => ({ id: `RT_${c.id}`, isResolved: !!c.resolved, comments: { nodes: [{ databaseId: c.id }] } }));
				return send(res, 200, { data: { repository: { pullRequest: { reviewThreads: { nodes } } } } });
			}
			if (query.includes('ReviewThread')) {
				const c = this.pulls.flatMap(roots).find((c) => `RT_${c.id}` === body.variables?.id);
				if (!c) return send(res, 200, { errors: [{ message: 'Could not resolve to a node' }] });
				c.resolved = !query.includes('unresolveReviewThread');
				return send(res, 200, { data: {} });
			}
			const p = this.pulls.find((p) => `PR_${p.number}` === body.variables?.id);
			if (!p) return send(res, 200, { errors: [{ message: 'Could not resolve to a node' }] });
			p.draft = query.includes('convertPullRequestToDraft');
			return send(res, 200, { data: {} });
		}
		const listed = (full_name: string, description = '') => ({ full_name, description, owner: { login: full_name.split('/')[0] } });
		if (method === 'GET' && path === '/api/v3/user') return send(res, 200, { login, id: login.length * 1000, avatar_url: `${this.url}/avatars/${login}` });
		if (method === 'GET' && path === '/api/v3/user/installations')
			return send(res, 200, { total_count: this.installedOn.length, installations: this.installedOn.map((a, i) => ({ id: i + 1, account: { login: a } })) });
		if (method === 'GET' && /^\/api\/v3\/user\/installations\/\d+\/repositories$/.test(path))
			return send(res, 200, { total_count: 2, repositories: [listed('e2e/plain', 'The fixture repository'), listed('e2e/profiles')] });
		if (method === 'GET' && path === '/api/v3/installation/repositories') return send(res, 200, { total_count: 2, repositories: [] });
		if (method === 'GET' && path === '/api/v3/user/repos') return send(res, 200, [listed('e2e/plain', 'The fixture repository'), listed('e2e/profiles')]);
		// Only searching finds e2e/archive-2019 (as if beyond the first page), and only within the owners asked for.
		if (method === 'GET' && path === '/api/v3/search/repositories') {
			const q = url.searchParams.get('q') ?? '';
			const words = q.split(' ').filter((w) => !w.includes(':'));
			const owners = [...q.matchAll(/user:(\S+)/g)].map((m) => m[1]);
			const items = [listed('e2e/plain'), listed('e2e/profiles'), listed('e2e/archive-2019', 'Old work'), listed('elsewhere/archive')]
				.filter((r) => owners.includes(r.owner.login) && words.every((w) => r.full_name.split('/')[1].includes(w)));
			return send(res, 200, { total_count: items.length, items });
		}
		const m = path.match(/^\/api\/v3\/repos\/e2e\/(\w+)(\/.*)?$/);
		if (!m) return send(res, 404, { message: 'Not Found' });
		const [, repo, rest = ''] = m;
		let r: RegExpMatchArray | null;
		if (method === 'GET' && rest === '') {
			const may = this.access[login] ?? 'push';
			return send(res, 200, { name: repo, allow_merge_commit: true, allow_squash_merge: true, allow_rebase_merge: false,
				permissions: { push: may === 'push', pull: may !== 'none' } });
		}
		if (method === 'GET' && rest === '/collaborators') return send(res, 200, this.collaborators.map((login) => ({ login })));
		if (method === 'GET' && rest === '/labels') return send(res, 200, this.labels.map((name) => ({ name })));
		if (method === 'GET' && rest === '/pulls') {
			const head = url.searchParams.get('head')?.split(':')[1];
			return send(res, 200, this.pulls.filter((p) => p.state === 'open' && (!head || p.head === head)).map(json));
		}
		if (method === 'POST' && rest === '/pulls') {
			if (this.pulls.some((p) => p.head === body.head && p.state === 'open'))
				return send(res, 422, { message: 'Validation Failed', errors: [{ message: `A pull request already exists for e2e:${body.head}.` }] });
			const head = await sha(repo, body.head).catch(() => '');
			if (!head) return send(res, 422, { message: 'Validation Failed', errors: [{ field: 'head', code: 'invalid' }] });
			return send(res, 201, json(this.newPull(body.head, body.base, body.title, body.body ?? '', !!body.draft, login, head)));
		}
		if ((r = rest.match(/^\/commits\/(\w+)\/(check-runs|status)$/))) {
			const p = this.pulls.find((p) => p.sha === r![1]);
			if (r[2] === 'status') return send(res, 200, { statuses: [] });
			return send(res, 200, {
				total_count: p?.checks.length ?? 0,
				check_runs: (p?.checks ?? []).map((c, i) => ({ id: i, name: c.name, status: c.status, conclusion: c.conclusion ?? null, details_url: `${this.url}/ci/${i}`, started_at: new Date(Date.now() - 65_000).toISOString() }))
			});
		}
		if (!(r = rest.match(/^\/(pulls|issues)\/(\d+)(\/[\w/]+)?$/))) return send(res, 404, { message: 'Not Found' });
		const p = this.pulls.find((p) => p.number === Number(r![2]));
		if (!p) return send(res, 404, { message: 'Not Found' });
		const [, kind, , sub = ''] = r;
		const card = (c: Comment) => ({ id: c.id, user: { login: c.login }, body: c.body, created_at: c.at, html_url: `${web}/${p.number}#c${c.id}` });
		if (kind === 'issues' && sub === '/comments' && method === 'GET') return send(res, 200, p.comments.filter((c) => !c.path && !c.review).map(card));
		if (kind === 'issues' && sub === '/comments' && method === 'POST') {
			this.comment(p.number, { login, body: body.body });
			return send(res, 201, { id: this.nextId });
		}
		if (method === 'GET' && sub === '') return send(res, 200, json(p));
		if (kind === 'issues' && sub === '/labels' && method === 'POST') {
			p.labels.push(...(body as unknown as string[]).filter((l) => !p.labels.includes(l)));
			return send(res, 200, p.labels.map((name) => ({ name })));
		}
		if (kind === 'issues' && sub.startsWith('/labels/') && method === 'DELETE') {
			p.labels = p.labels.filter((l) => l !== sub.slice('/labels/'.length));
			return send(res, 200, []);
		}
		if (method === 'PATCH' && sub === '') {
			Object.assign(p, body.title !== undefined && { title: body.title }, body.body !== undefined && { body: body.body });
			return send(res, 200, json(p));
		}
		if (method === 'GET' && sub === '/reviews')
			return send(res, 200, p.comments.filter((c) => c.review).map((c) => ({ ...card(c), state: c.review, submitted_at: c.at })));
		if (method === 'GET' && sub === '/comments')
			return send(res, 200, p.comments.filter((c) => c.path).map((c) => ({ ...card(c), path: c.path, line: c.line, in_reply_to_id: c.inReplyTo, diff_hunk: '@@ -0,0 +1 @@\n+hello' })));
		if (method === 'POST' && sub === '/comments') {
			const parent = p.comments.find((c) => c.id === body.in_reply_to);
			if (!parent) return send(res, 404, { message: 'Not Found' });
			this.comment(p.number, { login, body: body.body, path: parent.path, line: parent.line, inReplyTo: parent.id });
			return send(res, 201, { id: this.nextId });
		}
		if (method === 'POST' && sub === '/requested_reviewers') {
			p.reviewers.push(...body.reviewers);
			return send(res, 201, json(p));
		}
		if (method === 'PUT' && sub === '/merge') {
			if (p.state !== 'open' || p.draft) return send(res, 405, { message: 'Pull Request is not mergeable' });
			Object.assign(p, { state: 'merged', mergedBy: login, mergeMethod: body.merge_method });
			return send(res, 200, { merged: true });
		}
		send(res, 404, { message: 'Not Found' });
	}

	/** The app's own endpoints, and GitHub's pages a browser goes through; false for anything else. */
	private async githubApp(method: string, path: string, url: URL, body: Record<string, any>, req: IncomingMessage, res: ServerResponse) {
		const redirect = (to: string) => res.writeHead(302, { Location: to }).end();
		let m: RegExpMatchArray | null;
		// The page that creates an app from a posted manifest: it confirms at once and returns with a code.
		if (method === 'POST' && (m = path.match(/^(?:\/organizations\/([\w-]+))?\/settings\/apps\/new$/))) {
			const manifest = JSON.parse(body.manifest);
			const t = this.tamper;
			this.makeApp({ ...manifest, public: t.public ?? manifest.public, default_permissions: t.permissions ?? manifest.default_permissions,
				default_events: t.events ?? manifest.default_events, hook_attributes: { ...manifest.hook_attributes, url: t.hookUrl ?? manifest.hook_attributes.url } },
				t.owner ?? m[1] ?? (this.signedIn || 'e2e'), t.ownerType ?? (m[1] ? 'Organization' : 'User'));
			return redirect(`${manifest.redirect_url}?${new URLSearchParams({ code: 'manifest-code', state: url.searchParams.get('state') ?? '' })}`);
		}
		if (method === 'POST' && path === '/api/v3/app-manifests/manifest-code/conversions' && this.app) {
			const a = this.app;
			return send(res, 201, { id: a.id, slug: a.slug, client_id: a.clientId, client_secret: a.clientSecret, pem: a.pem, owner: { login: a.owner, type: a.ownerType },
				html_url: `${this.url}/apps/${a.slug}`, name: a.manifest.name, permissions: a.manifest.default_permissions, events: a.manifest.default_events });
		}
		// A private app's page isn't found without signing in.
		if (method === 'GET' && (m = path.match(/^\/api\/v3\/apps\/([\w-]+)$/)) && !req.headers.authorization) {
			const a = this.apps.find((a) => a.slug === m![1]);
			return a?.manifest.public ? send(res, 200, { slug: a.slug }) : send(res, 404, { message: 'Not Found' });
		}
		// Installing: on the account the test says, then back to the app's setup URL.
		if (method === 'GET' && path === `/apps/${this.app?.slug}/installations/new`) {
			if (!this.installedOn.includes('e2e')) this.installedOn.push('e2e');
			return redirect(`${this.app!.manifest.setup_url}?installation_id=${this.installedOn.indexOf('e2e') + 1}&setup_action=install`);
		}
		// Authorizing: the signed-in GitHub user approves at once.
		if (method === 'GET' && path === '/login/oauth/authorize') {
			const q = url.searchParams;
			if (q.get('client_id') !== this.app?.clientId || q.get('code_challenge_method') !== 'S256') return send(res, 400, { message: 'bad authorize request' });
			const code = `code-${randomBytes(4).toString('hex')}`;
			this.codes.set(code, { login: this.signedIn, challenge: q.get('code_challenge')!, redirect: q.get('redirect_uri')! });
			return redirect(`${q.get('redirect_uri')}?${new URLSearchParams({ code, state: q.get('state')! })}`);
		}
		if (method === 'POST' && path === '/login/oauth/access_token') {
			if (body.client_id !== this.app?.clientId || body.client_secret !== this.app?.clientSecret) return send(res, 200, { error: 'incorrect_client_credentials' });
			let login: string, ttl = 28800;
			if (body.grant_type === 'refresh_token' && this.refreshFails !== undefined)
				return typeof this.refreshFails === 'number' ? send(res, this.refreshFails, { message: 'unavailable' }) : send(res, 200, { error: this.refreshFails });
			if (body.grant_type === 'refresh_token') {
				login = Object.keys(this.refreshTokens).find((l) => this.refreshTokens[l] === body.refresh_token) ?? '';
				if (!login) return send(res, 200, { error: 'bad_refresh_token' });
				this.refreshed.push(body.refresh_token);
			} else {
				const c = this.codes.get(body.code);
				this.codes.delete(body.code);
				const pkce = createHash('sha256').update(body.code_verifier ?? '').digest('base64url');
				if (!c || c.challenge !== pkce || c.redirect !== body.redirect_uri) return send(res, 200, { error: 'bad_verification_code' });
				[login, ttl] = [c.login, this.userTokenTTL];
			}
			for (const [t, l] of Object.entries(this.tokens)) if (l === login && t.startsWith('ghu_')) delete this.tokens[t]; // the old one stops working
			const access = `ghu_${randomBytes(18).toString('hex')}`;
			this.tokens[access] = login;
			this.refreshTokens[login] = `ghr_${randomBytes(18).toString('hex')}`;
			return send(res, 200, { access_token: access, refresh_token: this.refreshTokens[login], expires_in: ttl, refresh_token_expires_in: 15897600, token_type: 'bearer' });
		}
		if (method === 'DELETE' && (m = path.match(/^\/api\/v3\/applications\/([\w.-]+)\/grant$/))) {
			const a = this.apps.find((a) => a.clientId === m![1]);
			const basic = Buffer.from((req.headers.authorization ?? '').replace(/^Basic /, ''), 'base64').toString();
			if (!a || basic !== `${a.clientId}:${a.clientSecret}`) return send(res, 401, { message: 'Bad credentials' });
			this.revokeAll(this.tokens[body.access_token] ?? '');
			this.revoked.push(body.access_token);
			return res.writeHead(204).end();
		}
		// As the app: a JWT signed with its key.
		const jwt = (req.headers.authorization ?? '').replace(/^Bearer /, '');
		if (!/^\/api\/v3\/(app|repos\/[\w-]+\/[\w-]+\/installation$)/.test(path) || !jwt.startsWith('ey')) return false;
		if (!this.app || !this.validJWT(jwt)) return send(res, 401, { message: 'A JSON web token could not be decoded' });
		if (method === 'GET' && (m = path.match(/^\/api\/v3\/repos\/([\w-]+)\/[\w-]+\/installation$/))) {
			const i = this.installedOn.indexOf(m[1]);
			return i < 0 ? send(res, 404, { message: 'Not Found' }) : send(res, 200, { id: i + 1, account: { login: m[1] } });
		}
		if (method === 'GET' && path === '/api/v3/app') return send(res, 200, { id: this.app.id, slug: this.app.slug, owner: { login: this.app.owner, type: this.app.ownerType } });
		if (method === 'GET' && path === '/api/v3/app/hook/config') return send(res, 200, { url: this.app.manifest.hook_attributes?.url, content_type: 'json' });
		if (method === 'GET' && path === '/api/v3/app/installations')
			return send(res, 200, this.installedOn.map((a, i) => ({ id: i + 1, account: { login: a, avatar_url: `${this.url}/avatars/${a}` }, repository_selection: 'selected',
				html_url: `${this.url}/organizations/${a}/settings/installations/${i + 1}` })));
		if (method === 'POST' && (m = path.match(/^\/api\/v3\/app\/installations\/(\d+)\/access_tokens$/))) {
			const token = `ghs_${randomBytes(18).toString('hex')}`;
			this.tokens[token] = `${this.app.slug}[bot]`;
			this.minted.push({ token, repositories: body.repositories ?? [], permissions: body.permissions ?? {} });
			return send(res, 201, { token, expires_at: new Date(Date.now() + 3600_000).toISOString() });
		}
		return send(res, 404, { message: 'Not Found' });
	}

	/** Makes an app, as GitHub does from a manifest; the next ones get other ids and slugs. Returns its Secret's data, as for one made by hand. */
	makeApp(manifest: Record<string, any>, owner: string, ownerType: 'User' | 'Organization' = 'User') {
		const { publicKey, privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048 });
		const n = this.apps.length;
		const a: FakeApp = { id: 4242 + n, slug: n ? `banda-e2e-${n + 1}` : 'banda-e2e', clientId: `Iv1.e2e${n || ''}`, clientSecret: `e2e-client-secret-${n}`,
			key: publicKey, pem: privateKey.export({ type: 'pkcs1', format: 'pem' }) as string, manifest, owner, ownerType };
		this.apps.push(a);
		this.app = a;
		return { 'app-id': String(a.id), slug: a.slug, 'client-id': a.clientId, 'client-secret': a.clientSecret, 'private-key': a.pem };
	}

	/** The app's JWT: RS256 by its key, issued for at most 10 minutes. */
	private validJWT(jwt: string) {
		const [h, p, sig] = jwt.split('.');
		const claims = JSON.parse(Buffer.from(p, 'base64url').toString());
		return verify('sha256', Buffer.from(`${h}.${p}`), this.app!.key, Buffer.from(sig, 'base64url')) && claims.iss === this.app!.clientId &&
			claims.exp - claims.iat <= 600 && claims.exp > Date.now() / 1000;
	}

	/** Revokes a user's tokens, as GitHub does when they revoke the app (or Banda deletes the grant). */
	revokeAll(login: string) {
		for (const [t, l] of Object.entries(this.tokens)) if (l === login && t.startsWith('ghu_')) delete this.tokens[t];
		delete this.refreshTokens[login];
	}

	/** A user's current access token for the app. */
	userToken(login: string) {
		return Object.keys(this.tokens).find((t) => t.startsWith('ghu_') && this.tokens[t] === login);
	}

	/** GitLab's OAuth endpoints (Doorkeeper): authorize (the signed-in user approves at once), token, revoke. */
	private async gitlabOAuth(method: string, path: string, url: URL, body: Record<string, any>, req: IncomingMessage, res: ServerResponse) {
		if (method === 'GET' && path === '/oauth/token/info') {
			const g = this.grants.find((g) => g.access && g.access === (req.headers.authorization ?? '').replace(/^Bearer /, '') && !g.revoked && !g.expired);
			if (!g) return send(res, 401, { error: 'invalid_token', error_description: 'The access token is invalid', state: 'unauthorized' });
			return send(res, 200, { resource_owner_id: this.idOf(g.login), scope: [g.scope], expires_in: 7200, application: { uid: g.clientId } });
		}
		if (method === 'GET' && path === '/oauth/authorize') {
			const q = url.searchParams;
			const a = this.gitlabApps.find((a) => a.clientId === q.get('client_id'));
			const scope = q.get('scope') ?? '';
			if (!a || q.get('client_id') !== a.clientId || q.get('redirect_uri') !== a.redirect || q.get('response_type') !== 'code' ||
				q.get('code_challenge_method') !== 'S256' || !scope || !scope.split(' ').every((s) => a.scopes.includes(s)))
				return send(res, 400, { error: 'invalid_request', scope, app: a?.scopes });
			this.gitlabAuthorized.push(scope);
			const back = (p: Record<string, string>) => res.writeHead(302, { Location: `${a.redirect}?${new URLSearchParams({ ...p, state: q.get('state')! })}` }).end();
			if (scope === this.gitlabDeny) return back({ error: 'access_denied' });
			const code = `glcode-${randomBytes(4).toString('hex')}`;
			this.gitlabCodes.set(code, { login: this.gitlabSignedInFor[scope] ?? this.signedIn, challenge: q.get('code_challenge')!, redirect: q.get('redirect_uri')!, scope });
			return back({ code });
		}
		const a = this.gitlabApps.find((a) => body.client_id === a.clientId && body.client_secret === a.clientSecret);
		if (method === 'POST' && path === '/oauth/token') {
			if (!a) return send(res, 401, { error: 'invalid_client' });
			let g: (typeof this.grants)[number] | undefined;
			if (body.grant_type === 'refresh_token' && this.gitlabRefreshFails !== undefined)
				return typeof this.gitlabRefreshFails === 'number'
					? send(res, this.gitlabRefreshFails, { message: 'unavailable' })
					: send(res, this.gitlabRefreshFails === 'invalid_client' ? 401 : 400, { error: this.gitlabRefreshFails });
			if (body.grant_type === 'refresh_token') {
				g = this.grants.find((g) => g.refresh === body.refresh_token && g.clientId === a.clientId && !g.revoked);
				if (!g) return send(res, 400, { error: 'invalid_grant' });
				this.gitlabRefreshed.push(body.refresh_token);
				delete this.tokens[g.access]; // GitLab revokes the old pair
			} else {
				const c = this.gitlabCodes.get(body.code);
				this.gitlabCodes.delete(body.code);
				const pkce = createHash('sha256').update(body.code_verifier ?? '').digest('base64url');
				if (!c || c.challenge !== pkce || c.redirect !== body.redirect_uri) return send(res, 400, { error: 'invalid_grant' });
				g = { login: c.login, scope: c.scope, access: '', refresh: '', clientId: a.clientId };
				this.grants.push(g);
			}
			g.access = randomBytes(32).toString('hex');
			g.refresh = randomBytes(32).toString('hex');
			this.tokens[g.access] = g.login;
			const ttl = body.grant_type === 'refresh_token' ? 7200 : this.gitlabTokenTTL;
			return send(res, 200, { access_token: g.access, token_type: 'Bearer', expires_in: ttl, refresh_token: g.refresh, scope: g.scope,
				created_at: Math.floor(Date.now() / 1000) });
		}
		if (method === 'POST' && path === '/oauth/revoke') {
			if (!a) return send(res, 403, { error: 'unauthorized_client', error_description: 'You are not authorized to revoke this token' });
			const g = this.grants.find((g) => (g.access === body.token || g.refresh === body.token) && g.clientId === a.clientId);
			if (g && !g.revoked) {
				g.revoked = true;
				delete this.tokens[g.access];
			}
			return send(res, 200, {});
		}
		return send(res, 404, { message: 'Not Found' });
	}

	/** Expires a user's access token on GitLab, whatever expires_in said: its refresh token still works. */
	gitlabExpire(login: string, scope: string) {
		this.grant(login, scope)!.expired = true;
	}

	/** Revokes all of a user's grants of the GitLab application, as revoking it in their GitLab settings does. */
	gitlabRevokeAll(login: string) {
		for (const g of this.grants.filter((g) => g.login === login && !g.revoked)) {
			g.revoked = true;
			delete this.tokens[g.access];
		}
	}

	private async gitlab(method: string, path: string, url: URL, body: Record<string, any>, login: string, res: ServerResponse) {
		if (method === 'GET' && path === '/api/v4/user') return send(res, 200, { id: this.idOf(login), username: login, avatar_url: `${this.url}/avatars/${login}` });
		if (method === 'GET' && path === '/api/v4/users') {
			const name = url.searchParams.get('username');
			return send(res, 200, name ? [{ id: this.idOf(name), username: name }] : []);
		}
		if (method === 'GET' && path === '/api/v4/projects') {
			const search = url.searchParams.get('search') ?? '';
			return send(res, 200, this.gitlabProjects.filter((p) => p.includes(search)).map((path_with_namespace) => ({ path_with_namespace, description: '' })));
		}
		if (method === 'GET' && /^\/api\/v4\/projects\/e2e\/\w+\/members\/all$/.test(path)) return send(res, 200, this.collaborators.map((username) => ({ username })));
		if (method === 'GET' && /^\/api\/v4\/projects\/e2e\/\w+\/labels$/.test(path)) return send(res, 200, this.labels.map((name) => ({ name })));
		const m = path.match(/^\/api\/v4\/projects\/e2e\/(\w+)(\/.*)$/);
		if (!m) return send(res, 404, { message: '404 Not Found' });
		const [, repo, rest] = m;
		const web = `${this.url}/e2e/plain/-/merge_requests`;
		const json = (p: Pull) => ({
			iid: p.number, title: (p.draft ? 'Draft: ' : '') + p.title, description: p.body, draft: p.draft,
			state: p.state === 'open' ? 'opened' : p.state, web_url: `${web}/${p.number}`, author: { username: p.author },
			source_branch: p.head, target_branch: p.base, sha: p.sha, labels: p.labels, changes_count: '1',
			detailed_merge_status: p.blocked ?? (p.draft ? 'draft_status' : 'mergeable'), merged_by: p.mergedBy ? { username: p.mergedBy } : null,
			reviewers: p.reviewers.map((u) => ({ id: this.idOf(u), username: u })), head_pipeline: p.checks.length ? { id: p.number } : null
		});
		let r: RegExpMatchArray | null;
		if (method === 'GET' && rest === '/merge_requests') {
			const head = url.searchParams.get('source_branch');
			// Only a single merge request has its head pipeline.
			return send(res, 200, this.pulls.filter((p) => p.state === 'open' && (!head || p.head === head)).map((p) => ({ ...json(p), head_pipeline: undefined })));
		}
		if (method === 'POST' && rest === '/merge_requests') {
			const open = this.pulls.find((p) => p.head === body.source_branch && p.state === 'open');
			if (open) return send(res, 409, { message: [`Another open merge request already exists for this source branch: !${open.number}`] });
			const head = await sha(repo, body.source_branch).catch(() => '');
			if (!head) return send(res, 400, { message: { source_branch: ['does not exist'] } });
			const draft = /^draft:\s*/i.test(body.title);
			return send(res, 201, json(this.newPull(body.source_branch, body.target_branch, body.title.replace(/^draft:\s*/i, ''), body.description ?? '', draft, login, head)));
		}
		if ((r = rest.match(/^\/pipelines\/(\d+)\/jobs$/))) {
			const p = this.pull(Number(r[1]));
			const status = (c: Check) => (c.status !== 'completed' ? 'running' : c.conclusion === 'failure' ? 'failed' : c.conclusion === 'skipped' ? 'skipped' : 'success');
			return send(res, 200, p.checks.map((c, i) => ({ id: i, name: c.name, stage: 'test', status: status(c), web_url: `${this.url}/ci/${i}` })).reverse());
		}
		if (!(r = rest.match(/^\/merge_requests\/(\d+)(\/[\w/]+)?$/))) return send(res, 404, { message: '404 Not Found' });
		const p = this.pulls.find((p) => p.number === Number(r![1]));
		if (!p) return send(res, 404, { message: '404 Not Found' });
		const sub = r[2] ?? '';
		const note = (c: Comment) => ({ id: c.id, body: c.body, author: { username: c.login }, created_at: c.at, position: c.path ? { new_path: c.path, new_line: c.line } : null,
			resolvable: !!c.path || !!c.thread, resolved: !!c.resolved });
		if (method === 'GET' && sub === '') return send(res, 200, json(p));
		if (method === 'PUT' && sub === '') {
			if (body.title !== undefined) [p.draft, p.title] = [/^draft:\s*/i.test(body.title), body.title.replace(/^draft:\s*/i, '')];
			if (body.description !== undefined) p.body = body.description;
			if (body.reviewer_ids) p.reviewers = body.reviewer_ids.map((id: number) => this.users[id - 1]);
			if (body.add_labels) p.labels.push(...body.add_labels.split(','));
			if (body.remove_labels) p.labels = p.labels.filter((l) => !body.remove_labels.split(',').includes(l));
			return send(res, 200, json(p));
		}
		if (method === 'GET' && sub === '/reviewers')
			return send(res, 200, p.reviewers.map((username) => ({ user: { username }, state: p.comments.some((c) => c.login === username && c.review === 'CHANGES_REQUESTED') ? 'requested_changes' : 'unreviewed' })));
		if (method === 'GET' && sub === '/approvals')
			return send(res, 200, { approved_by: p.comments.filter((c) => c.review === 'APPROVED').map((c) => ({ user: { username: c.login } })) });
		if (method === 'GET' && sub === '/discussions') {
			const roots = p.comments.filter((c) => !c.inReplyTo && !c.review);
			return send(res, 200, roots.map((c) => ({ id: `d${c.id}`, individual_note: !c.path && !c.thread, notes: [c, ...p.comments.filter((x) => x.inReplyTo === c.id)].map(note) })));
		}
		// A note is a plain comment; a new discussion is a thread to resolve, with or without a position.
		if (method === 'POST' && sub === '/notes') {
			this.comment(p.number, { login, body: body.body });
			return send(res, 201, { id: this.nextId, body: body.body, system: false });
		}
		if (method === 'POST' && sub === '/discussions') {
			this.comment(p.number, { login, body: body.body, thread: true });
			return send(res, 201, { id: `d${this.nextId}` });
		}
		if (method === 'PUT' && (r = sub.match(/^\/discussions\/d(\d+)$/))) {
			const root = p.comments.find((c) => c.id === Number(r![1]))!;
			root.resolved = body.resolved;
			return send(res, 200, { id: `d${root.id}` });
		}
		if (method === 'POST' && (r = sub.match(/^\/discussions\/d(\d+)\/notes$/))) {
			const parent = p.comments.find((c) => c.id === Number(r![1]))!;
			this.comment(p.number, { login, body: body.body, path: parent.path, line: parent.line, inReplyTo: parent.id });
			return send(res, 201, { id: this.nextId });
		}
		if (method === 'PUT' && sub === '/merge') {
			if (p.state !== 'open' || p.draft || p.blocked) return send(res, 405, { message: '405 Method Not Allowed' });
			Object.assign(p, { state: 'merged', mergedBy: login, mergeMethod: body.squash ? 'squash' : 'merge' });
			return send(res, 200, json(p));
		}
		send(res, 404, { message: '404 Not Found' });
	}
}

function send(res: ServerResponse, status: number, body: unknown) {
	res.writeHead(status, { 'Content-Type': 'application/json' }).end(JSON.stringify(body));
}

/** The branch's head on the run's git server: a pull request needs its branch pushed. */
async function sha(repo: string, branch: string) {
	return (await kexec('e2e-git', `git --git-dir=/tmp/git/${repo}.git rev-parse --verify refs/heads/${branch}`)).trim();
}
