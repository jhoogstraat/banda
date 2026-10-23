<script lang="ts">
	// Settings: the team's GitHub App, set up by anyone once: create it on GitHub from a manifest, install it on
	// the accounts whose repositories threads use, and each teammate connects under Accounts. Its keys stay in the
	// API. Until it exists this is a guided list of steps; then a summary of the app and where it's installed.
	import { page } from '$app/state';
	import { afterNavigate, replaceState } from '$app/navigation';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { ConnectError } from '@connectrpc/connect';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import GitHubMark from '#lib/components/github-mark.svelte';
	import SettingsHeading from '#lib/components/settings-heading.svelte';
	import SetupSteps from '#lib/components/setup-steps.svelte';
	import * as Avatar from '#lib/components/ui/avatar/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import * as NativeSelect from '#lib/components/ui/native-select/index.js';
	import { settingsApi } from '#lib/api.js';
	import type { GitHubAppChange, GitHubInstallation } from '#lib/gen/banda/v1/banda_pb.js';
	import { connectUrl, github, loadGitHub } from '#lib/github.svelte.js';
	import { userName } from '#lib/threads.svelte.js';
	import { initials, since } from '#lib/utils.js';

	const outcomes: Record<string, string> = {
		failed: 'GitHub didn’t hand the new app over to Banda. Create it again; delete the unfinished one under your GitHub developer settings.',
		state: 'That link from GitHub expired or was started in another session. Create the app again from here.',
		changed:
			'The team’s GitHub App changed while you were on GitHub, so Banda didn’t take the new one. Delete it under your GitHub developer settings, then look again here.'
	};
	let error = $state(outcomes[page.url.searchParams.get('github-app') ?? ''] ?? '');
	// Said once: dropped from the address once the router can.
	afterNavigate(() => {
		if (!page.url.searchParams.has('github-app')) return;
		const url = new URL(page.url.href);
		url.searchParams.delete('github-app');
		replaceState(url, page.state);
	});

	$effect(() => {
		loadGitHub();
	});
	const res = $derived(github.res);
	const app = $derived(res?.app?.error ? undefined : res?.app);
	const broken = $derived(res?.app?.error ?? '');
	// The latest create, replace or remove: shown while it describes what is there now.
	const change = $derived(res?.lastChange);
	const was = (c: GitHubAppChange) => (c.wasSlug ? `, was ${c.wasSlug}${c.wasOwner ? ` owned by ${c.wasOwner}` : ''}` : '');

	// Where it's installed: asked of GitHub only here, once the app exists, without holding up the rest of the page.
	let installs = $state<GitHubInstallation[]>();
	let installsError = $state('');
	// Back from installing it (/github/installed): ask GitHub afresh.
	const installed = page.url.searchParams.get('github-app') === 'installed';
	$effect(() => {
		if (!app?.slug) return;
		installs = undefined;
		settingsApi
			.listGitHubInstallations({ fresh: installed })
			.then((r) => ((installs = r.installations), (installsError = '')))
			.catch((e) => (installsError = ConnectError.from(e).rawMessage));
	});

	// The create form.
	let host = $state('');
	let owner = $state<'me' | 'org'>('org');
	let org = $state('');
	let creating = $state(false);
	// Replacing the app: asked for, with what it does, before the same form.
	let replacing = $state(false);

	async function create(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		creating = true;
		try {
			const r = await settingsApi.gitHubAppManifest({
				host: host || res?.hosts[0],
				organization: owner === 'org' ? org.trim() : '',
				replace: replacing
			});
			// GitHub takes the manifest as a form post, and comes back to /github/app.
			const form = Object.assign(document.createElement('form'), { method: 'post', action: r.url });
			form.append(Object.assign(document.createElement('input'), { type: 'hidden', name: 'manifest', value: r.manifest }));
			document.body.append(form);
			form.submit();
		} catch (err) {
			error = ConnectError.from(err).rawMessage;
			creating = false;
		}
	}

	async function remove() {
		const what = app ? `${app.slug} on ${app.host}` : 'the app';
		if (!confirm(`Remove ${what} from Banda? Everyone’s GitHub connections end, and pushes fall back to GIT_TOKEN secrets. The app stays on GitHub, where its owner can delete it. Settings shows everyone that you removed it.`)) return;
		try {
			await settingsApi.removeGitHubApp({});
			error = '';
		} catch (e) {
			error = ConnectError.from(e).rawMessage;
		}
		await loadGitHub();
	}

	const repos = (i: GitHubInstallation) => (i.allRepositories ? 'All repositories' : i.repositories === 1 ? '1 repository' : `${i.repositories} repositories`);
	const me = $derived(res?.connection);
</script>

