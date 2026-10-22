<script lang="ts">
	import BrainIcon from '@lucide/svelte/icons/brain';
	import ContactRoundIcon from '@lucide/svelte/icons/contact-round';
	import CpuIcon from '@lucide/svelte/icons/cpu';
	import ShieldCheckIcon from '@lucide/svelte/icons/shield-check';
	import ShieldIcon from '@lucide/svelte/icons/shield';
	import XIcon from '@lucide/svelte/icons/x';
	import { Loader } from '#lib/components/ai-elements/loader/index.js';
	import * as PromptInput from '#lib/components/ai-elements/prompt-input/index.js';
	import type { PromptInputMessage } from '#lib/components/ai-elements/prompt-input/index.js';
	import AttachButton from '#lib/components/attach-button.svelte';
	import MentionInput from '#lib/components/mention-input.svelte';
	import OptionMenu, { efforts, models } from '#lib/components/option-menu.svelte';
	import Plan from '#lib/components/plan.svelte';
	import ThreadBanners from '#lib/components/thread-banners.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';
	import { api } from '#lib/api.js';
	import type { Chat } from '#lib/events.js';
	import { Status, type Thread } from '#lib/gen/banda/v1/banda_pb.js';
	import { permissionLabel, permissionModes, ruleLabel } from '#lib/permissions.js';
	import { profileLabel, profileLabels, profileName } from '#lib/profiles.js';

	// The bottom of a thread: who is producing something, the error, banners, the plan and the prompt box.
	// typing: the names of the others typing. send throws to keep the prompt; typed reports typing.
	let {
		thread,
		owner,
		chat,
		typing,
		error = $bindable(),
		act,
		send,
		typed,
		setAgent
	}: {
		thread?: Thread;
		owner: boolean;
		chat: Chat;
		typing: string[];
		error: string;
		act: (call: Promise<unknown>) => Promise<boolean>;
		send: (m: PromptInputMessage) => Promise<void>;
		typed: () => void;
		setAgent: (change: { model?: string; effort?: string; profile?: string }) => void;
	} = $props();

	const typingText = $derived(
		typing.length > 2 ? `${typing.length} people are typing…` : typing.length ? `${typing.join(' and ')} ${typing.length > 1 ? 'are' : 'is'} typing…` : ''
	);
</script>

<div class="mx-auto w-full max-w-3xl shrink-0 px-4 pb-4">
	<!-- Who is producing something right now: the agent while it works, then people typing. Always takes its line, so the composer doesn't jump. -->
	<div class="flex h-6 items-center gap-2 text-xs text-muted-foreground" aria-live="polite">
		{#if thread?.permissionPending}
			<ShieldIcon class="size-3 text-amber-500" /><span>Agent is waiting for an answer</span>
		{:else if thread?.status === Status.WORKING}
			<Loader size={12} /><span class="animate-pulse">Agent is {chat.streaming ? 'writing' : 'working'}…</span>
		{/if}
		{#if typingText}
			{#if thread?.status === Status.WORKING}<span>·</span>{/if}
			<span class="flex gap-0.5"><span class="size-1 animate-bounce rounded-full bg-current"></span><span class="size-1 animate-bounce rounded-full bg-current [animation-delay:150ms]"></span><span class="size-1 animate-bounce rounded-full bg-current [animation-delay:300ms]"></span></span>
			<span class="truncate">{typingText}</span>
		{/if}
	</div>
	{#if error}<p role="alert" class="mb-2 text-sm text-destructive">{error}</p>{/if}
	{#if thread}<ThreadBanners {thread} {act} />{/if}
	<Plan plan={chat.plan} />
	<PromptInput.Root onSubmit={send} multiple maxFiles={5} maxFileSize={5 << 20} onError={(e) => (error = e.message)}>
		<fieldset disabled={thread?.status === Status.STARTING} class="contents" oninput={typed}>
			<PromptInput.Attachments class="px-2">
				{#snippet children(a)}<PromptInput.Attachment data={a} />{/snippet}
			</PromptInput.Attachments>
			<PromptInput.Body>
				<MentionInput placeholder="Message the agent. Use @agent to mention it in chat mode." />
			</PromptInput.Body>
			<PromptInput.Toolbar>
				<div class="flex items-center gap-1.5 pl-1">
					<AttachButton />
					{#if thread && owner}
						<!-- A running agent switches model, effort and profile once its turns have ended, permissions at once. -->
						<OptionMenu label="Model" icon={CpuIcon} bind:value={() => thread!.model, (model) => setAgent({ model })} options={models} />
						<OptionMenu label="Effort" icon={BrainIcon} bind:value={() => thread!.effort, (effort) => setAgent({ effort })} options={efforts} />
						<OptionMenu
							label="Profile"
							icon={ContactRoundIcon}
							options={profileLabels}
							bind:value={() => profileLabel(thread!.profile), (v) => setAgent({ profile: profileName(v) })}
						/>
						<OptionMenu
							label="Permissions"
							icon={ShieldIcon}
							options={Object.keys(permissionModes)}
							bind:value={() => permissionLabel(thread!.permissionMode), (v) => act(api.permissionSetMode({ threadId: thread!.id, mode: permissionModes[v] }))}
						/>
					{:else if thread}
						<Badge variant="outline" class="font-normal text-muted-foreground"><CpuIcon /> {thread.model}</Badge>
						<Badge variant="outline" class="font-normal text-muted-foreground"><BrainIcon /> {thread.effort}</Badge>
						<Badge variant="outline" class="font-normal text-muted-foreground"><ContactRoundIcon /> {profileLabel(thread.profile)}</Badge>
						<Badge variant="outline" class="font-normal text-muted-foreground"><ShieldIcon /> {permissionLabel(thread.permissionMode)}</Badge>
					{/if}
					{#if thread?.allowedTools.length}
						<!-- What the agent does without asking in this thread; whoever answers its requests can take it back. -->
						<DropdownMenu.Root>
							<DropdownMenu.Trigger>
								{#snippet child({ props })}
									<PromptInput.Button {...props} size="sm" aria-label="Allowed: {thread!.allowedTools.length}"><ShieldCheckIcon /> Allowed · {thread!.allowedTools.length}</PromptInput.Button>
								{/snippet}
							</DropdownMenu.Trigger>
							<DropdownMenu.Content align="start" class="w-64">
								<DropdownMenu.Label>Runs without asking</DropdownMenu.Label>
								{#each thread.allowedTools as rule (rule)}
									<DropdownMenu.Item
										disabled={!owner && !thread.trusted}
										onSelect={() => act(api.permissionRevoke({ threadId: thread!.id, rule }))}
										aria-label="Stop allowing {ruleLabel(rule)}"
										class="font-mono text-xs"
									>
										<span class="min-w-0 flex-1 truncate">{ruleLabel(rule)}</span>
										{#if owner || thread.trusted}<XIcon class="text-muted-foreground" />{/if}
									</DropdownMenu.Item>
								{/each}
							</DropdownMenu.Content>
						</DropdownMenu.Root>
					{/if}
				</div>
				<PromptInput.Submit />
			</PromptInput.Toolbar>
		</fieldset>
	</PromptInput.Root>
</div>
