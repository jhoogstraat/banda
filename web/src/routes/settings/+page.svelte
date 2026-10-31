<script lang="ts">
	import { confirmAction } from '#lib/components/confirm-dialog.svelte';
	import { page } from '$app/state';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import LoginDialog from '#lib/components/login-dialog.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import TeamSecrets from '#lib/components/team-secrets.svelte';
	import WorkloadIdentity from '#lib/components/workload-identity.svelte';
	import ForgeAccounts from '#lib/components/forge-accounts.svelte';
	import GitHubApp from '#lib/components/github-app.svelte';
	import GitLabApp from '#lib/components/gitlab-app.svelte';
	import SettingsHeading from '#lib/components/settings-heading.svelte';
	import { github } from '#lib/github.svelte.js';
	import { gitlab } from '#lib/gitlab.svelte.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { ConnectError } from '@connectrpc/connect';
	import { settingsApi } from '#lib/api.js';
	import {
		AuthMethod,
		LoginAction,
		LoginState,
		PermissionMode,
		type GitIdentity,
		type HarnessAccount,
		type SecretInfo,
		type ThreadDefaults
	} from '#lib/gen/banda/v1/banda_pb.js';
	import { profiles } from '#lib/profiles.js';
	import { efforts, models } from '#lib/components/option-menu.svelte';
	import * as NativeSelect from '#lib/components/ui/native-select/index.js';
	import { permissionLabel, permissionModes } from '#lib/permissions.js';
	import { shortcuts } from '#lib/shortcuts.svelte.js';
	import { askNotify, notifyKinds, setNotifyKind, threads } from '#lib/threads.svelte.js';
	import type { AlertKind } from '#lib/inbox.js';
	import { since } from '#lib/utils.js';
	import { setMode, userPrefersMode } from 'mode-watcher';

	let accounts = $state<HarnessAccount[]>([]);
	let secrets = $state<SecretInfo[]>([]);
	let defaultProfile = $state('');
	let gitIdentity = $state<GitIdentity>();
	let threadDefaults = $state<ThreadDefaults>();
	let loaded = $state(false);
	// An error and the section it belongs to.
	let error = $state('');
	let errorAt = $state<'accounts' | 'git' | 'defaults' | 'secrets'>('accounts');

	const message = (e: unknown) => ConnectError.from(e).rawMessage;

	// Only the latest load applies: an earlier one can answer last, with what was there before.
	let loads = 0;
	async function load() {
		const n = ++loads;
		try {
			const res = await settingsApi.getSettings({});
			if (n !== loads) return;
			({ accounts, secrets, defaultProfile, gitIdentity, threadDefaults } = res);
			if (!loaded) ({ name: gitName, email: gitEmail } = gitIdentity ?? { name: '', email: '' });
			loaded = true;
			error = '';
		} catch (e) {
			error = message(e);
		}
	}
	$effect(() => {
		load();
	});

	// Runs a change, then reloads. Errors (also the server's validation) show in the section `at`.
	async function act(at: typeof errorAt, call: Promise<unknown>) {
		errorAt = at;
		try {
			await call;
		} catch (e) {
			error = message(e);
			return false;
		}
		await load();
		return true;
	}

	// [dot colour, label] of an account.
	function status(a: HarnessAccount): [string, string] {
		if (a.authMethod === AuthMethod.API_KEY) return a.apiKeySet ? ['bg-success', 'Connected (API key)'] : ['bg-warning', 'No API key'];
		if (a.loginState === LoginState.LOGGED_IN) return ['bg-success', 'Connected (subscription)'];
		if (a.loginState === LoginState.LOGGED_OUT) return ['bg-warning', 'Not logged in'];
		return ['border border-muted-foreground', 'Not checked yet'];
	}

	// The login dialog, and per harness the method picked but not saved yet: being saved, or API key waiting for a key.
	// Dropping the pick (also when saving fails) shows the server's method again.
	let login = $state({ open: false, action: LoginAction.LOGIN, harness: '', name: '' });
	let chosen = $state<Record<string, AuthMethod>>({});
	let keys = $state<Record<string, string>>({});

	async function pick(a: HarnessAccount, method: AuthMethod) {
		chosen[a.harness] = method;
		if (method === AuthMethod.API_KEY && !a.apiKeySet) return;
		if (method !== a.authMethod) await act('accounts', settingsApi.setAuthMethod({ harness: a.harness, authMethod: method }));
		delete chosen[a.harness];
	}

	async function saveKey(a: HarnessAccount) {
		const apiKey = keys[a.harness]?.trim();
		if (!apiKey) return;
		if (await act('accounts', settingsApi.setAuthMethod({ harness: a.harness, authMethod: AuthMethod.API_KEY, apiKey }))) {
			keys[a.harness] = '';
			delete chosen[a.harness];
		}
	}

	// Shows the pick at once, and the saved one again if saving fails.
	async function setProfile(name: string) {
		const before = defaultProfile;
		defaultProfile = name;
		if (!(await act('defaults', settingsApi.setDefaultProfile({ profile: name })))) defaultProfile = before;
	}

	// What new threads start with: Banda's default until changed (the API's, as in the composer). The latest pick
	// shows until it is saved and reloaded, so quick changes build on each other, not on a reload still on its way.
	type Defaults = { model: string; effort: string; permissions: string };
	let picked = $state.raw<Defaults>();
	const shown = $derived<Defaults>(
		picked ?? {
			model: threadDefaults?.model || models[0],
			effort: threadDefaults?.effort || 'high',
			permissions: permissionLabel(threadDefaults?.permissionMode || PermissionMode.BYPASS)
		}
	);
	async function setDefaults(change: Partial<Defaults>) {
		const d = (picked = { ...shown, ...change });
		await act('defaults', settingsApi.setThreadDefaults({ defaults: { model: d.model, effort: d.effort, permissionMode: permissionModes[d.permissions] } }));
		if (picked === d) picked = undefined; // saved and reloaded, or failed: the server's again
	}

	// Git identity: empty fields use the sign-in's name and email.
	let gitName = $state('');
	let gitEmail = $state('');
	async function saveGit(e: SubmitEvent) {
		e.preventDefault();
		await act('git', settingsApi.setGitIdentity({ name: gitName, email: gitEmail }));
	}
	const commitsBy = $derived(
		`${gitIdentity?.name || gitIdentity?.signInName || 'Banda'} <${gitIdentity?.email || gitIdentity?.signInEmail || 'banda@localhost'}>`
	);

	const kinds: [AlertKind, string, string][] = [
		['asks', 'My agent asks', 'a permission, a plan, a question, or a teammate asks to run it'],
		['done', 'My agent finishes or fails', ''],
		['mention', 'Mentions', 'also a thread handed to me'],
		['login', 'Login needed', 'Claude Code needs me to log in again']
	];
	// The browser's permission: 'denied' also where there are no notifications.
	const permission = $derived(
		typeof Notification === 'undefined'
			? 'This browser has no notifications.'
			: threads.notify === 'granted'
				? 'On in this browser.'
				: threads.notify === 'denied'
					? 'Blocked: allow notifications for this site in the browser’s settings.'
					: 'Off: the browser hasn’t been asked yet.'
	);

	// Two groups: what is yours (applies to you only), and what the team shares (applies to everyone).
	const groups: [string, string, [string, string][]][] = [
		[
			'You',
			'',
			[
				['claude', 'Claude Code'],
				['accounts', 'Accounts'],
				['git', 'Git identity'],
				['defaults', 'New threads'],
				['notifications', 'Notifications'],
				['appearance', 'Appearance'],
				['secrets', 'Secrets'],
				['shortcuts', 'Keyboard shortcuts']
			]
		],
		[
			'Team',
			'Shared by everyone: a change here applies to every teammate.',
			[
				['github-app', 'GitHub App'],
				['gitlab-app', 'GitLab'],
				['team-secrets', 'Team secrets'],
				['workload-identity', 'Workload identity']
			]
		]
	];
	const sections = groups.flatMap(([, , s]) => s);
	// What waits on me, marked in the nav: Claude Code to log in, an account to connect to a forge the team set up.
	const attention = $derived({
		claude: accounts.some((a) => a.authMethod !== AuthMethod.API_KEY && a.loginState === LoginState.LOGGED_OUT),
		accounts:
			(!!github.res?.app && !github.res.app.error && (!github.res.connection || github.res.connection.broken)) ||
			!!gitlab.res?.hosts.some((h) => h.app && !h.app.error && (!h.connection || h.connection.broken))
	} as Record<string, boolean>);
	// The nav marks the section in view: the last whose top has scrolled past the top, the last one at the bottom.
	let scroller = $state<HTMLElement>();
	let current = $state('claude');
	function spy() {
		if (!scroller) return;
		const top = scroller.getBoundingClientRect().top + 32;
		let at = sections[0][0];
		for (const [id] of sections) if ((document.getElementById(id)?.getBoundingClientRect().top ?? Infinity) <= top) at = id;
		if (scroller.scrollTop > 0 && scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 2) at = sections.at(-1)![0];
		current = at;
	}
	$effect(spy);

	// Secrets: the add form (?secret=NAME fills in the name, e.g. from a thread that needs GIT_TOKEN), and the row being replaced.
	let name = $state(page.url.searchParams.get('secret') ?? '');
	let value = $state('');
	let replacing = $state('');
	let newValue = $state('');
	const validName = (n: string) => /^[A-Z_][A-Z0-9_]*$/.test(n);

	async function add(e: SubmitEvent) {
		e.preventDefault();
		if (!validName(name)) {
			errorAt = 'secrets';
			error = 'Names are environment variables: capital letters, digits and _, not starting with a digit.';
			return;
		}
		if (await act('secrets', settingsApi.setSecret({ name, value }))) name = value = '';
	}

	async function replace(e: SubmitEvent) {
		e.preventDefault();
		if (await act('secrets', settingsApi.setSecret({ name: replacing, value: newValue }))) replacing = newValue = '';
	}

	async function remove(n: string) {
		if (await confirmAction({ title: `Delete the secret ${n}?`, body: "Environments started afterwards won't have it.", action: 'Delete secret' })) act('secrets', settingsApi.deleteSecret({ name: n }));
	}