{#snippet warning(text: string)}
	<p class="flex gap-1.5 text-xs text-amber-700 dark:text-amber-400">
		<TriangleAlertIcon class="mt-px size-3.5 shrink-0" />
		<span>{text}</span>
	</p>
{/snippet}

{#snippet form(submit: string)}
	<form class="space-y-3" onsubmit={create}>
		{#if res && res.hosts.length > 1}
			<label class="flex items-center gap-2 text-sm">
				<span class="text-muted-foreground">Host</span>
				<NativeSelect.Root size="sm" bind:value={host}>
					{#each res.hosts as h (h)}<NativeSelect.Option value={h}>{h}</NativeSelect.Option>{/each}
				</NativeSelect.Root>
			</label>
		{/if}
		<fieldset class="space-y-2 text-sm">
			<legend class="mb-2 text-muted-foreground">Owned by</legend>
			<div class="flex flex-wrap items-center gap-x-2 gap-y-2">
				<label class="flex items-center gap-1.5">
					<input type="radio" name="gh-owner" class="accent-primary" value="org" bind:group={owner} /> An organization
				</label>
				{#if owner === 'org'}
					<Input class="h-7 w-48" aria-label="Organization" placeholder="organization" autocomplete="off" spellcheck={false} bind:value={org} required />
				{/if}
			</div>
			<label class="flex items-center gap-1.5">
				<input type="radio" name="gh-owner" class="accent-primary" value="me" bind:group={owner} /> My GitHub account
			</label>
		</fieldset>
		{#if owner === 'me'}
			{@render warning(
				'Owned by your account: you’ll be able to reach every repository it’s installed on, whatever your own access. For an organization’s repositories, create it in the organization.'
			)}
		{/if}
		<details class="group/perms text-xs text-muted-foreground">
			<summary class="flex w-fit cursor-pointer list-none items-center gap-1 rounded-sm select-none hover:text-foreground [&::-webkit-details-marker]:hidden">
				<ChevronRightIcon class="size-3.5 transition-transform group-open/perms:rotate-90" />What the app may do
			</summary>
			<ul class="mt-2 list-disc space-y-1 pl-5">
				<li>Read and push code, including workflow files (a branch that changes <span class="font-mono">.github/workflows</span> needs it)</li>
				<li>Open, update, comment on and merge pull requests</li>
				<li>Read checks and commit statuses</li>
			</ul>
			<p class="mt-2">Only on the repositories it is installed on, and never more than the person using it may do.</p>
		</details>
		<div class="flex gap-2">
			<Button type="submit" size="sm" variant={replacing ? 'destructive' : 'default'} disabled={creating || (owner === 'org' && !org.trim())}>
				<GitHubMark class="size-3.5" />
				{creating ? 'Opening GitHub…' : submit}
			</Button>
			{#if replacing}
				<Button size="sm" variant="ghost" onclick={() => (replacing = false)}>Cancel</Button>
			{/if}
		</div>
	</form>
{/snippet}

<section id="github-app" aria-label="GitHub App">
	<SettingsHeading title="GitHub App" note="lets threads push and open pull requests with hour-long tokens for their own repository" {error} />
	{#if res?.refusal}
		<p class="mb-3 text-sm text-destructive" role="alert">
			GitHub made {res.refusal.slug} other than Banda asked for ({res.refusal.reason}), so Banda didn’t keep it.
			<a href={res.refusal.deleteUrl} target="_blank" rel="noreferrer" class="font-medium underline underline-offset-2">Delete it on GitHub</a>, then create the
			app again from here.
		</p>
	{/if}
	{#if !res}
		<p class="rounded-lg border p-4 text-sm text-muted-foreground">{github.error ? 'Couldn’t load.' : 'Loading…'}</p>
	{:else if !res.app}
		{#if change?.action === 'removed'}
			<p class="mb-3 text-xs text-muted-foreground" title={change.at && timestampDate(change.at).toLocaleString()}>
				Removed by {userName(change.by)} · {since(change.at)}{was(change)}
			</p>
		{/if}
		<SetupSteps steps={['Create the app on GitHub', 'Install it where your repositories are', 'Everyone connects their account']} current={1}>
			{#snippet body(n: number)}
				{#if n === 1}
					<p class="max-w-prose text-sm text-muted-foreground">
						Banda sends GitHub a description of the app. GitHub shows you its name and permissions before you confirm, then brings you back here.
						It is private to its owner and has no webhook.
					</p>
					{@render form('Create on GitHub')}
				{/if}
			{/snippet}
		</SetupSteps>
	{:else}
		<div class="divide-y rounded-lg border">
			<div class="flex flex-wrap items-start gap-x-3 gap-y-2 p-4">
				<span class="grid size-9 shrink-0 place-items-center rounded-full bg-muted text-muted-foreground"><GitHubMark class="size-5" /></span>
				{#if app}
					<div class="min-w-0 flex-1 basis-48 space-y-0.5">
						<p class="text-sm">
							<span class="font-medium">{app.slug}</span>
							<span class="text-muted-foreground">on {app.host}{#if app.owner}, owned by {app.owner}{/if}</span>
						</p>
						{#if !app.createdBy}
							<p class="text-xs text-muted-foreground">Set up by the deployment (the Secret banda-github-app): it is changed there, not here.</p>
						{:else if change?.action === 'replaced' && change.slug === app.slug}
							<p class="text-xs text-muted-foreground" title={change.at && timestampDate(change.at).toLocaleString()}>
								Replaced by {userName(change.by)} · {since(change.at)}{was(change)}
							</p>
						{:else}
							<p class="text-xs text-muted-foreground" title={app.createdAt && timestampDate(app.createdAt).toLocaleString()}>
								Set up by {userName(app.createdBy)} · {since(app.createdAt)}
							</p>
						{/if}
					</div>
					<Button size="sm" variant="ghost" class="text-muted-foreground" href={app.url} target="_blank" rel="noreferrer">
						View on GitHub <ExternalLinkIcon />
					</Button>
				{:else}
					<p class="min-w-0 flex-1 basis-48 text-sm text-destructive">
						Banda can’t use the app it has: {broken}. Fix the Secret, or remove it from Banda below and create the app again.
					</p>
				{/if}
			</div>

			{#if app?.personal}
				<div class="px-4 py-3">
					{@render warning(
						`Owned by ${app.owner}’s personal account: they reach every repository it’s installed on, whatever their own access. For an organization’s repositories, create it in the organization.`
					)}
				</div>
			{/if}

			{#if app}
				<div class="space-y-2 p-4">
					{#if installsError}
						<p class="text-sm text-destructive">{installsError}</p>
					{:else if !installs}
						<p class="text-sm text-muted-foreground">Asking GitHub where it’s installed…</p>
					{:else if installs.length}
						<h4 class="text-xs text-muted-foreground">Installed on</h4>
						<ul class="divide-y rounded-md border" aria-label="Installed on">
							{#each installs as inst (inst.account)}
								<li class="flex items-center gap-x-3 px-3 py-2 text-sm">
									<Avatar.Root class="size-6">
										<Avatar.Image src={inst.avatarUrl} alt="" />
										<Avatar.Fallback class="text-[10px]">{initials(inst.account)}</Avatar.Fallback>
									</Avatar.Root>
									<span class="min-w-0 truncate font-medium">{inst.account}</span>
									<span class="shrink-0 text-muted-foreground">{repos(inst)}</span>
									<a href={inst.manageUrl} target="_blank" rel="noreferrer" class="ml-auto inline-flex shrink-0 items-center gap-1 rounded-sm text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline" aria-label="Manage on GitHub: {inst.account}">
										Manage <ExternalLinkIcon class="size-3" />
									</a>
								</li>
							{/each}
						</ul>
						<a href={app.installUrl} class="inline-block rounded-sm text-xs text-muted-foreground underline underline-offset-2 hover:text-foreground">Install on another account</a>
					{:else}
						<p class="flex items-center gap-1.5 text-sm font-medium">
							<span class="size-2 shrink-0 rounded-full bg-amber-400"></span>
							Not installed yet
						</p>
						<p class="max-w-prose text-sm text-muted-foreground">
							Pick the accounts and repositories threads work on. GitHub brings you back here; until then pushes and pull requests use personal tokens.
						</p>
						<Button size="sm" href={app.installUrl}><GitHubMark class="size-3.5" /> Install on GitHub</Button>
					{/if}
				</div>
				{#if installs?.length && (!me || me.broken)}
					<div class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3 text-sm">
						<p class="min-w-0 flex-1 basis-48 text-muted-foreground">
							Everyone connects their own GitHub account once, under Accounts. You haven’t yet.
						</p>
						<Button size="sm" variant="outline" href={connectUrl} data-sveltekit-reload>
							<GitHubMark class="size-3.5" />
							{me?.broken ? 'Reconnect GitHub' : 'Connect GitHub'}
						</Button>
					</div>
				{/if}
			{/if}

			{#if replacing && app}
				<div class="space-y-3 bg-amber-500/5 p-4" role="group" aria-label="Replace the app">
					<p class="max-w-prose text-sm">
						<span class="font-medium">Replacing {app.slug} ends everyone’s GitHub connection.</span>
						Banda revokes them, each teammate connects again, and an owner of
						{installs?.length ? installs.map((i) => i.account).join(', ') : 'each account it’s installed on'} installs the new app there.
						Settings shows everyone that you replaced it. {app.slug} stays on GitHub, where its owner can delete it.
					</p>
					{@render form('Replace on GitHub')}
				</div>
			{/if}
		</div>
		<div class="mt-2 flex justify-end gap-1">
			{#if app?.createdBy && !replacing}
				<Button size="xs" variant="ghost" class="text-muted-foreground" onclick={() => (replacing = true)}>Replace the app…</Button>
			{/if}
			<Button size="xs" variant="ghost" class="text-muted-foreground hover:text-destructive" onclick={remove}>Remove from Banda</Button>
		</div>
	{/if}
</section>
