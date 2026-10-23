<script lang="ts">
	// Settings: the signed-in person's own forge accounts, a row per forge and host the team has set up (GitHub
	// through the team's GitHub App, GitLab through its application per host). Pull requests, comments and pushes
	// from their threads then go out as them; the tokens stay in the API. A forge the team doesn't use isn't shown.
	import { page } from '$app/state';
	import { afterNavigate, replaceState } from '$app/navigation';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { ConnectError } from '@connectrpc/connect';
	import ForgeAccount from '#lib/components/forge-account.svelte';
	import GitHubMark from '#lib/components/github-mark.svelte';
	import GitLabMark from '#lib/components/gitlab-mark.svelte';
	import SettingsHeading from '#lib/components/settings-heading.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { settingsApi } from '#lib/api.js';
	import { connectUrl, github, loadGitHub } from '#lib/github.svelte.js';
	import { gitLabConnectUrl, gitlab, loadGitLab } from '#lib/gitlab.svelte.js';
	import { userName } from '#lib/threads.svelte.js';
	import { since } from '#lib/utils.js';

	// Coming back from GitHub or GitLab: the outcome of the flow (internal/api/github.go, gitlab.go), said once.
	const outcomes: Record<string, Record<string, string>> = {
		github: {
			denied: 'You cancelled on GitHub, so nothing was connected.',
			failed: 'GitHub didn’t finish connecting. Try again; if it keeps failing, the API log says why.',
			state: 'That sign-in link expired or was started in another session. Connect again from here.',
			'no-app': 'The team’s GitHub App isn’t set up yet, so there is nothing to connect to.',
			'cross-site': 'Another site tried to connect your GitHub account, so Banda didn’t. Start connecting from Settings.'
		},
		gitlab: {
			denied: 'You cancelled on GitLab, so nothing was connected. Banda needs both approvals: one for itself, one for git.',
			failed: 'GitLab didn’t finish connecting. Try again; if it keeps failing, the API log says why.',
			state: 'That sign-in link expired or was started in another session. Connect again from here.',
			'no-app': 'The team’s GitLab application isn’t set up for that host yet, so there is nothing to connect to.',
			'cross-site': 'Another site tried to connect your GitLab account, so Banda didn’t. Start connecting from Settings.',
			account: `Both approvals have to come from one GitLab account, and the second came from another than @${page.url.searchParams.get('login')}. Nothing was connected: sign in to GitLab as @${page.url.searchParams.get('login')}, or as the account you mean for both, and connect again.`
		}
	};
	let notice = $state(
		outcomes.github[page.url.searchParams.get('github') ?? ''] ?? outcomes.gitlab[page.url.searchParams.get('gitlab') ?? ''] ?? ''
	);
	// Said once: dropped from the address once the router can.
	afterNavigate(() => {
		if (!page.url.searchParams.has('github') && !page.url.searchParams.has('gitlab')) return;
		const url = new URL(page.url.href);
		url.searchParams.delete('github');
		url.searchParams.delete('gitlab');
		url.searchParams.delete('login');
		replaceState(url, page.state);
	});

	$effect(() => {
		loadGitHub();
		loadGitLab();
	});

	const gh = $derived(github.res);
	const app = $derived(gh?.app);
	const conn = $derived(gh?.connection);
	const host = $derived(app?.host || 'github.com');
	// Someone replaced or removed the team's app: everyone's connection to the old one ended, and this says who and when.
	const change = $derived(gh?.lastChange);
	const ready = $derived(gitlab.res?.hosts.filter((h) => h.app && !h.app.error) ?? []);
	const gitlabBroken = $derived(gitlab.res?.hosts.some((h) => h.app?.error) ?? false);
	// Someone replaced or removed a host's application, which ended everyone's connection through the old one: said
	// to whoever isn't connected there now.
	const ended = $derived(
		gitlab.res?.hosts.filter(
			(h) =>
				!h.connection &&
				((h.lastChange?.action === 'replaced' && h.app?.applicationId === h.lastChange.applicationId) || (h.lastChange?.action === 'removed' && !h.app))
		) ?? []
	);
	// GitLab shows once the team uses it; GitHub unless only GitLab is in use (it is the default forge).
	const showGitLab = $derived(ready.length > 0 || gitlabBroken || ended.length > 0);
	const showGitHub = $derived(!!app || !!gh?.legacyToken || change?.action === 'removed' || !showGitLab);
	const loading = $derived((!gh && !github.error) || (!gitlab.res && !gitlab.error));

	let busy = $state('');
	async function disconnect(forge: 'github' | 'gitlab', on: string, login = '') {
		const ask =
			forge === 'github'
				? `Disconnect GitHub? Banda revokes its access to @${login}. Pull requests and pushes from your threads stop until you connect again.`
				: `Disconnect GitLab on ${on}? Banda revokes both of its tokens for @${login}. Merge requests and pushes from your threads stop until you connect again.`;
		if (!confirm(ask)) return;
		busy = `${forge} ${on}`;
		notice = '';
		try {
			await (forge === 'github' ? settingsApi.disconnectGitHub({}) : settingsApi.disconnectGitLab({ host: on }));
		} catch (e) {
			notice = ConnectError.from(e).rawMessage;
		}
		await (forge === 'github' ? loadGitHub() : loadGitLab());
		busy = '';
	}