</script>

{#snippet heading(at: typeof errorAt | '', text: string, note: string)}
	<SettingsHeading title={text} {note} error={errorAt === at ? error : ''} />
{/snippet}

{#snippet group(name: string, note: string)}
	<div class="-mb-4 border-b pb-2 not-first:mt-10">
		<h2 class="text-base font-semibold">{name}</h2>
		{#if note}<p class="text-xs text-muted-foreground">{note}</p>{/if}
	</div>
{/snippet}

<div class="flex min-w-0 flex-1 flex-col">
	<PageHeader><h1 class="text-sm font-medium">Settings</h1></PageHeader>

	<div class="min-h-0 flex-1 overflow-y-auto" bind:this={scroller} onscroll={spy}>
		<!-- The section nav: a sticky column on desktop, a row of links above the sections on a phone. -->
		<div class="mx-auto w-full max-w-5xl p-4 md:flex md:gap-8">
			<nav aria-label="Settings sections" class="mb-6 md:sticky md:top-4 md:mb-0 md:w-44 md:shrink-0 md:self-start">
				{#each groups as [name, , items] (name)}
					<p class="px-2 pt-3 pb-1 text-xs font-medium text-muted-foreground first:pt-0 max-md:pt-1" id="nav-{name}">{name}</p>
					<ul class="flex flex-wrap gap-1 md:flex-col max-md:mb-1" aria-labelledby="nav-{name}">
						{#each items as [id, label] (id)}
							<li>
								<a
									href="#{id}"
									aria-current={current === id ? 'location' : undefined}
									class="flex items-center gap-2 rounded-md px-2 py-1 text-sm text-muted-foreground hover:bg-accent hover:text-foreground aria-[current=location]:bg-accent aria-[current=location]:font-medium aria-[current=location]:text-foreground"
								>
									{label}
									{#if attention[id]}<span class="size-1.5 shrink-0 rounded-full bg-warning" aria-hidden="true"></span><span class="sr-only">(needs you)</span>{/if}
								</a>
							</li>
						{/each}
					</ul>
				{/each}
			</nav>
			<div class="flex min-w-0 flex-1 flex-col gap-10 [&>section]:scroll-mt-4">
			{@render group('You', '')}

			<section id="claude" aria-label="Claude Code">
				{@render heading('accounts', 'Claude Code', 'applies to environments started afterwards')}
				<div class="divide-y rounded-lg border">
					{#each accounts as a (a.harness)}
						{@const [dot, label] = status(a)}
						{@const method = chosen[a.harness] ?? a.authMethod}
						<div class="space-y-3 p-4">
							<div class="flex flex-wrap items-center gap-x-3 gap-y-2">
								<!-- One harness: the heading names it already. -->
								{#if accounts.length > 1}<span class="font-medium">{a.displayName || a.harness}</span>{/if}
								<span class="inline-flex items-center gap-1.5 text-sm">
									<span class="size-2 shrink-0 rounded-full {dot}"></span>
									{label}
								</span>
								{#if a.authMethod !== AuthMethod.API_KEY && a.loginCheckedAt}
									<span class="text-xs text-muted-foreground" title={timestampDate(a.loginCheckedAt).toLocaleString()}>
										checked {since(a.loginCheckedAt)}
									</span>
								{/if}
								{#if a.authMethod !== AuthMethod.API_KEY}
									{@const out = a.loginState === LoginState.LOGGED_IN}
									<Button
										size="sm"
										variant={out ? 'outline' : 'default'}
										class="ml-auto"
										onclick={() =>
											(login = {
												open: true,
												action: out ? LoginAction.LOGOUT : LoginAction.LOGIN,
												harness: a.harness,
												name: a.displayName || a.harness
											})}
									>
										{out ? 'Log out' : 'Connect'}
									</Button>
								{/if}
							</div>
							<fieldset class="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
								<legend class="float-left mr-4 text-muted-foreground">Sign in with:</legend>
								{#each [[AuthMethod.SUBSCRIPTION, 'Subscription'], [AuthMethod.API_KEY, 'API key']] as const as [m, text] (m)}
									<label class="flex items-center gap-1.5">
										<input
											type="radio"
											name="method-{a.harness}"
											class="accent-primary"
											checked={method === m || (m === AuthMethod.SUBSCRIPTION && method === AuthMethod.UNSPECIFIED)}
											onchange={() => pick(a, m)}
										/>
										{text}
									</label>
								{/each}
							</fieldset>
							{#if method === AuthMethod.API_KEY}
								<form class="flex gap-2" onsubmit={(e) => (e.preventDefault(), saveKey(a))}>
									<Input
										type="password"
										autocomplete="off"
										aria-label="API key"
										placeholder={a.apiKeySet ? 'Replace the stored key (write-only)' : 'sk-ant-… (write-only)'}
										bind:value={keys[a.harness]}
									/>
									<Button type="submit" size="sm" variant="outline" disabled={!keys[a.harness]?.trim()}>Save key</Button>
								</form>
							{/if}
						</div>
					{:else}
						<p class="p-4 text-sm text-muted-foreground">{loaded ? 'No accounts.' : 'Loading…'}</p>
					{/each}
				</div>
			</section>

			<ForgeAccounts />

			<section id="git" aria-label="Git identity">
				{@render heading('git', 'Git identity', 'who your commits are by; applies to environments started afterwards')}
				<form class="space-y-3 rounded-lg border p-4" onsubmit={saveGit}>
					<div class="flex flex-wrap gap-2">
						<Input class="w-48 flex-auto" aria-label="Name" placeholder={gitIdentity?.signInName || 'Name'} autocomplete="off" bind:value={gitName} />
						<Input class="w-48 flex-auto" type="email" aria-label="Email" placeholder={gitIdentity?.signInEmail || 'Email'} autocomplete="off" bind:value={gitEmail} />
						<Button type="submit" size="sm" variant="outline">Save</Button>
					</div>
					<p class="text-xs text-muted-foreground">
						Commits are by <span class="font-mono">{commitsBy}</span>. Empty fields use your sign-in’s; a thread handed to you uses yours.
					</p>
				</form>
			</section>

			<section id="defaults" aria-label="New threads">
				{@render heading('defaults', 'New threads', 'what the new-thread composer starts with; change them per thread there')}
				<div class="divide-y rounded-lg border">
					<div class="flex flex-wrap gap-x-6 gap-y-3 p-4 text-sm">
						<label class="flex items-center gap-2">
							Model
							<NativeSelect.Root size="sm" value={shown.model} onchange={(e) => setDefaults({ model: e.currentTarget.value })}>
								{#each models as m (m)}<NativeSelect.Option value={m}>{m}</NativeSelect.Option>{/each}
							</NativeSelect.Root>
						</label>
						<label class="flex items-center gap-2">
							Effort
							<NativeSelect.Root size="sm" value={shown.effort} onchange={(e) => setDefaults({ effort: e.currentTarget.value })}>
								{#each efforts as m (m)}<NativeSelect.Option value={m}>{m}</NativeSelect.Option>{/each}
							</NativeSelect.Root>
						</label>
						<label class="flex items-center gap-2">
							Permissions
							<NativeSelect.Root size="sm" value={shown.permissions} onchange={(e) => setDefaults({ permissions: e.currentTarget.value })}>
								{#each Object.keys(permissionModes) as m (m)}<NativeSelect.Option value={m}>{m}</NativeSelect.Option>{/each}
							</NativeSelect.Root>
						</label>
					</div>
					<fieldset class="divide-y">
						<legend class="sr-only">Default profile</legend>
						<p class="px-4 py-3 text-sm text-muted-foreground">Agent profile: how the agent works with you</p>
						{#each profiles as p (p.name)}
							<label class="flex items-start gap-3 px-4 py-3 text-sm">
								<input type="radio" name="profile" class="mt-1 accent-primary" checked={p.name === defaultProfile} onchange={() => setProfile(p.name)} />
								<span><span class="font-medium">{p.label}</span> <span class="block text-muted-foreground">{p.note}</span></span>
							</label>
						{/each}
					</fieldset>
				</div>
			</section>

			<section id="notifications" aria-label="Notifications">
				{@render heading('', 'Notifications', 'in this browser, never for the page on screen')}
				<div class="divide-y rounded-lg border">
					<div class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3 text-sm">
						<span>{permission}</span>
						{#if threads.notify === 'default'}<Button size="sm" class="ml-auto" onclick={askNotify}>Enable</Button>{/if}
					</div>
					{#each kinds as [kind, label, note] (kind)}
						<label class="flex items-start gap-3 px-4 py-3 text-sm">
							<input
								type="checkbox"
								class="mt-1 accent-primary"
								checked={notifyKinds[kind]}
								onchange={(e) => setNotifyKind(kind, e.currentTarget.checked)}
							/>
							<span><span class="font-medium">{label}</span>{#if note}<span class="block text-muted-foreground">{note}</span>{/if}</span>
						</label>
					{/each}
				</div>
			</section>

			<section id="appearance" aria-label="Appearance">
				{@render heading('', 'Appearance', 'in this browser')}
				<fieldset class="flex flex-wrap gap-x-6 gap-y-2 rounded-lg border px-4 py-3 text-sm">
					<legend class="sr-only">Theme</legend>
					{#each [['system', 'System'], ['light', 'Light'], ['dark', 'Dark']] as const as [m, text] (m)}
						<label class="flex items-center gap-1.5">
							<input type="radio" name="theme" class="accent-primary" checked={userPrefersMode.current === m} onchange={() => setMode(m)} />
							{text}
						</label>
					{/each}
				</fieldset>
			</section>

			<section id="secrets" aria-label="Secrets & variables">
				{@render heading('secrets', 'Secrets & variables', 'passed to all your threads as environment variables')}
				<div class="divide-y rounded-lg border">
					{#each secrets as s (s.name)}
						<div class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2">
							<span class="min-w-0 truncate font-mono text-sm">{s.name}</span>
							<span class="text-xs text-muted-foreground" title={s.updatedAt && timestampDate(s.updatedAt).toLocaleString()}>
								set · {since(s.updatedAt)}
							</span>
							{#if replacing === s.name}
								<form class="flex w-full gap-2" onsubmit={replace}>
									<!-- svelte-ignore a11y_autofocus -->
									<Input type="password" autocomplete="off" aria-label="New value of {s.name}" placeholder="New value" autofocus bind:value={newValue} />
									<Button type="submit" size="sm">Save</Button>
									<Button size="sm" variant="ghost" onclick={() => (replacing = newValue = '')}>Cancel</Button>
								</form>
							{:else}
								<div class="ml-auto flex gap-1">
									<Button size="sm" variant="ghost" onclick={() => ((replacing = s.name), (newValue = ''))}>Replace</Button>
									<Button size="sm" variant="ghost" class="text-destructive" onclick={() => remove(s.name)}>Delete</Button>
								</div>
							{/if}
						</div>
					{/each}
					<form class="flex flex-wrap gap-2 p-4" onsubmit={add}>
						<Input
							class="w-48 flex-auto font-mono"
							aria-label="Name"
							placeholder="NAME"
							autocomplete="off"
							aria-invalid={!!name && !validName(name)}
							bind:value={name}
						/>
						<Input class="w-48 flex-[2_1_12rem]" type="password" autocomplete="off" aria-label="Value" placeholder="value" bind:value />
						<Button type="submit" size="sm" disabled={!name || !value}>Add</Button>
					</form>
				</div>
			</section>

			<section id="shortcuts" aria-label="Keyboard shortcuts">
				{@render heading('', 'Keyboard shortcuts', 'never while typing; ? shows them anywhere')}
				<dl class="grid grid-cols-[auto_1fr] items-center gap-x-4 gap-y-2 rounded-lg border p-4 text-sm">
					{#each shortcuts as [keys, what] (keys)}
						<dt><kbd class="rounded border bg-muted px-1.5 py-0.5 font-mono text-xs">{keys}</kbd></dt>
						<dd class="text-muted-foreground">{what}</dd>
					{/each}
				</dl>
			</section>

			{@render group('Team', groups[1][1])}

			<GitHubApp />

			<GitLabApp />

			<TeamSecrets />

			<WorkloadIdentity />

			</div>
		</div>
	</div>
</div>

<LoginDialog bind:open={login.open} action={login.action} harness={login.harness} name={login.name} onclose={load} />
