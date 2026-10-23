// Run: node src/lib/identity-recipes.check.ts
import { execFileSync } from 'node:child_process';
import { marked, recipes, sh } from './identity-recipes.ts';

const ok = (cond: boolean, msg: string) => {
	if (!cond) throw new Error(msg);
};
const v = { issuer: 'https://banda.example', audience: 'vault', path: '/run/banda/id-tokens/vault', repo: 'github.com/acme/api', email: 'ana@acme.io', pems: [] };
const all = (r: ReturnType<typeof recipes>) => r.flatMap((x) => x.steps.map((s) => s.code ?? '')).join('\n');

const filled = recipes(v);
ok(filled.map((r) => r.id).join() === 'vault,infisical,aws,gcp,kubernetes', 'one recipe per target');
for (const r of filled) {
	const code = r.steps.map((s) => s.code ?? '').join('\n');
	ok(code.includes(v.issuer) && code.includes(v.audience) && code.includes(v.path), `${r.id}: issuer, audience and token path filled in`);
	ok(!/undefined|null|\[object/.test(code), `${r.id}: no unfilled value`);
}
ok(all(filled).includes('"repo": "github.com/acme/api"'), 'the vault role binds the repository');
ok(!filled[0].steps.some((s) => s.optional), 'no keys, no offline step');
ok(!all(recipes({ ...v, repo: '' })).includes('github.com/acme/api'), 'any repository binds none');
for (const r of filled) ok(all([r]).includes(v.email), `${r.id}: binds the person, as the repo claim grants nothing alone`);
for (const id of ['vault', 'gcp', 'kubernetes']) ok(all(filled.filter((r) => r.id === id)).includes('email_verified'), `${id}: binds email_verified`);
ok(!/\.banda\/(gcp\.json|kubeconfig)/.test(all(filled)), 'no credential file goes into the repository');
ok(!all(filled).includes('ReadOnlyAccess') && !all(filled).includes('add-iam-policy-binding "$PROJECT"'), 'no broad grants');
ok(recipes({ ...v, pems: ['A', 'B'] })[0].steps[1].code!.includes('jwt_validation_pubkeys=@banda-2.pem'), 'every published key is trusted offline');
ok(all(filled).includes('"bound_audiences": ["vault"]'), 'one-item arrays on one line');
const http = recipes({ ...v, issuer: 'http://banda.lan', pems: ['A'] })[0].steps;
ok(http[0].code!.includes('jwt_validation_pubkeys') && !http[0].optional && !!http[1].optional && http[1].code!.includes('oidc_discovery_url'), 'over http the vault trusts the keys, discovery folded away');
ok(!recipes({ ...v, issuer: 'http://banda.lan' })[0].steps[0].optional, 'over http without keys, discovery it is');

// Every value Banda fills into shell is one word whatever it holds: the shell gives back exactly the value.
const echo = (word: string) => execFileSync('sh', ['-c', `printf %s ${word}`]).toString();
for (const x of ['plain/x.y@z', "it's", 'a b', '$(touch /tmp/pwned)', '`id`', '"q"', "''", '\\n;|&<>*?[]{}!#~'])
	ok(echo(sh(x)) === x, `sh(${JSON.stringify(x)}) quotes it`);
ok(sh('/run/banda/id-tokens/vault') === '/run/banda/id-tokens/vault', 'plain values stay readable');
const evil = { ...v, issuer: "https://b.example/'$(id)", audience: 'a$(id)', email: "o'neil$(id)@acme.io" };
/** The line without its single-quoted parts and escapes: what the shell would expand. */
const unquoted = (line: string) => {
	let out = '';
	for (let i = 0, q = false; i < line.length; i++) {
		if (line[i] === "'") q = !q;
		else if (!q && line[i] === '\\') i++;
		else if (!q) out += line[i];
	}
	return out;
};
for (const st of recipes(evil).flatMap((r) => r.steps.filter((s) => s.lang === 'sh'))) {
	let heredoc = ''; // a quoted heredoc's terminator while inside one: its lines are data
	for (const line of st.code!.split('\n')) {
		if (heredoc) heredoc = line === heredoc ? '' : heredoc;
		else {
			ok(!unquoted(line).includes('$(id)'), `${st.title}: unquoted value in ${line}`);
			heredoc = line.match(/<<'(\w+)'/)?.[1] ?? '';
			ok(!/<<\w/.test(line) || !line.includes('$(id)'), `${st.title}: a value in an unquoted heredoc`);
		}
	}
}
const runs = marked('jwt=@/run/banda/id-tokens/vault vault', [v.path, v.audience, '']);
ok(JSON.stringify(runs.map((r) => [r.text, r.filled])) === JSON.stringify([['jwt=@', false], [v.path, true], [' ', false], ['vault', true]]), 'the longest value marks first');
console.log('identity-recipes ok');