</script>

{#snippet unavailable(broken: boolean, forge: 'GitHub' | 'GitLab')}
	<div class="flex flex-wrap items-center gap-x-3 gap-y-3 p-4 text-sm">
		<span class="grid size-9 shrink-0 place-items-center rounded-full bg-muted text-muted-foreground">
			{#if forge === 'GitHub'}<GitHubMark class="size-5" />{:else}<GitLabMark class="size-5" />{/if}
		</span>
		<div class="min-w-0 flex-1 basis-48">
			<p class="font-medium">{forge}</p>
			<p class="text-xs {broken ? 'text-amber-700 dark:text-amber-400' : 'text-muted-foreground'}">
				{#if broken}
					The team’s {forge === 'GitHub' ? 'GitHub App' : 'GitLab application'} needs attention before you can connect.
				{:else}
					Nothing to connect yet: the team’s GitHub App isn’t set up.
				{/if}
			</p>
		</div>
		<Button size="sm" variant="outline" class="ml-auto" href={forge === 'GitHub' ? '#github-app' : '#gitlab-app'}>
			{broken ? 'See what’s wrong' : 'Set it up'}
		</Button>
	</div>
{/snippet}

<section id="accounts" aria-label="Accounts">
	<SettingsHeading title="Accounts" note="connect once, so your threads open pull requests and push as you" error={github.error || gitlab.error} />
	{#if notice}<p class="mb-3 text-sm text-amber-700 dark:text-amber-400" role="status">{notice}</p>{/if}
	<div class="divide-y rounded-lg border">
		{#if loading}
			<p class="p-4 text-sm text-muted-foreground">Loading…</p>
		{:else}
			{#if gh && showGitHub}
				<section id="github" aria-label="GitHub" class="scroll-mt-4">
					{#if app && !app.error}
						<ForgeAccount name="GitHub" {host} showHost={host !== 'github.com'} {conn} {connectUrl} busy={busy === `github ${host}`}
							ondisconnect={() => disconnect('github', host, conn?.login)}>
							{#snippet mark(cls: string)}<GitHubMark class={cls} />{/snippet}
						</ForgeAccount>
					{:else}
						{@render unavailable(!!app?.error, 'GitHub')}
					{/if}
					{#if gh.legacyToken}
						<p class="border-t px-4 py-3 text-xs text-muted-foreground">
							{#if conn && !conn.broken}
								Your <a href="#secrets" class="font-mono underline underline-offset-2">GIT_TOKEN</a> secret is no longer used on {host}. Delete it
								unless you need it elsewhere.
							{:else if app && !app.error}
								Until you connect, Banda uses your <span class="font-mono">GIT_TOKEN</span> secret on {host}. Personal tokens there are deprecated.
							{:else}
								Meanwhile Banda uses your <span class="font-mono">GIT_TOKEN</span> secret.
							{/if}
						</p>
					{/if}
					{#if change && !conn && (change.action === 'removed' ? !app : change.action === 'replaced' && change.slug === app?.slug)}
						<p class="border-t px-4 py-3 text-xs text-muted-foreground" title={change.at && timestampDate(change.at).toLocaleString()}>
							{#if change.action === 'replaced'}
								<span class="font-medium text-foreground">{userName(change.by)}</span> replaced the team’s GitHub App {since(change.at)}{#if change.wasSlug}{' '}(it was {change.wasSlug}{#if change.wasOwner}, owned by {change.wasOwner}{/if}){/if}, which ended every connection to the old one. Connect again to keep working as you.
							{:else}
								<span class="font-medium text-foreground">{userName(change.by)}</span> removed the team’s GitHub App {since(change.at)}{#if change.wasSlug}{' '}({change.wasSlug}){/if}, which ended every connection to it.
							{/if}
						</p>
					{/if}
				</section>
			{/if}
			{#if gitlab.res && showGitLab}
				<section id="gitlab" aria-label="GitLab" class="divide-y scroll-mt-4">
					{#each ready as h (h.host)}
						<ForgeAccount name="GitLab" host={h.host} showHost={ready.length > 1 || h.host !== 'gitlab.com'} conn={h.connection}
							connectUrl={gitLabConnectUrl(h.host)} busy={busy === `gitlab ${h.host}`} ondisconnect={() => disconnect('gitlab', h.host, h.connection?.login)}>
							{#snippet mark(cls: string)}<GitLabMark class={cls} />{/snippet}
							{#snippet note()}GitLab asks you to approve twice: once for Banda, once for git in your threads.{/snippet}
						</ForgeAccount>
						{#if h.app?.needsAttention}
							<p class="px-4 py-3 text-xs text-amber-700 dark:text-amber-400">
								The team’s GitLab application for {h.host} needs attention: GitLab refused its ID or secret, so Banda can’t renew connections
								there. Your tokens are kept; <a href="#gitlab-app" class="underline underline-offset-2">see the application</a>.
							</p>
						{/if}
					{:else}
						{#if gitlabBroken}{@render unavailable(true, 'GitLab')}{/if}
					{/each}
					{#each ended as h (h.host)}
						{@const c = h.lastChange!}
						<p class="px-4 py-3 text-xs text-muted-foreground" title={c.at && timestampDate(c.at).toLocaleString()}>
							<span class="font-medium text-foreground">{userName(c.by)}</span>
							{c.action} the team’s GitLab application for {h.host}
							{since(c.at)}, which ended every connection {c.action === 'replaced' ? 'to the old one. Connect again to keep working as you.' : 'to it.'}
						</p>
					{/each}
					{#if gitlab.res.legacyToken && ready.length}
						<p class="px-4 py-3 text-xs text-muted-foreground">
							{#if ready.every((h) => h.connection && !h.connection.broken)}
								Your <a href="#secrets" class="font-mono underline underline-offset-2">GITLAB_TOKEN</a> or <span class="font-mono">GIT_TOKEN</span>
								secret is no longer used on {ready.map((h) => h.host).join(', ')}. Delete it unless you need it elsewhere.
							{:else}
								Until you connect, Banda uses your <span class="font-mono">GITLAB_TOKEN</span> or <span class="font-mono">GIT_TOKEN</span> secret.
								Personal tokens are deprecated where the team has an application.
							{/if}
						</p>
					{/if}
				</section>
			{/if}
		{/if}
	</div>
</section>
