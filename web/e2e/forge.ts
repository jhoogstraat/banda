// A fake forge for web/e2e/forge.spec.ts: the GitHub REST (and the GraphQL mutations Banda uses) and GitLab
// REST endpoints internal/forge calls, at /api/v3 and /api/v4, backed by one in-memory list of pull requests
// whose head branches must exist on the run's git server. An API of the test's own reaches it through
// TEST_FORGE_URL; the test changes its state (checks, reviews) as a forge's users would.
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { kexec } from './fixtures';

export type Check = { name: string; status: 'queued' | 'in_progress' | 'completed'; conclusion?: 'success' | 'failure' | 'skipped' };
export type Comment = { id: number; login: string; body: string; at: string; path?: string; line?: number; inReplyTo?: number; review?: string };

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
};

export class FakeForge {
	url = '';
	pulls: Pull[] = [];
	/** Tokens and whose they are: anything else is refused with a 401. */
	tokens: Record<string, string> = {};
	/** "METHOD path" of each request, for what tests can't see otherwise. */
	calls: string[] = [];
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
		const body = raw ? JSON.parse(raw) : {};
		const token = (req.headers.authorization ?? '').replace(/^Bearer /, '') || String(req.headers['private-token'] ?? '');
		const login = this.tokens[token];
		if (!login) return send(res, 401, { message: 'Bad credentials' });
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
			const p = this.pulls.find((p) => `PR_${p.number}` === body.variables?.id);
			if (!p) return send(res, 200, { errors: [{ message: 'Could not resolve to a node' }] });
			p.draft = String(body.query).includes('convertPullRequestToDraft');
			return send(res, 200, { data: {} });
		}
		const m = path.match(/^\/api\/v3\/repos\/e2e\/(\w+)(\/.*)?$/);
		if (!m) return send(res, 404, { message: 'Not Found' });
		const [, repo, rest = ''] = m;
		let r: RegExpMatchArray | null;
		if (method === 'GET' && rest === '') return send(res, 200, { name: repo, allow_merge_commit: true, allow_squash_merge: true, allow_rebase_merge: false });
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

	private async gitlab(method: string, path: string, url: URL, body: Record<string, any>, login: string, res: ServerResponse) {
		if (method === 'GET' && path === '/api/v4/users') {
			const name = url.searchParams.get('username');
			return send(res, 200, name ? [{ id: this.idOf(name), username: name }] : []);
		}
		const m = path.match(/^\/api\/v4\/projects\/e2e\/(\w+)(\/.*)$/);
		if (!m) return send(res, 404, { message: '404 Not Found' });
		const [, repo, rest] = m;
		const web = `${this.url}/e2e/plain/-/merge_requests`;
		const json = (p: Pull) => ({
			iid: p.number, title: (p.draft ? 'Draft: ' : '') + p.title, description: p.body, draft: p.draft,
			state: p.state === 'open' ? 'opened' : p.state, web_url: `${web}/${p.number}`, author: { username: p.author },
			source_branch: p.head, target_branch: p.base, sha: p.sha, labels: p.labels, changes_count: '1',
			detailed_merge_status: p.draft ? 'draft_status' : 'mergeable', merged_by: p.mergedBy ? { username: p.mergedBy } : null,
			reviewers: p.reviewers.map((u) => ({ id: this.idOf(u), username: u })), head_pipeline: p.checks.length ? { id: p.number } : null
		});
		let r: RegExpMatchArray | null;
		if (method === 'GET' && rest === '/merge_requests') {
			const head = url.searchParams.get('source_branch');
			return send(res, 200, this.pulls.filter((p) => p.state === 'open' && (!head || p.head === head)).map(json));
		}
		if (method === 'POST' && rest === '/merge_requests') {
			const head = await sha(repo, body.source_branch).catch(() => '');
			if (!head) return send(res, 400, { message: 'Source branch does not exist' });
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
		const note = (c: Comment) => ({ id: c.id, body: c.body, author: { username: c.login }, created_at: c.at, position: c.path ? { new_path: c.path, new_line: c.line } : null });
		if (method === 'GET' && sub === '') return send(res, 200, json(p));
		if (method === 'PUT' && sub === '') {
			if (body.title !== undefined) [p.draft, p.title] = [/^draft:\s*/i.test(body.title), body.title.replace(/^draft:\s*/i, '')];
			if (body.description !== undefined) p.body = body.description;
			if (body.reviewer_ids) p.reviewers = body.reviewer_ids.map((id: number) => this.users[id - 1]);
			return send(res, 200, json(p));
		}
		if (method === 'GET' && sub === '/approvals')
			return send(res, 200, { approved_by: p.comments.filter((c) => c.review === 'APPROVED').map((c) => ({ user: { username: c.login } })) });
		if (method === 'GET' && sub === '/discussions') {
			const roots = p.comments.filter((c) => !c.inReplyTo && !c.review);
			return send(res, 200, roots.map((c) => ({ id: `d${c.id}`, individual_note: !c.path, notes: [c, ...p.comments.filter((x) => x.inReplyTo === c.id)].map(note) })));
		}
		if (method === 'POST' && sub === '/discussions') {
			this.comment(p.number, { login, body: body.body });
			return send(res, 201, { id: `d${this.nextId}` });
		}
		if (method === 'POST' && (r = sub.match(/^\/discussions\/d(\d+)\/notes$/))) {
			const parent = p.comments.find((c) => c.id === Number(r![1]))!;
			this.comment(p.number, { login, body: body.body, path: parent.path, line: parent.line, inReplyTo: parent.id });
			return send(res, 201, { id: this.nextId });
		}
		if (method === 'PUT' && sub === '/merge') {
			if (p.state !== 'open' || p.draft) return send(res, 405, { message: '405 Method Not Allowed' });
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
