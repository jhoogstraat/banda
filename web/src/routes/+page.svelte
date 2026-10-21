<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import BotIcon from '@lucide/svelte/icons/bot';
	import BrainIcon from '@lucide/svelte/icons/brain';
	import CpuIcon from '@lucide/svelte/icons/cpu';
	import PlugIcon from '@lucide/svelte/icons/plug';
	import SplitIcon from '@lucide/svelte/icons/split';
	import { ConversationEmptyState } from '#lib/components/ai-elements/conversation/index.js';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import LoginDialog from '#lib/components/login-dialog.svelte';
	import OptionMenu, { efforts, models, raceTries, tryOptions } from '#lib/components/option-menu.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import RepoPicker from '#lib/components/repo-picker.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { api, settingsApi } from '#lib/api.js';
	import ShieldIcon from '@lucide/svelte/icons/shield';
	import { AuthMethod, LoginState, PermissionMode } from '#lib/gen/banda/v1/banda_pb.js';
	import { permissionLabel, permissionModes } from '#lib/permissions.js';
	import { projects, threads } from '#lib/threads.svelte.js';

	// A new thread is an empty chat: the first message creates it. The last choices are remembered
	// (the app is a client-only SPA, so localStorage is always there).
	const saved = (key: string, fallback: string) => localStorage.getItem(`banda.${key}`) ?? fallback;
	let repoUrl = $state(threads.project || saved('repoUrl', ''));
	let baseBranch = $state(saved('baseBranch', 'main'));
	let model = $state(saved('model', models[0]));
	let effort = $state(saved('effort', 'high'));
	let permissions = $state(saved('permissions', permissionLabel(PermissionMode.BYPASS)));
	// Not remembered: every try runs (and bills) its own agent.
	let tries = $state(tryOptions[0]);
	// Without a remembered choice, default to the most recently active repository once the sidebar has loaded.
	$effect(() => {
		if (!repoUrl) repoUrl = projects()[0] ?? '';
	});
	// The new-thread dialog links here with ?repo=<url>.
	$effect(() => {
		const repo = page.url.searchParams.get('repo');
		if (repo) repoUrl = repo;
	});
	// A hint, not a gate: threads can still be started without a usable Claude account.
	// Why the account isn't usable: no subscription login, or the API key method without a key.
	let connect = $state<'' | 'login' | 'key'>('');
	let loginOpen = $state(false);
	const checkAccount = () =>
		settingsApi
			.getSettings({})
			.then(({ accounts }) => {
				const a = accounts.find((a) => a.harness === 'claude');
				if (!a) connect = '';
				else if (a.authMethod === AuthMethod.API_KEY) connect = a.apiKeySet ? '' : 'key';
				else connect = a.loginState === LoginState.LOGGED_IN ? '' : 'login';
			})
			.catch(() => {});
	$effect(() => {
		checkAccount();
	});
	let busy = $state(false);
	let error = $state('');

	async function submit({ text }: { text: string }) {
		if (!text.trim()) return;
		if (!repoUrl) {
			error = 'Choose a repository first.';
			throw new Error(error); // keeps the typed prompt in the box
		}
		busy = true;
		error = '';
		for (const [k, v] of Object.entries({ repoUrl, baseBranch, model, effort, permissions })) localStorage.setItem(`banda.${k}`, v);
		try {
			const race = raceTries(tries, model, effort);
			// Set before the environments are up: a runner gets the mode once it is ready, before the prompt.
			const mode = permissionModes[permissions] ?? PermissionMode.BYPASS;
			const ask = (ts: { id: string }[]) => Promise.all(mode === PermissionMode.BYPASS ? [] : ts.map((t) => api.permissionSetMode({ threadId: t.id, mode })));
			if (race.length > 1) {
				const { threads } = await api.race({ repoUrl, baseBranch, prompt: text, tries: race });
				await ask(threads);
				await goto(`/compare?race=${threads[0].raceId}`);
			} else {
				const { thread } = await api.createThread({ repoUrl, baseBranch, model, effort, prompt: text });
				await ask([thread!]);
				await goto(`/t/${thread!.id}`);
			}
		} catch (err) {
			error = (err as Error).message;
			busy = false;
			throw err;
		}
	}
</script>

<div class="flex min-w-0 flex-1 flex-col">
	<PageHeader><h1 class="text-sm font-medium">New thread</h1></PageHeader>

	<div class="flex min-h-0 flex-1 items-center justify-center overflow-y-auto p-6">
		<ConversationEmptyState
			title="What should the agent work on?"
			description="It works in its own sandbox, on its own branch. Teammates can follow along and join in."
		>
			{#snippet icon()}<BotIcon class="size-8" />{/snippet}
		</ConversationEmptyState>
	</div>

	<div class="mx-auto w-full max-w-3xl shrink-0 px-4 pb-4">
		{#if connect}
			<div class="mb-2 flex flex-wrap items-center gap-2 rounded-lg border bg-muted/40 py-1.5 pr-1.5 pl-3 text-sm">
				<PlugIcon class="size-4 shrink-0 text-muted-foreground" />
				<span class="flex-1">Connect Claude Code to start a thread.</span>
				{#if connect === 'key'}
					<Button size="sm" href="/settings">Add an API key</Button>
				{:else}
					<Button size="sm" variant="ghost" href="/settings">Settings</Button>
					<Button size="sm" onclick={() => (loginOpen = true)}>Connect</Button>
				{/if}
			</div>
		{/if}
		{#if error}<p class="mb-2 text-sm text-destructive">{error}</p>{/if}
		<PromptInput.Root onSubmit={submit}>
			<fieldset disabled={busy} class="contents">
				<PromptInput.Body>
					<PromptInput.Textarea placeholder="Describe the task. Enter sends, Shift+Enter adds a line." />
				</PromptInput.Body>
				<PromptInput.Toolbar>
					<PromptInput.Tools class="min-w-0 flex-wrap">
						<RepoPicker bind:repoUrl bind:baseBranch />
						<OptionMenu label="Model" icon={CpuIcon} bind:value={model} options={models} />
						<OptionMenu label="Effort" icon={BrainIcon} bind:value={effort} options={efforts} />
						<OptionMenu label="Permissions" icon={ShieldIcon} bind:value={permissions} options={Object.keys(permissionModes)} />
						<OptionMenu label="Try N ways" icon={SplitIcon} bind:value={tries} options={tryOptions} />
					</PromptInput.Tools>
					<PromptInput.Submit status={busy ? 'submitted' : undefined} />
				</PromptInput.Toolbar>
			</fieldset>
		</PromptInput.Root>
	</div>
</div>

<LoginDialog bind:open={loginOpen} onclose={checkAccount} />
