<script lang="ts">
	import ForgeAction from '#lib/components/forge-action.svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Popover } from 'bits-ui';
	import BrainIcon from '@lucide/svelte/icons/brain';
	import ContactRoundIcon from '@lucide/svelte/icons/contact-round';
	import CpuIcon from '@lucide/svelte/icons/cpu';
	import PlugIcon from '@lucide/svelte/icons/plug';
	import SlidersHorizontalIcon from '@lucide/svelte/icons/sliders-horizontal';
	import SplitIcon from '@lucide/svelte/icons/split';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import type { PromptInputMessage } from '#lib/components/ai-elements/prompt-input/index.js';
	import AttachButton, { attachLimits } from '#lib/components/attach-button.svelte';
	import LoginDialog from '#lib/components/login-dialog.svelte';
	import OptionMenu, { efforts, models, raceTries, tryOptions } from '#lib/components/option-menu.svelte';
	import PageHeader from '#lib/components/page-header.svelte';
	import RepoPicker from '#lib/components/repo-picker.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import { api, settingsApi, upload } from '#lib/api.js';
	import ShieldIcon from '@lucide/svelte/icons/shield';
	import { AuthMethod, LoginState, PermissionMode } from '#lib/gen/banda/v1/banda_pb.js';
	import { permissionLabel, permissionModes } from '#lib/permissions.js';
	import { profileLabel, profileLabels, profileName } from '#lib/profiles.js';
	import { noRepo, projects, threads } from '#lib/threads.svelte.js';

	// A new thread is an empty chat: the first message creates it. The last choices are remembered
	// (the app is a client-only SPA, so localStorage is always there).
	const saved = (key: string, fallback: string) => localStorage.getItem(`banda.${key}`) ?? fallback;
	let repoUrl = $state(threads.project || saved('repoUrl', ''));
	let baseBranch = $state(saved('baseBranch', 'main'));
	// Not remembered: they start at the defaults from Settings (Banda's until loaded; '' profile: the API uses the default).
	let model = $state(models[0]);
	let effort = $state('high');
	let permissions = $state(permissionLabel(PermissionMode.BYPASS));
	let profile = $state('');
	// Not remembered: every try runs (and bills) its own agent.
	let tries = $state(tryOptions[0]);
	// The Options chip names only what differs from these.
	const defaults = $state({ model: models[0], effort: 'high', permissions: permissionLabel(PermissionMode.BYPASS), profile: '' });
	const changed = $derived(
		[
			model !== defaults.model && model,
			effort !== defaults.effort && effort,
			profile !== defaults.profile && profileLabel(profile),
			permissions !== defaults.permissions && permissions,
			tries !== tryOptions[0] && tries
		]
			.filter(Boolean)
			.join(', ')
	);
	// What was being typed survives a reload, like the last choices.
	let prompt = $state(saved('prompt', ''));
	$effect(() => localStorage.setItem('banda.prompt', prompt));
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
			.then(({ accounts, defaultProfile, threadDefaults: d }) => {
				if (!profile) {
					// The first load: the defaults, not what was picked since (closing the login dialog reloads).
					profile = defaultProfile;
					model = d?.model || model;
					effort = d?.effort || effort;
					if (d?.permissionMode) permissions = permissionLabel(d.permissionMode);
					Object.assign(defaults, { model, effort, permissions, profile });
				}
				const a = accounts.find((a) => a.harness === 'claude');
				if (!a) connect = '';
				else if (a.authMethod === AuthMethod.API_KEY) connect = a.apiKeySet ? '' : 'key';
				else connect = a.loginState === LoginState.LOGGED_IN ? '' : 'login';
			})
			.catch((e) => (error = `Couldn't load your defaults, so these are Banda's: ${e.message}`));
	$effect(() => {
		checkAccount();
	});
	let busy = $state(false);
	let error = $state('');

	async function submit({ text, attachments: files }: PromptInputMessage) {
		if (!text.trim()) return;
		if (!repoUrl) {
			error = 'Choose a repository first.';
			throw new Error(error); // keeps the typed prompt in the box
		}
		busy = true;
		error = '';
		for (const [k, v] of Object.entries({ repoUrl, baseBranch })) localStorage.setItem(`banda.${k}`, v);
		const repo = repoUrl === noRepo ? '' : repoUrl;
		try {
			const attachments = await Promise.all(files.map((f) => upload(f.file)));
			const race = raceTries(tries, model, effort);
			const permissionMode = permissionModes[permissions] ?? PermissionMode.BYPASS;
			if (race.length > 1) {
				const { threads } = await api.race({ repoUrl: repo, baseBranch, prompt: text, tries: race, permissionMode, profile, attachments });
				prompt = '';
				await goto(`/compare?race=${threads[0].raceId}`);
			} else {
				const { thread } = await api.createThread({ repoUrl: repo, baseBranch, model, effort, prompt: text, permissionMode, profile, attachments });
				prompt = '';
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

	<!-- The question sits on the box that answers it: centered on a desktop, at the thumb on a phone. -->
	<div class="mx-auto flex min-h-0 w-full max-w-3xl flex-1 flex-col justify-end overflow-y-auto px-4 pb-4 md:justify-center md:pb-[12dvh]">
		<div class="mb-6 space-y-1 text-center">
			<h2 class="font-heading text-2xl font-semibold tracking-tight highlighter:font-bold logbook:text-3xl logbook:font-medium common-room:text-3xl common-room:font-bold">What should the agent work on?</h2>
			<p class="text-sm text-pretty text-muted-foreground">
				It works in its own sandbox, {repoUrl === noRepo ? 'in an empty folder' : 'on its own branch'}. Teammates can follow along and join in.
			</p>
		</div>
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
		{#if error}
			<div class="mb-2 flex flex-wrap items-center gap-2">
				<p class="text-sm text-destructive">{error}</p>
				<ForgeAction message={error} />
			</div>
		{/if}
		<PromptInput.Root onSubmit={submit} {...attachLimits} onError={(e) => (error = e.message)}>
			<fieldset disabled={busy} class="contents">
				<PromptInput.Attachments class="px-2">
					{#snippet children(a)}<PromptInput.Attachment data={a} />{/snippet}
				</PromptInput.Attachments>
				<PromptInput.Body>
					<PromptInput.Textarea bind:value={prompt} placeholder="Describe the task. Enter sends, Shift+Enter adds a line." />
				</PromptInput.Body>
				<PromptInput.Toolbar class="items-end">
					<PromptInput.Tools class="min-w-0 py-0.5 max-sm:overflow-x-auto max-sm:[scrollbar-width:none] sm:flex-wrap">
						<AttachButton />
						<RepoPicker bind:repoUrl bind:baseBranch />
						<Popover.Root>
							<Popover.Trigger>
								{#snippet child({ props })}
									<PromptInput.Button {...props} size="sm" class="min-w-0" aria-label={changed ? `Options: ${changed}` : 'Options'}>
										<SlidersHorizontalIcon />
										<span class="max-w-56 truncate">Options{changed ? ` · ${changed}` : ''}</span>
									</PromptInput.Button>
								{/snippet}
							</Popover.Trigger>
							<Popover.Portal>
								<Popover.Content align="start" sideOffset={4} class="z-50 flex flex-col items-start gap-1 rounded-md border bg-popover p-1.5 text-popover-foreground shadow-md">
									<OptionMenu label="Model" icon={CpuIcon} bind:value={model} options={models} />
									<OptionMenu label="Effort" icon={BrainIcon} bind:value={effort} options={efforts} />
									<OptionMenu label="Profile" icon={ContactRoundIcon} bind:value={() => profileLabel(profile), (v) => (profile = profileName(v))} options={profileLabels} />
									<OptionMenu label="Permissions" icon={ShieldIcon} bind:value={permissions} options={Object.keys(permissionModes)} />
									<OptionMenu label="Try N ways" icon={SplitIcon} bind:value={tries} options={tryOptions} />
								</Popover.Content>
							</Popover.Portal>
						</Popover.Root>
					</PromptInput.Tools>
					<PromptInput.Submit status={busy ? 'submitted' : undefined} />
				</PromptInput.Toolbar>
			</fieldset>
		</PromptInput.Root>
	</div>
</div>

<LoginDialog bind:open={loginOpen} onclose={checkAccount} />
