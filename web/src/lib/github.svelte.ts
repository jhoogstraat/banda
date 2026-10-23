// The team's GitHub App and the caller's connection to it, shared by the two Settings sections
// (components/forge-accounts.svelte, components/github-app.svelte), so connecting in one shows in the other.
import { ConnectError } from '@connectrpc/connect';
import { settingsApi } from './api.js';
import { gitLabConnectUrl } from './gitlab.svelte.js';
import type { GetGitHubResponse } from './gen/banda/v1/banda_pb.js';

export const github = $state<{ res?: GetGitHubResponse; error: string }>({ error: '' });

let pending: Promise<void> | undefined;
/** Loads it once for both sections; again after a change. */
export function loadGitHub() {
	pending ??= settingsApi
		.getGitHub({})
		.then((res) => void Object.assign(github, { res, error: '' }))
		.catch((e) => void (github.error = ConnectError.from(e).rawMessage))
		.finally(() => (pending = undefined));
	return pending;
}

/** Where connecting starts: the API sends the browser on to GitHub (internal/api/github.go). */
export const connectUrl = '/github/connect';

/**
 * The action a forge error names (see noToken and GitCredential in internal/api), as a link: connect or reconnect
 * GitHub or GitLab, install or check the team's GitHub App, set up or check GitLab, or add a secret. Undefined if it names none.
 * On a thread's page (thread), connecting goes straight to the forge and comes back to the thread; elsewhere to Settings.
 */
export function forgeAction(
	message: string,
	thread?: { id: string; repoUrl: string }
): { label: string; href: string; external?: boolean; connects?: 'github' | 'gitlab'; host?: string } | undefined {
	const back = thread ? `back=${encodeURIComponent(`/t/${thread.id}`)}` : '';
	const gitlabHost = thread?.repoUrl ? URL.parse(thread.repoUrl)?.host : undefined;
	for (const [re, label] of [[/\breconnect GitHub in Settings/i, 'Reconnect GitHub'], [/\bconnect GitHub in Settings/i, 'Connect GitHub']] as const)
		if (re.test(message)) return { label, href: back ? `${connectUrl}?${back}` : '/settings#github', connects: 'github' };
	for (const [re, label] of [[/\breconnect GitLab in Settings/i, 'Reconnect GitLab'], [/\bconnect GitLab in Settings/i, 'Connect GitLab']] as const)
		if (re.test(message))
			return { label, href: back && gitlabHost ? `${gitLabConnectUrl(gitlabHost)}&${back}` : '/settings#gitlab', connects: 'gitlab', host: gitlabHost };
	if (/\bset up GitLab for \S+ in Settings/i.test(message)) return { label: 'Set up GitLab', href: '/settings#gitlab-app' };
	if (/\bsee GitLab in Settings|\bcheck the GitLab application in Settings/i.test(message)) return { label: 'Check GitLab', href: '/settings#gitlab-app' };
	if (/\bcheck the (GitHub )?app in Settings/i.test(message)) return { label: 'Check the GitHub App', href: '/settings#github-app' };
	const install = message.match(/https?:\/\/\S+\/installations\/new/);
	if (install) return { label: 'Install the GitHub App', href: install[0], external: true };
	const secret = message.match(/\badd ([A-Z_]+)(?: or [A-Z_]+)?(?: secret)? in Settings/);
	if (secret) return { label: `Add ${secret[1]} in Settings`, href: `/settings?secret=${secret[1]}#secrets` };
}
