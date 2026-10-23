<script lang="ts">
	// Settings: the team's GitLab application, one per GitLab host, set up by anyone once. GitLab has no way to
	// create one for Banda, so this walks through making it by hand: where, with which values, then its ID and
	// secret pasted here, checked with GitLab and kept in the API. Then each teammate connects under Accounts.
	// A team that doesn't use GitLab sees one quiet line until someone opens the walk-through.
	import { page } from '$app/state';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { ConnectError } from '@connectrpc/connect';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import CopyText from '#lib/components/copy-text.svelte';
	import GitLabMark from '#lib/components/gitlab-mark.svelte';
	import SettingsHeading from '#lib/components/settings-heading.svelte';
	import SetupSteps from '#lib/components/setup-steps.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import * as NativeSelect from '#lib/components/ui/native-select/index.js';
	import { settingsApi } from '#lib/api.js';
	import { gitLabConnectUrl, gitlab, loadGitLab } from '#lib/gitlab.svelte.js';
	import { userName } from '#lib/threads.svelte.js';
	import { since } from '#lib/utils.js';

	$effect(() => {
		loadGitLab();
	});
	const res = $derived(gitlab.res);
	const setUp = $derived(res?.hosts.filter((x) => x.app) ?? []);
	const unset = $derived(res?.hosts.filter((x) => !x.app) ?? []);

	// The walk-through: open when asked for (a link to #gitlab-app, "Set up GitLab"), or replacing a host's application.
	let walking = $state(false);
	let replacing = $state('');
	$effect(() => {
		if (page.url.hash === '#gitlab-app') walking = true;
	});
	const flow = $derived(!!replacing || (walking && unset.length > 0));
	let pick = $state('');
	// The host walked through: the one replaced, else the one picked, else the first not set up.
	const h = $derived(
		res?.hosts.find((x) => x.host === replacing) ?? unset.find((x) => x.host === pick) ?? unset[0]
	);

	// Step 1: where the application lives. Not a person's account: whoever can edit it can take everyone's tokens.
	let owner = $state<'group' | 'admin'>('group');
	let group = $state('');
	const groupPath = $derived(group.trim().replace(/^\/+|\/+$/g, ''));
	const where = $derived(!h ? '' : owner === 'admin' ? h.adminAppsUrl : groupPath ? h.groupAppsUrl.replace('{group}', groupPath) : '');
	const owners = $derived(
		[
			['group', 'A group', 'Recommended: one with few Owners. It outlives any one person.'],
			['admin', 'The instance', 'Needs an administrator. Admins can mark it trusted, which skips GitLab’s consent screens.']
		].filter(([o]) => o !== 'admin' || h?.adminAppsUrl)
	);
	let opened = $state(false);

	// Step 2: its ID and secret.
	let appId = $state('');
	let secret = $state('');
	let saving = $state(false);
	let saveError = $state('');
	// Removing one.
	let error = $state('');

	function close() {
		walking = opened = false;
		replacing = pick = saveError = '';
		owner = 'group';
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!h) return;
		saveError = '';
		saving = true;
		try {
			await settingsApi.setGitLabApp({ host: h.host, applicationId: appId.trim(), secret: secret.trim(), replace: !!replacing });
			appId = secret = '';
			close();
		} catch (err) {
			saveError = ConnectError.from(err).rawMessage;
		}
		await loadGitLab();
		saving = false;
	}

	async function remove(host: string) {
		if (!confirm(`Remove the GitLab application on ${host} from Banda? Everyone’s GitLab connections there end and are revoked on GitLab, and pushes fall back to GIT_TOKEN secrets. The application stays on GitLab, where its owner can delete it. Settings shows everyone that you removed it.`)) return;
		try {
			await settingsApi.removeGitLabApp({ host });
			error = '';
		} catch (e) {
			error = ConnectError.from(e).rawMessage;
		}
		await loadGitLab();
	}

	// A host set up that I haven't connected to: the one nudge this section gives me.
	const unconnected = $derived(setUp.find((x) => x.app && !x.app.error && (!x.connection || x.connection.broken)));
	const short = (id: string) => (id.length > 12 ? `${id.slice(0, 12)}…` : id);
	// Hosts whose application someone removed: who and when, so a removal is never silent.
	const removed = $derived(unset.filter((x) => x.lastChange?.action === 'removed'));
	const replacingApp = $derived(res?.hosts.find((x) => x.host === replacing)?.app);
