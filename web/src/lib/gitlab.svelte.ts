// The team's GitLab applications and the caller's connections through them, shared by the two Settings sections
// (components/forge-accounts.svelte, components/gitlab-app.svelte), so connecting in one shows in the other.
import { ConnectError } from '@connectrpc/connect';
import { settingsApi } from './api.js';
import type { GetGitLabResponse } from './gen/banda/v1/banda_pb.js';

export const gitlab = $state<{ res?: GetGitLabResponse; error: string }>({ error: '' });

let pending: Promise<void> | undefined;
/** Loads it once for both sections; again after a change. */
export function loadGitLab() {
	pending ??= settingsApi
		.getGitLab({})
		.then((res) => void Object.assign(gitlab, { res, error: '' }))
		.catch((e) => void (gitlab.error = ConnectError.from(e).rawMessage))
		.finally(() => (pending = undefined));
	return pending;
}

/** Where connecting to host starts: the API sends the browser on to GitLab, twice (internal/api/gitlab.go). */
export const gitLabConnectUrl = (host: string) => `/gitlab/connect?host=${encodeURIComponent(host)}`;
