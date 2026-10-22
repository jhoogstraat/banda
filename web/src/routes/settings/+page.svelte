<script lang="ts">
	import { page } from '$app/state';
	import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt';
	import LoginDialog from '#lib/components/login-dialog.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import TeamSecrets from '#lib/components/team-secrets.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { ConnectError } from '@connectrpc/connect';
	import { settingsApi } from '#lib/api.js';
	import {
		AuthMethod,
		LoginAction,
		LoginState,
		type HarnessAccount,
		type SecretInfo
	} from '#lib/gen/banda/v1/banda_pb.js';
	import { profiles } from '#lib/profiles.js';
	import { ago } from '#lib/utils.js';

	let accounts = $state<HarnessAccount[]>([]);
	let secrets = $state<SecretInfo[]>([]);
	let defaultProfile = $state('');
	let loaded = $state(false);
	// An error and the section it belongs to.
	let error = $state('');
	let errorAt = $state<'accounts' | 'profile' | 'secrets'>('accounts');

	const message = (e: unknown) => ConnectError.from(e).rawMessage;

	async function load() {
		try {
			({ accounts, secrets, defaultProfile } = await settingsApi.getSettings({}));
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

	const since = (t?: Timestamp) => {
		const a = ago(t);
		return a === 'now' ? 'just now' : `${a} ago`;
	};

	// [dot colour, label] of an account.
	function status(a: HarnessAccount): [string, string] {
		if (a.authMethod === AuthMethod.API_KEY) return a.apiKeySet ? ['bg-emerald-500', 'Connected (API key)'] : ['bg-amber-400', 'No API key'];
		if (a.loginState === LoginState.LOGGED_IN) return ['bg-emerald-500', 'Connected (subscription)'];
		if (a.loginState === LoginState.LOGGED_OUT) return ['bg-amber-400', 'Not logged in'];
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
		if (!(await act('profile', settingsApi.setDefaultProfile({ profile: name })))) defaultProfile = before;
	}

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

	function remove(n: string) {
		if (confirm(`Delete the secret ${n}? Environments started afterwards won't have it.`)) act('secrets', settingsApi.deleteSecret({ name: n }));
	}
</script>

{#snippet heading(at: typeof errorAt, text: string, note: string)}
	<h2 class="mb-3 flex flex-wrap items-baseline gap-x-2 text-sm font-medium">
		{text}
		{#if note}<span class="text-xs font-normal text-muted-foreground">{note}</span>{/if}
	</h2>
	{#if error && errorAt === at}<p class="mb-3 text-sm text-destructive">{error}</p>{/if}
{/snippet}

<div class="flex min-w-0 flex-1 flex-col">
	<PageHeader><h1 class="text-sm font-medium">Settings</h1></PageHeader>

	<div class="min-h-0 flex-1 overflow-y-auto">
		<div class="mx-auto w-full max-w-3xl space-y-8 p-4">
			<section>
				{@render heading('accounts', 'Accounts', 'applies to environments started afterwards')}
				<div class="divide-y rounded-lg border">
					{#each accounts as a (a.harness)}
						{@const [dot, label] = status(a)}
						{@const method = chosen[a.harness] ?? a.authMethod}
						<div class="space-y-3 p-4">
							<div class="flex flex-wrap items-center gap-x-3 gap-y-2">
								<span class="font-medium">{a.displayName || a.harness}</span>
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

			<section>
				{@render heading('profile', 'Agent profile', 'how the agent works with you in new threads; switch it per thread in the composer')}
				<fieldset class="divide-y rounded-lg border">
					<legend class="sr-only">Default profile</legend>
					{#each profiles as p (p.name)}
						<label class="flex items-start gap-3 px-4 py-3 text-sm">
							<input type="radio" name="profile" class="mt-1 accent-primary" checked={p.name === defaultProfile} onchange={() => setProfile(p.name)} />
							<span><span class="font-medium">{p.label}</span> <span class="block text-muted-foreground">{p.note}</span></span>
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

			<TeamSecrets />
		</div>
	</div>
</div>

<LoginDialog bind:open={login.open} action={login.action} harness={login.harness} name={login.name} onclose={load} />