</script>

{#snippet value(label: string, text: string, note: string)}
	<div class="grid gap-x-3 gap-y-0.5 px-3 py-2 sm:grid-cols-[9rem_1fr] sm:items-baseline">
		<dt class="text-muted-foreground">{label}</dt>
		<dd class="min-w-0">
			<span class="flex items-center gap-1">
				<code class="min-w-0 truncate font-mono text-xs text-foreground">{text}</code>
				<CopyText text={text} label="Copy the {label.toLowerCase()}" />
			</span>
			{#if note}<span class="block text-xs text-muted-foreground">{note}</span>{/if}
		</dd>
	</div>
{/snippet}

<section id="gitlab-app" aria-label="GitLab application">
	<SettingsHeading title="GitLab application" note="the same for GitLab: two-hour, git-only tokens, one application per host" {error} />
	{#if !res}
		<p class="rounded-lg border p-4 text-sm text-muted-foreground">{gitlab.error ? 'Couldn’t load.' : 'Loading…'}</p>
	{:else if !res.hosts.length}
		<p class="rounded-lg border p-4 text-sm text-muted-foreground">Banda knows no GitLab host: name self-hosted ones in FORGE_HOSTS.</p>
	{:else}
		<div class="space-y-3">
			{#if setUp.length}
				<ul class="divide-y rounded-lg border" aria-label="Set up">
					{#each setUp as x (x.host)}
						{@const c = x.lastChange}
						<li class="flex flex-wrap items-start gap-x-3 gap-y-2 p-4">
							<span class="grid size-9 shrink-0 place-items-center rounded-full bg-muted text-muted-foreground"><GitLabMark class="size-5" /></span>
							<div class="min-w-0 flex-1 basis-48 space-y-0.5">
								<p class="text-sm">
									<span class="font-medium">{x.host}</span>
									{#if x.app && !x.app.error}
										<span class="text-muted-foreground">
											application <code class="font-mono text-xs">{short(x.app.applicationId)}</code>, checked with GitLab
										</span>
									{/if}
								</p>
								{#if x.app?.error}
									<p class="text-sm text-destructive">Banda can’t use the application it has: {x.app.error}. Fix the Secret, or remove it from Banda and set it up again.</p>
								{:else if !x.app?.createdBy}
									<p class="text-xs text-muted-foreground">Set up by the deployment (the Secret banda-gitlab-app-{x.host.replace(':', '-')}): it is changed there, not here.</p>
								{:else if c?.action === 'replaced' && c.applicationId === x.app.applicationId}
									<p class="text-xs text-muted-foreground" title={c.at && timestampDate(c.at).toLocaleString()}>
										Replaced by {userName(c.by)} · {since(c.at)}{#if c.wasApplicationId && c.wasApplicationId !== x.app.applicationId}, was {short(c.wasApplicationId)}{/if}
									</p>
								{:else}
									<p class="text-xs text-muted-foreground" title={x.app.createdAt && timestampDate(x.app.createdAt).toLocaleString()}>
										Set up by {userName(x.app.createdBy)} · {since(x.app.createdAt)}
									</p>
								{/if}
								{#if x.app?.needsAttention}
									<p class="flex gap-1.5 text-xs text-amber-700 dark:text-amber-400">
										<TriangleAlertIcon class="mt-px size-3.5 shrink-0" />
										<span>
											GitLab refused this application’s ID or secret when renewing a connection: someone renewed the secret or deleted the
											application on GitLab. {x.app.createdBy ? 'Replace it with the current ones' : 'Update its Secret'}; connections keep their tokens meanwhile.
										</span>
									</p>
								{/if}
							</div>
							<div class="ml-auto flex gap-1">
								{#if replacing !== x.host && x.app?.createdBy && !x.app.error}
									<Button size="xs" variant="ghost" class="text-muted-foreground" aria-label="Replace the application on {x.host}"
										onclick={() => ((replacing = x.host), (opened = false))}>Replace</Button>
								{/if}
								<Button size="xs" variant="ghost" class="text-muted-foreground hover:text-destructive" aria-label="Remove the application on {x.host} from Banda"
									onclick={() => remove(x.host)}>Remove from Banda</Button>
							</div>
						</li>
					{/each}
					{#if unconnected && !flow}
						<li class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3 text-sm">
							<p class="min-w-0 flex-1 basis-48 text-muted-foreground">
								Everyone connects their own GitLab account once, under Accounts. You haven’t yet{setUp.length > 1 ? ` on ${unconnected.host}` : ''}.
							</p>
							<Button size="sm" variant="outline" href={gitLabConnectUrl(unconnected.host)} data-sveltekit-reload>
								<GitLabMark class="size-3.5" />
								{unconnected.connection?.broken ? 'Reconnect GitLab' : 'Connect GitLab'}
							</Button>
						</li>
					{/if}
				</ul>
			{/if}

			{#each removed as x (x.host)}
				{@const c = x.lastChange!}
				<p class="text-xs text-muted-foreground" title={c.at && timestampDate(c.at).toLocaleString()}>
					{userName(c.by)} removed the application on {x.host} · {since(c.at)}{#if c.wasApplicationId}, was {short(c.wasApplicationId)}{/if}
				</p>
			{/each}

			{#if flow && h}
				{#if !replacing && unset.length > 1}
					<label class="flex items-center gap-2 text-sm">
						<span class="text-muted-foreground">Host</span>
						<NativeSelect.Root size="sm" value={h.host} onchange={(e) => ((pick = e.currentTarget.value), (opened = false), (owner = 'group'))}>
							{#each unset as x (x.host)}<NativeSelect.Option value={x.host}>{x.host}</NativeSelect.Option>{/each}
						</NativeSelect.Root>
					</label>
				{/if}
				<SetupSteps
					steps={[replacing ? `Create another application on ${h.host}` : 'Create an application on GitLab', 'Paste its ID and secret here', 'Everyone connects their account']}
					current={opened || replacing ? 2 : 1}
				>
					{#snippet body(n: number)}
						{#if n === 1}
							<p class="max-w-prose text-sm text-muted-foreground">
								GitLab has no way to create it for Banda, so this takes a minute by hand. Pick who owns it on {h.host}, open its applications page,
								and fill in the values below.
							</p>
							<fieldset class="space-y-2 text-sm">
								<legend class="sr-only">Owned by</legend>
								{#each owners as [o, label, note] (o)}
									<label class="flex items-start gap-2">
										<input type="radio" name="gl-owner" class="mt-1 accent-primary" value={o} bind:group={owner} />
										<span>
											{label}
											<span class="block text-xs text-muted-foreground">{note}</span>
										</span>
									</label>
									{#if o === 'group' && owner === 'group'}
										<Input class="ml-5 h-7 w-[calc(100%-1.25rem)] max-w-64" aria-label="Group path" placeholder="group or group/subgroup" autocomplete="off" spellcheck={false} bind:value={group} />
									{/if}
								{/each}
							</fieldset>
							<p class="max-w-prose text-xs text-muted-foreground">
								Whoever can edit the application on GitLab ({owner === 'admin' ? 'the instance’s administrators' : 'the group’s Owners'}) can get tokens
								for every teammate who connects, so keep that circle small.
							</p>
							<dl class="divide-y rounded-md border text-sm" aria-label="Values for GitLab">
								{@render value('Name', 'Banda', 'Anything you like: people see it when they connect.')}
								{@render value('Redirect URI', res.redirectUri, 'Exactly this; GitLab sends people back here.')}
								<div class="grid gap-x-3 gap-y-0.5 px-3 py-2 sm:grid-cols-[9rem_1fr] sm:items-baseline">
									<dt class="text-muted-foreground">Confidential</dt>
									<dd class="text-xs text-muted-foreground"><span class="text-sm text-foreground">Ticked</span> (the default): only Banda knows its secret.</dd>
								</div>
								<div class="grid gap-x-3 gap-y-0.5 px-3 py-2 sm:grid-cols-[9rem_1fr] sm:items-baseline">
									<dt class="text-muted-foreground">Scopes</dt>
									<dd class="space-y-1">
										<span class="flex flex-wrap gap-1.5">
											{#each res.scopes as s (s)}<code class="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">{s}</code>{/each}
										</span>
										<span class="block text-xs text-muted-foreground">
											Only these two. <span class="font-mono">api</span> is for merge requests and stays in Banda;
											<span class="font-mono">write_repository</span> is all a sandbox ever gets.
										</span>
									</dd>
								</div>
							</dl>
							<div class="flex flex-wrap items-center gap-x-3 gap-y-1">
								{#if where}
									<Button size="sm" variant={opened ? 'outline' : 'default'} href={where} target="_blank" rel="noreferrer" onclick={() => (opened = true)}>
										<GitLabMark class="size-3.5" /> Open GitLab’s applications page <ExternalLinkIcon />
									</Button>
								{:else}
									<Button size="sm" disabled><GitLabMark class="size-3.5" /> Open GitLab’s applications page</Button>
									<span class="text-xs text-muted-foreground">Name the group first.</span>
								{/if}
							</div>
						{:else if n === 2}
							<form class="space-y-3" onsubmit={save}>
								<p class="max-w-prose text-sm text-muted-foreground">
									GitLab shows both once you save the application. Banda checks them with GitLab and keeps them where no sandbox can read them.
								</p>
								<div class="grid max-w-md gap-2 text-sm">
									<label class="grid gap-1">
										<span class="text-muted-foreground">Application ID</span>
										<Input class="h-8 font-mono text-xs" autocomplete="off" spellcheck={false} bind:value={appId} required />
									</label>
									<label class="grid gap-1">
										<span class="text-muted-foreground">Secret</span>
										<Input class="h-8 font-mono text-xs" type="password" autocomplete="off" bind:value={secret} required />
									</label>
								</div>
								{#if saveError}<p class="text-sm text-destructive" role="alert">{saveError}</p>{/if}
								{#if replacing && replacingApp}
									<p class="max-w-prose text-xs text-amber-700 dark:text-amber-400" role="group" aria-label="Replace the application">
										Replacing application {short(replacingApp.applicationId)} ends everyone’s GitLab connection on {h.host}: Banda revokes them and
										each teammate connects again. A new secret of the same application keeps them. Settings shows everyone that you replaced it.
									</p>
								{/if}
								<Button type="submit" size="sm" variant={replacing ? 'destructive' : 'default'} disabled={saving || !appId.trim() || !secret.trim()}>
									{saving ? 'Checking with GitLab…' : replacing ? 'Replace and check' : 'Save and check'}
								</Button>
							</form>
						{/if}
					{/snippet}
				</SetupSteps>
				<div class="flex justify-end">
					<Button size="xs" variant="ghost" class="text-muted-foreground" onclick={close}>Cancel</Button>
				</div>
			{:else if !setUp.length}
				<div class="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border p-4 text-sm">
					<span class="grid size-9 shrink-0 place-items-center rounded-full bg-muted text-muted-foreground"><GitLabMark class="size-5" /></span>
					<p class="min-w-0 flex-1 basis-48 text-muted-foreground">
						Not set up. Only needed if the team’s repositories are on GitLab; until then, threads there push with a GIT_TOKEN secret.
					</p>
					<Button size="sm" variant="outline" class="ml-auto" onclick={() => (walking = true)}>
						<GitLabMark class="size-3.5" /> Set up GitLab
					</Button>
				</div>
			{:else if unset.length}
				<Button size="xs" variant="ghost" class="text-muted-foreground" onclick={() => (walking = true)}>Set up another GitLab host…</Button>
			{/if}
		</div>
	{/if}
</section>
